package auth_test

// S13 - Service tests B, refresh (plan P-03; US-3 s1-11; FR-040..FR-043, D-06, D-10, D-11, SC-004,
// SC-005, BR-CLI-04). Real Store, fake clock; every boundary is tested at +-1 minute and at the
// exact instant (a limit is inclusive of expiry: at the expiry instant the session is expired).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

const day = 24 * time.Hour

func TestRefresh_RotatesR1ToR2(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	tok := svSignIn(t, s, u, device)
	e.Secrets.Add("refresh R1", tok.RefreshToken)
	sess0 := svSession(t, e, tok.SessionID)

	e.Clock.Advance(time.Hour)
	now := e.Clock.Now()
	res, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
	if err != nil {
		t.Fatalf("Refresh(R1): %v", err)
	}
	e.Secrets.Add("refresh R2", res.RefreshToken)
	if res.RefreshToken == tok.RefreshToken || !strings.HasPrefix(res.RefreshToken, "drt_") || res.SessionID != tok.SessionID {
		t.Fatalf("response = %+v, want a new drt_ token on the same session", res)
	}
	claims, err := svTokens(e).Verify(res.AccessToken)
	if err != nil || claims.SessionID != tok.SessionID || claims.UserID != u.ID || !claims.ExpiresAt.Equal(now.Add(15*time.Minute)) {
		t.Errorf("new access token claims = %+v, %v", claims, err)
	}
	if res.User.ID != u.ID || !res.SessionExpiresAt.Equal(sess0.AbsoluteExpiresAt) || !res.IdleExpiresAt.Equal(now.Add(30*day)) {
		t.Errorf("response session fields = %+v", res)
	}

	r1, r2 := svRefreshRow(t, e, tok.RefreshToken), svRefreshRow(t, e, res.RefreshToken)
	if r1.UsedAt == nil || !r1.UsedAt.Equal(now) || r1.ReplacedBy != r2.ID || r2.UsedAt != nil || r2.SessionID != tok.SessionID {
		t.Errorf("rows: R1 = %+v, R2 = %+v; want R1 used now and replaced_by R2, R2 unused", r1, r2)
	}
	if n := svUnusedRefresh(t, e, tok.SessionID); n != 1 {
		t.Errorf("unused tokens in the family = %d, want exactly 1", n)
	}
	sess := svSession(t, e, tok.SessionID)
	if !sess.LastActiveAt.Equal(now) || !sess.IdleExpiresAt.Equal(now.Add(30*day)) || !sess.AbsoluteExpiresAt.Equal(sess0.AbsoluteExpiresAt) || sess.EndedAt != nil {
		t.Errorf("session after refresh = %+v (absolute must not move)", sess)
	}

	rows := e.AuditOf(t, auth.ActionSessionRefreshed)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindClient || rows[0].ActorID != u.ID || rows[0].EntityID != u.ID ||
		svJSON(t, rows[0].After)["session_id"] != tok.SessionID.String() {
		t.Errorf("session.refreshed audit = %+v", rows)
	}

	e.Clock.Advance(time.Minute)
	if _, err := s.Refresh(t.Context(), svRefreshIn(device, res.RefreshToken)); err != nil {
		t.Errorf("Refresh(R2): %v", err)
	}
	e.Secrets.ScanAuditLog(t.Context(), t, e.Pool)
}

