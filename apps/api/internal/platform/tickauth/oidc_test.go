// Offline tests for the Google OIDC authenticator (feature 002, FR-002, US2.1-2.4, SC-004).
//
// No network and no Google: a local httptest server serves a JWKS for an RSA key generated in the
// test, and every token is minted and signed in the test with the standard library (RS256), so
// this file needs no JOSE library. The clock is injected through the `now` argument of NewOIDC.
//
// Contract: contracts/go-module-api.md, section internal/platform/tickauth:
//
//	NewOIDC(ctx, audience, serviceAccount, now func() time.Time, opts ...OIDCOption) Authenticator
//	WithJWKSURL(url string) OIDCOption
//	WithHTTPClient(c *http.Client) OIDCOption
//
// Verification order (plan.md, Security notes): header present and Bearer, signature (RS256 only),
// issuer (https://accounts.google.com or accounts.google.com), audience, expiry against the injected
// clock, then email == serviceAccount and email_verified == true. Every failure before the email
// check is ErrUnauthenticated (401); a valid token for another identity is ErrForbidden (403).
//
// Assumptions where the contract is silent (recorded for the implementer):
//   - NewOIDC with an empty audience or an empty service account fails closed: it never returns nil
//     from Authenticate (the wiring normally uses Disabled() instead; this is defence in depth
//     because go-oidc skips the audience check when the client id is empty).
//   - A JWKS endpoint that cannot be fetched fails closed with a non-nil error.
//   - A missing email claim is an identity mismatch (ErrForbidden), not a malformed token.
//
// Note for the task order: this file references NewOIDC, which only exists after T018, so the
// package's tests (including the Dev/Disabled/Contract ones in tickauth_test.go) compile only once
// T018 has at least declared NewOIDC, WithJWKSURL and WithHTTPClient.
package tickauth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testAudience       = "https://divya-api-abc123-el.a.run.app"
	testServiceAccount = "scheduler@divya-test.iam.gserviceaccount.com"
	googleIssuer       = "https://accounts.google.com"
)

var t0 = time.Date(2026, time.November, 13, 5, 0, 0, 0, time.UTC)

// ---- keys -----------------------------------------------------------------------------------

type signer struct {
	kid string
	key *rsa.PrivateKey
}

var (
	keyOnce          sync.Once
	signerA, signerB signer
)

// keys returns two independent RSA keys: A is published in the JWKS, B is never published unless
// a test rotates it in.
func keys(t *testing.T) (a, b signer) {
	t.Helper()
	keyOnce.Do(func() {
		ka, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate key A: %v", err)
		}
		kb, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate key B: %v", err)
		}
		signerA, signerB = signer{"key-a", ka}, signer{"key-b", kb}
	})
	return signerA, signerB
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jsonB64(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b64(raw)
}

func (s signer) jwk() map[string]any {
	return map[string]any{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": s.kid,
		"n": b64(s.key.N.Bytes()),
		"e": b64(big.NewInt(int64(s.key.E)).Bytes()),
	}
}

// ---- tokens ---------------------------------------------------------------------------------

func validClaims() map[string]any {
	return map[string]any{
		"iss":            googleIssuer,
		"aud":            testAudience,
		"sub":            "104599999999999999999",
		"azp":            "104599999999999999999",
		"email":          testServiceAccount,
		"email_verified": true,
		"iat":            t0.Add(-time.Minute).Unix(),
		"exp":            t0.Add(time.Hour).Unix(),
	}
}

func claimsWith(mods ...func(map[string]any)) map[string]any {
	c := validClaims()
	for _, m := range mods {
		m(c)
	}
	return c
}

func set(k string, v any) func(map[string]any) { return func(c map[string]any) { c[k] = v } }
func del(k string) func(map[string]any)        { return func(c map[string]any) { delete(c, k) } }

