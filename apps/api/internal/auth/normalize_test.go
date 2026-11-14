// Tests for identifier normalisation (T014; FR-003, D-16, D-17, US-1 s1-2 and s11).
//
// API assumptions (contracts name only the ErrInvalidIdentifier sentinel):
//
//	type IdentifierKind string; const KindEmail IdentifierKind = "email"; const KindMobile IdentifierKind = "mobile"
//	func NormalizeEmail(raw string) (string, error)
//	func NormalizeMobile(raw string) (string, error)
//	func KindOf(raw string) IdentifierKind                                   // "@" present => email, otherwise mobile
//	func NormalizeIdentifier(raw string) (IdentifierKind, string, error)     // KindOf then the matching normaliser
//
// Every rejection wraps ErrInvalidIdentifier (errors.Is). Grammar (plan P-08): strip spaces and
// hyphens, then accept (+91|91|0)?[6-9][0-9]{9}, ASCII digits only, and store +91 followed by the ten digits.
package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

func TestNormalizeMobile_AcceptedForms(t *testing.T) {
	const want = "+919876543210"
	for _, in := range []string{
		"9876543210",    // D-17 plain
		"09876543210",   // trunk prefix 0
		"919876543210",  // country code without plus
		"+919876543210", // E.164
		"98765 43210",   // spaces
		"98765-43210",   // hyphen
		"+91 98765 43210",
		"+91-98765-43210",
		"+91 9876543210",
		"0 98765 43210",
		"91 98765 43210",
		"  9876543210  ", // surrounding spaces are spaces
		" 9 8 7 6 5 4 3 2 1 0 ",
		"98--76--54--32--10",
	} {
		t.Run(in, func(t *testing.T) {
			got, err := auth.NormalizeMobile(in)
			if err != nil || got != want {
				t.Errorf("NormalizeMobile(%q) = %q, %v; want %q", in, got, err, want)
			}
		})
	}
}

func TestNormalizeMobile_FirstDigitRange(t *testing.T) {
	for d := '0'; d <= '9'; d++ {
		in := string(d) + "876543210"
		got, err := auth.NormalizeMobile(in)
		if d >= '6' {
			if err != nil || got != "+91"+in {
				t.Errorf("NormalizeMobile(%q) = %q, %v; want +91%s", in, got, err, in)
			}
		} else if err == nil {
			t.Errorf("NormalizeMobile(%q) accepted a number starting with %c (must be 6-9)", in, d)
		}
	}
}

func TestNormalizeMobile_Rejections(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                        "",
		"only spaces":                  "   ",
		"nine digits":                  "987654321",
		"eleven digits":                "98765432101",
		"starts with 5":                "5876543210",
		"starts with 0 after prefix 0": "05876543210",
		"starts with 1":                "1234567890",
		"plus 91 then 5":               "+915876543210",
		"91 then 5":                    "915876543210",
		"plus 91 nine digits":          "+91987654321",
		"plus 91 eleven digits":        "+9198765432101",
		"letters":                      "98765abcde",
		"letter inside":                "98765x3210",
		"plus in the middle":           "98765+43210",
		"double plus":                  "++919876543210",
		"other country code":           "+449876543210",
		"+1 US number":                 "+1 9876543210",
		"00 international prefix":      "00919876543210",
		"plus 0 prefix":                "+09876543210",
		"parentheses":                  "(+91) 9876543210",
		"dot separators":               "98765.43210",
		"slash separators":             "98765/43210",
		"devanagari digits":            "९८७६५४३२१०",
		"gujarati digits":              "૯૮૭૬૫૪૩૨૧૦",
		"fullwidth digits":             "９８７６５４３２１０",
		"email-like":                   "a@b.in",
		"newline inside":               "98765\n43210",
		"nul byte":                     "9876543210\x00",
		"just plus 91":                 "+91",
		"sql injection":                "9876543210' OR '1'='1",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := auth.NormalizeMobile(in)
			if err == nil {
				t.Fatalf("NormalizeMobile(%q) = %q, want an error", in, got)
			}
			if !errors.Is(err, auth.ErrInvalidIdentifier) {
				t.Errorf("error %v does not wrap ErrInvalidIdentifier", err)
			}
			if got != "" {
				t.Errorf("a rejected input must return an empty string, got %q", got)
			}
		})
	}
}

