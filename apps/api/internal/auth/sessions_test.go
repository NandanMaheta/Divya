package auth_test

// S13 - Service tests B, sessions and the effect of revocation (US-2 s7-8, US-6, US-8 s1-3,
// FR-033, FR-047..FR-049, D-05, D-12, SC-002; BR-CLI-04). Real Store, fake clock.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/authz"
)

// svThreeDevices signs a client in on A (android), B (iphone) and C (android), a minute apart.
func svThreeDevices(t *testing.T, e *authtest.Env, s *auth.Service, u authtest.User) (a, b, c auth.SessionTokens, devs [3]uuid.UUID) {
	t.Helper()
	sign := func(i int, name, platform string) auth.SessionTokens {
		devs[i] = uuid.New()
		in := svInput(u, devs[i])
		in.Device = auth.DeviceInfo{ID: devs[i], Name: name, Platform: platform}
		res, err := s.SignIn(t.Context(), in)
		if err != nil || res.Session == nil {
			t.Fatalf("SignIn %s: %+v %v", name, res, err)
		}
		e.Clock.Advance(time.Minute)
		return *res.Session
	}
	return sign(0, "Pixel 9", auth.PlatformAndroid), sign(1, "Asha's iPhone", auth.PlatformIPhone), sign(2, "Galaxy S24", auth.PlatformAndroid), devs
}

func TestSessions_SignOutEndsOnlyThisDevice(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	a, b, c, devs := svThreeDevices(t, e, s, u)

	if err := s.SignOut(t.Context(), svPrincipal(e, u, a.SessionID)); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if ss := svSession(t, e, a.SessionID); ss.EndedAt == nil || ss.EndReason != "signed_out" || !ss.EndedAt.Equal(e.Clock.Now()) {
		t.Errorf("session A ended=%v reason=%q, want signed_out now", ss.EndedAt, ss.EndReason)
	}
	e.Clock.Advance(time.Second)
	v := s.BearerVerifier()
	if err := svVerify(t, v, a.AccessToken); err == nil {
		t.Errorf("A's access token must stop working on the next request")
	}
	if _, err := s.Refresh(t.Context(), svRefreshIn(devs[0], a.RefreshToken)); !errors.Is(err, auth.ErrRefreshRejected) {
		t.Errorf("A's refresh token: err = %v, want ErrRefreshRejected", err)
	}
	for name, tok := range map[string]auth.SessionTokens{"B": b, "C": c} {
		if err := svVerify(t, v, tok.AccessToken); err != nil {
			t.Errorf("device %s must keep working: %v", name, err)
		}
	}
	rows := e.AuditOf(t, auth.ActionSessionSignedOut)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindClient || rows[0].ActorID != u.ID || rows[0].EntityID != u.ID ||
		svJSON(t, rows[0].After)["session_id"] != a.SessionID.String() {
		t.Errorf("signed_out audit = %+v", rows)
	}

	// Signing in again on A gives a fresh session and A is listed once (US-6 s6).
	e.Clock.Advance(time.Minute)
	again := svSignIn(t, s, u, devs[0])
	if again.SessionID == a.SessionID {
		t.Errorf("a new session id is required after sign-out")
	}
	list, err := s.ListDevices(t.Context(), svPrincipal(e, u, again.SessionID))
	if err != nil || len(list) != 3 {
		t.Fatalf("ListDevices = %d items, %v; want 3", len(list), err)
	}
}

func TestSessions_SignOutEverywhereEndsEveryDeviceIncludingTheCurrent(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	other := e.UserWithStatus(t, auth.StatusActive)
	keep := svSignIn(t, s, other, uuid.New())
	a, b, c, _ := svThreeDevices(t, e, s, u)

	n, err := s.SignOutEverywhere(t.Context(), svPrincipal(e, u, a.SessionID))
	if err != nil || n != 3 {
		t.Fatalf("SignOutEverywhere = %d, %v; want 3", n, err)
	}
	e.Clock.Advance(time.Second)
	v := s.BearerVerifier()
	for name, tok := range map[string]auth.SessionTokens{"A": a, "B": b, "C": c} {
		if err := svVerify(t, v, tok.AccessToken); err == nil {
			t.Errorf("device %s still works after sign-out everywhere (D-12 includes the current device)", name)
		}
		if ss := svSession(t, e, tok.SessionID); ss.EndReason != "signed_out_everywhere" {
			t.Errorf("device %s end reason = %q", name, ss.EndReason)
		}
	}
	if err := svVerify(t, v, keep.AccessToken); err != nil {
		t.Errorf("another user's session was affected: %v", err)
	}
	rows := e.AuditOf(t, auth.ActionSessionSignedOutEverywhere)
	ids, _ := svJSON(t, rows[0].After)["session_ids"].([]any)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindClient || rows[0].EntityID != u.ID || len(ids) != 3 {
		t.Errorf("signed_out_everywhere audit = %+v", rows)
	}
}

