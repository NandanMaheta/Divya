package auth_test

// Helpers of the S13 service tests (plan P-02, P-03).

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/authz"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

// svFixtureSession inserts a live session by SQL plus one unused refresh token and returns both.
// It is used where a role without a sign-in path yet (admin) or a custom lifetime is needed.
func svFixtureSession(t *testing.T, e *authtest.Env, user uuid.UUID, spec authtest.SessionSpec) (session uuid.UUID, device uuid.UUID, refresh string) {
	t.Helper()
	if spec.DeviceID == uuid.Nil {
		spec.DeviceID = uuid.New()
	}
	session = e.NewSession(t, user, spec)
	plain, digest, err := crypto.NewToken("drt_")
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	e.Secrets.Add("fixture refresh token", plain)
	stInTx(t, e, func(ctx context.Context) error {
		_, err := e.Store.InsertRefresh(ctx, e.Org, session, digest, e.Clock.Now())
		return err
	})
	return session, spec.DeviceID, plain
}

// svVerify runs a verifier on a token and returns its error.
func svVerify(t *testing.T, v authz.Verifier, token string) error {
	t.Helper()
	_, err := v.Verify(t.Context(), token)
	return err
}

// svRefreshIn is a refresh by the mobile transport (token in the body).
func svRefreshIn(device uuid.UUID, token string) auth.RefreshInput {
	return auth.RefreshInput{DeviceID: device, RefreshToken: token, ClientType: auth.ClientTypeMobile, IP: testIP}
}

// svPrincipal is the principal of a client session as the bearer verifier would produce it.
func svPrincipal(e *authtest.Env, u authtest.User, session uuid.UUID) authz.Principal {
	return authz.Principal{UserID: u.ID, OrgID: e.Org, SessionID: session, Role: authz.Role(u.Role)}
}

// svTokens issues and verifies access tokens with the fixture key ring on the fake clock.
func svTokens(e *authtest.Env) *auth.Tokens { return auth.NewTokens(e.Signing, e.Clock.Now) }

// svUnusedRefresh counts the session's unused refresh tokens.
func svUnusedRefresh(t *testing.T, e *authtest.Env, session uuid.UUID) int {
	t.Helper()
	return svCount(t, e, `SELECT count(*) FROM refresh_tokens WHERE session_id = $1 AND used_at IS NULL`, session)
}
