// Tests for the HMAC-SHA-256 blind indexer (T002; go-capabilities section 5, plan P-07, R-8).
package crypto_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"testing"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

func newIndexer(t testing.TB, b byte) crypto.BlindIndexer {
	t.Helper()
	bi, err := crypto.NewBlindIndexer(key(b))
	if err != nil {
		t.Fatalf("NewBlindIndexer: %v", err)
	}
	return bi
}

func TestBlindIndexer_MatchesHMACDefinition(t *testing.T) {
	// Index(purpose, value) = HMAC-SHA-256(key, purpose || 0x00 || value).
	bi := newIndexer(t, 7)
	for _, tc := range []struct{ purpose, value string }{
		{"throttle", "user|0190c1a2"},
		{"pan", "ABCDE1234F"},
		{"", ""},
		{"throttle", ""},
	} {
		mac := hmac.New(sha256.New, key(7))
		mac.Write([]byte(tc.purpose))
		mac.Write([]byte{0x00})
		mac.Write([]byte(tc.value))
		want := mac.Sum(nil)
		if got := bi.Index(tc.purpose, []byte(tc.value)); !bytes.Equal(got, want) {
			t.Errorf("Index(%q,%q) = %x, want %x", tc.purpose, tc.value, got, want)
		}
	}
}

func TestBlindIndexer_Deterministic(t *testing.T) {
	a, b := newIndexer(t, 1), newIndexer(t, 1)
	v := []byte("mobile|+919876543210")
	if !bytes.Equal(a.Index("throttle", v), a.Index("throttle", v)) {
		t.Error("same indexer gave different outputs for the same input")
	}
	if !bytes.Equal(a.Index("throttle", v), b.Index("throttle", v)) {
		t.Error("two indexers with the same key disagree")
	}
	if got := a.Index("throttle", v); len(got) != 32 {
		t.Errorf("output length = %d, want 32", len(got))
	}
}

func TestBlindIndexer_PurposeSeparation(t *testing.T) {
	bi := newIndexer(t, 1)
	v := []byte("same value")
	if bytes.Equal(bi.Index("throttle", v), bi.Index("pan", v)) {
		t.Error("different purposes gave the same index")
	}
	// The 0x00 separator prevents purpose/value boundary shifts from colliding.
	if bytes.Equal(bi.Index("ab", []byte("c")), bi.Index("a", []byte("bc"))) {
		t.Error("purpose/value boundary is ambiguous")
	}
}

func TestBlindIndexer_KeySeparation(t *testing.T) {
	v := []byte("v")
	if bytes.Equal(newIndexer(t, 1).Index("p", v), newIndexer(t, 2).Index("p", v)) {
		t.Error("different keys gave the same index")
	}
}

func TestBlindIndexer_RejectsBadKeyLength(t *testing.T) {
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		if _, err := crypto.NewBlindIndexer(make([]byte, n)); err == nil {
			t.Errorf("NewBlindIndexer accepted a %d-byte key", n)
		}
	}
	if _, err := crypto.NewBlindIndexer(nil); err == nil {
		t.Error("NewBlindIndexer accepted a nil key")
	}
}

func TestBlindIndexer_DoesNotAliasCallerKey(t *testing.T) {
	material := key(5)
	bi, err := crypto.NewBlindIndexer(material)
	if err != nil {
		t.Fatal(err)
	}
	want := bi.Index("p", []byte("v"))
	for i := range material {
		material[i] = 0
	}
	if !bytes.Equal(bi.Index("p", []byte("v")), want) {
		t.Error("indexer depended on caller-owned key bytes")
	}
}

func TestBlindIndexer_PropertyInjectiveOnDistinctInputs(t *testing.T) {
	bi := newIndexer(t, 9)
	rapid.Check(t, func(t *rapid.T) {
		p := rapid.StringN(0, 12, 12).Draw(t, "purpose")
		a := rapid.SliceOfN(rapid.Byte(), 0, 40).Draw(t, "a")
		b := rapid.SliceOfN(rapid.Byte(), 0, 40).Draw(t, "b")
		if bytes.Equal(a, b) {
			return
		}
		if bytes.Equal(bi.Index(p, a), bi.Index(p, b)) {
			t.Fatalf("collision for %x and %x", a, b)
		}
	})
}
