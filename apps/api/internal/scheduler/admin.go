package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/audit"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

// FailedFilter narrows ListFailedJobs. State is "retrying", "dead" or "" for both; Kind ""
// means every kind. Limit 0 means the default page size; Cursor is the NextCursor of the
// previous page.
type FailedFilter struct {
	State  string
	Kind   string
	Limit  int
	Cursor string
}

// FailedJob is the admin view of a job (schema FailedJob in the admin contract). It never
// carries the payload, the lease or the worker name.
type FailedJob struct {
	ID             uuid.UUID  `json:"id"`
	Kind           string     `json:"kind"`
	Subject        string     `json:"subject"`
	DedupeKey      string     `json:"dedupe_key"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	FirstAttemptAt *time.Time `json:"first_attempt_at"`
	LastAttemptAt  *time.Time `json:"last_attempt_at"`
	NextAttemptAt  *time.Time `json:"next_attempt_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	LastError      *string    `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Page is one page of a keyset-paginated list.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// Status is the scheduler health view of one organisation (schema SchedulerStatus).
type Status struct {
	LastTickStartedAt   *time.Time `json:"last_tick_started_at"`
	LastTickFinishedAt  *time.Time `json:"last_tick_finished_at"`
	LastTickStatus      *string    `json:"last_tick_status"`
	Overdue             bool       `json:"overdue"`
	OverdueAfterSeconds int        `json:"overdue_after_seconds"`
	DeadJobs            int        `json:"dead_jobs"`
	DeadMessages        int        `json:"dead_messages"`
	RetryingJobs        int        `json:"retrying_jobs"`
	RetryingMessages    int        `json:"retrying_messages"`
}

func failedOf(j dbgen.ScheduledJob) FailedJob {
	f := FailedJob{
		ID: j.ID, Kind: j.Kind, Subject: j.Subject, DedupeKey: j.DedupeKey, State: j.Status,
		Attempts: int(j.Attempts), MaxAttempts: int(j.MaxAttempts),
		FirstAttemptAt: db.TimePtr(j.FirstAttemptAt), LastAttemptAt: db.TimePtr(j.LastAttemptAt),
		ExpiresAt: db.TimePtr(j.ExpiresAt), LastError: db.TextPtr(j.LastError), CreatedAt: db.Time(j.CreatedAt),
	}
	if j.Status == "pending" {
		f.NextAttemptAt = db.TimePtr(j.RunAt)
		if j.Attempts > 0 {
			f.State = "retrying"
		}
	}
	return f
}

// Status reports when the last tick started and finished and whether the scheduler is overdue
// (no tick started in the last 15 minutes, or none ever), plus the failed-work counts of the org.
func (s *Service) Status(ctx context.Context, org uuid.UUID) (Status, error) {
	st := Status{OverdueAfterSeconds: int(overdueAfter / time.Second)}
	q := dbgen.New(s.txr.Conn(ctx))
	last, err := q.LastTickRun(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		st.Overdue = true
	case err != nil:
		return Status{}, fmt.Errorf("scheduler: status: last tick: %w", err)
	default:
		started := db.Time(last.StartedAt)
		st.LastTickStartedAt, st.LastTickFinishedAt, st.LastTickStatus = &started, db.TimePtr(last.FinishedAt), &last.Status
		st.Overdue = s.now().Sub(started) > overdueAfter
	}
	jobs, err := q.CountJobsByState(ctx, org)
	if err != nil {
		return Status{}, fmt.Errorf("scheduler: status: count jobs: %w", err)
	}
	st.DeadJobs, st.RetryingJobs = int(jobs.Dead), int(jobs.Retrying)
	msgs, err := s.ob.Counts(ctx, org)
	if err != nil {
		return Status{}, fmt.Errorf("scheduler: status: %w", err)
	}
	st.DeadMessages, st.RetryingMessages = msgs.Dead, msgs.Retrying
	return st, nil
}

// ListFailedJobs lists the org's retrying and dead jobs, newest last attempt first.
func (s *Service) ListFailedJobs(ctx context.Context, org uuid.UUID, f FailedFilter) (Page[FailedJob], error) {
	page := Page[FailedJob]{Items: []FailedJob{}}
	if f.State != "" && f.State != "retrying" && f.State != "dead" {
		return page, invalid("state %q must be retrying or dead", f.State)
	}
	if len(f.Kind) > maxKindLen {
		return page, invalid("kind is longer than %d characters", maxKindLen)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)

	p := dbgen.ListFailedJobsParams{OrgID: org, State: f.State, Kind: f.Kind, RowLimit: int32(limit + 1)} //nolint:gosec // bounded by maxPageSize
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return page, err
		}
		p.CursorAt, p.CursorID = db.Timestamptz(at), uuid.NullUUID{UUID: id, Valid: true}
	}
	rows, err := dbgen.New(s.txr.Conn(ctx)).ListFailedJobs(ctx, p)
	if err != nil {
		return page, fmt.Errorf("scheduler: list failed jobs: %w", err)
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		c := encodeCursor(db.Time(last.LastAttemptAt), last.ID)
		page.NextCursor = &c
	}
	for _, r := range rows {
		page.Items = append(page.Items, failedOf(r))
	}
	return page, nil
}

