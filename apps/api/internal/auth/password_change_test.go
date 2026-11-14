package auth_test

// S13 - Service tests B, change password (plan P-06 "Change password"; US-7; FR-070, D-07, D-08,
// D-13; US-4 s8 counting). Real Store, fake clock.

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/authz"
)

const newPassword = "second-lantern-passphrase-58"

func TestPasswordChange_EndsOtherSessionsKeepsTheCurrentOne(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	e.Secrets.Add("new password", newPassword)
	u := e.UserWithStatus(t, auth.StatusActive)
	a, b, c, _ := svThreeDevices(t, e, s, u)

	if err := s.ChangePassword(t.Context(), svPrincipal(e, u, a.SessionID), u.Password, newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	for name, tok := range map[string]auth.SessionTokens{"B": b, "C": c} {
		if ss := svSession(t, e, tok.SessionID); ss.EndedAt == nil || ss.EndReason != "password_changed" {
			t.Errorf("device %s ended=%v reason=%q, want password_changed", name, ss.EndedAt, ss.EndReason)
		}
	}
	if svSession(t, e, a.SessionID).EndedAt != nil {
		t.Errorf("the current device must stay signed in")
	}
	e.Clock.Advance(time.Second)
	v := s.BearerVerifier()
	if err := svVerify(t, v, a.AccessToken); err != nil {
		t.Errorf("current device's access token: %v", err)
	}
	if svVerify(t, v, b.AccessToken) == nil || svVerify(t, v, c.AccessToken) == nil {
		t.Errorf("other devices must lose access on their next request")
	}

	row := svUserRow(t, e, u.ID)
	if !row.PasswordChangedAt.Equal(authtest.T0.Add(3 * time.Minute)) {
		t.Errorf("password_changed_at = %v, want the time of the change", row.PasswordChangedAt)
	}
	if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("old password: err = %v, want the generic failure", err)
	}
	in := svInput(u, uuid.New())
	in.Password = newPassword
	if res, err := s.SignIn(t.Context(), in); err != nil || res.Session == nil {
		t.Errorf("new password: %+v, %v", res, err)
	}

	rows := e.AuditOf(t, auth.ActionPasswordChanged)
	if len(rows) != 1 {
		t.Fatalf("password.changed rows = %d, want 1", len(rows))
	}
	ended, _ := svJSON(t, rows[0].After)["ended_session_ids"].([]any)
	if rows[0].ActorKind != audit.KindClient || rows[0].ActorID != u.ID || rows[0].EntityID != u.ID || len(ended) != 2 {
		t.Errorf("audit = %+v", rows[0])
	}
	e.Secrets.ScanAuditLog(t.Context(), t, e.Pool)
}

