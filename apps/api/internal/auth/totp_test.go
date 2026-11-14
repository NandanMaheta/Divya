// Tests for admin TOTP (T027; FR-061, US-5 s4-5, plan P-05, RFC 6238 Appendix B).
//
// API assumptions (contracts are silent; the implementer must match these names):
//
//	func GenerateTOTP(accountName string) (secret, uri string, err error)
//	    // 20 random bytes; secret is upper-case base32 WITHOUT padding (32 chars); uri is
//	    // otpauth://totp/Swastik:<accountName>?secret=..&issuer=Swastik&algorithm=SHA1&digits=6&period=30
//	func TOTPStepAt(t time.Time) int64                        // floor(unix seconds / 30)
//	func VerifyTOTP(secret, code string, now time.Time, lastUsedStep int64) (step int64, ok bool)
//	    // N = TOTPStepAt(now); candidate steps N-1, N, N+1; a candidate counts only if step > lastUsedStep;
//	    // returns the matched step (the caller stores it as last_used_step). A malformed secret or a
//	    // code that is not exactly six ASCII digits is simply (0, false). SHA-1, 6 digits, 30 s.
//
// RFC 6238 vectors are the SHA-1 rows of Appendix B (secret "12345678901234567890"); the RFC lists
// 8-digit values, whose last six digits are the 6-digit codes used here. Whether the implementation
// evaluates every candidate before deciding (no early exit) is a timing property and is not
// observable in a unit test.
package auth_test

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 mandates HMAC-SHA-1 for authenticator-app compatibility
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // base32("12345678901234567890")

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// refHOTP is an independent RFC 4226 implementation used only to compute expected codes.
func refHOTP(t testing.TB, secretB32 string, counter int64) string {
	t.Helper()
	key, err := b32.DecodeString(secretB32)
	if err != nil {
		t.Fatalf("test secret is not base32: %v", err)
	}
	mac := hmac.New(sha1.New, key)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", bin%1_000_000)
}

func totpWrong(code string) string { // a six-digit code different from the given one
	n, _ := strconv.Atoi(code)
	return fmt.Sprintf("%06d", (n+1)%1_000_000)
}

func TestTotp_RFC6238AppendixBVectors(t *testing.T) {
	for _, v := range []struct {
		unix int64
		code string // last six digits of the RFC's 8-digit value
	}{
		{59, "287082"},          // 94287082
		{1111111109, "081804"},  // 07081804 (leading zero must survive)
		{1111111111, "050471"},  // 14050471
		{1234567890, "005924"},  // 89005924 (leading zeros)
		{2000000000, "279037"},  // 69279037
		{20000000000, "353130"}, // 65353130
	} {
		t.Run(fmt.Sprint(v.unix), func(t *testing.T) {
			if got := refHOTP(t, rfcSecret, v.unix/30); got != v.code {
				t.Fatalf("test helper is wrong: HOTP = %s, RFC says %s", got, v.code)
			}
			step, ok := auth.VerifyTOTP(rfcSecret, v.code, time.Unix(v.unix, 0).UTC(), 0)
			if !ok || step != v.unix/30 {
				t.Errorf("VerifyTOTP = %d, %v; want %d, true", step, ok, v.unix/30)
			}
		})
	}
}

func TestTotp_StepAt(t *testing.T) {
	for _, tt := range []struct {
		at   time.Time
		want int64
	}{
		{time.Unix(0, 0), 0},
		{time.Unix(29, 999_999_999), 0},
		{time.Unix(30, 0), 1},
		{time.Unix(59, 0), 1},
		{time.Unix(60, 0), 2},
		{time.Unix(1111111111, 0), 37037037},
		{time.Unix(1111111110, 0), 37037037},
		{time.Unix(1111111109, 999_999_999), 37037036},
	} {
		if got := auth.TOTPStepAt(tt.at); got != tt.want {
			t.Errorf("TOTPStepAt(%v) = %d, want %d", tt.at.Unix(), got, tt.want)
		}
	}
}

// stepNow is a time inside step n = 37037037 (US-5 s4 uses "the fake clock in step N").
var stepN = int64(37037037)

func inStep(n int64, offset time.Duration) time.Time {
	return time.Unix(n*30, 0).UTC().Add(offset)
}

func TestTotp_SkewWindowIsOneStepEitherSide(t *testing.T) {
	now := inStep(stepN, 12*time.Second)
	tests := []struct {
		name   string
		step   int64
		wantOK bool
	}{
		{"N-2 rejected (US-5 s4)", stepN - 2, false},
		{"N-1 accepted (US-5 s4)", stepN - 1, true},
		{"N accepted", stepN, true},
		{"N+1 accepted", stepN + 1, true},
		{"N+2 rejected", stepN + 2, false},
		{"N-10 rejected", stepN - 10, false},
		{"N+10 rejected", stepN + 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := refHOTP(t, rfcSecret, tt.step)
			step, ok := auth.VerifyTOTP(rfcSecret, code, now, 0)
			if ok != tt.wantOK {
				t.Fatalf("VerifyTOTP(code for step %d) ok = %v, want %v", tt.step, ok, tt.wantOK)
			}
			if ok && step != tt.step {
				t.Errorf("matched step = %d, want %d", step, tt.step)
			}
			if !ok && step != 0 {
				t.Errorf("a rejected code returned step %d, want 0", step)
			}
		})
	}
}

