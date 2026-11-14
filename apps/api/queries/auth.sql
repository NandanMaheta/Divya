-- Auth core queries (feature 003). Design: specs/003-auth-core/data-model.md.
-- Every instant is a parameter (@now) taken from the injected clock; no behavioural query reads
-- the database clock, and there is no string-built SQL. Lock order: users, device_sessions,
-- refresh_tokens, admin_mfa, mfa_recovery_codes, mfa_challenges, auth_throttles.

-- ---- organisation ----

-- Two rows are enough to tell "none", "one" and "many" apart.
-- name: ListFirstTwoOrgIDs :many
SELECT id FROM orgs ORDER BY id LIMIT 2;

-- ---- users ----

-- name: InsertUser :one
INSERT INTO users (
    org_id, role, status, name, email, mobile, email_verified_at,
    password_hash, password_changed_at, created_at, updated_at
) VALUES (
    @org_id, @role, @status, @name, @email, sqlc.narg('mobile'), sqlc.narg('email_verified_at'),
    @password_hash, @now, @now, @now
)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = @email;

-- name: GetUserByMobile :one
SELECT * FROM users WHERE mobile = @mobile;

-- name: LockUserByID :one
SELECT * FROM users WHERE id = @id FOR UPDATE;

-- Every admin of the org, any status, ordered by id: the last-admin guard (lock order step 1).
-- name: LockAdminsByOrg :many
SELECT * FROM users WHERE org_id = @org_id AND role = 'admin' ORDER BY id FOR UPDATE;

-- name: SetUserStatus :execrows
UPDATE users
SET status = @status, status_before_disabled = sqlc.narg('status_before_disabled'), updated_at = @now
WHERE id = @id;

-- The first stamp is kept: an already verified contact changes nothing (zero rows).
-- name: MarkEmailVerified :execrows
UPDATE users SET email_verified_at = @now, updated_at = @now
WHERE id = @id AND email_verified_at IS NULL;

-- name: MarkMobileVerified :execrows
UPDATE users SET mobile_verified_at = @now, updated_at = @now
WHERE id = @id AND mobile IS NOT NULL AND mobile_verified_at IS NULL;

-- name: ReplaceUserEmail :execrows
UPDATE users SET email = @email, updated_at = @now WHERE id = @id;

-- name: ReplaceUserMobile :execrows
UPDATE users SET mobile = @mobile, updated_at = @now WHERE id = @id;

-- name: SetUserPassword :execrows
UPDATE users SET password_hash = @password_hash, password_changed_at = @now, updated_at = @now
WHERE id = @id;

-- Compare and set: a rehash never overwrites a password changed in the meantime and is not a
-- password change (password_changed_at stays).
-- name: RehashUserPassword :execrows
UPDATE users SET password_hash = @new_hash, updated_at = @now
WHERE id = @id AND password_hash = @old_hash;

-- name: SetUserLastSignIn :execrows
UPDATE users SET last_sign_in_at = @now, updated_at = @now WHERE id = @id;

-- ---- device sessions ----

-- name: InsertSession :one
INSERT INTO device_sessions (
    org_id, user_id, device_id, device_name, platform, client_type, mfa_method,
    created_at, last_active_at, idle_expires_at, absolute_expires_at
) VALUES (
    @org_id, @user_id, @device_id, @device_name, @platform, @client_type, sqlc.narg('mfa_method'),
    @now, @now, @idle_expires_at, @absolute_expires_at
)
RETURNING *;

-- name: GetSessionByID :one
SELECT * FROM device_sessions WHERE id = @id;

-- name: LockSessionByID :one
SELECT * FROM device_sessions WHERE id = @id FOR UPDATE;

-- The per-request principal query (plan P-02): raw columns, the caller decides liveness.
-- name: GetSessionPrincipal :one
SELECT s.id AS session_id, s.user_id, s.org_id, s.ended_at, s.idle_expires_at, s.absolute_expires_at,
       u.role, u.status
FROM device_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id = @session_id;

