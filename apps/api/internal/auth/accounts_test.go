package auth_test

// S11 - Service tests A, Accounts capability (FR-050..FR-055, US-8 s4-6, US-10, BR-CLI-04,
// BR-CLI-05 boundary; contracts/go-capabilities.md section 2). Real Store, fake clock.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

// svJSON decodes an audit before/after column.
func svJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("audit JSON %q: %v", raw, err)
	}
	return m
}

func svNewClient(t *testing.T, s *auth.Service, e *authtest.Env, email, mobile string) uuid.UUID {
	t.Helper()
	id, err := s.CreateClient(t.Context(), auth.NewClient{
		OrgID: e.Org, Name: "Ravi Shah", Email: email, Mobile: mobile, Password: authtest.Password,
	}, authtest.System())
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	return id
}

func TestAccounts_CreateClientNormalisesAndStoresPending(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	id, err := s.CreateClient(t.Context(), auth.NewClient{
		OrgID: e.Org, Name: "Ravi", Email: "Ravi@Example.com", Mobile: "98123 45678", Password: authtest.Password,
	}, authtest.System())
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	u := svUserRow(t, e, id)
	if u.Role != "client" || u.Status != auth.StatusPendingVerification || u.OrgID != e.Org || u.Name != "Ravi" {
		t.Errorf("user = role %q status %q org %v name %q", u.Role, u.Status, u.OrgID, u.Name)
	}
	if u.Email != "ravi@example.com" || u.Mobile != "+919812345678" {
		t.Errorf("stored contacts = %q %q, want ravi@example.com +919812345678", u.Email, u.Mobile)
	}
	if u.EmailVerifiedAt != nil || u.MobileVerifiedAt != nil {
		t.Errorf("a new client must be unverified: %v %v", u.EmailVerifiedAt, u.MobileVerifiedAt)
	}
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") || strings.Contains(u.PasswordHash, authtest.Password) {
		t.Errorf("password must be stored as an Argon2id PHC string, got %q", u.PasswordHash)
	}
	ok, err := e.Hasher.Verify(t.Context(), u.PasswordHash, auth.NormalizePassword(authtest.Password))
	if err != nil || !ok {
		t.Errorf("stored hash does not verify the password: ok=%v err=%v", ok, err)
	}
	rows := e.AuditOf(t, auth.ActionUserCreated)
	if len(rows) != 1 {
		t.Fatalf("audit %s rows = %d, want 1", auth.ActionUserCreated, len(rows))
	}
	r := rows[0]
	if r.ActorKind != audit.KindSystem || r.ActorID != uuid.Nil || r.Entity != auth.EntityUser || r.EntityID != id {
		t.Errorf("audit row = %+v, want system actor, entity user %v", r, id)
	}
	after := svJSON(t, r.After)
	if after["email"] != "ravi@example.com" || after["mobile"] != "+919812345678" || after["status"] != "pending_verification" || after["role"] != "client" {
		t.Errorf("audit after = %v", after)
	}
	e.Secrets.Scan(t, "the user.created audit row", r.After)
	e.Secrets.ScanAuditLog(t.Context(), t, e.Pool)
}

func TestAccounts_CreateClientAuditActorFollowsCaller(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	admin := e.NewAdmin(t)
	id, err := s.CreateClient(t.Context(), auth.NewClient{
		OrgID: e.Org, Name: "Ravi", Email: "ravi@example.com", Mobile: "9812345678", Password: authtest.Password,
	}, authtest.Admin(admin.ID))
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	rows := e.AuditOf(t, auth.ActionUserCreated)
	if len(rows) != 1 || rows[0].ActorKind != audit.KindAdmin || rows[0].ActorID != admin.ID || rows[0].EntityID != id {
		t.Errorf("audit = %+v, want one row by admin %v for %v", rows, admin.ID, id)
	}
}