func TestTotp_StepBoundariesInsideTheClockStep(t *testing.T) {
	for _, off := range []time.Duration{0, time.Nanosecond, 15 * time.Second, 30*time.Second - time.Nanosecond} {
		now := inStep(stepN, off)
		for _, step := range []int64{stepN - 1, stepN, stepN + 1} {
			if _, ok := auth.VerifyTOTP(rfcSecret, refHOTP(t, rfcSecret, step), now, 0); !ok {
				t.Errorf("offset %v: code for step %d rejected", off, step)
			}
		}
		for _, step := range []int64{stepN - 2, stepN + 2} {
			if _, ok := auth.VerifyTOTP(rfcSecret, refHOTP(t, rfcSecret, step), now, 0); ok {
				t.Errorf("offset %v: code for step %d accepted", off, step)
			}
		}
	}
	// One nanosecond before the step boundary the clock is still in N-1, so N+1 is two steps ahead.
	justBefore := inStep(stepN, -time.Nanosecond)
	if _, ok := auth.VerifyTOTP(rfcSecret, refHOTP(t, rfcSecret, stepN+1), justBefore, 0); ok {
		t.Error("the clock is still in step N-1; the code for N+1 must be rejected")
	}
	if step, ok := auth.VerifyTOTP(rfcSecret, refHOTP(t, rfcSecret, stepN), justBefore, 0); !ok || step != stepN {
		t.Errorf("the code for N is the +1 candidate and must be accepted: %d, %v", step, ok)
	}
}

func TestTotp_ReplayGuardRejectsStepsAtOrBeforeLastUsed(t *testing.T) {
	now := inStep(stepN, 5*time.Second)
	codeN, codePrev, codeNext := refHOTP(t, rfcSecret, stepN), refHOTP(t, rfcSecret, stepN-1), refHOTP(t, rfcSecret, stepN+1)

	// US-5 s5: code accepted for step N; the same code again, and then step N-1, are rejected.
	step, ok := auth.VerifyTOTP(rfcSecret, codeN, now, stepN-1)
	if !ok || step != stepN {
		t.Fatalf("first use: %d, %v", step, ok)
	}
	if _, ok := auth.VerifyTOTP(rfcSecret, codeN, now, step); ok {
		t.Error("the same code was accepted twice (replay)")
	}
	if _, ok := auth.VerifyTOTP(rfcSecret, codePrev, now, step); ok {
		t.Error("the code for step N-1 was accepted after step N had been used")
	}
	// A later step is still fine.
	if got, ok := auth.VerifyTOTP(rfcSecret, codeNext, now, step); !ok || got != stepN+1 {
		t.Errorf("the code for N+1 after using N: %d, %v; want %d, true", got, ok, stepN+1)
	}

	tests := []struct {
		name         string
		code         string
		last         int64
		wantOK       bool
		wantStepOnOK int64
	}{
		{"last = N-2: N-1 usable", codePrev, stepN - 2, true, stepN - 1},
		{"last = N-1: N-1 is replay", codePrev, stepN - 1, false, 0},
		{"last = N-1: N usable", codeN, stepN - 1, true, stepN},
		{"last = N: N is replay", codeN, stepN, false, 0},
		{"last = N: N+1 usable", codeNext, stepN, true, stepN + 1},
		{"last = N+1: N+1 is replay", codeNext, stepN + 1, false, 0},
		{"last = N+1: N is replay", codeN, stepN + 1, false, 0},
		{"last far in the future", codeN, stepN + 1000, false, 0},
		{"last = 0 (fresh enrolment)", codeN, 0, true, stepN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := auth.VerifyTOTP(rfcSecret, tt.code, now, tt.last)
			if ok != tt.wantOK || (ok && got != tt.wantStepOnOK) {
				t.Errorf("VerifyTOTP = %d, %v; want %d, %v", got, ok, tt.wantStepOnOK, tt.wantOK)
			}
		})
	}
}

