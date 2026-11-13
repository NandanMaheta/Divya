# Implementation Plan: Scheduler tick + scheduled jobs + outbox (fake-clock testable)

**Branch**: `feat/002-scheduler-tick` | **Date**: 2026-11-13 | **Spec**: [spec.md](./spec.md) (owner-approved 2026-11-13)

**Input**: Feature specification from `specs/002-scheduler-tick/spec.md`, research in [research.md](./research.md) (sections 1–7 from the research phase, section 8 = plan decisions).

**Setup note**: `.specify/scripts/bash/setup-plan.sh --json` ran and copied the template; `.specify/extensions.yml` has no `before_plan`/`after_plan` hooks.

## Summary

Build the M9 time engine every later feature depends on: a `POST /internal/tick` endpoint (hidden from the public OpenAPI) that Cloud Scheduler calls every 5 minutes with a Google OIDC token; an in-repo `scheduled_jobs` queue with one-row `FOR UPDATE SKIP LOCKED` claims, leases and fencing tokens, where a handler's DB effects and its "done" mark commit atomically; a transactional outbox relayed by the same tick through per-topic senders, with quiet-hours deferral, dedupe, withdrawal and expiry; IST business-date helpers including the BR-SET-08 due-date calculator (GS-04); a settable clock used by every "now" decision; and admin capabilities (failed-work list, retry, cancel, scheduler status, grouped dead-item alert as outbox event `admin.jobs_dead`). Admin HTTP operations land in a second PR after 003 (auth) merges.

## Technical Context

**Language/Version**: Go 1.27.4 (`apps/api`), PostgreSQL 18.

**Primary Dependencies**: existing Huma v2.40.0, pgx v5.13.0, sqlc v1.31.1, goose v3.30.1, testcontainers-go v0.45.0. New (owner approval required, see below): `github.com/coreos/go-oidc/v3` v3.21.0 (+ `go-jose/v4`, `x/oauth2`); `github.com/google/uuid` v1.6.0 promoted from indirect to direct; `pgregory.net/rapid` v1.2.0 (test-only, already in go.sum).

**Storage**: PostgreSQL 18 (Supabase in staging/production via session pooler port 5432). Migration `00002_scheduler_outbox.sql`: `scheduled_jobs`, `outbox_messages`, `quiet_hours`, `tick_runs` ([data-model.md](./data-model.md)).

**Testing**: `go test -race` with table tests, `pgregory.net/rapid` properties, `dbtest.New(t)` integration on PostgreSQL 18, `humatest` handler tests, OpenAPI contract test; fake clock everywhere.

**Target Platform**: Cloud Run (asia-south1), scale to zero, request-scoped CPU; distroless static image.

**Project Type**: web service (Go modular monolith), backend-only feature (no UI; screens are 022/023).

**Performance Goals**: an idle tick < 100 ms of DB work; 250 jobs + 250 messages per tick within a 45 s budget; a 500-job backlog drains in ≤ 3 ticks (SC-003); two-week time-travel test < 60 s in CI (SC-006). Public API p95 < 200 ms is unaffected (tick is not on user paths).

**Constraints**: ₹0 hosting (one Cloud Scheduler job, free Supabase, 500 MB DB → 30-day retention); MaxConns 5 per instance; HTTP `WriteTimeout` 30 s globally (extended to 90 s for the tick route only); no goroutines outliving the request; no `time.Now`/SQL `now()` in scheduling decisions.

**Scale/Scope**: 1 operator, ~100 clients × ≤ 10 accounts; hundreds of jobs/messages per day with bursts on closing and listing days.

No open NEEDS CLARIFICATION items: all plan-level questions from spec "Notes for the architect" are resolved in research.md §8.

## Constitution Check

*Gate before Phase 0 and re-checked after Phase 1 design. Result: PASS (two justified deviations in Complexity Tracking).*

| Principle | How this plan complies | Status |
|---|---|---|
| I. Business rules are the source of truth | Mechanism only for BR-IPO-03, BR-TASK-03, BR-SET-08 (+ GS-04), as the spec states. No business behaviour invented: kinds/topics/cadences belong to owning features. Every rule ID appears in test names (Test strategy). Defaults D1–D3 approved. | PASS |
| II. Money exact, history immutable | No money. `scheduled_jobs`/`outbox_messages`/`quiet_hours`/`tick_runs` are mutable operational tables by owner decision (Clarifications); no `forbid_mutation()`. Admin retry/cancel and quiet-hours changes write `audit_log` in the same transaction. | PASS |
| III. Test-first, deterministic | Tests written first by test-engineer; injected `clock.Func` in every service, SQL receives `$now`; forbidigo bans `time.Now`/`time.Local` in business code; no sleeps (fake clock + direct claims for crash simulation). Property tests for AddWeekdays, quiet hours, backoff, claim/lease invariants. | PASS |
| IV. Modular monolith, hard boundaries | New modules `internal/scheduler`, `internal/outbox`; new platform packages import no business module (depguard rules extended). Owning features use only the Go contracts in `contracts/go-module-api.md`; nobody reads these tables directly. | PASS |
| V. Contract-first APIs | Phase A changes nothing in the public OpenAPI (tick is `Hidden`, internal machine endpoint, contract in `contracts/internal-tick.openapi.yaml`). Phase B adds admin operations additively (oasdiff non-breaking), RFC 9457 errors, `Idempotency-Key` on mutations. | PASS (deviation 2) |
| VI. Security and privacy | OIDC signature/issuer/audience/expiry + service-account email check; dev bypass only in local/ci and refused at startup elsewhere; tokens never logged; `last_error` sanitised; payloads carry ids, never PAN; admin HTTP surface not shipped before 003. | PASS |
| VII. Simple, fast, accessible | Boring table + SKIP LOCKED instead of a queue framework; smallest viable OIDC library; each new dependency justified below. | PASS |
| Technology constraints | Single `/internal/tick` endpoint, `scheduled_jobs` + outbox tables (constitution wording). | PASS |
| Workflow gates | PR touches `migrations/` → owner approval required on the PR. New runtime dependency → owner approval at this plan gate. | Noted |

