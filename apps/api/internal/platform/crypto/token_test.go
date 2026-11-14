// Tests for opaque random tokens and digests (T002; go-capabilities section 5, plan P-03/P-05, FR-092).
//
// API assumption: NewToken returns prefix + base64url(32 random bytes) using the unpadded
// URL-safe alphabet (43 characters), so len(plain) == len(prefix)+43.
package crypto_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

func TestNewToken_ShapeAndEntropy(t *testing.T) {
	for _, prefix := range []string{"drt_", "dmc_", ""} {
		t.Run("prefix "+prefix, func(t *testing.T) {
			plain, digest, err := crypto.NewToken(prefix)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(plain, prefix) {
				t.Fatalf("token %q lacks prefix %q", plain, prefix)
			}
			raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(plain, prefix))
			if err != nil {
				t.Fatalf("token body is not unpadded base64url: %v", err)
			}
			if len(raw) != 32 {
				t.Errorf("token carries %d random bytes, want 32", len(raw))
			}
			if len(digest) != sha256.Size {
				t.Errorf("digest length = %d, want 32", len(digest))
			}
		})
	}
}

func TestNewToken_DigestIsSHA256OfPlain(t *testing.T) {
	plain, digest, err := crypto.NewToken("drt_")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(plain))
	if !bytes.Equal(digest, sum[:]) {
		t.Errorf("digest = %x, want SHA-256 of the whole plain token %x", digest, sum)
	}
	if !bytes.Equal(crypto.Digest(plain), digest) {
		t.Error("Digest(plain) differs from the digest NewToken returned")
	}
}

func TestNewToken_AllDistinct(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		plain, _, err := crypto.NewToken("drt_")
		if err != nil {
			t.Fatal(err)
		}
		if seen[plain] {
			t.Fatalf("duplicate token %q", plain)
		}
		seen[plain] = true
	}
}

func TestNewToken_DigestDoesNotContainToken(t *testing.T) {
	plain, digest, _ := crypto.NewToken("drt_")
	if bytes.Contains(digest, []byte(plain)) || bytes.Contains([]byte(plain), digest) {
		t.Error("digest leaks the token")
	}
}

func TestDigest_KnownVectors(t *testing.T) {
	// SHA-256 test vectors (FIPS 180-4 / NIST examples).
	for in, want := range map[string]string{
		"":    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"abc": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	} {
		got := crypto.Digest(in)
		if hex := strings.ToLower(hexOf(got)); hex != want {
			t.Errorf("Digest(%q) = %s, want %s", in, hex, want)
		}
	}
}

func TestDigest_Deterministic(t *testing.T) {
	if !bytes.Equal(crypto.Digest("x"), crypto.Digest("x")) || bytes.Equal(crypto.Digest("x"), crypto.Digest("y")) {
		t.Error("Digest must be deterministic and input-sensitive")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}
