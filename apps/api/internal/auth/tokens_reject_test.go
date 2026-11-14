// Tests for access-token rejection (T025; FR-030, FR-035, SC-001, plan P-02).
//
// Uses the Tokens API documented at the top of tokens_test.go (NewSigningKeys, NewTokens, Verify,
// ErrInvalidToken). Tokens are forged here with the standard library only, so the tests do not
// depend on how the implementation builds them: every Verify failure must wrap ErrInvalidToken,
// never panic, and never echo the token in the error text.
package auth_test

import (
	"crypto"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

const fgKID = "k1"

var (
	fgNow     = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	fgSubject = "0190c1a2-0000-7000-8000-00000000a001"
	fgSession = "0190c1a2-0000-7000-8000-00000000b002"
	fgOrg     = "0190c1a2-0000-7000-8000-00000000c003"
)

func fgSeed() []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(0x40 + i)
	}
	return s
}

func fgPriv() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(fgSeed()) }

func fgAttacker() ed25519.PrivateKey {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(0xE0 - i)
	}
	return ed25519.NewKeyFromSeed(s)
}

func fgVerifier(t testing.TB, now time.Time) *auth.Tokens {
	t.Helper()
	keys, err := auth.NewSigningKeys(fgKID, map[string][]byte{fgKID: fgSeed()})
	if err != nil {
		t.Fatal(err)
	}
	return auth.NewTokens(keys, func() time.Time { return now })
}

func fgB64(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func fgHeader() map[string]any {
	return map[string]any{"alg": "EdDSA", "typ": "at+jwt", "kid": fgKID}
}

func fgClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": "swastik-api", "aud": []string{"swastik-api"},
		"sub": fgSubject, "sid": fgSession, "org": fgOrg, "role": "client",
		"iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(), "jti": "AAAAAAAAAAAAAAAAAAAAAA",
	}
}

func fgSignEd(header, claims map[string]any, priv ed25519.PrivateKey) string {
	in := fgB64(header) + "." + fgB64(claims)
	return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(in)))
}

func fgSignHMAC(header, claims map[string]any, key []byte, h crypto.Hash) string {
	in := fgB64(header) + "." + fgB64(claims)
	var mac []byte
	switch h {
	case crypto.SHA512:
		m := hmac.New(sha512.New, key)
		m.Write([]byte(in))
		mac = m.Sum(nil)
	default:
		m := hmac.New(sha256.New, key)
		m.Write([]byte(in))
		mac = m.Sum(nil)
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(mac)
}

var (
	fgRSAOnce sync.Once
	fgRSAKey  *rsa.PrivateKey
)

func fgRSA(t testing.TB) *rsa.PrivateKey {
	fgRSAOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		fgRSAKey = k
	})
	return fgRSAKey
}

