package auth_test

// S08 - Store integration tests, admin MFA, recovery codes, challenges and throttles (FR-023,
// FR-045, FR-046, FR-060..FR-064, D-08, D-14; data-model.md sections 4 to 7).

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

// ---- admin_mfa ----

func TestStoreMFA_PendingThenConfirm(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	if _, err := e.Store.GetMFA(t.Context(), a.ID); !errors.Is(err, auth.ErrMFANotFound) {
		t.Fatalf("GetMFA before enrolment = %v, want ErrMFANotFound", err)
	}

	pending := []byte("envelope-pending-1")
	t1 := e.Clock.Now()
	stInTx(t, e, func(ctx context.Context) error { return e.Store.SetPendingSecret(ctx, e.Org, a.ID, pending, t1) })
	row, err := e.Store.GetMFA(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.UserID != a.ID || row.OrgID != e.Org || string(row.PendingSecretEnc) != string(pending) || row.SecretEnc != nil ||
		row.PendingCreatedAt == nil || !row.PendingCreatedAt.Equal(t1) || row.EnrolledAt != nil || row.LastUsedStep != 0 {
		t.Fatalf("after SetPendingSecret = %+v", row)
	}

	// A second start overwrites the pending secret (US-5: starting again discards the first).
	pending2 := []byte("envelope-pending-2")
	t2 := t1.Add(time.Minute)
	stInTx(t, e, func(ctx context.Context) error { return e.Store.SetPendingSecret(ctx, e.Org, a.ID, pending2, t2) })
	row, _ = e.Store.GetMFA(t.Context(), a.ID)
	if string(row.PendingSecretEnc) != string(pending2) || !row.PendingCreatedAt.Equal(t2) {
		t.Fatalf("pending was not overwritten: %+v", row)
	}

	t3 := t2.Add(time.Minute)
	stInTx(t, e, func(ctx context.Context) error { return e.Store.ConfirmPending(ctx, a.ID, 12345, t3) })
	row, _ = e.Store.GetMFA(t.Context(), a.ID)
	if string(row.SecretEnc) != string(pending2) || row.PendingSecretEnc != nil || row.PendingCreatedAt != nil ||
		row.EnrolledAt == nil || !row.EnrolledAt.Equal(t3) || row.LastUsedStep != 12345 {
		t.Fatalf("after ConfirmPending = %+v, want the pending secret promoted, step 12345", row)
	}
}

func TestStoreMFA_ConfirmWithoutPendingFails(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error { return e.Store.ConfirmPending(ctx, a.ID, 1, e.Clock.Now()) })
	if !errors.Is(err, auth.ErrNoPendingSecret) {
		t.Fatalf("ConfirmPending with no row = %v, want ErrNoPendingSecret", err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.SetPendingSecret(ctx, e.Org, a.ID, []byte("p"), e.Clock.Now())
	})
	stInTx(t, e, func(ctx context.Context) error { return e.Store.ConfirmPending(ctx, a.ID, 1, e.Clock.Now()) })
	err = e.Tx.InTx(t.Context(), func(ctx context.Context) error { return e.Store.ConfirmPending(ctx, a.ID, 2, e.Clock.Now()) })
	if !errors.Is(err, auth.ErrNoPendingSecret) {
		t.Fatalf("a second ConfirmPending = %v, want ErrNoPendingSecret", err)
	}
}

func TestStoreMFA_ReplacementKeepsTheConfirmedSecretUntilConfirmed(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	stInTx(t, e, func(ctx context.Context) error {
		if err := e.Store.SetPendingSecret(ctx, e.Org, a.ID, []byte("old"), e.Clock.Now()); err != nil {
			return err
		}
		return e.Store.ConfirmPending(ctx, a.ID, 7, e.Clock.Now())
	})
	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.SetPendingSecret(ctx, e.Org, a.ID, []byte("new"), e.Clock.Now())
	})
	row, _ := e.Store.GetMFA(t.Context(), a.ID)
	if string(row.SecretEnc) != "old" || string(row.PendingSecretEnc) != "new" || row.EnrolledAt == nil || row.LastUsedStep != 7 {
		t.Fatalf("during replacement = %+v, want the confirmed secret untouched", row)
	}
	stInTx(t, e, func(ctx context.Context) error { return e.Store.ConfirmPending(ctx, a.ID, 9, e.Clock.Now()) })
	row, _ = e.Store.GetMFA(t.Context(), a.ID)
	if string(row.SecretEnc) != "new" || row.PendingSecretEnc != nil || row.LastUsedStep != 9 {
		t.Fatalf("after confirming the replacement = %+v", row)
	}
}

