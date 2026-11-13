# Data model: 002 Scheduler tick + scheduled jobs + outbox

Migration: `apps/api/migrations/00002_scheduler_outbox.sql` (goose, expand-only: four new tables, no change to existing tables). Queries: `apps/api/queries/scheduler.sql`, `apps/api/queries/outbox.sql`, additions to `apps/api/queries/platform.sql`. Generated code: `internal/platform/db/dbgen` (existing sqlc package).

Conventions (`.claude/rules/sql.md`): `uuidv7()` primary keys, `timestamptz` (UTC) for instants, CHECK constraints for every state, FK to `orgs(id)`, `org_id` on every row that belongs to an organisation. All timestamps that drive scheduling (`run_at`, `available_at`, `locked_until`, `expires_at`, `*_attempt_at`, `finished_at`, `sent_at`) are written from the injected Go clock (passed as query parameters); DB defaults exist only as a safety net for `created_at`/`updated_at` (FR-032).

None of these tables is append-only: they are operational queues (owner decision, spec Clarifications). They get no `forbid_mutation()` trigger. Admin actions on them write `audit_log` (existing, append-only).

## 1. `scheduled_jobs` (owned by `internal/scheduler`)

| Column | Type | Null | Default | Notes |
|---|---|---|---|---|
| `id` | uuid | no | `uuidv7()` | PK |
| `org_id` | uuid | no | | FK `orgs(id)` (FR-034) |
| `kind` | text | no | | registered handler name, `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`, ≤ 64 chars (e.g. `ipo.open`) |
| `dedupe_key` | text | no | | business key, 1–200 chars, stable across reschedules (e.g. `ipo:<uuid>:open`); must not contain the date |
| `subject` | text | no | | business item the job concerns, 1–200 chars (e.g. `ipo:<uuid>`); used for "cancel all for item" (FR-013) and shown in the admin list (US6.4) |
| `payload` | jsonb | no | `'{}'` | ids only (FR-012: handlers read current data); `octet_length(payload::text) <= 8192` |
| `run_at` | timestamptz | no | | due time; for pending rows also "next attempt" |
| `expires_at` | timestamptz | yes | | time-sensitive work: if `expires_at <= now` when it would run, it becomes `expired` (FR-014) |
| `recurring` | boolean | no | false | recurring row is re-armed instead of finished (FR-015) |
| `status` | text | no | `'pending'` | CHECK in (`pending`,`running`,`done`,`dead`,`cancelled`,`expired`) |
| `attempts` | int | no | 0 | CHECK `>= 0`; incremented on claim |
| `max_attempts` | int | no | 8 | CHECK `between 1 and 50` |
| `first_attempt_at` | timestamptz | yes | | FR-018 |
| `last_attempt_at` | timestamptz | yes | | FR-018 |
| `last_error` | text | yes | | sanitised and truncated to 500 chars (FR-018) |
| `locked_until` | timestamptz | yes | | lease end (FR-011) |
| `lock_token` | uuid | yes | | fencing token set per claim |
| `locked_by` | text | yes | | instance/hostname, diagnostics only |
| `rerun` | boolean | no | false | set when `Schedule` hits a `running` row: the row goes back to `pending` at the new `run_at` when the run finishes (US5.5) |
| `cancel_requested` | boolean | no | false | set when `Cancel` hits a `running` row: a failing run becomes `cancelled` instead of retrying |
| `alerted_at` | timestamptz | yes | | when this dead row was included in an `admin.jobs_dead` alert; NULL = not yet alerted |
| `finished_at` | timestamptz | yes | | set on `done`/`cancelled`/`expired`/`dead` |
| `created_at` | timestamptz | no | `now()` | |
| `updated_at` | timestamptz | no | `now()` | |

Constraints:

- `CHECK (status <> 'running' OR (locked_until IS NOT NULL AND lock_token IS NOT NULL))`
- `CHECK (status NOT IN ('done','cancelled','expired','dead') OR finished_at IS NOT NULL)`
- `CHECK (NOT (rerun AND cancel_requested))`

Indexes:

| Name | Definition | Purpose |
|---|---|---|
| `scheduled_jobs_active_key_uq` | UNIQUE `(org_id, kind, dedupe_key) WHERE status IN ('pending','running')` | one active job per key (FR-009); `ON CONFLICT` target for `Schedule` |
| `scheduled_jobs_due_idx` | `(run_at) WHERE status = 'pending'` | claim (FR-010, FR-016) |
| `scheduled_jobs_lease_idx` | `(locked_until) WHERE status = 'running'` | reclaim after lease expiry (FR-011) |
| `scheduled_jobs_expiry_idx` | `(expires_at) WHERE status = 'pending' AND expires_at IS NOT NULL` | expiry sweep (FR-014) |
| `scheduled_jobs_subject_idx` | `(org_id, subject) WHERE status IN ('pending','running')` | cancel/reschedule by item (FR-013) |
| `scheduled_jobs_failed_idx` | `(org_id, last_attempt_at DESC, id DESC) WHERE status = 'dead' OR (status = 'pending' AND attempts > 0)` | admin list (FR-019) |
| `scheduled_jobs_unalerted_idx` | `(org_id) WHERE status = 'dead' AND alerted_at IS NULL` | dead-alert sweep |
| `scheduled_jobs_retention_idx` | `(finished_at) WHERE status IN ('done','cancelled','expired')` | retention purge (FR-035) |

State machine (derived state "retrying" = `pending AND attempts > 0`):

```
             Schedule (upsert)                  claim (run_at<=now, SKIP LOCKED)
   (none) ───────────────────► pending ───────────────────────────────► running
                                 ▲  │ expires_at<=now (sweep)            │
                                 │  └──────────────► expired             │ handler ok, token matches:
                                 │                                       │   recurring  → pending (run_at = next(now), attempts=0)
                                 │                                       │   rerun      → pending (run_at as rescheduled, attempts=0)
                                 │                                       │   otherwise  → done
                                 │   handler error / panic / timeout /   │
                                 │   unknown kind, attempts < max:       │
                                 └───────────── pending (run_at = now + backoff) ◄┘
                                     attempts >= max  → dead
                                     cancel_requested → cancelled
   running with locked_until <= now ──► reclaimed by claim (attempts+1; if attempts > max → dead "lease expired")
   pending ── Cancel/CancelSubject ──► cancelled          running ── Cancel ──► cancel_requested = true
   dead ── admin retry ──► pending (attempts=0, run_at=now, alerted_at=NULL, first_attempt_at=NULL) + audit_log
   dead ── admin cancel ──► cancelled + audit_log
```

Rules:

- **Schedule** = `INSERT … ON CONFLICT (org_id, kind, dedupe_key) WHERE status IN ('pending','running') DO UPDATE SET run_at, expires_at, payload, subject, rerun = (status = 'running'), cancel_requested = false, updated_at`. Pending rows simply take the latest values (FR-009, US1.9). A `done` row for the same key is outside the partial index, so a new pending row is created and the old one stays as history (US5.6). Must be called with a transaction in the context (FR-008, US1.10).
- **Reschedule** updates `run_at` (and optionally `expires_at`) of the active row for a key; never creates (US5.7). On a running row it sets `rerun = true`.
- **Cancel / CancelSubject**: pending → `cancelled`; running → `cancel_requested = true` (the in-flight run, which reads current data, decides its own no-op). Returns the number of rows affected; zero is not an error (US5.7).
- **Claim** (one row per call): `WITH c AS (SELECT id FROM scheduled_jobs WHERE (status='pending' AND run_at <= $now) OR (status='running' AND locked_until <= $now) ORDER BY run_at, id LIMIT 1 FOR UPDATE SKIP LOCKED) UPDATE … SET status='running', attempts=attempts+1, first_attempt_at=COALESCE(first_attempt_at,$now), last_attempt_at=$now, locked_until=$now+$lease, lock_token=gen_random_uuid(), locked_by=$instance FROM c WHERE … RETURNING *`.
- **Complete** runs inside the handler's transaction: `UPDATE … WHERE id=$id AND lock_token=$token AND status='running'`; zero rows means the lease was lost, and the whole transaction (handler effects included) is rolled back (US1.5, US1.6).
- **Fail** (own short transaction, same fencing): `attempts < max_attempts` → `pending`, `run_at = now + backoff(attempts)`; else `dead`; `cancel_requested` → `cancelled`. `last_error` = sanitised text.
- **Expire sweep** (start of jobs phase): `UPDATE … SET status='expired', finished_at=$now WHERE status='pending' AND expires_at <= $now` (bounded to 1,000 rows per tick).
- Business state changes caused by a job write `audit_log` in the owning feature's handler, not here.

