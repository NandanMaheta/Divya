package auth

import (
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

// RecoveryCodeCount is how many recovery codes an admin receives.
const RecoveryCodeCount = 10

// crockford is Crockford base32 without I, L, O and U.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GenerateRecoveryCodes returns RecoveryCodeCount codes of the form XXXX-XXXX-XXXX-XXXX, each 80
// random bits. They are shown once; only digests are stored.
func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, RecoveryCodeCount)
	for i := range codes {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("auth: recovery code: %w", err)
		}
		var b strings.Builder
		for j, v := range raw {
			if j > 0 && j%4 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(crockford[v&31]) // 256 is a multiple of 32: no modulo bias
		}
		codes[i] = b.String()
	}
	return codes, nil
}

// NormalizeRecoveryCode upper-cases, removes spaces and hyphens and maps the look-alikes O to 0 and
// I, L to 1. It does not validate length.
func NormalizeRecoveryCode(input string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(input) {
		switch r {
		case ' ', '-':
		case 'O':
			b.WriteByte('0')
		case 'I', 'L':
			b.WriteByte('1')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RecoveryCodeDigest is the SHA-256 of the normalised code.
func RecoveryCodeDigest(input string) []byte {
	return crypto.Digest(NormalizeRecoveryCode(input))
}
