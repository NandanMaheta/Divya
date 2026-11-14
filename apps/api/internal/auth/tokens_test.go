// Tests for access-token issuing and claims (T024; D-05, plan P-02).
//
// API assumptions (contracts are silent; the implementer must match these names):
//
//	type SigningKeys struct{ ... }
//	func NewSigningKeys(activeKID string, seeds map[string][]byte) (*SigningKeys, error)
//	    // kid -> 32-byte Ed25519 seed; every key verifies, only the active one signs.
//	    // Errors: empty ring, active kid not in the ring, a seed that is not 32 bytes.
//	type AccessSubject struct{ UserID, SessionID, OrgID uuid.UUID; Role string }   // Role: "admin" | "client"
//	type AccessClaims struct {
//	    UserID, SessionID, OrgID uuid.UUID
//	    Role                     string
//	    IssuedAt, ExpiresAt      time.Time   // whole seconds, UTC
//	    ID                       string      // jti
//	}
//	var ErrInvalidToken error                // every Verify failure wraps it (errors.Is)
//	func NewTokens(keys *SigningKeys, now func() time.Time) *Tokens
//	func (*Tokens) Issue(sub AccessSubject, absoluteExpiry time.Time) (string, error)
//	func (*Tokens) Verify(raw string) (AccessClaims, error)
//
// Compact JWS only. Header {"alg":"EdDSA","typ":"at+jwt","kid":<active kid>}; claims iss and aud are
// "swastik-api" (aud may serialise as a string or a one-element array); sub, sid and org are the
// canonical UUID strings; iat and exp are integer NumericDates; jti is a random string of at
// least 16 characters. exp = min(now+15m, absoluteExpiry) truncated to whole seconds.
package auth_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

var (
	tkNow = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

	tkSub = auth.AccessSubject{
		UserID:    uuid.MustParse("0190c1a2-0000-7000-8000-00000000a001"),
		SessionID: uuid.MustParse("0190c1a2-0000-7000-8000-00000000b002"),
		OrgID:     uuid.MustParse("0190c1a2-0000-7000-8000-00000000c003"),
		Role:      "client",
	}
	tkFarFuture = tkNow.Add(90 * 24 * time.Hour)
)

func tkSeed(b byte) []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = b + byte(i)
	}
	return s
}

func tkKeys(t testing.TB, active string, kids ...string) *auth.SigningKeys {
	t.Helper()
	seeds := map[string][]byte{}
	for i, k := range kids {
		seeds[k] = tkSeed(byte(10 * (i + 1)))
	}
	keys, err := auth.NewSigningKeys(active, seeds)
	if err != nil {
		t.Fatalf("NewSigningKeys: %v", err)
	}
	return keys
}

// tkSeedFor returns the seed tkKeys assigned to kid (kids are numbered in call order, so tests
// must pass them in a fixed order).
func tkSeedFor(kids []string, kid string) []byte {
	for i, k := range kids {
		if k == kid {
			return tkSeed(byte(10 * (i + 1)))
		}
	}
	return nil
}

type tkClock struct{ now time.Time }

func (c *tkClock) Now() time.Time { return c.now }

func tkTokens(t testing.TB, keys *auth.SigningKeys) (*auth.Tokens, *tkClock) {
	t.Helper()
	c := &tkClock{now: tkNow}
	return auth.NewTokens(keys, c.Now), c
}

type tkParts struct {
	Header       map[string]any
	Claims       map[string]any
	SigningInput string
	Signature    []byte
}

func tkSplit(t testing.TB, raw string) tkParts {
	t.Helper()
	segs := strings.Split(raw, ".")
	if len(segs) != 3 {
		t.Fatalf("token has %d segments, want 3 (compact JWS): %q", len(segs), raw)
	}
	var p tkParts
	for i, dst := range []*map[string]any{&p.Header, &p.Claims} {
		b, err := base64.RawURLEncoding.DecodeString(segs[i])
		if err != nil {
			t.Fatalf("segment %d is not unpadded base64url: %v", i, err)
		}
		if err := json.Unmarshal(b, dst); err != nil {
			t.Fatalf("segment %d is not JSON: %v", i, err)
		}
	}
	sig, err := base64.RawURLEncoding.DecodeString(segs[2])
	if err != nil {
		t.Fatalf("signature is not base64url: %v", err)
	}
	p.SigningInput, p.Signature = segs[0]+"."+segs[1], sig
	return p
}

func tkNum(t testing.TB, claims map[string]any, name string) int64 {
	t.Helper()
	f, ok := claims[name].(float64)
	if !ok || f != float64(int64(f)) {
		t.Fatalf("claim %s = %#v, want an integer NumericDate", name, claims[name])
	}
	return int64(f)
}

