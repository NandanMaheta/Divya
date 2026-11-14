package auth_test

// S11 - Service tests A, lockout applied through sign-in (D-08, FR-045, FR-046, plan P-07;
// US-4 s1-6, s8-9, SC-004). The pure state machine is covered by TestLockout_* in
// throttle_apply_test.go; these tests drive the whole service on a real database and the fake clock.

import (
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
)

// svAuditTimes returns created_at of every row with the action, in write order.
func svAuditTimes(t *testing.T, e *authtest.Env, action string) []time.Time {
	t.Helper()
	rows, err := e.Pool.Query(t.Context(), `SELECT created_at FROM audit_log WHERE action = $1 ORDER BY created_at, id`, action)
	if err != nil {
		t.Fatalf("audit times: %v", err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			t.Fatal(err)
		}
		out = append(out, ts.UTC())
	}
	return out
}

// svLockWithFiveFailures makes wrong attempts at T, T+1m .. T+4m and leaves the clock at T+4m.
func svLockWithFiveFailures(t *testing.T, e *authtest.Env, s *auth.Service, u authtest.User) {
	t.Helper()
	for i := range 5 {
		if i > 0 {
			e.Clock.Advance(time.Minute)
		}
		svWrong(t, s, u)
	}
}

func TestLockoutService_LocksAt5thFailureUntilPlus19mThenUnlocks(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	t0 := e.Clock.Now()

	svLockWithFiveFailures(t, e, s, u)
	wantUntil := t0.Add(19 * time.Minute)
	st := svThrottle(t, e, svUserKey(u))
	if st.LockedUntil == nil || !st.LockedUntil.Equal(wantUntil) {
		t.Fatalf("LockedUntil = %v, want T+19m = %v", st.LockedUntil, wantUntil)
	}
	locks := e.AuditOf(t, auth.ActionLockoutStarted)
	if len(locks) != 1 {
		t.Fatalf("lockout.started rows = %d, want 1", len(locks))
	}
	if locks[0].ActorKind != audit.KindSystem || locks[0].EntityID != u.ID {
		t.Errorf("lockout row = %+v", locks[0])
	}
	if got := svJSON(t, locks[0].After)["locked_until"].(string); !mustTime(t, got).Equal(wantUntil) {
		t.Errorf("audit locked_until = %s, want %v", got, wantUntil)
	}
	if ts := svAuditTimes(t, e, auth.ActionLockoutStarted); len(ts) != 1 || !ts[0].Equal(t0.Add(4*time.Minute)) {
		t.Errorf("lockout.started written at %v, want T+4m", ts)
	}
	if n := len(e.AuditOf(t, auth.ActionSignInFailed)); n != 5 {
		t.Errorf("sign_in.failed rows = %d, want 5", n)
	}

	// T+18m59s: the right password gets the same generic failure, no session, nothing counted.
	e.Clock.Set(t0.Add(18*time.Minute + 59*time.Second))
	_, err := s.SignIn(t.Context(), svInput(u, uuid.New()))
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("right password while locked: err = %v, want the generic failure", err)
	}
	if n := svAllSessions(t, e, u.ID); n != 0 {
		t.Errorf("sessions = %d, want 0 while locked", n)
	}
	rows := e.AuditOf(t, auth.ActionSignInFailed)
	if last := rows[len(rows)-1]; svJSON(t, last.After)["reason"] != auth.ReasonLocked || last.EntityID != u.ID {
		t.Errorf("last failure row = %+v, want reason locked for the user", last)
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil == nil || !st.LockedUntil.Equal(wantUntil) || len(st.Failures) != 0 {
		t.Errorf("a locked attempt changed the state: %+v", st)
	}
	if n := len(e.AuditOf(t, auth.ActionLockoutStarted)); n != 1 {
		t.Errorf("lockout.started rows = %d after a locked attempt, want still 1", n)
	}

	// T+19m: unlocked, success clears the counter.
	e.Clock.Set(wantUntil)
	svSignIn(t, s, u, uuid.New())
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil != nil || len(st.Failures) != 0 {
		t.Errorf("state after the first success = %+v, want cleared", st)
	}
}

