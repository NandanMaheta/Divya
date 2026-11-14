package auth_test

// S11 - Service tests A, sign-in (plan P-06, P-08; US-1, US-5 s1/s8/s13; BR-CLI-04, D-04, D-09,
// FR-020..FR-023, FR-065). Real Store, fake clock, cheap Argon2 parameters.

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/crypto"
)

const testIP = "203.0.113.7"

// svEnrol gives the admin a confirmed authenticator (the secret itself is never used by sign-in).
func svEnrol(t *testing.T, e *authtest.Env, admin uuid.UUID) {
	t.Helper()
	enc, err := e.Keyring.Seal([]byte("JBSWY3DPEHPK3PXP"), []byte("admin_mfa.pending_secret:"+admin.String()))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		if err := e.Store.SetPendingSecret(ctx, e.Org, admin, enc, e.Clock.Now()); err != nil {
			return err
		}
		return e.Store.ConfirmPending(ctx, admin, 1, e.Clock.Now())
	})
}

func TestSignIn_BR_CLI_04_ActiveSucceeds(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.NewUser(t, authtest.UserSpec{Email: "asha@example.com", Mobile: "+919876543210"})
	device := uuid.New()
	now := e.Clock.Now()

	in := svInput(u, device)
	in.Identifier = "  Asha@Example.COM "
	res, err := s.SignIn(t.Context(), in)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if res.NextStep != auth.NextStepSignedIn || res.Session == nil || res.Challenge != nil {
		t.Fatalf("result = %+v, want signed_in with a session only", res)
	}
	tok := *res.Session
	e.Secrets.Add("access token", tok.AccessToken)
	e.Secrets.Add("refresh token", tok.RefreshToken)

	sess := svSession(t, e, tok.SessionID)
	if sess.UserID != u.ID || sess.OrgID != e.Org || sess.DeviceID != device || sess.DeviceName != "Pixel 9" ||
		sess.Platform != "android" || sess.ClientType != "mobile" || sess.MFAMethod != "" || sess.EndedAt != nil {
		t.Errorf("session = %+v", sess)
	}
	if !sess.CreatedAt.Equal(now) || !sess.IdleExpiresAt.Equal(now.Add(30*24*time.Hour)) || !sess.AbsoluteExpiresAt.Equal(now.Add(90*24*time.Hour)) {
		t.Errorf("session times = created %v idle %v absolute %v (client: 30 d idle, 90 d absolute)", sess.CreatedAt, sess.IdleExpiresAt, sess.AbsoluteExpiresAt)
	}
	if !tok.SessionExpiresAt.Equal(sess.AbsoluteExpiresAt) || !tok.IdleExpiresAt.Equal(sess.IdleExpiresAt) {
		t.Errorf("tokens expiry = %v / %v, want the session's", tok.SessionExpiresAt, tok.IdleExpiresAt)
	}
	if tok.User.ID != u.ID || tok.User.Role != "client" || tok.User.Email != "asha@example.com" || tok.User.Name != u.Name {
		t.Errorf("user summary = %+v", tok.User)
	}

	claims, err := auth.NewTokens(e.Signing, e.Clock.Now).Verify(tok.AccessToken)
	if err != nil {
		t.Fatalf("access token does not verify: %v", err)
	}
	if claims.UserID != u.ID || claims.SessionID != tok.SessionID || claims.OrgID != e.Org || claims.Role != "client" {
		t.Errorf("claims = %+v", claims)
	}
	if !claims.ExpiresAt.Equal(now.Add(15*time.Minute)) || !tok.AccessTokenExpiresAt.Equal(claims.ExpiresAt) {
		t.Errorf("access expiry = %v (response %v), want now+15m", claims.ExpiresAt, tok.AccessTokenExpiresAt)
	}

	if !strings.HasPrefix(tok.RefreshToken, "drt_") {
		t.Errorf("refresh token prefix: %.4q", tok.RefreshToken)
	}
	rr := svRefreshRow(t, e, tok.RefreshToken)
	if rr.SessionID != tok.SessionID || rr.UsedAt != nil || rr.OrgID != e.Org {
		t.Errorf("refresh row = %+v, want an unused token of the session", rr)
	}
	if n := svCount(t, e, `SELECT count(*) FROM refresh_tokens`); n != 1 {
		t.Errorf("refresh_tokens rows = %d, want 1", n)
	}

	row := svUserRow(t, e, u.ID)
	if row.LastSignInAt == nil || !row.LastSignInAt.Equal(now) {
		t.Errorf("last_sign_in_at = %v, want now", row.LastSignInAt)
	}

	rows := e.AuditOf(t, auth.ActionSignInSucceeded)
	if len(rows) != 1 {
		t.Fatalf("sign_in.succeeded rows = %d, want 1", len(rows))
	}
	r := rows[0]
	after := svJSON(t, r.After)
	if r.ActorKind != audit.KindClient || r.ActorID != u.ID || r.EntityID != u.ID || r.Entity != auth.EntityUser ||
		after["session_id"] != tok.SessionID.String() || after["platform"] != "android" || after["client_type"] != "mobile" || after["ip"] != testIP {
		t.Errorf("audit row = %+v after=%v", r, after)
	}
	if len(e.AuditRows(t)) != 1 {
		t.Errorf("audit rows = %v, want exactly one event for a client sign-in", e.AuditActions(t))
	}
	e.Secrets.ScanAuditLog(t.Context(), t, e.Pool)
}