func TestTokens_HeaderAndSignature(t *testing.T) {
	kids := []string{"kid-a", "kid-b"}
	keys := tkKeys(t, "kid-b", kids...)
	tokens, _ := tkTokens(t, keys)
	raw, err := tokens.Issue(tkSub, tkFarFuture)
	if err != nil {
		t.Fatal(err)
	}
	p := tkSplit(t, raw)
	if p.Header["alg"] != "EdDSA" || p.Header["typ"] != "at+jwt" || p.Header["kid"] != "kid-b" {
		t.Errorf("header = %v, want alg=EdDSA typ=at+jwt kid=kid-b (the active key)", p.Header)
	}
	for _, banned := range []string{"jwk", "jku", "x5u", "x5c", "crit"} {
		if _, ok := p.Header[banned]; ok {
			t.Errorf("header carries %q", banned)
		}
	}
	pub := ed25519.NewKeyFromSeed(tkSeedFor(kids, "kid-b")).Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, []byte(p.SigningInput), p.Signature) {
		t.Error("signature does not verify under the active key's Ed25519 public key")
	}
	other := ed25519.NewKeyFromSeed(tkSeedFor(kids, "kid-a")).Public().(ed25519.PublicKey)
	if ed25519.Verify(other, []byte(p.SigningInput), p.Signature) {
		t.Error("token verifies under the non-active key: only the active key may sign")
	}
}

func TestTokens_Claims(t *testing.T) {
	tokens, _ := tkTokens(t, tkKeys(t, "k1", "k1"))
	raw, err := tokens.Issue(tkSub, tkFarFuture)
	if err != nil {
		t.Fatal(err)
	}
	c := tkSplit(t, raw).Claims
	if c["iss"] != "swastik-api" {
		t.Errorf("iss = %v", c["iss"])
	}
	switch aud := c["aud"].(type) {
	case string:
		if aud != "swastik-api" {
			t.Errorf("aud = %q", aud)
		}
	case []any:
		if len(aud) != 1 || aud[0] != "swastik-api" {
			t.Errorf("aud = %v", aud)
		}
	default:
		t.Errorf("aud = %#v, want swastik-api", c["aud"])
	}
	for name, want := range map[string]string{
		"sub": tkSub.UserID.String(), "sid": tkSub.SessionID.String(), "org": tkSub.OrgID.String(), "role": "client",
	} {
		if c[name] != want {
			t.Errorf("claim %s = %v, want %s", name, c[name], want)
		}
	}
	if iat := tkNum(t, c, "iat"); iat != tkNow.Unix() {
		t.Errorf("iat = %d, want %d", iat, tkNow.Unix())
	}
	if exp := tkNum(t, c, "exp"); exp != tkNow.Add(15*time.Minute).Unix() {
		t.Errorf("exp = %d, want iat+15m = %d", exp, tkNow.Add(15*time.Minute).Unix())
	}
	if jti, _ := c["jti"].(string); len(jti) < 16 {
		t.Errorf("jti = %q, want a random string of at least 16 characters (128 bits)", c["jti"])
	}
}

func TestTokens_RolesAreCarried(t *testing.T) {
	tokens, _ := tkTokens(t, tkKeys(t, "k1", "k1"))
	for _, role := range []string{"admin", "client"} {
		sub := tkSub
		sub.Role = role
		raw, err := tokens.Issue(sub, tkFarFuture)
		if err != nil {
			t.Fatal(err)
		}
		if got := tkSplit(t, raw).Claims["role"]; got != role {
			t.Errorf("role claim = %v, want %s", got, role)
		}
	}
}