func TestLockoutService_FailuresDuringTheLockDoNotExtendIt(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	t0 := e.Clock.Now()
	svLockWithFiveFailures(t, e, s, u)
	for _, at := range []time.Duration{10 * time.Minute, 15 * time.Minute} {
		e.Clock.Set(t0.Add(at))
		svWrong(t, s, u)
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil == nil || !st.LockedUntil.Equal(t0.Add(19*time.Minute)) {
		t.Fatalf("LockedUntil = %v, want unchanged T+19m", st.LockedUntil)
	}
	if n := len(e.AuditOf(t, auth.ActionLockoutStarted)); n != 1 {
		t.Errorf("lockout.started rows = %d, want 1", n)
	}
	e.Clock.Set(t0.Add(19 * time.Minute))
	svSignIn(t, s, u, uuid.New())
}

func TestLockoutService_SlidingWindowFourPlusOneLaterIsNotLocked(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	t0 := e.Clock.Now()
	for _, at := range []time.Duration{0, time.Minute, 2 * time.Minute, 3 * time.Minute, 17 * time.Minute} {
		e.Clock.Set(t0.Add(at))
		svWrong(t, s, u)
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil != nil {
		t.Fatalf("locked at %v with failures %v; only the failures inside the last 15 minutes count", st.LockedUntil, st.Failures)
	}
	if n := len(e.AuditOf(t, auth.ActionLockoutStarted)); n != 0 {
		t.Errorf("lockout.started rows = %d, want 0", n)
	}
	e.Clock.Set(t0.Add(17*time.Minute + 30*time.Second))
	svSignIn(t, s, u, uuid.New())
}

func TestLockoutService_UnknownIdentifierSixAttemptsLookLikeALockedAccount(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	acct := e.UserWithStatus(t, auth.StatusActive)
	svLockWithFiveFailures(t, e, s, acct)
	realIn := svInput(acct, uuid.New())
	_, lockedErr := s.SignIn(t.Context(), realIn)
	if !errors.Is(lockedErr, auth.ErrInvalidCredentials) {
		t.Fatalf("locked account: err = %v", lockedErr)
	}

	for i := range 6 {
		in := svInput(acct, uuid.New())
		in.Identifier, in.Password = "ghost@example.com", wrongPassword
		_, err := s.SignIn(t.Context(), in)
		if !errors.Is(err, auth.ErrInvalidCredentials) || err.Error() != lockedErr.Error() {
			t.Fatalf("attempt %d: err = %v, want identical to the locked account's %v", i+1, err, lockedErr)
		}
	}
	st := svThrottle(t, e, "email|ghost@example.com")
	if st.LockedUntil == nil {
		t.Fatalf("an unknown identifier must lock like a real one (state %+v)", st)
	}
	var ghostLocks []authtest.AuditRow
	for _, r := range e.AuditOf(t, auth.ActionLockoutStarted) {
		if r.EntityID == uuid.Nil {
			ghostLocks = append(ghostLocks, r)
		}
	}
	if len(ghostLocks) != 1 {
		t.Fatalf("lockout.started rows for the unknown identifier = %d, want 1", len(ghostLocks))
	}
	wantRef := hex.EncodeToString(svSubject(e, "signin", "email|ghost@example.com")[:8])
	if svJSON(t, ghostLocks[0].After)["subject_ref"] != wantRef || ghostLocks[0].ActorKind != audit.KindSystem {
		t.Errorf("lock row = %+v, want subject_ref %s", ghostLocks[0], wantRef)
	}
}

func TestLockoutService_EmailAndMobileShareTheAccountCounter(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	for i, id := range []string{u.Email, u.Mobile, u.Email, u.Mobile, u.Email} {
		in := svInput(u, uuid.New())
		in.Identifier, in.Password = id, wrongPassword
		if _, err := s.SignIn(t.Context(), in); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	for _, id := range []string{u.Email, u.Mobile} {
		in := svInput(u, uuid.New())
		in.Identifier = id
		if _, err := s.SignIn(t.Context(), in); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("right password by %q while locked: err = %v, want the generic failure", id, err)
		}
	}
	if n := svAllSessions(t, e, u.ID); n != 0 {
		t.Errorf("sessions = %d, want 0", n)
	}
}

func TestLockoutService_OtherAccountsAreUnaffected(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	a := e.UserWithStatus(t, auth.StatusActive)
	b := e.UserWithStatus(t, auth.StatusActive)
	svLockWithFiveFailures(t, e, s, a)
	svSignIn(t, s, b, uuid.New())
}

func TestLockoutService_ResetPasswordLiftsTheLockAtOnce(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	svLockWithFiveFailures(t, e, s, u)
	if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("precondition: locked, err = %v", err)
	}
	const fresh = "fresh-passphrase-after-reset-8"
	if err := s.ResetPassword(t.Context(), u.ID, fresh, authtest.System()); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	in := svInput(u, uuid.New())
	in.Password = fresh
	if res, err := s.SignIn(t.Context(), in); err != nil || res.Session == nil {
		t.Fatalf("sign-in right after the reset: %+v, %v", res, err)
	}
}

func TestLockoutService_AdminWrongPasswordsLockLikeClients(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	admin := e.NewAdmin(t)
	svEnrol(t, e, admin.ID)
	svLockWithFiveFailures(t, e, s, admin)
	if _, err := s.SignIn(t.Context(), svInput(admin, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("admin locked, right password: err = %v, want the generic failure", err)
	}
	if n := svCount(t, e, `SELECT count(*) FROM mfa_challenges`); n != 0 {
		t.Errorf("a locked admin must not get a challenge, got %d", n)
	}
}

func TestLockoutService_SharedAcrossServiceInstances(t *testing.T) {
	e := authtest.New(t)
	a, b := e.Service(t), e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	for i := range 5 {
		if i%2 == 0 {
			svWrong(t, a, u)
		} else {
			svWrong(t, b, u)
		}
	}
	for name, s := range map[string]*auth.Service{"first instance": a, "second instance": b} {
		if _, err := s.SignIn(t.Context(), svInput(u, uuid.New())); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("%s: err = %v, want locked (state lives in PostgreSQL, not memory)", name, err)
		}
	}
}

func TestLockoutService_ConcurrentWrongAttemptsStartExactlyOneLock(t *testing.T) {
	e := authtest.New(t)
	s := e.Service(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	t0 := e.Clock.Now()
	errs := stRace(10, func(int) error {
		in := svInput(u, uuid.New())
		in.Password = wrongPassword
		_, err := s.SignIn(t.Context(), in)
		return err
	})
	for i, err := range errs {
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if n := len(e.AuditOf(t, auth.ActionLockoutStarted)); n != 1 {
		t.Errorf("lockout.started rows = %d, want exactly 1", n)
	}
	if n := len(e.AuditOf(t, auth.ActionSignInFailed)); n != 10 {
		t.Errorf("sign_in.failed rows = %d, want 10", n)
	}
	if st := svThrottle(t, e, svUserKey(u)); st.LockedUntil == nil || !st.LockedUntil.Equal(t0.Add(15*time.Minute)) || len(st.Failures) != 0 {
		t.Errorf("state = %+v, want locked until T+15m with the counter cleared", st)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("time %q: %v", s, err)
	}
	return ts.UTC()
}
