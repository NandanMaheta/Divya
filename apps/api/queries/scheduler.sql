-- Scheduler queries (feature 002). Every instant is a parameter taken from the injected clock;
-- nothing here calls now() (FR-032). Rows are claimed one at a time with FOR UPDATE SKIP LOCKED.

-- name: ListOrgIDs :many
SELECT id FROM orgs ORDER BY id;

-- Schedule: one active (pending or running) job per (org, kind, dedupe_key). A pending row takes
-- the latest values; a running row is flagged to run again at the new time when it finishes.
-- name: UpsertJob :one
INSERT INTO scheduled_jobs (
    org_id, kind, dedupe_key, subject, payload, run_at, expires_at, recurring, max_attempts,
    created_at, updated_at
) VALUES (
    @org_id, @kind, @dedupe_key, @subject, @payload, @run_at, sqlc.narg('expires_at'), @recurring, @max_attempts,
    @now, @now
)
ON CONFLICT (org_id, kind, dedupe_key) WHERE status IN ('pending', 'running')
DO UPDATE SET
    subject          = EXCLUDED.subject,
    payload          = EXCLUDED.payload,
    run_at           = EXCLUDED.run_at,
    expires_at       = EXCLUDED.expires_at,
    rerun            = (scheduled_jobs.status = 'running'),
    cancel_requested = false,
    updated_at       = @now
RETURNING id;

-- Reschedule never creates a row. A running job is flagged to run again at the new time.
-- name: RescheduleJob :execrows
UPDATE scheduled_jobs SET
    run_at           = @run_at,
    rerun            = (status = 'running'),
    cancel_requested = false,
    updated_at       = @now
WHERE org_id = @org_id AND kind = @kind AND dedupe_key = @dedupe_key
  AND status IN ('pending', 'running');

-- Cancel: pending becomes cancelled; a running job is only flagged, its in-flight run decides.
-- name: CancelJobByKey :execrows
UPDATE scheduled_jobs SET
    status           = CASE WHEN status = 'pending' THEN 'cancelled' ELSE status END,
    finished_at      = CASE WHEN status = 'pending' THEN @now::timestamptz ELSE finished_at END,
    cancel_requested = (status = 'running'),
    rerun            = false,
    updated_at       = @now
WHERE org_id = @org_id AND kind = @kind AND dedupe_key = @dedupe_key
  AND status IN ('pending', 'running');

-- name: CancelJobsBySubject :execrows
UPDATE scheduled_jobs SET
    status           = CASE WHEN status = 'pending' THEN 'cancelled' ELSE status END,
    finished_at      = CASE WHEN status = 'pending' THEN @now::timestamptz ELSE finished_at END,
    cancel_requested = (status = 'running'),
    rerun            = false,
    updated_at       = @now
WHERE org_id = @org_id AND subject = @subject
  AND status IN ('pending', 'running');

-- Expiry sweep: time-sensitive work whose expiry has passed is never run (FR-014). Bounded per tick.
-- name: ExpireDueJobs :execrows
WITH due AS (
    SELECT id FROM scheduled_jobs
    WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at <= @now
    ORDER BY expires_at, id
    LIMIT 1000
    FOR UPDATE SKIP LOCKED
)
UPDATE scheduled_jobs j SET status = 'expired', finished_at = @now, updated_at = @now
FROM due WHERE j.id = due.id;

-- Recover jobs whose run crashed (status running, lease over) before any claim. Precedence:
-- cancel requested -> cancelled; expired -> expired; rerun -> pending again at its run_at with a
-- fresh attempt budget. Other stale rows are left for ClaimJob to take over. Bounded per tick.
-- name: RecoverStaleJobs :execrows
WITH stale AS (
    SELECT id FROM scheduled_jobs
    WHERE status = 'running' AND locked_until <= @now
      AND (cancel_requested OR rerun OR (expires_at IS NOT NULL AND expires_at <= @now))
    ORDER BY locked_until, id
    LIMIT 1000
    FOR UPDATE SKIP LOCKED
)
UPDATE scheduled_jobs j SET
    status           = CASE
                           WHEN j.cancel_requested THEN 'cancelled'
                           WHEN j.expires_at IS NOT NULL AND j.expires_at <= @now THEN 'expired'
                           ELSE 'pending'
                       END,
    finished_at      = CASE
                           WHEN j.cancel_requested OR (j.expires_at IS NOT NULL AND j.expires_at <= @now) THEN @now::timestamptz
                           ELSE NULL
                       END,
    attempts         = CASE WHEN j.cancel_requested OR (j.expires_at IS NOT NULL AND j.expires_at <= @now) THEN j.attempts ELSE 0 END,
    first_attempt_at = CASE WHEN j.cancel_requested OR (j.expires_at IS NOT NULL AND j.expires_at <= @now) THEN j.first_attempt_at ELSE NULL END,
    rerun            = false,
    cancel_requested = false,
    locked_until     = NULL,
    lock_token       = NULL,
    locked_by        = NULL,
    updated_at       = @now
