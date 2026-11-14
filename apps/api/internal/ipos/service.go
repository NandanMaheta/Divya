package ipos

import (
	"context"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/clock"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/scheduler"
)

// Scheduler is satisfied by *scheduler.Service.
type Scheduler interface {
	Schedule(ctx context.Context, spec scheduler.JobSpec) error
	Cancel(ctx context.Context, org uuid.UUID, kind, key string) (int, error)
	CancelSubject(ctx context.Context, org uuid.UUID, subject string) (int, error)
}

// JobRegistrar is satisfied by *scheduler.Service.
type JobRegistrar interface {
	Register(kind string, h scheduler.Handler, opts scheduler.KindOptions)
}

// Deps are the collaborators of the service.
type Deps struct {
	Tx        db.TxRunner
	Scheduler Scheduler
	Now       clock.Func
	Log       *slog.Logger
}

// Service is the IPO catalogue: mutations, reads, lifecycle steps and signals.
type Service struct {
	d     Deps
	store *Store
	sig   listeners
}

// NewService builds the service; it needs no other wiring (the Store is stateless).
func NewService(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{d: d, store: &Store{}}
}

// inTx runs fn in the caller's transaction, or in a new one when ctx has none.
func (s *Service) inTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.d.Tx.InTx(ctx, fn)
}

// Register adds the HTTP operations (admin + client).
func Register(api huma.API, s *Service) { panic(errNotImplemented) }
