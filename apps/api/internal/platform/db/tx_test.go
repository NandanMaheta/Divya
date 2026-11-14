package db_test

// T023 - TxRunner (specs/002-scheduler-tick/contracts/go-module-api.md, "internal/platform/db").
//
// Contract under test: ErrNoTx, NewTxRunner(pool), TxRunner.InTx / Conn, MustTx.
// Refs: FR-008, FR-023, US1.10.
//
// Assumptions:
//   - The tests use the 00001 table `orgs` as the probe table, so they need no new migration.
//   - A panic inside fn must roll back. Whether InTx re-panics or converts the panic into an error
//     is not fixed by the contract, so the test accepts either, but never "committed and silent".
//   - Nesting is "join": the inner InTx runs in the outer transaction and neither commits nor
//     rolls back on its own; the outcome is decided by the outermost call.
//   - db.Open limits the pool to 5 connections, which the leak test relies on.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/dbtest"
)

var errTxBoom = errors.New("boom")

// poolCount counts rows through the pool, i.e. through a different connection than any open
// transaction, so it only sees committed data (READ COMMITTED).
func poolCount(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orgs WHERE name = $1`, name).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", name, err)
	}
	return n
}

func insertViaCtx(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	tx, err := db.MustTx(ctx)
	if err != nil {
		t.Fatalf("MustTx inside InTx: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO orgs (name) VALUES ($1)`, name); err != nil {
		t.Fatalf("insert %q: %v", name, err)
	}
}

func TestTxRunner_ErrNoTxIsASentinel(t *testing.T) {
	if db.ErrNoTx == nil {
		t.Fatal("ErrNoTx is nil")
	}
	wrapped := fmt.Errorf("outbox: add: %w", db.ErrNoTx)
	if !errors.Is(wrapped, db.ErrNoTx) {
		t.Fatal("ErrNoTx must be matchable with errors.Is through wrapping")
	}
}

func TestTxRunner_MustTxWithoutTransactionReturnsErrNoTx(t *testing.T) {
	tx, err := db.MustTx(t.Context())
	if !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("MustTx(ctx without tx) error = %v, want ErrNoTx", err)
	}
	if tx != nil {
		t.Fatalf("MustTx returned a non-nil DBTX together with ErrNoTx: %v", tx)
	}
}

