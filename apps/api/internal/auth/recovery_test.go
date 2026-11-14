// Tests for admin recovery codes (T029; FR-062, US-5 s6, plan P-05).
//
// API assumptions (contracts are silent; the implementer must match these names):
//
//	const RecoveryCodeCount = 10
//	func GenerateRecoveryCodes() ([]string, error)       // 10 codes shown once, each XXXX-XXXX-XXXX-XXXX
//	func NormalizeRecoveryCode(input string) string      // upper-case; spaces and hyphens removed; O->0, I->1, L->1
//	func RecoveryCodeDigest(input string) []byte         // SHA-256 of NormalizeRecoveryCode(input) (via platform/crypto)
//
// Alphabet: Crockford base32 (0-9 A-Z without I, L, O, U); 16 symbols = 80 random bits per code.
// Normalisation does not validate length; a malformed input just never matches a stored digest.
package auth_test

import (
	"bytes"
	"crypto/sha256"
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// asciiPrintable is every printable ASCII character: properties stay clear of exotic Unicode case mappings.
const asciiPrintable = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"

var recoveryShape = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`)

func mustGenerate(t testing.TB) []string {
	t.Helper()
	codes, err := auth.GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	return codes
}

func TestRecovery_TenCodesInDisplayFormat(t *testing.T) {
	if auth.RecoveryCodeCount != 10 {
		t.Fatalf("RecoveryCodeCount = %d, want 10", auth.RecoveryCodeCount)
	}
	codes := mustGenerate(t)
	if len(codes) != 10 {
		t.Fatalf("got %d codes, want 10", len(codes))
	}
	for _, c := range codes {
		if !recoveryShape.MatchString(c) {
			t.Errorf("code %q does not match XXXX-XXXX-XXXX-XXXX in the Crockford alphabet", c)
		}
	}
}

func TestRecovery_AllDistinct(t *testing.T) {
	seen := map[string]bool{}
	for range 300 {
		for _, c := range mustGenerate(t) {
			if seen[c] {
				t.Fatalf("duplicate recovery code %q", c)
			}
			seen[c] = true
		}
	}
}

func TestRecovery_SymbolsAreUniformlyRandom(t *testing.T) {
	// 80 bits per code means each of the 16 symbols is a uniform draw from 32. 4000 codes give
	// 4000 draws per position; the bounds are about 8 standard deviations wide.
	const codes = 4000
	var total [32]int
	var first [32]int
	var last [32]int
	for range codes / 10 {
		for _, c := range mustGenerate(t) {
			plain := strings.ReplaceAll(c, "-", "")
			for _, r := range plain {
				total[strings.IndexRune(crockford, r)]++
			}
			first[strings.IndexRune(crockford, rune(plain[0]))]++
			last[strings.IndexRune(crockford, rune(plain[15]))]++
		}
	}
	expectTotal := float64(codes*16) / 32 // 2000 per symbol
	for i, n := range total {
		if float64(n) < expectTotal*0.8 || float64(n) > expectTotal*1.2 {
			t.Errorf("symbol %c appeared %d times, expected about %.0f", crockford[i], n, expectTotal)
		}
	}
	for name, hist := range map[string][32]int{"first": first, "last": last} {
		for i, n := range hist {
			if n < 60 || n > 200 { // expected 125 +- ~11
				t.Errorf("%s position: symbol %c appeared %d times, expected about 125 (a position is biased or truncated)", name, crockford[i], n)
			}
		}
	}
}

func TestRecovery_NormalizeTable(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"displayed form", "ABCD-EFGH-JKMN-PQRS", "ABCDEFGHJKMNPQRS"},
		{"lower case", "abcd-efgh-jkmn-pqrs", "ABCDEFGHJKMNPQRS"},
		{"mixed case", "aBcD-eFgH-jKmN-pQrS", "ABCDEFGHJKMNPQRS"},
		{"no hyphens", "abcdefghjkmnpqrs", "ABCDEFGHJKMNPQRS"},
		{"spaces instead of hyphens", "abcd efgh jkmn pqrs", "ABCDEFGHJKMNPQRS"},
		{"hyphens and spaces", "abcd - efgh - jkmn - pqrs", "ABCDEFGHJKMNPQRS"},
		{"surrounding spaces", "  abcd-efgh-jkmn-pqrs  ", "ABCDEFGHJKMNPQRS"},
		{"hyphens anywhere", "a-b-c-d-e-f-g-h-j-k-m-n-p-q-r-s", "ABCDEFGHJKMNPQRS"},
		{"O is 0", "OOOO-oooo-0000-0O0o", "0000000000000000"},
		{"I is 1", "IIII-iiii-1111-1I1i", "1111111111111111"},
		{"L is 1", "LLLL-llll-1111-1L1l", "1111111111111111"},
		{"digits untouched", "0123-4567-89AB-CDEF", "0123456789ABCDEF"},
		{"all three aliases", "o1il-ILOo-abcd-efgh", "01111100ABCDEFGH"},
		{"empty", "", ""},
		{"only separators", " - - ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auth.NormalizeRecoveryCode(tt.in); got != tt.want {
				t.Errorf("NormalizeRecoveryCode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRecovery_GeneratedCodesNormaliseToTheirSixteenSymbols(t *testing.T) {
	for _, c := range mustGenerate(t) {
		got := auth.NormalizeRecoveryCode(c)
		if got != strings.ReplaceAll(c, "-", "") || len(got) != 16 {
			t.Errorf("NormalizeRecoveryCode(%q) = %q, want the 16 symbols without hyphens", c, got)
		}
	}
}

func TestRecovery_DigestIsSHA256OfTheNormalisedCode(t *testing.T) {
	code := mustGenerate(t)[0]
	sum := sha256.Sum256([]byte(auth.NormalizeRecoveryCode(code)))
	if got := auth.RecoveryCodeDigest(code); !bytes.Equal(got, sum[:]) {
		t.Errorf("RecoveryCodeDigest = %x, want SHA-256 of the 16 normalised symbols %x", got, sum)
	}
	if len(auth.RecoveryCodeDigest(code)) != 32 {
		t.Error("digest must be 32 bytes (mfa_recovery_codes.code_hash CHECK octet_length = 32)")
	}
}

func TestRecovery_DigestIsStableAcrossSpellings(t *testing.T) {
	// A code containing every alias-sensitive symbol, written the ways an admin might type it.
	const code = "0O1I-L1AB-CDEF-GH0J"
	want := auth.RecoveryCodeDigest(code)
	for _, spelling := range []string{
		"0O1I-L1AB-CDEF-GH0J",
		"0o1i-l1ab-cdef-gh0j",
		"0O1IL1ABCDEFGH0J",
		"0o1i l1ab cdef gh0j",
		" 0O1I - L1AB - CDEF - GH0J ",
		"001I-L1AB-CDEF-GH0J", // O typed as 0
		"0O11-11AB-CDEF-GH0J", // I and L typed as 1
		"0OlI-LlAB-CDEF-GHoJ",
		"0o1l-IlAb-cDeF-gHoJ",
	} {
		if got := auth.RecoveryCodeDigest(spelling); !bytes.Equal(got, want) {
			t.Errorf("digest of %q differs from the canonical spelling", spelling)
		}
	}
}

func TestRecovery_GeneratedDigestsAreDistinctAndWrongCodesNeverMatch(t *testing.T) {
	codes := mustGenerate(t)
	stored := map[string]bool{}
	for _, c := range codes {
		d := string(auth.RecoveryCodeDigest(c))
		if stored[d] {
			t.Fatalf("two codes share a digest")
		}
		stored[d] = true
	}
	// A wrong code never matches: one changed symbol, a swapped pair, truncation, extension, another batch.
	for _, c := range codes {
		plain := []byte(strings.ReplaceAll(c, "-", ""))
		changed := bytes.Clone(plain)
		changed[7] = crockford[(strings.IndexByte(crockford, changed[7])+1)%32]
		swapped := bytes.Clone(plain)
		swapped[0], swapped[1] = swapped[1], swapped[0]
		if swapped[0] == swapped[1] {
			swapped = nil
		}
		for name, wrong := range map[string]string{
			"one symbol changed": string(changed),
			"truncated":          string(plain[:15]),
			"extended":           string(plain) + "0",
			"empty":              "",
		} {
			if stored[string(auth.RecoveryCodeDigest(wrong))] {
				t.Errorf("%s: %q matched a stored digest", name, wrong)
			}
		}
		if swapped != nil && stored[string(auth.RecoveryCodeDigest(string(swapped)))] {
			t.Errorf("a code with two symbols swapped matched")
		}
	}
	for _, other := range mustGenerate(t) {
		if stored[string(auth.RecoveryCodeDigest(other))] {
			t.Error("a code from another batch matched this batch's digests")
		}
	}
}

func TestRecovery_PropertyNormalizeIsIdempotentAndCanonical(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := rapid.StringOf(rapid.RuneFrom([]rune(asciiPrintable))).Draw(t, "input")
		once := auth.NormalizeRecoveryCode(in)
		if twice := auth.NormalizeRecoveryCode(once); twice != once {
			t.Fatalf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
		if strings.ContainsAny(once, "- ilLOo") {
			t.Fatalf("normalised form %q still contains separators or aliases", once)
		}
		if once != strings.ToUpper(once) {
			t.Fatalf("normalised form %q is not upper case", once)
		}
	})
}

func TestRecovery_PropertySpellingsOfTheSameCodeShareADigest(t *testing.T) {
	symbols := []rune(crockford)
	rapid.Check(t, func(t *rapid.T) {
		plain := rapid.SliceOfN(rapid.SampledFrom(symbols), 16, 16).Draw(t, "symbols")
		var canonical, spelled strings.Builder
		for i, r := range plain {
			if i > 0 && i%4 == 0 {
				canonical.WriteByte('-')
				spelled.WriteString(rapid.SampledFrom([]string{"", "-", " ", " - "}).Draw(t, "sep"))
			}
			canonical.WriteRune(r)
			s := string(r)
			switch r {
			case '0':
				s = rapid.SampledFrom([]string{"0", "O", "o"}).Draw(t, "zero")
			case '1':
				s = rapid.SampledFrom([]string{"1", "I", "i", "L", "l"}).Draw(t, "one")
			default:
				s = rapid.SampledFrom([]string{s, strings.ToLower(s)}).Draw(t, "case")
			}
			spelled.WriteString(s)
		}
		if !bytes.Equal(auth.RecoveryCodeDigest(canonical.String()), auth.RecoveryCodeDigest(spelled.String())) {
			t.Fatalf("%q and %q are the same code but digests differ", canonical.String(), spelled.String())
		}
	})
}
