package ipos

import (
	"context"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
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

type Deps struct {
	Tx        db.TxRunner
	Scheduler Scheduler
	Now       clock.Func
	Log       *slog.Logger
}

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

// RegisterJobs registers ipo.open, ipo.close, ipo.allotment, ipo.list.
func RegisterJobs(r JobRegistrar, s *Service) { panic(errNotImplemented) }

// Register adds the HTTP operations (admin + client).
func Register(api huma.API, s *Service) { panic(errNotImplemented) }

func (s *Service) MarkSettled(ctx context.Context, org, id uuid.UUID) error {
	return errNotImplemented
}

func (s *Service) OverrideStatus(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, to Status, reason string, idemKey string) (Result, error) {
	return Result{}, errNotImplemented
}
func (s *Service) Delete(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string) (bool, error) {
	return false, errNotImplemented
}
