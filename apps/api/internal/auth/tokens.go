package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

const (
	tokenIssuer    = "swastik-api"
	tokenAudience  = "swastik-api"
	tokenType      = "at+jwt"
	accessLifetime = 15 * time.Minute
	maxTokenLength = 4096
)

// ErrInvalidToken wraps every access-token verification failure. It never contains the token.
var ErrInvalidToken = errors.New("auth: invalid access token")

// SigningKeys is the Ed25519 key ring: every key verifies, only the active one signs.
type SigningKeys struct {
	active string
	priv   map[string]ed25519.PrivateKey
}

// NewSigningKeys builds the ring from kid -> 32-byte Ed25519 seed. Seeds are copied.
func NewSigningKeys(activeKID string, seeds map[string][]byte) (*SigningKeys, error) {
	if len(seeds) == 0 {
		return nil, errors.New("auth: signing key ring is empty")
	}
	priv := make(map[string]ed25519.PrivateKey, len(seeds))
	for kid, seed := range seeds {
		if kid == "" {
			return nil, errors.New("auth: signing key id is empty")
		}
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("auth: signing key %q: seed must be %d bytes", kid, ed25519.SeedSize)
		}
		priv[kid] = ed25519.NewKeyFromSeed(seed) // copies the seed
	}
	if _, ok := priv[activeKID]; !ok {
		return nil, errors.New("auth: active signing key id is not in the ring")
	}
	return &SigningKeys{active: activeKID, priv: priv}, nil
}

// AccessSubject is what an access token asserts.
type AccessSubject struct {
	UserID, SessionID, OrgID uuid.UUID
	Role                     string // "admin" | "client"
}

// AccessClaims are the verified claims of an access token.
type AccessClaims struct {
	UserID, SessionID, OrgID uuid.UUID
	Role                     string
	IssuedAt, ExpiresAt      time.Time // whole seconds, UTC
	ID                       string    // jti
}

// Tokens issues and verifies access tokens (EdDSA JWTs, plan P-02). go-jose is confined to this
// file.
type Tokens struct {
	keys *SigningKeys
	now  func() time.Time
}

// NewTokens builds the issuer/verifier. now is the clock; it is read on every call.
func NewTokens(keys *SigningKeys, now func() time.Time) *Tokens {
	return &Tokens{keys: keys, now: now}
}

type customClaims struct {
	SessionID string `json:"sid"`
	OrgID     string `json:"org"`
	Role      string `json:"role"`
}

// Issue signs an access token that expires at min(now+15m, absoluteExpiry), in whole seconds.
func (t *Tokens) Issue(sub AccessSubject, absoluteExpiry time.Time) (string, error) {
	now := t.now()
	iat := now.Unix()
	exp := min(now.Add(accessLifetime).Unix(), absoluteExpiry.Unix())
	if exp <= iat {
		return "", errors.New("auth: session has no time left for an access token")
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("auth: jti: %w", err)
	}
	kid := t.keys.active
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: t.keys.priv[kid], KeyID: kid}},
		(&jose.SignerOptions{}).WithType(tokenType),
	)
	if err != nil {
		return "", fmt.Errorf("auth: signer: %w", err)
	}
	std := jwt.Claims{
		Issuer:   tokenIssuer,
		Audience: jwt.Audience{tokenAudience},
		Subject:  sub.UserID.String(),
		IssuedAt: jwt.NewNumericDate(time.Unix(iat, 0)),
		Expiry:   jwt.NewNumericDate(time.Unix(exp, 0)),
		ID:       base64.RawURLEncoding.EncodeToString(jti),
	}
	custom := customClaims{SessionID: sub.SessionID.String(), OrgID: sub.OrgID.String(), Role: sub.Role}
	raw, err := jwt.Signed(signer).Claims(std).Claims(custom).Serialize()
	if err != nil {
		return "", fmt.Errorf("auth: sign: %w", err)
	}
	return raw, nil
}

// Verify checks an access token in the order of plan P-02. Every failure wraps ErrInvalidToken.
func (t *Tokens) Verify(raw string) (AccessClaims, error) {
	claims, err := t.verify(raw)
	if err != nil {
		return AccessClaims{}, fmt.Errorf("%w", ErrInvalidToken)
	}
	return claims, nil
}

func (t *Tokens) verify(raw string) (AccessClaims, error) {
	if raw == "" || len(raw) > maxTokenLength {
		return AccessClaims{}, errors.New("size")
	}
	// 1. Algorithm allowlist: none, HS* and RS* are rejected before any key is touched.
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return AccessClaims{}, fmt.Errorf("parse: %w", err)
	}
	// 2. One signature; the key comes only from the configured ring by kid. jku, jwk and x5u
	// headers are never consulted.
	if len(tok.Headers) != 1 {
		return AccessClaims{}, errors.New("signatures")
	}
	hdr := tok.Headers[0]
	priv, ok := t.keys.priv[hdr.KeyID]
	if hdr.KeyID == "" || !ok {
		return AccessClaims{}, errors.New("kid")
	}
	// 3. go-jose does not check typ.
	if typ, _ := hdr.ExtraHeaders[jose.HeaderType].(string); typ != tokenType {
		return AccessClaims{}, errors.New("typ")
	}
	// 4. Signature.
	var std jwt.Claims
	var custom customClaims
	if err := tok.Claims(priv.Public().(ed25519.PublicKey), &std, &custom); err != nil {
		return AccessClaims{}, fmt.Errorf("signature: %w", err)
	}
	// 5. Standard claims against the injected clock (never time.Now), no leeway.
	now := t.now()
	if err := std.ValidateWithLeeway(jwt.Expected{
		Issuer:      tokenIssuer,
		AnyAudience: jwt.Audience{tokenAudience},
		Time:        now,
	}, 0); err != nil {
		return AccessClaims{}, fmt.Errorf("claims: %w", err)
	}
	// 6. What go-jose leaves to the caller: exp and iat present, and now < exp (go-jose accepts
	// a token at exactly exp).
	if std.Expiry == nil || std.IssuedAt == nil || !now.Before(std.Expiry.Time()) {
		return AccessClaims{}, errors.New("time")
	}
	if custom.Role != "admin" && custom.Role != "client" {
		return AccessClaims{}, errors.New("role")
	}
	userID, err1 := uuid.Parse(std.Subject)
	sessionID, err2 := uuid.Parse(custom.SessionID)
	orgID, err3 := uuid.Parse(custom.OrgID)
	if err := errors.Join(err1, err2, err3); err != nil {
		return AccessClaims{}, fmt.Errorf("ids: %w", err)
	}
	return AccessClaims{
		UserID: userID, SessionID: sessionID, OrgID: orgID, Role: custom.Role,
		IssuedAt: std.IssuedAt.Time().UTC(), ExpiresAt: std.Expiry.Time().UTC(), ID: std.ID,
	}, nil
}
