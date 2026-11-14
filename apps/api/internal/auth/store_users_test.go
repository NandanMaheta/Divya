package auth_test

// S08 - Store integration tests, users and org (FR-001..FR-004, D-02, D-18, plan P-12;
// data-model.md sections 1 and "Lock order"). Contract: store.go.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

func TestStoreUsers_DefaultOrg(t *testing.T) {
	t.Run("one org", func(t *testing.T) {
		e := authtest.New(t)
		got, err := e.Store.DefaultOrg(t.Context())
		if err != nil || got != e.Org {
			t.Fatalf("DefaultOrg = %v, %v; want %v", got, err, e.Org)
		}
	})
	t.Run("none", func(t *testing.T) {
		e := authtest.New(t)
		e.Exec(t, `DELETE FROM orgs`)
		if _, err := e.Store.DefaultOrg(t.Context()); !errors.Is(err, auth.ErrNoOrg) {
			t.Fatalf("DefaultOrg = %v, want ErrNoOrg", err)
		}
	})
	t.Run("many", func(t *testing.T) {
		e := authtest.New(t)
		e.Exec(t, `INSERT INTO orgs (name) VALUES ('Second')`)
		if _, err := e.Store.DefaultOrg(t.Context()); !errors.Is(err, auth.ErrMultipleOrgs) {
			t.Fatalf("DefaultOrg = %v, want ErrMultipleOrgs", err)
		}
	})
}

func TestStoreUsers_CreateAndGetRoundTrip(t *testing.T) {
	e := authtest.New(t)
	in := stNewUser(e, "asha@example.com", "+919812345678")
	in.Name = "Asha Mehta"
	created := stCreate(t, e, in)

	if created.ID == uuid.Nil || created.OrgID != e.Org || created.Role != "client" || created.Status != auth.StatusPendingVerification {
		t.Fatalf("created = %+v", created)
	}
	got, err := e.Store.GetUser(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Email != "asha@example.com" || got.Mobile != "+919812345678" || got.Name != "Asha Mehta" || got.PasswordHash != validPHC {
		t.Errorf("GetUser = %+v", got)
	}
	if got.EmailVerifiedAt != nil || got.MobileVerifiedAt != nil || got.LastSignInAt != nil || got.StatusBeforeDisabled != "" {
		t.Errorf("a new client must have no verification, sign-in or disabled state: %+v", got)
	}
	for name, ts := range map[string]time.Time{"created_at": got.CreatedAt, "updated_at": got.UpdatedAt, "password_changed_at": got.PasswordChangedAt} {
		if !ts.Equal(authtest.T0) {
			t.Errorf("%s = %v, want the injected Now %v", name, ts, authtest.T0)
		}
	}
}

func TestStoreUsers_AdminWithoutMobileAndVerifiedEmail(t *testing.T) {
	e := authtest.New(t)
	in := stNewUser(e, "boss@example.com", "")
	in.Role, in.Status = "admin", auth.StatusActive
	verified := authtest.T0
	in.EmailVerifiedAt = &verified
	u := stCreate(t, e, in)
	got, err := e.Store.GetUser(t.Context(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mobile != "" || got.Role != "admin" || got.Status != auth.StatusActive || got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(verified) {
		t.Fatalf("admin = %+v", got)
	}
	// Admins without a mobile never collide on the (absent) mobile.
	in2 := stNewUser(e, "boss2@example.com", "")
	in2.Role, in2.Status = "admin", auth.StatusActive
	stCreate(t, e, in2)
}

func TestStoreUsers_GetUserNotFound(t *testing.T) {
	e := authtest.New(t)
	if _, err := e.Store.GetUser(t.Context(), uuid.New()); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("GetUser(unknown) = %v, want ErrUserNotFound", err)
	}
}

func TestStoreUsers_DuplicateEmailAndMobileAreDomainErrors(t *testing.T) {
	e := authtest.New(t)
	stCreate(t, e, stNewUser(e, "a@example.com", "+919800000001"))

	create := func(in auth.NewUser) error {
		return e.Tx.InTx(t.Context(), func(ctx context.Context) error {
			_, err := e.Store.CreateUser(ctx, in)
			return err
		})
	}
	if err := create(stNewUser(e, "a@example.com", "+919800000002")); !errors.Is(err, auth.ErrEmailTaken) {
		t.Errorf("duplicate email = %v, want ErrEmailTaken", err)
	}
	if err := create(stNewUser(e, "b@example.com", "+919800000001")); !errors.Is(err, auth.ErrMobileTaken) {
		t.Errorf("duplicate mobile = %v, want ErrMobileTaken", err)
	}
	if err := create(stNewUser(e, "c@example.com", "+919800000003")); err != nil {
		t.Errorf("a distinct user must be created: %v", err)
	}
}

func TestStoreUsers_ConcurrentCreateUserExactlyOneWinner(t *testing.T) {
	const n = 8
	cases := []struct {
		name      string
		contact   func(i int) (email, mobile string)
		wantTaken []error // any of these is an acceptable loser error
	}{
		{"same email", func(i int) (string, string) { return "race@example.com", fmt.Sprintf("+91981000000%d", i) }, []error{auth.ErrEmailTaken}},
		{"same mobile", func(i int) (string, string) { return fmt.Sprintf("race%d@example.com", i), "+919820000000" }, []error{auth.ErrMobileTaken}},
		{"same email and mobile", func(int) (string, string) { return "both@example.com", "+919830000000" }, []error{auth.ErrEmailTaken, auth.ErrMobileTaken}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := authtest.New(t)
			errs := stRace(n, func(i int) error {
				email, mobile := tc.contact(i)
				in := stNewUser(e, email, mobile)
				return e.Tx.InTx(t.Context(), func(ctx context.Context) error {
					_, err := e.Store.CreateUser(ctx, in)
					return err
				})
			})
			wins := 0
			for i, err := range errs {
				switch {
				case err == nil:
					wins++
				case slices.ContainsFunc(tc.wantTaken, func(w error) bool { return errors.Is(err, w) }):
				default:
					t.Errorf("goroutine %d lost with %v, want one of %v", i, err, tc.wantTaken)
				}
			}
			if wins != 1 {
				t.Fatalf("%d winners, want exactly 1", wins)
			}
			var rows int
			if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("users rows = %d (%v), want 1", rows, err)
			}
		})
	}
}