func TestSignIn_IdentifierFormsMatchTheSameUser(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.NewUser(t, authtest.UserSpec{Email: "asha@example.com", Mobile: "+919876543210"})
	forms := []string{
		"asha@example.com", "  Asha@Example.COM ", "ASHA@EXAMPLE.COM",
		"9876543210", "09876543210", "919876543210", "+919876543210", "+91 98765-43210", " 98765 43210 ",
	}
	for _, id := range forms {
		t.Run(id, func(t *testing.T) {
			in := svInput(u, uuid.New())
			in.Identifier = id
			res, err := s.SignIn(t.Context(), in)
			if err != nil || res.Session == nil || res.Session.User.ID != u.ID {
				t.Fatalf("SignIn(%q) = %+v, %v; want a session of %v", id, res, err, u.ID)
			}
		})
	}
}

func TestSignIn_IPhoneWebClientRecordsPlatform(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	in := svInput(u, uuid.New())
	in.ClientType = auth.ClientTypeWeb
	in.Device = auth.DeviceInfo{ID: in.Device.ID, Name: "Asha's iPhone", Platform: auth.PlatformIPhone}
	res, err := s.SignIn(t.Context(), in)
	if err != nil || res.Session == nil {
		t.Fatalf("SignIn = %+v, %v", res, err)
	}
	ss := svSession(t, e, res.Session.SessionID)
	if ss.Platform != "iphone" || ss.ClientType != "web" || ss.DeviceName != "Asha's iPhone" {
		t.Errorf("session = platform %q client_type %q name %q", ss.Platform, ss.ClientType, ss.DeviceName)
	}
}

func TestSignIn_PasswordIsCheckedInUnicodeNFC(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.NewUser(t, authtest.UserSpec{Password: "café-lantern-42"}) // composed
	in := svInput(u, uuid.New())
	in.Password = "café-lantern-42" // decomposed
	if res, err := s.SignIn(t.Context(), in); err != nil || res.Session == nil {
		t.Fatalf("a differently composed but canonically equal password must sign in: %+v, %v", res, err)
	}
}