-- Live: not ended and strictly before both expiry instants.
-- name: ListLiveSessionsByUser :many
SELECT * FROM device_sessions
WHERE user_id = @user_id
  AND ended_at IS NULL
  AND idle_expires_at > @now
  AND absolute_expires_at > @now
ORDER BY last_active_at DESC, id;

-- name: TouchSession :execrows
UPDATE device_sessions SET last_active_at = @now, idle_expires_at = @idle_expires_at
WHERE id = @id;

-- The first end stands: an already ended session is left alone (zero rows).
-- name: EndSession :execrows
UPDATE device_sessions SET ended_at = @now, end_reason = @end_reason
WHERE id = @id AND ended_at IS NULL;

-- An all-zero except id matches no session, so it ends every not-ended session of the user.
-- name: EndUserSessions :many
UPDATE device_sessions SET ended_at = @now, end_reason = @end_reason
WHERE user_id = @user_id AND ended_at IS NULL AND id <> @except_session_id
RETURNING id;

-- ---- refresh tokens ----

-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (org_id, session_id, token_hash, issued_at)
VALUES (@org_id, @session_id, @token_hash, @now)
RETURNING *;

-- name: GetRefreshTokenByID :one
SELECT * FROM refresh_tokens WHERE id = @id;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens WHERE token_hash = @token_hash;

-- name: LockRefreshTokenByID :one
SELECT * FROM refresh_tokens WHERE id = @id FOR UPDATE;

-- Rotation step 1: only an unused token can be marked used (no rows: unknown or already used).
-- name: MarkRefreshTokenUsed :one
UPDATE refresh_tokens SET used_at = @now
WHERE id = @id AND used_at IS NULL
RETURNING org_id, session_id;

-- Rotation step 3: link the used token to its successor.
-- name: SetRefreshTokenReplacedBy :execrows
UPDATE refresh_tokens SET replaced_by = @replaced_by WHERE id = @id;

-- ---- admin MFA ----

-- name: GetAdminMFA :one
SELECT * FROM admin_mfa WHERE user_id = @user_id;

-- name: LockAdminMFA :one
SELECT * FROM admin_mfa WHERE user_id = @user_id FOR UPDATE;

-- Creates the row or overwrites the pending secret; a confirmed secret is never touched.
-- name: UpsertPendingMFASecret :exec
INSERT INTO admin_mfa (user_id, org_id, pending_secret_enc, pending_created_at, updated_at)
VALUES (@user_id, @org_id, @pending_secret_enc, @now, @now)
ON CONFLICT (user_id) DO UPDATE
SET pending_secret_enc = EXCLUDED.pending_secret_enc,
    pending_created_at = EXCLUDED.pending_created_at,
    updated_at = EXCLUDED.updated_at;

-- name: ConfirmPendingMFASecret :execrows
UPDATE admin_mfa
SET secret_enc = pending_secret_enc, pending_secret_enc = NULL, pending_created_at = NULL,
    enrolled_at = @now, last_used_step = @step, updated_at = @now
WHERE user_id = @user_id AND pending_secret_enc IS NOT NULL;

-- Replay guard: only a strictly greater step is accepted, atomically.
-- name: AdvanceMFAStep :execrows
UPDATE admin_mfa SET last_used_step = @step, updated_at = @now
WHERE user_id = @user_id AND last_used_step < @step;

-- ---- recovery codes ----

-- Revokes every unrevoked code and reports how many of them were still usable.
-- name: RevokeRecoveryCodes :one
WITH revoked AS (
    UPDATE mfa_recovery_codes SET revoked_at = @now
    WHERE user_id = @user_id AND revoked_at IS NULL
    RETURNING used_at
)
SELECT (count(*) FILTER (WHERE used_at IS NULL))::int AS usable FROM revoked;

-- name: InsertRecoveryCodes :execrows
INSERT INTO mfa_recovery_codes (org_id, user_id, batch_id, code_hash, created_at)
SELECT @org_id, @user_id, @batch_id, h, @now FROM unnest(@code_hashes::bytea[]) AS h;

