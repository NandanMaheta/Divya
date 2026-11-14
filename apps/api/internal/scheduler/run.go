package scheduler

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/jackc/pgx/v5"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/retry"
)

// errLeaseLost means the completion mark matched no row: another claim holds the job now, so the
// whole transaction (handler effects included) is rolled back and nothing is recorded.
var errLeaseLost = errors.New("scheduler: lease lost")

// execute runs one claimed job and records the outcome. A failing handler is data (a retry, or
// dead when the attempts are used up); only database errors are returned.
func (s *Service) execute(ctx context.Context, q *dbgen.Queries, job dbgen.ScheduledJob, sum *TickSummary) error {
	token := job.LockToken.UUID
	if job.Attempts > job.MaxAttempts {
		// The lease ran out so often that the attempts are used up without a reported failure.
		n, err := q.MarkJobDead(ctx, dbgen.MarkJobDeadParams{
			Now: db.Timestamptz(s.now()), LastError: "lease expired too often", ID: job.ID, LockToken: token,
		})
		if err != nil {
			return fmt.Errorf("scheduler: mark dead: %w", err)
		}
		if n == 1 {
			sum.Jobs.Dead++
			s.log.WarnContext(ctx, "scheduler: job is dead", "job_id", job.ID, "kind", job.Kind, "reason", "lease expired too often")
		}
		return nil
	}

	reg, ok := s.registrationFor(job.Kind)
	if !ok {
		return s.fail(ctx, q, job, fmt.Errorf("%w: %q", ErrUnknownKind, job.Kind), sum)
	}

	err := s.runInTx(ctx, reg, job)
	switch {
	case err == nil:
		sum.Jobs.Run++
		return nil
	case errors.Is(err, errLeaseLost):
		s.log.WarnContext(ctx, "scheduler: lease lost before completion; effects rolled back", "job_id", job.ID, "kind", job.Kind)
		return nil
	default:
		return s.fail(ctx, q, job, err, sum)
	}
}

// runInTx runs the handler and the fenced completion in one transaction: the effects of the
// handler become visible together with the "done" mark, or not at all. The claim committed
// earlier and the handler's transaction does not touch the job row until it completes it, so
// another instance can reclaim the row when the lease runs out mid-run.
func (s *Service) runInTx(ctx context.Context, reg registration, job dbgen.ScheduledJob) error {
	hctx, cancel := context.WithTimeout(ctx, s.opts.HandlerTimeout)
	defer cancel()
	return s.txr.InTx(hctx, func(txCtx context.Context) error {
		if err := s.callHandler(txCtx, reg, job); err != nil {
			return err
		}
		tx, err := db.MustTx(txCtx)
		if err != nil {
			return err
		}
		now := s.now()
		next := now
		if job.Recurring && reg.opts.Recurring != nil {
			next = reg.opts.Recurring(now)
		}
		n, err := dbgen.New(tx).CompleteJob(txCtx, dbgen.CompleteJobParams{
			NextRunAt: db.Timestamptz(next), Now: db.Timestamptz(now), ID: job.ID, LockToken: job.LockToken.UUID,
		})
		if err != nil {
			return fmt.Errorf("scheduler: complete job: %w", err)
		}
		if n == 0 {
			return errLeaseLost
		}
		return nil
	})
}

// callHandler calls the handler and turns a panic into an error; the stack stays in the log.
func (s *Service) callHandler(ctx context.Context, reg registration, job dbgen.ScheduledJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.ErrorContext(ctx, "scheduler: handler panicked", "job_id", job.ID, "kind", job.Kind, "stack", string(debug.Stack()))
			err = fmt.Errorf("handler panicked: %v", r)
		}
	}()
	return reg.handler(ctx, jobOf(job))
}

func jobOf(j dbgen.ScheduledJob) Job {
	return Job{
		ID: j.ID, OrgID: j.OrgID, Kind: j.Kind, Key: j.DedupeKey, Subject: j.Subject, Payload: j.Payload,
		RunAt: db.Time(j.RunAt), ExpiresAt: db.TimePtr(j.ExpiresAt), Attempt: int(j.Attempts),
	}
}

// fail records a failed attempt: retry after a backoff, dead when the attempts are used up. The
// fenced update means a run that lost its lease cannot change the job.
func (s *Service) fail(ctx context.Context, q *dbgen.Queries, job dbgen.ScheduledJob, cause error, sum *TickSummary) error {
	now := s.now()
	res, err := q.FailJob(ctx, dbgen.FailJobParams{
		NextRunAt: db.Timestamptz(now.Add(retry.Backoff(int(job.Attempts), job.ID.String()))),
		Now:       db.Timestamptz(now),
		LastError: retry.ErrorText(cause),
		ID:        job.ID,
		LockToken: job.LockToken.UUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.log.WarnContext(ctx, "scheduler: lease lost before the failure was recorded", "job_id", job.ID, "kind", job.Kind)
		return nil
	}
	if err != nil {
		return fmt.Errorf("scheduler: record failure: %w", err)
	}
	switch res.Status {
	case "dead":
		sum.Jobs.Dead++
		s.log.WarnContext(ctx, "scheduler: job is dead", "job_id", job.ID, "kind", job.Kind, "attempts", res.Attempts)
	case "cancelled":
	default:
		sum.Jobs.Failed++
	}
	return nil
}