func TestStoreMFA_SecretsAreStoredOpaque(t *testing.T) {
	// The Store never sees a plaintext secret: it keeps exactly the envelope bytes it was given.
	e := authtest.New(t)
	a := e.NewAdmin(t)
	env, err := e.Keyring.Seal([]byte("JBSWY3DPEHPK3PXP"), []byte("admin_mfa.pending_secret:"+a.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	stInTx(t, e, func(ctx context.Context) error { return e.Store.SetPendingSecret(ctx, e.Org, a.ID, env, e.Clock.Now()) })
	row, _ := e.Store.GetMFA(t.Context(), a.ID)
	if string(row.PendingSecretEnc) != string(env) {
		t.Fatal("the stored envelope differs from the one given")
	}
	var text string
	if err := e.Pool.QueryRow(t.Context(), `SELECT a::text FROM admin_mfa a`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "JBSWY3DPEHPK3PXP") {
		t.Fatal("the plaintext secret is visible in the table")
	}
}

func TestStoreMFA_LockMFA(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	if _, err := e.Store.LockMFA(t.Context(), a.ID); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("LockMFA without a transaction = %v, want db.ErrNoTx", err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		if _, err := e.Store.LockMFA(ctx, a.ID); !errors.Is(err, auth.ErrMFANotFound) {
			t.Errorf("LockMFA with no row = %v, want ErrMFANotFound", err)
		}
		return nil
	})
	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.SetPendingSecret(ctx, e.Org, a.ID, []byte("p"), e.Clock.Now())
	})

	var running bool
	var finish func() error
	stInTx(t, e, func(ctx context.Context) error {
		if _, err := e.Store.LockMFA(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		running, finish = stBlocked(stWait, func() error {
			return e.Tx.InTx(t.Context(), func(ctx2 context.Context) error {
				_, err := e.Store.LockMFA(ctx2, a.ID)
				return err
			})
		})
		return nil
	})
	if !running {
		t.Fatal("a second LockMFA returned while the first transaction held the row")
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreMFA_AdvanceStepIsAReplayGuard(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	stInTx(t, e, func(ctx context.Context) error {
		if err := e.Store.SetPendingSecret(ctx, e.Org, a.ID, []byte("s"), e.Clock.Now()); err != nil {
			return err
		}
		return e.Store.ConfirmPending(ctx, a.ID, 100, e.Clock.Now())
	})
	advance := func(step int64) bool {
		var ok bool
		stInTx(t, e, func(ctx context.Context) (err error) {
			if _, err = e.Store.LockMFA(ctx, a.ID); err != nil {
				return err
			}
			ok, err = e.Store.AdvanceStep(ctx, a.ID, step, e.Clock.Now())
			return err
		})
		return ok
	}
	for _, tc := range []struct {
		step int64
		want bool
	}{{100, false}, {99, false}, {101, true}, {101, false}, {100, false}, {105, true}, {104, false}} {
		if got := advance(tc.step); got != tc.want {
			t.Errorf("AdvanceStep(%d) = %v, want %v", tc.step, got, tc.want)
		}
	}
	row, _ := e.Store.GetMFA(t.Context(), a.ID)
	if row.LastUsedStep != 105 {
		t.Fatalf("last_used_step = %d, want 105", row.LastUsedStep)
	}
}

func TestStoreMFA_ConcurrentAdvanceStepExactlyOneWinner(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	stInTx(t, e, func(ctx context.Context) error {
		if err := e.Store.SetPendingSecret(ctx, e.Org, a.ID, []byte("s"), e.Clock.Now()); err != nil {
			return err
		}
		return e.Store.ConfirmPending(ctx, a.ID, 1, e.Clock.Now())
	})
	won := make([]bool, 8)
	errs := stRace(len(won), func(i int) error {
		return e.Tx.InTx(t.Context(), func(ctx context.Context) (err error) {
			if _, err = e.Store.LockMFA(ctx, a.ID); err != nil {
				return err
			}
			won[i], err = e.Store.AdvanceStep(ctx, a.ID, 50, e.Clock.Now())
			return err
		})
	})
	stNoDeadlock(t, errs)
	wins := 0
	for _, w := range won {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d goroutines accepted the same TOTP step, want exactly 1 (replay)", wins)
	}
}

// ---- recovery codes ----

func stCodes(from, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = stHash(byte(from + i))
	}
	return out
}

func TestStoreMFA_RecoveryCodesUseOnceAndRegenerateRevokesTheOldBatch(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	count := func() int {
		n, err := e.Store.CountUsableRecoveryCodes(t.Context(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	batch1 := uuid.New()
	stInTx(t, e, func(ctx context.Context) error {
		revoked, err := e.Store.ReplaceRecoveryCodes(ctx, e.Org, a.ID, batch1, stCodes(1, 10), e.Clock.Now())
		if revoked != 0 {
			t.Errorf("first issue revoked %d, want 0", revoked)
		}
		return err
	})
	if got := count(); got != 10 {
		t.Fatalf("usable after issue = %d, want 10", got)
	}

	use := func(h byte) (int, bool) {
		var rem int
		var ok bool
		stInTx(t, e, func(ctx context.Context) (err error) {
			rem, ok, err = e.Store.UseRecoveryCode(ctx, a.ID, stHash(h), e.Clock.Now())
			return err
		})
		return rem, ok
	}
	if rem, ok := use(3); !ok || rem != 9 {
		t.Fatalf("first use = %d, %v; want 9 remaining, ok", rem, ok)
	}
	if _, ok := use(3); ok {
		t.Fatal("a used code was accepted again (single use)")
	}
	if _, ok := use(200); ok {
		t.Fatal("an unknown code was accepted")
	}
	if got := count(); got != 9 {
		t.Fatalf("usable after one use = %d, want 9", got)
	}

	// Regeneration revokes the 9 usable codes of the old batch and issues 10 new ones.
	var revoked int
	stInTx(t, e, func(ctx context.Context) (err error) {
		revoked, err = e.Store.ReplaceRecoveryCodes(ctx, e.Org, a.ID, uuid.New(), stCodes(50, 10), e.Clock.Now().Add(time.Hour))
		return err
	})
	if revoked != 9 {
		t.Fatalf("regeneration revoked %d usable codes, want 9", revoked)
	}
	if _, ok := use(4); ok {
		t.Fatal("a code of the revoked batch was accepted")
	}
	if rem, ok := use(50); !ok || rem != 9 {
		t.Fatalf("use of a new code = %d, %v; want 9 remaining, ok", rem, ok)
	}
	if got := count(); got != 9 {
		t.Fatalf("usable after regeneration and one use = %d, want 9", got)
	}
}

func TestStoreMFA_RecoveryCodesBelongToTheirUser(t *testing.T) {
	e := authtest.New(t)
	a, b := e.NewAdmin(t), e.NewAdmin(t)
	stInTx(t, e, func(ctx context.Context) error {
		_, err := e.Store.ReplaceRecoveryCodes(ctx, e.Org, a.ID, uuid.New(), stCodes(1, 3), e.Clock.Now())
		return err
	})
	var ok bool
	stInTx(t, e, func(ctx context.Context) (err error) {
		_, ok, err = e.Store.UseRecoveryCode(ctx, b.ID, stHash(1), e.Clock.Now())
		return err
	})
	if ok {
		t.Fatal("a recovery code of one admin was accepted for another")
	}
	if n, _ := e.Store.CountUsableRecoveryCodes(t.Context(), a.ID); n != 3 {
		t.Fatalf("the owner lost a code: %d usable, want 3", n)
	}
}

func TestStoreMFA_ConcurrentUseOfOneRecoveryCodeExactlyOneWinner(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	stInTx(t, e, func(ctx context.Context) error {
		_, err := e.Store.ReplaceRecoveryCodes(ctx, e.Org, a.ID, uuid.New(), stCodes(1, 10), e.Clock.Now())
		return err
	})
	won := make([]bool, 8)
	errs := stRace(len(won), func(i int) error {
		return e.Tx.InTx(t.Context(), func(ctx context.Context) (err error) {
			_, won[i], err = e.Store.UseRecoveryCode(ctx, a.ID, stHash(5), e.Clock.Now())
			return err
		})
	})
	stNoDeadlock(t, errs)
	wins := 0
	for _, w := range won {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d goroutines used the same recovery code, want exactly 1", wins)
	}
}

// ---- challenges ----

func stChallenge(e *authtest.Env, user uuid.UUID, h byte) auth.NewChallenge {
	return auth.NewChallenge{
		OrgID: e.Org, UserID: user, DeviceID: uuid.New(), TokenHash: stHash(h), Purpose: "verify",
		DeviceName: "Chrome on Mac", Platform: "web", ClientType: "web",
		Now: e.Clock.Now(), ExpiresAt: e.Clock.Now().Add(5 * time.Minute),
	}
}

func TestStoreMFA_ChallengeRoundTrip(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	in := stChallenge(e, a.ID, 1)
	in.Purpose = "enrol"
	var c auth.ChallengeRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		c, err = e.Store.CreateChallenge(ctx, in)
		return err
	})
	got, err := e.Store.FindChallengeByHash(t.Context(), stHash(1))
	if err != nil {
		t.Fatalf("FindChallengeByHash: %v", err)
	}
	if got.ID != c.ID || got.UserID != a.ID || got.OrgID != e.Org || got.DeviceID != in.DeviceID || got.Purpose != "enrol" ||
		got.DeviceName != "Chrome on Mac" || got.Platform != "web" || got.ClientType != "web" || got.ConsumedAt != nil ||
		!got.CreatedAt.Equal(e.Clock.Now()) || !got.ExpiresAt.Equal(e.Clock.Now().Add(5*time.Minute)) {
		t.Fatalf("challenge = %+v", got)
	}
	if _, err := e.Store.FindChallengeByHash(t.Context(), stHash(2)); !errors.Is(err, auth.ErrChallengeNotFound) {
		t.Fatalf("FindChallengeByHash(unknown) = %v, want ErrChallengeNotFound", err)
	}
	err = e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		_, err := e.Store.CreateChallenge(ctx, stChallenge(e, a.ID, 1))
		return err
	})
	if err == nil {
		t.Fatal("a duplicate challenge digest must be refused")
	}
}