Post-design re-check (after data-model and contracts): no new violations; `tick_runs` without `org_id` and the hidden tick operation are the only deviations, both justified below.

## Key decisions

| # | Decision | Where detailed |
|---|---|---|
| K1 | Own `scheduled_jobs` table, not River; ADR-0003 amended (Amendment 1, proposed with this plan). | research §8.1, ADR-0003 |
| K2 | One-row claims with 60 s lease + `lock_token` fencing; handler effects + completion in one transaction; failure recorded in a separate fenced transaction. Lock order is always "business rows, then job row", so owning-feature reschedules cannot deadlock with running handlers. | data-model §1 |
| K3 | Reschedule while running is kept via a `rerun` flag (US5.5); cancel while running via `cancel_requested`. | data-model §1 |
| K4 | Expiry is a column on jobs and messages; a sweep marks expired rows before claiming (FR-014, FR-028, US7). Recurring kinds re-arm from `now`, so catch-up runs once (FR-015). | data-model §1–2 |
| K5 | Quiet hours are applied when a message is recorded **and** when a retry is scheduled, so no quiet-hours message ever becomes available inside the window (SC-008). Per-org `quiet_hours` row, default 21:30–08:00 when absent. | data-model §2–3 |
| K6 | Tick auth: `coreos/go-oidc/v3` + email check, public Cloud Run with app-level check; `TICK_AUTH=dev` only in `APP_ENV=local|ci`; OIDC mode without audience/service account = tick disabled (401) with a startup WARN, so deploys do not break before 027 provisions the Scheduler job. | research §8.2–8.3 |
| K7 | Admin HTTP operations deferred to Phase B (second PR, after 003 merges); capabilities, audit and tests ship in Phase A. | research §8.6 |
| K8 | Dead alert = outbox topic `admin.jobs_dead`, one per org per tick via an `alerted_at` sweep; delivered by 014. | contracts/outbox-events.md |
| K9 | `platform/db.TxRunner` puts the transaction in `context`; `Schedule`/`Add` require it (`ErrNoTx`) so they always join the caller's business transaction. | contracts/go-module-api.md |
| K10 | One migration file for both modules (`00002_scheduler_outbox.sql`) to keep one version number while 003 also adds migrations. | Risks |

## Module placement and dependencies

```
cmd/divya ──► scheduler ──► outbox ──► platform/{db, audit, clock, istdate, retry}
     │            └──────────────────► platform/{db, audit, clock, istdate, retry}
     ├──► platform/tickauth ──► go-oidc/v3 (only here)
     ├──► platform/{config, httpx, db}
     └──► health (unchanged)
Future owning modules (008, 012, 013, 018, 019 …) ──► scheduler (Schedule/Reschedule/Cancel/Register) and outbox (Add/Withdraw/RegisterSender)
```