-- name: UseRecoveryCode :execrows
UPDATE mfa_recovery_codes SET used_at = @now
WHERE user_id = @user_id AND code_hash = @code_hash AND used_at IS NULL AND revoked_at IS NULL;

-- name: CountUsableRecoveryCodes :one
SELECT count(*)::int AS usable FROM mfa_recovery_codes
WHERE user_id = @user_id AND used_at IS NULL AND revoked_at IS NULL;

-- ---- MFA challenges ----

-- name: InsertChallenge :one
INSERT INTO mfa_challenges (
    org_id, user_id, token_hash, purpose, device_id, device_name, platform, client_type,
    created_at, expires_at
) VALUES (
    @org_id, @user_id, @token_hash, @purpose, @device_id, @device_name, @platform, @client_type,
    @now, @expires_at
)
RETURNING *;

-- name: GetChallengeByHash :one
SELECT * FROM mfa_challenges WHERE token_hash = @token_hash;

-- Single use and valid iff now < expires_at: exactly one concurrent caller gets a row.
-- name: ConsumeChallenge :execrows
UPDATE mfa_challenges SET consumed_at = @now
WHERE id = @id AND consumed_at IS NULL AND @now < expires_at;

-- ---- throttles ----

-- name: GetThrottle :one
SELECT failures, locked_until FROM auth_throttles
WHERE org_id = @org_id AND scope = @scope AND subject = @subject;

-- name: EnsureThrottle :exec
INSERT INTO auth_throttles (org_id, scope, subject, updated_at)
VALUES (@org_id, @scope, @subject, @now)
ON CONFLICT (org_id, scope, subject) DO NOTHING;

-- name: LockThrottle :one
SELECT failures, locked_until FROM auth_throttles
WHERE org_id = @org_id AND scope = @scope AND subject = @subject
FOR UPDATE;

-- name: UpsertThrottle :exec
INSERT INTO auth_throttles (org_id, scope, subject, failures, locked_until, updated_at)
VALUES (@org_id, @scope, @subject, @failures::timestamptz[], sqlc.narg('locked_until'), @now)
ON CONFLICT (org_id, scope, subject) DO UPDATE
SET failures = EXCLUDED.failures, locked_until = EXCLUDED.locked_until, updated_at = EXCLUDED.updated_at;

-- name: DeleteThrottle :exec
DELETE FROM auth_throttles WHERE org_id = @org_id AND scope = @scope AND subject = @subject;

-- ---- housekeeping (plan P-20): one single-table DELETE at a time, bounded ----

-- name: PurgeStaleThrottles :execrows
DELETE FROM auth_throttles
WHERE (org_id, scope, subject) IN (
    SELECT t.org_id, t.scope, t.subject FROM auth_throttles t
    WHERE t.org_id = @org_id
      AND t.updated_at + interval '15 minutes' <= @now
      AND (t.locked_until IS NULL OR t.locked_until <= @now)
    ORDER BY t.updated_at
    LIMIT 1000
);

-- name: PurgeExpiredChallenges :execrows
DELETE FROM mfa_challenges
WHERE id IN (
    SELECT c.id FROM mfa_challenges c
    WHERE c.org_id = @org_id AND c.expires_at <= @now
    ORDER BY c.expires_at, c.id
    LIMIT 1000
);

-- The cap counts whole families (sessions), never half a family: replaced_by links tokens of
-- one session, so deleting a family completely cannot break the self-referencing foreign key.
-- device_sessions rows themselves are kept (device history).
-- name: PurgeDeadFamilyTokens :execrows
WITH dead AS (
    SELECT s.id FROM device_sessions s
    WHERE s.org_id = @org_id
      AND LEAST(s.ended_at, s.absolute_expires_at) + interval '30 days' <= @now
      AND s.id IN (SELECT DISTINCT r.session_id FROM refresh_tokens r)
    ORDER BY s.id
    LIMIT 1000
)
DELETE FROM refresh_tokens WHERE session_id IN (SELECT id FROM dead);
