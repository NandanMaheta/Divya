-- +goose Up
-- Auth core (feature 003). Expand-only: adds seven tables, touches nothing existing.
-- Design: specs/003-auth-core/data-model.md. Times are written from the injected clock via
-- @now parameters; the now() defaults below are only a safety net.
-- Requires only 00001 (orgs). Independent of 00002 objects.

CREATE TABLE users (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id                 uuid NOT NULL REFERENCES orgs (id),
    role                   text NOT NULL CHECK (role IN ('admin', 'client')),
    status                 text NOT NULL CHECK (status IN ('pending_verification', 'pending_approval', 'active', 'disabled')),
    status_before_disabled text CHECK (status_before_disabled IN ('pending_verification', 'pending_approval', 'active')),
    name                   text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 200 AND name ~ '\S'),
    email                  text NOT NULL CHECK (email = lower(btrim(email)) AND char_length(email) BETWEEN 3 AND 254 AND position('@' IN email) > 1),
    mobile                 text CHECK (mobile ~ '^\+91[6-9][0-9]{9}$'),
    email_verified_at      timestamptz,
    mobile_verified_at     timestamptz,
    password_hash          text NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
    password_changed_at    timestamptz NOT NULL,
    last_sign_in_at        timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_disabled_state_check CHECK ((status = 'disabled') = (status_before_disabled IS NOT NULL)),
    CONSTRAINT users_client_mobile_check CHECK (role = 'admin' OR mobile IS NOT NULL),
    CONSTRAINT users_mobile_verified_check CHECK (mobile IS NOT NULL OR mobile_verified_at IS NULL)
);
CREATE UNIQUE INDEX users_email_key ON users (email);
CREATE UNIQUE INDEX users_mobile_key ON users (mobile) WHERE mobile IS NOT NULL;
CREATE INDEX users_org_role_status_idx ON users (org_id, role, status);

CREATE TABLE device_sessions (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id              uuid NOT NULL REFERENCES orgs (id),
    user_id             uuid NOT NULL REFERENCES users (id),
    device_id           uuid NOT NULL,
    device_name         text NOT NULL CHECK (char_length(device_name) BETWEEN 1 AND 100),
    platform            text NOT NULL CHECK (platform IN ('android', 'iphone', 'web')),
    client_type         text NOT NULL CHECK (client_type IN ('mobile', 'web')),
    mfa_method          text CHECK (mfa_method IN ('totp', 'recovery_code', 'enrolment')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_active_at      timestamptz NOT NULL DEFAULT now(),
    idle_expires_at     timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    ended_at            timestamptz,
    end_reason          text CHECK (end_reason IN (
        'signed_out', 'signed_out_everywhere', 'revoked_by_user', 'revoked_by_admin', 'ended_by_admin',
        'refresh_reuse', 'device_mismatch', 'password_changed', 'password_reset', 'mfa_replaced',
        'disabled', 'replaced', 'ended_by_feature', 'expired')),
    CONSTRAINT device_sessions_end_state_check CHECK ((ended_at IS NULL) = (end_reason IS NULL))
);
CREATE UNIQUE INDEX device_sessions_live_device_key ON device_sessions (user_id, device_id) WHERE ended_at IS NULL;
CREATE INDEX device_sessions_user_live_idx ON device_sessions (user_id) WHERE ended_at IS NULL;

CREATE TABLE refresh_tokens (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid NOT NULL REFERENCES orgs (id),
    session_id  uuid NOT NULL REFERENCES device_sessions (id),
    token_hash  bytea NOT NULL CHECK (octet_length(token_hash) = 32),
    issued_at   timestamptz NOT NULL DEFAULT now(),
    used_at     timestamptz,
    replaced_by uuid REFERENCES refresh_tokens (id),
    CONSTRAINT refresh_tokens_replaced_check CHECK (replaced_by IS NULL OR used_at IS NOT NULL)
);
CREATE UNIQUE INDEX refresh_tokens_hash_key ON refresh_tokens (token_hash);
CREATE UNIQUE INDEX refresh_tokens_one_live_key ON refresh_tokens (session_id) WHERE used_at IS NULL;
CREATE INDEX refresh_tokens_session_idx ON refresh_tokens (session_id);

CREATE TABLE admin_mfa (
    user_id            uuid PRIMARY KEY REFERENCES users (id),
    org_id             uuid NOT NULL REFERENCES orgs (id),
    secret_enc         bytea,
    pending_secret_enc bytea,
    pending_created_at timestamptz,
    last_used_step     bigint NOT NULL DEFAULT 0,
    enrolled_at        timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT admin_mfa_pending_check CHECK ((pending_secret_enc IS NULL) = (pending_created_at IS NULL)),
    CONSTRAINT admin_mfa_enrolled_check CHECK ((secret_enc IS NULL) = (enrolled_at IS NULL))
);

CREATE TABLE mfa_recovery_codes (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id     uuid NOT NULL REFERENCES orgs (id),
    user_id    uuid NOT NULL REFERENCES users (id),
    batch_id   uuid NOT NULL,
    code_hash  bytea NOT NULL CHECK (octet_length(code_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at    timestamptz,
    revoked_at timestamptz
);
CREATE UNIQUE INDEX mfa_recovery_codes_user_hash_key ON mfa_recovery_codes (user_id, code_hash);
CREATE INDEX mfa_recovery_codes_usable_idx ON mfa_recovery_codes (user_id) WHERE used_at IS NULL AND revoked_at IS NULL;

CREATE TABLE mfa_challenges (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid NOT NULL REFERENCES orgs (id),
    user_id     uuid NOT NULL REFERENCES users (id),
    token_hash  bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    purpose     text NOT NULL CHECK (purpose IN ('verify', 'enrol')),
    device_id   uuid NOT NULL,
    device_name text NOT NULL CHECK (char_length(device_name) BETWEEN 1 AND 100),
    platform    text NOT NULL CHECK (platform IN ('android', 'iphone', 'web')),
    client_type text NOT NULL CHECK (client_type IN ('mobile', 'web')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE INDEX mfa_challenges_expires_idx ON mfa_challenges (expires_at);

-- No FK to users on purpose: unknown identifiers need throttle rows too.
CREATE TABLE auth_throttles (
    org_id       uuid NOT NULL REFERENCES orgs (id),
    scope        text NOT NULL CHECK (scope IN ('signin')),
    subject      bytea NOT NULL CHECK (octet_length(subject) = 32),
    failures     timestamptz[] NOT NULL DEFAULT '{}' CHECK (cardinality(failures) <= 5),
    locked_until timestamptz,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, scope, subject)
);

-- +goose Down
DROP TABLE auth_throttles;
DROP TABLE mfa_challenges;
DROP TABLE mfa_recovery_codes;
DROP TABLE admin_mfa;
DROP TABLE refresh_tokens;
DROP TABLE device_sessions;
DROP TABLE users;
