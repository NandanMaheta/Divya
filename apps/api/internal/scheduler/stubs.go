package scheduler

// Compile skeleton (T045): every exported name the harness and tests use, so the package builds.
// T046-T056 replace each stub with the real implementation and this file disappears.

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/tickauth"
)

var errTodo = errors.New("scheduler: not implemented")

func (s *Service) Schedule(context.Context, JobSpec) error { return errTodo }
func (s *Service) Reschedule(context.Context, uuid.UUID, string, string, time.Time) (bool, error) {
	return false, errTodo
}
func (s *Service) Cancel(context.Context, uuid.UUID, string, string) (int, error) { return 0, errTodo }
func (s *Service) CancelSubject(context.Context, uuid.UUID, string) (int, error) {
	return 0, errTodo
}
func (s *Service) Tick(context.Context) (TickSummary, error) { return TickSummary{}, errTodo }

type Status struct{}

func (s *Service) Status(context.Context, uuid.UUID) (Status, error) { return Status{}, errTodo }

type FailedFilter struct {
	State, Kind string
	Limit       int
	Cursor      string
}
type FailedJob struct{}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func (s *Service) ListFailedJobs(context.Context, uuid.UUID, FailedFilter) (Page[FailedJob], error) {
	return Page[FailedJob]{}, errTodo
}
func (s *Service) RetryJob(context.Context, audit.Actor, uuid.UUID, uuid.UUID, string) (FailedJob, error) {
	return FailedJob{}, errTodo
}
func (s *Service) CancelJob(context.Context, audit.Actor, uuid.UUID, uuid.UUID, string) (FailedJob, error) {
	return FailedJob{}, errTodo
}

type Ticker interface {
	Tick(ctx context.Context) (TickSummary, error)
}

func RegisterTick(huma.API, Ticker, tickauth.Authenticator) {}

const RetentionKind = "scheduler.retention"

func (s *Service) RetentionHandler() Handler { return func(context.Context, Job) error { return nil } }
func RetentionOptions() KindOptions         { return KindOptions{} }
