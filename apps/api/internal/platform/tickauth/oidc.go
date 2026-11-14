package tickauth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	issuerGoogle = "https://accounts.google.com"
	jwksGoogle   = "https://www.googleapis.com/oauth2/v3/certs"
)

// OIDCOption customises NewOIDC.
type OIDCOption func(*oidcConfig)

type oidcConfig struct {
	jwksURL string
	client  *http.Client
}

// WithJWKSURL overrides Google's key set URL. Tests only (httptest server with a local RSA key).
func WithJWKSURL(url string) OIDCOption { return func(c *oidcConfig) { c.jwksURL = url } }

// WithHTTPClient sets the HTTP client used to fetch the key set.
func WithHTTPClient(client *http.Client) OIDCOption { return func(c *oidcConfig) { c.client = client } }

// NewOIDC verifies a Google-signed ID token: RS256 signature against Google's JWKS, issuer
// (https://accounts.google.com or accounts.google.com), audience, expiry against the injected
// clock, then email == serviceAccount and email_verified == true (otherwise ErrForbidden).
// With an empty audience or service account it fails closed and rejects everything.
//
// ctx only carries configuration for the key set fetches (its cancellation is ignored by
// go-oidc); pass the process context.
func NewOIDC(ctx context.Context, audience, serviceAccount string, now func() time.Time, opts ...OIDCOption) Authenticator {
	if audience == "" || serviceAccount == "" {
		return Disabled()
	}
	cfg := oidcConfig{jwksURL: jwksGoogle}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.client != nil {
		ctx = oidc.ClientContext(ctx, cfg.client)
	}
	verifier := oidc.NewVerifier(issuerGoogle, oidc.NewRemoteKeySet(ctx, cfg.jwksURL), &oidc.Config{
		ClientID:             audience,
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  now,
	})
	return &oidcAuth{verifier: verifier, serviceAccount: serviceAccount}
}

type oidcAuth struct {
	verifier       *oidc.IDTokenVerifier
	serviceAccount string
}

func (a *oidcAuth) Authenticate(ctx context.Context, header string) error {
	raw, ok := bearerToken(header)
	if !ok {
		return ErrUnauthenticated
	}
	token, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		// go-oidc errors name claims and algorithms, never the token itself.
		return fmt.Errorf("%w: verify token: %w", ErrUnauthenticated, err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := token.Claims(&claims); err != nil {
		return fmt.Errorf("%w: decode claims: %w", ErrUnauthenticated, err)
	}
	if !claims.EmailVerified || claims.Email != a.serviceAccount {
		return fmt.Errorf("%w: token is for another identity", ErrForbidden)
	}
	return nil
}
