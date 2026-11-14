package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

// ErrNoTx is returned by operations that must run inside TxRunner.InTx (scheduling, outbox
// recording, audit) when the context carries no transaction.
var ErrNoTx = errors.New("db: operation requires a transaction in context")

type txKey struct{}

// TxRunner runs functions in a single transaction that is carried in the context.
type TxRunner struct {
	pool *pgxpool.Pool
}

// NewTxRunner returns a runner on pool.
func NewTxRunner(pool *pgxpool.Pool) TxRunner { return TxRunner{pool: pool} }

// InTx runs fn in one transaction stored in the context passed to fn. A nested InTx joins the
// outer transaction: only the outermost call commits or rolls back. An error or a panic in fn
// rolls back; a panic is re-raised after the rollback.
func (r TxRunner) InTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// The caller's ctx may already be cancelled; roll back on a context that is not.
		rbCtx := context.WithoutCancel(ctx)
		if rbErr := tx.Rollback(rbCtx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) && err == nil {
			err = fmt.Errorf("db: rollback: %w", rbErr)
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	committed = true
	return nil
}

// Conn returns the transaction in ctx, or the pool when there is none (read-only callers).
func (r TxRunner) Conn(ctx context.Context) dbgen.DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return r.pool
}

// MustTx returns the transaction in ctx or ErrNoTx.
func MustTx(ctx context.Context) (dbgen.DBTX, error) {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx, nil
	}
	return nil, ErrNoTx
}
