# Research: 002 Scheduler tick + scheduled_jobs + outbox

Date: 2026-11-13. Scope: BR-IPO-03 (date change reschedules every dependent job), BR-TASK-03 (reminders), BR-SET-08 (due date = listing day + 2 weekdays, 23:59 IST).

Source-quality note: docs.cloud.google.com, riverqueue.com and supabase.com were not reachable from the research session (network allow-list). Claims from those hosts come from search-result snippets or are marked "VERIFY". Version numbers were read from proxy.golang.org.

## 1. External needs and current versions

| Need | Package | Version (proxy.golang.org, 2026-11-13) | New runtime dep? |
|---|---|---|---|
| Verify Cloud Scheduler OIDC token | `google.golang.org/api/idtoken` | v0.314.0 (2026-11-12) | Yes. Needs owner approval per CLAUDE.md. |
| Alternative verifier | `github.com/coreos/go-oidc/v3` | v3.21.0 (2026-09-01) | Rejected, see 2.1 |
| Job queue (rejected for now) | `github.com/riverqueue/river` | v0.51.2 (2026-11-09) | No |
| Fake clock (optional) | `github.com/jonboulle/clockwork` v0.5.0 (2024-11-29); `github.com/benbjohnson/clock` v1.3.5 (2023-05-18) | | Rejected, see 2.4 |
| IST tz data | stdlib `time/tzdata` | Go 1.27 toolchain (go.mod) | No (stdlib) |

Already in go.mod and reused: pgx v5.13.0, goose v3.30.1, huma v2.40.0, testcontainers-go v0.45.0.

