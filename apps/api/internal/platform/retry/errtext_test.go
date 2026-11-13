// Tests for retry.ErrorText (feature 002, FR-018, constitution VI).
//
// Contract (plan.md): ErrorText(err) truncates to 500 and redacts Bearer tokens, JWT-shaped
// strings and the PAN pattern [A-Z]{5}[0-9]{4}[A-Z] so that last_error columns never hold secrets.
//
// Assumptions where the plan is silent:
//
//		func ErrorText(err error) string
//
//	  - ErrorText(nil) is "".
//	  - The replacement marker text is not specified; tests only require the secret to be gone and
//	    the surrounding diagnostic text to stay.
//	  - Redaction happens before truncation, so a secret that straddles the 500-character cut
//	    leaves no fragment behind.
//	  - The 500 limit is counted in characters (runes); the result is always valid UTF-8.
package retry

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

const (
	testPAN = "ABCDE1234F"
	// A JWT-shaped string: three base64url segments, the first two starting with eyJ.
	testJWT = "eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIn0.eyJpc3MiOiJodHRwczovL2FjY291bnRzLmdvb2dsZS5jb20iLCJlbWFpbCI6InNjaGVkQGV4YW1wbGUuY29tIn0.c2lnbmF0dXJlLWJ5dGVzLWZvci10ZXN0cw"
)

func TestErrorText_NilIsEmpty(t *testing.T) {
	if got := ErrorText(nil); got != "" {
		t.Fatalf("ErrorText(nil) = %q, want empty", got)
	}
}

func TestErrorText_PlainMessagesPassThroughUnchanged(t *testing.T) {
	for _, msg := range []string{
		"context deadline exceeded",
		"dial tcp 10.0.0.1:5432: connect: connection refused",
		"expo push: status 503 service unavailable",
		"lease expired",
		"handler panic: runtime error: index out of range [3] with length 2",
		"unicode: नमस्ते ₹100",
		"",
	} {
		if got := ErrorText(errors.New(msg)); got != msg {
			t.Errorf("ErrorText(%q) = %q, want unchanged", msg, got)
		}
	}
}

func TestErrorText_UsesTheWholeWrappedChain(t *testing.T) {
	err := fmt.Errorf("send: %w", fmt.Errorf("transport: %w", errors.New("connection reset")))
	if got, want := ErrorText(err), "send: transport: connection reset"; got != want {
		t.Fatalf("ErrorText = %q, want %q", got, want)
	}
}

func TestErrorText_Truncates(t *testing.T) {
	tests := []struct {
		name   string
		runes  int
		maxLen int
	}{
		{"exactly 500 is kept", 500, 500},
		{"501 is cut", 501, 500},
		{"10000 is cut", 10000, 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := strings.Repeat("x", tt.runes)
			got := ErrorText(errors.New(in))
			if n := utf8.RuneCountInString(got); n > tt.maxLen {
				t.Fatalf("ErrorText produced %d characters, want at most %d", n, tt.maxLen)
			}
			if !strings.HasPrefix(got, in[:480]) {
				t.Fatalf("ErrorText did not keep the start of the message: %.60q", got)
			}
			if tt.runes == 500 && got != in {
				t.Fatalf("a message of exactly 500 characters must be kept whole, got %d characters", utf8.RuneCountInString(got))
			}
		})
	}
}

func TestErrorText_TruncatesMultibyteWithoutBreakingCharacters(t *testing.T) {
	in := strings.Repeat("₹", 800) // 3 bytes per character
	got := ErrorText(errors.New(in))
	if !utf8.ValidString(got) {
		t.Fatal("ErrorText cut a multi-byte character in half")
	}
	if n := utf8.RuneCountInString(got); n > 500 {
		t.Fatalf("ErrorText produced %d characters, want at most 500", n)
	}
}

func TestErrorText_RedactsBearerTokens(t *testing.T) {
	const token = "ya29.a0AfH6SMBxYzSECRETtokenValue-1234567890_abc"
	tests := []struct {
		name, msg, keep string
	}{
		{"header form", "request failed: Authorization: Bearer " + token + " rejected", "request failed"},
		{"bare", "Bearer " + token, ""},
		{"lowercase scheme", "got bearer " + token + " from upstream", "from upstream"},
		{"uppercase scheme", "BEARER " + token, ""},
		{"inside quotes", `header "Authorization: Bearer ` + token + `" invalid`, "invalid"},
		{"url error", `Post "https://exp.host/push": 401 Bearer ` + token, "401"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorText(errors.New(tt.msg))
			if strings.Contains(got, "SECRET") || strings.Contains(got, token) || strings.Contains(got, token[:12]) {
				t.Fatalf("token leaked: %q", got)
			}
			if tt.keep != "" && !strings.Contains(got, tt.keep) {
				t.Fatalf("redaction removed the diagnostic text %q: %q", tt.keep, got)
			}
		})
	}
}

