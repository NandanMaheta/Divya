package scheduler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/NandanMaheta/swastik/apps/api/internal/outbox"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/retry"
)

// Ticker is what the tick handler calls; *Service satisfies it.
type Ticker interface {
	Tick(ctx context.Context) (TickSummary, error)
}

// Tick is called only by the tick handler (and tests). It records a tick_runs row, makes sure
// every recurring kind has an active job per organisation, expires stale jobs, runs due jobs
// oldest first within the caps and time budget, relays the outbox, alerts about newly dead items
// and finishes the row. Failures of single jobs or messages are data, not errors; an error is
// returned only when the tick could not do its work (the handler answers 503, FR-007), and each
// committed job is atomic anyway.
func (s *Service) Tick(ctx context.Context) (TickSummary, error) {
	var sum TickSummary
	if _, err := db.MustTx(ctx); err == nil {
		return sum, errors.New("scheduler: Tick must not run inside a transaction")
	}
	q := dbgen.New(s.txr.Conn(ctx))
	started := s.now()
	id, err := q.InsertTickRun(ctx, dbgen.InsertTickRunParams{StartedAt: db.Timestamptz(started), Instance: s.instance})
	if err != nil {
		return sum, fmt.Errorf("scheduler: tick: insert tick run: %w", err)
	}
	sum.TickID, sum.StartedAt = id, started

	status := "ok"
	runErr := s.runPhases(ctx, q, &sum)
	switch {
	case runErr != nil:
		status = "error"
	default:
		// The alert sweep runs after the work; a failure marks nothing and is retried next tick
		// (K8). It does not fail the tick: the jobs and messages phases already ran.
		if err := s.sweepDead(ctx, id); err != nil {
			status = "error"
			s.log.ErrorContext(ctx, "scheduler: dead-item alert sweep failed; it is retried on the next tick", "tick_id", id, "error", retry.ErrorText(err))
		}
	}
	sum.FinishedAt = s.now()

	finishCtx := context.WithoutCancel(ctx)
	if err := q.FinishTickRun(finishCtx, dbgen.FinishTickRunParams{
		FinishedAt: db.Timestamptz(sum.FinishedAt), Status: status,
		JobsRun: int32(sum.Jobs.Run), JobsFailed: int32(sum.Jobs.Failed), JobsDead: int32(sum.Jobs.Dead), JobsExpired: int32(sum.Jobs.Expired), //nolint:gosec // bounded by the per-tick caps
		MsgsSent: int32(sum.Messages.Sent), MsgsFailed: int32(sum.Messages.Failed), MsgsDead: int32(sum.Messages.Dead), MsgsExpired: int32(sum.Messages.Expired), //nolint:gosec // bounded by the per-tick caps
		BudgetExhausted: sum.BudgetExhausted, ID: id,
	}); err != nil && runErr == nil {
		runErr = fmt.Errorf("scheduler: tick: finish tick run: %w", err)
	}
	if runErr != nil {
		return sum, runErr
	}
	s.log.InfoContext(ctx, "scheduler: tick finished", "tick_id", id, "status", status,
		"jobs_run", sum.Jobs.Run, "jobs_failed", sum.Jobs.Failed, "jobs_dead", sum.Jobs.Dead, "jobs_expired", sum.Jobs.Expired,
		"msgs_sent", sum.Messages.Sent, "msgs_failed", sum.Messages.Failed, "msgs_dead", sum.Messages.Dead, "msgs_expired", sum.Messages.Expired,
		"budget_exhausted", sum.BudgetExhausted)
	return sum, nil
}

// runPhases is the jobs phase followed by the messages phase.
func (s *Service) runPhases(ctx context.Context, q *dbgen.Queries, sum *TickSummary) error {
	if err := s.ensureRecurring(ctx, q); err != nil {
		return err
	}
	expired, err := q.ExpireDueJobs(ctx, db.Timestamptz(s.now()))
	if err != nil {
		return fmt.Errorf("scheduler: tick: expire due jobs: %w", err)
	}
	sum.Jobs.Expired = int(expired)

	if err := s.runJobs(ctx, q, sum); err != nil {
		return err
	}

	deadline := sum.StartedAt.Add(s.opts.TickBudget)
	rs, err := s.ob.Relay(ctx, outbox.RelayBudget{MaxMessages: s.opts.MaxMessages, Deadline: deadline})
	sum.Messages = MessageCounts{Sent: rs.Sent, Failed: rs.Failed, Dead: rs.Dead, Expired: rs.Expired}
	if err != nil {
		return fmt.Errorf("scheduler: tick: %w", err)
	}
	if rs.Sent+rs.Failed+rs.Dead >= s.opts.MaxMessages || !s.now().Before(deadline) {
		sum.BudgetExhausted = true
	}
	return nil
}

// ensureRecurring gives every organisation one active row per recurring kind, so recurring work
// self-heals after a dead or lost row. A new row first runs at the next occurrence.
func (s *Service) ensureRecurring(ctx context.Context, q *dbgen.Queries) error {
	kinds := s.recurringKinds()
	if len(kinds) == 0 {
		return nil
	}
	orgs, err := q.ListOrgIDs(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: tick: list orgs: %w", err)
	}
	now := s.now()
	for _, k := range kinds {
		for _, org := range orgs {
			err := q.EnsureRecurringJob(ctx, dbgen.EnsureRecurringJobParams{
				OrgID: org, Kind: k.kind, Subject: "scheduler:" + k.kind, RunAt: db.Timestamptz(k.reg.opts.Recurring(now)),
				MaxAttempts: s.maxAttemptsFor(k.reg), Now: db.Timestamptz(now),
			})
			if err != nil {
				return fmt.Errorf("scheduler: tick: ensure recurring %s: %w", k.kind, err)
			}
		}
	}
	return nil
}

// runJobs claims and runs due jobs one at a time until none is left, the per-tick cap is reached
// or the jobs phase budget is used up; in the last two cases the rest waits for the next tick.
func (s *Service) runJobs(ctx context.Context, q *dbgen.Queries, sum *TickSummary) error {
	deadline := sum.StartedAt.Add(s.opts.JobsPhase)
	for claimed := 0; ; claimed++ {
		now := s.now()
		if claimed >= s.opts.MaxJobs || !now.Before(deadline) {
			sum.BudgetExhausted = true
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("scheduler: tick: %w", err)
		}
		job, err := q.ClaimJob(ctx, dbgen.ClaimJobParams{
			Now: db.Timestamptz(now), LockedUntil: db.Timestamptz(now.Add(s.opts.Lease)), LockedBy: db.Text(s.instance),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scheduler: tick: claim job: %w", err)
		}
		if err := s.execute(ctx, q, job, sum); err != nil {
			return fmt.Errorf("scheduler: tick: %w", err)
		}
	}
}
