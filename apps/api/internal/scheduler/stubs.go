package scheduler

// Compile skeleton (T045): exported names still to be implemented. T047-T056 replace each stub
// and this file disappears.

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/tickauth"
)

var errTodo = errors.New("scheduler: not implemented")

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

func RegisterTick(huma.API, Ticker, tickauth.Authenticator) {}

const RetentionKind = "scheduler.retention"

func (s *Service) RetentionHandler() Handler { return func(context.Context, Job) error { return nil } }
func RetentionOptions() KindOptions          { return KindOptions{} }