func TestSessions_ListShowsLiveDevicesMostRecentFirstAndMarksTheCurrent(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	a, b, c, devs := svThreeDevices(t, e, s, u)
	signedIn := map[uuid.UUID]time.Time{a.SessionID: authtest.T0, b.SessionID: authtest.T0.Add(time.Minute), c.SessionID: authtest.T0.Add(2 * time.Minute)}

	list, err := s.ListDevices(t.Context(), svPrincipal(e, u, a.SessionID))
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("items = %d, want exactly 3", len(list))
	}
	if list[0].SessionID != c.SessionID || list[1].SessionID != b.SessionID || list[2].SessionID != a.SessionID {
		t.Errorf("order = %v %v %v, want C, B, A (most recently active first)", list[0].SessionID, list[1].SessionID, list[2].SessionID)
	}
	for _, d := range list {
		if d.IsCurrent != (d.SessionID == a.SessionID) {
			t.Errorf("device %v IsCurrent = %v", d.SessionID, d.IsCurrent)
		}
		if !d.SignedInAt.Equal(signedIn[d.SessionID]) || !d.LastActiveAt.Equal(signedIn[d.SessionID]) {
			t.Errorf("device %v times = %v / %v, want %v", d.SessionID, d.SignedInAt, d.LastActiveAt, signedIn[d.SessionID])
		}
	}
	if list[1].DeviceName != "Asha's iPhone" || list[1].Platform != "iphone" || list[2].DeviceName != "Pixel 9" || list[2].Platform != "android" {
		t.Errorf("names/platforms = %+v", list)
	}

	// A refresh moves a device to the top and updates last-active, not sign-in time.
	e.Clock.Advance(time.Hour)
	if _, err := s.Refresh(t.Context(), svRefreshIn(devs[0], a.RefreshToken)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	list, _ = s.ListDevices(t.Context(), svPrincipal(e, u, a.SessionID))
	if list[0].SessionID != a.SessionID || !list[0].LastActiveAt.Equal(e.Clock.Now()) || !list[0].SignedInAt.Equal(authtest.T0) {
		t.Errorf("after refresh first = %+v", list[0])
	}
}

func TestSessions_ListExcludesEndedAndExpiredSessions(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	live := e.NewSession(t, u.ID, authtest.SessionSpec{})
	ended := e.NewSession(t, u.ID, authtest.SessionSpec{})
	idle := e.NewSession(t, u.ID, authtest.SessionSpec{Idle: time.Hour})
	abs := e.NewSession(t, u.ID, authtest.SessionSpec{Absolute: time.Hour})
	stInTx(t, e, func(ctx context.Context) error {
		_, err := e.Store.EndSession(ctx, ended, "signed_out", e.Clock.Now())
		return err
	})
	e.Clock.Advance(time.Hour) // exactly at both expiry instants
	list, err := s.ListDevices(t.Context(), svPrincipal(e, u, live))
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(list) != 1 || list[0].SessionID != live {
		t.Errorf("list = %+v, want only the live session (ended %v, idle-expired %v, absolute-expired %v excluded)", list, ended, idle, abs)
	}
}

func TestSessions_RevokeOwnDevice(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	a, b, c, _ := svThreeDevices(t, e, s, u)

	if err := s.RevokeDevice(t.Context(), svPrincipal(e, u, a.SessionID), b.SessionID); err != nil {
		t.Fatalf("RevokeDevice(B): %v", err)
	}
	if ss := svSession(t, e, b.SessionID); ss.EndedAt == nil || ss.EndReason != "revoked_by_user" {
		t.Errorf("B ended=%v reason=%q, want revoked_by_user", ss.EndedAt, ss.EndReason)
	}
	e.Clock.Advance(time.Second)
	v := s.BearerVerifier()
	if svVerify(t, v, b.AccessToken) == nil {
		t.Errorf("B must lose access on its next request")
	}
	if svVerify(t, v, a.AccessToken) != nil || svVerify(t, v, c.AccessToken) != nil {
		t.Errorf("A and C must keep working")
	}
	list, _ := s.ListDevices(t.Context(), svPrincipal(e, u, a.SessionID))
	if len(list) != 2 {
		t.Errorf("list after revoke = %d, want 2", len(list))
	}
	rows := e.AuditOf(t, auth.ActionSessionRevoked)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindClient || rows[0].ActorID != u.ID || rows[0].EntityID != u.ID {
		t.Fatalf("revoked audit = %+v", rows)
	}
	if after := svJSON(t, rows[0].After); after["session_id"] != b.SessionID.String() || after["by"] != "user" {
		t.Errorf("after = %v", after)
	}

	// Revoking the current device behaves exactly like signing out (US-6 s7).
	if err := s.RevokeDevice(t.Context(), svPrincipal(e, u, a.SessionID), a.SessionID); err != nil {
		t.Fatalf("RevokeDevice(current): %v", err)
	}
	e.Clock.Advance(time.Second)
	if svVerify(t, v, a.AccessToken) == nil {
		t.Errorf("the revoked current device must be rejected on its next request")
	}
}

