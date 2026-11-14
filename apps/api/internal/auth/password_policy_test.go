// Tests for the password policy (T018; FR-011, D-07, US-7 s3-6, plan P-06).
//
// API assumptions (contracts name only PolicyError{Code string}):
//
//	func CheckPolicy(raw string, identifiers ...string) error
//	    identifiers are the user's stored (already normalised) email and mobile; empty strings are ignored.
//	    nil when the password is acceptable, otherwise *PolicyError for the FIRST failing rule.
//	func NormalizePassword(raw string) string   // Unicode NFC; what the service hashes and checks
//	const PolicyTooShort, PolicyTooLong, PolicyMatchesIdentifier, PolicyTooCommon, PolicyUnchanged string
//	    // "auth.password_too_short", "auth.password_too_long", "auth.password_matches_identifier",
//	    // "auth.password_too_common", "auth.password_unchanged"
//	func (e *PolicyError) Error() string        // mentions the code, never the password
//
// "unchanged" is not decided by CheckPolicy (it needs the stored hash, plan P-06); only its code
// constant and error shape are pinned here.
package auth_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

const (
	policyEmail  = "asha.shah@example.com"
	policyMobile = "+919876543210"
)

func policyCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var pe *auth.PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("error %T (%v) is not a *auth.PolicyError", err, err)
	}
	return pe.Code
}

func TestPolicy_CodesAreStable(t *testing.T) {
	for got, want := range map[string]string{
		auth.PolicyTooShort:          "auth.password_too_short",
		auth.PolicyTooLong:           "auth.password_too_long",
		auth.PolicyMatchesIdentifier: "auth.password_matches_identifier",
		auth.PolicyTooCommon:         "auth.password_too_common",
		auth.PolicyUnchanged:         "auth.password_unchanged",
	} {
		if got != want {
			t.Errorf("code constant = %q, want %q", got, want)
		}
	}
}

func TestPolicy_ErrorMentionsCodeNotPassword(t *testing.T) {
	const pw = "short-pw"
	err := auth.CheckPolicy(pw)
	if err == nil {
		t.Fatal("expected a policy error")
	}
	if !strings.Contains(err.Error(), auth.PolicyTooShort) {
		t.Errorf("Error() = %q, want it to contain the code", err.Error())
	}
	if strings.Contains(err.Error(), pw) {
		t.Errorf("Error() echoes the password: %q", err.Error())
	}
	e := &auth.PolicyError{Code: auth.PolicyUnchanged}
	if !strings.Contains(e.Error(), auth.PolicyUnchanged) {
		t.Errorf("PolicyError{unchanged}.Error() = %q", e.Error())
	}
}