func TestRefresh_ReuseEndsTheWholeSessionButNotOthers(t *testing.T) {
	for _, fromDevice := range []string{"same installation", "another installation"} {
		t.Run(fromDevice, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, auth.StatusActive)
			device, otherDevice := uuid.New(), uuid.New()
			tok := svSignIn(t, s, u, device)
			other := svSignIn(t, s, u, otherDevice)

			r2, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
			if err != nil {
				t.Fatalf("Refresh(R1): %v", err)
			}
			presenter := device
			if fromDevice == "another installation" {
				presenter = uuid.New()
			}
			e.Clock.Advance(time.Minute)
			if _, err := s.Refresh(t.Context(), svRefreshIn(presenter, tok.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
				t.Fatalf("replayed R1: err = %v, want ErrRefreshRejected", err)
			}
			if ss := svSession(t, e, tok.SessionID); ss.EndedAt == nil || ss.EndReason != "refresh_reuse" || !ss.EndedAt.Equal(e.Clock.Now()) {
				t.Errorf("session after reuse: ended=%v reason=%q, want ended now with refresh_reuse", ss.EndedAt, ss.EndReason)
			}
			if _, err := s.Refresh(t.Context(), svRefreshIn(device, r2.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
				t.Errorf("R2 after reuse: err = %v, want ErrRefreshRejected (the whole family is dead)", err)
			}
			if err := svVerify(t, s.BearerVerifier(), r2.AccessToken); err == nil {
				t.Errorf("the session's access token must be rejected on the next request")
			}
			if err := svVerify(t, s.BearerVerifier(), other.AccessToken); err != nil {
				t.Errorf("the same user's other device must keep working: %v", err)
			}
			if svSession(t, e, other.SessionID).EndedAt != nil {
				t.Errorf("other session ended")
			}
			rows := e.AuditOf(t, auth.ActionRefreshReuseDetected)
			if len(rows) != 1 || rows[0].ActorKind != audit.KindSystem || rows[0].EntityID != u.ID || svJSON(t, rows[0].After)["session_id"] != tok.SessionID.String() {
				t.Errorf("reuse audit = %+v, want exactly one system row for the session", rows)
			}
			if got := len(e.AuditOf(t, auth.ActionDeviceMismatch)); got != 0 {
				t.Errorf("a used token is reuse even from another installation, got %d device_mismatch rows", got)
			}
		})
	}
}

func TestRefresh_DeviceMismatchEndsTheSession(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	tok := svSignIn(t, s, u, device)

	if _, err := s.Refresh(t.Context(), svRefreshIn(uuid.New(), tok.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
		t.Fatalf("err = %v, want ErrRefreshRejected", err)
	}
	if ss := svSession(t, e, tok.SessionID); ss.EndedAt == nil || ss.EndReason != "device_mismatch" {
		t.Errorf("session ended=%v reason=%q, want device_mismatch", ss.EndedAt, ss.EndReason)
	}
	if rr := svRefreshRow(t, e, tok.RefreshToken); rr.UsedAt != nil {
		t.Errorf("a mismatched presentation must not consume the token")
	}
	rows := e.AuditOf(t, auth.ActionDeviceMismatch)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindSystem || rows[0].EntityID != u.ID {
		t.Errorf("device_mismatch audit = %+v", rows)
	}
	if got := len(e.AuditOf(t, auth.ActionRefreshReuseDetected)); got != 0 {
		t.Errorf("reuse rows = %d, want 0", got)
	}
	// The genuine installation cannot revive it.
	if _, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
		t.Errorf("genuine installation after mismatch: err = %v, want ErrRefreshRejected", err)
	}
}

func TestRefresh_TransportMismatchRejectsWithoutChange(t *testing.T) {
	tests := []struct {
		name           string
		signInType     string
		wrongTransport string
	}{
		{"cookie transport for a mobile session", auth.ClientTypeMobile, auth.ClientTypeWeb},
		{"body transport for a web session", auth.ClientTypeWeb, auth.ClientTypeMobile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, auth.StatusActive)
			device := uuid.New()
			in := svInput(u, device)
			in.ClientType = tt.signInType
			res, err := s.SignIn(t.Context(), in)
			if err != nil || res.Session == nil {
				t.Fatalf("SignIn: %+v %v", res, err)
			}
			tok := *res.Session
			auditBefore := len(e.AuditRows(t))

			bad := svRefreshIn(device, tok.RefreshToken)
			bad.ClientType = tt.wrongTransport
			if _, err := s.Refresh(t.Context(), bad); !errors.Is(err, auth.ErrRefreshRejected) {
				t.Fatalf("err = %v, want ErrRefreshRejected", err)
			}
			if ss := svSession(t, e, tok.SessionID); ss.EndedAt != nil {
				t.Errorf("a transport mismatch changes nothing, but the session ended (%q)", ss.EndReason)
			}
			if svRefreshRow(t, e, tok.RefreshToken).UsedAt != nil {
				t.Errorf("token consumed by a rejected presentation")
			}
			if got := len(e.AuditRows(t)); got != auditBefore {
				t.Errorf("audit rows = %d, want %d", got, auditBefore)
			}
			good := svRefreshIn(device, tok.RefreshToken)
			good.ClientType = tt.signInType
			if _, err := s.Refresh(t.Context(), good); err != nil {
				t.Errorf("the right transport must still work: %v", err)
			}
		})
	}
}

