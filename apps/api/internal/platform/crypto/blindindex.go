package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

// BlindIndexer produces deterministic, keyed digests so equal values can be looked up without
// storing them: HMAC-SHA-256(key, purpose || 0x00 || value).
type BlindIndexer struct {
	key [keySize]byte
}

// NewBlindIndexer requires a 32-byte key and copies it.
func NewBlindIndexer(key []byte) (BlindIndexer, error) {
	if len(key) != keySize {
		return BlindIndexer{}, errors.New("crypto: blind index key must be 32 bytes")
	}
	var b BlindIndexer
	copy(b.key[:], key)
	return b, nil
}

// Index returns the 32-byte digest for the purpose and value.
func (b BlindIndexer) Index(purpose string, value []byte) []byte {
	mac := hmac.New(sha256.New, b.key[:])
	mac.Write([]byte(purpose))
	mac.Write([]byte{0x00})
	mac.Write(value)
	return mac.Sum(nil)
}