// RetryJob requeues a dead job with fresh attempts; it runs on the next tick and still expires
// when its expiry has passed. Only dead jobs can be retried, and not while a newer job for the
// same key is active. idemKey is stored in the audit entry so the HTTP layer can recognise a replay.
func (s *Service) RetryJob(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedJob, error) {
	return s.adminAct(ctx, actor, org, id, idemKey, "scheduled_job.retry", func(ctx context.Context, q *dbgen.Queries, _ dbgen.ScheduledJob, now time.Time) (dbgen.ScheduledJob, error) {
		return q.AdminRetryJob(ctx, dbgen.AdminRetryJobParams{Now: db.Timestamptz(now), ID: id, OrgID: org})
	})
}

// CancelJob ends a dead job for good; its key becomes free again.
func (s *Service) CancelJob(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey string) (FailedJob, error) {
	return s.adminAct(ctx, actor, org, id, idemKey, "scheduled_job.cancel", func(ctx context.Context, q *dbgen.Queries, _ dbgen.ScheduledJob, now time.Time) (dbgen.ScheduledJob, error) {
		return q.AdminCancelJob(ctx, dbgen.AdminCancelJobParams{Now: db.Timestamptz(now), ID: id, OrgID: org})
	})
}

type adminChange func(ctx context.Context, q *dbgen.Queries, cur dbgen.ScheduledJob, now time.Time) (dbgen.ScheduledJob, error)

// adminAct runs one admin change on a dead job and its audit entry in one transaction.
func (s *Service) adminAct(ctx context.Context, actor audit.Actor, org, id uuid.UUID, idemKey, action string, change adminChange) (FailedJob, error) {
	var out FailedJob
	err := s.txr.InTx(ctx, func(ctx context.Context) error {
		tx, err := db.MustTx(ctx)
		if err != nil {
			return err
		}
		q := dbgen.New(tx)
		cur, err := q.GetJobForOrg(ctx, dbgen.GetJobForOrgParams{ID: id, OrgID: org})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: job %s", ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("scheduler: load job: %w", err)
		}
		if cur.Status != "dead" {
			return fmt.Errorf("%w: job %s is %s, only dead jobs can be changed", ErrConflict, id, failedOf(cur).State)
		}
		upd, err := change(ctx, q, cur, s.now())
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // another job for the key is active
			return fmt.Errorf("%w: another job for %s is already active", ErrConflict, cur.DedupeKey)
		}
		if err != nil {
			return fmt.Errorf("scheduler: %s: %w", action, err)
		}
		out = failedOf(upd)
		return audit.Write(ctx, audit.Entry{
			OrgID: org, Actor: actor, Entity: "scheduled_job", EntityID: id, Action: action,
			Before: auditState(cur, ""), After: auditState(upd, idemKey),
		})
	})
	if err != nil {
		return FailedJob{}, err
	}
	return out, nil
}

func auditState(j dbgen.ScheduledJob, idemKey string) map[string]any {
	state := map[string]any{
		"status": j.Status, "attempts": j.Attempts, "run_at": db.Time(j.RunAt), "last_error": db.TextPtr(j.LastError),
	}
	if idemKey != "" {
		state["idempotency_key"] = idemKey
	}
	return state
}
