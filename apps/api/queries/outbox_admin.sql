-- Outbox admin and retention queries (feature 002).

-- Retrying and dead messages of one org, newest last attempt first, keyset-paginated (FR-019).
-- state: empty for both, retrying or dead; topic: empty for every topic.
-- name: ListFailedMessages :many
SELECT * FROM outbox_messages
WHERE org_id = @org_id
  AND (status = 'dead' OR (status = 'pending' AND attempts > 0))
  AND (@state::text = '' OR (@state::text = 'dead' AND status = 'dead') OR (@state::text = 'retrying' AND status = 'pending'))
  AND (@topic::text = '' OR topic = @topic::text)
  AND (
      sqlc.narg('cursor_at')::timestamptz IS NULL
      OR (last_attempt_at, id) < (sqlc.narg('cursor_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY last_attempt_at DESC, id DESC
LIMIT @row_limit;

-- name: CountMessagesByState :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::int                   AS pending,
    count(*) FILTER (WHERE status = 'pending' AND attempts > 0)::int  AS retrying,
    count(*) FILTER (WHERE status = 'sending')::int                   AS sending,
    count(*) FILTER (WHERE status = 'dead')::int                      AS dead
FROM outbox_messages
WHERE org_id = @org_id;

-- The row is locked so a retry or cancel decides on a state nobody changes meanwhile.
-- name: GetMessageForOrg :one
SELECT * FROM outbox_messages WHERE id = @id AND org_id = @org_id FOR UPDATE;

-- Retry: fresh attempts; available_at is computed by the caller so quiet hours still apply.
-- name: AdminRetryMessage :one
UPDATE outbox_messages SET
    status           = 'pending',
    attempts         = 0,
    available_at     = @available_at,
    first_attempt_at = NULL,
    alerted_at       = NULL,
    finished_at      = NULL,
    updated_at       = @now
WHERE id = @id AND org_id = @org_id AND status = 'dead'
RETURNING *;

-- Cancel keeps the dedupe key reserved: the row stays, as withdrawn.
-- name: AdminCancelMessage :one
UPDATE outbox_messages SET
    status      = 'withdrawn',
    finished_at = @now,
    updated_at  = @now
WHERE id = @id AND org_id = @org_id AND status = 'dead'
RETURNING *;

-- Dead messages not yet included in an alert. Locked, so a concurrent sweep skips them.
-- name: SelectUnalertedDeadMessages :many
SELECT id, org_id, topic FROM outbox_messages
WHERE status = 'dead' AND alerted_at IS NULL
ORDER BY org_id, id
LIMIT 1000
FOR UPDATE SKIP LOCKED;

-- name: MarkMessagesAlerted :exec
UPDATE outbox_messages SET alerted_at = @now, updated_at = @now
WHERE id = ANY(@ids::uuid[]);

-- Retention (FR-035): finished messages older than the cutoff; dead messages are kept until an admin acts.
-- name: PurgeFinishedMessages :execrows
DELETE FROM outbox_messages
WHERE id IN (
    SELECT m.id FROM outbox_messages m
    WHERE m.org_id = @org_id AND m.status IN ('sent', 'withdrawn', 'expired') AND m.finished_at < @cutoff
    ORDER BY m.finished_at
    LIMIT 5000
);