## 2. `outbox_messages` (owned by `internal/outbox`)

| Column | Type | Null | Default | Notes |
|---|---|---|---|---|
| `id` | uuid | no | `uuidv7()` | PK; time-ordered, used for relay order (FR-030) |
| `org_id` | uuid | no | | FK `orgs(id)` |
| `topic` | text | no | | `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`, ≤ 64 chars (e.g. `admin.jobs_dead`, `task.reminder`) |
| `recipient_kind` | text | no | | CHECK in (`client`,`admin`) |
| `recipient_id` | uuid | yes | | user id; NULL with `admin` = every admin of the org. CHECK `recipient_kind <> 'client' OR recipient_id IS NOT NULL`. No FK yet (users table arrives with 003; a later migration may add it) |
| `subject` | text | no | | business item (e.g. `task:<uuid>`), for withdraw-by-item (FR-027) and admin list |
| `dedupe_key` | text | no | | 1–200 chars; passed to the sender (FR-026) |
| `payload` | jsonb | no | `'{}'` | content reference (template key + ids); never PAN, tokens or secrets; ≤ 8 KB |
| `respects_quiet_hours` | boolean | no | true | false only when the owning feature marks the message exempt |
| `available_at` | timestamptz | no | | computed on record and on retry: never inside quiet hours when `respects_quiet_hours` (FR-031, SC-008) |
| `expires_at` | timestamptz | yes | | FR-028 |
| `status` | text | no | `'pending'` | CHECK in (`pending`,`sending`,`sent`,`dead`,`withdrawn`,`expired`) |
| `attempts`, `max_attempts`, `first_attempt_at`, `last_attempt_at`, `last_error`, `locked_until`, `lock_token`, `locked_by`, `alerted_at` | | | | same meaning as in `scheduled_jobs` |
| `sent_at` | timestamptz | yes | | FR-025 |
| `finished_at` | timestamptz | yes | | set on `sent`/`dead`/`withdrawn`/`expired` |
| `created_at`, `updated_at` | timestamptz | no | `now()` | |

Constraints: `UNIQUE (org_id, topic, dedupe_key)` across all statuses (FR-024: a repeat is ignored for as long as the row is retained); same lease and finished checks as jobs.

Indexes: `(available_at) WHERE status='pending'` (relay claim); `(locked_until) WHERE status='sending'`; `(expires_at) WHERE status='pending' AND expires_at IS NOT NULL`; `(org_id, subject) WHERE status='pending'`; `(org_id, last_attempt_at DESC, id DESC) WHERE status='dead' OR (status='pending' AND attempts>0)`; `(org_id) WHERE status='dead' AND alerted_at IS NULL`; `(finished_at) WHERE status IN ('sent','withdrawn','expired')`.

State machine (derived "retrying" = `pending AND attempts > 0`):

```
 Add (INSERT … ON CONFLICT (org_id, topic, dedupe_key) DO NOTHING) ──► pending (available_at = quietDefer(now))
 pending ── expires_at <= now (sweep) ──► expired
 pending ── Withdraw / WithdrawSubject ──► withdrawn                  (sending rows are not withdrawn: at-least-once)
 pending ── claim (available_at <= now, ORDER BY id, SKIP LOCKED) ──► sending (lease, token)
 sending ── Sender.Send ok, token matches ──► sent (sent_at = now)
 sending ── error / no sender / timeout ──► pending (available_at = quietDefer(now + backoff)) | dead (attempts >= max)
 sending with locked_until <= now ──► reclaimed (at-least-once re-send with the same dedupe_key, US3.4)
 dead ── admin retry ──► pending (attempts=0, available_at=quietDefer(now)) + audit_log
 dead ── admin cancel ──► withdrawn + audit_log (action `outbox_message.cancel`)
```

The send happens outside any transaction (the claim commits first, then the sender is called, then `MarkSent` commits with the token check).

## 3. `quiet_hours` (owned by `internal/outbox`)