func fgSignRS256(t testing.TB, header, claims map[string]any) string {
	in := fgB64(header) + "." + fgB64(claims)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, fgRSA(t), crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func fgSet(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func fgDel(m map[string]any, keys ...string) map[string]any {
	out := fgSet(m)
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

func fgJWK(pub ed25519.PublicKey) map[string]any {
	return map[string]any{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub)}
}

func TestTokensReject_ForgedBaselineIsAccepted(t *testing.T) {
	// Guards the forging helpers: a token built exactly as the spec describes must verify.
	raw := fgSignEd(fgHeader(), fgClaims(fgNow), fgPriv())
	c, err := fgVerifier(t, fgNow).Verify(raw)
	if err != nil {
		t.Fatalf("baseline forged token rejected: %v", err)
	}
	if c.UserID.String() != fgSubject || c.SessionID.String() != fgSession || c.OrgID.String() != fgOrg || c.Role != "client" {
		t.Errorf("claims = %+v", c)
	}
}

func TestTokensReject_Table(t *testing.T) {
	good := func() (map[string]any, map[string]any) { return fgHeader(), fgClaims(fgNow) }
	ed := func(h, c map[string]any) string { return fgSignEd(h, c, fgPriv()) }
	attacker := fgAttacker()

	valid := ed(good())
	segs := strings.Split(valid, ".")
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	sigBytes, _ := base64.RawURLEncoding.DecodeString(segs[2])
	flippedSig := append([]byte(nil), sigBytes...)
	flippedSig[10] ^= 0x01
	tamperedClaims := fgSet(fgClaims(fgNow), "role", "admin")

	general := func(h, c map[string]any, sigs ...string) string {
		type sig struct {
			Protected string `json:"protected"`
			Signature string `json:"signature"`
		}
		var out []sig
		for _, s := range sigs {
			out = append(out, sig{Protected: fgB64(h), Signature: s})
		}
		b, _ := json.Marshal(map[string]any{"payload": fgB64(c), "signatures": out})
		return string(b)
	}

	h0, c0 := good()
	hmacPub := func(alg string) string {
		pub := fgPriv().Public().(ed25519.PublicKey)
		return fgSignHMAC(fgSet(h0, "alg", alg), c0, pub, crypto.SHA256)
	}

	cases := []struct{ name, raw string }{
		// ---- algorithm confusion ----
		{"alg none, empty signature", fgB64(fgSet(h0, "alg", "none")) + "." + fgB64(c0) + "."},
		{"alg none, no trailing dot", fgB64(fgSet(h0, "alg", "none")) + "." + fgB64(c0)},
		{"alg none, with a signature part", fgB64(fgSet(h0, "alg", "none")) + "." + fgB64(c0) + "." + segs[2]},
		{"alg None capitalised", fgB64(fgSet(h0, "alg", "None")) + "." + fgB64(c0) + "."},
		{"alg missing", ed(fgDel(h0, "alg"), c0)},
		{"alg empty", ed(fgSet(h0, "alg", ""), c0)},
		{"HS256 keyed with the Ed25519 public key", hmacPub("HS256")},
		{"HS256 keyed with the seed", fgSignHMAC(fgSet(h0, "alg", "HS256"), c0, fgSeed(), crypto.SHA256)},
		{"HS512 keyed with the public key", fgSignHMAC(fgSet(h0, "alg", "HS512"), c0, fgPriv().Public().(ed25519.PublicKey), crypto.SHA512)},
		{"RS256 with an RSA key", fgSignRS256(t, fgSet(h0, "alg", "RS256"), c0)},
		{"RS256 header but EdDSA signature", ed(fgSet(h0, "alg", "RS256"), c0)},
		{"ES256 header but EdDSA signature", ed(fgSet(h0, "alg", "ES256"), c0)},

		// ---- key selection ----
		{"unknown kid", ed(fgSet(h0, "kid", "k9"), c0)},
		{"missing kid", ed(fgDel(h0, "kid"), c0)},
		{"empty kid", ed(fgSet(h0, "kid", ""), c0)},
		{"right kid, attacker's signature", fgSignEd(h0, c0, attacker)},
		{"embedded jwk, attacker's key, no kid", fgSignEd(fgSet(fgDel(h0, "kid"), "jwk", fgJWK(attacker.Public().(ed25519.PublicKey))), c0, attacker)},
		{"embedded jwk, attacker's key, known kid", fgSignEd(fgSet(h0, "jwk", fgJWK(attacker.Public().(ed25519.PublicKey))), c0, attacker)},
		{"embedded jwk, attacker's key, unknown kid", fgSignEd(fgSet(h0, "kid", "evil", "jwk", fgJWK(attacker.Public().(ed25519.PublicKey))), c0, attacker)},
		{"jku pointing at the attacker", fgSignEd(fgSet(h0, "kid", "evil", "jku", "https://evil.example/keys.json"), c0, attacker)},
		{"x5u pointing at the attacker", fgSignEd(fgSet(h0, "kid", "evil", "x5u", "https://evil.example/cert.pem"), c0, attacker)},
		{"jku with the right kid but the attacker's signature", fgSignEd(fgSet(h0, "jku", "https://evil.example/keys.json"), c0, attacker)},

		// ---- typ ----
		{"typ missing", ed(fgDel(h0, "typ"), c0)},
		{"typ JWT", ed(fgSet(h0, "typ", "JWT"), c0)},
		{"typ empty", ed(fgSet(h0, "typ", ""), c0)},
		{"typ of another token class", ed(fgSet(h0, "typ", "dmc+jwt"), c0)},

		// ---- claims ----
		{"exp missing", ed(h0, fgDel(c0, "exp"))},
		{"iat missing", ed(h0, fgDel(c0, "iat"))},
		{"iss missing", ed(h0, fgDel(c0, "iss"))},
		{"iss wrong", ed(h0, fgSet(c0, "iss", "someone-else"))},
		{"iss different case", ed(h0, fgSet(c0, "iss", "Swastik-API"))},
		{"aud missing", ed(h0, fgDel(c0, "aud"))},
		{"aud wrong", ed(h0, fgSet(c0, "aud", []string{"other-service"}))},
		{"aud wrong string", ed(h0, fgSet(c0, "aud", "other-service"))},
		{"aud empty", ed(h0, fgSet(c0, "aud", []string{}))},
		{"exp as a string", ed(h0, fgSet(c0, "exp", "1900000000"))},
		{"sub not a uuid", ed(h0, fgSet(c0, "sub", "not-a-uuid"))},
		{"sub missing", ed(h0, fgDel(c0, "sub"))},
		{"sid missing", ed(h0, fgDel(c0, "sid"))},
		{"org missing", ed(h0, fgDel(c0, "org"))},
		{"role missing", ed(h0, fgDel(c0, "role"))},

		// ---- time (clock is fixed at fgNow) ----
		{"expired a second ago", ed(h0, fgSet(c0, "iat", fgNow.Add(-time.Hour).Unix(), "exp", fgNow.Add(-time.Second).Unix()))},
		{"expired an hour ago", ed(h0, fgSet(c0, "iat", fgNow.Add(-2*time.Hour).Unix(), "exp", fgNow.Add(-time.Hour).Unix()))},
		{"expires exactly now (go-jose would accept)", ed(h0, fgSet(c0, "iat", fgNow.Add(-time.Minute).Unix(), "exp", fgNow.Unix()))},
		{"issued an hour in the future", ed(h0, fgSet(c0, "iat", fgNow.Add(time.Hour).Unix(), "exp", fgNow.Add(2*time.Hour).Unix()))},
		{"issued a day in the future", ed(h0, fgSet(c0, "iat", fgNow.Add(24*time.Hour).Unix(), "exp", fgNow.Add(25*time.Hour).Unix()))},

		// ---- tampering ----
		{"payload replaced, signature kept", segs[0] + "." + fgB64(tamperedClaims) + "." + segs[2]},
		{"payload one character flipped", segs[0] + "." + flip(segs[1], 20) + "." + segs[2]},
		{"header one character flipped", flip(segs[0], 5) + "." + segs[1] + "." + segs[2]},
		{"signature first character flipped", segs[0] + "." + segs[1] + "." + flip(segs[2], 0)},
		{"signature bit flipped", segs[0] + "." + segs[1] + "." + base64.RawURLEncoding.EncodeToString(flippedSig)},
		{"signature truncated", segs[0] + "." + segs[1] + "." + segs[2][:len(segs[2])-4]},
		{"signature empty", segs[0] + "." + segs[1] + "."},
		{"signature replaced by zeros", segs[0] + "." + segs[1] + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 64))},
		{"signature from another message", segs[0] + "." + segs[1] + "." + strings.Split(ed(h0, fgSet(c0, "jti", "different")), ".")[2]},

		// ---- shape ----
		{"empty string", ""},
		{"one segment", segs[0]},
		{"two segments", segs[0] + "." + segs[1]},
		{"four segments", valid + ".extra"},
		{"five segments (JWE shape)", valid + ".a.b"},
		{"garbage", "not.a.jwt"},
		{"all dots", "..."},
		{"leading whitespace", " " + valid},
		{"trailing whitespace", valid + " "},
		{"Bearer prefix left on", "Bearer " + valid},
		{"refresh token", "drt_" + strings.Repeat("A", 43)},
		{"mfa challenge token", "dmc_" + strings.Repeat("B", 43)},
		{"non-base64 segments", "!!!.@@@.###"},
		{"standard base64 with padding in a segment", segs[0] + "==." + segs[1] + "." + segs[2]},
		{"json serialisation, two signatures", general(h0, c0, segs[2], segs[2])},
		{"json serialisation, no signatures", general(h0, c0)},
		{"huge token", strings.Repeat("A", 200_000)},
		{"huge payload", segs[0] + "." + strings.Repeat("A", 200_000) + "." + segs[2]},
	}
	v := fgVerifier(t, fgNow)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := v.Verify(tc.raw)
			if err == nil {
				t.Fatalf("Verify accepted the token; claims = %+v", got)
			}
			if !errors.Is(err, auth.ErrInvalidToken) {
				t.Errorf("error %v does not wrap ErrInvalidToken", err)
			}
			if len(tc.raw) > 20 && len(tc.raw) < 5000 && strings.Contains(err.Error(), tc.raw) {
				t.Errorf("error text echoes the token: %v", err)
			}
		})
	}
}