func TestStoreMFA_ConsumeChallengeIsSingleUseAndExpiresAtTheInstant(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	mk := func(h byte) auth.ChallengeRow {
		var c auth.ChallengeRow
		stInTx(t, e, func(ctx context.Context) (err error) {
			c, err = e.Store.CreateChallenge(ctx, stChallenge(e, a.ID, h))
			return err
		})
		return c
	}
	consume := func(id uuid.UUID, now time.Time) bool {
		var ok bool
		stInTx(t, e, func(ctx context.Context) (err error) {
			ok, err = e.Store.ConsumeChallenge(ctx, id, now)
			return err
		})
		return ok
	}
	expiry := e.Clock.Now().Add(5 * time.Minute)

	c := mk(1)
	if !consume(c.ID, expiry.Add(-time.Nanosecond)) {
		t.Fatal("a challenge one nanosecond before expiry must be consumable")
	}
	if consume(c.ID, expiry.Add(-time.Second)) {
		t.Fatal("a consumed challenge was consumed twice")
	}
	got, _ := e.Store.FindChallengeByHash(t.Context(), stHash(1))
	if got.ConsumedAt == nil || got.ConsumedAt.After(expiry) {
		t.Fatalf("consumed_at = %v", got.ConsumedAt)
	}

	if consume(mk(2).ID, expiry) {
		t.Fatal("a challenge is expired at exactly 5 minutes (valid iff now < expires_at)")
	}
	if consume(mk(3).ID, expiry.Add(time.Hour)) {
		t.Fatal("an expired challenge was consumed")
	}
	if consume(uuid.New(), e.Clock.Now()) {
		t.Fatal("an unknown challenge was consumed")
	}
}