func TestRefresh_UnknownOrMalformedTokenIsRejectedWithoutTrace(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	tok := svSignIn(t, s, u, device)
	auditBefore := len(e.AuditRows(t))
	for _, raw := range []string{"", "drt_", "drt_" + strings.Repeat("A", 43), "garbage", tok.AccessToken, strings.ToUpper(tok.RefreshToken)} {
		if _, err := s.Refresh(t.Context(), svRefreshIn(device, raw)); !errors.Is(err, auth.ErrRefreshRejected) {
			t.Errorf("Refresh(%.12q): err = %v, want ErrRefreshRejected", raw, err)
		}
	}
	if svSession(t, e, tok.SessionID).EndedAt != nil {
		t.Errorf("unknown tokens must not end a session")
	}
	if got := len(e.AuditRows(t)); got != auditBefore {
		t.Errorf("audit rows = %d, want %d (nothing to attribute)", got, auditBefore)
	}
}

func TestRefresh_EndedSessionIsRejected(t *testing.T) {
	for _, reason := range []string{"signed_out", "revoked_by_admin", "replaced"} {
		t.Run(reason, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, auth.StatusActive)
			device := uuid.New()
			tok := svSignIn(t, s, u, device)
			stInTx(t, e, func(ctx context.Context) error {
				_, err := e.Store.EndSession(ctx, tok.SessionID, reason, e.Clock.Now())
				return err
			})
			auditBefore := len(e.AuditRows(t))
			if _, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
				t.Fatalf("err = %v, want ErrRefreshRejected", err)
			}
			if got := len(e.AuditRows(t)); got != auditBefore {
				t.Errorf("audit rows = %d, want %d", got, auditBefore)
			}
			if svRefreshRow(t, e, tok.RefreshToken).UsedAt != nil {
				t.Errorf("token consumed on an ended session")
			}
		})
	}
}

