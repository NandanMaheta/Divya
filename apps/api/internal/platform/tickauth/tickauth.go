// Package tickauth authenticates callers of the scheduler tick endpoint (feature 002, FR-002,
// FR-003). Production verifies a Google-signed OIDC ID token (oidc.go); local development and CI
// use a shared bearer token (dev.go). It knows nothing about HTTP: the handler passes the raw
// Authorization header and maps the two sentinel errors to 401 and 403.
package tickauth

import (
	"context"
	"errors"
	"strings"
)

// ErrUnauthenticated means the credentials are missing, malformed or invalid (HTTP 401).
var ErrUnauthenticated = errors.New("tickauth: unauthenticated")

// ErrForbidden means the credentials are valid but belong to another identity (HTTP 403).
var ErrForbidden = errors.New("tickauth: forbidden")

// Authenticator checks the value of an Authorization header. It returns nil to accept, an error
// wrapping ErrUnauthenticated or ErrForbidden to reject. Errors never contain the credentials.
type Authenticator interface {
	Authenticate(ctx context.Context, authorizationHeader string) error
}

// Disabled rejects every request with ErrUnauthenticated. It is used when OIDC mode has no
// audience or service account configured, so the tick endpoint is closed rather than open.
func Disabled() Authenticator { return disabled{} }

type disabled struct{}

func (disabled) Authenticate(context.Context, string) error { return ErrUnauthenticated }

// bearerToken returns the token of an "Authorization: Bearer <token>" value.
func bearerToken(header string) (string, bool) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return "", false
	}
	return token, true
}