func TestStoreMFA_ConcurrentConsumeChallengeExactlyOneWinner(t *testing.T) {
	e := authtest.New(t)
	a := e.NewAdmin(t)
	var c auth.ChallengeRow
	stInTx(t, e, func(ctx context.Context) (err error) {
		c, err = e.Store.CreateChallenge(ctx, stChallenge(e, a.ID, 1))
		return err
	})
	won := make([]bool, 8)
	errs := stRace(len(won), func(i int) error {
		return e.Tx.InTx(t.Context(), func(ctx context.Context) (err error) {
			won[i], err = e.Store.ConsumeChallenge(ctx, c.ID, e.Clock.Now())
			return err
		})
	})
	stNoDeadlock(t, errs)
	wins := 0
	for _, w := range won {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d goroutines consumed the same challenge, want exactly 1", wins)
	}
}

// ---- throttles ----

var stPolicy = auth.ThrottlePolicy{MaxFailures: 5, Window: 15 * time.Minute, Lock: 15 * time.Minute}

func stSubject(e *authtest.Env, id string) []byte {
	return e.Blind.Index("throttle", []byte("signin|email|"+id))
}

func TestStoreMFA_ThrottleStateRoundTripAndDelete(t *testing.T) {
	e := authtest.New(t)
	subj := stSubject(e, "someone@example.com")

	if st, err := e.Store.GetThrottle(t.Context(), e.Org, "signin", subj); err != nil || len(st.Failures) != 0 || st.LockedUntil != nil {
		t.Fatalf("GetThrottle on an unknown subject = %+v, %v; want the zero state", st, err)
	}

	f1, f2 := e.Clock.Now(), e.Clock.Now().Add(time.Minute)
	until := f2.Add(15 * time.Minute)
	stInTx(t, e, func(ctx context.Context) error {
		if _, err := e.Store.LockThrottle(ctx, e.Org, "signin", subj, f1); err != nil {
			return err
		}
		return e.Store.SaveThrottle(ctx, e.Org, "signin", subj, auth.ThrottleState{Failures: []time.Time{f1, f2}}, f2)
	})
	st, err := e.Store.GetThrottle(t.Context(), e.Org, "signin", subj)
	if err != nil || len(st.Failures) != 2 || !st.Failures[0].Equal(f1) || !st.Failures[1].Equal(f2) || st.LockedUntil != nil {
		t.Fatalf("after save = %+v, %v", st, err)
	}
	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.SaveThrottle(ctx, e.Org, "signin", subj, auth.ThrottleState{LockedUntil: &until}, f2)
	})
	st, _ = e.Store.GetThrottle(t.Context(), e.Org, "signin", subj)
	if len(st.Failures) != 0 || st.LockedUntil == nil || !st.LockedUntil.Equal(until) {
		t.Fatalf("after lock = %+v", st)
	}

	stInTx(t, e, func(ctx context.Context) error { return e.Store.DeleteThrottle(ctx, e.Org, "signin", subj) })
	stInTx(t, e, func(ctx context.Context) error { return e.Store.DeleteThrottle(ctx, e.Org, "signin", subj) }) // absent: no error
	if st, _ := e.Store.GetThrottle(t.Context(), e.Org, "signin", subj); len(st.Failures) != 0 || st.LockedUntil != nil {
		t.Fatalf("after delete = %+v", st)
	}
}

