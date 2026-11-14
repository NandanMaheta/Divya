// Tests for the AES-256-GCM key ring (T002; go-capabilities section 5, plan P-05/P-14, FR-091).
//
// API assumptions (contracts are silent): NewKeyring and BlindIndexer constructors return a non-nil
// error for bad input and callers must not depend on a specific sentinel; Open returns a non-nil
// error and nil plaintext on any failure.
package crypto_test

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"testing"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newRing(t testing.TB, active uint16, ids ...uint16) *crypto.Keyring {
	t.Helper()
	keys := map[uint16][]byte{}
	for _, id := range ids {
		keys[id] = key(byte(id))
	}
	k, err := crypto.NewKeyring(active, keys)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return k
}

func TestKeyring_SealOpenRoundTrip(t *testing.T) {
	k := newRing(t, 1, 1)
	for _, tc := range []struct {
		name      string
		plaintext []byte
		aad       []byte
	}{
		{"short with aad", []byte("JBSWY3DPEHPK3PXP"), []byte("admin_mfa.secret:0190-user")},
		{"empty plaintext", []byte{}, []byte("aad")},
		{"nil aad", []byte("secret"), nil},
		{"binary", []byte{0, 1, 2, 255, 254}, []byte{0, 0}},
		{"large", bytes.Repeat([]byte("x"), 100_000), []byte("aad")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := k.Seal(tc.plaintext, tc.aad)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			got, err := k.Open(env, tc.aad)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if !bytes.Equal(got, tc.plaintext) {
				t.Fatalf("Open = %q, want %q", got, tc.plaintext)
			}
		})
	}
}

func TestKeyring_EnvelopeLayout(t *testing.T) {
	// Layout: 0x01 | key_id uint16 big-endian | nonce(12) | ciphertext+tag(16).
	k := newRing(t, 0x0102, 0x0102)
	pt := []byte("hello envelope")
	env, err := k.Seal(pt, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if want := 1 + 2 + 12 + len(pt) + 16; len(env) != want {
		t.Fatalf("envelope length = %d, want %d", len(env), want)
	}
	if env[0] != 0x01 {
		t.Errorf("version byte = %#x, want 0x01", env[0])
	}
	if got := binary.BigEndian.Uint16(env[1:3]); got != 0x0102 {
		t.Errorf("key id bytes = %#x, want 0x0102", got)
	}
	if bytes.Contains(env, pt) {
		t.Error("plaintext appears in the envelope")
	}
	id, err := k.KeyID(env)
	if err != nil || id != 0x0102 {
		t.Errorf("KeyID = %d, %v; want 258, nil", id, err)
	}
}

func TestKeyring_SealUsesFreshNonce(t *testing.T) {
	k := newRing(t, 1, 1)
	seen := map[string]bool{}
	for range 200 {
		env, err := k.Seal([]byte("same"), []byte("aad"))
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(env[3:15])
		if seen[nonce] {
			t.Fatal("nonce reused across Seal calls")
		}
		seen[nonce] = true
	}
}

func TestKeyring_OpenRejectsWrongAAD(t *testing.T) {
	k := newRing(t, 1, 1)
	env, _ := k.Seal([]byte("secret"), []byte("admin_mfa.secret:user-a"))
	for name, aad := range map[string][]byte{
		"other user":      []byte("admin_mfa.secret:user-b"),
		"other column":    []byte("admin_mfa.pending_secret:user-a"),
		"empty":           {},
		"nil":             nil,
		"prefix of right": []byte("admin_mfa.secret:user-"),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := k.Open(env, aad); err == nil {
				t.Fatalf("Open with wrong AAD succeeded: %q", got)
			}
		})
	}
}

func TestKeyring_OpenRejectsAnyTamperedByte(t *testing.T) {
	k := newRing(t, 7, 7)
	env, err := k.Seal([]byte("totp-secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range env {
		bad := bytes.Clone(env)
		bad[i] ^= 0x01
		if got, err := k.Open(bad, []byte("aad")); err == nil {
			t.Fatalf("Open accepted envelope with byte %d flipped: %q", i, got)
		}
	}
}

func TestKeyring_OpenRejectsMalformedEnvelopes(t *testing.T) {
	k := newRing(t, 1, 1)
	env, _ := k.Seal([]byte("x"), nil)
	for name, bad := range map[string][]byte{
		"nil":               nil,
		"empty":             {},
		"version only":      {0x01},
		"header only":       env[:3],
		"no tag":            env[:15],
		"truncated by one":  env[:len(env)-1],
		"unknown version":   append([]byte{0x02}, env[1:]...),
		"zero version":      append([]byte{0x00}, env[1:]...),
		"trailing garbage":  append(bytes.Clone(env), 0x00),
		"all zero 40 bytes": make([]byte, 40),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := k.Open(bad, nil); err == nil {
				t.Fatal("Open accepted a malformed envelope")
			}
			if _, err := k.KeyID(bad); err == nil && (len(bad) < 3 || bad[0] != 0x01) {
				t.Fatal("KeyID accepted a malformed envelope")
			}
		})
	}
}