func TestSignIn_BR_CLI_04_NotActiveAndWrongPasswordGenericFailure(t *testing.T) {
	tests := []struct {
		name      string
		status    auth.Status
		password  string // "" = the right one
		reason    string
		wantCount int // failures recorded against the account
	}{
		{"pending_verification right password is not counted", auth.StatusPendingVerification, "", auth.ReasonNotActive, 0},
		{"pending_approval right password is not counted", auth.StatusPendingApproval, "", auth.ReasonNotActive, 0},
		{"disabled right password is not counted", auth.StatusDisabled, "", auth.ReasonNotActive, 0},
		{"active wrong password is counted", auth.StatusActive, wrongPassword, auth.ReasonWrongPassword, 1},
		{"pending_verification wrong password is counted", auth.StatusPendingVerification, wrongPassword, auth.ReasonWrongPassword, 1},
	}
	var first string
	for _, tt := range tests {
		t.Run("BR_CLI_04 "+tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, tt.status)
			in := svInput(u, uuid.New())
			if tt.password != "" {
				in.Password = tt.password
			}
			res, err := s.SignIn(t.Context(), in)
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatalf("err = %v, want ErrInvalidCredentials", err)
			}
			if res.Session != nil || res.Challenge != nil || res.NextStep != "" {
				t.Errorf("a failed sign-in returned %+v", res)
			}
			if first == "" {
				first = err.Error()
			} else if err.Error() != first {
				t.Errorf("error text %q differs from %q: the failure must be identical in every case", err.Error(), first)
			}
			if n := svAllSessions(t, e, u.ID); n != 0 {
				t.Errorf("sessions = %d, want 0", n)
			}
			if n := svCount(t, e, `SELECT count(*) FROM refresh_tokens`); n != 0 {
				t.Errorf("refresh_tokens = %d, want 0", n)
			}
			if got := len(svThrottle(t, e, svUserKey(u)).Failures); got != tt.wantCount {
				t.Errorf("counted failures = %d, want %d", got, tt.wantCount)
			}
			rows := e.AuditOf(t, auth.ActionSignInFailed)
			if len(rows) != 1 {
				t.Fatalf("sign_in.failed rows = %d, want 1", len(rows))
			}
			after := svJSON(t, rows[0].After)
			if rows[0].ActorKind != audit.KindSystem || rows[0].ActorID != uuid.Nil || rows[0].EntityID != u.ID ||
				after["reason"] != tt.reason || after["identifier_kind"] != "email" || after["ip"] != testIP {
				t.Errorf("row = %+v after = %v", rows[0], after)
			}
			if _, has := after["subject_ref"]; has {
				t.Errorf("subject_ref is only for unknown identifiers: %v", after)
			}
			if got := len(e.AuditOf(t, auth.ActionSignInSucceeded)); got != 0 {
				t.Errorf("sign_in.succeeded rows = %d", got)
			}
			e.Secrets.Add("wrong password", wrongPassword)
			e.Secrets.ScanAuditLog(t.Context(), t, e.Pool)
		})
	}
}

func TestSignIn_UnknownIdentifierIsIdenticalAndAuditedWithoutTheTypedValue(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	known := e.UserWithStatus(t, auth.StatusActive)
	in := svInput(known, uuid.New())
	in.Password = wrongPassword
	_, wrongErr := s.SignIn(t.Context(), in)
	if !errors.Is(wrongErr, auth.ErrInvalidCredentials) {
		t.Fatalf("baseline wrong-password err = %v", wrongErr)
	}

	for _, tt := range []struct{ typed, kind, key string }{
		{"  Nobody@Example.com ", "email", "email|nobody@example.com"},
		{"+91 90000-00000", "mobile", "mobile|+919000000000"},
	} {
		t.Run(tt.typed, func(t *testing.T) {
			in := svInput(known, uuid.New())
			in.Identifier = tt.typed
			res, err := s.SignIn(t.Context(), in)
			if !errors.Is(err, auth.ErrInvalidCredentials) || err.Error() != wrongErr.Error() {
				t.Fatalf("err = %v, want exactly the wrong-password failure %v", err, wrongErr)
			}
			if res.Session != nil || res.Challenge != nil {
				t.Errorf("result = %+v", res)
			}
			if got := len(svThrottle(t, e, tt.key).Failures); got != 1 {
				t.Errorf("failures under %s = %d, want 1 (unknown identifiers are counted too)", tt.key, got)
			}
			var row authtest.AuditRow
			for _, r := range e.AuditOf(t, auth.ActionSignInFailed) {
				if r.EntityID == uuid.Nil && strings.Contains(r.After, tt.kind) {
					row = r
				}
			}
			if row.Action == "" {
				t.Fatalf("no sign_in.failed row with a NULL entity for %s", tt.kind)
			}
			after := svJSON(t, row.After)
			wantRef := hex.EncodeToString(svSubject(e, "signin", tt.key)[:8])
			if row.ActorKind != audit.KindSystem || row.Entity != auth.EntityUser || after["reason"] != auth.ReasonUnknownIdentifier ||
				after["identifier_kind"] != tt.kind || after["subject_ref"] != wantRef {
				t.Errorf("row = %+v after = %v, want reason unknown_identifier and subject_ref %s", row, after, wantRef)
			}
			dump := row.Before + row.After
			for _, frag := range []string{"nobody", "Nobody", "90000", "9000000000"} {
				if strings.Contains(dump, frag) {
					t.Errorf("audit row holds the identifier as typed (%q): %s", frag, dump)
				}
			}
		})
	}
}