// mintRS256 signs claims with s using RS256 and the given extra header values.
func mintRS256(t *testing.T, s signer, claims map[string]any) string {
	t.Helper()
	input := jsonB64(t, map[string]any{"alg": "RS256", "typ": "JWT", "kid": s.kid}) + "." + jsonB64(t, claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + b64(sig)
}

func mintRS512(t *testing.T, s signer, claims map[string]any) string {
	t.Helper()
	input := jsonB64(t, map[string]any{"alg": "RS512", "typ": "JWT", "kid": s.kid}) + "." + jsonB64(t, claims)
	sum := sha512.Sum512([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA512, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + b64(sig)
}

// mintHS256 is the classic key-confusion attack: an HMAC keyed with the public modulus bytes.
func mintHS256(t *testing.T, s signer, claims map[string]any) string {
	t.Helper()
	input := jsonB64(t, map[string]any{"alg": "HS256", "typ": "JWT", "kid": s.kid}) + "." + jsonB64(t, claims)
	mac := hmac.New(sha256.New, s.key.N.Bytes())
	mac.Write([]byte(input))
	return input + "." + b64(mac.Sum(nil))
}

func mintNone(t *testing.T, s signer, claims map[string]any) string {
	t.Helper()
	return jsonB64(t, map[string]any{"alg": "none", "typ": "JWT", "kid": s.kid}) + "." + jsonB64(t, claims) + "."
}

func bearer(token string) string { return "Bearer " + token }

// ---- harness --------------------------------------------------------------------------------

// testNow is a tiny settable clock so this file does not depend on the clock package.
type testNow struct {
	mu sync.Mutex
	t  time.Time
}

func (n *testNow) Now() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.t
}

func (n *testNow) Advance(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.t = n.t.Add(d)
}

type harness struct {
	t      *testing.T
	srv    *httptest.Server
	clock  *testNow
	hits   atomic.Int32
	mu     sync.Mutex
	pub    []signer
	status int
	auth   Authenticator
}

type harnessOpt func(h *harness) []OIDCOption

func newHarness(t *testing.T, opts ...harnessOpt) *harness {
	t.Helper()
	a, _ := keys(t)
	h := &harness{t: t, clock: &testNow{t: t0}, pub: []signer{a}, status: http.StatusOK}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.status != http.StatusOK {
			http.Error(w, "boom", h.status)
			return
		}
		jwks := make([]map[string]any, 0, len(h.pub))
		for _, s := range h.pub {
			jwks = append(jwks, s.jwk())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": jwks})
	}))
	t.Cleanup(h.srv.Close)

	oidcOpts := []OIDCOption{WithJWKSURL(h.srv.URL)}
	for _, o := range opts {
		oidcOpts = append(oidcOpts, o(h)...)
	}
	h.auth = NewOIDC(t.Context(), testAudience, testServiceAccount, h.clock.Now, oidcOpts...)
	return h
}

func (h *harness) publish(s ...signer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pub = s
}

func (h *harness) check(header string) error {
	h.t.Helper()
	return h.auth.Authenticate(h.t.Context(), header)
}

// assertNoLeak fails if the error text contains the token or any long segment of it.
func assertNoLeak(t *testing.T, err error, header string) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	token := strings.TrimPrefix(header, "Bearer ")
	if len(token) >= 12 && strings.Contains(msg, token) {
		t.Errorf("error leaks the credential: %q", msg)
	}
	for _, part := range strings.Split(token, ".") {
		if len(part) >= 20 && strings.Contains(msg, part) {
			t.Errorf("error leaks a token segment: %q", msg)
		}
	}
}

// ---- tests ----------------------------------------------------------------------------------