func TestKeyring_UnknownKeyID(t *testing.T) {
	sealer := newRing(t, 5, 5)
	env, _ := sealer.Seal([]byte("secret"), []byte("aad"))
	other := newRing(t, 6, 6)
	if _, err := other.Open(env, []byte("aad")); err == nil {
		t.Fatal("Open succeeded with a ring that lacks the envelope's key id")
	}
}

func TestKeyring_Rotation(t *testing.T) {
	old := newRing(t, 1, 1)
	oldEnv, err := old.Seal([]byte("old secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}

	// After rotation the ring holds both keys and signs with 2.
	rotated, err := crypto.NewKeyring(2, map[uint16][]byte{1: key(1), 2: key(2)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := rotated.Open(oldEnv, []byte("aad"))
	if err != nil || string(got) != "old secret" {
		t.Fatalf("old envelope no longer opens after rotation: %q, %v", got, err)
	}
	if id, err := rotated.KeyID(oldEnv); err != nil || id != 1 {
		t.Errorf("KeyID(old envelope) = %d, %v; want 1", id, err)
	}
	newEnv, err := rotated.Seal([]byte("new secret"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if id, err := rotated.KeyID(newEnv); err != nil || id != 2 {
		t.Errorf("KeyID(new envelope) = %d, %v; want 2 (the active key)", id, err)
	}
	// A ring that dropped key 1 can no longer open old data.
	dropped := newRing(t, 2, 2)
	if _, err := dropped.Open(oldEnv, []byte("aad")); err == nil {
		t.Error("Open succeeded after key 1 was removed from the ring")
	}
	// Keys with different material under the same id must not open each other's data.
	impostor, _ := crypto.NewKeyring(1, map[uint16][]byte{1: key(99)})
	if _, err := impostor.Open(oldEnv, []byte("aad")); err == nil {
		t.Error("Open succeeded with different key material under the same id")
	}
}

func TestKeyring_NewKeyringValidation(t *testing.T) {
	tests := []struct {
		name   string
		active uint16
		keys   map[uint16][]byte
	}{
		{"nil map", 1, nil},
		{"empty map", 1, map[uint16][]byte{}},
		{"active id missing", 2, map[uint16][]byte{1: key(1)}},
		{"key too short (16)", 1, map[uint16][]byte{1: make([]byte, 16)}},
		{"key too short (31)", 1, map[uint16][]byte{1: make([]byte, 31)}},
		{"key too long (33)", 1, map[uint16][]byte{1: make([]byte, 33)}},
		{"key too long (64)", 1, map[uint16][]byte{1: make([]byte, 64)}},
		{"nil key", 1, map[uint16][]byte{1: nil}},
		{"one good one bad", 1, map[uint16][]byte{1: key(1), 2: make([]byte, 31)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if k, err := crypto.NewKeyring(tt.active, tt.keys); err == nil {
				t.Fatalf("NewKeyring accepted bad input, got %v", k)
			}
		})
	}
}

func TestKeyring_DoesNotAliasCallerKeyMaterial(t *testing.T) {
	material := key(3)
	k, err := crypto.NewKeyring(1, map[uint16][]byte{1: material})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := k.Seal([]byte("secret"), nil)
	for i := range material { // caller scribbles over its slice afterwards
		material[i] = 0
	}
	if got, err := k.Open(env, nil); err != nil || string(got) != "secret" {
		t.Fatalf("ring depended on caller-owned key bytes: %q, %v", got, err)
	}
}

func TestKeyring_PropertyRoundTripAndTamper(t *testing.T) {
	k := newRing(t, 1, 1)
	rapid.Check(t, func(t *rapid.T) {
		pt := rapid.SliceOfN(rapid.Byte(), 0, 512).Draw(t, "plaintext")
		aad := rapid.SliceOfN(rapid.Byte(), 0, 64).Draw(t, "aad")
		env, err := k.Seal(pt, aad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := k.Open(env, aad)
		if err != nil || !bytes.Equal(got, pt) {
			t.Fatalf("round trip failed: %v", err)
		}
		i := rapid.IntRange(0, len(env)-1).Draw(t, "flip index")
		env[i] ^= rapid.Byte().Filter(func(b byte) bool { return b != 0 }).Draw(t, "flip mask")
		if _, err := k.Open(env, aad); err == nil {
			t.Fatalf("tampered envelope (byte %d) opened", i)
		}
	})
}

func TestKeyring_RandomKeysRoundTrip(t *testing.T) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	k, err := crypto.NewKeyring(65535, map[uint16][]byte{65535: raw})
	if err != nil {
		t.Fatalf("highest key id rejected: %v", err)
	}
	env, _ := k.Seal([]byte("x"), nil)
	if id, _ := k.KeyID(env); id != 65535 {
		t.Errorf("KeyID = %d, want 65535", id)
	}
}