Sources: [idtoken](https://pkg.go.dev/google.golang.org/api/idtoken), [api @latest](https://proxy.golang.org/google.golang.org/api/@latest), [river @latest](https://proxy.golang.org/github.com/riverqueue/river/@latest), [go-oidc @latest](https://proxy.golang.org/github.com/coreos/go-oidc/v3/@latest), [clockwork @latest](https://proxy.golang.org/github.com/jonboulle/clockwork/@latest), [benbjohnson/clock @latest](https://proxy.golang.org/github.com/benbjohnson/clock/@latest).

## 2. Recommended approach

### 2.1 Tick authentication (OIDC)

Recommendation: `idtoken.NewValidator(ctx)` once at startup (caches Google JWKs); per request take `Authorization: Bearer <jwt>`, call `validator.Validate(ctx, token, audience)`.

- Signature: `func (v *Validator) Validate(ctx, idToken, audience string) (*Payload, error)`; "if audience is not empty the audience claim is validated". Payload has `Issuer`, `Audience`, `Expires`, `Subject`, `Claims map`. Source: [idtoken docs](https://pkg.go.dev/google.golang.org/api/idtoken).
- Never pass an empty audience (it skips the check). Config: `TICK_AUDIENCE` (the Cloud Run service URL / the OIDC `audience` set on the scheduler job). If the scheduler job omits audience, it defaults to the target URI ([gcloud reference snippet](https://cloud.google.com/sdk/gcloud/reference/scheduler/jobs/update/http?hl=fr)). Recommend setting audience explicitly in OpenTofu to the bare service URL (no path) so it is stable.
- After Validate, additionally check in our code: `Claims["email"] == TICK_SERVICE_ACCOUNT_EMAIL` and `Claims["email_verified"] == true`. Validate proves "Google signed it for this audience"; it does not prove which service account. Without the email check any Google identity that can mint a token for that audience could call the tick. (Claims `email`/`email_verified` are not described on the idtoken page; they are standard Google ID token claims for service accounts; VERIFY with a real token in staging.)
- Issuer check: idtoken validates against Google's issuers internally (`https://accounts.google.com`); the pkg page does not document this, so add a defensive assertion that `Payload.Issuer` is `https://accounts.google.com` or `accounts.google.com`.
- Service account must be in the same project as the scheduler job; it needs `roles/run.invoker` on the service if Cloud Run IAM is enforced ([gcloud reference snippet](https://cloud.google.com/sdk/gcloud/reference/scheduler/jobs/create/http)). Design choice: keep Cloud Run "allow unauthenticated" off for `/internal/tick` is impossible per-path, so either (a) Cloud Run IAM invoker-only for the whole service (breaks public API) or (b) service public, app-level OIDC check on `/internal/*` only. Recommend (b); the app check is then the sole gate, so test it hard (missing header, wrong aud, wrong email, expired, non-Google token).
- Local/dev bypass design: an interface `TickAuthenticator { Verify(ctx, r) error }` with two implementations selected by `APP_ENV`: `oidcAuth` (staging, production) and `sharedSecretAuth`/`noAuth` allowed only when `APP_ENV` is `local` or `ci`. `config.validate()` must refuse the bypass when `APP_ENV` is `staging|production` (fail closed at startup). Tests inject a fake authenticator; one unit test covers the real verifier with a locally minted token only if idtoken offers a test hook (it does not publicly; use `idtoken.WithHTTPClient`/custom keys is not supported, so unit-test our claim checks on a function taking `*idtoken.Payload`, not the signature step).
- Fail closed: any error returns 401 (bad/absent token) with no detail; 403 for email mismatch.

Rejected alternative: `coreos/go-oidc` v3.21.0. Works (`NewRemoteVerifier`/`oidc.NewProvider("https://accounts.google.com")`, `SkipClientIDCheck` off, check `email`), but we then own issuer/JWKS handling and claim structs; idtoken is Google-specific and smaller surface. Rejected alternative 2: shared-secret header (no OIDC): weaker, rotating secret in prod, contradicts ADR-0003.

### 2.2 Cloud Scheduler facts

- Free tier: per-billing-account free jobs; 3 free jobs, about $0.10 per extra job per month (pricing page excerpt did not show the number; figure matches BLUEPRINT assumption; VERIFY at [pricing](https://cloud.google.com/scheduler/pricing)). One tick job uses 1 of 3; plan the other two (nightly backup, spare). Jobs count per billing account, not per project.
- Cadence: cron `*/5 * * * *`, time zone `Etc/UTC` or `Asia/Kolkata`; irrelevant for interval cron.
- Attempt deadline for HTTP targets: 15 s to 30 min allowed; default 180 s ([Config Connector reference](https://docs.cloud.google.com/config-connector/docs/reference/resource-docs/cloudscheduler/cloudschedulerjob), 180 s default from [Ansible module docs](https://docs.ansible.com/ansible/latest/modules/gcp_cloudscheduler_job_module.html); not confirmed in a Google page, VERIFY).
- Retry config: `retry_count` (total tries), min/max backoff, max retry duration, max doublings; gcloud lists max-backoff default 3600 s ([gcloud reference](https://cloud.google.com/sdk/gcloud/reference/beta/scheduler/jobs/create/http?hl=ja)). Recommendation: `retry_count=0` or 1 with short backoff. Rationale: the next tick is 5 min away and handlers are idempotent; long retries just stack overlapping ticks. Scheduler treats any 2xx as success, so the tick must return 2xx even when individual jobs fail (they are retried by our table); return non-2xx only for infrastructure failure (DB down).
- Cloud Run request timeout (default 300 s, max 60 min per Cloud Run docs; VERIFY, docs host unreachable): keep tick work budget well under both. Recommend an internal tick budget of 60 s (context deadline), attempt deadline 120 s, so we never rely on Scheduler timeouts.
- Reliability: Cloud Scheduler is at-least-once; a tick can be skipped, delayed or duplicated. The design must not assume exactly one tick per 5 minutes: "due" is `run_at <= now()`, never "within the last 5 minutes".
- Cold start: scale to zero means the first request pays container start plus first DB connect (a few seconds). Set Scheduler attempt deadline above that; consider `min-instances=0` still (cost ₹0).
- CPU: Cloud Run request-based billing only gives CPU while a request is in flight. Do all tick work inside the request; never start goroutines that outlive the response (they get throttled). Run handlers synchronously, respond after drain.

### 2.3 Job queue: own table vs River (recommend for ADR-0003: own table now)

Recommendation: build a small `scheduled_jobs` module (about 300 lines + sqlc queries). Keep ADR-0003's "River can replace it later" by keeping module code behind `scheduler.Register(kind, handler)` and `scheduler.Enqueue(ctx, tx, Job)`; modules never touch the table.

Why not River v0.51.2 now:
- It can insert without running: omit `Queues` and do not call `Start` ("insert-only"); `InsertTx`/`InsertManyTx` give transactional enqueue ([river pkg](https://pkg.go.dev/github.com/riverqueue/river)). But insert-only means no one works jobs; running workers inside a request requires `Start` (long-lived fetch/notify/maintenance goroutines: elector, scheduler, cleaner, stuck-job rescuer) which fights scale-to-zero and request-only CPU. A "start, work until empty, stop on each tick" pattern is possible via `Start`+`Stop`, but is not a documented first-class mode; VERIFY with River docs (riverqueue.com unreachable) before relying on it.
- River needs its own migration set (`river_job`, `river_leader`, `river_queue`, etc.) via `rivermigrate`, which collides with our goose-owned migrations (needs wrapping River SQL in a goose file; ledger/auth schema rule requires owner OK for any schema change).
- Pooler: River's work coordinators use LISTEN/NOTIFY and need session pooling or a direct connection; insert-only clients need at least transaction pooling; if LISTEN/NOTIFY is unavailable the client falls back to poll-only ([River PgBouncer doc mirror](https://apis.emri.workers.dev/https-riverqueue.com/docs/pgbouncer), [issue 302](https://github.com/riverqueue/river/issues/302), [v0.48.0 notes](https://newreleases.io/project/github/riverqueue/river/release/v0.48.0)). Workable, but extra moving parts on 500 MB free Supabase.
- Our needs are small: ~100 clients, dozens to low hundreds of jobs/day, deadlines by run_at. River features (priorities, many queues, UI, periodic jobs) are unused. Dependency count and owner-approval cost outweigh benefits. Revisit if we move to an always-on VM (ADR-0003 already says so).

Rejected alternative: pg_cron / Supabase Cron in the database. Hidden outside the app, hard to fake-clock test, free-tier limits; contradicts ADR-0003.

#### Proposed schema (expand-only, goose `00002_scheduler.sql`)

```
scheduled_jobs(
  id uuid pk default uuidv7(),
  org_id uuid not null references orgs(id),
  kind text not null,                       -- registered handler name
  payload jsonb not null default '{}',
  dedupe_key text not null,                 -- e.g. 'ipo:<id>:open'
  run_at timestamptz not null,
  status text not null check (status in ('pending','running','done','failed','dead','cancelled')),
  attempts int not null default 0,
  max_attempts int not null default 8,
  locked_until timestamptz,                 -- lease
  locked_by text,                           -- instance id, diagnostics
  last_error text,
  created_at, updated_at timestamptz not null default now(),
  finished_at timestamptz
)
unique (org_id, kind, dedupe_key) where status in ('pending','running')   -- partial unique = per-job dedupe
index on (run_at) where status = 'pending'                                 -- claim index
index on (locked_until) where status = 'running'                           -- lease reaper
index on (org_id, kind, dedupe_key)                                        -- reschedule lookup
```

Claim (short transaction, then run handler outside it):

```
WITH c AS (
  SELECT id FROM scheduled_jobs
  WHERE (status='pending' AND run_at <= $now)
     OR (status='running' AND locked_until <= $now)      -- crashed/expired lease
  ORDER BY run_at
  LIMIT $batch
  FOR UPDATE SKIP LOCKED)
UPDATE scheduled_jobs j SET status='running', attempts=attempts+1,
  locked_until=$now + $lease, locked_by=$instance, updated_at=$now
FROM c WHERE j.id=c.id RETURNING j.*;
```

- Pass `$now` from the injected clock, not `now()`, so fake-clock tests work (decision: all scheduling timestamps come from the Go clock).
- Lease (visibility timeout): 2-5 min, longer than the handler budget; handlers needing more must be split. Semantics are at-least-once: a handler can run twice after lease expiry, so handlers must be idempotent (own dedupe via outbox `dedupe_key` unique constraint, or "state already reached" checks).
- Completion: `UPDATE ... SET status='done'` guarded by `WHERE id=$1 AND locked_by=$me AND status='running'` (fencing: a late finisher after lease loss does not clobber).
- Failure: `run_at = now + backoff(attempts)` (e.g. 1m, 5m, 15m, 1h, 3h with +/-20% jitter, capped), `status='pending'`; at `attempts >= max_attempts` set `status='dead'` (dead letter = same table; admin view and an audit_log/outbox admin alert row).
- Rescheduling for BR-IPO-03: `Reschedule(ctx, tx, kindPrefix/dedupeKey, newRunAt)` updates `run_at` of pending rows (and cancels now-irrelevant ones) in the same transaction as the IPO date change; if the row is `running`, the dedupe key lets the handler re-check the current IPO dates and no-op (handlers read the source-of-truth date, not the payload copy). Prefer payload = ids only.
- Append-only rule: `scheduled_jobs` is mutable operational state (not ledger/audit); the audit_log row for the business change remains the history. Confirm with owner that this table is exempt from `forbid_mutation()`.
- Periodic jobs (reminders every 1-2 h): handler of a "reminder.tick" kind re-enqueues itself, or better a fixed seed row per org kept by `EnsurePeriodic` at startup. Dedupe key includes the slot (`reminder:<task>:<yyyy-mm-ddThh>`) so a missed tick does not create a storm.

#### Supabase free and pgx

- Direct connection vs pooler: Supabase's shared pooler (Supavisor) port 5432 = session mode (prepared statements OK), port 6543 = transaction mode (no prepared statements; pgx defaults to a statement cache so it errors). The shared pooler is IPv4; direct connection is IPv6 only on free ([Supabase prepared-statement note](https://supabase.com/docs/guides/troubleshooting/disabling-prepared-statements-qL8lEL), [IPv4 changelog](https://supabase.com/changelog/17817-pgbouncer-and-ipv4-deprecation), [Supavisor FAQ](https://supabase.com/kb/troubleshooting/supavisor-faq-YyP5tI)). Cloud Run egress is IPv4 unless VPC egress is set up, so the production URL is likely the session-mode pooler on 5432 or transaction mode on 6543.
- Action: current `db.Open` uses defaults. If production uses 6543, set `cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe` or `QueryExecModeExec`/`SimpleProtocol` (pgx docs; VERIFY on pkg.go.dev/github.com/jackc/pgx/v5). That is a `platform/db` change; sqlc-generated code works with any mode. Decide by `DATABASE_URL` query param or env flag `DB_POOLER_TXN_MODE`.
- Compatible with transaction mode: `FOR UPDATE SKIP LOCKED`, `pg_advisory_xact_lock`, plain transactions. NOT compatible: session-level `pg_advisory_lock`, `LISTEN/NOTIFY` (not needed), session `SET`. So do not use session advisory locks for tick mutual exclusion; use the row lease.
- Connection limits: free-tier numbers not retrieved (supabase.com unreachable; VERIFY). Existing pool MaxConns=5 is fine; keep tick concurrency at most 2-3 handler goroutines and release the claim transaction before running handlers.
- Keep-alive: the 5-minute tick that runs a query prevents the free-project pause (blueprint section 9; ADR-0003).

### 2.4 Transactional outbox

Table `outbox` (same migration or `00003`): `id uuid pk uuidv7()` (time-ordered), `org_id`, `topic text` (e.g. `notification.push`, `event.ipo.status_changed`), `payload jsonb`, `dedupe_key text`, `available_at timestamptz` (quiet-hours deferral, backoff), `status` (`pending|sent|failed|dead`), `attempts`, `last_error`, `created_at`, `sent_at`. `unique (org_id, topic, dedupe_key)` gives "never the same reminder twice" (blueprint 7.2). Partial index `(available_at) where status='pending'`.

- Write path: modules call `outbox.Add(ctx, tx, msg)` inside the business transaction (and the audit_log row). `INSERT ... ON CONFLICT (org_id, topic, dedupe_key) DO NOTHING`.
- Relay: part of each tick (a built-in job kind `outbox.relay`): claim with the same `FOR UPDATE SKIP LOCKED` + lease pattern, in `id` order per org, deliver via a `Sender` interface registered per topic (push delivery is feature 013+; 002 ships the relay with a fake sender and the interface), mark sent. Delivery is at-least-once, so downstream push needs the dedupe key as an idempotency/collapse key.
- Ordering: only best-effort per org/topic (id order within a batch); `SKIP LOCKED` concurrent relays do not preserve global order. Strict ordering is not a business need here; document it. If strict per-entity order is needed later, add `ordering_key` and claim only the oldest pending per key.
- Retention: delete `sent` rows older than 14-30 days in a nightly maintenance job (also `done` scheduled_jobs older than 30 days; keep `dead` until acknowledged). 500 MB DB budget makes this mandatory. Because Notifications Inbox ("source of truth if push missed", blueprint) is a separate table owned by `notify`, outbox is transport, not history.
- Ref: [microservices.io transactional outbox](https://microservices.io/patterns/data/transactional-outbox.html) (pattern; polling publisher variant is what we use).

### 2.5 Clock and IST helpers

Recommendation: no third-party clock. Define in `internal/platform/clock`:
- `type Clock interface { Now() time.Time }` (or the repo convention `func() time.Time`, per `.claude/rules/api.md`; health already takes `time.Now` as a func). Use `type Func func() time.Time` plus a test `Fake` with `Set/Advance` guarded by a mutex. The tick, claim query and handlers all take `now` from it; code never calls `time.Now()` or SQL `now()` for business timing (add a depguard/forbidigo rule for `time.Now` outside `main`).
- Rejected alternative: `jonboulle/clockwork` v0.5.0 / `benbjohnson/clock` v1.3.5. They give fake timers/tickers (`Sleep`, `After`, `BlockUntil`), which we do not need: the scheduler is poll-by-tick, "time travel" is `fake.Advance(d); svc.Tick(ctx)`. benbjohnson/clock is stale (2023), clockwork is the maintained option if tickers are ever needed. A new dependency also needs owner approval. Alternatively Go's `testing/synctest` (stable since 1.25) fakes the stdlib clock for goroutine/timer code; not needed for DB-bound logic, and containers do not run inside a synctest bubble.
- Time-travel test pattern (integration, `dbtest.New`): seed IPO with dates, `Fake.Set(dayX 10:00 IST)`, call `Tick`, assert status/outbox rows; advance across open, close, listing; change IPO date and assert `run_at` moved (BR-IPO-03); run Tick twice at the same instant and assert no duplicate outbox rows; simulate crash by a handler that returns after claiming without finishing, advance past lease, assert re-run. Test names carry BR ids (`TestTick_BR_IPO_03_DateChangeReschedules`).
- Package `internal/platform/bizdate` (or in clock): `IST *time.Location` loaded once via `time.LoadLocation("Asia/Kolkata")`; `import _ "time/tzdata"` in the package (adds about 450 KB) because distroless has no `/usr/share/zoneinfo`. Alternative fixed `time.FixedZone("IST", 19800)` is DST-safe for India (no DST since 1945) and needs no tzdata, but `LoadLocation` + embedded tzdata is conventional and future-proof. Fail startup if load fails. Source: [time/tzdata](https://pkg.go.dev/time/tzdata) (stdlib: "importing this package embeds the timezone database").
- Helpers: `BusinessDate(t) civil date in IST`; `StartOfDayIST`; `AtISTClock(date, h, m)`; `AddWeekdays(date, n)` (skip Sat/Sun only); `DueDate(listingDate) = AddWeekdays(listingDate, 2)` at 23:59 IST (BR-SET-08). Persist `timestamptz` (UTC) for instants and `date` columns for business dates. Table test: Fri +2 = Tue; Sat/Sun input; month and year ends. Exchange holidays are NOT in BR-SET-08 (weekdays only): do not add a holiday calendar; flag as an open question for the owner only if business later wants it.
- DST: IST has none, so UTC storage + IST display has no ambiguous or skipped local times; the risk is only host-timezone leakage (`time.Local` in containers is UTC). Forbid `time.Local`/`time.Now().Date()` in business code.

## 3. Pitfalls, limits, costs

- Overlapping ticks (previous tick still running, Scheduler retry, or two instances): safe by design via `SKIP LOCKED` claim plus lease; the lease is what protects across a long handler, because a `FOR UPDATE` lock only lives for the claim transaction. Never hold the claim transaction open while running handlers (also pins a pooler connection).
- Long jobs exceeding tick: per-tick time budget (60 s), batch limit (e.g. 50 jobs), per-handler context timeout < lease. Jobs that exceed are split (fan out one job per client/IPO) rather than lengthened.
- Cloud Run instance shutdown mid-job (scale-down/SIGTERM after response): not an issue while work is inside the request; on SIGTERM cancel context and let lease expiry recover the job.
- Clock skew: all `run_at`/lease comparisons use one clock source per tick (`$now` parameter from the app clock); app instances on Cloud Run are NTP-synced, DB `now()` is only used for `created_at` defaults. In production the app clock is `time.Now`; do not mix DB `now()` in claim comparisons (would break fake-clock tests and split time sources).
- Job storm after downtime: a long outage makes many `run_at` rows due (and reminders stale). Catch-up policy: (1) per-tick batch cap so the backlog drains over a few ticks; (2) `ORDER BY run_at`; (3) time-sensitive kinds carry `expires_at` or a handler check "now > deadline: skip and mark `done` (expired)" so users do not get "closes in 1 hour" reminders after the IPO closed; (4) periodic reminders use slot dedupe keys so only the latest slot fires, not N missed ones; (5) state-change jobs (OPEN/CLOSED by dates) are always executed late, never skipped, since they are idempotent state reconciliation. A reconciler that sets the lifecycle from dates (not from "this job ran") is the safest way to meet BR-IPO-03.
- Dedupe vs reschedule race: dedupe key must be stable across reschedule (date not in key); use `ON CONFLICT (org_id, kind, dedupe_key) WHERE status IN ('pending','running') DO UPDATE SET run_at = EXCLUDED.run_at` for upsert.
- sqlc caveats: `FOR UPDATE SKIP LOCKED` inside CTE with `UPDATE ... FROM` is fine for sqlc/pgx; check `sqlc vet`. Partial unique index conflict targets need the `WHERE` repeated.
- iPhone PWA push: Web Push on iOS works only for a home-screen-installed PWA, iOS 16.4+, with user permission from a gesture ([pushpad iOS requirements](https://pushpad.xyz/blog/ios-special-requirements-for-web-push-notifications), already cited in blueprint). It is delivery-side (feature 013), so the outbox must not assume push success; the in-app Inbox is the source of truth.
- Costs: Cloud Scheduler within 3 free jobs, Cloud Run requests (8,640 tick calls/month) well inside the free tier; DB growth controlled by retention.
- Security: `/internal/*` must not appear in the generated public OpenAPI / TS client (`apps/api` `openapi` command feeds `packages/api-client`); register it with `Hidden: true` in Huma or on a separate mux path. Do not log tokens. Rate-limit is moot behind OIDC.

## 4. Existing code to reuse; golden reference

Golden module: `/home/user/divya-002/apps/api/internal/health` (`health.go` service with injected `now func() time.Time`, `handler.go` `Register(api huma.API, svc *Service)`, `health_test.go`). Copy that shape: new module `/home/user/divya-002/apps/api/internal/scheduler` with `service.go`, `handler.go`, `queries via sqlc`, tests; the clock/bizdate packages go under `internal/platform/` (must import no business module).

Reuse:
- `/home/user/divya-002/apps/api/cmd/divya/main.go`: `buildAPI` (register tick route there) and `serve` (ctx cancel on SIGTERM).
- `/home/user/divya-002/apps/api/internal/platform/httpx/httpx.go`: `NewAPI`, `NewServer`.
- `/home/user/divya-002/apps/api/internal/platform/config/config.go`: add `TickAudience`, `TickServiceAccount`, auth-mode validation (bypass only in `local|ci`).
- `/home/user/divya-002/apps/api/internal/platform/db/db.go`: pool (MaxConns 5); add exec-mode option for transaction pooler.
- `/home/user/divya-002/apps/api/internal/platform/dbtest/dbtest.go`: `dbtest.New(t)` (postgres:18-alpine, migrations applied) for claim/lease/time-travel tests.
- `/home/user/divya-002/apps/api/migrations/00001_platform.sql` (`orgs`, `audit_log`, `forbid_mutation()`); new migration `00002_scheduler.sql` (goose Up/Down, `uuidv7()`, `org_id`).
- `/home/user/divya-002/apps/api/queries/platform.sql` and `sqlc.yaml` (add `scheduler.sql`, regenerate `internal/platform/db/dbgen` or a module-local package per sqlc.yaml).
- Rules: `/home/user/divya-002/.claude/rules/api.md`, `/home/user/divya-002/.claude/rules/sql.md`.

## 5. Open questions for the owner

1. Approve new runtime dependency `google.golang.org/api` (idtoken) (CLAUDE.md requires asking). It pulls in a large module graph; fallback is `go-oidc` v3.21.0 (smaller), or hand-rolled JWKS verification (not recommended: security-sensitive).
2. Confirm `scheduled_jobs` and `outbox` are mutable operational tables (not append-only), with audit_log rows for business changes.
3. Production DB URL: Supabase session pooler (5432) or transaction pooler (6543)? Determines pgx exec mode.
4. Confirm weekday-only for BR-SET-08 (no NSE holiday calendar).
5. Public vs IAM-protected Cloud Run: confirm app-level OIDC on `/internal/*` with a publicly invokable service.

## 6. ADR-0003 wording suggestion

Append: "Implemented as an in-repo `scheduled_jobs` table with lease-based `SKIP LOCKED` claim and a transactional `outbox`; River v0.51.x evaluated and deferred (needs its own migrations, long-lived workers, LISTEN/NOTIFY session connections), revisit on always-on hosting."

## 7. Sources

- idtoken: https://pkg.go.dev/google.golang.org/api/idtoken
- Module versions: https://proxy.golang.org/google.golang.org/api/@latest , https://proxy.golang.org/github.com/riverqueue/river/@latest , https://proxy.golang.org/github.com/coreos/go-oidc/v3/@latest , https://proxy.golang.org/github.com/jonboulle/clockwork/@latest , https://proxy.golang.org/github.com/benbjohnson/clock/@latest
- River API: https://pkg.go.dev/github.com/riverqueue/river ; PgBouncer doc mirror https://apis.emri.workers.dev/https-riverqueue.com/docs/pgbouncer ; https://github.com/riverqueue/river/issues/302 ; https://newreleases.io/project/github/riverqueue/river/release/v0.48.0
- Cloud Scheduler: https://cloud.google.com/scheduler/pricing ; https://cloud.google.com/sdk/gcloud/reference/scheduler/jobs/create/http ; https://cloud.google.com/sdk/gcloud/reference/beta/scheduler/jobs/create/http?hl=ja ; https://docs.cloud.google.com/config-connector/docs/reference/resource-docs/cloudscheduler/cloudschedulerjob ; https://docs.ansible.com/ansible/latest/modules/gcp_cloudscheduler_job_module.html
- Supabase: https://supabase.com/docs/guides/troubleshooting/disabling-prepared-statements-qL8lEL ; https://supabase.com/kb/troubleshooting/supavisor-faq-YyP5tI ; https://supabase.com/changelog/17817-pgbouncer-and-ipv4-deprecation
- Outbox pattern: https://microservices.io/patterns/data/transactional-outbox.html
- tzdata: https://pkg.go.dev/time/tzdata
- iOS web push: https://pushpad.xyz/blog/ios-special-requirements-for-web-push-notifications
- Repo: docs/adr/0003-tick-scheduler.md, docs/product/business-rules.md (BR-IPO-03, BR-TASK-03, BR-SET-08), docs/product/product-blueprint.md section 7.

Items marked VERIFY need confirmation once docs.cloud.google.com, riverqueue.com and supabase.com are allowed in the session network settings: Scheduler attempt-deadline default, Cloud Run request timeout, Supabase free connection limits, River start/stop-per-request pattern, pgx exec-mode names, token `email` claims.
