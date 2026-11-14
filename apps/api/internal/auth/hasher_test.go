// Tests for the Argon2id hasher (T020; FR-010, FR-012, SC-003, plan P-06).
//
// API assumptions (contracts are silent; the implementer must match these names):
//
//	type HasherParams struct{ MemoryKiB, Time uint32; Threads uint8 }
//	var DefaultHasherParams = HasherParams{MemoryKiB: 19456, Time: 2, Threads: 1}
//	func EncodePHC(p HasherParams, salt, key []byte) string       // $argon2id$v=19$m=..,t=..,p=..$<b64 salt>$<b64 key>, raw std base64
//	func DecodePHC(phc string) (HasherParams, []byte, []byte, error)
//	var ErrBusy error
//	type HasherOptions struct {
//	    Params         HasherParams                           // zero value => DefaultHasherParams
//	    MaxConcurrency int                                    // semaphore size; 0 => 4
//	    WaitBound      time.Duration                          // 0 => 2*time.Second
//	    After          func(time.Duration) <-chan time.Time   // nil => time.After; injected so tests never sleep
//	    OnArgon2       func(HasherParams)                     // called once per Argon2 run, AFTER the slot is acquired and
//	                                                          // BEFORE the key derivation, with the parameters used
//	}
//	func NewHasher(HasherOptions) *Hasher
//	func (*Hasher) Hash(ctx context.Context, password string) (phc string, err error)
//	func (*Hasher) Verify(ctx context.Context, phc, password string) (ok bool, err error)
//	    phc == "" means "no such account": the password is checked against a dummy hash (current
//	    parameters, computed once on first use) and the result is always false with a nil error.
//	    A malformed or out-of-bounds stored hash behaves like the dummy path: false, and still exactly one
//	    Argon2 run (with the current parameters), so it costs the same as any other failure.
//	func (*Hasher) NeedsRehash(phc string) bool   // true when the encoded parameters differ from the current ones
//
// The hasher does NOT apply NFC; callers pass NormalizePassword output. Timing equality itself is
// the nightly SC-003 test, not asserted here.
package auth_test

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

var cheap = auth.HasherParams{MemoryKiB: 8, Time: 1, Threads: 1}

type runCounter struct {
	mu   sync.Mutex
	runs []auth.HasherParams
}

func (c *runCounter) hook(p auth.HasherParams) {
	c.mu.Lock()
	c.runs = append(c.runs, p)
	c.mu.Unlock()
}
func (c *runCounter) n() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.runs) }
func (c *runCounter) last() auth.HasherParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs[len(c.runs)-1]
}

func cheapHasher() *auth.Hasher { return auth.NewHasher(auth.HasherOptions{Params: cheap}) }

func mustHash(t testing.TB, h *auth.Hasher, pw string) string {
	t.Helper()
	phc, err := h.Hash(context.Background(), pw)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	return phc
}

// ---- PHC codec ----