func TestTxRunner_InTxCommitsWhenFnReturnsNil(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	r := db.NewTxRunner(pool)

	err := r.InTx(ctx, func(ctx context.Context) error {
		insertViaCtx(ctx, t, "commit-me")
		if got := poolCount(ctx, t, pool, "commit-me"); got != 0 {
			t.Errorf("another connection sees %d uncommitted rows; InTx must not autocommit", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("InTx = %v, want nil", err)
	}
	if got := poolCount(ctx, t, pool, "commit-me"); got != 1 {
		t.Fatalf("rows after commit = %d, want 1", got)
	}
}

func TestTxRunner_InTxRollsBackAndReturnsTheErrorWhenFnFails(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	r := db.NewTxRunner(pool)

	err := r.InTx(ctx, func(ctx context.Context) error {
		insertViaCtx(ctx, t, "rollback-me")
		return errTxBoom
	})
	if !errors.Is(err, errTxBoom) {
		t.Fatalf("InTx = %v, want an error matching fn's error", err)
	}
	if got := poolCount(ctx, t, pool, "rollback-me"); got != 0 {
		t.Fatalf("rows after rollback = %d, want 0", got)
	}
}

func TestTxRunner_InTxRollsBackWhenFnPanics(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	r := db.NewTxRunner(pool)

	var (
		recovered any
		err       error
	)
	func() {
		defer func() { recovered = recover() }()
		err = r.InTx(ctx, func(ctx context.Context) error {
			insertViaCtx(ctx, t, "panic-me")
			panic("handler exploded")
		})
	}()
	if recovered == nil && err == nil {
		t.Fatal("a panic inside fn vanished: InTx neither re-panicked nor returned an error")
	}
	if got := poolCount(ctx, t, pool, "panic-me"); got != 0 {
		t.Fatalf("rows after panic = %d, want 0 (rolled back)", got)
	}
	// The pool must still be usable: the connection went back.
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("%d connections still acquired after a panic in InTx", got)
	}
}

func TestTxRunner_NestedInTxJoinsTheOuterTransaction(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	r := db.NewTxRunner(pool)

	t.Run("inner sees the outer's uncommitted write and outer error rolls both back", func(t *testing.T) {
		err := r.InTx(ctx, func(ctx context.Context) error {
			insertViaCtx(ctx, t, "outer-1")
			inner := r.InTx(ctx, func(ctx context.Context) error {
				tx, err := db.MustTx(ctx)
				if err != nil {
					return err
				}
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM orgs WHERE name = 'outer-1'`).Scan(&n); err != nil {
					return err
				}
				if n != 1 {
					t.Errorf("inner InTx sees %d outer rows, want 1: nested InTx must join, not open a new transaction", n)
				}
				insertViaCtx(ctx, t, "inner-1")
				return nil
			})
			if inner != nil {
				t.Errorf("inner InTx = %v, want nil", inner)
			}
			if got := poolCount(ctx, t, pool, "inner-1"); got != 0 {
				t.Errorf("inner InTx committed on its own (%d rows visible); it must wait for the outer", got)
			}
			return errTxBoom
		})
		if !errors.Is(err, errTxBoom) {
			t.Fatalf("outer InTx = %v, want errTxBoom", err)
		}
		for _, name := range []string{"outer-1", "inner-1"} {
			if got := poolCount(ctx, t, pool, name); got != 0 {
				t.Errorf("%s survived the outer rollback (%d rows)", name, got)
			}
		}
	})

	t.Run("both commit together when the outer succeeds", func(t *testing.T) {
		err := r.InTx(ctx, func(ctx context.Context) error {
			insertViaCtx(ctx, t, "outer-2")
			return r.InTx(ctx, func(ctx context.Context) error {
				insertViaCtx(ctx, t, "inner-2")
				return nil
			})
		})
		if err != nil {
			t.Fatalf("outer InTx = %v", err)
		}
		for _, name := range []string{"outer-2", "inner-2"} {
			if got := poolCount(ctx, t, pool, name); got != 1 {
				t.Errorf("%s rows = %d, want 1", name, got)
			}
		}
	})

	t.Run("an inner error that the outer returns rolls everything back", func(t *testing.T) {
		err := r.InTx(ctx, func(ctx context.Context) error {
			insertViaCtx(ctx, t, "outer-3")
			return r.InTx(ctx, func(ctx context.Context) error {
				insertViaCtx(ctx, t, "inner-3")
				return errTxBoom
			})
		})
		if !errors.Is(err, errTxBoom) {
			t.Fatalf("outer InTx = %v, want errTxBoom", err)
		}
		for _, name := range []string{"outer-3", "inner-3"} {
			if got := poolCount(ctx, t, pool, name); got != 0 {
				t.Errorf("%s survived (%d rows)", name, got)
			}
		}
	})
}

func TestTxRunner_ConnReturnsTheTransactionInsideAndThePoolOutside(t *testing.T) {
	pool := dbtest.New(t)
	ctx := t.Context()
	r := db.NewTxRunner(pool)

	t.Run("inside InTx Conn is the transaction", func(t *testing.T) {
		err := r.InTx(ctx, func(ctx context.Context) error {
			if _, err := r.Conn(ctx).Exec(ctx, `INSERT INTO orgs (name) VALUES ('via-conn')`); err != nil {
				return err
			}
			if got := poolCount(ctx, t, pool, "via-conn"); got != 0 {
				t.Errorf("Conn inside InTx wrote outside the transaction (%d rows visible to another connection)", got)
			}
			return errTxBoom // roll back
		})
		if !errors.Is(err, errTxBoom) {
			t.Fatalf("InTx = %v", err)
		}
		if got := poolCount(ctx, t, pool, "via-conn"); got != 0 {
			t.Fatalf("write through Conn survived the rollback (%d rows)", got)
		}
	})

	t.Run("outside a transaction Conn falls back to the pool", func(t *testing.T) {
		conn := r.Conn(ctx)
		if conn == nil {
			t.Fatal("Conn returned nil without a transaction")
		}
		if _, err := conn.Exec(ctx, `INSERT INTO orgs (name) VALUES ('via-pool')`); err != nil {
			t.Fatalf("exec through pool fallback: %v", err)
		}
		if got := poolCount(ctx, t, pool, "via-pool"); got != 1 {
			t.Fatalf("pool fallback did not autocommit (%d rows visible)", got)
		}
		var one int
		if err := conn.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatalf("read through pool fallback: %d, %v", one, err)
		}
	})
}

func TestTxRunner_TransactionDoesNotLeakIntoTheCallersContext(t *testing.T) {
	pool := dbtest.New(t)
	outer := t.Context()
	r := db.NewTxRunner(pool)

	if err := r.InTx(outer, func(ctx context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MustTx(outer); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("MustTx(caller ctx) after InTx = %v, want ErrNoTx: the transaction must live only in fn's ctx", err)
	}
}

func TestTxRunner_EveryOutcomeReleasesItsConnection(t *testing.T) {
	pool := dbtest.New(t)
	r := db.NewTxRunner(pool)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// db.Open caps the pool at 5 connections: a leak per outcome would deadlock well before 60 runs.
	for i := 0; i < 20; i++ {
		if err := r.InTx(ctx, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("commit run %d: %v", i, err)
		}
		if err := r.InTx(ctx, func(context.Context) error { return errTxBoom }); !errors.Is(err, errTxBoom) {
			t.Fatalf("rollback run %d: %v", i, err)
		}
		func() {
			defer func() { _ = recover() }()
			_ = r.InTx(ctx, func(context.Context) error { panic("x") })
		}()
	}
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("%d connections still acquired after 60 transactions", got)
	}
}