| Column | Type | Null | Default | Notes |
|---|---|---|---|---|
| `org_id` | uuid | no | | PK, FK `orgs(id)` |
| `start_minute` | smallint | no | 1290 | minutes after 00:00 IST (21:30); CHECK `between 0 and 1439` |
| `end_minute` | smallint | no | 480 | 08:00; CHECK `between 0 and 1439`; CHECK `start_minute <> end_minute` |
| `updated_at` | timestamptz | no | `now()` | |

A missing row means the default 21:30–08:00 IST (no seeding needed). Window is `[start, end)` in IST, wrapping midnight when `start > end`; applies every day (D3). Changes go through `outbox.Service.SetQuietHours` (writes `audit_log`, action `quiet_hours.update`); HTTP/UI for it is feature 023. Already-deferred messages keep their `available_at` (US8.8).

## 4. `tick_runs` (owned by `internal/scheduler`)

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | uuid | no | PK `uuidv7()` |
| `started_at` | timestamptz | no | from the clock |
| `finished_at` | timestamptz | yes | |
| `status` | text | no | CHECK in (`running`,`ok`,`error`) |
| `instance` | text | no | hostname, diagnostics |
| `jobs_run`, `jobs_failed`, `jobs_dead`, `jobs_expired`, `msgs_sent`, `msgs_failed`, `msgs_dead`, `msgs_expired` | int | no | default 0, CHECK `>= 0` (FR-006) |
| `budget_exhausted` | boolean | no | default false |

Index: `(started_at DESC)`. No `org_id`: a tick serves every organisation and holds no business data (see plan Complexity Tracking). "Last tick" = max `started_at`; overdue when `now - started_at > 15 min` or no row exists (FR-021). Purged after 30 days.

## 5. `audit_log` usage (existing table, 001)

| Action | entity | entity_id | before / after (jsonb) |
|---|---|---|---|
| `scheduled_job.retry` | `scheduled_job` | job id | `{status, attempts, run_at, last_error}` → `{status, attempts, run_at, idempotency_key?}` |
| `scheduled_job.cancel` | `scheduled_job` | job id | same shape |
| `outbox_message.retry` / `outbox_message.cancel` | `outbox_message` | message id | `{status, attempts, available_at, last_error}` → … |
| `quiet_hours.update` | `quiet_hours` | org id | `{start_minute, end_minute}` → … |

`actor_kind = 'admin'`, `actor_id` = admin user id (from 003's principal once the HTTP layer exists; passed explicitly to the service now). New query in `queries/platform.sql`: `InsertAuditLog :exec`, and (Phase B) `FindAuditByIdempotencyKey :one` on `(org_id, entity, entity_id)` filtered by `after->>'idempotency_key'`.

## 6. Go value types (not tables)

- `istdate.Date{Year, Month, Day}`: civil business date in IST; `istdate.WallClock{Hour, Minute}`; `istdate.QuietHours{Start, End WallClock}`.
- `scheduler.JobSpec`, `scheduler.Job`, `outbox.Message`, `outbox.Envelope` (what a sender receives): see `contracts/go-module-api.md`.

## 7. Operational settings (code constants in `scheduler.Options` / `outbox.Options`, not env)

| Setting | Value | Why |
|---|---|---|
| Lease (jobs and messages) | 60 s | > job timeout 10 s / send timeout 5 s, small enough that a crashed tick's work is retried on the next tick |
| Job handler timeout | 10 s | handlers are short DB work; long work must be split |
| Sender timeout | 5 s | |
| Tick budget | 45 s total, jobs phase ≤ 30 s | HTTP write deadline for the tick route is extended to 90 s; Cloud Scheduler attempt deadline 120 s (027) |
| Per-tick caps | 250 jobs, 250 messages | SC-003: 500-job backlog drains in ≤ 3 ticks |
| Claim size | 1 row per claim | simplest lease reasoning; a few ms per row at this volume |
| Backoff | 1 m, 5 m, 15 m, 1 h, 3 h, then 6 h cap; ±20 % jitter below the cap, derived deterministically from `hash(id, attempt)` | FR-017, testable without randomness |
| Default `max_attempts` | 8 (≈ 16 h of retries) | per-kind / per-topic override at registration |
| Retention | 30 days for `done`/`cancelled`/`expired` jobs, `sent`/`withdrawn`/`expired` messages, `tick_runs`; `dead` kept until admin acts | FR-035, 500 MB free DB |
| Overdue threshold | 15 min | FR-021 |
| Expiry sweep cap | 1,000 rows per tick per table | |