func TestRefresh_SigningInAgainOnTheSameInstallationKillsTheOldRefreshToken(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	first := svSignIn(t, s, u, device)
	e.Clock.Advance(time.Minute)
	second := svSignIn(t, s, u, device)
	if _, err := s.Refresh(t.Context(), svRefreshIn(device, first.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
		t.Errorf("old session's token: err = %v, want ErrRefreshRejected", err)
	}
	if _, err := s.Refresh(t.Context(), svRefreshIn(device, second.RefreshToken)); err != nil {
		t.Errorf("new session's token: %v", err)
	}
}

// svSignInAt signs a client in with the clock at T0 and returns the tokens and the device.
func svSignInAt0(t *testing.T, e *authtest.Env, s *auth.Service) (authtest.User, auth.SessionTokens, uuid.UUID) {
	t.Helper()
	e.Clock.Set(authtest.T0)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	return u, svSignIn(t, s, u, device), device
}

// svWalk refreshes at each offset from T0 in order and requires every step to succeed.
func svWalk(t *testing.T, e *authtest.Env, s *auth.Service, tok auth.SessionTokens, device uuid.UUID, steps ...time.Duration) auth.SessionTokens {
	t.Helper()
	for _, at := range steps {
		e.Clock.Set(authtest.T0.Add(at))
		next, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
		if err != nil {
			t.Fatalf("walk step at T+%v: %v", at, err)
		}
		tok = next
	}
	return tok
}

func TestRefresh_ClientIdle30d_Boundary(t *testing.T) {
	tests := []struct {
		name string
		at   time.Duration
		ok   bool
	}{
		{"29d23h59m succeeds", 29*day + 23*time.Hour + 59*time.Minute, true},
		{"exactly 30d is expired", 30 * day, false},
		{"30d1m fails", 30*day + time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			_, tok, device := svSignInAt0(t, e, s)
			e.Clock.Set(authtest.T0.Add(tt.at))
			auditBefore := len(e.AuditRows(t))
			_, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
			if tt.ok {
				if err != nil {
					t.Fatalf("err = %v, want success", err)
				}
				return
			}
			if !errors.Is(err, auth.ErrRefreshRejected) {
				t.Fatalf("err = %v, want ErrRefreshRejected", err)
			}
			if ss := svSession(t, e, tok.SessionID); ss.EndedAt != nil {
				t.Errorf("expiry is computed, not written: session shows ended (%q)", ss.EndReason)
			}
			if svRefreshRow(t, e, tok.RefreshToken).UsedAt != nil {
				t.Errorf("token consumed by an expired presentation")
			}
			if got := len(e.AuditRows(t)); got != auditBefore {
				t.Errorf("audit rows = %d, want %d", got, auditBefore)
			}
		})
	}
}

func TestRefresh_ClientAbsolute90d_Boundary(t *testing.T) {
	// Refreshed every 29 days so the idle limit never bites; only the 90-day absolute limit does.
	steps := []time.Duration{29 * day, 58 * day, 87 * day}
	tests := []struct {
		name string
		at   time.Duration
		ok   bool
	}{
		{"89d23h succeeds", 89*day + 23*time.Hour, true},
		{"exactly 90d is expired", 90 * day, false},
		{"90d1m fails", 90*day + time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			_, tok, device := svSignInAt0(t, e, s)
			tok = svWalk(t, e, s, tok, device, steps...)
			e.Clock.Set(authtest.T0.Add(tt.at))
			_, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
			if tt.ok != (err == nil) || (!tt.ok && !errors.Is(err, auth.ErrRefreshRejected)) {
				t.Fatalf("err = %v, want success=%v", err, tt.ok)
			}
		})
	}
}

func TestRefresh_AccessTokenNeverOutlivesTheAbsoluteLimit(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	_, tok, device := svSignInAt0(t, e, s)
	tok = svWalk(t, e, s, tok, device, 29*day, 58*day, 87*day)
	e.Clock.Set(authtest.T0.Add(90*day - 5*time.Minute))
	res, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	abs := authtest.T0.Add(90 * day)
	claims, err := svTokens(e).Verify(res.AccessToken)
	if err != nil || !claims.ExpiresAt.Equal(abs) || !res.AccessTokenExpiresAt.Equal(abs) {
		t.Errorf("access expiry = %v (response %v), err %v; want the absolute limit %v, not now+15m", claims.ExpiresAt, res.AccessTokenExpiresAt, err, abs)
	}
}

