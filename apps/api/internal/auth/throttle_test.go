package auth_test

// S11 - Service tests A, the Throttle capability for 004's OTP limits (plan P-07, data-model
// section 7, contracts/go-capabilities.md section 3). Real Store, fake clock.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
)

// svFailN records n counted failures one minute apart in separate transactions and returns the
// last result.
func svFailN(t *testing.T, e *authtest.Env, th auth.Throttle, org uuid.UUID, key string, n int, p auth.ThrottlePolicy) (started bool, until *time.Time) {
	t.Helper()
	for i := range n {
		if i > 0 {
			e.Clock.Advance(time.Minute)
		}
		stInTx(t, e, func(ctx context.Context) (err error) {
			started, until, err = th.Fail(ctx, org, "signin", key, p)
			return err
		})
	}
	return started, until
}

func TestThrottle_CheckOnUnknownKeyIsNotLocked(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	until, err := th.Check(t.Context(), e.Org, "signin", "email|nobody@example.com")
	if err != nil || until != nil {
		t.Fatalf("Check = %v, %v; want nil, nil", until, err)
	}
}

func TestThrottle_FifthFailureStartsLockUntilPlus19m(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	key := "user|" + uuid.NewString()
	t0 := e.Clock.Now()

	started, until := svFailN(t, e, th, e.Org, key, 4, svPolicy)
	if started || until != nil {
		t.Fatalf("after 4 failures started=%v until=%v, want neither", started, until)
	}
	if got, err := th.Check(t.Context(), e.Org, "signin", key); err != nil || got != nil {
		t.Fatalf("Check after 4 failures = %v, %v; want nil", got, err)
	}
	e.Clock.Advance(time.Minute)
	stInTx(t, e, func(ctx context.Context) (err error) {
		started, until, err = th.Fail(ctx, e.Org, "signin", key, svPolicy)
		return err
	})
	want := t0.Add(4*time.Minute + 15*time.Minute)
	if !started || until == nil || !until.Equal(want) {
		t.Fatalf("5th failure = started %v until %v, want started until %v", started, until, want)
	}
	got, err := th.Check(t.Context(), e.Org, "signin", key)
	if err != nil || got == nil || !got.Equal(want) {
		t.Fatalf("Check while locked = %v, %v; want %v", got, err, want)
	}

	e.Clock.Set(want.Add(-time.Second))
	if got, _ := th.Check(t.Context(), e.Org, "signin", key); got == nil {
		t.Errorf("1s before the end the subject must still be locked")
	}
	e.Clock.Set(want)
	if got, _ := th.Check(t.Context(), e.Org, "signin", key); got != nil {
		t.Errorf("at the lock end the subject must be unlocked, got %v", got)
	}
}

func TestThrottle_FailureDuringLockDoesNotExtend(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	key := "mobile|+919812345678"
	_, until := svFailN(t, e, th, e.Org, key, 5, svPolicy)
	if until == nil {
		t.Fatal("precondition: locked")
	}
	locked := *until
	e.Clock.Advance(5 * time.Minute)
	var started bool
	var again *time.Time
	stInTx(t, e, func(ctx context.Context) (err error) {
		started, again, err = th.Fail(ctx, e.Org, "signin", key, svPolicy)
		return err
	})
	if started {
		t.Errorf("a failure during a lock must not start a new lock")
	}
	if again == nil || !again.Equal(locked) {
		t.Errorf("lockedUntil = %v, want unchanged %v", again, locked)
	}
}

func TestThrottle_HonoursTheCallersPolicy(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	p := auth.ThrottlePolicy{MaxFailures: 3, Window: 10 * time.Minute, Lock: time.Hour}
	started, until := svFailN(t, e, th, e.Org, "otp|+919812345678", 3, p)
	if !started || until == nil || !until.Equal(authtest.T0.Add(2*time.Minute+time.Hour)) {
		t.Fatalf("3 failures under a 3/10m/1h policy: started=%v until=%v", started, until)
	}
}

func TestThrottle_KeysAreIsolated(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	svFailN(t, e, th, e.Org, "user|a", 5, svPolicy)
	if got, _ := th.Check(t.Context(), e.Org, "signin", "user|a"); got == nil {
		t.Fatal("user|a must be locked")
	}
	if got, _ := th.Check(t.Context(), e.Org, "signin", "user|b"); got != nil {
		t.Errorf("another key must not be locked")
	}
	other := uuid.New()
	e.Exec(t, `INSERT INTO orgs (id, name) VALUES ($1, 'Other')`, other)
	if got, _ := th.Check(t.Context(), other, "signin", "user|a"); got != nil {
		t.Errorf("another org must not share the lock")
	}
}