func TestOIDC_ValidTokenAccepted(t *testing.T) {
	a, _ := keys(t)
	tests := []struct {
		name  string
		token func(t *testing.T) string
	}{
		{"issuer with scheme", func(t *testing.T) string { return mintRS256(t, a, claimsWith()) }},
		{"issuer without scheme", func(t *testing.T) string {
			return mintRS256(t, a, claimsWith(set("iss", "accounts.google.com")))
		}},
		{"extra claims are ignored", func(t *testing.T) string {
			return mintRS256(t, a, claimsWith(set("hd", "example.com"), set("nonce", "x")))
		}},
		{"no iat claim", func(t *testing.T) string { return mintRS256(t, a, claimsWith(del("iat"))) }},
		{"last second before expiry", func(t *testing.T) string {
			return mintRS256(t, a, claimsWith(set("exp", t0.Add(time.Second).Unix())))
		}},
	}
	h := newHarness(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := h.check(bearer(tt.token(t))); err != nil {
				t.Fatalf("valid token rejected: %v", err)
			}
		})
	}
}

// Every rejection before the email check is ErrUnauthenticated (US2.1, US2.2).
func TestOIDC_RejectsInvalidCredentialsAsUnauthenticated(t *testing.T) {
	a, b := keys(t)
	valid := mintRS256(t, a, claimsWith())
	parts := strings.Split(valid, ".")
	tests := []struct {
		name   string
		header string
	}{
		// header problems
		{"missing header", ""},
		{"scheme only", "Bearer"},
		{"scheme and space only", "Bearer "},
		{"basic scheme", "Basic " + valid},
		{"token without scheme", valid},
		// malformed tokens
		{"not a jwt", bearer("not-a-jwt")},
		{"two segments", bearer(parts[0] + "." + parts[1])},
		{"garbage segments", bearer("a.b.c")},
		{"empty segments", bearer("..")},
		{"four segments", bearer(valid + ".extra")},
		{"header is not json", bearer(b64([]byte("{nope")) + "." + parts[1] + "." + parts[2])},
		// signature problems
		{"signed by an unpublished key with a published kid", bearer(mintRS256(t, signer{kid: a.kid, key: b.key}, claimsWith()))},
		{"signed by an unpublished key with an unknown kid", bearer(mintRS256(t, b, claimsWith()))},
		{"payload tampered after signing", bearer(parts[0] + "." + jsonB64(t, claimsWith(set("email", "attacker@example.com"))) + "." + parts[2])},
		{"signature stripped", bearer(parts[0] + "." + parts[1] + ".")},
		{"signature corrupted", bearer(parts[0] + "." + parts[1] + "." + b64([]byte(strings.Repeat("x", 256))))},
		// algorithm problems (RS256 only)
		{"alg none", bearer(mintNone(t, a, claimsWith()))},
		{"alg HS256 keyed with the public modulus", bearer(mintHS256(t, a, claimsWith()))},
		{"alg RS512 with a published key", bearer(mintRS512(t, a, claimsWith()))},
		// claim problems
		{"expired a minute ago", bearer(mintRS256(t, a, claimsWith(set("exp", t0.Add(-time.Minute).Unix()))))},
		{"expired long ago", bearer(mintRS256(t, a, claimsWith(set("exp", t0.Add(-48*time.Hour).Unix()))))},
		{"no exp claim", bearer(mintRS256(t, a, claimsWith(del("exp"))))},
		{"wrong audience", bearer(mintRS256(t, a, claimsWith(set("aud", "https://other-service.run.app"))))},
		{"audience is a prefix of ours", bearer(mintRS256(t, a, claimsWith(set("aud", testAudience[:len(testAudience)-3]))))},
		{"no audience", bearer(mintRS256(t, a, claimsWith(del("aud"))))},
		{"wrong issuer", bearer(mintRS256(t, a, claimsWith(set("iss", "https://evil.example.com"))))},
		{"issuer that merely starts with google", bearer(mintRS256(t, a, claimsWith(set("iss", "https://accounts.google.com.evil.example"))))},
		{"no issuer", bearer(mintRS256(t, a, claimsWith(del("iss"))))},
	}
	h := newHarness(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.check(tt.header)
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("got %v, want ErrUnauthenticated", err)
			}
			if errors.Is(err, ErrForbidden) {
				t.Fatalf("got %v, must not also be ErrForbidden", err)
			}
			assertNoLeak(t, err, tt.header)
		})
	}
}

