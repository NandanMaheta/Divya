-- +goose Up
-- Scheduler and outbox (feature 002). Operational queues: updatable and deletable, so no
-- forbid_mutation() trigger. Admin actions on them are recorded in audit_log (append-only).
-- All scheduling instants are written by the application from its injected clock.

CREATE TABLE scheduled_jobs (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id           uuid NOT NULL REFERENCES orgs (id),
    kind             text NOT NULL
        CHECK (kind ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$' AND char_length(kind) <= 64),
    dedupe_key       text NOT NULL CHECK (char_length(dedupe_key) BETWEEN 1 AND 200),
    subject          text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 200),
    payload          jsonb NOT NULL DEFAULT '{}' CHECK (octet_length(payload::text) <= 8192),
    run_at           timestamptz NOT NULL,
    expires_at       timestamptz,
    recurring        boolean NOT NULL DEFAULT false,
    status           text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'done', 'dead', 'cancelled', 'expired')),
    attempts         int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts     int NOT NULL DEFAULT 8 CHECK (max_attempts BETWEEN 1 AND 50),
    first_attempt_at timestamptz,
    last_attempt_at  timestamptz,
    last_error       text,
    locked_until     timestamptz,
    lock_token       uuid,
    locked_by        text,
    rerun            boolean NOT NULL DEFAULT false,
    cancel_requested boolean NOT NULL DEFAULT false,
    alerted_at       timestamptz,
    finished_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT scheduled_jobs_running_lease_ck
        CHECK (status <> 'running' OR (locked_until IS NOT NULL AND lock_token IS NOT NULL)),
    CONSTRAINT scheduled_jobs_finished_ck
        CHECK (status NOT IN ('done', 'cancelled', 'expired', 'dead') OR finished_at IS NOT NULL),
    CONSTRAINT scheduled_jobs_rerun_cancel_ck CHECK (NOT (rerun AND cancel_requested))
);

-- One active job per business key (FR-009); finished rows stay as history.
CREATE UNIQUE INDEX scheduled_jobs_active_key_uq
    ON scheduled_jobs (org_id, kind, dedupe_key) WHERE status IN ('pending', 'running');
CREATE INDEX scheduled_jobs_due_idx ON scheduled_jobs (run_at) WHERE status = 'pending';
CREATE INDEX scheduled_jobs_lease_idx ON scheduled_jobs (locked_until) WHERE status = 'running';
CREATE INDEX scheduled_jobs_expiry_idx ON scheduled_jobs (expires_at)
    WHERE status = 'pending' AND expires_at IS NOT NULL;
CREATE INDEX scheduled_jobs_subject_idx ON scheduled_jobs (org_id, subject)
    WHERE status IN ('pending', 'running');
CREATE INDEX scheduled_jobs_failed_idx ON scheduled_jobs (org_id, last_attempt_at DESC, id DESC)
    WHERE status = 'dead' OR (status = 'pending' AND attempts > 0);
CREATE INDEX scheduled_jobs_unalerted_idx ON scheduled_jobs (org_id)
    WHERE status = 'dead' AND alerted_at IS NULL;
CREATE INDEX scheduled_jobs_retention_idx ON scheduled_jobs (finished_at)
    WHERE status IN ('done', 'cancelled', 'expired');

