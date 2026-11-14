// Property tests for the Argon2id hasher with cheap parameters (T020; FR-010, plan P-06).
package auth_test

import (
	"context"
	"testing"

	"pgregory.net/rapid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
)

func TestHasher_PropertyPHCRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		threads := rapid.IntRange(1, 4).Draw(t, "threads")
		p := auth.HasherParams{
			MemoryKiB: uint32(rapid.IntRange(8*threads, 262144).Draw(t, "memory")),
			Time:      uint32(rapid.IntRange(1, 10).Draw(t, "time")),
			Threads:   uint8(threads),
		}
		salt := rapid.SliceOfN(rapid.Byte(), 8, 32).Draw(t, "salt")
		key := rapid.SliceOfN(rapid.Byte(), 16, 64).Draw(t, "key")
		phc := auth.EncodePHC(p, salt, key)
		gp, gs, gk, err := auth.DecodePHC(phc)
		if err != nil {
			t.Fatalf("DecodePHC(%q): %v", phc, err)
		}
		if gp != p || string(gs) != string(salt) || string(gk) != string(key) {
			t.Fatalf("round trip changed the value: %+v %x %x", gp, gs, gk)
		}
		if auth.EncodePHC(gp, gs, gk) != phc {
			t.Fatal("encode(decode(phc)) != phc")
		}
	})
}

func TestHasher_PropertyDecodeNeverPanicsOnArbitraryInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "phc")
		_, _, _, _ = auth.DecodePHC(s) // must return an error or a value, never panic or allocate by parameter
	})
}

func TestHasher_PropertyHashThenVerify(t *testing.T) {
	h := auth.NewHasher(auth.HasherOptions{Params: cheap})
	rapid.Check(t, func(t *rapid.T) {
		pw := rapid.StringN(0, 64, 256).Draw(t, "password")
		phc, err := h.Hash(context.Background(), pw)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := h.Verify(context.Background(), phc, pw); err != nil || !ok {
			t.Fatalf("own hash did not verify: %v, %v", ok, err)
		}
		if ok, _ := h.Verify(context.Background(), phc, pw+"x"); ok {
			t.Fatal("a different password verified")
		}
		if h.NeedsRehash(phc) {
			t.Fatal("fresh hash flagged for rehash")
		}
	})
}
