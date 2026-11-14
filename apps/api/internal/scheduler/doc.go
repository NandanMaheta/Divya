// Package scheduler runs the durable, tick-driven scheduled jobs of the platform: owning features
// schedule work in their own transaction, the tick (called by Cloud Scheduler) claims due jobs
// under a lease, runs each handler atomically with its "done" mark, retries failures with backoff,
// expires stale work, relays the outbox and alerts the admin about dead items. Time always comes
// from the injected clock (constitution III).
//
// # Scheduling from an owning feature
//
// Register the kind once at wiring time, then call Schedule inside the same transaction as the
// business change, so the job commits or vanishes with it:
//
//	svc.Register("ipo.open", handler, scheduler.KindOptions{})
//	err := txr.InTx(ctx, func(ctx context.Context) error {
//		// ... business change ...
//		return svc.Schedule(ctx, scheduler.JobSpec{
//			OrgID: org, Kind: "ipo.open", Key: "ipo:"+id+":open",
//			Subject: "ipo:"+id, RunAt: openAt, Payload: map[string]string{"ipo_id": id},
//		})
//	})
//
// Key is the dedupe key: rescheduling with the same key replaces the pending job. A nil ExpiresAt
// means state-change work that always runs, however late.
//
// # Handler idempotency contract
//
// A handler runs in the same transaction as the job's "done" mark, but may still be retried (lease
// expiry, crash, backoff), so it must be idempotent: re-running it for the same job must not
// double-apply effects. Use the job Key or business state as the guard, carry ids only in the
// payload, and return an error to request a retry with backoff.
package scheduler
