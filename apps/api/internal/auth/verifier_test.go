package auth_test

// S13 - Service tests B, the two authz.Verifier implementations (plan P-02, P-09; US-2 s5-10,
// FR-031, FR-035, D-05). Real Store, fake clock. Tokens are minted with auth.Tokens on the fixture
// key ring so each rejection reason is isolated.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/authz"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

// svChallenge stores a challenge of the admin and returns its raw token.
func svChallenge(t *testing.T, e *authtest.Env, admin uuid.UUID, purpose string) string {
	t.Helper()
	plain, digest, err := crypto.NewToken("dmc_")
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	e.Secrets.Add("challenge token", plain)
	stInTx(t, e, func(ctx context.Context) error {
		_, err := e.Store.CreateChallenge(ctx, auth.NewChallenge{
			OrgID: e.Org, UserID: admin, DeviceID: uuid.New(), TokenHash: digest, Purpose: purpose,
			DeviceName: "Chrome", Platform: "web", ClientType: "web", Now: e.Clock.Now(), ExpiresAt: e.Clock.Now().Add(5 * time.Minute),
		})
		return err
	})
	return plain
}

func TestVerifier_BearerAcceptsAValidClientToken(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	tok := svSignIn(t, s, u, uuid.New())
	p, err := s.BearerVerifier().Verify(t.Context(), tok.AccessToken)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := authz.Principal{UserID: u.ID, OrgID: e.Org, SessionID: tok.SessionID, Role: authz.RoleClient}
	if p != want {
		t.Errorf("principal = %+v, want %+v", p, want)
	}
}

func TestVerifier_BearerAcceptsAnAdminTokenAsAdmin(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	admin := e.NewAdmin(t)
	sess, _, _ := svFixtureSession(t, e, admin.ID, authtest.SessionSpec{MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * day})
	raw, err := svTokens(e).Issue(auth.AccessSubject{UserID: admin.ID, SessionID: sess, OrgID: e.Org, Role: "admin"}, e.Clock.Now().Add(7*day))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.BearerVerifier().Verify(t.Context(), raw)
	if err != nil || p.Role != authz.RoleAdmin || p.UserID != admin.ID || p.SessionID != sess {
		t.Errorf("principal = %+v, %v", p, err)
	}
}

