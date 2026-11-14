package tickauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
)

// NewDev accepts "Bearer <token>" and compares it in constant time. An empty token rejects
// everything. It is constructed only when config allows it (APP_ENV local or ci).
func NewDev(token string) Authenticator {
	return dev{want: sha256.Sum256([]byte(token)), enabled: token != ""}
}

type dev struct {
	want    [sha256.Size]byte // digest, so the comparison does not depend on the token length
	enabled bool
}

func (d dev) Authenticate(_ context.Context, header string) error {
	got, ok := bearerToken(header)
	if !ok || !d.enabled {
		return ErrUnauthenticated
	}
	sum := sha256.Sum256([]byte(got))
	if subtle.ConstantTimeCompare(sum[:], d.want[:]) != 1 {
		return ErrUnauthenticated
	}
	return nil
}