func TestRefresh_AdminIdle12h_Boundary(t *testing.T) {
	tests := []struct {
		name string
		at   time.Duration
		ok   bool
	}{
		{"11h59m succeeds without a TOTP code", 11*time.Hour + 59*time.Minute, true},
		{"exactly 12h is expired", 12 * time.Hour, false},
		{"12h1m fails", 12*time.Hour + time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			admin := e.NewAdmin(t)
			sess, device, refresh := svFixtureSession(t, e, admin.ID, authtest.SessionSpec{MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * day})
			e.Clock.Set(authtest.T0.Add(tt.at))
			res, err := s.Refresh(t.Context(), svRefreshIn(device, refresh))
			if !tt.ok {
				if !errors.Is(err, auth.ErrRefreshRejected) {
					t.Fatalf("err = %v, want ErrRefreshRejected", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want success", err)
			}
			if got := svSession(t, e, sess).IdleExpiresAt; !got.Equal(e.Clock.Now().Add(12 * time.Hour)) {
				t.Errorf("idle after an admin refresh = %v, want now+12h", got)
			}
			if res.User.Role != "admin" {
				t.Errorf("user = %+v", res.User)
			}
		})
	}
}

func TestRefresh_AdminAbsolute7d_Boundary(t *testing.T) {
	var steps []time.Duration
	for h := 11; h <= 165; h += 11 { // refreshed every 11 hours so the 12 h idle limit never bites
		steps = append(steps, time.Duration(h)*time.Hour)
	}
	tests := []struct {
		name string
		at   time.Duration
		ok   bool
	}{
		{"6d23h59m succeeds", 7*day - time.Minute, true},
		{"exactly 7d is expired", 7 * day, false},
		{"7d1m fails", 7*day + time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			admin := e.NewAdmin(t)
			_, device, refresh := svFixtureSession(t, e, admin.ID, authtest.SessionSpec{MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * day})
			tok := svWalk(t, e, s, auth.SessionTokens{RefreshToken: refresh}, device, steps...)
			e.Clock.Set(authtest.T0.Add(tt.at))
			_, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken))
			if tt.ok != (err == nil) || (!tt.ok && !errors.Is(err, auth.ErrRefreshRejected)) {
				t.Fatalf("err = %v, want success=%v", err, tt.ok)
			}
		})
	}
}

func TestRefresh_BR_CLI_04_DisabledUserRejected(t *testing.T) {
	tests := []struct {
		name string
		stop func(t *testing.T, e *authtest.Env, s *auth.Service, u authtest.User)
	}{
		{"status disabled, session left alone", func(t *testing.T, e *authtest.Env, _ *auth.Service, u authtest.User) {
			e.Exec(t, `UPDATE users SET status = 'disabled', status_before_disabled = 'active' WHERE id = $1`, u.ID)
		}},
		{"status pending_approval, session left alone", func(t *testing.T, e *authtest.Env, _ *auth.Service, u authtest.User) {
			e.Exec(t, `UPDATE users SET status = 'pending_approval' WHERE id = $1`, u.ID)
		}},
		{"disabled through the Accounts capability", func(t *testing.T, e *authtest.Env, s *auth.Service, u authtest.User) {
			e.NewAdmin(t)
			e.NewAdmin(t)
			if err := s.Disable(t.Context(), u.ID, authtest.System()); err != nil {
				t.Fatalf("Disable: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run("BR_CLI_04 "+tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, auth.StatusActive)
			device := uuid.New()
			tok := svSignIn(t, s, u, device)
			tt.stop(t, e, s, u)
			e.Clock.Advance(time.Second)
			if _, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
				t.Fatalf("err = %v, want ErrRefreshRejected", err)
			}
			if svRefreshRow(t, e, tok.RefreshToken).UsedAt != nil {
				t.Errorf("a rejected refresh must not rotate the token")
			}
		})
	}
}

func TestRefresh_LockedAccountCanStillRefresh(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	tok := svSignIn(t, s, u, device)
	svLockWithFiveFailures(t, e, s, u)
	locked := svThrottle(t, e, svUserKey(u))
	if locked.LockedUntil == nil {
		t.Fatalf("precondition: account locked")
	}
	e.Clock.Advance(time.Minute)
	if _, err := s.Refresh(t.Context(), svRefreshIn(device, tok.RefreshToken)); err != nil {
		t.Fatalf("a lock blocks new sign-ins only (D-08), refresh failed: %v", err)
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil == nil || !st.LockedUntil.Equal(*locked.LockedUntil) {
		t.Errorf("refresh touched the throttle: %+v", st)
	}
}