// US2.3: valid credentials of another identity are forbidden, not unauthenticated.
func TestOIDC_ValidTokenForAnotherIdentityIsForbidden(t *testing.T) {
	a, _ := keys(t)
	tests := []struct {
		name   string
		claims map[string]any
	}{
		{"different service account", claimsWith(set("email", "someone-else@divya-test.iam.gserviceaccount.com"))},
		{"a human at the same domain", claimsWith(set("email", "owner@example.com"))},
		{"email is a superstring of ours", claimsWith(set("email", "x"+testServiceAccount))},
		{"email is a substring of ours", claimsWith(set("email", testServiceAccount[1:]))},
		{"empty email", claimsWith(set("email", ""))},
		{"no email claim", claimsWith(del("email"))},
		{"email_verified false", claimsWith(set("email_verified", false))},
		{"email_verified missing", claimsWith(del("email_verified"))},
	}
	h := newHarness(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := bearer(mintRS256(t, a, tt.claims))
			err := h.check(header)
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("got %v, want ErrForbidden", err)
			}
			if errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("got %v, must not also be ErrUnauthenticated", err)
			}
			assertNoLeak(t, err, header)
		})
	}
}

// A token that fails an earlier check is 401 even when its email is also wrong: the identity is
// only examined for tokens that are otherwise valid.
func TestOIDC_EarlierFailureWinsOverWrongEmail(t *testing.T) {
	a, _ := keys(t)
	h := newHarness(t)
	token := mintRS256(t, a, claimsWith(set("email", "someone-else@example.com"), set("aud", "https://other.run.app")))
	if err := h.check(bearer(token)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("got %v, want ErrUnauthenticated", err)
	}
}

// Expiry is judged against the injected clock, not the machine clock.
func TestOIDC_ExpiryUsesTheInjectedClock(t *testing.T) {
	a, _ := keys(t)
	h := newHarness(t)
	token := bearer(mintRS256(t, a, claimsWith())) // expires t0 + 1h

	if err := h.check(token); err != nil {
		t.Fatalf("valid at t0: %v", err)
	}
	h.clock.Advance(59 * time.Minute)
	if err := h.check(token); err != nil {
		t.Fatalf("still valid at t0+59m: %v", err)
	}
	h.clock.Advance(2 * time.Minute) // t0 + 61m
	if err := h.check(token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("at t0+61m got %v, want ErrUnauthenticated (expired)", err)
	}
}

// Real wall-clock time (2020s) is long past a token that expires in Nov 2026 +1h only if the
// machine clock were used; a clock set far in the past must make a "long expired" token valid.
func TestOIDC_ClockIsNotTheMachineClock(t *testing.T) {
	a, _ := keys(t)
	h := newHarness(t)
	h.clock.t = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	token := bearer(mintRS256(t, a, claimsWith(
		set("iat", time.Date(2019, time.December, 31, 23, 0, 0, 0, time.UTC).Unix()),
		set("exp", time.Date(2020, time.January, 1, 1, 0, 0, 0, time.UTC).Unix()),
	)))
	if err := h.check(token); err != nil {
		t.Fatalf("token valid at the injected time rejected: %v", err)
	}
}

func TestOIDC_KeyRotationIsPickedUp(t *testing.T) {
	a, b := keys(t)
	h := newHarness(t)
	if err := h.check(bearer(mintRS256(t, a, claimsWith()))); err != nil {
		t.Fatalf("token from published key A: %v", err)
	}
	tokenB := bearer(mintRS256(t, b, claimsWith()))
	if err := h.check(tokenB); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("token from unpublished key B: got %v, want ErrUnauthenticated", err)
	}
	h.publish(a, b) // Google rotates: B appears in the JWKS
	if err := h.check(tokenB); err != nil {
		t.Fatalf("token from key B after it was published: %v", err)
	}
	h.publish(b) // A is retired
	if err := h.check(bearer(mintRS256(t, b, claimsWith()))); err != nil {
		t.Fatalf("token from key B after A was retired: %v", err)
	}
}