func TestSignIn_AdminByMobileIsGenericFailureAndCountsAsNoMatch(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	admin := e.NewUser(t, authtest.UserSpec{Role: "admin", Mobile: "+919811111111"})
	in := svInput(admin, uuid.New())
	in.Identifier = "9811111111"
	if _, err := s.SignIn(t.Context(), in); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("admin by mobile: err = %v, want the generic failure (D-04)", err)
	}
	if n := svCount(t, e, `SELECT count(*) FROM mfa_challenges`); n != 0 {
		t.Errorf("challenges = %d, want 0", n)
	}
	if n := svAllSessions(t, e, admin.ID); n != 0 {
		t.Errorf("sessions = %d, want 0", n)
	}
	rows := e.AuditOf(t, auth.ActionSignInFailed)
	if len(rows) != 1 || rows[0].EntityID != uuid.Nil || svJSON(t, rows[0].After)["reason"] != auth.ReasonUnknownIdentifier {
		t.Errorf("audit = %+v, want one unknown_identifier row with no entity", rows)
	}
	if got := len(svThrottle(t, e, "mobile|+919811111111").Failures); got != 1 {
		t.Errorf("failures under the mobile subject = %d, want 1", got)
	}
	if got := len(svThrottle(t, e, svUserKey(admin)).Failures); got != 0 {
		t.Errorf("the admin account itself must not be charged: %d", got)
	}
}

func TestSignIn_MalformedIdentifierIsAValidationErrorNotCounted(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	for _, id := range []string{"12345", "", "a@", "@example.com", "98765 4321", "+1 202 555 0100", "5876543210"} {
		in := svInput(u, uuid.New())
		in.Identifier = id
		_, err := s.SignIn(t.Context(), in)
		if !errors.Is(err, auth.ErrInvalidIdentifier) || errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("SignIn(%q): err = %v, want ErrInvalidIdentifier only", id, err)
		}
	}
	if n := len(e.AuditRows(t)); n != 0 {
		t.Errorf("audit rows = %d, want 0 for malformed input", n)
	}
	if n := svCount(t, e, `SELECT count(*) FROM auth_throttles`); n != 0 {
		t.Errorf("throttle rows = %d, want 0 for malformed input", n)
	}
}

func TestSignIn_SuccessResetsTheFailureCounter(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	for range 3 {
		svWrong(t, s, u)
	}
	if got := len(svThrottle(t, e, svUserKey(u)).Failures); got != 3 {
		t.Fatalf("precondition: failures = %d, want 3", got)
	}
	svSignIn(t, s, u, uuid.New())
	if st := svThrottle(t, e, svUserKey(u)); len(st.Failures) != 0 || st.LockedUntil != nil {
		t.Errorf("throttle after success = %+v, want cleared", st)
	}
}

