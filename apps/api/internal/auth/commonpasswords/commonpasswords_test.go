// Tests for the embedded common-password list (T016; D-07, plan P-06).
//
// API assumptions:
//
//	package commonpasswords
//	func Set() map[string]struct{}   // lowercase entries; loaded once; callers must not mutate it
//
// SOURCE.md records, each on its own line and case-insensitively, "Line count: <n>" and
// "SHA-256: <64 lowercase hex>" for 10k.txt, plus the source URL (SecLists 10k-most-common.txt)
// and the licence name. 10k.txt holds one lowercase entry per line, no blank lines, no duplicates,
// "\n" line endings, with a final newline. The pinned values change only deliberately.
package commonpasswords_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth/commonpasswords"
)

func TestSet_ContainsTheKnownWeakPasswords(t *testing.T) {
	set := commonpasswords.Set()
	for _, p := range []string{"1234567890", "qwertyuiop", "password", "123456", "qwerty", "letmein", "123456789"} {
		if _, ok := set[p]; !ok {
			t.Errorf("list lacks %q", p)
		}
	}
	for _, p := range []string{"", "correct horse battery staple", "swastik-not-a-common-password-7391"} {
		if _, ok := set[p]; ok {
			t.Errorf("list unexpectedly contains %q", p)
		}
	}
}

func TestSet_IsLowercaseAndSane(t *testing.T) {
	set := commonpasswords.Set()
	if n := len(set); n < 9000 || n > 10100 {
		t.Fatalf("len(Set) = %d, want roughly 10,000 (9,000..10,100)", n)
	}
	for p := range set {
		if p == "" || p != strings.ToLower(p) || strings.ContainsAny(p, "\r\n") || p != strings.TrimSpace(p) {
			t.Fatalf("entry %q is empty, not lowercase, or has stray whitespace", p)
		}
	}
}

func TestSet_IsLoadedOnce(t *testing.T) {
	a, b := commonpasswords.Set(), commonpasswords.Set()
	if reflect.ValueOf(a).Pointer() != reflect.ValueOf(b).Pointer() {
		t.Error("Set() builds a new map on every call; it must be loaded once")
	}
}

func TestDataFile_IsLowercaseDeduplicatedAndMatchesSet(t *testing.T) {
	raw, err := os.ReadFile("10k.txt")
	if err != nil {
		t.Fatalf("read 10k.txt: %v", err)
	}
	text := string(raw)
	if !strings.HasSuffix(text, "\n") || strings.Contains(text, "\r") {
		t.Fatal("10k.txt must use \\n line endings and end with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	seen := make(map[string]bool, len(lines))
	for i, l := range lines {
		if l == "" {
			t.Fatalf("line %d is blank", i+1)
		}
		if l != strings.ToLower(l) {
			t.Fatalf("line %d %q is not lowercase", i+1, l)
		}
		if seen[l] {
			t.Fatalf("line %d %q is a duplicate", i+1, l)
		}
		seen[l] = true
	}
	set := commonpasswords.Set()
	if len(set) != len(lines) {
		t.Errorf("len(Set) = %d, 10k.txt has %d lines", len(set), len(lines))
	}
	for _, l := range lines {
		if _, ok := set[l]; !ok {
			t.Fatalf("Set lacks line %q", l)
		}
	}
}

func TestSourceNote_PinsLineCountAndSHA256(t *testing.T) {
	note, err := os.ReadFile("SOURCE.md")
	if err != nil {
		t.Fatalf("read SOURCE.md: %v", err)
	}
	data, err := os.ReadFile("10k.txt")
	if err != nil {
		t.Fatalf("read 10k.txt: %v", err)
	}

	countRe := regexp.MustCompile(`(?im)^\W*line count\W*[:=]\s*([0-9][0-9,_]*)`)
	m := countRe.FindSubmatch(note)
	if m == nil {
		t.Fatal(`SOURCE.md has no "Line count: <n>" line`)
	}
	recorded, err := strconv.Atoi(strings.NewReplacer(",", "", "_", "").Replace(string(m[1])))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != recorded {
		t.Errorf("10k.txt has %d lines, SOURCE.md records %d (update both deliberately)", got, recorded)
	}

	shaRe := regexp.MustCompile(`(?im)^\W*sha-?256\W*[:=]\s*` + "`?" + `([0-9a-f]{64})`)
	m = shaRe.FindSubmatch(note)
	if m == nil {
		t.Fatal(`SOURCE.md has no "SHA-256: <64 hex>" line`)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != string(m[1]) {
		t.Errorf("SHA-256 of 10k.txt = %s, SOURCE.md records %s (update both deliberately)", got, m[1])
	}
}

func TestSourceNote_RecordsProvenance(t *testing.T) {
	note, err := os.ReadFile("SOURCE.md")
	if err != nil {
		t.Fatalf("read SOURCE.md: %v", err)
	}
	s := string(note)
	for _, want := range []string{"SecLists", "10k-most-common.txt", "MIT", "https://"} {
		if !strings.Contains(s, want) {
			t.Errorf("SOURCE.md does not mention %q", want)
		}
	}
	if !regexp.MustCompile(`20[0-9]{2}-[0-9]{2}-[0-9]{2}`).MatchString(s) {
		t.Error("SOURCE.md records no retrieval date (YYYY-MM-DD)")
	}
}
