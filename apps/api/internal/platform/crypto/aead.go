// Package crypto holds the platform's small cryptographic helpers: an AES-256-GCM key ring for
// secrets at rest, an HMAC blind indexer and opaque random tokens. No other package should
// import crypto/* primitives for these purposes.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	keySize      = 32
	envelopeV1   = 0x01
	headerSize   = 1 + 2 // version + key id
	nonceSize    = 12
	gcmTagSize   = 16
	minEnvelope  = headerSize + nonceSize + gcmTagSize
	nonceOffset  = headerSize
	cipherOffset = headerSize + nonceSize
)

// ErrInvalidEnvelope is returned for any envelope that is malformed, uses an unknown key or fails
// authentication. Callers must not distinguish the causes.
var ErrInvalidEnvelope = errors.New("crypto: invalid envelope")

// Keyring seals with the active key and opens with any known key id.
type Keyring struct {
	active uint16
	aeads  map[uint16]cipher.AEAD
}

// NewKeyring builds a ring. Every key must be exactly 32 bytes and the active id must be present.
// Key material is copied; later changes to the caller's slices have no effect.
func NewKeyring(active uint16, keys map[uint16][]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, errors.New("crypto: keyring needs at least one key")
	}
	aeads := make(map[uint16]cipher.AEAD, len(keys))
	for id, material := range keys {
		if len(material) != keySize {
			return nil, fmt.Errorf("crypto: key %d must be %d bytes", id, keySize)
		}
		block, err := aes.NewCipher(material)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %d: %w", id, err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("crypto: key %d: %w", id, err)
		}
		aeads[id] = gcm
	}
	if _, ok := aeads[active]; !ok {
		return nil, fmt.Errorf("crypto: active key %d is not in the ring", active)
	}
	return &Keyring{active: active, aeads: aeads}, nil
}

// Seal encrypts plaintext under the active key, binding aad.
// Layout: 0x01 | key_id uint16 BE | nonce(12) | ciphertext+tag.
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error) {
	out := make([]byte, cipherOffset, cipherOffset+len(plaintext)+gcmTagSize)
	out[0] = envelopeV1
	binary.BigEndian.PutUint16(out[1:headerSize], k.active)
	if _, err := io.ReadFull(rand.Reader, out[nonceOffset:cipherOffset]); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return k.aeads[k.active].Seal(out, out[nonceOffset:cipherOffset], plaintext, aad), nil
}

// Open decrypts an envelope produced by Seal under any key still in the ring.
func (k *Keyring) Open(envelope, aad []byte) ([]byte, error) {
	id, err := k.KeyID(envelope)
	if err != nil || len(envelope) < minEnvelope {
		return nil, ErrInvalidEnvelope
	}
	aead, ok := k.aeads[id]
	if !ok {
		return nil, ErrInvalidEnvelope
	}
	pt, err := aead.Open(nil, envelope[nonceOffset:cipherOffset], envelope[cipherOffset:], aad)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	return pt, nil
}

// KeyID reports which key sealed the envelope (for rotation checks).
func (k *Keyring) KeyID(envelope []byte) (uint16, error) {
	if len(envelope) < headerSize || envelope[0] != envelopeV1 {
		return 0, ErrInvalidEnvelope
	}
	return binary.BigEndian.Uint16(envelope[1:headerSize]), nil
}