func TestSignIn_FailedAttemptsOfEmailAndMobileShareOneAccountCounter(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	for i, id := range []string{u.Email, u.Mobile, u.Email} {
		in := svInput(u, uuid.New())
		in.Identifier, in.Password = id, wrongPassword
		if _, err := s.SignIn(t.Context(), in); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if got := len(svThrottle(t, e, svUserKey(u)).Failures); got != 3 {
		t.Errorf("account counter = %d, want 3 (the lock is per account, not per identifier)", got)
	}
	if got := len(svThrottle(t, e, "email|"+u.Email).Failures) + len(svThrottle(t, e, "mobile|"+u.Mobile).Failures); got != 0 {
		t.Errorf("identifier subjects hold %d failures of a real account, want 0", got)
	}
}

func TestSignIn_SameInstallationReplacesItsPreviousSession(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	device := uuid.New()
	first := svSignIn(t, s, u, device)
	e.Clock.Advance(time.Minute)
	second := svSignIn(t, s, u, device)
	if first.SessionID == second.SessionID {
		t.Fatalf("a new session id is required")
	}
	if ss := svSession(t, e, first.SessionID); ss.EndedAt == nil || ss.EndReason != "replaced" {
		t.Errorf("first session ended=%v reason=%q, want replaced", ss.EndedAt, ss.EndReason)
	}
	if svSession(t, e, second.SessionID).EndedAt != nil {
		t.Errorf("second session must be live")
	}
	if n := svLiveSessions(t, e, u.ID); n != 1 {
		t.Errorf("live sessions = %d, want 1", n)
	}
	rows := e.AuditOf(t, auth.ActionSignInSucceeded)
	if len(rows) != 2 || svJSON(t, rows[1].After)["replaced_session_id"] != first.SessionID.String() {
		t.Errorf("second audit row must record replaced_session_id: %+v", rows)
	}
	// The same user from another installation keeps both.
	svSignIn(t, s, u, uuid.New())
	if n := svLiveSessions(t, e, u.ID); n != 2 {
		t.Errorf("live sessions after a second installation = %d, want 2", n)
	}
}

func TestSignIn_BR_CLI_04_AdminGetsAChallengeNeverASession(t *testing.T) {
	tests := []struct {
		name     string
		enrolled bool
		next     string
		purpose  string
	}{
		{"enrolled admin must verify", true, auth.NextStepMFARequired, "verify"},
		{"fresh admin must enrol", false, auth.NextStepMFAEnrolmentNeeded, "enrol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			admin := e.NewAdmin(t)
			if tt.enrolled {
				svEnrol(t, e, admin.ID)
			}
			device := uuid.New()
			in := svInput(admin, device)
			in.ClientType = auth.ClientTypeWeb
			in.Device = auth.DeviceInfo{ID: device, Name: "Chrome on Mac", Platform: auth.PlatformWeb}
			res, err := s.SignIn(t.Context(), in)
			if err != nil {
				t.Fatalf("SignIn: %v", err)
			}
			if res.NextStep != tt.next || res.Session != nil || res.Challenge == nil {
				t.Fatalf("result = %+v, want %s with a challenge and no session", res, tt.next)
			}
			ch := res.Challenge
			e.Secrets.Add("challenge token", ch.Token)
			if !strings.HasPrefix(ch.Token, "dmc_") || !ch.ExpiresAt.Equal(e.Clock.Now().Add(5*time.Minute)) {
				t.Errorf("challenge = %+v, want dmc_ token expiring in 5 minutes", ch)
			}
			row, err := e.Store.FindChallengeByHash(t.Context(), crypto.Digest(ch.Token))
			if err != nil {
				t.Fatalf("challenge not stored by digest: %v", err)
			}
			if row.UserID != admin.ID || row.OrgID != e.Org || row.Purpose != tt.purpose || row.DeviceID != device ||
				row.DeviceName != "Chrome on Mac" || row.Platform != "web" || row.ClientType != "web" || row.ConsumedAt != nil {
				t.Errorf("challenge row = %+v", row)
			}
			if n := svAllSessions(t, e, admin.ID); n != 0 {
				t.Errorf("sessions = %d, want 0 until the second factor completes", n)
			}
			if n := svCount(t, e, `SELECT count(*) FROM refresh_tokens`); n != 0 {
				t.Errorf("refresh tokens = %d, want 0", n)
			}
			if svUserRow(t, e, admin.ID).LastSignInAt != nil {
				t.Errorf("last_sign_in_at must wait for the completed sign-in")
			}
			rows := e.AuditOf(t, auth.ActionMFAChallengeIssued)
			if len(rows) != 1 || rows[0].ActorKind != audit.KindSystem || rows[0].EntityID != admin.ID || svJSON(t, rows[0].After)["purpose"] != tt.purpose {
				t.Errorf("challenge_issued audit = %+v", rows)
			}
			if got := len(e.AuditOf(t, auth.ActionSignInSucceeded)); got != 0 {
				t.Errorf("sign_in.succeeded rows = %d, want 0 before the second factor", got)
			}
			if st := svThrottle(t, e, svUserKey(admin)); len(st.Failures) != 0 {
				t.Errorf("a correct password must not be counted: %+v", st)
			}
			e.Secrets.ScanAuditLog(t.Context(), t, e.Pool)
		})
	}
}

func TestSignIn_AdminWrongPasswordIsGenericAndCounted(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	admin := e.NewAdmin(t)
	svEnrol(t, e, admin.ID)
	in := svInput(admin, uuid.New())
	in.Password = wrongPassword
	if _, err := s.SignIn(t.Context(), in); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("err = %v, want the generic failure", err)
	}
	if n := svCount(t, e, `SELECT count(*) FROM mfa_challenges`); n != 0 {
		t.Errorf("challenges = %d, want 0", n)
	}
	if got := len(svThrottle(t, e, svUserKey(admin)).Failures); got != 1 {
		t.Errorf("failures = %d, want 1", got)
	}
}

func TestSignIn_BR_CLI_04_ClientIsNeverAskedForTOTP(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	res, err := s.SignIn(t.Context(), svInput(u, uuid.New()))
	if err != nil || res.NextStep != auth.NextStepSignedIn || res.Challenge != nil {
		t.Fatalf("client sign-in = %+v, %v; want signed_in without a challenge (FR-065)", res, err)
	}
}