func TestPolicy_Table(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want string // "" = accepted
	}{
		// length: US-7 s3 (too short), s6 (too long), D-07 (10..256 accepted)
		{"empty", "", auth.PolicyTooShort},
		{"nine ascii", "abcdefghi", auth.PolicyTooShort},
		{"exactly ten", "kT9#vL2$qZ", ""},
		{"eleven", "kT9#vL2$qZw", ""},
		{"passphrase", "correct horse battery staple", ""},
		{"exactly 256", strings.Repeat("ab", 128), ""},
		{"257", strings.Repeat("ab", 128) + "c", auth.PolicyTooLong},
		{"1000", strings.Repeat("xy", 500), auth.PolicyTooLong},

		// no composition rules: digits only, one letter case, repeated characters, spaces
		{"digits only", "7392018465", ""},
		{"lowercase only", "zxqvbnmklp", ""},
		{"repeated char", "aaaaaaaaaa", ""},
		{"with spaces", "a b c d e f g h", ""},
		{"symbols only", "!@#$%^&*()_+", ""},

		// identifier rules (US-7 s5)
		{"equals email", policyEmail, auth.PolicyMatchesIdentifier},
		{"equals email in capitals", strings.ToUpper(policyEmail), auth.PolicyMatchesIdentifier},
		{"equals email mixed case", "Asha.Shah@Example.com", auth.PolicyMatchesIdentifier},
		{"equals email with padding", "  " + policyEmail + "  ", auth.PolicyMatchesIdentifier},
		{"email plus suffix is fine", policyEmail + "!!", ""},
		{"other email is fine", "asha.shah@example.org", ""},
		{"mobile bare digits", "9876543210", auth.PolicyMatchesIdentifier},
		{"mobile e164", "+919876543210", auth.PolicyMatchesIdentifier},
		{"mobile with prefix 91", "919876543210", auth.PolicyMatchesIdentifier},
		{"mobile with prefix 0", "09876543210", auth.PolicyMatchesIdentifier},
		{"mobile spaced", "+91 98765 43210", auth.PolicyMatchesIdentifier},
		{"mobile hyphenated", "98765-43210", auth.PolicyMatchesIdentifier},
		{"other mobile is fine", "9876543211", ""},
		{"mobile plus extra digit is fine", "98765432101", ""},

		// common list (D-07): case-insensitive, 10k list
		{"common digits", "1234567890", auth.PolicyTooCommon},
		{"common letters", "qwertyuiop", auth.PolicyTooCommon},
		{"common letters upper (US-7 s4)", "QWERTYUIOP", auth.PolicyTooCommon},
		{"common mixed case", "BaskETbalL", auth.PolicyTooCommon},
		{"common alnum", "1q2w3e4r5t", auth.PolicyTooCommon},
		{"common word", "experience", auth.PolicyTooCommon},
		{"common word upper", "EXPERIENCE", auth.PolicyTooCommon},
		{"common word plus suffix is fine", "experience!", ""},

		// order: the first failing rule wins
		{"short beats common", "password", auth.PolicyTooShort},
		{"short beats common 6 digits", "123456", auth.PolicyTooShort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.CheckPolicy(tt.pw, policyEmail, policyMobile)
			if got := policyCode(t, err); got != tt.want {
				t.Errorf("CheckPolicy(%q) code = %q, want %q", tt.pw, got, tt.want)
			}
		})
	}
}

func TestPolicy_NoIdentifiersMeansNoIdentifierRule(t *testing.T) {
	// Admins may have no mobile; empty identifiers must not match anything.
	if err := auth.CheckPolicy("9876543210", policyEmail, ""); err != nil {
		t.Errorf("mobile-shaped password rejected although the user has no mobile: %v", err)
	}
	if err := auth.CheckPolicy("9876543210"); err != nil {
		t.Errorf("CheckPolicy without identifiers = %v, want nil", err)
	}
	if err := auth.CheckPolicy("kT9#vL2$qZ", "", ""); err != nil {
		t.Errorf("empty identifiers matched an ordinary password: %v", err)
	}
}

func TestPolicy_CountsCodePointsNotBytes(t *testing.T) {
	gujarati9 := "ગુજરાતીભા"    // 9 code points, 27 bytes
	gujarati12 := "ગુજરાતીભાષા" // 12 code points
	if utf8.RuneCountInString(gujarati9) != 9 || len(gujarati9) < 10 {
		t.Fatalf("fixture changed: %d runes, %d bytes", utf8.RuneCountInString(gujarati9), len(gujarati9))
	}
	if got := policyCode(t, auth.CheckPolicy(gujarati9)); got != auth.PolicyTooShort {
		t.Errorf("9 Gujarati code points (27 bytes): code = %q, want too_short", got)
	}
	if got := policyCode(t, auth.CheckPolicy(gujarati12)); got != "" {
		t.Errorf("12 Gujarati code points: code = %q, want accepted", got)
	}
	emoji9 := strings.Repeat("😀", 9) // 9 code points, 36 bytes
	if got := policyCode(t, auth.CheckPolicy(emoji9)); got != auth.PolicyTooShort {
		t.Errorf("9 emoji: code = %q, want too_short", got)
	}
	if got := policyCode(t, auth.CheckPolicy(strings.Repeat("😀", 10))); got != "" {
		t.Errorf("10 emoji: code = %q, want accepted", got)
	}
}

