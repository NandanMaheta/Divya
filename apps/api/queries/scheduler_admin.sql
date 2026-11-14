-- Scheduler status, admin and retention queries (feature 002).

-- name: InsertTickRun :one
INSERT INTO tick_runs (started_at, status, instance)
VALUES (@started_at, 'running', @instance)
RETURNING id;

-- name: FinishTickRun :exec
UPDATE tick_runs SET
    finished_at      = @finished_at,
    status           = @status,
    jobs_run         = @jobs_run,
    jobs_failed      = @jobs_failed,
    jobs_dead        = @jobs_dead,
    jobs_expired     = @jobs_expired,
    msgs_sent        = @msgs_sent,
    msgs_failed      = @msgs_failed,
    msgs_dead        = @msgs_dead,
    msgs_expired     = @msgs_expired,
    budget_exhausted = @budget_exhausted
WHERE id = @id;

-- The most recent tick, whatever its outcome (FR-021).
-- name: LastTickRun :one
SELECT * FROM tick_runs ORDER BY started_at DESC, id DESC LIMIT 1;

-- Retrying and dead jobs of one org, newest last attempt first, keyset-paginated (FR-019).
-- state: empty for both, retrying or dead; kind: empty for every kind.
-- name: ListFailedJobs :many
SELECT * FROM scheduled_jobs
WHERE org_id = @org_id
  AND (status = 'dead' OR (status = 'pending' AND attempts > 0))
  AND (@state::text = '' OR (@state::text = 'dead' AND status = 'dead') OR (@state::text = 'retrying' AND status = 'pending'))
  AND (@kind::text = '' OR kind = @kind::text)
  AND (
      sqlc.narg('cursor_at')::timestamptz IS NULL
      OR (last_attempt_at, id) < (sqlc.narg('cursor_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY last_attempt_at DESC, id DESC
LIMIT @row_limit;

-- name: CountJobsByState :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::int                   AS pending,
    count(*) FILTER (WHERE status = 'pending' AND attempts > 0)::int  AS retrying,
    count(*) FILTER (WHERE status = 'running')::int                   AS running,
    count(*) FILTER (WHERE status = 'dead')::int                      AS dead
FROM scheduled_jobs
WHERE org_id = @org_id;

-- The row is locked so a retry or cancel decides on a state nobody changes meanwhile.
-- name: GetJobForOrg :one
SELECT * FROM scheduled_jobs WHERE id = @id AND org_id = @org_id FOR UPDATE;

-- name: AdminRetryJob :one
UPDATE scheduled_jobs SET
    status           = 'pending',
    attempts         = 0,
    run_at           = @now,
    first_attempt_at = NULL,
    alerted_at       = NULL,
    finished_at      = NULL,
    updated_at       = @now
WHERE id = @id AND org_id = @org_id AND status = 'dead'
RETURNING *;

-- name: AdminCancelJob :one
UPDATE scheduled_jobs SET
    status      = 'cancelled',
    finished_at = @now,
    updated_at  = @now
WHERE id = @id AND org_id = @org_id AND status = 'dead'
RETURNING *;

-- Dead jobs not yet included in an alert. Locked, so a concurrent sweep skips them.
-- name: SelectUnalertedDeadJobs :many
SELECT id, org_id, kind FROM scheduled_jobs
WHERE status = 'dead' AND alerted_at IS NULL
ORDER BY org_id, id
LIMIT 1000
FOR UPDATE SKIP LOCKED;

-- name: MarkJobsAlerted :exec
UPDATE scheduled_jobs SET alerted_at = @now, updated_at = @now
WHERE id = ANY(@ids::uuid[]);

-- Retention (FR-035): finished jobs older than the cutoff; dead jobs are kept until an admin acts.
-- name: PurgeFinishedJobs :execrows
DELETE FROM scheduled_jobs
WHERE id IN (
    SELECT s.id FROM scheduled_jobs s
    WHERE s.org_id = @org_id AND s.status IN ('done', 'cancelled', 'expired') AND s.finished_at < @cutoff
    ORDER BY s.finished_at
    LIMIT 5000
);

-- name: PurgeTickRuns :execrows
DELETE FROM tick_runs WHERE started_at < @cutoff;