func TestAccounts_CreateClientRejectsDuplicatesAndStoresNothing(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	e.NewUser(t, authtest.UserSpec{Email: "taken@example.com", Mobile: "+919812345678"})
	before := svUserCount(t, e)
	auditBefore := len(e.AuditRows(t))

	tests := []struct {
		name          string
		email, mobile string
		want          error
	}{
		{"email in another case and with spaces", "  TAKEN@Example.com ", "9000000001", auth.ErrEmailTaken},
		{"mobile with 0 prefix", "fresh1@example.com", "09812345678", auth.ErrMobileTaken},
		{"mobile with country code and hyphen", "fresh2@example.com", "+91 98123-45678", auth.ErrMobileTaken},
		{"mobile 91 prefix", "fresh3@example.com", "919812345678", auth.ErrMobileTaken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateClient(t.Context(), auth.NewClient{
				OrgID: e.Org, Name: "Dup", Email: tt.email, Mobile: tt.mobile, Password: authtest.Password,
			}, authtest.System())
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if got := svUserCount(t, e); got != before {
		t.Errorf("users = %d, want %d (nothing stored)", got, before)
	}
	if got := len(e.AuditRows(t)); got != auditBefore {
		t.Errorf("audit rows = %d, want %d (no audit for a refused create)", got, auditBefore)
	}
}

func TestAccounts_CreateClientRejectsBadIdentifiers(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	tests := []struct{ name, email, mobile string }{
		{"mobile too short", "a@example.com", "98123"},
		{"mobile starts with 5", "b@example.com", "5812345678"},
		{"email without at", "not-an-email", "9812345678"},
		{"email with display name", "Ravi <r@example.com>", "9812345678"},
		{"empty mobile for a client", "c@example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateClient(t.Context(), auth.NewClient{
				OrgID: e.Org, Name: "Bad", Email: tt.email, Mobile: tt.mobile, Password: authtest.Password,
			}, authtest.System())
			if !errors.Is(err, auth.ErrInvalidIdentifier) {
				t.Fatalf("err = %v, want ErrInvalidIdentifier", err)
			}
		})
	}
	if got := svUserCount(t, e); got != 0 {
		t.Errorf("users = %d, want 0", got)
	}
}

func TestAccounts_CreateClientPolicyErrorStoresNothing(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	tests := []struct{ name, password, code string }{
		{"too short", "short-pw9", auth.PolicyTooShort},
		{"too long", strings.Repeat("a", 257), auth.PolicyTooLong},
		{"common 1234567890", "1234567890", auth.PolicyTooCommon},
		{"common upper case", "QWERTYUIOP", auth.PolicyTooCommon},
		{"equals own email", "ravi@example.com", auth.PolicyMatchesIdentifier},
		{"equals own mobile", "9812345678", auth.PolicyMatchesIdentifier},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateClient(t.Context(), auth.NewClient{
				OrgID: e.Org, Name: "Ravi", Email: "ravi@example.com", Mobile: "9812345678", Password: tt.password,
			}, authtest.System())
			var pe *auth.PolicyError
			if !errors.As(err, &pe) || pe.Code != tt.code {
				t.Fatalf("err = %v, want PolicyError %s", err, tt.code)
			}
		})
	}
	if got := svUserCount(t, e); got != 0 {
		t.Errorf("users = %d, want 0", got)
	}
}

func TestAccounts_CreateClientAcceptsLongUnicodePassphrase(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	pass := strings.Repeat("નમસ્તે ગુજરાત ", 5) // spaces and Gujarati letters
	if n := len([]rune(pass)); n < 40 {
		t.Fatalf("fixture passphrase too short: %d", n)
	}
	_, err := s.CreateClient(t.Context(), auth.NewClient{
		OrgID: e.Org, Name: "Ravi", Email: "ravi@example.com", Mobile: "9812345678", Password: pass,
	}, authtest.System())
	if err != nil {
		t.Fatalf("CreateClient with a Gujarati passphrase: %v", err)
	}
}

func TestAccounts_CreateClientJoinsCallerTransaction(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	boom := errors.New("caller failed")
	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		if _, err := s.CreateClient(ctx, auth.NewClient{
			OrgID: e.Org, Name: "Ravi", Email: "ravi@example.com", Mobile: "9812345678", Password: authtest.Password,
		}, authtest.System()); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx err = %v, want the caller's error", err)
	}
	if got := svUserCount(t, e); got != 0 {
		t.Errorf("users = %d after the caller rolled back, want 0", got)
	}
	if got := len(e.AuditRows(t)); got != 0 {
		t.Errorf("audit rows = %d after the caller rolled back, want 0 (audit joins the transaction)", got)
	}

	if err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, err := s.CreateClient(ctx, auth.NewClient{
			OrgID: e.Org, Name: "Ravi", Email: "ravi@example.com", Mobile: "9812345678", Password: authtest.Password,
		}, authtest.System())
		return err
	}); err != nil {
		t.Fatalf("committed InTx: %v", err)
	}
	if got := svUserCount(t, e); got != 1 {
		t.Errorf("users = %d after commit, want 1", got)
	}
}