func TestHasher_DefaultParametersAndFormat(t *testing.T) {
	if p := auth.DefaultHasherParams; p.MemoryKiB != 19456 || p.Time != 2 || p.Threads != 1 {
		t.Fatalf("DefaultHasherParams = %+v, want m=19456,t=2,p=1 (OWASP minimum, plan P-06)", p)
	}
	h := auth.NewHasher(auth.HasherOptions{}) // defaults: real cost, once
	phc := mustHash(t, h, "correct horse battery staple")
	re := regexp.MustCompile(`^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	if !re.MatchString(phc) {
		t.Fatalf("PHC string %q does not match $argon2id$v=19$m=19456,t=2,p=1$<16-byte salt>$<32-byte key> in raw std base64", phc)
	}
	// Independent recomputation with the reference library proves it is real Argon2id with those parameters.
	p, salt, key, err := auth.DecodePHC(phc)
	if err != nil {
		t.Fatal(err)
	}
	if len(salt) != 16 || len(key) != 32 || p != auth.DefaultHasherParams {
		t.Fatalf("decoded %+v salt=%d key=%d", p, len(salt), len(key))
	}
	want := argon2.IDKey([]byte("correct horse battery staple"), salt, 2, 19456, 1, 32)
	if string(want) != string(key) {
		t.Error("stored key is not Argon2id(password, salt, t=2, m=19456, p=1, 32)")
	}
	if ok, err := h.Verify(context.Background(), phc, "correct horse battery staple"); err != nil || !ok {
		t.Errorf("Verify of its own hash = %v, %v", ok, err)
	}
}

func TestHasher_HashesAreSaltedAndSelfDescribing(t *testing.T) {
	h := cheapHasher()
	a, b := mustHash(t, h, "same password"), mustHash(t, h, "same password")
	if a == b {
		t.Error("two hashes of one password are identical: salt is not random")
	}
	fields := strings.Split(a, "$")
	if strings.Contains(a, "same password") || len(fields) != 6 || strings.Contains(fields[4]+fields[5], "=") {
		t.Errorf("PHC string leaks the password or uses padded base64: %q", a)
	}
	if !strings.HasPrefix(a, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Errorf("PHC string %q must carry the hasher's own parameters", a)
	}
}

func TestHasher_VerifiesHashesFromTheReferenceLibrary(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("from elsewhere"), salt, cheap.Time, cheap.MemoryKiB, cheap.Threads, 32)
	phc := auth.EncodePHC(cheap, salt, key)
	h := cheapHasher()
	if ok, err := h.Verify(context.Background(), phc, "from elsewhere"); err != nil || !ok {
		t.Errorf("Verify of a reference-library hash = %v, %v", ok, err)
	}
	if ok, _ := h.Verify(context.Background(), phc, "From elsewhere"); ok {
		t.Error("Verify accepted a password differing in case")
	}
}

func TestHasher_PHCRoundTrip(t *testing.T) {
	p := auth.HasherParams{MemoryKiB: 4096, Time: 3, Threads: 2}
	salt, key := []byte("saltsaltsaltsalt"), []byte("0123456789abcdef0123456789abcdef")
	phc := auth.EncodePHC(p, salt, key)
	if want := "$argon2id$v=19$m=4096,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"; phc != want {
		t.Errorf("EncodePHC = %q, want %q", phc, want)
	}
	gp, gs, gk, err := auth.DecodePHC(phc)
	if err != nil || gp != p || string(gs) != string(salt) || string(gk) != string(key) {
		t.Errorf("DecodePHC = %+v %q %q %v", gp, gs, gk, err)
	}
}

func TestHasher_DecodePHCBounds(t *testing.T) {
	const salt, key = "c2FsdHNhbHRzYWx0c2FsdA", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"
	mk := func(head string) string { return head + "$" + salt + "$" + key }
	tests := []struct {
		name    string
		phc     string
		wantErr bool
	}{
		{"default parameters", mk("$argon2id$v=19$m=19456,t=2,p=1"), false},
		{"max memory 256 MiB", mk("$argon2id$v=19$m=262144,t=2,p=1"), false},
		{"max time 10", mk("$argon2id$v=19$m=19456,t=10,p=1"), false},
		{"max threads 4", mk("$argon2id$v=19$m=19456,t=2,p=4"), false},

		{"memory above 256 MiB", mk("$argon2id$v=19$m=262145,t=2,p=1"), true},
		{"memory 4 GiB (blow-up attempt)", mk("$argon2id$v=19$m=4194304,t=2,p=1"), true},
		{"memory overflowing uint32", mk("$argon2id$v=19$m=99999999999,t=2,p=1"), true},
		{"time above 10", mk("$argon2id$v=19$m=19456,t=11,p=1"), true},
		{"threads above 4", mk("$argon2id$v=19$m=19456,t=2,p=5"), true},
		{"threads 255", mk("$argon2id$v=19$m=19456,t=2,p=255"), true},
		{"zero memory", mk("$argon2id$v=19$m=0,t=2,p=1"), true},
		{"zero time", mk("$argon2id$v=19$m=19456,t=0,p=1"), true},
		{"zero threads", mk("$argon2id$v=19$m=19456,t=2,p=0"), true},
		{"negative memory", mk("$argon2id$v=19$m=-1,t=2,p=1"), true},

		{"version 16", mk("$argon2id$v=16$m=19456,t=2,p=1"), true},
		{"version 18", mk("$argon2id$v=18$m=19456,t=2,p=1"), true},
		{"version 20", mk("$argon2id$v=20$m=19456,t=2,p=1"), true},
		{"version missing", mk("$argon2id$m=19456,t=2,p=1"), true},
		{"argon2i", mk("$argon2i$v=19$m=19456,t=2,p=1"), true},
		{"argon2d", mk("$argon2d$v=19$m=19456,t=2,p=1"), true},
		{"bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy", true},
		{"empty", "", true},
		{"just dollar", "$", true},
		{"missing key", "$argon2id$v=19$m=19456,t=2,p=1$" + salt, true},
		{"empty key", "$argon2id$v=19$m=19456,t=2,p=1$" + salt + "$", true},
		{"empty salt", "$argon2id$v=19$m=19456,t=2,p=1$$" + key, true},
		{"trailing field", mk("$argon2id$v=19$m=19456,t=2,p=1") + "$extra", true},
		{"parameter order swapped", mk("$argon2id$v=19$t=2,m=19456,p=1"), true},
		{"extra parameter", mk("$argon2id$v=19$m=19456,t=2,p=1,x=3"), true},
		{"missing parameter", mk("$argon2id$v=19$m=19456,t=2"), true},
		{"salt not base64", "$argon2id$v=19$m=19456,t=2,p=1$!!!$" + key, true},
		{"key not base64", "$argon2id$v=19$m=19456,t=2,p=1$" + salt + "$@@@", true},
		{"leading garbage", "x" + mk("$argon2id$v=19$m=19456,t=2,p=1"), true},
		{"whitespace padded", " " + mk("$argon2id$v=19$m=19456,t=2,p=1") + " ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := auth.DecodePHC(tt.phc)
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodePHC(%q) error = %v, wantErr %v", tt.phc, err, tt.wantErr)
			}
		})
	}
}

// ---- verification ----

func TestHasher_Verify(t *testing.T) {
	h := cheapHasher()
	phc := mustHash(t, h, "correct horse battery staple")
	tests := []struct {
		name, pw string
		want     bool
	}{
		{"right password", "correct horse battery staple", true},
		{"wrong password", "wrong horse battery staple", false},
		{"different case", "Correct horse battery staple", false},
		{"prefix", "correct horse battery", false},
		{"extra suffix", "correct horse battery staple!", false},
		{"empty", "", false},
		{"padded", " correct horse battery staple", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := h.Verify(context.Background(), phc, tt.pw)
			if err != nil || ok != tt.want {
				t.Errorf("Verify = %v, %v; want %v, nil", ok, err, tt.want)
			}
		})
	}
}

func TestHasher_VerifyLongNonASCIIPassword(t *testing.T) {
	h := cheapHasher()
	pw := strings.Repeat("ક", 256) // 768 bytes
	phc := mustHash(t, h, pw)
	if ok, err := h.Verify(context.Background(), phc, pw); err != nil || !ok {
		t.Errorf("Verify = %v, %v", ok, err)
	}
	if ok, _ := h.Verify(context.Background(), phc, pw[:len(pw)-3]); ok {
		t.Error("a truncated password verified: the hasher must not truncate")
	}
}

func TestHasher_VerifyUsesTheParametersEncodedInTheHash(t *testing.T) {
	old := auth.NewHasher(auth.HasherOptions{Params: auth.HasherParams{MemoryKiB: 16, Time: 2, Threads: 1}})
	phc := mustHash(t, old, "pw-for-old-params")
	rc := &runCounter{}
	current := auth.NewHasher(auth.HasherOptions{Params: cheap, OnArgon2: rc.hook})
	if ok, err := current.Verify(context.Background(), phc, "pw-for-old-params"); err != nil || !ok {
		t.Fatalf("a hash made under older parameters must still verify: %v, %v", ok, err)
	}
	if got := rc.last(); got != (auth.HasherParams{MemoryKiB: 16, Time: 2, Threads: 1}) {
		t.Errorf("verify ran with %+v, want the parameters stored in the hash", got)
	}
}

func TestHasher_NeedsRehash(t *testing.T) {
	a := cheapHasher()
	phc := mustHash(t, a, "pw")
	if a.NeedsRehash(phc) {
		t.Error("NeedsRehash = true for a hash made with the current parameters")
	}
	for name, p := range map[string]auth.HasherParams{
		"memory changed":  {MemoryKiB: 16, Time: 1, Threads: 1},
		"time changed":    {MemoryKiB: 8, Time: 2, Threads: 1},
		"threads changed": {MemoryKiB: 8, Time: 1, Threads: 2},
		"all changed":     {MemoryKiB: 64, Time: 3, Threads: 2},
	} {
		if !auth.NewHasher(auth.HasherOptions{Params: p}).NeedsRehash(phc) {
			t.Errorf("%s: NeedsRehash = false, want true", name)
		}
	}
	// And in the other direction: raising the cost marks stored hashes stale.
	old := mustHash(t, auth.NewHasher(auth.HasherOptions{Params: auth.HasherParams{MemoryKiB: 8, Time: 1, Threads: 1}}), "pw")
	if !auth.NewHasher(auth.HasherOptions{}).NeedsRehash(old) {
		t.Error("default-cost hasher did not flag a cheap hash as stale")
	}
}

// ---- constant cost (SC-003) ----

func TestHasher_EveryVerifyCostsExactlyOneArgon2Run(t *testing.T) {
	rc := &runCounter{}
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, OnArgon2: rc.hook})
	phc := mustHash(t, h, "right")
	// Warm the dummy hash so its one-off computation is not attributed to a verify.
	if ok, err := h.Verify(context.Background(), "", "warm-up"); ok || err != nil {
		t.Fatalf("no-account verify = %v, %v; want false, nil", ok, err)
	}

	cases := []struct {
		name, phc, pw string
	}{
		{"right password", phc, "right"},
		{"wrong password", phc, "wrong"},
		{"empty password", phc, ""},
		{"no such account", "", "anything"},
		{"no such account, empty password", "", ""},
		{"malformed stored hash", "$argon2id$garbage", "right"},
		{"unsupported algorithm", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy", "right"},
		{"out-of-bounds stored hash", "$argon2id$v=19$m=4194304,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY", "right"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := rc.n()
			ok, _ := h.Verify(context.Background(), tc.phc, tc.pw)
			if got := rc.n() - before; got != 1 {
				t.Errorf("Verify performed %d Argon2 runs, want exactly 1", got)
			}
			if want := tc.name == "right password"; ok != want {
				t.Errorf("ok = %v, want %v", ok, want)
			}
		})
	}
}

func TestHasher_DummyHashIsComputedOnceWithCurrentParameters(t *testing.T) {
	rc := &runCounter{}
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, OnArgon2: rc.hook})
	const calls = 6
	for range calls {
		if ok, err := h.Verify(context.Background(), "", "pw"); ok || err != nil {
			t.Fatalf("no-account verify = %v, %v; want false, nil", ok, err)
		}
	}
	// One Argon2 run per verify plus at most one run in total for creating the dummy hash.
	if got := rc.n(); got != calls && got != calls+1 {
		t.Errorf("%d no-account verifies caused %d Argon2 runs, want %d or %d (dummy computed once)", calls, got, calls, calls+1)
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for i, p := range rc.runs {
		if p != cheap {
			t.Errorf("run %d used %+v; the dummy path must use the current parameters %+v", i, p, cheap)
		}
	}
}

func TestHasher_DummyHashIsComputedOnceUnderConcurrency(t *testing.T) {
	rc := &runCounter{}
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, MaxConcurrency: 16, OnArgon2: rc.hook})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.Verify(context.Background(), "", "pw")
		}()
	}
	wg.Wait()
	if got := rc.n(); got != 16 && got != 17 {
		t.Errorf("16 concurrent no-account verifies caused %d runs, want 16 or 17 (sync.Once for the dummy)", got)
	}
}

// ---- concurrency cap ----

type gate struct {
	entered chan struct{}
	release chan struct{}
}

func newGate() *gate { return &gate{entered: make(chan struct{}, 16), release: make(chan struct{})} }
func (g *gate) hook(auth.HasherParams) {
	g.entered <- struct{}{}
	<-g.release
}

type fakeAfter struct {
	mu   sync.Mutex
	asks []time.Duration
	fire chan time.Time
}

func newFakeAfter() *fakeAfter { return &fakeAfter{fire: make(chan time.Time)} }
func (f *fakeAfter) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.asks = append(f.asks, d)
	f.mu.Unlock()
	return f.fire
}
func (f *fakeAfter) asked() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.asks...)
}

func holdSlots(t *testing.T, h *auth.Hasher, g *gate, phc string, n int) (wait func()) {
	t.Helper()
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := h.Verify(context.Background(), phc, "pw"); err != nil || !ok {
				t.Errorf("slot holder: Verify = %v, %v", ok, err)
			}
		}()
	}
	for range n {
		<-g.entered // each holder is now inside the semaphore, blocked in the hook
	}
	return wg.Wait
}

func TestHasher_SaturatedSemaphoreReturnsErrBusyAfterTheWaitBound(t *testing.T) {
	phc := mustHash(t, cheapHasher(), "pw")
	g, fa := newGate(), newFakeAfter()
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, MaxConcurrency: 2, After: fa.After, OnArgon2: g.hook})
	wait := holdSlots(t, h, g, phc, 2)

	close(fa.fire) // the wait bound elapses
	ok, err := h.Verify(context.Background(), phc, "pw")
	if ok || !errors.Is(err, auth.ErrBusy) {
		t.Fatalf("third verify with two slots held = %v, %v; want false, ErrBusy", ok, err)
	}
	if _, err := h.Hash(context.Background(), "pw"); !errors.Is(err, auth.ErrBusy) {
		t.Errorf("Hash on a saturated hasher: err = %v, want ErrBusy", err)
	}
	if asks := fa.asked(); len(asks) == 0 || asks[0] != 2*time.Second {
		t.Errorf("wait bound requested = %v, want 2s by default", asks)
	}
	close(g.release)
	wait()

	// Slots are released: the hasher works again, including after the failed waits.
	for i := range 4 {
		if ok, err := h.Verify(context.Background(), phc, "pw"); err != nil || !ok {
			t.Fatalf("verify %d after release = %v, %v", i, ok, err)
		}
	}
}

func TestHasher_WaitBoundIsConfigurable(t *testing.T) {
	phc := mustHash(t, cheapHasher(), "pw")
	g, fa := newGate(), newFakeAfter()
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, MaxConcurrency: 1, WaitBound: 750 * time.Millisecond, After: fa.After, OnArgon2: g.hook})
	wait := holdSlots(t, h, g, phc, 1)
	close(fa.fire)
	if _, err := h.Verify(context.Background(), phc, "pw"); !errors.Is(err, auth.ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if asks := fa.asked(); len(asks) == 0 || asks[0] != 750*time.Millisecond {
		t.Errorf("wait bound requested = %v, want 750ms", asks)
	}
	close(g.release)
	wait()
}

func TestHasher_DefaultConcurrencyIsFour(t *testing.T) {
	phc := mustHash(t, cheapHasher(), "pw")
	g, fa := newGate(), newFakeAfter()
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, After: fa.After, OnArgon2: g.hook}) // MaxConcurrency unset
	wait := holdSlots(t, h, g, phc, 4)                                                        // four fit
	close(fa.fire)
	if _, err := h.Verify(context.Background(), phc, "pw"); !errors.Is(err, auth.ErrBusy) {
		t.Fatalf("fifth concurrent verify: err = %v, want ErrBusy", err)
	}
	close(g.release)
	wait()
}

func TestHasher_ContextCancelWhileWaitingIsNotBusy(t *testing.T) {
	phc := mustHash(t, cheapHasher(), "pw")
	g, fa := newGate(), newFakeAfter() // fa.fire is never closed: only the context can end the wait
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, MaxConcurrency: 1, After: fa.After, OnArgon2: g.hook})
	wait := holdSlots(t, h, g, phc, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ok, err := h.Verify(ctx, phc, "pw")
	if ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify with a cancelled context = %v, %v; want false, context.Canceled", ok, err)
	}
	if errors.Is(err, auth.ErrBusy) {
		t.Error("a cancelled request must not be reported as busy")
	}
	close(g.release)
	wait()
}

func TestHasher_ConcurrencyNeverExceedsTheCap(t *testing.T) {
	const limit = 3
	var mu sync.Mutex
	inflight, peak := 0, 0
	h := auth.NewHasher(auth.HasherOptions{Params: cheap, MaxConcurrency: limit, WaitBound: time.Minute, OnArgon2: func(auth.HasherParams) {
		mu.Lock()
		inflight++
		peak = max(peak, inflight)
		mu.Unlock()
		for range 200 { // yield so that overlapping runs have a chance to be observed
			runtime.Gosched()
		}
		mu.Lock()
		inflight--
		mu.Unlock()
	}})
	phc := mustHash(t, cheapHasher(), "pw")
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := h.Verify(context.Background(), phc, "pw"); err != nil || !ok {
				t.Errorf("Verify = %v, %v", ok, err)
			}
		}()
	}
	wg.Wait()
	if peak > limit {
		t.Errorf("observed %d concurrent Argon2 runs, cap is %d", peak, limit)
	}
}

func TestHasher_RandomnessComesFromCryptoRand(t *testing.T) {
	// Sanity: 64 hashes of one password never repeat a salt (16 random bytes each).
	h := cheapHasher()
	seen := map[string]bool{}
	for range 64 {
		_, salt, _, err := auth.DecodePHC(mustHash(t, h, "pw"))
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(salt)] {
			t.Fatal("repeated salt")
		}
		seen[string(salt)] = true
	}
}
