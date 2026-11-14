package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// NewToken returns prefix + base64url(32 random bytes) and its SHA-256 digest. Only the digest is
// stored; the plain token is shown once.
func NewToken(prefix string) (plain string, digest []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("crypto: token: %w", err)
	}
	plain = prefix + base64.RawURLEncoding.EncodeToString(raw)
	return plain, Digest(plain), nil
}

// Digest is SHA-256 of s.
func Digest(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