func TestNormalizeEmail_Accepted(t *testing.T) {
	tests := []struct{ in, want string }{
		{"user@example.com", "user@example.com"},
		{"  user@example.com  ", "user@example.com"},
		{"User@Example.COM", "user@example.com"},
		{"\tUSER@EXAMPLE.COM\n", "user@example.com"},
		{"first.last+tag@sub.example.co.in", "first.last+tag@sub.example.co.in"},
		{"a@b.in", "a@b.in"},
		{"Priya.Shah@Gmail.com", "priya.shah@gmail.com"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := auth.NormalizeEmail(tt.in)
			if err != nil || got != tt.want {
				t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestNormalizeEmail_SameAddressDifferentSpellingsCollide(t *testing.T) {
	// D-18: one email belongs to at most one user regardless of case and spacing.
	a, _ := auth.NormalizeEmail("Asha@Example.com")
	b, _ := auth.NormalizeEmail("  asha@example.COM ")
	if a == "" || a != b {
		t.Errorf("normalised forms differ: %q vs %q", a, b)
	}
}

func TestNormalizeEmail_Rejections(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                "",
		"only spaces":          "   ",
		"no at sign":           "plainaddress",
		"missing local":        "@example.com",
		"missing domain":       "user@",
		"two at signs":         "a@b@example.com",
		"display name":         "Asha Shah <asha@example.com>",
		"display name quoted":  `"Asha" <asha@example.com>`,
		"address list":         "a@example.com, b@example.com",
		"space in address":     "us er@example.com",
		"trailing dot local":   "user.@example.com",
		"leading dot local":    ".user@example.com",
		"consecutive dots":     "us..er@example.com",
		"control character":    "user@exa\x00mple.com",
		"newline header split": "user@example.com\nBcc: x@y.com",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := auth.NormalizeEmail(in)
			if err == nil {
				t.Fatalf("NormalizeEmail(%q) = %q, want an error", in, got)
			}
			if !errors.Is(err, auth.ErrInvalidIdentifier) {
				t.Errorf("error %v does not wrap ErrInvalidIdentifier", err)
			}
			if got != "" {
				t.Errorf("a rejected input must return an empty string, got %q", got)
			}
		})
	}
}

func TestNormalizeEmail_Length254Limit(t *testing.T) {
	build := func(total int) string {
		const local = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz01" // 64 chars
		rest := total - len(local) - 1 - len(".com")
		return local + "@" + strings.Repeat("b", rest) + ".com"
	}
	at254, at255 := build(254), build(255)
	if len(at254) != 254 || len(at255) != 255 {
		t.Fatalf("test fixture lengths = %d, %d", len(at254), len(at255))
	}
	if got, err := auth.NormalizeEmail(at254); err != nil || got != at254 {
		t.Errorf("254-character address rejected: %v", err)
	}
	if _, err := auth.NormalizeEmail(at255); !errors.Is(err, auth.ErrInvalidIdentifier) {
		t.Errorf("255-character address: err = %v, want ErrInvalidIdentifier", err)
	}
}

func TestKindOf_AtSignDecides(t *testing.T) {
	tests := []struct {
		in   string
		want auth.IdentifierKind
	}{
		{"a@b.in", auth.KindEmail},
		{"@", auth.KindEmail},
		{"not an email but has @ sign", auth.KindEmail},
		{"9876543210", auth.KindMobile},
		{"+91 98765 43210", auth.KindMobile},
		{"garbage", auth.KindMobile},
		{"", auth.KindMobile},
	}
	for _, tt := range tests {
		if got := auth.KindOf(tt.in); got != tt.want {
			t.Errorf("KindOf(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if auth.KindEmail != "email" || auth.KindMobile != "mobile" {
		t.Errorf("kind values must be \"email\" and \"mobile\" (they appear in throttle subjects and audit), got %q and %q", auth.KindEmail, auth.KindMobile)
	}
}

func TestNormalizeIdentifier_RoutesByKind(t *testing.T) {
	tests := []struct {
		in       string
		wantKind auth.IdentifierKind
		want     string
		wantErr  bool
	}{
		{"Asha@Example.com", auth.KindEmail, "asha@example.com", false},
		{"98765 43210", auth.KindMobile, "+919876543210", false},
		{"+919876543210", auth.KindMobile, "+919876543210", false},
		{"asha@", auth.KindEmail, "", true},
		{"12345", auth.KindMobile, "", true},
		{"", auth.KindMobile, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			kind, got, err := auth.NormalizeIdentifier(tt.in)
			if kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", kind, tt.wantKind)
			}
			if tt.wantErr {
				if !errors.Is(err, auth.ErrInvalidIdentifier) || got != "" {
					t.Errorf("got %q, %v; want ErrInvalidIdentifier and empty result", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestNormalize_ResultsNeverEchoRawInputOnError(t *testing.T) {
	// FR-091: an unmatched identifier as typed must never reach logs or responses via the error text.
	_, err := auth.NormalizeMobile("SECRET-typed-value")
	if err == nil || strings.Contains(err.Error(), "SECRET-typed-value") {
		t.Errorf("error missing or echoes the typed value: %v", err)
	}
	_, err = auth.NormalizeEmail("SECRET-typed@@value")
	if err == nil || strings.Contains(err.Error(), "SECRET-typed") {
		t.Errorf("error missing or echoes the typed value: %v", err)
	}
}
