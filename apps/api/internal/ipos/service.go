//nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land
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
}

func NewService(d Deps) *Service { return &Service{d: d} }

// RegisterJobs registers ipo.open, ipo.close, ipo.allotment, ipo.list.
func RegisterJobs(r JobRegistrar, s *Service) { panic(errNotImplemented) }

// Register adds the HTTP operations (admin + client).
func Register(api huma.API, s *Service) { panic(errNotImplemented) }

func (s *Service) Get(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Service) RequireEligible(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	return IPO{}, errNotImplemented
}
func (s *Service) MarkSettled(ctx context.Context, org, id uuid.UUID) error {
	return errNotImplemented
}

func (s *Service) Create(ctx context.Context, by audit.Actor, org uuid.UUID, in Draft, idemKey string) (Result, error) {
	return Result{}, errNotImplemented
}
func (s *Service) Update(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, in Draft, idemKey string) (Result, error) {
	return Result{}, errNotImplemented
}
func (s *Service) SetFlag(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, flag *ApplyFlag, note *string, idemKey string) (Result, error) {
	return Result{}, errNotImplemented
}
func (s *Service) Publish(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string) (Result, error) {
	return Result{}, errNotImplemented
}
func (s *Service) Unpublish(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string) (Result, error) {
	return Result{}, errNotImplemented
}
func (s *Service) OverrideStatus(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, to Status, reason string, idemKey string) (Result, error) {
	return Result{}, errNotImplemented
}
func (s *Service) Delete(ctx context.Context, by audit.Actor, org, id uuid.UUID, version int32, idemKey string) (bool, error) {
	return false, errNotImplemented
}
func (s *Service) List(ctx context.Context, org uuid.UUID, f AdminFilter) (Page[IPO], error) {
	return Page[IPO]{}, errNotImplemented
}
func (s *Service) ListPublished(ctx context.Context, org uuid.UUID, f ClientFilter) (Page[IPO], error) {
	return Page[IPO]{}, errNotImplemented
}
func (s *Service) GetPublished(ctx context.Context, org, id uuid.UUID) (IPO, error) {
	return IPO{}, errNotImplemented
}