func TestStoreUsers_FindUserByIdentifierIsGlobalAndExact(t *testing.T) {
	e := authtest.New(t)
	u := stCreate(t, e, stNewUser(e, "find@example.com", "+919840000000"))

	byEmail, err := e.Store.FindUserByIdentifier(t.Context(), auth.KindEmail, "find@example.com")
	if err != nil || byEmail.ID != u.ID {
		t.Fatalf("by email = %+v, %v", byEmail, err)
	}
	byMobile, err := e.Store.FindUserByIdentifier(t.Context(), auth.KindMobile, "+919840000000")
	if err != nil || byMobile.ID != u.ID {
		t.Fatalf("by mobile = %+v, %v", byMobile, err)
	}
	for _, c := range []struct {
		kind  auth.IdentifierKind
		value string
	}{
		{auth.KindEmail, "other@example.com"},
		{auth.KindMobile, "+919840000001"},
		{auth.KindEmail, "+919840000000"}, // a mobile is never matched as an email
		{auth.KindMobile, "find@example.com"},
	} {
		if _, err := e.Store.FindUserByIdentifier(t.Context(), c.kind, c.value); !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("Find(%s, %q) = %v, want ErrUserNotFound", c.kind, c.value, err)
		}
	}
}

func TestStoreUsers_FindUserByIdentifierReturnsEveryStatus(t *testing.T) {
	// The sign-in gate (BR-CLI-04) belongs to the Service; the Store never filters by status.
	e := authtest.New(t)
	for _, st := range []auth.Status{auth.StatusPendingVerification, auth.StatusPendingApproval, auth.StatusActive, auth.StatusDisabled} {
		u := e.UserWithStatus(t, st)
		got, err := e.Store.FindUserByIdentifier(t.Context(), auth.KindEmail, u.Email)
		if err != nil || got.ID != u.ID || got.Status != st {
			t.Errorf("status %s: Find = %+v, %v", st, got, err)
		}
	}
}