func TestPasswordChange_AdminKeepsTheCurrentSessionAndEndsTheOther(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	admin := e.NewAdmin(t)
	cur, _, _ := svFixtureSession(t, e, admin.ID, authtest.SessionSpec{MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * day})
	other, _, _ := svFixtureSession(t, e, admin.ID, authtest.SessionSpec{MFAMethod: "totp", Idle: 12 * time.Hour, Absolute: 7 * day})
	p := authz.Principal{UserID: admin.ID, OrgID: e.Org, SessionID: cur, Role: authz.RoleAdmin}

	if err := s.ChangePassword(t.Context(), p, admin.Password, newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if svSession(t, e, cur).EndedAt != nil {
		t.Errorf("the current admin session must continue")
	}
	if ss := svSession(t, e, other); ss.EndedAt == nil || ss.EndReason != "password_changed" {
		t.Errorf("other admin session ended=%v reason=%q", ss.EndedAt, ss.EndReason)
	}
	rows := e.AuditOf(t, auth.ActionPasswordChanged)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindAdmin || rows[0].ActorID != admin.ID {
		t.Errorf("audit = %+v, want one row by the admin", rows)
	}
}

func TestPasswordChange_WrongCurrentPasswordIsRefusedAndCounted(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	a, b, _, _ := svThreeDevices(t, e, s, u)
	hash := svUserRow(t, e, u.ID).PasswordHash
	auditBefore := len(e.AuditOf(t, auth.ActionPasswordChanged))

	err := s.ChangePassword(t.Context(), svPrincipal(e, u, a.SessionID), wrongPassword, newPassword)
	if !errors.Is(err, auth.ErrReauthFailed) {
		t.Fatalf("err = %v, want ErrReauthFailed", err)
	}
	if got := svUserRow(t, e, u.ID).PasswordHash; got != hash {
		t.Errorf("hash changed by a refused change")
	}
	if svLiveSessions(t, e, u.ID) != 3 || svSession(t, e, b.SessionID).EndedAt != nil {
		t.Errorf("a refused change must not end sessions")
	}
	if got := len(e.AuditOf(t, auth.ActionPasswordChanged)); got != auditBefore {
		t.Errorf("password.changed rows = %d, want %d", got, auditBefore)
	}
	if got := len(svThrottle(t, e, svUserKey(u)).Failures); got != 1 {
		t.Errorf("counted failures = %d, want 1 (D-08)", got)
	}
}

func TestPasswordChange_FiveWrongCurrentPasswordsLockSignInAndFurtherChanges(t *testing.T) {
	e := authtest.New(t)
	var runs atomic.Int64
	d := e.Deps()
	d.Hasher = auth.NewHasher(auth.HasherOptions{Params: authtest.CheapHasherParams, OnArgon2: func(auth.HasherParams) { runs.Add(1) }})
	s := e.ServiceWith(t, d)
	u := e.UserWithStatus(t, auth.StatusActive)
	tok := svSignIn(t, s, u, uuid.New())
	p := svPrincipal(e, u, tok.SessionID)

	for i := range 5 {
		if err := s.ChangePassword(t.Context(), p, wrongPassword, newPassword); !errors.Is(err, auth.ErrReauthFailed) {
			t.Fatalf("attempt %d: err = %v, want ErrReauthFailed", i+1, err)
		}
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil == nil {
		t.Fatalf("five wrong current passwords must lock the account: %+v", st)
	}
	if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("sign-in while locked by change-password failures: err = %v, want the generic failure", err)
	}

	runs.Store(0)
	if err := s.ChangePassword(t.Context(), p, u.Password, newPassword); !errors.Is(err, auth.ErrReauthFailed) {
		t.Fatalf("locked, correct current password: err = %v, want ErrReauthFailed", err)
	}
	if got := runs.Load(); got != 0 {
		t.Errorf("Argon2 runs while locked = %d, want 0 (no verification at all)", got)
	}
	if ok, _ := e.Hasher.Verify(t.Context(), svUserRow(t, e, u.ID).PasswordHash, auth.NormalizePassword(u.Password)); !ok {
		t.Errorf("the password changed while the account was locked")
	}
	// Existing sessions keep working under a lock.
	if err := svVerify(t, s.BearerVerifier(), tok.AccessToken); err != nil {
		t.Errorf("a lock must not end the existing session: %v", err)
	}
}

func TestPasswordChange_PolicyRulesAreEnforcedWithCodes(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.NewUser(t, authtest.UserSpec{Email: "asha@example.com", Mobile: "+919876543210"})
	tok := svSignIn(t, s, u, uuid.New())
	p := svPrincipal(e, u, tok.SessionID)
	hash := svUserRow(t, e, u.ID).PasswordHash

	tests := []struct{ name, pw, code string }{
		{"9 characters", "abcd-1234", auth.PolicyTooShort},
		{"common 1234567890", "1234567890", auth.PolicyTooCommon},
		{"common QWERTYUIOP in capitals", "QWERTYUIOP", auth.PolicyTooCommon},
		{"257 characters", strings.Repeat("ab1", 86)[:257], auth.PolicyTooLong},
		{"own email", "Asha@Example.com", auth.PolicyMatchesIdentifier},
		{"own mobile", "9876543210", auth.PolicyMatchesIdentifier},
		{"the current password", u.Password, auth.PolicyUnchanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.ChangePassword(t.Context(), p, u.Password, tt.pw)
			var pe *auth.PolicyError
			if !errors.As(err, &pe) || pe.Code != tt.code {
				t.Fatalf("err = %v, want PolicyError %s", err, tt.code)
			}
		})
	}
	if got := svUserRow(t, e, u.ID).PasswordHash; got != hash {
		t.Errorf("a policy refusal changed the hash")
	}
	if got := len(svThrottle(t, e, svUserKey(u)).Failures); got != 0 {
		t.Errorf("policy failures counted as attempts: %d", got)
	}
}

func TestPasswordChange_AcceptsA64CharacterGujaratiHindiPassphraseButNot257(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	tok := svSignIn(t, s, u, uuid.New())
	runes := []rune(strings.Repeat("નમસ્તે गुजरात और भारत ", 6))[:64]
	pass := string(runes)
	e.Secrets.Add("passphrase", pass)
	if err := s.ChangePassword(t.Context(), svPrincipal(e, u, tok.SessionID), u.Password, pass); err != nil {
		t.Fatalf("64-character passphrase with spaces and Gujarati/Hindi letters: %v", err)
	}
	in := svInput(u, uuid.New())
	in.Password = pass
	if _, err := s.SignIn(t.Context(), in); err != nil {
		t.Errorf("sign-in with the new passphrase: %v", err)
	}
}

func TestPasswordChange_RetryAfterALostResponseSucceedsWithoutCounting(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	tok := svSignIn(t, s, u, uuid.New())
	p := svPrincipal(e, u, tok.SessionID)
	if err := s.ChangePassword(t.Context(), p, u.Password, newPassword); err != nil {
		t.Fatalf("first change: %v", err)
	}
	row := svUserRow(t, e, u.ID)

	e.Clock.Advance(time.Minute)
	// The client never saw the 204 and sends the same request again: the current password no
	// longer matches but the new one does, so the change has already happened.
	if err := s.ChangePassword(t.Context(), p, u.Password, newPassword); err != nil {
		t.Fatalf("retry: err = %v, want nil (safe retry)", err)
	}
	after := svUserRow(t, e, u.ID)
	if after.PasswordHash != row.PasswordHash || !after.PasswordChangedAt.Equal(row.PasswordChangedAt) {
		t.Errorf("a retry must change nothing")
	}
	if got := len(svThrottle(t, e, svUserKey(u)).Failures); got != 0 {
		t.Errorf("a safe retry must not be counted: %d failures", got)
	}
	if got := len(e.AuditOf(t, auth.ActionPasswordChanged)); got != 1 {
		t.Errorf("password.changed rows = %d, want 1", got)
	}
}