func TestStoreMFA_ThrottleWritesAndLocksNeedATransaction(t *testing.T) {
	e := authtest.New(t)
	subj := stSubject(e, "x@example.com")
	if _, err := e.Store.LockThrottle(t.Context(), e.Org, "signin", subj, e.Clock.Now()); !errors.Is(err, db.ErrNoTx) {
		t.Errorf("LockThrottle without a transaction = %v, want db.ErrNoTx", err)
	}
	if err := e.Store.SaveThrottle(t.Context(), e.Org, "signin", subj, auth.ThrottleState{}, e.Clock.Now()); !errors.Is(err, db.ErrNoTx) {
		t.Errorf("SaveThrottle without a transaction = %v, want db.ErrNoTx", err)
	}
	if err := e.Store.DeleteThrottle(t.Context(), e.Org, "signin", subj); !errors.Is(err, db.ErrNoTx) {
		t.Errorf("DeleteThrottle without a transaction = %v, want db.ErrNoTx", err)
	}
}

func TestStoreMFA_ThrottleSubjectIsStoredAsGivenAndNeverAsTheIdentifier(t *testing.T) {
	e := authtest.New(t)
	ident := "someone@example.com"
	subj := stSubject(e, ident)
	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.SaveThrottle(ctx, e.Org, "signin", subj, auth.ThrottleState{Failures: []time.Time{e.Clock.Now()}}, e.Clock.Now())
	})
	var stored []byte
	var text string
	if err := e.Pool.QueryRow(t.Context(), `SELECT subject, t::text FROM auth_throttles t`).Scan(&stored, &text); err != nil {
		t.Fatalf("read auth_throttles: %v", err)
	}
	if string(stored) != string(subj) || len(stored) != 32 {
		t.Fatalf("stored subject = %x, want the 32-byte digest %x", stored, subj)
	}
	if strings.Contains(text, ident) || strings.Contains(text, "someone") {
		t.Fatalf("the identifier as typed is visible in auth_throttles: %s", text)
	}
}