func TestTokens_ExpiryIsMinOfFifteenMinutesAndAbsoluteExpiry(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		absolute time.Time
		wantExp  time.Time
	}{
		{"absolute far away", tkNow, tkNow.Add(90 * 24 * time.Hour), tkNow.Add(15 * time.Minute)},
		{"absolute exactly 15m away", tkNow, tkNow.Add(15 * time.Minute), tkNow.Add(15 * time.Minute)},
		{"absolute 10m away", tkNow, tkNow.Add(10 * time.Minute), tkNow.Add(10 * time.Minute)},
		{"absolute 1s away", tkNow, tkNow.Add(time.Second), tkNow.Add(time.Second)},
		{"absolute just over 15m", tkNow, tkNow.Add(15*time.Minute + time.Second), tkNow.Add(15 * time.Minute)},
		// Whole-second truncation (NumericDate): sub-second parts never round up.
		{"sub-second now", tkNow.Add(900 * time.Millisecond), tkFarFuture, tkNow.Add(15 * time.Minute)},
		{"sub-second absolute", tkNow, tkNow.Add(10*time.Minute + 999*time.Millisecond), tkNow.Add(10 * time.Minute)},
		{"sub-second both", tkNow.Add(500 * time.Millisecond), tkNow.Add(10*time.Minute + 750*time.Millisecond), tkNow.Add(10 * time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, clock := tkTokens(t, tkKeys(t, "k1", "k1"))
			clock.now = tt.now
			raw, err := tokens.Issue(tkSub, tt.absolute)
			if err != nil {
				t.Fatal(err)
			}
			c := tkSplit(t, raw).Claims
			if got := tkNum(t, c, "exp"); got != tt.wantExp.Unix() {
				t.Errorf("exp = %d (%v), want %d (%v)", got, time.Unix(got, 0).UTC(), tt.wantExp.Unix(), tt.wantExp)
			}
			if got := tkNum(t, c, "iat"); got != tt.now.Unix() {
				t.Errorf("iat = %d, want %d (truncated now)", got, tt.now.Unix())
			}
		})
	}
}

func TestTokens_IssueUsesTheInjectedClock(t *testing.T) {
	tokens, clock := tkTokens(t, tkKeys(t, "k1", "k1"))
	for _, now := range []time.Time{
		time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2100, 6, 1, 12, 0, 0, 0, time.UTC),
	} {
		clock.now = now
		raw, err := tokens.Issue(tkSub, now.Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if got := tkNum(t, tkSplit(t, raw).Claims, "iat"); got != now.Unix() {
			t.Errorf("iat = %d, want the injected clock's %d", got, now.Unix())
		}
	}
}

func TestTokens_RoundTrip(t *testing.T) {
	tokens, clock := tkTokens(t, tkKeys(t, "k1", "k1"))
	clock.now = tkNow.Add(250 * time.Millisecond)
	raw, err := tokens.Issue(tkSub, tkFarFuture)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = tkNow.Add(time.Minute) // verified a minute later
	got, err := tokens.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != tkSub.UserID || got.SessionID != tkSub.SessionID || got.OrgID != tkSub.OrgID || got.Role != "client" {
		t.Errorf("claims = %+v, want the issued subject", got)
	}
	if !got.IssuedAt.Equal(tkNow) || !got.ExpiresAt.Equal(tkNow.Add(15*time.Minute)) {
		t.Errorf("iat/exp = %v / %v, want %v / %v", got.IssuedAt, got.ExpiresAt, tkNow, tkNow.Add(15*time.Minute))
	}
	if len(got.ID) < 16 {
		t.Errorf("jti = %q", got.ID)
	}
}

func TestTokens_ValidUntilTheSecondBeforeExpiry(t *testing.T) {
	tokens, clock := tkTokens(t, tkKeys(t, "k1", "k1"))
	raw, _ := tokens.Issue(tkSub, tkFarFuture)
	exp := tkNow.Add(15 * time.Minute)
	for _, now := range []time.Time{tkNow, tkNow.Add(time.Minute), exp.Add(-time.Second), exp.Add(-time.Nanosecond)} {
		clock.now = now
		if _, err := tokens.Verify(raw); err != nil {
			t.Errorf("Verify at %v (exp %v): %v", now.Sub(tkNow), exp.Sub(tkNow), err)
		}
	}
	for _, now := range []time.Time{exp, exp.Add(time.Nanosecond), exp.Add(time.Hour)} {
		clock.now = now
		if _, err := tokens.Verify(raw); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("Verify at exp+%v: err = %v, want ErrInvalidToken", now.Sub(exp), err)
		}
	}
}

func TestTokens_JTIIsUniquePerToken(t *testing.T) {
	tokens, _ := tkTokens(t, tkKeys(t, "k1", "k1"))
	seen := map[string]bool{}
	for range 500 {
		raw, err := tokens.Issue(tkSub, tkFarFuture)
		if err != nil {
			t.Fatal(err)
		}
		jti, _ := tkSplit(t, raw).Claims["jti"].(string)
		if seen[jti] {
			t.Fatalf("duplicate jti %q", jti)
		}
		seen[jti] = true
	}
}