func TestThrottle_KeyIsStoredOnlyAsBlindIndex(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	key := "email|someone.private@example.com"
	svFailN(t, e, th, e.Org, key, 2, svPolicy)

	st, err := e.Store.GetThrottle(t.Context(), e.Org, "signin", svSubject(e, "signin", key))
	if err != nil || len(st.Failures) != 2 {
		t.Fatalf("state under the blind-index subject = %+v, %v; want 2 failures", st, err)
	}
	var dump string
	if err := e.Pool.QueryRow(t.Context(), `SELECT COALESCE(string_agg(t::text, ' '), '') FROM auth_throttles t`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, "someone.private") || strings.Contains(dump, "private@example.com") {
		t.Errorf("auth_throttles holds the key in readable form: %s", dump)
	}
}

func TestThrottle_ClearRemovesState(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	svFailN(t, e, th, e.Org, "user|c", 5, svPolicy)
	stInTx(t, e, func(ctx context.Context) error { return th.Clear(ctx, e.Org, "signin", "user|c") })
	if got, _ := th.Check(t.Context(), e.Org, "signin", "user|c"); got != nil {
		t.Errorf("Check after Clear = %v, want nil", got)
	}
	if n := svCount(t, e, `SELECT count(*) FROM auth_throttles`); n != 0 {
		t.Errorf("auth_throttles rows = %d, want 0", n)
	}
	stInTx(t, e, func(ctx context.Context) error { return th.Clear(ctx, e.Org, "signin", "user|never-failed") })
}

func TestThrottle_FailAndClearRequireATransaction(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	if _, _, err := th.Fail(t.Context(), e.Org, "signin", "user|d", svPolicy); !errors.Is(err, db.ErrNoTx) {
		t.Errorf("Fail without a transaction: err = %v, want db.ErrNoTx", err)
	}
	if err := th.Clear(t.Context(), e.Org, "signin", "user|d"); !errors.Is(err, db.ErrNoTx) {
		t.Errorf("Clear without a transaction: err = %v, want db.ErrNoTx", err)
	}
	if n := svCount(t, e, `SELECT count(*) FROM auth_throttles`); n != 0 {
		t.Errorf("a refused Fail wrote %d rows", n)
	}
}

func TestThrottle_FailCommitsAndRollsBackWithTheCaller(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	boom := errors.New("caller failed")
	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		if _, _, err := th.Fail(ctx, e.Org, "signin", "user|e", svPolicy); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n := svCount(t, e, `SELECT count(*) FROM auth_throttles`); n != 0 {
		t.Errorf("rolled-back Fail left %d rows", n)
	}
	svFailN(t, e, th, e.Org, "user|e", 1, svPolicy)
	if n := svCount(t, e, `SELECT count(*) FROM auth_throttles`); n != 1 {
		t.Errorf("committed Fail rows = %d, want 1", n)
	}
}

func TestThrottle_UnknownScopeIsRefused(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	svFailN(t, e, th, e.Org, "user|f", 1, svPolicy) // the known scope works, so the refusal below is about the scope
	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, _, err := th.Fail(ctx, e.Org, "not_a_scope", "user|f", svPolicy)
		return err
	})
	if err == nil {
		t.Errorf("a scope outside the auth_throttles CHECK list must be refused")
	}
}

func TestThrottle_ConcurrentFailuresAreCountedOnce(t *testing.T) {
	e := authtest.New(t)
	var th auth.Throttle = e.Service(t)
	key := "user|race"
	results := make([]bool, 12)
	errs := stRace(12, func(i int) error {
		return e.Tx.InTx(t.Context(), func(ctx context.Context) error {
			started, _, err := th.Fail(ctx, e.Org, "signin", key, svPolicy)
			results[i] = started
			return err
		})
	})
	for _, err := range errs {
		if err != nil {
			t.Fatalf("Fail: %v", err)
		}
	}
	started := 0
	for _, s := range results {
		if s {
			started++
		}
	}
	if started != 1 {
		t.Errorf("locks started = %d, want exactly 1 (row lock serialises the counter)", started)
	}
	if got, _ := th.Check(t.Context(), e.Org, "signin", key); got == nil {
		t.Errorf("subject must be locked")
	}
}