func TestAccounts_CreateClientConcurrentSameEmailOneWins(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	errs := stRace(6, func(i int) error {
		_, err := s.CreateClient(t.Context(), auth.NewClient{
			OrgID: e.Org, Name: "Racer", Email: "same@example.com", Mobile: "+9190000000" + string(rune('0'+i)) + "1", Password: authtest.Password,
		}, authtest.System())
		return err
	})
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, auth.ErrEmailTaken):
			t.Errorf("loser err = %v, want ErrEmailTaken", err)
		}
	}
	if wins != 1 {
		t.Errorf("winners = %d, want exactly 1", wins)
	}
	if got := svUserCount(t, e); got != 1 {
		t.Errorf("users = %d, want 1", got)
	}
}

func TestAccounts_BR_CLI_04_ActiveOnlyAfterBothVerifiedAndApproved(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	id := svNewClient(t, s, e, "ravi@example.com", "9812345678")
	u := authtest.User{ID: id, Email: "ravi@example.com", Password: authtest.Password}
	admin := authtest.Admin(uuid.New())

	expectBlocked := func(stage string, want auth.Status) {
		t.Helper()
		if got := svUserRow(t, e, id).Status; got != want {
			t.Fatalf("%s: status = %q, want %q", stage, got, want)
		}
		if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("%s: sign-in err = %v, want the generic failure", stage, err)
		}
	}
	expectBlocked("created", auth.StatusPendingVerification)

	st, err := s.MarkEmailVerified(t.Context(), id, authtest.System())
	if err != nil || st != auth.StatusPendingVerification {
		t.Fatalf("MarkEmailVerified = %q, %v; want pending_verification", st, err)
	}
	expectBlocked("email verified", auth.StatusPendingVerification)

	st, err = s.MarkMobileVerified(t.Context(), id, authtest.System())
	if err != nil || st != auth.StatusPendingApproval {
		t.Fatalf("MarkMobileVerified = %q, %v; want pending_approval", st, err)
	}
	expectBlocked("both verified", auth.StatusPendingApproval)

	if err := s.Approve(t.Context(), id, admin); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := svUserRow(t, e, id).Status; got != auth.StatusActive {
		t.Fatalf("after approve status = %q, want active", got)
	}
	svSignIn(t, s, u, uuid.New())

	rows := e.AuditOf(t, auth.ActionUserStatusChanged)
	if len(rows) == 0 {
		t.Fatalf("approval must write %s", auth.ActionUserStatusChanged)
	}
	last := rows[len(rows)-1]
	if before := svJSON(t, last.Before); before["status"] != "pending_approval" {
		t.Errorf("status_changed before = %v", before)
	}
	if after := svJSON(t, last.After); after["status"] != "active" {
		t.Errorf("status_changed after = %v", after)
	}
	if last.ActorKind != audit.KindAdmin || last.EntityID != id {
		t.Errorf("status_changed row = %+v", last)
	}
}

func TestAccounts_BR_CLI_04_VerificationOrderIndependent(t *testing.T) {
	tests := []struct {
		name   string
		first  func(*auth.Service, context.Context, uuid.UUID) (auth.Status, error)
		second func(*auth.Service, context.Context, uuid.UUID) (auth.Status, error)
	}{
		{"email then mobile",
			func(s *auth.Service, c context.Context, id uuid.UUID) (auth.Status, error) {
				return s.MarkEmailVerified(c, id, authtest.System())
			},
			func(s *auth.Service, c context.Context, id uuid.UUID) (auth.Status, error) {
				return s.MarkMobileVerified(c, id, authtest.System())
			}},
		{"mobile then email",
			func(s *auth.Service, c context.Context, id uuid.UUID) (auth.Status, error) {
				return s.MarkMobileVerified(c, id, authtest.System())
			},
			func(s *auth.Service, c context.Context, id uuid.UUID) (auth.Status, error) {
				return s.MarkEmailVerified(c, id, authtest.System())
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			id := svNewClient(t, s, e, "ravi@example.com", "9812345678")
			if st, err := tt.first(s, t.Context(), id); err != nil || st != auth.StatusPendingVerification {
				t.Fatalf("first mark = %q, %v; want pending_verification", st, err)
			}
			if st, err := tt.second(s, t.Context(), id); err != nil || st != auth.StatusPendingApproval {
				t.Fatalf("second mark = %q, %v; want pending_approval", st, err)
			}
			u := svUserRow(t, e, id)
			if u.EmailVerifiedAt == nil || u.MobileVerifiedAt == nil {
				t.Errorf("both verified timestamps must be set: %v %v", u.EmailVerifiedAt, u.MobileVerifiedAt)
			}
		})
	}
}

