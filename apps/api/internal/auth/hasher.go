package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// HasherParams are the Argon2id cost parameters.
type HasherParams struct {
	MemoryKiB, Time uint32
	Threads         uint8
}

// DefaultHasherParams is the OWASP minimum for Argon2id (plan P-06).
var DefaultHasherParams = HasherParams{MemoryKiB: 19456, Time: 2, Threads: 1}

// ErrBusy means the Argon2 concurrency cap was saturated for longer than the wait bound. It is
// independent of the account (503 auth.busy).
var ErrBusy = errors.New("auth: password hashing is busy")

// Bounds applied when decoding a stored hash, so a tampered hash cannot cause a memory blow-up.
const (
	maxMemoryKiB                 = 256 * 1024
	maxTime                      = 10
	maxThreads                   = 4
	saltBytes                    = 16
	keyBytes                     = 32
	minDecodeSalt, maxDecodeSalt = 8, 64
	minDecodeKey, maxDecodeKey   = 16, 64
)

var phcBase64 = base64.RawStdEncoding.Strict()

// EncodePHC renders $argon2id$v=19$m=..,t=..,p=..$<salt>$<key> with raw standard base64.
func EncodePHC(p HasherParams, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.MemoryKiB, p.Time, p.Threads,
		phcBase64.EncodeToString(salt), phcBase64.EncodeToString(key))
}

// DecodePHC parses and bounds-checks an Argon2id PHC string.
func DecodePHC(phc string) (HasherParams, []byte, []byte, error) {
	errBad := errors.New("auth: malformed password hash")
	fields := strings.Split(phc, "$")
	if len(fields) != 6 || fields[0] != "" || fields[1] != "argon2id" || fields[2] != "v="+strconv.Itoa(argon2.Version) {
		return HasherParams{}, nil, nil, errBad
	}
	parts := strings.Split(fields[3], ",")
	if len(parts) != 3 {
		return HasherParams{}, nil, nil, errBad
	}
	var vals [3]uint64
	for i, prefix := range []string{"m=", "t=", "p="} {
		num, ok := strings.CutPrefix(parts[i], prefix)
		if !ok || num == "" || strings.Trim(num, "0123456789") != "" {
			return HasherParams{}, nil, nil, errBad
		}
		v, err := strconv.ParseUint(num, 10, 32)
		if err != nil {
			return HasherParams{}, nil, nil, errBad
		}
		vals[i] = v
	}
	m, t, th := vals[0], vals[1], vals[2]
	if th < 1 || th > maxThreads || t < 1 || t > maxTime || m < 8*th || m > maxMemoryKiB {
		return HasherParams{}, nil, nil, errBad
	}
	salt, err := phcBase64.DecodeString(fields[4])
	if err != nil || len(salt) < minDecodeSalt || len(salt) > maxDecodeSalt {
		return HasherParams{}, nil, nil, errBad
	}
	key, err := phcBase64.DecodeString(fields[5])
	if err != nil || len(key) < minDecodeKey || len(key) > maxDecodeKey {
		return HasherParams{}, nil, nil, errBad
	}
	return HasherParams{MemoryKiB: uint32(m), Time: uint32(t), Threads: uint8(th)}, salt, key, nil
}

// HasherOptions configures NewHasher; zero values pick the production defaults.
type HasherOptions struct {
	Params         HasherParams                         // zero => DefaultHasherParams
	MaxConcurrency int                                  // semaphore size; 0 => 4
	WaitBound      time.Duration                        // 0 => 2s
	After          func(time.Duration) <-chan time.Time // nil => time.After; injected by tests
	OnArgon2       func(HasherParams)                   // test hook: once per run, after the slot is taken, before deriving
}

// Hasher hashes and verifies passwords with Argon2id under a concurrency cap. Callers pass
// NormalizePassword output; the hasher never applies NFC, trims or truncates.
type Hasher struct {
	params    HasherParams
	sem       chan struct{}
	waitBound time.Duration
	after     func(time.Duration) <-chan time.Time
	onArgon2  func(HasherParams)

	dummyOnce sync.Once
	dummySalt []byte
}

// NewHasher builds a hasher.
func NewHasher(o HasherOptions) *Hasher {
	h := &Hasher{
		params:    o.Params,
		waitBound: o.WaitBound,
		after:     o.After,
		onArgon2:  o.OnArgon2,
	}
	if h.params == (HasherParams{}) {
		h.params = DefaultHasherParams
	}
	if o.MaxConcurrency <= 0 {
		o.MaxConcurrency = 4
	}
	if h.waitBound <= 0 {
		h.waitBound = 2 * time.Second
	}
	if h.after == nil {
		h.after = time.After
	}
	h.sem = make(chan struct{}, o.MaxConcurrency)
	return h
}

// acquire takes a slot, waiting at most waitBound. A cancelled context returns its own error.
func (h *Hasher) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("auth: hasher: %w", err)
	}
	select {
	case h.sem <- struct{}{}:
		return nil
	default:
	}
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("auth: hasher: %w", ctx.Err())
	case <-h.after(h.waitBound):
		return ErrBusy
	}
}

func (h *Hasher) release() { <-h.sem }

// Hash returns the PHC string for password under the current parameters and a fresh random salt.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	if h.onArgon2 != nil {
		h.onArgon2(h.params)
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	return EncodePHC(h.params, salt, derive(h.params, password, salt)), nil
}

// Verify checks password against phc. An empty, malformed or out-of-bounds phc is verified against
// a dummy hash instead, so every call costs exactly one Argon2 run and returns false with a nil
// error. The error is non-nil only for ErrBusy or a cancelled context.
func (h *Hasher) Verify(ctx context.Context, phc, password string) (bool, error) {
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()

	params, salt, want, err := DecodePHC(phc)
	if phc == "" || err != nil {
		if h.onArgon2 != nil {
			h.onArgon2(h.params)
		}
		h.dummyOnce.Do(h.makeDummy)
		_ = derive(h.params, password, h.dummySalt)
		return false, nil
	}
	if h.onArgon2 != nil {
		h.onArgon2(params)
	}
	// The stored key length is part of the hash; derive with the same length (16 to 64 bytes,
	// bounded by DecodePHC).
	got := argon2.IDKey([]byte(password), salt, params.Time, params.MemoryKiB, params.Threads, uint32(len(want))) //nolint:gosec // bounded by DecodePHC
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether phc was made with parameters other than the current ones (or is not
// decodable at all).
func (h *Hasher) NeedsRehash(phc string) bool {
	p, _, _, err := DecodePHC(phc)
	return err != nil || p != h.params
}

// makeDummy creates the dummy salt once. Every dummy verify derives against it with the current
// parameters, so a no-account attempt costs the same as a real one.
func (h *Hasher) makeDummy() {
	h.dummySalt = make([]byte, saltBytes)
	if _, err := rand.Read(h.dummySalt); err != nil {
		panic("auth: no randomness for the dummy hash: " + err.Error())
	}
}

func derive(p HasherParams, password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, keyBytes)
}