func TestStoreUsers_SetStatusPersistsTheStateMachine(t *testing.T) {
	e := authtest.New(t)
	u := stCreate(t, e, stNewUser(e, "sm@example.com", "+919850000000"))
	later := authtest.T0.Add(time.Hour)

	set := func(to, before auth.Status, now time.Time) error {
		return e.Tx.InTx(t.Context(), func(ctx context.Context) error { return e.Store.SetStatus(ctx, u.ID, to, before, now) })
	}
	read := func() auth.UserRow {
		got, err := e.Store.GetUser(t.Context(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if err := set(auth.StatusPendingApproval, "", later); err != nil {
		t.Fatalf("to pending_approval: %v", err)
	}
	if got := read(); got.Status != auth.StatusPendingApproval || !got.UpdatedAt.Equal(later) || got.StatusBeforeDisabled != "" {
		t.Fatalf("after verify = %+v", got)
	}
	if err := set(auth.StatusDisabled, auth.StatusPendingApproval, later.Add(time.Hour)); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := read(); got.Status != auth.StatusDisabled || got.StatusBeforeDisabled != auth.StatusPendingApproval {
		t.Fatalf("after disable = %+v, want status_before_disabled pending_approval", got)
	}
	if err := set(auth.StatusPendingApproval, "", later.Add(2*time.Hour)); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := read(); got.Status != auth.StatusPendingApproval || got.StatusBeforeDisabled != "" {
		t.Fatalf("after enable = %+v, want the previous status restored and the memory cleared", got)
	}
}

func TestStoreUsers_SetStatusRefusesAnInconsistentDisabledPair(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	for name, args := range map[string][2]auth.Status{
		"disabled without a status before":   {auth.StatusDisabled, ""},
		"active with a stale status before":  {auth.StatusActive, auth.StatusPendingApproval},
		"disabled remembering disabled":      {auth.StatusDisabled, auth.StatusDisabled},
		"unknown status":                     {"suspended", ""},
		"disabled remembering unknown state": {auth.StatusDisabled, "suspended"},
	} {
		err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
			return e.Store.SetStatus(ctx, u.ID, args[0], args[1], authtest.T0)
		})
		if err == nil {
			t.Errorf("%s: SetStatus succeeded, want an error", name)
		}
	}
	if got, _ := e.Store.GetUser(t.Context(), u.ID); got.Status != auth.StatusActive {
		t.Fatalf("a refused change must leave the status alone, got %s", got.Status)
	}
}

func TestStoreUsers_MarkVerifiedIsIdempotentAndKeepsTheFirstTimestamp(t *testing.T) {
	e := authtest.New(t)
	u := stCreate(t, e, stNewUser(e, "v@example.com", "+919860000000"))
	first, second := authtest.T0.Add(time.Minute), authtest.T0.Add(time.Hour)

	mark := func(kind auth.IdentifierKind, now time.Time) bool {
		var changed bool
		stInTx(t, e, func(ctx context.Context) (err error) {
			changed, err = e.Store.MarkVerified(ctx, u.ID, kind, now)
			return err
		})
		return changed
	}
	if !mark(auth.KindEmail, first) {
		t.Fatal("first email verification must report a change")
	}
	if mark(auth.KindEmail, second) {
		t.Fatal("re-verifying an already verified email must report no change")
	}
	if !mark(auth.KindMobile, second) {
		t.Fatal("mobile verification is independent of email verification")
	}
	got, _ := e.Store.GetUser(t.Context(), u.ID)
	if got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(first) {
		t.Errorf("email_verified_at = %v, want the first stamp %v", got.EmailVerifiedAt, first)
	}
	if got.MobileVerifiedAt == nil || !got.MobileVerifiedAt.Equal(second) {
		t.Errorf("mobile_verified_at = %v, want %v", got.MobileVerifiedAt, second)
	}
	if got.Status != auth.StatusPendingVerification {
		t.Errorf("MarkVerified must not move the status (the Service does), got %s", got.Status)
	}
}

func TestStoreUsers_ReplaceContactChangesValueKeepsStatusAndEnforcesUniqueness(t *testing.T) {
	e := authtest.New(t)
	a := e.UserWithStatus(t, auth.StatusActive)
	b := e.UserWithStatus(t, auth.StatusActive)
	replace := func(id uuid.UUID, kind auth.IdentifierKind, v string) error {
		return e.Tx.InTx(t.Context(), func(ctx context.Context) error {
			return e.Store.ReplaceContact(ctx, id, kind, v, authtest.T0.Add(time.Hour))
		})
	}
	if err := replace(a.ID, auth.KindEmail, "new.a@example.com"); err != nil {
		t.Fatalf("replace email: %v", err)
	}
	if err := replace(a.ID, auth.KindMobile, "+919870000099"); err != nil {
		t.Fatalf("replace mobile: %v", err)
	}
	got, _ := e.Store.GetUser(t.Context(), a.ID)
	if got.Email != "new.a@example.com" || got.Mobile != "+919870000099" || got.Status != auth.StatusActive {
		t.Fatalf("after replace = %+v", got)
	}
	if err := replace(a.ID, auth.KindEmail, b.Email); !errors.Is(err, auth.ErrEmailTaken) {
		t.Errorf("replace with a taken email = %v, want ErrEmailTaken", err)
	}
	if err := replace(a.ID, auth.KindMobile, b.Mobile); !errors.Is(err, auth.ErrMobileTaken) {
		t.Errorf("replace with a taken mobile = %v, want ErrMobileTaken", err)
	}
	if err := replace(uuid.New(), auth.KindEmail, "ghost@example.com"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("replace for an unknown user = %v, want ErrUserNotFound", err)
	}
}

func TestStoreUsers_ChangePasswordAndRehash(t *testing.T) {
	e := authtest.New(t)
	u := e.NewUser(t, authtest.UserSpec{})
	var oldPHC string
	if err := e.Pool.QueryRow(t.Context(), `SELECT password_hash FROM users WHERE id=$1`, u.ID).Scan(&oldPHC); err != nil {
		t.Fatal(err)
	}
	changedAt := authtest.T0.Add(2 * time.Hour)
	stInTx(t, e, func(ctx context.Context) error { return e.Store.ChangePassword(ctx, u.ID, validPHC, changedAt) })
	got, _ := e.Store.GetUser(t.Context(), u.ID)
	if got.PasswordHash != validPHC || !got.PasswordChangedAt.Equal(changedAt) {
		t.Fatalf("after ChangePassword = hash %q changed %v", got.PasswordHash, got.PasswordChangedAt)
	}

	rehashed := validPHC + "x"
	var ok bool
	stInTx(t, e, func(ctx context.Context) (err error) {
		ok, err = e.Store.Rehash(ctx, u.ID, oldPHC, rehashed, changedAt.Add(time.Hour))
		return err
	})
	if ok {
		t.Fatal("Rehash with a stale old hash must change nothing (compare and set)")
	}
	stInTx(t, e, func(ctx context.Context) (err error) {
		ok, err = e.Store.Rehash(ctx, u.ID, validPHC, rehashed, changedAt.Add(time.Hour))
		return err
	})
	got, _ = e.Store.GetUser(t.Context(), u.ID)
	if !ok || got.PasswordHash != rehashed {
		t.Fatalf("Rehash with the current hash = %v, stored %q", ok, got.PasswordHash)
	}
	if !got.PasswordChangedAt.Equal(changedAt) {
		t.Errorf("a rehash is not a password change: password_changed_at moved to %v", got.PasswordChangedAt)
	}
}

func TestStoreUsers_RecordSignIn(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	at := authtest.T0.Add(3 * time.Hour)
	stInTx(t, e, func(ctx context.Context) error { return e.Store.RecordSignIn(ctx, u.ID, at) })
	got, _ := e.Store.GetUser(t.Context(), u.ID)
	if got.LastSignInAt == nil || !got.LastSignInAt.Equal(at) {
		t.Fatalf("last_sign_in_at = %v, want %v", got.LastSignInAt, at)
	}
}

func TestStoreUsers_LockUserNeedsATransactionAndFindsTheRow(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	if _, err := e.Store.LockUser(t.Context(), u.ID); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("LockUser without a transaction = %v, want db.ErrNoTx", err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		got, err := e.Store.LockUser(ctx, u.ID)
		if err != nil || got.ID != u.ID || got.Email != u.Email {
			t.Errorf("LockUser = %+v, %v", got, err)
		}
		if _, err := e.Store.LockUser(ctx, uuid.New()); !errors.Is(err, auth.ErrUserNotFound) {
			t.Errorf("LockUser(unknown) = %v, want ErrUserNotFound", err)
		}
		return nil
	})
}