func TestSessions_RevokeSomeoneElsesOrMissingOrEndedSessionIsNotFound(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	other := e.UserWithStatus(t, auth.StatusActive)
	mine := svSignIn(t, s, u, uuid.New())
	theirs := svSignIn(t, s, other, uuid.New())
	gone := svSignIn(t, s, u, uuid.New())
	if err := s.SignOut(t.Context(), svPrincipal(e, u, gone.SessionID)); err != nil {
		t.Fatal(err)
	}
	auditBefore := len(e.AuditRows(t))

	for name, id := range map[string]uuid.UUID{"another user's": theirs.SessionID, "unknown": uuid.New(), "already ended": gone.SessionID} {
		if err := s.RevokeDevice(t.Context(), svPrincipal(e, u, mine.SessionID), id); !errors.Is(err, auth.ErrSessionNotFound) {
			t.Errorf("%s session: err = %v, want ErrSessionNotFound (indistinguishable)", name, err)
		}
	}
	if svSession(t, e, theirs.SessionID).EndedAt != nil {
		t.Errorf("another user's session must be untouched")
	}
	if got := len(e.AuditRows(t)); got != auditBefore {
		t.Errorf("audit rows = %d, want %d", got, auditBefore)
	}
}

func TestSessions_AdminListRevokeAndEndAll(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	a, b, c, _ := svThreeDevices(t, e, s, u)
	admin := e.NewAdmin(t)
	adminSess, _, _ := svFixtureSession(t, e, admin.ID, authtest.SessionSpec{MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * day})
	p := authz.Principal{UserID: admin.ID, OrgID: e.Org, SessionID: adminSess, Role: authz.RoleAdmin}

	list, err := s.AdminListDevices(t.Context(), p, u.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("AdminListDevices = %d items, %v; want 3", len(list), err)
	}
	for _, d := range list {
		if d.IsCurrent {
			t.Errorf("IsCurrent must be false in an admin listing of another user")
		}
	}

	if err := s.AdminRevokeDevice(t.Context(), p, u.ID, b.SessionID); err != nil {
		t.Fatalf("AdminRevokeDevice: %v", err)
	}
	if ss := svSession(t, e, b.SessionID); ss.EndReason != "revoked_by_admin" {
		t.Errorf("B end reason = %q, want revoked_by_admin", ss.EndReason)
	}
	rows := e.AuditOf(t, auth.ActionSessionRevoked)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindAdmin || rows[0].ActorID != admin.ID || rows[0].EntityID != u.ID || svJSON(t, rows[0].After)["by"] != "admin" {
		t.Errorf("revoked audit = %+v, want actor admin, entity the owner, by admin", rows)
	}

	n, err := s.AdminEndAllSessions(t.Context(), p, u.ID)
	if err != nil || n != 2 {
		t.Fatalf("AdminEndAllSessions = %d, %v; want the 2 remaining", n, err)
	}
	e.Clock.Advance(time.Second)
	for name, tok := range map[string]auth.SessionTokens{"A": a, "B": b, "C": c} {
		if svVerify(t, s.BearerVerifier(), tok.AccessToken) == nil {
			t.Errorf("device %s still works after the admin ended all sessions", name)
		}
	}
	if ss := svSession(t, e, c.SessionID); ss.EndReason != "ended_by_admin" {
		t.Errorf("C end reason = %q, want ended_by_admin", ss.EndReason)
	}
	all := e.AuditOf(t, auth.ActionSessionEndedAllByAdmin)
	ids, _ := svJSON(t, all[0].After)["session_ids"].([]any)
	if len(all) != 1 || all[0].ActorKind != audit.KindAdmin || all[0].EntityID != u.ID || len(ids) != 2 {
		t.Errorf("ended_all_by_admin audit = %+v", all)
	}
	if svSession(t, e, adminSess).EndedAt != nil {
		t.Errorf("the admin's own session must be untouched")
	}
}

