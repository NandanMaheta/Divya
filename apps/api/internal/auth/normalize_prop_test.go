// Property tests for identifier normalisation (T014; D-16, D-17).
package auth_test

import (
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

var mobileGrammar = regexp.MustCompile(`^(?:\+91|91|0)?([6-9][0-9]{9})$`)

// mobileLike draws strings over the alphabet the grammar cares about.
func mobileLike() *rapid.Generator[string] {
	return rapid.StringOfN(rapid.SampledFrom([]rune("0123456789 -+")), 0, 18, -1)
}

func TestNormalizeMobile_PropertyAcceptedExactlyByTheGrammar(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := mobileLike().Draw(t, "input")
		stripped := strings.NewReplacer(" ", "", "-", "").Replace(in)
		m := mobileGrammar.FindStringSubmatch(stripped)
		got, err := auth.NormalizeMobile(in)
		if m == nil {
			if err == nil {
				t.Fatalf("NormalizeMobile(%q) = %q, grammar says invalid", in, got)
			}
			return
		}
		if err != nil || got != "+91"+m[1] {
			t.Fatalf("NormalizeMobile(%q) = %q, %v; want +91%s", in, got, err, m[1])
		}
	})
}

func TestNormalizeMobile_PropertyGeneratedValidFormsNormalise(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.SampledFrom([]string{"", "0", "91", "+91"}).Draw(t, "prefix")
		first := rapid.SampledFrom([]rune("6789")).Draw(t, "first")
		rest := rapid.StringOfN(rapid.RuneFrom([]rune("0123456789")), 9, 9, -1).Draw(t, "rest")
		core := prefix + string(first) + rest
		// Sprinkle separators between any two characters.
		var b strings.Builder
		for _, r := range core {
			if rapid.IntRange(0, 3).Draw(t, "sep") == 0 {
				b.WriteString(rapid.SampledFrom([]string{" ", "-", "  ", " - "}).Draw(t, "sepText"))
			}
			b.WriteRune(r)
		}
		got, err := auth.NormalizeMobile(b.String())
		if want := "+91" + string(first) + rest; err != nil || got != want {
			t.Fatalf("NormalizeMobile(%q) = %q, %v; want %q", b.String(), got, err, want)
		}
	})
}

func TestNormalizeMobile_PropertyIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := mobileLike().Draw(t, "input")
		once, err := auth.NormalizeMobile(in)
		if err != nil {
			return
		}
		twice, err := auth.NormalizeMobile(once)
		if err != nil || twice != once {
			t.Fatalf("normalising %q again: %q, %v; want %q", once, twice, err, once)
		}
	})
}

func TestNormalizeEmail_PropertyIdempotentAndLowercase(t *testing.T) {
	localRunes := []rune("abcXYZ019._+-")
	domainRunes := []rune("abcXYZ019-")
	rapid.Check(t, func(t *rapid.T) {
		local := rapid.StringOfN(rapid.SampledFrom(localRunes), 1, 12, -1).Draw(t, "local")
		domain := rapid.StringOfN(rapid.SampledFrom(domainRunes), 1, 12, -1).Draw(t, "domain")
		tld := rapid.SampledFrom([]string{"com", "IN", "Co.In"}).Draw(t, "tld")
		pad := rapid.SampledFrom([]string{"", " ", "  \t"}).Draw(t, "pad")
		in := pad + local + "@" + domain + "." + tld + pad
		once, err := auth.NormalizeEmail(in)
		if err != nil {
			return // the generator can produce dotted locals that are invalid; only accepted values are checked
		}
		if once != strings.ToLower(once) || once != strings.TrimSpace(once) {
			t.Fatalf("NormalizeEmail(%q) = %q is not trimmed and lowercase", in, once)
		}
		twice, err := auth.NormalizeEmail(once)
		if err != nil || twice != once {
			t.Fatalf("normalising %q again: %q, %v", once, twice, err)
		}
	})
}

func TestNormalizeEmail_PropertyCaseAndPaddingDoNotChangeIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		local := rapid.StringOfN(rapid.SampledFrom([]rune("abcdefgh0123")), 1, 10, -1).Draw(t, "local")
		domain := rapid.StringOfN(rapid.SampledFrom([]rune("abcdefgh")), 1, 10, -1).Draw(t, "domain")
		base := local + "@" + domain + ".com"
		upper := " " + strings.ToUpper(base) + " "
		a, errA := auth.NormalizeEmail(base)
		b, errB := auth.NormalizeEmail(upper)
		if errA != nil || errB != nil || a != b || a != base {
			t.Fatalf("%q -> %q (%v); %q -> %q (%v)", base, a, errA, upper, b, errB)
		}
	})
}
