package auth_test

// S08 - Store lock-order tests (data-model.md "Lock order", plan P-03 step 2 and the concurrency
// note): users -> device_sessions -> refresh_tokens, all admins ordered by id. A lock method must
// really lock (a second transaction waits), and transactions that follow the documented order must
// never deadlock (SQLSTATE 40P01).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NandanMaheta/swastik/apps/api/internal/auth"
	"github.com/NandanMaheta/swastik/apps/api/internal/auth/authtest"
)

const stWait = 400 * time.Millisecond

func TestStoreLock_LockUserBlocksAnotherLockUntilCommit(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)

	var running bool
	var finish func() error
	stInTx(t, e, func(ctx context.Context) error {
		if _, err := e.Store.LockUser(ctx, u.ID); err != nil {
			t.Fatalf("LockUser: %v", err)
		}
		running, finish = stBlocked(stWait, func() error {
			return e.Tx.InTx(t.Context(), func(ctx2 context.Context) error {
				_, err := e.Store.LockUser(ctx2, u.ID)
				return err
			})
		})
		return nil
	})
	if !running {
		t.Fatal("a second LockUser returned while the first transaction still held the row: the read is not FOR UPDATE")
	}
	if err := finish(); err != nil {
		t.Fatalf("the waiting LockUser must succeed after the commit: %v", err)
	}
}

func TestStoreLock_PlainReadsDoNotWaitForRowLocks(t *testing.T) {
	e := authtest.New(t)
	u := e.UserWithStatus(t, auth.StatusActive)
	s := e.NewSession(t, u.ID, authtest.SessionSpec{})
	stInTx(t, e, func(ctx context.Context) error {
		if _, err := e.Store.LockUser(ctx, u.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Store.LockSession(ctx, s); err != nil {
			t.Fatal(err)
		}
		running, finish := stBlocked(stWait, func() error {
			if _, err := e.Store.GetUser(t.Context(), u.ID); err != nil {
				return err
			}
			if _, err := e.Store.GetSessionPrincipal(t.Context(), s); err != nil {
				return err
			}
			_, err := e.Store.FindUserByIdentifier(t.Context(), auth.KindEmail, u.Email)
			return err
		})
		if running {
			t.Error("a plain read waited for a row lock (MVCC reads must not block)")
		}
		if err := finish(); err != nil {
			t.Errorf("plain reads: %v", err)
		}
		return nil
	})
}

func TestStoreLock_LockAdminsHoldsEveryAdminRow(t *testing.T) {
	e := authtest.New(t)
	a1, a2 := e.NewAdmin(t), e.NewAdmin(t)

	var running bool
	var finish func() error
	stInTx(t, e, func(ctx context.Context) error {
		if _, err := e.Store.LockAdmins(ctx, e.Org); err != nil {
			t.Fatalf("LockAdmins: %v", err)
		}
		running, finish = stBlocked(stWait, func() error {
			// The last-admin race: another transaction touching either admin must wait.
			return e.Tx.InTx(t.Context(), func(ctx2 context.Context) error {
				_, err := e.Store.LockUser(ctx2, a2.ID)
				return err
			})
		})
		return nil
	})
	_ = a1
	if !running {
		t.Fatal("LockUser(admin) returned while LockAdmins held the admin rows")
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLock_ConcurrentLockAdminsNeverDeadlock(t *testing.T) {
	e := authtest.New(t)
	for range 6 {
		e.NewAdmin(t)
	}
	errs := stRace(8, func(int) error {
		for range 5 {
			err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
				_, err := e.Store.LockAdmins(ctx, e.Org)
				return err
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	stNoDeadlock(t, errs)
}

// TestStoreLock_DocumentedOrderNeverDeadlocks mixes the three transaction shapes of the plan:
// a refresh (user, session, token), a disable-user (admins, user, session) and a sign-out
// (user, session). Every one takes locks in the documented order.
func TestStoreLock_DocumentedOrderNeverDeadlocks(t *testing.T) {
	e := authtest.New(t)
	admin := e.NewAdmin(t)
	other := e.NewAdmin(t)
	user := e.UserWithStatus(t, auth.StatusActive)

	type fam struct{ user, session uuid.UUID }
	var fams []fam
	for _, u := range []uuid.UUID{admin.ID, other.ID, user.ID} {
		s := e.NewSession(t, u, authtest.SessionSpec{})
		stInTx(t, e, func(ctx context.Context) error {
			_, err := e.Store.InsertRefresh(ctx, e.Org, s, stHash(byte(len(fams)+1)), e.Clock.Now())
			return err
		})
		fams = append(fams, fam{u, s})
	}

	errs := stRace(9, func(i int) error {
		f := fams[i%len(fams)]
		for range 4 {
			err := e.Tx.InTx(t.Context(), func(ctx context.Context) error {
				switch i % 3 {
				case 0: // refresh
					if _, err := e.Store.LockUser(ctx, f.user); err != nil {
						return err
					}
					if _, err := e.Store.LockSession(ctx, f.session); err != nil {
						return err
					}
					var tok auth.RefreshRow
					if err := e.Pool.QueryRow(ctx, `SELECT id, org_id, session_id FROM refresh_tokens WHERE session_id=$1 AND used_at IS NULL`, f.session).Scan(&tok.ID, &tok.OrgID, &tok.SessionID); err != nil {
						return err
					}
					_, err := e.Store.LockRefresh(ctx, tok.ID)
					return err
				case 1: // disable a user: all admins, then the user, then its sessions
					if _, err := e.Store.LockAdmins(ctx, e.Org); err != nil {
						return err
					}
					if _, err := e.Store.LockUser(ctx, f.user); err != nil {
						return err
					}
					_, err := e.Store.LockSession(ctx, f.session)
					return err
				default: // sign out
					if _, err := e.Store.LockUser(ctx, f.user); err != nil {
						return err
					}
					_, err := e.Store.LockSession(ctx, f.session)
					return err
				}
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	stNoDeadlock(t, errs)
}

func stNoDeadlock(t *testing.T, errs []error) {
	t.Helper()
	for i, err := range errs {
		var pe *pgconn.PgError
		switch {
		case err == nil:
		case errors.As(err, &pe) && pe.Code == "40P01":
			t.Errorf("goroutine %d deadlocked: %v", i, err)
		default:
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}