func TestOIDC_JWKSEndpointFailureFailsClosed(t *testing.T) {
	a, _ := keys(t)
	h := newHarness(t)
	h.mu.Lock()
	h.status = http.StatusInternalServerError
	h.mu.Unlock()
	err := h.check(bearer(mintRS256(t, a, claimsWith())))
	if err == nil {
		t.Fatal("token accepted although the key set could not be fetched")
	}
	if errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v; an unverifiable token is not a verified wrong identity", err)
	}
}

func TestOIDC_RejectsWithoutContactingTheKeySetForTrivialFailures(t *testing.T) {
	h := newHarness(t)
	for _, header := range []string{"", "Bearer", "Bearer ", "Basic abc", "Bearer not-a-jwt"} {
		_ = h.check(header)
	}
	if n := h.hits.Load(); n != 0 {
		t.Fatalf("the key set was fetched %d times for credentials that cannot be tokens", n)
	}
}

type countingTransport struct {
	n    atomic.Int32
	next http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.next.RoundTrip(r)
}

func TestOIDC_WithHTTPClientIsUsedToFetchTheKeySet(t *testing.T) {
	a, _ := keys(t)
	ct := &countingTransport{next: http.DefaultTransport}
	h := newHarness(t, func(*harness) []OIDCOption {
		return []OIDCOption{WithHTTPClient(&http.Client{Transport: ct})}
	})
	if err := h.check(bearer(mintRS256(t, a, claimsWith()))); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if ct.n.Load() == 0 {
		t.Fatal("the injected HTTP client was never used")
	}
}

// Defence in depth: go-oidc skips the audience check for an empty client id, so an empty audience
// must never end up as "accept any audience".
func TestOIDC_EmptyAudienceOrServiceAccountFailsClosed(t *testing.T) {
	a, _ := keys(t)
	srvHarness := newHarness(t)
	tests := []struct {
		name           string
		audience, acct string
		claims         map[string]any
	}{
		{"empty audience, token for some audience", "", testServiceAccount, claimsWith(set("aud", "https://anything.run.app"))},
		{"empty audience, token without audience", "", testServiceAccount, claimsWith(del("aud"))},
		{"empty service account, token with empty email", testAudience, "", claimsWith(set("email", ""))},
		{"empty service account, token without email", testAudience, "", claimsWith(del("email"))},
		{"empty service account, token with an email", testAudience, "", claimsWith()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := NewOIDC(t.Context(), tt.audience, tt.acct, srvHarness.clock.Now, WithJWKSURL(srvHarness.srv.URL))
			if err := auth.Authenticate(context.Background(), bearer(mintRS256(t, a, tt.claims))); err == nil {
				t.Fatal("token accepted by an authenticator without audience or service account")
			}
		})
	}
}

func TestOIDC_ErrorsNeverContainTheToken(t *testing.T) {
	a, b := keys(t)
	h := newHarness(t)
	for _, token := range []string{
		mintRS256(t, b, claimsWith()),
		mintRS256(t, a, claimsWith(set("exp", t0.Add(-time.Hour).Unix()))),
		mintRS256(t, a, claimsWith(set("aud", "https://other.run.app"))),
		mintRS256(t, a, claimsWith(set("email", "other@example.com"))),
		mintHS256(t, a, claimsWith()),
	} {
		assertNoLeak(t, h.check(bearer(token)), bearer(token))
	}
}

func TestOIDC_ConcurrentAuthenticateIsSafe(t *testing.T) {
	a, b := keys(t)
	h := newHarness(t)
	good, bad := bearer(mintRS256(t, a, claimsWith())), bearer(mintRS256(t, b, claimsWith()))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				if err := h.check(good); err != nil {
					t.Errorf("valid token rejected under concurrency: %v", err)
					return
				}
				if err := h.check(bad); !errors.Is(err, ErrUnauthenticated) {
					t.Errorf("bad token got %v under concurrency", err)
					return
				}
			}
		})
	}
	wg.Wait()
}
