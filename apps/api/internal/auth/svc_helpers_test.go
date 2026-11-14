// Shared helpers of the S11/S13 service tests (specs/003-auth-core/plan.md P-03, P-07, P-08).
// Everything runs on the real Store and a real PostgreSQL through authtest, with the fake clock.
package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

const wrongPassword = "not-the-right-one-77"

// svPolicy is the sign-in lockout of D-08.
var svPolicy = auth.ThrottlePolicy{MaxFailures: 5, Window: 15 * time.Minute, Lock: 15 * time.Minute}

// svInput is a valid mobile-client sign-in of u by email from the given installation.
func svInput(u authtest.User, device uuid.UUID) auth.SignInInput {
	return auth.SignInInput{
		Identifier: u.Email, Password: u.Password, ClientType: auth.ClientTypeMobile,
		Device: auth.DeviceInfo{ID: device, Name: "Pixel 9", Platform: auth.PlatformAndroid}, IP: "203.0.113.7",
	}
}

// svSignIn signs u in from a new installation and fails the test unless it reaches a session.
func svSignIn(t *testing.T, s *auth.Service, u authtest.User, device uuid.UUID) auth.SessionTokens {
	t.Helper()
	res, err := s.SignIn(t.Context(), svInput(u, device))
	if err != nil {
		t.Fatalf("SignIn(%s): %v", u.Email, err)
	}
	if res.NextStep != auth.NextStepSignedIn || res.Session == nil {
		t.Fatalf("SignIn next step = %q session=%v, want signed_in with a session", res.NextStep, res.Session)
	}
	return *res.Session
}

// svWrong makes one wrong-password attempt and requires the generic failure.
func svWrong(t *testing.T, s *auth.Service, u authtest.User) {
	t.Helper()
	in := svInput(u, uuid.New())
	in.Password = wrongPassword
	if _, err := s.SignIn(t.Context(), in); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong password: err = %v, want ErrInvalidCredentials", err)
	}
}

// svCount runs a COUNT query.
func svCount(t *testing.T, e *authtest.Env, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func svLiveSessions(t *testing.T, e *authtest.Env, user uuid.UUID) int {
	t.Helper()
	return svCount(t, e, `SELECT count(*) FROM device_sessions WHERE user_id = $1 AND ended_at IS NULL`, user)
}

func svAllSessions(t *testing.T, e *authtest.Env, user uuid.UUID) int {
	t.Helper()
	return svCount(t, e, `SELECT count(*) FROM device_sessions WHERE user_id = $1`, user)
}

func svUserCount(t *testing.T, e *authtest.Env) int {
	t.Helper()
	return svCount(t, e, `SELECT count(*) FROM users`)
}

// svSubject is the stored throttle subject of a key ("user|<id>", "email|<x>", "mobile|<x>").
func svSubject(e *authtest.Env, scope, key string) []byte {
	return e.Blind.Index("throttle", []byte(scope+"|"+key))
}

// svThrottle reads the sign-in throttle state of a key straight from the Store.
func svThrottle(t *testing.T, e *authtest.Env, key string) auth.ThrottleState {
	t.Helper()
	st, err := e.Store.GetThrottle(t.Context(), e.Org, "signin", svSubject(e, "signin", key))
	if err != nil {
		t.Fatalf("GetThrottle(%s): %v", key, err)
	}
	return st
}

func svUserKey(u authtest.User) string { return "user|" + u.ID.String() }

func svUserRow(t *testing.T, e *authtest.Env, id uuid.UUID) auth.UserRow {
	t.Helper()
	u, err := e.Store.GetUser(t.Context(), id)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	return u
}

func svSession(t *testing.T, e *authtest.Env, id uuid.UUID) auth.SessionRow {
	t.Helper()
	s, err := e.Store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	return s
}

// svRefreshRow finds a refresh token by its raw value.
func svRefreshRow(t *testing.T, e *authtest.Env, raw string) auth.RefreshRow {
	t.Helper()
	r, err := e.Store.FindRefreshByHash(t.Context(), crypto.Digest(raw))
	if err != nil {
		t.Fatalf("FindRefreshByHash: %v", err)
	}
	return r
}