FROM stale WHERE j.id = stale.id;

-- Claim one job (or one whose lease ran out) under a fresh lease and fencing token.
-- name: ClaimJob :one
WITH c AS (
    SELECT id FROM scheduled_jobs
    WHERE (status = 'pending' AND run_at <= @now AND (expires_at IS NULL OR expires_at > @now))
       OR (status = 'running' AND run_at <= @now AND locked_until <= @now)
    ORDER BY run_at, id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE scheduled_jobs j SET
    status           = 'running',
    attempts         = j.attempts + 1,
    first_attempt_at = COALESCE(j.first_attempt_at, @now),
    last_attempt_at  = @now,
    locked_until     = @locked_until,
    lock_token       = gen_random_uuid(),
    locked_by        = @locked_by,
    updated_at       = @now
FROM c WHERE j.id = c.id
RETURNING j.*;

-- Complete runs inside the handler's transaction and is fenced by the lock token: zero rows
-- means the lease was lost and the caller rolls the whole transaction back.
-- name: CompleteJob :execrows
UPDATE scheduled_jobs SET
    status           = CASE
                           WHEN cancel_requested AND recurring THEN 'cancelled'
                           WHEN recurring OR rerun THEN 'pending'
                           ELSE 'done'
                       END,
    run_at           = CASE WHEN rerun THEN run_at WHEN recurring THEN @next_run_at::timestamptz ELSE run_at END,
    attempts         = CASE WHEN recurring OR rerun THEN 0 ELSE attempts END,
    first_attempt_at = CASE WHEN recurring OR rerun THEN NULL ELSE first_attempt_at END,
    last_error       = CASE WHEN recurring OR rerun THEN NULL ELSE last_error END,
    finished_at      = CASE WHEN (recurring AND NOT cancel_requested) OR rerun THEN NULL ELSE @now::timestamptz END,
    rerun            = false,
    cancel_requested = false,
    locked_until     = NULL,
    lock_token       = NULL,
    locked_by        = NULL,
    updated_at       = @now
WHERE id = @id AND lock_token = @lock_token::uuid AND status = 'running';

-- Fail: back to pending after a backoff, dead when attempts are used up, cancelled when a cancel
-- was requested during the run, pending again at the new time when it was rescheduled meanwhile.
-- name: FailJob :one
UPDATE scheduled_jobs SET
    status           = CASE
                           WHEN cancel_requested THEN 'cancelled'
                           WHEN rerun THEN 'pending'
                           WHEN attempts >= max_attempts THEN 'dead'
                           ELSE 'pending'
                       END,
    run_at           = CASE
                           WHEN rerun OR cancel_requested OR attempts >= max_attempts THEN run_at
                           ELSE @next_run_at::timestamptz
                       END,
    attempts         = CASE WHEN rerun THEN 0 ELSE attempts END,
    finished_at      = CASE
                           WHEN cancel_requested OR (NOT rerun AND attempts >= max_attempts) THEN @now::timestamptz
                           ELSE NULL
                       END,
    last_error       = @last_error::text,
    rerun            = false,
    cancel_requested = false,
    locked_until     = NULL,
    lock_token       = NULL,
    locked_by        = NULL,
    updated_at       = @now
WHERE id = @id AND lock_token = @lock_token::uuid AND status = 'running'
RETURNING status, attempts;

-- A job whose lease expired more often than it has attempts left is dead without running again.
-- name: MarkJobDead :execrows
UPDATE scheduled_jobs SET
    status       = 'dead',
    finished_at  = @now,
    last_error   = @last_error::text,
    locked_until = NULL,
    lock_token   = NULL,
    locked_by    = NULL,
    updated_at   = @now
WHERE id = @id AND lock_token = @lock_token::uuid AND status = 'running';

-- Recurring kinds always have one active row per org; this self-heals after a dead or lost row.
-- name: EnsureRecurringJob :exec
INSERT INTO scheduled_jobs (
    org_id, kind, dedupe_key, subject, run_at, recurring, max_attempts, created_at, updated_at
) VALUES (
    @org_id, @kind, 'recurring', @subject, @run_at, true, @max_attempts, @now, @now
)
ON CONFLICT (org_id, kind, dedupe_key) WHERE status IN ('pending', 'running') DO NOTHING;