func TestStoreUsers_LockAdminsReturnsEveryAdminOfTheOrgOrderedByID(t *testing.T) {
	e := authtest.New(t)
	var want []uuid.UUID
	for range 5 {
		want = append(want, e.NewAdmin(t).ID)
	}
	disabled := e.NewUser(t, authtest.UserSpec{Role: "admin", Status: auth.StatusDisabled})
	want = append(want, disabled.ID)  // any status: the last-admin guard needs the whole set
	e.NewUser(t, authtest.UserSpec{}) // a client: never part of the set
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })

	if _, err := e.Store.LockAdmins(t.Context(), e.Org); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("LockAdmins without a transaction = %v, want db.ErrNoTx", err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		rows, err := e.Store.LockAdmins(ctx, e.Org)
		if err != nil {
			t.Fatalf("LockAdmins: %v", err)
		}
		var got []uuid.UUID
		for _, r := range rows {
			if r.Role != "admin" {
				t.Errorf("LockAdmins returned a %s", r.Role)
			}
			got = append(got, r.ID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("LockAdmins ids = %v, want %v (ascending)", got, want)
		}
		return nil
	})
}

func TestStoreUsers_LockAdminsIgnoresAdminsOfOtherOrgs(t *testing.T) {
	e := authtest.New(t)
	mine := e.NewAdmin(t)
	other := uuid.New()
	e.Exec(t, `INSERT INTO orgs (id, name) VALUES ($1, 'Other')`, other)
	e.Exec(t, `INSERT INTO users (id, org_id, role, status, name, email, password_hash, password_changed_at)
		SELECT uuidv7(), $1, 'admin', 'active', 'Other Admin', 'other-admin@example.com', password_hash, password_changed_at FROM users WHERE id = $2`, other, mine.ID)
	stInTx(t, e, func(ctx context.Context) error {
		rows, err := e.Store.LockAdmins(ctx, e.Org)
		if err != nil || len(rows) != 1 || rows[0].ID != mine.ID {
			t.Errorf("LockAdmins(org) = %v, %v; want only %v", rows, err, mine.ID)
		}
		return nil
	})
}