| Package | Responsibility | Imports allowed |
|---|---|---|
| `internal/platform/clock` | `Func`, `Real`, `Fake` | stdlib |
| `internal/platform/istdate` | IST location (embedded tzdata), `Date`, `AddWeekdays`, `PaymentDue`, `WallClock`, `QuietHours`, `NextDailyAt` | stdlib |
| `internal/platform/retry` | `Backoff(attempt, seed)` (deterministic jitter), `ErrorText(err)` (truncate 500, redact bearer/JWT-like strings and PAN pattern `[A-Z]{5}[0-9]{4}[A-Z]`) | stdlib |
| `internal/platform/db` (+`tx.go`) | `TxRunner`, `MustTx`, `ErrNoTx` | pgx, dbgen |
| `internal/platform/audit` | `Actor`, `Entry`, `Write` | db, dbgen, uuid |
| `internal/platform/tickauth` | `Authenticator`, `NewOIDC`, `NewDev`, `Disabled` | go-oidc/v3, stdlib |
| `internal/platform/config` | new `Tick` settings + validation | stdlib |
| `internal/platform/httpx` | `statusRecorder.Unwrap()`; `ExtendWriteDeadline(ctx huma.Context, d)` | huma |
| `internal/outbox` | `Add`, `Withdraw*`, quiet hours, relay, dead sweep, admin list/retry/cancel; `outboxtest.Sender` | platform/* |
| `internal/scheduler` | registry, `Schedule`/`Reschedule`/`Cancel*`, tick orchestration, `tick_runs`, status, retention kind, dead-alert emission, admin list/retry/cancel, hidden tick handler | outbox, platform/* |

Boundary rules (`apps/api/.golangci.yml`, implemented by a task; exact intent):

```yaml
depguard:
  rules:
    platform-is-a-leaf:
      files: ["**/internal/platform/**"]
      deny:
        - { pkg: "github.com/NandanMaheta/divya/apps/api/internal/health",    desc: platform packages must not import business modules }
        - { pkg: "github.com/NandanMaheta/divya/apps/api/internal/scheduler", desc: platform packages must not import business modules }
        - { pkg: "github.com/NandanMaheta/divya/apps/api/internal/outbox",    desc: platform packages must not import business modules }
    outbox-does-not-know-scheduler:
      files: ["**/internal/outbox/**"]
      deny:
        - { pkg: "github.com/NandanMaheta/divya/apps/api/internal/scheduler", desc: scheduler depends on outbox, never the reverse }
    oidc-only-in-tickauth:
      files: ["$all", "!**/internal/platform/tickauth/**"]
      deny:
        - { pkg: "github.com/coreos/go-oidc/v3", desc: OIDC verification lives in platform/tickauth }
    no-legacy-libs: (unchanged)
forbidigo:
  forbid:
    - { pattern: '^time\.(Now|Since|Until)$', msg: "inject a clock.Func (constitution III)" }
    - { pattern: '^time\.Local$',             msg: "use istdate.IST; never the host time zone" }
  analyze-types: true
# exclusions: cmd/, internal/platform/clock/, internal/platform/httpx/ (request timing), and _test.go
```

The implementer must confirm with `golangci-lint run ./...` that a deliberate violation fails (depguard v2 `files` negation syntax). No cross-module table access: `scheduler` code only calls `queries/scheduler.sql` + `queries/platform.sql` queries and `outbox` only `queries/outbox.sql` + platform (convention; reviewer checks because all queries share `dbgen`).

## Data model

Full definition in [data-model.md](./data-model.md). Summary of migration `apps/api/migrations/00002_scheduler_outbox.sql` (goose Up/Down, expand-only, no change to 00001 tables):

- `scheduled_jobs`: org-scoped; states `pending|running|done|dead|cancelled|expired` (retrying derived); partial unique `(org_id, kind, dedupe_key) WHERE status IN ('pending','running')`; partial indexes for due claim, lease reclaim, expiry, subject, failed list, unalerted dead, retention.
- `outbox_messages`: org-scoped; states `pending|sending|sent|dead|withdrawn|expired`; unique `(org_id, topic, dedupe_key)` over all statuses; recipient kind/id; `respects_quiet_hours`, `available_at`, `expires_at`; same lease/fencing/alert columns.
- `quiet_hours`: per-org override of 21:30–08:00 (minutes after midnight IST).
- `tick_runs`: one row per tick with counts and status (no `org_id`, justified below).
- No append-only triggers on these four tables (owner decision); `audit_log` unchanged and used for admin actions.
- Down: drop the four tables.

sqlc (`apps/api/sqlc.yaml`): add overrides `db_type: uuid → github.com/google/uuid.UUID` (and nullable `uuid.NullUUID`). New query files:

- `queries/scheduler.sql`: `UpsertJob`, `RescheduleJob`, `CancelJobByKey`, `CancelJobsBySubject`, `ExpireDueJobs`, `ClaimJob`, `CompleteJob`, `FailJob`, `MarkJobDead`, `EnsureRecurringJob`, `InsertTickRun`, `FinishTickRun`, `LastTickRun`, `ListFailedJobs`, `CountJobsByState`, `GetJobForOrg`, `AdminRetryJob`, `AdminCancelJob`, `SelectUnalertedDeadJobs`, `MarkJobsAlerted`, `PurgeFinishedJobs`, `PurgeTickRuns`.
- `queries/outbox.sql`: `InsertMessage` (ON CONFLICT DO NOTHING), `WithdrawByKey`, `WithdrawBySubject`, `ExpireDueMessages`, `ClaimMessage`, `MarkMessageSent`, `FailMessage`, `MarkMessageDead`, `GetQuietHours`, `UpsertQuietHours`, `ListFailedMessages`, `CountMessagesByState`, `GetMessageForOrg`, `AdminRetryMessage`, `AdminCancelMessage`, `SelectUnalertedDeadMessages`, `MarkMessagesAlerted`, `PurgeFinishedMessages`.
- `queries/platform.sql`: `InsertAuditLog`; Phase B adds `FindAuditByIdempotencyKey`.

Every query that compares time takes `$now` (and `$lease`) as parameters; `sqlc vet` must pass.

## API operations (OpenAPI diff)

**Phase A (first PR): public OpenAPI diff = none.** `packages/api-client/openapi.yaml` and the TS client do not change.

| Method | Path | Operation | Visibility | Request | Response | Errors | Idempotency |
|---|---|---|---|---|---|---|---|
| POST | `/internal/tick` | `scheduler-tick` | `Hidden: true` (not in OpenAPI, not in TS client) | header `Authorization` | 200 `TickSummary` | 401 (`WWW-Authenticate: Bearer`), 403, 503 | naturally idempotent: duplicate ticks are safe (SKIP LOCKED + leases + dedupe) |

Contract: [contracts/internal-tick.openapi.yaml](./contracts/internal-tick.openapi.yaml). The handler authenticates before touching the database; it extends the response write deadline to 90 s via `httpx.ExtendWriteDeadline` (global `WriteTimeout` stays 30 s). It does not participate in 003's auth middleware (no `Security` on the operation; hidden operations are not in `Paths`, so 003's contract walk is unaffected).

**Phase B (second PR, after 003 is merged to main): additive diff** from [contracts/admin-scheduler.openapi.yaml](./contracts/admin-scheduler.openapi.yaml), all `security: [{bearer: [admin]}]`, tags `Scheduler`/`Outbox`, org and actor from 003's principal:

| Method | Path | Operation | Response | Errors | Idempotency |
|---|---|---|---|---|---|
| GET | `/v1/admin/scheduler/status` | `scheduler-get-status` | `SchedulerStatus` | 401, 403 | n/a |
| GET | `/v1/admin/scheduler/jobs?state&kind&limit&cursor` | `scheduler-list-failed-jobs` | `FailedJobPage` | 401, 403, 422 | n/a |
| POST | `/v1/admin/scheduler/jobs/{id}/retry` | `scheduler-retry-job` | `FailedJob` | 401, 403, 404, 409 | `Idempotency-Key` header; replay → 200 current item, no second audit row |
| POST | `/v1/admin/scheduler/jobs/{id}/cancel` | `scheduler-cancel-job` | `FailedJob` | 401, 403, 404, 409 | same |
| GET | `/v1/admin/outbox/messages?state&topic&limit&cursor` | `outbox-list-failed-messages` | `FailedMessagePage` | 401, 403, 422 | n/a |
| POST | `/v1/admin/outbox/messages/{id}/retry` | `outbox-retry-message` | `FailedMessage` | 401, 403, 404, 409 | same |
| POST | `/v1/admin/outbox/messages/{id}/cancel` | `outbox-cancel-message` | `FailedMessage` | 401, 403, 404, 409 | same |

Phase B steps: `admin_handler.go` in each module, wire in `buildAPI`, `go run ./cmd/divya openapi`, `pnpm --filter @divya/api-client generate`, commit `openapi.yaml` + `schema.ts`; oasdiff must report no breaking change. If 003 lands first, Phase B can be part of the same PR; the tasks for it are marked blocked on 003.

## Tick algorithm (scheduler.Tick)

1. `start := now()`; insert `tick_runs` (status `running`). DB error → return error → 503 (FR-006, FR-007, US1.7).
2. Ensure recurring rows: for each registered recurring kind, `EnsureRecurringJob` for all orgs (`INSERT … SELECT FROM orgs ON CONFLICT DO NOTHING`, `run_at` from `KindOptions.Recurring(now)`).
3. Jobs phase (until no claim, 250 jobs, or 30 s): `ExpireDueJobs($now)`; loop `ClaimJob($now, lease, instance)`; for each job: claimed `attempts > max_attempts` (lease expired repeatedly) → dead "lease expired"; unknown kind → fail; else `InTx`: run handler with 10 s timeout and panic recovery, then `CompleteJob` fenced (0 rows → rollback, count as lost lease); on error → `FailJob` (backoff via `retry.Backoff`, sanitised `retry.ErrorText`) or dead / cancelled.
4. Messages phase (until none, 250 messages, or 45 s total): `outbox.Relay`: `ExpireDueMessages`, loop `ClaimMessage` (ORDER BY id among `available_at <= now`), sender lookup (none → fail), `Send` with 5 s timeout outside a transaction, `MarkMessageSent` fenced, or `FailMessage` with `available_at = quiet.Defer(now + backoff)`.
5. Dead-alert sweep in one transaction: unalerted dead jobs + messages (excluding `admin.*` topics) grouped by org → `outbox.Add(admin.jobs_dead)` per org, mark `alerted_at` (contracts/outbox-events.md).
6. `FinishTickRun` with counts, `budget_exhausted`, status `ok`; log summary; return 200. Failure in steps 3–6 due to the DB → best-effort mark `error`, return 503 (committed jobs remain atomic).

Execution is sequential, one DB connection at a time, entirely inside the request (Cloud Run CPU). On SIGTERM the request context is cancelled; in-flight leases expire and the next tick reclaims.

## Scheduler jobs and outbox events

| Kind / topic | Type | Schedule | Owner |
|---|---|---|---|
| `scheduler.retention` | recurring job per org | daily 03:00 IST (`istdate.NextDailyAt`) | 002: purge finished jobs/messages older than 30 days and old `tick_runs`; keep `dead` |
| `admin.jobs_dead` | outbox event | at most one per org per tick | 002 emits; 014 delivers (push + bell) |
| Test-only kinds (`sample.*`) and topics (`sample.*`) | registered only in tests | | 002 tests |

No other kinds or topics are created by this feature; 008/012/013/018/019/021 register theirs.

## Configuration (`internal/platform/config`)

| Env var | Default | Validation |
|---|---|---|
| `TICK_AUTH` | `dev` when `APP_ENV` is `local`/`ci`, else `oidc` | one of `oidc`, `dev`; `dev` with `APP_ENV` `staging`/`production` → `Load()` error, service refuses to start (FR-003, US2.5) |
| `TICK_AUDIENCE` | empty | used in `oidc` mode; set to the bare Cloud Run service URL (027 sets the same value on the Scheduler job) |
| `TICK_SERVICE_ACCOUNT` | empty | expected `email` claim |
| `TICK_DEV_TOKEN` | empty | `dev` mode bearer token; empty = every tick rejected |

`oidc` with empty audience or service account → `tickauth.Disabled()` plus a startup WARN `tick auth not configured; /internal/tick will reject all calls`. `.github/workflows/deploy.yml` gains `TICK_AUDIENCE=${{ vars.TICK_AUDIENCE }},TICK_SERVICE_ACCOUNT=${{ vars.TICK_SERVICE_ACCOUNT }}` in `--set-env-vars` (owner sets the repository variables; empty values keep the tick disabled). Operational constants (lease, budgets, caps, backoff, retention) live in `scheduler.Options`/`outbox.Options` with defaults from data-model §7, not env.

## Wiring (`apps/api/cmd/divya/main.go`)

- `buildAPI(deps)` takes a small struct (`pool`, `cfg`, `log`, `tickAuth`, services) so `divya openapi` still builds with zero deps and never touches a DB or the network.
- `serve`: `txr := db.NewTxRunner(pool)`; `ob := outbox.New(txr, clock.Real, log, outbox.DefaultOptions())`; `sch := scheduler.New(txr, ob, clock.Real, log, hostname, scheduler.DefaultOptions())`; `sch.Register("scheduler.retention", sch.RetentionHandler(), …)`; `tickAuth := selectTickAuth(ctx, cfg)` (go-oidc key set bound to the long-lived serve context, not a request context); `scheduler.RegisterTick(api, sch, tickAuth)`.
- `run`: `config.Load()` errors (dev auth in staging/production) stop the process before listening.
- Phase B adds `scheduler.RegisterAdmin(api, sch)` and `outbox.RegisterAdmin(api, ob)` after 003's middleware.

## Observability (slog, JSON, existing handler)

| Event | Level | Fields |
|---|---|---|
| tick finished | INFO | `tick_id`, `instance`, `jobs_run`, `jobs_failed`, `jobs_dead`, `jobs_expired`, `msgs_sent`, `msgs_failed`, `msgs_dead`, `msgs_expired`, `budget_exhausted`, `took_ms` |
| job failed (will retry) | WARN | `tick_id`, `job_id`, `org_id`, `kind`, `attempt`, `next_run_at`, `err` (sanitised) |
| job/message dead | ERROR | `tick_id`, `job_id`/`message_id`, `org_id`, `kind`/`topic`, `attempts` |
| lease lost on completion | WARN | `job_id`, `kind` |
| handler panic | ERROR | `job_id`, `kind`, `panic`, `stack` (stack only in logs, never in `last_error`) |
| tick rejected | WARN | `reason` = `missing`/`invalid`/`forbidden`/`disabled`; never the header, token or claims |
| tick auth disabled at startup | WARN | `app_env` |

`httpx.logRequests` already logs only method, path, status and duration. Cloud Logging retention ≥ 180 days is handled by 027.

## Test strategy (written first by test-engineer; names carry rule IDs)

| Layer | Package / file | What | Rule IDs / requirements |
|---|---|---|---|
| Static | repo | `golangci-lint` (depguard, forbidigo, gosec), `sqlc vet`, gofmt | constitution III, IV |
| Unit (table) | `platform/istdate/istdate_test.go` | US4 cases 1–10, incl. Fri→Tue, Thu→Mon, Mon–Wed, year end 31 Dec 2026→4 Jan 2027, month end 27 Feb 2026→3 Mar 2026, holiday not skipped, Sat/Sun→Tue, 19:00 UTC→next IST date, 09:15 IST = 03:45 UTC; all repeated with `time.Local` forced to `America/New_York` | `TestDueDate_GS_04_*`, `TestPaymentDue_BR_SET_08_*`, `TestAddWeekdays_BR_SET_08_*`, `TestDateOf_BR_SET_08_ServerZoneIndependent` |
| Property (rapid) | `platform/istdate/istdate_prop_test.go` | AddWeekdays: n≥1 → result is a weekday; exactly n weekdays in (d, result]; composition `Add(Add(d,a),b) == Add(d,a+b)`; span ≤ n + 2·⌈n/5⌉ + 2 days; monotonic in n; PaymentDue always 23:59 IST on Tue–Fri or Mon | `TestAddWeekdays_BR_SET_08_Properties` |
| Unit + property | `platform/istdate/quiet_test.go` | Boundaries 21:29, 21:30 (inclusive), 02:00, 07:59, 08:00 (exclusive), non-wrapping windows; property: `Defer(t) >= t`, `!Contains(Defer(t))`, minimal (no earlier instant ≥ t outside the window) | `TestQuietHours_BR_TASK_03_*` |
| Unit + property | `platform/retry` | Backoff non-decreasing, strictly increasing until cap, ≤ 6 h, ≥ 48 s, deterministic per seed; `ErrorText` truncates and redacts `Bearer …`, JWT-shaped strings and PAN pattern | FR-017, FR-018 |
| Unit | `platform/clock` | Fake Set/Advance, race-safe | FR-032 |
| Unit | `platform/config/config_test.go` | `TICK_AUTH=dev` refused in staging/production; defaults per `APP_ENV`; invalid value | US2.5, FR-003 |
| Unit (offline crypto) | `platform/tickauth` | httptest JWKS + locally minted RS256 tokens: missing header, not Bearer, malformed, bad signature, unknown key, expired (fake clock), wrong audience, wrong issuer, `alg=none`/HS256, valid but wrong email → `ErrForbidden`, `email_verified=false` → forbidden, valid → nil; dev mode constant-time compare and empty token; Disabled | US2.1–2.4, SC-004 |
| Handler (humatest) | `scheduler/handler_test.go` | 401/403 do zero work (fake ticker counts calls), 200 body shape, 503 on DB error, `WWW-Authenticate` header, no token in captured logs | US2.1–2.4, US2.7, FR-002, FR-007 |
| Contract | `cmd/divya/main_test.go` | generated OpenAPI has no path starting with `/internal` and no `scheduler-tick` operation; Phase B: admin operations present with `security` admin; CI diff of `openapi.yaml` + oasdiff | US2.6, FR-004, constitution V |
| Integration (dbtest) | `platform/db/tx_test.go`, `platform/audit/audit_test.go` | InTx commit/rollback/nesting; `MustTx` without tx → `ErrNoTx`; audit row written in tx | FR-008, FR-020 |
| Integration | `scheduler/schedule_test.go` | upsert latest wins (US1.9); rollback leaves no job (US1.10); done key → new row (US5.6); reschedule/cancel/cancel-subject, commit vs rollback, into the past, no-op on missing (US5.1–5.4, 5.7); reschedule while running → pending at new time (US5.5) | `TestSchedule_BR_IPO_03_*` |
| Integration | `scheduler/tick_test.go` | US1.1–1.3, 1.7, 1.8 (caps, oldest first); expiry vs state-change (FR-014); unknown kind; handler panic; retries with growing waits then dead (US6.1–6.3); recurring catch-up once (US7.6, FR-015); tick record and status overdue at 15 min (US6.8, FR-021) | `TestTick_*`, `TestTick_BR_IPO_03_LateNotSkipped` |
| Integration (crash) | `scheduler/lease_test.go` | claim via store and never finish → skipped until lease end, then rerun (US1.5); effect written by a separate tx then crash → dedupe gives one message (US1.6); lost lease rolls back handler effects | FR-011, FR-012 |
| Concurrency | `scheduler/concurrency_test.go` | two (and five) `Tick` goroutines at the same fake instant over 10/100 due jobs and messages: each effect once, summed counts equal totals; run with `-race -count=20` in CI | US1.4, US3.8, FR-011, FR-026 |
| Property (rapid, DB) | `scheduler/model_test.go` | state-machine test vs an in-memory model: random schedule/reschedule/cancel/advance/tick/crash-after-claim/fail-on-demand; invariants: no effect twice, never before `run_at`, ≤ 1 active row per key, running ⇒ lease+token, dead ⇔ attempts ≥ max, no due unexpired pending job left after an uncapped tick; truncate between runs; `-rapid.checks=50` in PR | `TestScheduler_BR_IPO_03_ModelProperties` |
| Integration | `outbox/outbox_test.go` | commit/rollback (US3.1); relay once + `sent_at` (US3.2); dedupe ignored (US3.3, FR-024); crash between send and mark → resend with same key (US3.4); withdraw by key/subject (US3.5, US8.6); no sender → failure (US3.6); expiry (US7.3, US8.7); channel-agnostic (US3.7); per-recipient order within a tick (FR-030); retries re-deferred out of quiet hours (SC-008) | `TestOutbox_BR_TASK_03_*` |
| Integration | `outbox/quiet_test.go` | US8.1–8.9 with fake clock; settings change applies only to new messages + audit row | `TestQuietHours_BR_TASK_03_*` |
| Integration | `scheduler/admin_test.go`, `outbox/admin_test.go` | list retrying/dead with required fields and no secrets (US6.4); retry → pending fresh attempts + audit (US6.5); cancel + audit (US6.6); retry of expired still expires (US6.7); other org invisible / 404 (US6.9); non-dead → conflict; dead counts | FR-019, FR-020, SC-007 |
| Integration | `scheduler/alert_test.go` | one `admin.jobs_dead` per org per tick with correct payload; not repeated next tick; `admin.*` dead messages never alert; failed sweep retried next tick; retry then dies again → alerts again | owner Q1 decision |
| Time travel (L2) | `scheduler/lifecycle_test.go` | US9.1 Sun 8 Nov → Thu 19 Nov 2026 in 5-min ticks with sample IPO step jobs, reminder series (never in quiet hours), payment-due job at `PaymentDue(13 Nov)` running at the first tick after Tue 17 Nov 23:59 IST; US9.2 listing moved to Mon 16 Nov on Thu 12 Nov → payment-due Wed 18 Nov 23:59; US9.3 clock is the only time source; asserts runtime < 60 s | `TestLifecycle_BR_IPO_03_BR_SET_08_GS_04_TwoWeeks` |
| Catch-up | `scheduler/catchup_test.go` | US7.1–7.5; SC-003 (500-job backlog, 6-hour outage, state-change jobs done within 3 ticks, zero expired messages sent) | `TestCatchUp_BR_TASK_03_*`, `TestCatchUp_BR_IPO_03_*` |
| Soak | `scheduler/soak_test.go` | SC-001: 30 simulated days, 100 clients, 10 IPOs, random duplicate/overlap/skip/crash; PR runs a 3-day slice; `DIVYA_SOAK=full` nightly | SC-001 |
| Component / E2E | n/a | No UI in 002; admin screens and Playwright flows come with 022/023 | |
| Golden | `platform/istdate` | GS-04 test is the golden scenario for this feature and is reused by 018; GS-01..03 untouched and must keep passing | GS-04 |

Fixtures: `scheduler/schedulertest` helpers (`NewHarness(t)` = one `dbtest.New` + org + fake clock + services + `RunTicks(from, to, step)` + `Reset()` truncating the four tables) so each package starts one container per top-level test and uses subtests. All integration tests skip under `-short`. Coverage target: `platform/istdate` 100 % branches (it feeds payment deadlines); scheduler/outbox ≥ 85 % statements.

Traceability: optionally add `BR-SET-08` to the 002 row's Rules column in `docs/product/modules.md` (owner/conductor call); tests for it exist either way.

## Performance notes

- Session pooler (5432): prepared statements stay on; each tick uses one connection at a time (claim, handler tx, mark) from the 5-connection pool; the claim transaction is never held while a handler or sender runs. 027 should cap Cloud Run `max-instances` (suggest 3) so worst case is 15 pooler clients (Supabase free limits still VERIFY).
- Idle tick: insert tick row, ensure recurring (1 statement), 2 expiry sweeps, 2 empty claims, sweep, finish ≈ 8 statements, < 100 ms including a warm connection; it also keeps the Supabase free project from pausing (ADR-0003).
- Claims use partial indexes; one-row claims cost ~1–2 ms each, so 250 jobs ≈ a few seconds plus handler time. Message throughput is bounded by sender latency (≈ 100–300 ms per push): ~150 messages per 45 s tick. 014 may add a batch sender (Expo accepts up to 100 per request) behind the same `Sender` contract if closing-day bursts need it.
- Storage: hundreds of rows/day × 30 days retention ≈ tens of thousands of rows, well under 50 MB.
- Time-travel test: ~3,200 ticks × ~8 statements on a local container, targeted < 60 s; the harness may skip ticks only where the spec allows (it must not, for US9.1).
- Cold start: go-oidc fetches Google JWKS on the first verification (one HTTPS call), cached afterwards per instance.

## Security notes

- The tick is reachable on the internet; the app check is the only gate (research §2.1 option b). Verification order: header present and `Bearer` → signature (RS256 only) against Google JWKS → issuer → audience (never empty in OIDC mode, else Disabled) → expiry (clock-injected) → `email == TICK_SERVICE_ACCOUNT` and `email_verified`. Failures before the email check → 401; email mismatch → 403. Generic problem bodies; nothing about the token in logs (US2.1).
- Fail closed: `TICK_AUTH=dev` refused at startup in staging/production; OIDC without config → every tick 401; dev token empty → every tick 401; dev token compared in constant time.
- Unknown-`kid` tokens make go-oidc refetch JWKS (single-flight); acceptable at this scale; noted for the security reviewer.
- No unauthenticated admin surface: admin HTTP operations ship only with 003's admin role check (Phase B). Service-level methods always take `org_id` and filter by it (US6.9).
- `last_error` sanitised and truncated; payloads carry ids/template keys only; no PAN anywhere in these tables.
- Audit: retry/cancel/quiet-hours changes write `audit_log` in the same transaction (constitution II).
- Security-sensitive change → security reviewer on the PR (constitution workflow 4) and owner approval for the migration (workflow 5).

## Dependencies requiring owner approval

| Dependency | Kind | Why | Alternatives considered |
|---|---|---|---|
| `github.com/coreos/go-oidc/v3` v3.21.0 (brings `github.com/go-jose/go-jose/v4` v4.1.x and `golang.org/x/oauth2`) | **new runtime** | Verify Cloud Scheduler's Google OIDC token (FR-002). Two transitive modules; JWKS caching and rotation, issuer quirk for Google, injectable `Now`; full verification path testable offline. Isolated in `platform/tickauth` by depguard. | `google.golang.org/api/idtoken` v0.314.0 (research's original pick; ~20 direct requirements incl. gRPC, cloud auth, s2a, otelgrpc); `golang-jwt/jwt/v5` (if 003 adds it) + hand-written JWKS cache (we own rotation/caching security code); stdlib-only JWT verification (rejected, security-sensitive); Cloud Run IAM invoker-only (would block the public API on the same service). |
| `github.com/google/uuid` v1.6.0 | runtime, **promoted from indirect** (already in go.sum, no new download) | Typed ids in service signatures and sqlc overrides instead of leaking `pgtype.UUID` across module APIs (go-module-pattern: no pgx types in signatures). | Keep `pgtype.UUID` in `store.go` and pass ids as strings in service APIs (weaker typing, parsing everywhere). |
| `pgregory.net/rapid` v1.2.0 | **test-only** (already in go.sum) | Property tests required by `.claude/rules/tests.md` for scheduling invariants. | Hand-rolled randomized loops (no shrinking). |

Nothing is added to `go.mod` until the owner approves; tasks that need these are blocked on that approval.

## Delivery phases

- **Phase A (PR 1, this feature)**: platform packages (clock, istdate, retry, db tx, audit, tickauth, config, httpx tweak), migration 00002 + sqlc, outbox module, scheduler module, hidden tick handler, wiring, lint rules, deploy env vars, ADR-0003 amendment, all tests above except Phase B contract/handler tests. Public OpenAPI unchanged.
- **Phase B (PR 2, blocked on 003 merged)**: admin HTTP handlers for both modules, Idempotency-Key replay via audit lookup, OpenAPI + TS client regeneration, humatest + contract tests for admin operations.

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Migration number collision with 003 (both want `00002`) | goose ordering conflict at merge | Whoever merges second renumbers its unapplied file before merge (never edit an applied migration); one 002 file keeps it to a single rename. |
| `platform/db.TxRunner` / `platform/audit` also invented by 003 | duplicate abstractions or merge conflict | 002 defines them per the go-module-pattern skill; tell the 003 planner to reuse them (or reconcile at whichever merge is second). |
| Owner rejects go-oidc | tick cannot verify tokens | Fall back to idtoken behind the same `tickauth.Authenticator` interface; only `platform/tickauth/oidc.go` and its tests change. |
| No sender for `admin.jobs_dead` until 014 | those alerts end `dead` | Visible through dead counts/list (the owner's second channel); `admin.*` excluded from alerting so no loop; 014 registers the sender. |
| Message throughput limited by per-message sends | closing-day burst could take > 1 tick | Caps and ordering keep correctness; 014 can add batching behind `Sender`. |
| depguard `files` negation / forbidigo exclusions behave differently than expected | boundary not enforced | Task done-command includes a deliberate-violation check. |
| Time-travel test exceeds 60 s on slow CI runners | SC-006 fail | Keep per-tick statements minimal; measure in the test; profile before relaxing anything. |
| Handlers in later features doing non-transactional side effects | duplicate external effects | Contract: handlers must be idempotent; external effects go through the outbox (dedupe keys), not directly from handlers. |
| Tick disabled silently if TICK_* vars missing in staging/production | jobs stop | Startup WARN, FR-021 overdue status, external uptime monitoring (027). |

## Project Structure

### Documentation (this feature)

```text
specs/002-scheduler-tick/
├── spec.md
├── research.md                 # research phase + §8 plan decisions
├── plan.md                     # this file
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── go-module-api.md        # Go APIs for owning modules
│   ├── internal-tick.openapi.yaml
│   ├── admin-scheduler.openapi.yaml   # Phase B
│   └── outbox-events.md        # admin.jobs_dead
├── checklists/requirements.md
└── tasks.md                    # produced by the planner (/speckit-tasks), not by this command
```

### Source Code (repository root)

```text
apps/api/
├── migrations/00002_scheduler_outbox.sql            # new
├── queries/scheduler.sql                            # new
├── queries/outbox.sql                               # new
├── queries/platform.sql                             # + InsertAuditLog (Phase B: FindAuditByIdempotencyKey)
├── sqlc.yaml                                        # + uuid overrides
├── internal/platform/db/dbgen/*                     # regenerated
├── internal/platform/db/tx.go, tx_test.go           # new
├── internal/platform/audit/audit.go, audit_test.go  # new
├── internal/platform/clock/clock.go, fake.go, clock_test.go                      # new
├── internal/platform/istdate/istdate.go, quiet.go, *_test.go                     # new
├── internal/platform/retry/backoff.go, errtext.go, *_test.go                     # new
├── internal/platform/tickauth/tickauth.go, oidc.go, dev.go, *_test.go            # new
├── internal/platform/config/config.go, config_test.go                            # changed
├── internal/platform/httpx/httpx.go                                              # Unwrap + ExtendWriteDeadline
├── internal/outbox/service.go, store.go, relay.go, quiet.go, admin.go, *_test.go # new
├── internal/outbox/outboxtest/sender.go                                          # new (test double)
├── internal/scheduler/service.go, registry.go, store.go, tick.go, alert.go,
│   retention.go, admin.go, handler.go, *_test.go                                 # new
├── internal/scheduler/schedulertest/harness.go                                   # new (test harness)
├── internal/scheduler/admin_handler.go, internal/outbox/admin_handler.go         # Phase B
├── cmd/divya/main.go, main_test.go                                               # changed / new
├── .golangci.yml                                                                 # depguard + forbidigo
└── go.mod, go.sum                                                                # after owner approval
.github/workflows/deploy.yml                          # TICK_* env vars
docs/adr/0003-tick-scheduler.md                       # Amendment 1 (done in this plan)
packages/api-client/openapi.yaml, src/schema.ts       # Phase B only
```

**Structure Decision**: two new business modules (`scheduler`, `outbox`) shaped like `internal/health`, plus six small platform packages that business modules may share; everything inside the existing `apps/api` Go module.

## Complexity Tracking

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| `tick_runs` has no `org_id` (sql.md: "org_id on every business table") | A tick is one platform-level run serving all orgs; it stores counts and timestamps only, no business data. | One row per org per tick multiplies writes and still cannot express "the tick ran" when there are zero orgs. |
| `POST /internal/tick` is a Huma operation hidden from the OpenAPI contract (constitution V) | FR-004 and US2.6 require internal entry points to stay out of the public description and TS client; its contract is kept in `contracts/internal-tick.openapi.yaml` and enforced by tests. | A raw `http.Handler` outside Huma would lose typed input/output and RFC 9457 errors. |

## Notes for the planner (`/speckit-tasks`)

- Order: dependency approval gate → platform packages (clock, istdate, retry, config, tickauth) → migration + sqlc + db tx + audit → outbox → scheduler (schedule API → tick → alert/retention → admin) → handler + wiring + lint + deploy vars → time-travel, catch-up, concurrency, soak → Phase B (blocked on 003).
- Each implementation task is preceded by its test task (test-engineer), ≤ ~200 changed lines and ≤ 5 files, with one done command (e.g. `cd apps/api && go test -race ./internal/platform/istdate/...`).
- Tasks touching `migrations/` need owner approval on the PR; tasks adding go.mod requirements are blocked on the dependency approval above.