func TestSessions_AdminOperationsRefuseUnknownAndForeignTargets(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	other := e.UserWithStatus(t, auth.StatusActive)
	mine := svSignIn(t, s, u, uuid.New())
	theirs := svSignIn(t, s, other, uuid.New())
	admin := e.NewAdmin(t)
	p := authz.Principal{UserID: admin.ID, OrgID: e.Org, SessionID: uuid.New(), Role: authz.RoleAdmin}

	// A user of another organisation is "not found" for this org's admin.
	org2 := uuid.New()
	e.Exec(t, `INSERT INTO orgs (id, name) VALUES ($1, 'Other')`, org2)
	foreign := e.UserWithStatus(t, auth.StatusActive)
	e.Exec(t, `UPDATE users SET org_id = $2 WHERE id = $1`, foreign.ID, org2)

	for name, id := range map[string]uuid.UUID{"unknown user": uuid.New(), "other org": foreign.ID} {
		if _, err := s.AdminListDevices(t.Context(), p, id); !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("AdminListDevices(%s): err = %v, want ErrUserNotFound", name, err)
		}
		if err := s.AdminRevokeDevice(t.Context(), p, id, mine.SessionID); !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("AdminRevokeDevice(%s): err = %v, want ErrUserNotFound", name, err)
		}
		if _, err := s.AdminEndAllSessions(t.Context(), p, id); !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("AdminEndAllSessions(%s): err = %v, want ErrUserNotFound", name, err)
		}
	}
	if err := s.AdminRevokeDevice(t.Context(), p, u.ID, theirs.SessionID); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("a session of another user: err = %v, want ErrSessionNotFound", err)
	}
	if svSession(t, e, theirs.SessionID).EndedAt != nil || svSession(t, e, mine.SessionID).EndedAt != nil {
		t.Errorf("a refused admin operation changed a session")
	}
}

func TestAuthz_BR_CLI_04_DisabledUserNextRequestRejected(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	a, b, _, _ := svThreeDevices(t, e, s, u)
	e.NewAdmin(t)
	e.NewAdmin(t)
	v := s.BearerVerifier()
	if err := svVerify(t, v, a.AccessToken); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if err := s.Disable(t.Context(), u.ID, authtest.System()); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	e.Clock.Advance(time.Second)
	for name, tok := range map[string]auth.SessionTokens{"A": a, "B": b} {
		if svVerify(t, v, tok.AccessToken) == nil {
			t.Errorf("device %s: an unexpired access token must be rejected 1 s after the user is disabled (D-05)", name)
		}
	}
	if err := s.Enable(t.Context(), u.ID, authtest.System()); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if svVerify(t, v, a.AccessToken) == nil {
		t.Errorf("re-enabling must not revive ended sessions")
	}
}

func TestAuthz_RevocationTakesEffectOnTheNextRequestForEveryEnder(t *testing.T) {
	tests := []struct {
		name string
		end  func(t *testing.T, e *authtest.Env, s *auth.Service, u authtest.User, tok auth.SessionTokens)
	}{
		{"sign-out", func(t *testing.T, e *authtest.Env, s *auth.Service, u authtest.User, tok auth.SessionTokens) {
			if err := s.SignOut(t.Context(), svPrincipal(e, u, tok.SessionID)); err != nil {
				t.Fatal(err)
			}
		}},
		{"reset password", func(t *testing.T, _ *authtest.Env, s *auth.Service, u authtest.User, _ auth.SessionTokens) {
			if err := s.ResetPassword(t.Context(), u.ID, "another-fine-passphrase-5", authtest.System()); err != nil {
				t.Fatal(err)
			}
		}},
		{"end all sessions by a feature", func(t *testing.T, _ *authtest.Env, s *auth.Service, u authtest.User, _ auth.SessionTokens) {
			if _, err := s.EndAllSessions(t.Context(), u.ID, "test", authtest.System()); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, auth.StatusActive)
			tok := svSignIn(t, s, u, uuid.New())
			tt.end(t, e, s, u, tok)
			e.Clock.Advance(time.Second)
			if svVerify(t, s.BearerVerifier(), tok.AccessToken) == nil {
				t.Errorf("the unexpired access token still works one second after %s", tt.name)
			}
		})
	}
}