func TestAccounts_MarkVerifiedIsIdempotent(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	id := svNewClient(t, s, e, "ravi@example.com", "9812345678")
	if _, err := s.MarkEmailVerified(t.Context(), id, authtest.System()); err != nil {
		t.Fatal(err)
	}
	first := *svUserRow(t, e, id).EmailVerifiedAt
	auditN := len(e.AuditOf(t, auth.ActionUserEmailVerified))
	e.Clock.Advance(time.Hour)
	st, err := s.MarkEmailVerified(t.Context(), id, authtest.System())
	if err != nil || st != auth.StatusPendingVerification {
		t.Fatalf("second MarkEmailVerified = %q, %v", st, err)
	}
	if got := *svUserRow(t, e, id).EmailVerifiedAt; !got.Equal(first) {
		t.Errorf("verified_at moved from %v to %v; the first timestamp must stay", first, got)
	}
	if got := len(e.AuditOf(t, auth.ActionUserEmailVerified)); got != auditN {
		t.Errorf("email_verified audit rows = %d, want %d (a repeat writes nothing)", got, auditN)
	}
	// Both verified, then marking again keeps pending_approval.
	if _, err := s.MarkMobileVerified(t.Context(), id, authtest.System()); err != nil {
		t.Fatal(err)
	}
	if st, err := s.MarkMobileVerified(t.Context(), id, authtest.System()); err != nil || st != auth.StatusPendingApproval {
		t.Errorf("repeat MarkMobileVerified = %q, %v; want pending_approval", st, err)
	}
}

func TestAccounts_MarkVerifiedOnActiveUserKeepsActive(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	if st, err := s.MarkEmailVerified(t.Context(), u.ID, authtest.System()); err != nil || st != auth.StatusActive {
		t.Errorf("MarkEmailVerified on active = %q, %v; want active", st, err)
	}
}

func TestAccounts_MarkVerifiedUnknownUser(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	if _, err := s.MarkEmailVerified(t.Context(), uuid.New(), authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("err = %v, want ErrUserNotFound", err)
	}
	if _, err := s.MarkMobileVerified(t.Context(), uuid.New(), authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("err = %v, want ErrUserNotFound", err)
	}
}

func TestAccounts_BR_CLI_04_ApproveRefusedUnlessPendingApproval(t *testing.T) {
	for _, from := range []auth.Status{auth.StatusPendingVerification, auth.StatusActive, auth.StatusDisabled} {
		t.Run(string(from), func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, from)
			auditBefore := len(e.AuditRows(t))
			if err := s.Approve(t.Context(), u.ID, authtest.Admin(uuid.New())); !errors.Is(err, auth.ErrInvalidTransition) {
				t.Fatalf("Approve from %s: err = %v, want ErrInvalidTransition", from, err)
			}
			if got := svUserRow(t, e, u.ID).Status; got != from {
				t.Errorf("status = %q, want %q unchanged", got, from)
			}
			if got := len(e.AuditRows(t)); got != auditBefore {
				t.Errorf("audit rows = %d, want %d", got, auditBefore)
			}
		})
	}
	t.Run("unknown user", func(t *testing.T) {
		e := authtest.New(t)
		if err := e.Service(t).Approve(t.Context(), uuid.New(), authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("err = %v, want ErrUserNotFound", err)
		}
	})
}