func TestTokensReject_ExactlyAtExpiryBoundary(t *testing.T) {
	// iat = now-10m, exp = now+1s: valid. Advance the clock to exp: rejected (go-jose alone accepts at exp).
	exp := fgNow.Add(time.Second)
	raw := fgSignEd(fgHeader(), fgSet(fgClaims(fgNow), "iat", fgNow.Add(-10*time.Minute).Unix(), "exp", exp.Unix()), fgPriv())
	if _, err := fgVerifier(t, exp.Add(-time.Nanosecond)).Verify(raw); err != nil {
		t.Errorf("rejected a nanosecond before exp: %v", err)
	}
	if _, err := fgVerifier(t, exp.Add(-time.Second)).Verify(raw); err != nil {
		t.Errorf("rejected a second before exp: %v", err)
	}
	for _, now := range []time.Time{exp, exp.Add(time.Nanosecond), exp.Add(time.Second)} {
		if _, err := fgVerifier(t, now).Verify(raw); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("at exp%+v: err = %v, want ErrInvalidToken (US-2 s5: expired at exactly exp)", now.Sub(exp), err)
		}
	}
}

func TestTokensReject_ZeroExpectedTimeCannotFallBackToTheWallClock(t *testing.T) {
	// go-jose validates against time.Now() when Expected.Time is zero. These cases are chosen so that
	// a verifier that quietly uses the wall clock (real time is between 2001 and 2090) gets every
	// one of them wrong.
	issue := func(iat time.Time) string {
		return fgSignEd(fgHeader(), fgSet(fgClaims(iat)), fgPriv())
	}
	past := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)

	// Valid on the injected clock although long expired on the wall clock.
	if _, err := fgVerifier(t, past.Add(5*time.Minute)).Verify(issue(past)); err != nil {
		t.Errorf("a token that is valid at the injected (2001) time was rejected: %v", err)
	}
	// Valid on the injected clock although issued "in the future" on the wall clock.
	if _, err := fgVerifier(t, future.Add(5*time.Minute)).Verify(issue(future)); err != nil {
		t.Errorf("a token that is valid at the injected (2090) time was rejected: %v", err)
	}
	// Expired on the injected clock although still unexpired on the wall clock.
	if _, err := fgVerifier(t, future.Add(2*time.Hour)).Verify(issue(future)); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a token expired at the injected time was accepted: %v", err)
	}
	// Not yet issued on the injected clock although long past on the wall clock.
	if _, err := fgVerifier(t, past.Add(-time.Hour)).Verify(issue(past)); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a token issued after the injected time was accepted: %v", err)
	}
}

