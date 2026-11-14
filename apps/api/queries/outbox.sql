-- Outbox queries (feature 002). Every instant is a parameter taken from the injected clock.

-- Add: a repeated (org, topic, dedupe_key) is ignored whatever the first row's status (FR-024).
-- Zero rows affected means duplicate.
-- name: InsertMessage :execrows
INSERT INTO outbox_messages (
    org_id, topic, recipient_kind, recipient_id, subject, dedupe_key, payload,
    respects_quiet_hours, available_at, expires_at, max_attempts, created_at, updated_at
) VALUES (
    @org_id, @topic, @recipient_kind, sqlc.narg('recipient_id'), @subject, @dedupe_key, @payload,
    @respects_quiet_hours, @available_at, sqlc.narg('expires_at'), @max_attempts, @now, @now
)
ON CONFLICT (org_id, topic, dedupe_key) DO NOTHING;

-- Only pending messages are withdrawn; a message being sent is at-least-once and not stopped.
-- name: WithdrawByKey :execrows
UPDATE outbox_messages SET status = 'withdrawn', finished_at = @now, updated_at = @now
WHERE org_id = @org_id AND topic = @topic AND dedupe_key = @dedupe_key AND status = 'pending';

-- name: WithdrawBySubject :execrows
UPDATE outbox_messages SET status = 'withdrawn', finished_at = @now, updated_at = @now
WHERE org_id = @org_id AND subject = @subject AND status = 'pending';

-- Expiry sweep: a message past its expiry is never handed to a sender (FR-028). Bounded per tick.
-- name: ExpireDueMessages :execrows
WITH due AS (
    SELECT id FROM outbox_messages
    WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at <= @now
    ORDER BY expires_at, id
    LIMIT 1000
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_messages m SET status = 'expired', finished_at = @now, updated_at = @now
FROM due WHERE m.id = due.id;

-- Claim one available message (or one whose send lease ran out), oldest id first, so messages
-- of one recipient are relayed in the order they were recorded (FR-030).
-- name: ClaimMessage :one
WITH c AS (
    SELECT id FROM outbox_messages
    WHERE (status = 'pending' AND available_at <= @now AND (expires_at IS NULL OR expires_at > @now))
       OR (status = 'sending' AND locked_until <= @now)
    ORDER BY id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_messages m SET
    status           = 'sending',
    attempts         = m.attempts + 1,
    first_attempt_at = COALESCE(m.first_attempt_at, @now),
    last_attempt_at  = @now,
    locked_until     = @locked_until,
    lock_token       = gen_random_uuid(),
    locked_by        = @locked_by,
    updated_at       = @now
FROM c WHERE m.id = c.id
RETURNING m.*;

-- Fenced by the lock token: zero rows means another claimant took over and this mark must not win.
-- name: MarkMessageSent :execrows
UPDATE outbox_messages SET
    status       = 'sent',
    sent_at      = @now,
    finished_at  = @now,
    locked_until = NULL,
    lock_token   = NULL,
    locked_by    = NULL,
    updated_at   = @now
WHERE id = @id AND lock_token = @lock_token::uuid AND status = 'sending';

-- Back to pending at available_at (already moved out of quiet hours), or dead when attempts are used up.
-- name: FailMessage :one
UPDATE outbox_messages SET
    status       = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'pending' END,
    available_at = CASE WHEN attempts >= max_attempts THEN available_at ELSE @available_at::timestamptz END,
    finished_at  = CASE WHEN attempts >= max_attempts THEN @now::timestamptz ELSE NULL END,
    last_error   = @last_error::text,
    locked_until = NULL,
    lock_token   = NULL,
    locked_by    = NULL,
    updated_at   = @now
WHERE id = @id AND lock_token = @lock_token::uuid AND status = 'sending'
RETURNING status, attempts;

-- A message whose lease expired more often than it has attempts left is dead without being sent again.
-- name: MarkMessageDead :execrows
UPDATE outbox_messages SET
    status       = 'dead',
    finished_at  = @now,
    last_error   = @last_error::text,
    locked_until = NULL,
    lock_token   = NULL,
    locked_by    = NULL,
    updated_at   = @now
WHERE id = @id AND lock_token = @lock_token::uuid AND status = 'sending';

-- No row means the default window (21:30-08:00 IST).
-- name: GetQuietHours :one
SELECT start_minute, end_minute FROM quiet_hours WHERE org_id = @org_id;

-- name: UpsertQuietHours :exec
INSERT INTO quiet_hours (org_id, start_minute, end_minute, updated_at)
VALUES (@org_id, @start_minute, @end_minute, @now)
ON CONFLICT (org_id) DO UPDATE SET
    start_minute = EXCLUDED.start_minute,
    end_minute   = EXCLUDED.end_minute,
    updated_at   = EXCLUDED.updated_at;