func TestAccounts_BR_CLI_04_DisableEndsSessionsAndEnableRestoresPriorStatus(t *testing.T) {
	for _, from := range []auth.Status{auth.StatusPendingVerification, auth.StatusPendingApproval, auth.StatusActive} {
		t.Run(string(from), func(t *testing.T) {
			e := authtest.New(t)
			s := e.Service(t)
			u := e.UserWithStatus(t, from)
			s1 := e.NewSession(t, u.ID, authtest.SessionSpec{})
			s2 := e.NewSession(t, u.ID, authtest.SessionSpec{})
			admin := authtest.Admin(e.NewAdmin(t).ID)
			// A second active admin exists, so the admin guard cannot interfere.
			e.NewAdmin(t)

			if err := s.Disable(t.Context(), u.ID, admin); err != nil {
				t.Fatalf("Disable: %v", err)
			}
			row := svUserRow(t, e, u.ID)
			if row.Status != auth.StatusDisabled || row.StatusBeforeDisabled != from {
				t.Fatalf("after disable status = %q before = %q, want disabled / %q", row.Status, row.StatusBeforeDisabled, from)
			}
			for _, id := range []uuid.UUID{s1, s2} {
				ss := svSession(t, e, id)
				if ss.EndedAt == nil || ss.EndReason != "disabled" {
					t.Errorf("session %v ended=%v reason=%q, want ended with reason disabled", id, ss.EndedAt, ss.EndReason)
				}
			}
			rows := e.AuditOf(t, auth.ActionUserStatusChanged)
			if len(rows) != 1 {
				t.Fatalf("status_changed rows = %d, want 1", len(rows))
			}
			if svJSON(t, rows[0].Before)["status"] != string(from) {
				t.Errorf("before = %s", rows[0].Before)
			}
			after := svJSON(t, rows[0].After)
			ended, _ := after["ended_session_ids"].([]any)
			if after["status"] != "disabled" || len(ended) != 2 {
				t.Errorf("after = %s, want disabled with 2 ended session ids", rows[0].After)
			}
			if rows[0].ActorKind != audit.KindAdmin || rows[0].EntityID != u.ID {
				t.Errorf("row = %+v", rows[0])
			}

			if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Errorf("sign-in while disabled: err = %v, want the generic failure", err)
			}

			e.Clock.Advance(time.Minute)
			if err := s.Enable(t.Context(), u.ID, admin); err != nil {
				t.Fatalf("Enable: %v", err)
			}
			row = svUserRow(t, e, u.ID)
			if row.Status != from || row.StatusBeforeDisabled != "" {
				t.Errorf("after enable status = %q before = %q, want %q and none", row.Status, row.StatusBeforeDisabled, from)
			}
			if svLiveSessions(t, e, u.ID) != 0 {
				t.Errorf("enabling must not bring ended sessions back")
			}
			_, err := s.SignIn(t.Context(), svInput(u, uuid.New()))
			if from == auth.StatusActive && err != nil {
				t.Errorf("active user cannot sign in after enable: %v", err)
			}
			if from != auth.StatusActive && !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Errorf("a %s user must still fail sign-in after enable (BR-CLI-04): %v", from, err)
			}
		})
	}
}

func TestAccounts_BR_CLI_04_EnableRestoresPendingApproval(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	id := svNewClient(t, s, e, "ravi@example.com", "9812345678")
	_, _ = s.MarkEmailVerified(t.Context(), id, authtest.System())
	_, _ = s.MarkMobileVerified(t.Context(), id, authtest.System())
	if err := s.Disable(t.Context(), id, authtest.System()); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := s.Enable(t.Context(), id, authtest.System()); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if got := svUserRow(t, e, id).Status; got != auth.StatusPendingApproval {
		t.Fatalf("status = %q, want pending_approval (never straight to active)", got)
	}
	u := authtest.User{ID: id, Email: "ravi@example.com", Password: authtest.Password}
	if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("sign-in after enable = %v, want the generic failure", err)
	}
}

func TestAccounts_EnableRefusedUnlessDisabled(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	if err := s.Enable(t.Context(), u.ID, authtest.System()); !errors.Is(err, auth.ErrInvalidTransition) {
		t.Errorf("Enable on active: err = %v, want ErrInvalidTransition", err)
	}
	if err := s.Enable(t.Context(), uuid.New(), authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("Enable unknown: err = %v, want ErrUserNotFound", err)
	}
	if err := s.Disable(t.Context(), uuid.New(), authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("Disable unknown: err = %v, want ErrUserNotFound", err)
	}
}