func TestStoreMFA_ThrottlesAreIsolatedByScopeAndSubject(t *testing.T) {
	e := authtest.New(t)
	a, b := stSubject(e, "a@example.com"), stSubject(e, "b@example.com")
	fail := []time.Time{e.Clock.Now()}
	stInTx(t, e, func(ctx context.Context) error {
		return e.Store.SaveThrottle(ctx, e.Org, "signin", a, auth.ThrottleState{Failures: fail}, e.Clock.Now())
	})
	if st, _ := e.Store.GetThrottle(t.Context(), e.Org, "signin", b); len(st.Failures) != 0 {
		t.Fatalf("another subject shares the counter: %+v", st)
	}
	// An unknown scope is refused by the CHECK list (004 extends it in its own migration).
	err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		return e.Store.SaveThrottle(ctx, e.Org, "no_such_scope", a, auth.ThrottleState{Failures: fail}, e.Clock.Now())
	})
	if err == nil {
		t.Fatal("an unknown throttle scope must be refused")
	}
	// More than five recorded failures cannot be stored (cardinality CHECK).
	six := make([]time.Time, 6)
	for i := range six {
		six[i] = e.Clock.Now().Add(time.Duration(i) * time.Second)
	}
	err = e.Tx.InTx(t.Context(), func(ctx context.Context) error {
		return e.Store.SaveThrottle(ctx, e.Org, "signin", b, auth.ThrottleState{Failures: six}, e.Clock.Now())
	})
	if err == nil {
		t.Fatal("six stored failures must be refused")
	}
}

// TestStoreMFA_ConcurrentFailuresAreNotLost drives the Service's fail path with the Store's
// primitives: lock the row, apply the pure lockout, save. Under the row lock no failure is lost
// and exactly one of N concurrent failures starts the lock (US-4, FR-045).
func TestStoreMFA_ConcurrentFailuresAreNotLost(t *testing.T) {
	fail := func(e *authtest.Env, subj []byte) (started bool, err error) {
		err = e.Tx.InTx(t.Context(), func(ctx context.Context) error {
			st, err := e.Store.LockThrottle(ctx, e.Org, "signin", subj, e.Clock.Now())
			if err != nil {
				return err
			}
			next, s := auth.ApplyThrottle(st, e.Clock.Now(), stPolicy)
			started = s
			return e.Store.SaveThrottle(ctx, e.Org, "signin", subj, next, e.Clock.Now())
		})
		return started, err
	}

	t.Run("four failures all counted", func(t *testing.T) {
		e := authtest.New(t)
		subj := stSubject(e, "race@example.com")
		errs := stRace(4, func(int) error { _, err := fail(e, subj); return err })
		stNoDeadlock(t, errs)
		st, _ := e.Store.GetThrottle(t.Context(), e.Org, "signin", subj)
		if len(st.Failures) != 4 || st.LockedUntil != nil {
			t.Fatalf("after 4 concurrent failures: %d recorded, locked=%v; want 4 and no lock (a lost update)", len(st.Failures), st.LockedUntil)
		}
	})
	t.Run("eight failures start exactly one lock", func(t *testing.T) {
		e := authtest.New(t)
		subj := stSubject(e, "race2@example.com")
		started := make([]bool, 8)
		errs := stRace(len(started), func(i int) (err error) { started[i], err = fail(e, subj); return err })
		stNoDeadlock(t, errs)
		n := 0
		for _, s := range started {
			if s {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%d failures started a lock, want exactly 1", n)
		}
		st, _ := e.Store.GetThrottle(t.Context(), e.Org, "signin", subj)
		if st.LockedUntil == nil || !st.LockedUntil.Equal(e.Clock.Now().Add(stPolicy.Lock)) {
			t.Fatalf("locked_until = %v, want now+15m", st.LockedUntil)
		}
	})
}
