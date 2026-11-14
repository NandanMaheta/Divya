package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/db/dbgen"
	"github.com/NandanMaheta/swastik/apps/api/internal/platform/istdate"
)

// RetentionKind is the built-in recurring kind that purges finished work (FR-035).
const RetentionKind = "scheduler.retention"

// retentionAge is how long finished jobs, finished messages and tick runs are kept.
const retentionAge = 30 * 24 * time.Hour

// RetentionOptions makes the kind recurring, daily at 03:00 IST (one row per organisation).
func RetentionOptions() KindOptions {
	return KindOptions{Recurring: func(now time.Time) time.Time {
		return istdate.NextDailyAt(now, istdate.WallClock{Hour: 3, Minute: 0})
	}}
}

// RetentionHandler deletes the organisation's done, cancelled and expired jobs and its sent,
// withdrawn and expired messages finished more than 30 days ago, and tick runs older than that.
// Dead jobs and messages stay until an admin acts on them. It runs in the job's transaction.
func (s *Service) RetentionHandler() Handler {
	return func(ctx context.Context, job Job) error {
		cutoff := s.now().Add(-retentionAge)
		q := dbgen.New(s.txr.Conn(ctx))
		jobs, err := q.PurgeFinishedJobs(ctx, dbgen.PurgeFinishedJobsParams{OrgID: job.OrgID, Cutoff: db.Timestamptz(cutoff)})
		if err != nil {
			return fmt.Errorf("scheduler: retention: purge jobs: %w", err)
		}
		msgs, err := s.ob.PurgeFinished(ctx, job.OrgID, cutoff)
		if err != nil {
			return fmt.Errorf("scheduler: retention: %w", err)
		}
		runs, err := q.PurgeTickRuns(ctx, db.Timestamptz(cutoff))
		if err != nil {
			return fmt.Errorf("scheduler: retention: purge tick runs: %w", err)
		}
		s.log.InfoContext(ctx, "scheduler: retention", "org_id", job.OrgID, "jobs", jobs, "messages", msgs, "tick_runs", runs)
		return nil
	}
}