func TestAccounts_LastActiveAdminCannotBeDisabled(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	a1 := e.NewAdmin(t)
	sess := e.NewSession(t, a1.ID, authtest.SessionSpec{})
	auditBefore := len(e.AuditRows(t))

	if err := s.Disable(t.Context(), a1.ID, authtest.System()); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("Disable last admin: err = %v, want ErrLastAdmin", err)
	}
	if got := svUserRow(t, e, a1.ID).Status; got != auth.StatusActive {
		t.Errorf("status = %q, want active", got)
	}
	if svSession(t, e, sess).EndedAt != nil {
		t.Errorf("the refused disable must not end sessions")
	}
	if got := len(e.AuditRows(t)); got != auditBefore {
		t.Errorf("audit rows = %d, want %d", got, auditBefore)
	}

	a2 := e.NewAdmin(t)
	if err := s.Disable(t.Context(), a2.ID, authtest.System()); err != nil {
		t.Fatalf("Disable one of two admins: %v", err)
	}
	if err := s.Disable(t.Context(), a1.ID, authtest.System()); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("Disable the remaining admin: err = %v, want ErrLastAdmin", err)
	}
}

func TestAccounts_ResetPasswordEndsAllSessionsAndClearsLock(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s1 := e.NewSession(t, u.ID, authtest.SessionSpec{})
	s2 := e.NewSession(t, u.ID, authtest.SessionSpec{})
	for range 5 { // lock the account
		svWrong(t, s, u)
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil == nil {
		t.Fatalf("precondition: the account must be locked")
	}
	e.Clock.Advance(time.Minute)
	const newPassword = "brand-new-passphrase-31"
	e.Secrets.Add("reset password", newPassword)

	if err := s.ResetPassword(t.Context(), u.ID, newPassword, authtest.System()); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	for _, id := range []uuid.UUID{s1, s2} {
		if ss := svSession(t, e, id); ss.EndedAt == nil || ss.EndReason != "password_reset" {
			t.Errorf("session %v: ended=%v reason=%q, want password_reset", id, ss.EndedAt, ss.EndReason)
		}
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil != nil || len(st.Failures) != 0 {
		t.Errorf("throttle after reset = %+v, want cleared", st)
	}
	row := svUserRow(t, e, u.ID)
	if !row.PasswordChangedAt.Equal(e.Clock.Now()) {
		t.Errorf("password_changed_at = %v, want now %v", row.PasswordChangedAt, e.Clock.Now())
	}
	if !strings.HasPrefix(row.PasswordHash, "$argon2id$") {
		t.Errorf("hash = %q", row.PasswordHash)
	}

	if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("old password after reset: err = %v, want the generic failure", err)
	}
	in := svInput(u, uuid.New())
	in.Password = newPassword
	if res, err := s.SignIn(t.Context(), in); err != nil || res.Session == nil {
		t.Errorf("new password right after reset: res=%+v err=%v (lock must be gone)", res, err)
	}

	rows := e.AuditOf(t, auth.ActionPasswordReset)
	if len(rows) != 1 {
		t.Fatalf("password.reset rows = %d, want 1", len(rows))
	}
	after := svJSON(t, rows[0].After)
	ended, _ := after["ended_session_ids"].([]any)
	if rows[0].ActorKind != audit.KindSystem || rows[0].EntityID != u.ID || len(ended) != 2 || after["lock_cleared"] != true {
		t.Errorf("password.reset row = %+v after=%v", rows[0], after)
	}
	e.Secrets.ScanAuditLog(t.Context(), t, e.Pool)
}

func TestAccounts_ResetPasswordPolicyErrorChangesNothing(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	sess := e.NewSession(t, u.ID, authtest.SessionSpec{})
	hash := svUserRow(t, e, u.ID).PasswordHash
	for _, tt := range []struct{ pw, code string }{
		{"short", auth.PolicyTooShort}, {"1234567890", auth.PolicyTooCommon}, {strings.ToLower(u.Email), auth.PolicyMatchesIdentifier},
	} {
		var pe *auth.PolicyError
		err := s.ResetPassword(t.Context(), u.ID, tt.pw, authtest.System())
		if !errors.As(err, &pe) || pe.Code != tt.code {
			t.Errorf("ResetPassword(%q): err = %v, want %s", tt.pw, err, tt.code)
		}
	}
	if got := svUserRow(t, e, u.ID).PasswordHash; got != hash {
		t.Errorf("hash changed by a rejected reset")
	}
	if svSession(t, e, sess).EndedAt != nil {
		t.Errorf("a rejected reset must not end sessions")
	}
	if got := len(e.AuditOf(t, auth.ActionPasswordReset)); got != 0 {
		t.Errorf("password.reset rows = %d, want 0", got)
	}
	if err := s.ResetPassword(t.Context(), uuid.New(), "a-fine-password-91", authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
}

func TestAccounts_EndAllSessionsForFeature(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	other := e.UserWithStatus(t, auth.StatusActive)
	ids := []uuid.UUID{
		e.NewSession(t, u.ID, authtest.SessionSpec{}), e.NewSession(t, u.ID, authtest.SessionSpec{}), e.NewSession(t, u.ID, authtest.SessionSpec{}),
	}
	keep := e.NewSession(t, other.ID, authtest.SessionSpec{})
	admin := e.NewAdmin(t)

	n, err := s.EndAllSessions(t.Context(), u.ID, "005-approvals", authtest.Admin(admin.ID))
	if err != nil || n != 3 {
		t.Fatalf("EndAllSessions = %d, %v; want 3", n, err)
	}
	for _, id := range ids {
		if ss := svSession(t, e, id); ss.EndedAt == nil || ss.EndReason != "ended_by_feature" {
			t.Errorf("session %v: ended=%v reason=%q", id, ss.EndedAt, ss.EndReason)
		}
	}
	if svSession(t, e, keep).EndedAt != nil {
		t.Errorf("another user's session must stay live")
	}
	rows := e.AuditOf(t, auth.ActionSessionEndedByFeature)
	if len(rows) != 1 {
		t.Fatalf("ended_by_feature rows = %d, want 1", len(rows))
	}
	after := svJSON(t, rows[0].After)
	got, _ := after["session_ids"].([]any)
	if after["feature"] != "005-approvals" || len(got) != 3 || rows[0].ActorKind != audit.KindAdmin || rows[0].ActorID != admin.ID || rows[0].EntityID != u.ID {
		t.Errorf("row = %+v after = %v", rows[0], after)
	}

	if n, err := s.EndAllSessions(t.Context(), u.ID, "005-approvals", authtest.System()); err != nil || n != 0 {
		t.Errorf("second call = %d, %v; want 0, nil", n, err)
	}
	if _, err := s.EndAllSessions(t.Context(), uuid.New(), "x", authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
}

func TestAccounts_ReplaceMobile_BR_CLI_05_CapabilityKeepsStatusAndSessions(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.NewUser(t, authtest.UserSpec{Mobile: "+919811111111"})
	sess := e.NewSession(t, u.ID, authtest.SessionSpec{})
	verifiedBefore := svUserRow(t, e, u.ID).MobileVerifiedAt

	if err := s.ReplaceMobile(t.Context(), u.ID, " 98765-43210 ", authtest.Admin(e.NewAdmin(t).ID)); err != nil {
		t.Fatalf("ReplaceMobile: %v", err)
	}
	row := svUserRow(t, e, u.ID)
	if row.Mobile != "+919876543210" || row.Status != auth.StatusActive || row.Email != u.Email {
		t.Errorf("row = mobile %q status %q email %q", row.Mobile, row.Status, row.Email)
	}
	if verifiedBefore == nil || row.MobileVerifiedAt == nil {
		t.Errorf("verification timestamps unexpectedly absent")
	}
	if svSession(t, e, sess).EndedAt != nil {
		t.Errorf("replacing a contact must not end sessions (EndAllSessions is a separate capability)")
	}
	rows := e.AuditOf(t, auth.ActionUserContactReplaced)
	if len(rows) != 1 {
		t.Fatalf("contact_replaced rows = %d, want 1", len(rows))
	}
	if b, a := svJSON(t, rows[0].Before), svJSON(t, rows[0].After); b["mobile"] != "+919811111111" || a["mobile"] != "+919876543210" || rows[0].EntityID != u.ID {
		t.Errorf("before=%v after=%v", b, a)
	}

	in := svInput(u, uuid.New())
	in.Identifier = "9876543210"
	if _, err := s.SignIn(t.Context(), in); err != nil {
		t.Errorf("sign-in with the new mobile: %v", err)
	}
	in.Identifier = "9811111111"
	if _, err := s.SignIn(t.Context(), in); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("sign-in with the old mobile: err = %v, want the generic failure", err)
	}
}

func TestAccounts_ReplaceEmailNormalisesAndAudits(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	if err := s.ReplaceEmail(t.Context(), u.ID, "  New.Mail@Example.COM ", authtest.System()); err != nil {
		t.Fatalf("ReplaceEmail: %v", err)
	}
	if got := svUserRow(t, e, u.ID); got.Email != "new.mail@example.com" || got.Status != auth.StatusActive {
		t.Errorf("row = email %q status %q", got.Email, got.Status)
	}
	rows := e.AuditOf(t, auth.ActionUserContactReplaced)
	if len(rows) != 1 || svJSON(t, rows[0].Before)["email"] != u.Email || svJSON(t, rows[0].After)["email"] != "new.mail@example.com" {
		t.Errorf("audit = %+v", rows)
	}
	in := svInput(u, uuid.New())
	in.Identifier = "NEW.mail@example.com"
	if _, err := s.SignIn(t.Context(), in); err != nil {
		t.Errorf("sign-in with the new email: %v", err)
	}
}

func TestAccounts_ReplaceContactRefusals(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	a := e.NewUser(t, authtest.UserSpec{Email: "a@example.com", Mobile: "+919811111111"})
	e.NewUser(t, authtest.UserSpec{Email: "b@example.com", Mobile: "+919822222222"})
	auditBefore := len(e.AuditRows(t))

	if err := s.ReplaceEmail(t.Context(), a.ID, " B@Example.com", authtest.System()); !errors.Is(err, auth.ErrEmailTaken) {
		t.Errorf("email clash: err = %v, want ErrEmailTaken", err)
	}
	if err := s.ReplaceMobile(t.Context(), a.ID, "09822222222", authtest.System()); !errors.Is(err, auth.ErrMobileTaken) {
		t.Errorf("mobile clash: err = %v, want ErrMobileTaken", err)
	}
	if err := s.ReplaceEmail(t.Context(), a.ID, "no-at-sign", authtest.System()); !errors.Is(err, auth.ErrInvalidIdentifier) {
		t.Errorf("bad email: err = %v, want ErrInvalidIdentifier", err)
	}
	if err := s.ReplaceMobile(t.Context(), a.ID, "12345", authtest.System()); !errors.Is(err, auth.ErrInvalidIdentifier) {
		t.Errorf("bad mobile: err = %v, want ErrInvalidIdentifier", err)
	}
	if err := s.ReplaceEmail(t.Context(), uuid.New(), "x@example.com", authtest.System()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
	if got := svUserRow(t, e, a.ID); got.Email != "a@example.com" || got.Mobile != "+919811111111" {
		t.Errorf("refused replacements changed the user: %q %q", got.Email, got.Mobile)
	}
	if got := len(e.AuditRows(t)); got != auditBefore {
		t.Errorf("audit rows = %d, want %d", got, auditBefore)
	}
}

func TestAccounts_GetUserViewHasNoSecrets(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	svSignIn(t, s, u, uuid.New())

	v, err := s.GetUser(t.Context(), u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if v.ID != u.ID || v.OrgID != e.Org || v.Name != u.Name || v.Email != u.Email || v.Mobile != u.Mobile || v.Role != "client" || v.Status != auth.StatusActive {
		t.Errorf("view = %+v", v)
	}
	if !v.EmailVerified || !v.MobileVerified || !v.CreatedAt.Equal(authtest.T0) {
		t.Errorf("view flags/time = %+v", v)
	}
	if v.LastSignInAt == nil || !v.LastSignInAt.Equal(e.Clock.Now()) {
		t.Errorf("LastSignInAt = %v, want now", v.LastSignInAt)
	}
	if _, err := s.GetUser(t.Context(), uuid.New()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
}