func TestVerifier_BearerNeverWritesAndNeverSlidesTheSession(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	tok := svSignIn(t, s, u, uuid.New())
	before := svSession(t, e, tok.SessionID)
	auditBefore := len(e.AuditRows(t))
	e.Clock.Advance(5 * time.Minute)
	for range 3 {
		if err := svVerify(t, s.BearerVerifier(), tok.AccessToken); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	after := svSession(t, e, tok.SessionID)
	if !after.LastActiveAt.Equal(before.LastActiveAt) || !after.IdleExpiresAt.Equal(before.IdleExpiresAt) {
		t.Errorf("a request slid the session: %+v -> %+v (idle is measured from sign-in or refresh only, D-06)", before, after)
	}
	if got := len(e.AuditRows(t)); got != auditBefore {
		t.Errorf("audit rows = %d, want %d: authentication writes nothing", got, auditBefore)
	}
}

func TestVerifier_BearerRejections(t *testing.T) {
	type rejection struct {
		name  string
		build func(t *testing.T, e *authtest.Env, s *auth.Service) (token string, at time.Time)
	}
	// mint issues an access token for u's session sess valid until abs.
	mint := func(t *testing.T, e *authtest.Env, u authtest.User, sess uuid.UUID, role string, abs time.Duration) string {
		t.Helper()
		raw, err := svTokens(e).Issue(auth.AccessSubject{UserID: u.ID, SessionID: sess, OrgID: e.Org, Role: role}, e.Clock.Now().Add(abs))
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		return raw
	}
	now := authtest.T0
	tests := []rejection{
		{"empty token", func(*testing.T, *authtest.Env, *auth.Service) (string, time.Time) { return "", now }},
		{"not a token", func(*testing.T, *authtest.Env, *auth.Service) (string, time.Time) { return "garbage", now }},
		{"one character changed", func(t *testing.T, e *authtest.Env, s *auth.Service) (string, time.Time) {
			tok := svSignIn(t, s, e.UserWithStatus(t, auth.StatusActive), uuid.New())
			raw := tok.AccessToken
			last := raw[len(raw)-1]
			repl := byte('A')
			if last == 'A' {
				repl = 'B'
			}
			return raw[:len(raw)-1] + string(repl), now
		}},
		{"refresh token presented as access token", func(t *testing.T, e *authtest.Env, s *auth.Service) (string, time.Time) {
			return svSignIn(t, s, e.UserWithStatus(t, auth.StatusActive), uuid.New()).RefreshToken, now
		}},
		{"challenge token presented as access token", func(t *testing.T, e *authtest.Env, _ *auth.Service) (string, time.Time) {
			return svChallenge(t, e, e.NewAdmin(t).ID, "verify"), now
		}},
		{"signed by an unknown key", func(t *testing.T, e *authtest.Env, _ *auth.Service) (string, time.Time) {
			u := e.UserWithStatus(t, auth.StatusActive)
			sess := e.NewSession(t, u.ID, authtest.SessionSpec{})
			other, err := auth.NewSigningKeys("x", map[string][]byte{"x": []byte(strings.Repeat("z", 32))})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := auth.NewTokens(other, e.Clock.Now).Issue(auth.AccessSubject{UserID: u.ID, SessionID: sess, OrgID: e.Org, Role: "client"}, now.Add(day))
			if err != nil {
				t.Fatal(err)
			}
			return raw, now
		}},
		{"expired exactly at exp", func(t *testing.T, e *authtest.Env, s *auth.Service) (string, time.Time) {
			tok := svSignIn(t, s, e.UserWithStatus(t, auth.StatusActive), uuid.New())
			return tok.AccessToken, tok.AccessTokenExpiresAt
		}},
		{"unknown session id", func(t *testing.T, e *authtest.Env, _ *auth.Service) (string, time.Time) {
			return mint(t, e, e.UserWithStatus(t, auth.StatusActive), uuid.New(), "client", day), now
		}},
		{"subject is not the session's user", func(t *testing.T, e *authtest.Env, _ *auth.Service) (string, time.Time) {
			owner, intruder := e.UserWithStatus(t, auth.StatusActive), e.UserWithStatus(t, auth.StatusActive)
			sess := e.NewSession(t, owner.ID, authtest.SessionSpec{})
			return mint(t, e, intruder, sess, "client", day), now
		}},
		{"role claim is not the user's role", func(t *testing.T, e *authtest.Env, _ *auth.Service) (string, time.Time) {
			u := e.UserWithStatus(t, auth.StatusActive)
			return mint(t, e, u, e.NewSession(t, u.ID, authtest.SessionSpec{}), "admin", day), now
		}},
		{"session ended", func(t *testing.T, e *authtest.Env, s *auth.Service) (string, time.Time) {
			u := e.UserWithStatus(t, auth.StatusActive)
			tok := svSignIn(t, s, u, uuid.New())
			if err := s.SignOut(t.Context(), svPrincipal(e, u, tok.SessionID)); err != nil {
				t.Fatal(err)
			}
			return tok.AccessToken, now.Add(time.Second)
		}},
		{"idle limit reached exactly", func(t *testing.T, e *authtest.Env, _ *auth.Service) (string, time.Time) {
			u := e.UserWithStatus(t, auth.StatusActive)
			sess := e.NewSession(t, u.ID, authtest.SessionSpec{Idle: 5 * time.Minute})
			return mint(t, e, u, sess, "client", day), now.Add(5 * time.Minute)
		}},
		{"absolute limit reached exactly", func(t *testing.T, e *authtest.Env, _ *auth.Service) (string, time.Time) {
			u := e.UserWithStatus(t, auth.StatusActive)
			sess := e.NewSession(t, u.ID, authtest.SessionSpec{Absolute: 10 * time.Minute})
			return mint(t, e, u, sess, "client", day), now.Add(10 * time.Minute)
		}},
		{"BR_CLI_04 user disabled while the session is live", func(t *testing.T, e *authtest.Env, s *auth.Service) (string, time.Time) {
			u := e.UserWithStatus(t, auth.StatusActive)
			tok := svSignIn(t, s, u, uuid.New())
			e.Exec(t, `UPDATE users SET status = 'disabled', status_before_disabled = 'active' WHERE id = $1`, u.ID)
			return tok.AccessToken, now.Add(time.Second)
		}},
		{"BR_CLI_04 user not active (pending_approval) while the session is live", func(t *testing.T, e *authtest.Env, s *auth.Service) (string, time.Time) {
			u := e.UserWithStatus(t, auth.StatusActive)
			tok := svSignIn(t, s, u, uuid.New())
			e.Exec(t, `UPDATE users SET status = 'pending_approval' WHERE id = $1`, u.ID)
			return tok.AccessToken, now.Add(time.Second)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			// Control: a healthy token verifies, so a rejection below is about the case.
			ctl := svSignIn(t, s, e.UserWithStatus(t, auth.StatusActive), uuid.New())
			if err := svVerify(t, s.BearerVerifier(), ctl.AccessToken); err != nil {
				t.Fatalf("control token rejected: %v", err)
			}
			token, at := tt.build(t, e, s)
			e.Clock.Set(at)
			p, err := s.BearerVerifier().Verify(t.Context(), token)
			if err == nil {
				t.Fatalf("Verify accepted the token, principal %+v", p)
			}
			if p != (authz.Principal{}) {
				t.Errorf("a rejected token must yield the zero principal, got %+v", p)
			}
		})
	}
}

func TestVerifier_BearerAcceptsJustInsideEveryLimit(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	idle := e.NewSession(t, u.ID, authtest.SessionSpec{Idle: 5 * time.Minute})
	abs := e.NewSession(t, u.ID, authtest.SessionSpec{Absolute: 10 * time.Minute})
	mint := func(sess uuid.UUID) string {
		raw, err := svTokens(e).Issue(auth.AccessSubject{UserID: u.ID, SessionID: sess, OrgID: e.Org, Role: "client"}, e.Clock.Now().Add(day))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	tIdle, tAbs := mint(idle), mint(abs)
	e.Clock.Set(authtest.T0.Add(5*time.Minute - time.Second))
	if err := svVerify(t, s.BearerVerifier(), tIdle); err != nil {
		t.Errorf("1 s before the idle limit: %v", err)
	}
	e.Clock.Set(authtest.T0.Add(10*time.Minute - time.Second))
	if err := svVerify(t, s.BearerVerifier(), tAbs); err != nil {
		t.Errorf("1 s before the absolute limit: %v", err)
	}
	// And a token is valid until its own expiry.
	tok := svSignIn(t, s, u, uuid.New())
	e.Clock.Set(tok.AccessTokenExpiresAt.Add(-time.Second))
	if err := svVerify(t, s.BearerVerifier(), tok.AccessToken); err != nil {
		t.Errorf("1 s before exp: %v", err)
	}
}

func TestVerifier_ChallengeAcceptsAFreshChallengeWithoutConsumingIt(t *testing.T) {
	for _, purpose := range []string{"verify", "enrol"} {
		t.Run(purpose, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			admin := e.NewAdmin(t)
			raw := svChallenge(t, e, admin.ID, purpose)
			want := authz.Principal{UserID: admin.ID, OrgID: e.Org, SessionID: uuid.Nil, Role: authz.RoleAdmin}
			for i := range 2 {
				p, err := s.ChallengeVerifier().Verify(t.Context(), raw)
				if err != nil || p != want {
					t.Fatalf("Verify #%d = %+v, %v; want %+v (verification must not consume; completion does)", i+1, p, err, want)
				}
			}
			row, err := e.Store.FindChallengeByHash(t.Context(), crypto.Digest(raw))
			if err != nil || row.ConsumedAt != nil {
				t.Errorf("challenge row = %+v, %v; want unconsumed", row, err)
			}
		})
	}
}

func TestVerifier_ChallengeExpiresAtExactlyFiveMinutes(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	raw := svChallenge(t, e, e.NewAdmin(t).ID, "verify")
	e.Clock.Set(authtest.T0.Add(4*time.Minute + 59*time.Second))
	if err := svVerify(t, s.ChallengeVerifier(), raw); err != nil {
		t.Errorf("at 4m59s: %v", err)
	}
	e.Clock.Set(authtest.T0.Add(5 * time.Minute))
	if err := svVerify(t, s.ChallengeVerifier(), raw); err == nil {
		t.Errorf("at exactly 5m the proof is expired")
	}
	e.Clock.Set(authtest.T0.Add(5*time.Minute + time.Second))
	if err := svVerify(t, s.ChallengeVerifier(), raw); err == nil {
		t.Errorf("at 5m1s the proof is expired")
	}
}

func TestVerifier_ChallengeRejections(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, e *authtest.Env, s *auth.Service) string
	}{
		{"empty", func(*testing.T, *authtest.Env, *auth.Service) string { return "" }},
		{"unknown challenge", func(t *testing.T, e *authtest.Env, _ *auth.Service) string {
			plain, _, err := crypto.NewToken("dmc_")
			if err != nil {
				t.Fatal(err)
			}
			return plain
		}},
		{"already consumed", func(t *testing.T, e *authtest.Env, _ *auth.Service) string {
			raw := svChallenge(t, e, e.NewAdmin(t).ID, "verify")
			row, err := e.Store.FindChallengeByHash(t.Context(), crypto.Digest(raw))
			if err != nil {
				t.Fatal(err)
			}
			stInTx(t, e, func(ctx context.Context) error {
				ok, err := e.Store.ConsumeChallenge(ctx, row.ID, e.Clock.Now())
				if err == nil && !ok {
					t.Fatalf("precondition: consume")
				}
				return err
			})
			return raw
		}},
		{"one character changed", func(t *testing.T, e *authtest.Env, _ *auth.Service) string {
			raw := svChallenge(t, e, e.NewAdmin(t).ID, "verify")
			last := raw[len(raw)-1]
			repl := byte('A')
			if last == 'A' {
				repl = 'B'
			}
			return raw[:len(raw)-1] + string(repl)
		}},
		{"access token presented as a challenge", func(t *testing.T, e *authtest.Env, s *auth.Service) string {
			return svSignIn(t, s, e.UserWithStatus(t, auth.StatusActive), uuid.New()).AccessToken
		}},
		{"refresh token presented as a challenge", func(t *testing.T, e *authtest.Env, s *auth.Service) string {
			return svSignIn(t, s, e.UserWithStatus(t, auth.StatusActive), uuid.New()).RefreshToken
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			// Control: a fresh challenge verifies, so a rejection below is about the case.
			if err := svVerify(t, s.ChallengeVerifier(), svChallenge(t, e, e.NewAdmin(t).ID, "verify")); err != nil {
				t.Fatalf("control challenge rejected: %v", err)
			}
			raw := tt.build(t, e, s)
			if p, err := s.ChallengeVerifier().Verify(t.Context(), raw); err == nil {
				t.Fatalf("Verify accepted it, principal %+v", p)
			}
		})
	}
}