func TestTokensReject_VerifyReadsTheClockOnEveryCall(t *testing.T) {
	keys, _ := auth.NewSigningKeys(fgKID, map[string][]byte{fgKID: fgSeed()})
	clock := &tkClock{now: fgNow}
	v := auth.NewTokens(keys, clock.Now)
	raw := fgSignEd(fgHeader(), fgClaims(fgNow), fgPriv())
	if _, err := v.Verify(raw); err != nil {
		t.Fatal(err)
	}
	clock.now = fgNow.Add(15 * time.Minute)
	if _, err := v.Verify(raw); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("the verifier cached the time: err = %v", err)
	}
	clock.now = fgNow
	if _, err := v.Verify(raw); err != nil {
		t.Errorf("rewinding the clock did not restore validity: %v", err)
	}
}

func TestTokensReject_OtherTokensIssuedByTheServiceDoNotCrossOver(t *testing.T) {
	// A token whose signature is valid but whose key id belongs to a ring that is not configured here.
	other, err := auth.NewSigningKeys("other", map[string][]byte{"other": fgAttacker().Seed()})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := auth.NewTokens(other, func() time.Time { return fgNow }).Issue(auth.AccessSubject{
		UserID: mustUUID(fgSubject), SessionID: mustUUID(fgSession), OrgID: mustUUID(fgOrg), Role: "client",
	}, fgNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fgVerifier(t, fgNow).Verify(raw); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a token from another deployment's ring verified: %v", err)
	}
}

func TestTokensReject_PropertyArbitraryInputNeverVerifiesOrPanics(t *testing.T) {
	v := fgVerifier(t, fgNow)
	b64 := rapid.StringOfN(rapid.SampledFrom([]rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_")), 0, 90, -1)
	rapid.Check(t, func(t *rapid.T) {
		var raw string
		switch rapid.IntRange(0, 2).Draw(t, "shape") {
		case 0:
			raw = rapid.String().Draw(t, "any")
		case 1:
			raw = b64.Draw(t, "h") + "." + b64.Draw(t, "p") + "." + b64.Draw(t, "s")
		default:
			valid := fgSignEd(fgHeader(), fgClaims(fgNow), fgPriv())
			i := rapid.IntRange(0, len(valid)-1).Draw(t, "index")
			// The last character of a base64url segment carries unused bits; changing only those can
			// decode to the same bytes, so such positions are not a tamper.
			if i == len(valid)-1 || valid[i+1] == '.' {
				return
			}
			bs := []byte(valid)
			bs[i] = rapid.SampledFrom([]byte("AZaz09-_.=!")).Draw(t, "byte")
			raw = string(bs)
			if raw == valid {
				return
			}
		}
		if _, err := v.Verify(raw); err == nil {
			t.Fatalf("Verify accepted %q", raw)
		}
	})
}

func mustUUID(s string) uuid.UUID { return uuid.MustParse(s) }