func TestSignIn_RehashesAnOutdatedHashOnSuccessOnly(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	old := auth.NewHasher(auth.HasherOptions{Params: auth.HasherParams{MemoryKiB: 16, Time: 1, Threads: 1}})
	phc, err := old.Hash(t.Context(), auth.NormalizePassword(u.Password))
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(t, `UPDATE users SET password_hash = $2 WHERE id = $1`, u.ID, phc)

	svWrong(t, s, u)
	if got := svUserRow(t, e, u.ID).PasswordHash; got != phc {
		t.Fatalf("a failed sign-in must not rehash")
	}

	e.Clock.Advance(time.Hour)
	svSignIn(t, s, u, uuid.New())
	row := svUserRow(t, e, u.ID)
	params, _, _, err := auth.DecodePHC(row.PasswordHash)
	if err != nil || params != authtest.CheapHasherParams {
		t.Errorf("hash after success has params %+v (%v), want the current %+v", params, err, authtest.CheapHasherParams)
	}
	if ok, _ := e.Hasher.Verify(t.Context(), row.PasswordHash, auth.NormalizePassword(u.Password)); !ok {
		t.Errorf("rehashed value must still verify the password")
	}
	if !row.PasswordChangedAt.Equal(authtest.T0) {
		t.Errorf("password_changed_at = %v, a rehash is not a change (want %v)", row.PasswordChangedAt, authtest.T0)
	}
}

func TestSignIn_ExactlyOneArgon2RunPerAttempt(t *testing.T) {
	e := authtest.New(t)
	var runs atomic.Int64
	d := e.Deps()
	d.Hasher = auth.NewHasher(auth.HasherOptions{Params: authtest.CheapHasherParams, OnArgon2: func(auth.HasherParams) { runs.Add(1) }})
	s := e.ServiceWith(t, d)

	active := e.UserWithStatus(t, auth.StatusActive)
	pending := e.UserWithStatus(t, auth.StatusPendingVerification)
	disabled := e.UserWithStatus(t, auth.StatusDisabled)
	locked := e.UserWithStatus(t, auth.StatusActive)
	admin := e.NewUser(t, authtest.UserSpec{Role: "admin", Mobile: "+919811111111"})
	for range 5 {
		svWrong(t, s, locked)
	}

	attempt := func(u authtest.User, identifier, password string) auth.SignInInput {
		in := svInput(u, uuid.New())
		in.Identifier, in.Password = identifier, password
		return in
	}
	tests := []struct {
		name string
		in   auth.SignInInput
	}{
		{"success", attempt(active, active.Email, active.Password)},
		{"wrong password", attempt(active, active.Email, wrongPassword)},
		{"unknown email", attempt(active, "ghost@example.com", wrongPassword)},
		{"unknown mobile", attempt(active, "9000000000", wrongPassword)},
		{"locked, right password", attempt(locked, locked.Email, locked.Password)},
		{"pending_verification, right password", attempt(pending, pending.Email, pending.Password)},
		{"disabled, right password", attempt(disabled, disabled.Email, disabled.Password)},
		{"admin by mobile", attempt(admin, "9811111111", admin.Password)},
		{"admin by email (challenge)", attempt(admin, admin.Email, admin.Password)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs.Store(0)
			_, _ = s.SignIn(t.Context(), tt.in)
			if got := runs.Load(); got != 1 {
				t.Errorf("Argon2 runs = %d, want exactly 1 (real or dummy) so timing reveals nothing", got)
			}
		})
	}
}

func TestSignIn_ArgonIsNeverRunInsideATransaction(t *testing.T) {
	e := authtest.New(t)
	var inTx atomic.Bool
	d := e.Deps()
	d.Hasher = auth.NewHasher(auth.HasherOptions{Params: authtest.CheapHasherParams, OnArgon2: func(auth.HasherParams) {
		// While the hook runs the hasher holds its slot; any open transaction of this database
		// at that moment is one the service holds across hashing.
		var n int
		err := e.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND state LIKE 'idle in transaction%'`).Scan(&n)
		if err != nil {
			t.Errorf("pg_stat_activity: %v", err)
		}
		if n > 0 {
			inTx.Store(true)
		}
	}})
	s := e.ServiceWith(t, d)
	u := e.UserWithStatus(t, auth.StatusActive)
	svSignIn(t, s, u, uuid.New())
	svWrong(t, s, u)
	if inTx.Load() {
		t.Errorf("Argon2 ran while a transaction was open (plan P-06: never inside a transaction)")
	}
}