CREATE TABLE outbox_messages (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id               uuid NOT NULL REFERENCES orgs (id),
    topic                text NOT NULL
        CHECK (topic ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$' AND char_length(topic) <= 64),
    recipient_kind       text NOT NULL CHECK (recipient_kind IN ('client', 'admin')),
    -- No FK yet: the users table arrives with feature 003.
    recipient_id         uuid,
    subject              text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 200),
    dedupe_key           text NOT NULL CHECK (char_length(dedupe_key) BETWEEN 1 AND 200),
    payload              jsonb NOT NULL DEFAULT '{}' CHECK (octet_length(payload::text) <= 8192),
    respects_quiet_hours boolean NOT NULL DEFAULT true,
    available_at         timestamptz NOT NULL,
    expires_at           timestamptz,
    status               text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sending', 'sent', 'dead', 'withdrawn', 'expired')),
    attempts             int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts         int NOT NULL DEFAULT 8 CHECK (max_attempts BETWEEN 1 AND 50),
    first_attempt_at     timestamptz,
    last_attempt_at      timestamptz,
    last_error           text,
    locked_until         timestamptz,
    lock_token           uuid,
    locked_by            text,
    alerted_at           timestamptz,
    sent_at              timestamptz,
    finished_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbox_messages_client_recipient_ck
        CHECK (recipient_kind <> 'client' OR recipient_id IS NOT NULL),
    CONSTRAINT outbox_messages_sending_lease_ck
        CHECK (status <> 'sending' OR (locked_until IS NOT NULL AND lock_token IS NOT NULL)),
    CONSTRAINT outbox_messages_finished_ck
        CHECK (status NOT IN ('sent', 'dead', 'withdrawn', 'expired') OR finished_at IS NOT NULL),
    -- A repeat is ignored for as long as the row is retained, whatever its status (FR-024).
    CONSTRAINT outbox_messages_dedupe_uq UNIQUE (org_id, topic, dedupe_key)
);

CREATE INDEX outbox_messages_due_idx ON outbox_messages (available_at) WHERE status = 'pending';
CREATE INDEX outbox_messages_lease_idx ON outbox_messages (locked_until) WHERE status = 'sending';
CREATE INDEX outbox_messages_expiry_idx ON outbox_messages (expires_at)
    WHERE status = 'pending' AND expires_at IS NOT NULL;
CREATE INDEX outbox_messages_subject_idx ON outbox_messages (org_id, subject) WHERE status = 'pending';
CREATE INDEX outbox_messages_failed_idx ON outbox_messages (org_id, last_attempt_at DESC, id DESC)
    WHERE status = 'dead' OR (status = 'pending' AND attempts > 0);
CREATE INDEX outbox_messages_unalerted_idx ON outbox_messages (org_id)
    WHERE status = 'dead' AND alerted_at IS NULL;
CREATE INDEX outbox_messages_retention_idx ON outbox_messages (finished_at)
    WHERE status IN ('sent', 'withdrawn', 'expired');

-- One row per org; a missing row means the default window 21:30-08:00 IST.
CREATE TABLE quiet_hours (
    org_id       uuid PRIMARY KEY REFERENCES orgs (id),
    start_minute smallint NOT NULL DEFAULT 1290 CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute   smallint NOT NULL DEFAULT 480 CHECK (end_minute BETWEEN 0 AND 1439),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quiet_hours_window_ck CHECK (start_minute <> end_minute)
);

-- One row per tick run. No org_id: a tick serves every organisation and holds no business data.
CREATE TABLE tick_runs (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    started_at       timestamptz NOT NULL,
    finished_at      timestamptz,
    status           text NOT NULL CHECK (status IN ('running', 'ok', 'error')),
    instance         text NOT NULL,
    jobs_run         int NOT NULL DEFAULT 0 CHECK (jobs_run >= 0),
    jobs_failed      int NOT NULL DEFAULT 0 CHECK (jobs_failed >= 0),
    jobs_dead        int NOT NULL DEFAULT 0 CHECK (jobs_dead >= 0),
    jobs_expired     int NOT NULL DEFAULT 0 CHECK (jobs_expired >= 0),
    msgs_sent        int NOT NULL DEFAULT 0 CHECK (msgs_sent >= 0),
    msgs_failed      int NOT NULL DEFAULT 0 CHECK (msgs_failed >= 0),
    msgs_dead        int NOT NULL DEFAULT 0 CHECK (msgs_dead >= 0),
    msgs_expired     int NOT NULL DEFAULT 0 CHECK (msgs_expired >= 0),
    budget_exhausted boolean NOT NULL DEFAULT false
);
CREATE INDEX tick_runs_started_idx ON tick_runs (started_at DESC);

-- +goose Down
DROP TABLE tick_runs;
DROP TABLE quiet_hours;
DROP TABLE outbox_messages;
DROP TABLE scheduled_jobs;