func TestErrorText_RedactsJWTShapedStrings(t *testing.T) {
	tests := []struct {
		name, msg, keep string
	}{
		{"alone", testJWT, ""},
		{"in a sentence", "verify token " + testJWT + " failed: expired", "failed: expired"},
		{"in a url query", "GET /cb?id_token=" + testJWT + "&state=1 returned 400", "returned 400"},
		{"twice", "a=" + testJWT + " b=" + testJWT, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorText(errors.New(tt.msg))
			for _, part := range strings.Split(testJWT, ".") {
				if strings.Contains(got, part[:20]) {
					t.Fatalf("JWT segment %q... leaked: %q", part[:20], got)
				}
			}
			if tt.keep != "" && !strings.Contains(got, tt.keep) {
				t.Fatalf("redaction removed the diagnostic text %q: %q", tt.keep, got)
			}
		})
	}
}

func TestErrorText_RedactsPAN(t *testing.T) {
	tests := []struct {
		name, msg, keep string
	}{
		{"alone", testPAN, ""},
		{"in a sentence", "client PAN " + testPAN + " not found", "not found"},
		{"in a path", "/clients/" + testPAN + "/accounts: 404", "404"},
		{"two PANs", "ABCDE1234F and ZZZZZ9999Z", "and"},
		{"lowercase letters are not a PAN", "abcde1234f", "abcde1234f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorText(errors.New(tt.msg))
			if strings.Contains(got, "ABCDE1234F") || strings.Contains(got, "ZZZZZ9999Z") {
				t.Fatalf("PAN leaked: %q", got)
			}
			if tt.keep != "" && !strings.Contains(got, tt.keep) {
				t.Fatalf("expected %q to remain: %q", tt.keep, got)
			}
		})
	}
}

func TestErrorText_DoesNotRedactNearMisses(t *testing.T) {
	for _, msg := range []string{
		"ABCD1234F",   // four letters
		"ABCDE123F",   // three digits
		"ABCDE12345",  // no final letter
		"order 12345", // plain number
		"version 1.2.3",
	} {
		if got := ErrorText(errors.New(msg)); got != msg {
			t.Errorf("ErrorText(%q) = %q, want unchanged", msg, got)
		}
	}
}

// A secret that straddles the 500-character cut must not leave a recognisable fragment.
func TestErrorText_RedactsBeforeTruncating(t *testing.T) {
	pad := strings.Repeat("x", 495)
	tests := []struct {
		name, secret, fragment string
	}{
		{"bearer", "Bearer SECRETVALUE0123456789abcdef", "SECRET"},
		{"pan", testPAN, "ABCDE"},
		{"jwt", testJWT, "eyJhbGci"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorText(errors.New(pad + " " + tt.secret))
			if strings.Contains(got, tt.fragment) {
				t.Fatalf("a fragment of the secret survived truncation: ...%q", got[len(got)-30:])
			}
			if utf8.RuneCountInString(got) > 500 {
				t.Fatalf("result has %d characters", utf8.RuneCountInString(got))
			}
		})
	}
}

func TestErrorText_JWTBuiltFromParts(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	jwt := enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc([]byte(`{"sub":"1234567890","email":"x@example.com"}`)) + "." + enc([]byte("not-a-real-signature-bytes"))
	got := ErrorText(fmt.Errorf("oidc: could not verify %s", jwt))
	if strings.Contains(got, jwt[:20]) || strings.Contains(got, strings.Split(jwt, ".")[1]) {
		t.Fatalf("JWT leaked: %q", got)
	}
	if !strings.Contains(got, "could not verify") {
		t.Fatalf("diagnostic text removed: %q", got)
	}
}

func TestErrorText_Properties(t *testing.T) {
	filler := `[ :,/=-]{0,100}`
	t.Run("never longer than 500 characters and valid UTF-8", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			in := rapid.String().Draw(t, "msg")
			got := ErrorText(errors.New(in))
			if n := utf8.RuneCountInString(got); n > 500 {
				t.Fatalf("%d characters for input of %d", n, utf8.RuneCountInString(in))
			}
			if utf8.ValidString(in) && !utf8.ValidString(got) {
				t.Fatalf("invalid UTF-8 produced from valid input")
			}
		})
	})

	t.Run("a PAN anywhere is never present in the output", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			pan := rapid.StringMatching(`[A-Z]{5}[0-9]{4}[A-Z]`).Draw(t, "pan")
			msg := rapid.StringMatching(filler).Draw(t, "before") + pan + rapid.StringMatching(filler).Draw(t, "after")
			if got := ErrorText(errors.New(msg)); strings.Contains(got, pan) {
				t.Fatalf("PAN %s leaked: %q", pan, got)
			}
		})
	})

	t.Run("a bearer token anywhere is never present in the output", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			token := rapid.StringMatching(`[A-Za-z0-9._~+/-]{24,80}`).Draw(t, "token")
			msg := rapid.StringMatching(filler).Draw(t, "before") + " Bearer " + token + " " + rapid.StringMatching(filler).Draw(t, "after")
			if got := ErrorText(errors.New(msg)); strings.Contains(got, token) {
				t.Fatalf("token %s leaked: %q", token, got)
			}
		})
	})

	t.Run("messages without secrets and under the limit are unchanged", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			msg := rapid.StringMatching(`[a-z :,/_-]{0,300}`).Draw(t, "msg")
			if strings.Contains(strings.ToLower(msg), "bearer") {
				t.Skip("contains the word bearer")
			}
			if got := ErrorText(errors.New(msg)); got != msg {
				t.Fatalf("ErrorText(%q) = %q", msg, got)
			}
		})
	})
}