func TestTokens_RingVerifiesEveryKeyButSignsWithTheActiveOne(t *testing.T) {
	kids := []string{"k1", "k2"}
	oldOnly, _ := tkTokens(t, tkKeys(t, "k1", "k1"))
	bothActiveK2, _ := tkTokens(t, tkKeys(t, "k2", kids...))
	// tkKeys numbers seeds by position, so "k1" is seed #1 in both rings built above.
	oldToken, err := oldOnly.Issue(tkSub, tkFarFuture)
	if err != nil {
		t.Fatal(err)
	}
	newToken, err := bothActiveK2.Issue(tkSub, tkFarFuture)
	if err != nil {
		t.Fatal(err)
	}
	if kid := tkSplit(t, newToken).Header["kid"]; kid != "k2" {
		t.Errorf("rotated ring signed with kid %v, want the active k2", kid)
	}
	for name, raw := range map[string]string{"token signed by the old key": oldToken, "token signed by the active key": newToken} {
		if _, err := bothActiveK2.Verify(raw); err != nil {
			t.Errorf("two-key ring rejected a %s: %v", name, err)
		}
	}
}

func TestTokens_RotationLifecycle(t *testing.T) {
	// Deploy 1: only k1. Deploy 2: add k2 (still signing with k1). Deploy 3: activate k2.
	// Deploy 4 (after 15 minutes plus rollout): remove k1, so k1-signed tokens stop verifying.
	k1Only, _ := tkTokens(t, tkKeys(t, "k1", "k1"))
	t1, _ := k1Only.Issue(tkSub, tkFarFuture)

	addK2, _ := tkTokens(t, tkKeys(t, "k1", "k1", "k2"))
	if _, err := addK2.Verify(t1); err != nil {
		t.Errorf("after adding k2, a k1 token failed: %v", err)
	}

	activateK2, _ := tkTokens(t, tkKeys(t, "k2", "k1", "k2"))
	if _, err := activateK2.Verify(t1); err != nil {
		t.Errorf("after activating k2, an in-flight k1 token failed: %v", err)
	}
	t2, _ := activateK2.Issue(tkSub, tkFarFuture)
	if _, err := addK2.Verify(t2); err != nil {
		t.Errorf("instances still on the add-k2 config must verify tokens signed by k2: %v", err)
	}

	// k2 only. The seed for "k2" must be the same material as before, hence a ring built with the
	// same positional scheme: k1 -> #1, k2 -> #2.
	seeds := map[string][]byte{"k2": tkSeed(20)}
	keys, err := auth.NewSigningKeys("k2", seeds)
	if err != nil {
		t.Fatal(err)
	}
	removed := auth.NewTokens(keys, (&tkClock{now: tkNow}).Now)
	if _, err := removed.Verify(t2); err != nil {
		t.Errorf("k2 token failed on the k2-only ring: %v", err)
	}
	if _, err := removed.Verify(t1); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a k1 token on the k2-only ring: err = %v, want ErrInvalidToken (unknown kid)", err)
	}
}

func TestTokens_NewSigningKeysValidation(t *testing.T) {
	tests := []struct {
		name   string
		active string
		seeds  map[string][]byte
	}{
		{"nil ring", "k1", nil},
		{"empty ring", "k1", map[string][]byte{}},
		{"active kid missing", "k9", map[string][]byte{"k1": tkSeed(1)}},
		{"empty active kid", "", map[string][]byte{"k1": tkSeed(1)}},
		{"seed 31 bytes", "k1", map[string][]byte{"k1": tkSeed(1)[:31]}},
		{"seed 33 bytes", "k1", map[string][]byte{"k1": append(tkSeed(1), 0)}},
		{"nil seed", "k1", map[string][]byte{"k1": nil}},
		{"one bad seed among good", "k1", map[string][]byte{"k1": tkSeed(1), "k2": tkSeed(2)[:16]}},
		{"empty kid", "", map[string][]byte{"": tkSeed(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if k, err := auth.NewSigningKeys(tt.active, tt.seeds); err == nil {
				t.Fatalf("NewSigningKeys accepted bad input: %v", k)
			}
		})
	}
}

func TestTokens_SigningKeysDoNotAliasCallerSeeds(t *testing.T) {
	seed := tkSeed(7)
	keys, err := auth.NewSigningKeys("k1", map[string][]byte{"k1": seed})
	if err != nil {
		t.Fatal(err)
	}
	tokens, _ := tkTokens(t, keys)
	for i := range seed {
		seed[i] = 0
	}
	raw, err := tokens.Issue(tkSub, tkFarFuture)
	if err != nil {
		t.Fatal(err)
	}
	p := tkSplit(t, raw)
	pub := ed25519.NewKeyFromSeed(tkSeed(7)).Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, []byte(p.SigningInput), p.Signature) {
		t.Error("ring depended on caller-owned seed bytes")
	}
}