func TestTotp_WrongAndMalformedCodes(t *testing.T) {
	now := inStep(stepN, 5*time.Second)
	right := refHOTP(t, rfcSecret, stepN)
	neighbours := map[string]bool{
		refHOTP(t, rfcSecret, stepN-1): true, refHOTP(t, rfcSecret, stepN): true, refHOTP(t, rfcSecret, stepN+1): true,
	}
	wrong := totpWrong(right)
	for neighbours[wrong] { // never collide with a valid neighbour
		wrong = totpWrong(wrong)
	}
	for name, code := range map[string]string{
		"wrong digits":        wrong,
		"empty":               "",
		"five digits":         right[1:],
		"seven digits":        right + "0",
		"letters":             "abcdef",
		"six spaces":          "      ",
		"sign prefix":         "+" + right[1:],
		"right code plus nul": right + "\x00",
		"hex-looking":         "0x1234",
		"unicode digits":      "１２３４５６",
		"right code in words": "one two three",
	} {
		t.Run(name, func(t *testing.T) {
			if step, ok := auth.VerifyTOTP(rfcSecret, code, now, 0); ok || step != 0 {
				t.Errorf("VerifyTOTP(%q) = %d, %v; want 0, false", code, step, ok)
			}
		})
	}
	if _, ok := auth.VerifyTOTP(rfcSecret, right, now, 0); !ok {
		t.Fatal("control: the right code must verify")
	}
}

func TestTotp_LeadingZerosAreSignificant(t *testing.T) {
	now := time.Unix(1111111109, 0).UTC() // code 081804
	if _, ok := auth.VerifyTOTP(rfcSecret, "081804", now, 0); !ok {
		t.Fatal("081804 rejected")
	}
	if _, ok := auth.VerifyTOTP(rfcSecret, "81804", now, 0); ok {
		t.Error("81804 (leading zero dropped) accepted")
	}
}

func TestTotp_CodeFromAnotherSecretIsRejected(t *testing.T) {
	other, _, err := auth.GenerateTOTP("other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := inStep(stepN, 0)
	if _, ok := auth.VerifyTOTP(other, refHOTP(t, rfcSecret, stepN), now, 0); ok {
		t.Error("a code for a different secret was accepted")
	}
}

func TestTotp_MalformedSecretsNeverVerifyOrPanic(t *testing.T) {
	now := inStep(stepN, 0)
	for name, secret := range map[string]string{
		"empty":            "",
		"not base32":       "!!!!not-base32!!!!",
		"digits 0189":      "0189018901890189",
		"padded":           rfcSecret + "====",
		"too short":        "GEZDGNBV",
		"lowercase junk":   "zzzz",
		"whitespace":       "   ",
		"secret with null": "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQ\x00JQ",
	} {
		t.Run(name, func(t *testing.T) {
			if step, ok := auth.VerifyTOTP(secret, "123456", now, 0); ok || step != 0 {
				t.Errorf("VerifyTOTP with secret %q = %d, %v", secret, step, ok)
			}
		})
	}
}

func TestTotp_GenerateSecretShape(t *testing.T) {
	secret, _, err := auth.GenerateTOTP("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(secret, "=") {
		t.Errorf("secret %q has padding", secret)
	}
	if secret != strings.ToUpper(secret) || len(secret) != 32 {
		t.Errorf("secret %q is not 32 upper-case base32 characters (20 bytes)", secret)
	}
	raw, err := b32.DecodeString(secret)
	if err != nil || len(raw) != 20 {
		t.Fatalf("secret decodes to %d bytes, err %v; want 20 bytes", len(raw), err)
	}
	seen := map[string]bool{secret: true}
	for range 200 {
		s, _, err := auth.GenerateTOTP("admin@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatal("GenerateTOTP repeated a secret")
		}
		seen[s] = true
	}
}

func TestTotp_GeneratedSecretWorksWithStandardAuthenticatorSettings(t *testing.T) {
	secret, _, err := auth.GenerateTOTP("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 10, 0, 7, 0, time.UTC)
	code := refHOTP(t, secret, auth.TOTPStepAt(now)) // an independent SHA-1/6-digit/30-second implementation
	step, ok := auth.VerifyTOTP(secret, code, now, 0)
	if !ok || step != auth.TOTPStepAt(now) {
		t.Errorf("VerifyTOTP with an independently computed code = %d, %v", step, ok)
	}
}

func TestTotp_OtpauthURIShape(t *testing.T) {
	for _, email := range []string{"admin@example.com", "first.last+ops@example.co.in"} {
		t.Run(email, func(t *testing.T) {
			secret, uri, err := auth.GenerateTOTP(email)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(uri)
			if err != nil {
				t.Fatalf("URI does not parse: %v", err)
			}
			if u.Scheme != "otpauth" || u.Host != "totp" {
				t.Errorf("URI = %q, want otpauth://totp/...", uri)
			}
			if want := "/Swastik:" + email; u.Path != want {
				t.Errorf("label path = %q, want %q (issuer prefix and account email)", u.Path, want)
			}
			q := u.Query()
			for k, want := range map[string]string{"secret": secret, "issuer": "Swastik", "algorithm": "SHA1", "digits": "6", "period": "30"} {
				if got := q.Get(k); got != want {
					t.Errorf("query %s = %q, want %q", k, got, want)
				}
			}
		})
	}
}