func TestPolicy_LongDevanagariAndGujaratiPassphrases(t *testing.T) {
	// US-7 s6 / D-07: at least 64 accepted; 256 code points (up to 768 bytes) accepted; 257 rejected.
	gu64 := strings.Repeat("ગુજરાતી", 9) + "ગ" // 7*9+1 = 64 code points
	if n := utf8.RuneCountInString(gu64); n != 64 {
		t.Fatalf("fixture has %d code points", n)
	}
	if got := policyCode(t, auth.CheckPolicy(gu64)); got != "" {
		t.Errorf("64-code-point Gujarati passphrase: code = %q, want accepted", got)
	}
	hi256 := strings.Repeat("क", 256) // 256 code points, 768 bytes
	if len(hi256) != 768 {
		t.Fatalf("fixture has %d bytes", len(hi256))
	}
	if got := policyCode(t, auth.CheckPolicy(hi256)); got != "" {
		t.Errorf("256 Devanagari code points: code = %q, want accepted", got)
	}
	if got := policyCode(t, auth.CheckPolicy(hi256+"क")); got != auth.PolicyTooLong {
		t.Errorf("257 Devanagari code points: code = %q, want too_long", got)
	}
}

func TestPolicy_NFCEquivalence(t *testing.T) {
	// The same passphrase typed with different input methods must behave identically.
	composed := "ऩऩऩऩऩऩऩऩऩऩ"               // 10 x U+0929
	decomposed := strings.Repeat("ऩ", 10) // 10 x (U+0928 U+093C) => 20 code points before NFC
	if composed == decomposed {
		t.Fatal("fixture must differ before normalisation")
	}
	if auth.NormalizePassword(composed) != auth.NormalizePassword(decomposed) {
		t.Errorf("NormalizePassword differs: %+q vs %+q", auth.NormalizePassword(composed), auth.NormalizePassword(decomposed))
	}
	if got := utf8.RuneCountInString(auth.NormalizePassword(decomposed)); got != 10 {
		t.Errorf("NFC form has %d code points, want 10", got)
	}
	for name, pw := range map[string]string{"composed": composed, "decomposed": decomposed} {
		if got := policyCode(t, auth.CheckPolicy(pw)); got != "" {
			t.Errorf("%s: code = %q, want accepted (10 code points after NFC)", name, got)
		}
	}

	// Length is counted after NFC: 9 composed characters are too short in both spellings.
	for name, pw := range map[string]string{
		"composed":   strings.Repeat("é", 9),
		"decomposed": strings.Repeat("é", 9), // 18 code points before NFC
	} {
		if got := policyCode(t, auth.CheckPolicy(pw)); got != auth.PolicyTooShort {
			t.Errorf("9 x é, %s: code = %q, want too_short (counted after NFC)", name, got)
		}
	}
	// ... and too long only when the NFC form is too long.
	if got := policyCode(t, auth.CheckPolicy(strings.Repeat("é", 200))); got != "" {
		t.Errorf("200 x é decomposed (400 code points before NFC): code = %q, want accepted", got)
	}
	// The common-list lookup also runs on the NFC form.
	if got := policyCode(t, auth.CheckPolicy("experience")); got != auth.PolicyTooCommon {
		t.Errorf("control: code = %q", got)
	}
}

func TestPolicy_NormalizePasswordIsNFCAndIdempotent(t *testing.T) {
	for _, in := range []string{"plain ascii", "é", "ऩ", "ગુજરાતી", "", "😀"} {
		once := auth.NormalizePassword(in)
		if auth.NormalizePassword(once) != once {
			t.Errorf("NormalizePassword is not idempotent for %+q", in)
		}
	}
	if got := auth.NormalizePassword("é"); got != "é" {
		t.Errorf("NormalizePassword(e + combining acute) = %+q, want U+00E9", got)
	}
	// Passwords are never trimmed or case-folded: spaces and case are significant.
	if got := auth.NormalizePassword("  Ab  "); got != "  Ab  " {
		t.Errorf("NormalizePassword altered whitespace or case: %q", got)
	}
}

func TestPolicy_PassphraseWithWhitespaceIsNotTruncatedOrTrimmed(t *testing.T) {
	// Nine visible characters plus padding spaces are ten characters: no trimming before the length rule.
	if got := policyCode(t, auth.CheckPolicy(" abcdefghi")); got != "" {
		t.Errorf("leading space counted? code = %q, want accepted (10 code points)", got)
	}
}
