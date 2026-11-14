# Tasks: Scheduler tick + scheduled jobs + outbox (fake-clock testable)

**Feature**: 002-scheduler-tick | **Branch**: `feat/002-scheduler-tick` | **Inputs**: spec.md, plan.md, data-model.md, contracts/*, research.md, quickstart.md
**Business rules covered**: BR-IPO-03, BR-TASK-03, BR-SET-08 (mechanism only, per spec); golden scenario GS-04. GS-01..03 must keep passing.

## Format

`- [ ] T### [P] Owner | deps | rules | files(<=5) | est. lines(<=200) | done command`

- **[P]**: parallel-safe (different files, no unmet dependency between the tasks in the same group).
- **Owner**: exactly one of `test-engineer` or `backend-dev`. Implementers never edit tests written by `test-engineer`.
- **Expected**: `red` = the done command is expected to FAIL (or not compile) until the paired implementation task lands; `green` = must pass when the task is done. Test tasks are done when the tests are written, fail for the right reason, and are committed.
- All done commands run from the worktree root `/home/user/swastik-002`. Integration tests need Docker and skip under `-short`.
- Generated sqlc output (`internal/platform/db/dbgen/*`) is not counted toward the size budget.
- Where a rule is "none", the task is mechanism and traces to FR/US ids listed in `Refs`.
- Gates from the plan: T022 touches `migrations/` (owner approval on the PR); T018 and T058 are security-sensitive (security reviewer on the PR).
- Failures found by the Phase 7 verification tests (T061-T065) go through the bug flow to `backend-dev`; `test-engineer` does not patch implementation.
- Note: `docs/engineering/git-workflow.md` named in the brief does not exist in this repository; Conventional Commits and small PRs from CLAUDE.md apply.


## Phase 1 - Setup and guard rails

- [x] T001 **backend-dev**: Add approved dependencies: go-oidc/v3 v3.21.0 (new), google/uuid (indirect to direct), pgregory.net/rapid (test-only). Owner approval recorded in the task brief.
  - Depends on: none
  - Rules: none (mechanism) | Refs: plan: Dependencies requiring owner approval
  - Files (2, ~30 lines): `apps/api/go.mod`, `apps/api/go.sum`
  - Done command (green): `cd apps/api && go mod verify && go list -m github.com/coreos/go-oidc/v3 github.com/google/uuid pgregory.net/rapid && go build ./...`
  - Note: go mod tidy is deferred to T068 (it would drop requirements not yet imported).
- [x] T002 [P] **test-engineer**: Write the deliberate-violation check: a script that drops temporary offending files (platform importing health/scheduler/outbox, outbox importing scheduler, go-oidc outside tickauth, time.Now/time.Local in business code) and asserts golangci-lint reports depguard/forbidigo for each, then cleans up. Cases for packages that do not exist yet are skipped.
  - Depends on: none
  - Rules: none (mechanism) | Refs: plan: Boundary rules; Risks (depguard negation syntax)
  - Files (1, ~90 lines): `scripts/check-lint-boundaries.sh`
  - Done command (red): `bash scripts/check-lint-boundaries.sh`
- [x] T003 **backend-dev**: Extend apps/api/.golangci.yml: depguard rules platform-is-a-leaf (health, scheduler, outbox), outbox-does-not-know-scheduler, oidc-only-in-tickauth; forbidigo for time.Now/Since/Until and time.Local with analyze-types, exclusions for cmd/, platform/clock, platform/httpx and _test.go.
  - Depends on: T002
  - Rules: none (mechanism) | Refs: constitution III, IV; FR-032
  - Files (1, ~40 lines): `apps/api/.golangci.yml`
  - Done command (green): `bash scripts/check-lint-boundaries.sh`

## Phase 2 - Platform packages (tests, then implementation)

- [x] T004 [P] **test-engineer**: Tests for clock: Fake Set/Advance/Now, race-safety with concurrent readers, Real returns UTC.
  - Depends on: none
  - Rules: none (mechanism) | Refs: FR-032
  - Files (1, ~60 lines): `apps/api/internal/platform/clock/clock_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/clock/...`
- [x] T005 **backend-dev**: Implement clock.Func, Real, Fake (mutex guarded).
  - Depends on: T004, T003
  - Rules: none (mechanism) | Refs: FR-032
  - Files (2, ~60 lines): `apps/api/internal/platform/clock/clock.go`, `apps/api/internal/platform/clock/fake.go`
  - Done command (green): `cd apps/api && go test -race ./internal/platform/clock/...`
- [x] T006 [P] **test-engineer**: Table tests for istdate: Date/DateOf/At, AddWeekdays and PaymentDue cases US4.1-10 (Fri 13 Nov 2026 to Tue 17 Nov 23:59 IST, Thu 12 Nov to Mon 16 Nov, year end, month end, holiday not skipped, Sat/Sun to Tue, 19:00 UTC to next IST date, 09:15 IST = 03:45 UTC), all repeated with time.Local forced to America/New_York.
  - Depends on: none
  - Rules: BR-SET-08, GS-04 | Refs: US4; FR-033; names TestDueDate_GS_04_*, TestPaymentDue_BR_SET_08_*, TestAddWeekdays_BR_SET_08_*, TestDateOf_BR_SET_08_ServerZoneIndependent
  - Files (1, ~170 lines): `apps/api/internal/platform/istdate/istdate_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/istdate/...`
- [x] T007 [P] **test-engineer**: Rapid property tests for AddWeekdays and PaymentDue (result is a weekday, exactly n weekdays in (d, result], composition, span bound, monotonic, PaymentDue always 23:59 IST on Mon or Tue-Fri).
  - Depends on: T001
  - Rules: BR-SET-08, GS-04 | Refs: TestAddWeekdays_BR_SET_08_Properties
  - Files (1, ~110 lines): `apps/api/internal/platform/istdate/istdate_prop_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/istdate/...`
- [x] T008 [P] **test-engineer**: Unit and rapid tests for QuietHours (Contains/Defer boundaries 21:29, 21:30 inclusive, 02:00, 07:59, 08:00 exclusive, non-wrapping window; Defer minimal and idempotent) and NextDailyAt.
  - Depends on: T001
  - Rules: BR-TASK-03 | Refs: US8.1-8.4; TestQuietHours_BR_TASK_03_*
  - Files (1, ~150 lines): `apps/api/internal/platform/istdate/quiet_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/istdate/...`
- [x] T009 **backend-dev**: Implement istdate.go: IST via embedded tzdata, Date, DateOf, At, Weekday, AddDays, ParseDate, AddWeekdays (n<0 error), PaymentDue (listing + 2 weekdays at 23:59 IST). 100% branch coverage on this package.
  - Depends on: T006, T007
  - Rules: BR-SET-08, GS-04 | Refs: FR-033; D1, D2
  - Files (1, ~130 lines): `apps/api/internal/platform/istdate/istdate.go`
  - Done command (green): `cd apps/api && go test -race -run 'GS_04|BR_SET_08|AddWeekdays|Date' ./internal/platform/istdate/...`
- [x] T010 **backend-dev**: Implement quiet.go: WallClock, QuietHours, DefaultQuietHours (21:30-08:00), Contains, Defer, NextDailyAt.
  - Depends on: T008, T009
  - Rules: BR-TASK-03 | Refs: FR-031; D3
  - Files (1, ~90 lines): `apps/api/internal/platform/istdate/quiet.go`
  - Done command (green): `cd apps/api && go test -race ./internal/platform/istdate/...`
- [x] T011 [P] **test-engineer**: Tests for retry: Backoff (deterministic per seed, non-decreasing, strictly increasing to cap, >= 48 s, <= 6 h) and ErrorText (truncate 500, redact Bearer tokens, JWT-shaped strings, PAN pattern).
  - Depends on: T001
  - Rules: none (mechanism) | Refs: FR-017, FR-018; constitution VI
  - Files (2, ~140 lines): `apps/api/internal/platform/retry/backoff_test.go`, `apps/api/internal/platform/retry/errtext_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/retry/...`
- [x] T012 **backend-dev**: Implement retry.Backoff and retry.ErrorText.
  - Depends on: T011
  - Rules: none (mechanism) | Refs: FR-017, FR-018
  - Files (2, ~90 lines): `apps/api/internal/platform/retry/backoff.go`, `apps/api/internal/platform/retry/errtext.go`
  - Done command (green): `cd apps/api && go test -race ./internal/platform/retry/...`
- [x] T013 [P] **test-engineer**: Config tests: Tick settings (TICK_AUTH, TICK_AUDIENCE, TICK_SERVICE_ACCOUNT, TICK_DEV_TOKEN): defaults per APP_ENV, dev refused in staging/production, invalid value, oidc without audience/service account allowed (tick disabled).
  - Depends on: none
  - Rules: none (mechanism) | Refs: FR-003; US2.5
  - Files (1, ~80 lines): `apps/api/internal/platform/config/config_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/config/...`
- [x] T014 **backend-dev**: Implement config Tick settings and validation (Load() error when TICK_AUTH=dev in staging/production).
  - Depends on: T013
  - Rules: none (mechanism) | Refs: FR-003; US2.5
  - Files (1, ~60 lines): `apps/api/internal/platform/config/config.go`
  - Done command (green): `cd apps/api && go test -race ./internal/platform/config/...`
- [x] T015 [P] **test-engineer**: Tests for tickauth contract: Authenticator interface, dev mode (constant-time compare, empty token rejects all, non-Bearer, missing header), Disabled.
  - Depends on: none
  - Rules: none (mechanism) | Refs: FR-002, FR-003; US2.1-2.4
  - Files (1, ~90 lines): `apps/api/internal/platform/tickauth/tickauth_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/tickauth/...`
- [x] T016 [P] **test-engineer**: Offline OIDC tests with httptest JWKS and locally minted RS256 tokens: missing/malformed/bad signature/unknown key/expired (fake clock)/wrong audience/wrong issuer/alg none and HS256, valid with wrong email or email_verified=false to ErrForbidden, valid to nil.
  - Depends on: T001
  - Rules: none (mechanism) | Refs: FR-002; US2.1-2.4; SC-004
  - Files (1, ~190 lines): `apps/api/internal/platform/tickauth/oidc_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/tickauth/...`
- [x] T017 **backend-dev**: Implement tickauth.go (Authenticator, ErrUnauthenticated, ErrForbidden, Disabled) and dev.go (NewDev).
  - Depends on: T015
  - Rules: none (mechanism) | Refs: FR-002, FR-003
  - Files (2, ~80 lines): `apps/api/internal/platform/tickauth/tickauth.go`, `apps/api/internal/platform/tickauth/dev.go`
  - Done command (green): `cd apps/api && go test -race -run 'Dev|Disabled|Contract' ./internal/platform/tickauth/...`
- [x] T018 **backend-dev**: Implement oidc.go: NewOIDC on coreos/go-oidc/v3 (RS256 only, Google issuers, audience, injected clock, email and email_verified check), WithJWKSURL, WithHTTPClient. go-oidc imported only here.
  - Depends on: T016, T017, T005
  - Rules: none (mechanism) | Refs: FR-002; SC-004
  - Files (1, ~120 lines): `apps/api/internal/platform/tickauth/oidc.go`
  - Done command (green): `cd apps/api && go test -race ./internal/platform/tickauth/...`
- [x] T019 [P] **test-engineer**: Tests for httpx: statusRecorder.Unwrap keeps http.ResponseController working, ExtendWriteDeadline moves the response deadline on a real httptest server.
  - Depends on: none
  - Rules: none (mechanism) | Refs: plan: Tick handler 90 s deadline
  - Files (1, ~80 lines): `apps/api/internal/platform/httpx/httpx_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/httpx/...`
- [x] T020 **backend-dev**: Implement statusRecorder.Unwrap and httpx.ExtendWriteDeadline(huma.Context, d); global WriteTimeout stays 30 s.
  - Depends on: T019
  - Rules: none (mechanism) | Refs: plan: Tick handler
  - Files (1, ~40 lines): `apps/api/internal/platform/httpx/httpx.go`
  - Done command (green): `cd apps/api && go test -race ./internal/platform/httpx/...`

## Phase 3 - Migration, sqlc, transactions, audit

- [x] T021 **test-engineer**: Migration test: after goose Up the four tables exist with the expected columns, check constraints, partial unique index (org, kind, dedupe_key) for active jobs, unique (org, topic, dedupe_key) for messages, no append-only trigger; Down drops them and leaves 00001 intact.
  - Depends on: T001
  - Rules: none (mechanism) | Refs: data-model 1-4; FR-009, FR-024, FR-034
  - Files (1, ~150 lines): `apps/api/internal/platform/db/migration_00002_test.go`
  - Done command (red): `cd apps/api && go test -race -run Migration00002 ./internal/platform/db/...`
- [x] T022 **backend-dev**: Migration 00002_scheduler_outbox.sql (goose Up/Down; scheduled_jobs, outbox_messages, quiet_hours, tick_runs, indexes) and sqlc.yaml uuid overrides (uuid.UUID, uuid.NullUUID); regenerate dbgen. PR needs owner approval for migrations/.
  - Depends on: T021
  - Rules: none (mechanism) | Refs: data-model; K10
  - Files (2, ~190 lines): `apps/api/migrations/00002_scheduler_outbox.sql`, `apps/api/sqlc.yaml`
  - Done command (green): `cd apps/api && sqlc vet && sqlc generate && go build ./... && go test -race ./internal/platform/db/...`
  - Note: Generated dbgen files are excluded from the size budget.
  - Note (test-engineer): `tx_test.go` (T023) and `migration_00002_test.go` (T021) share one test binary in `internal/platform/db`, so `go test ./internal/platform/db/...` compiles only once T024 (`ErrNoTx`, `TxRunner`, `MustTx`) exists. Do T022 and T024 together, or verify T022 with a temporary stub; the T021 tests were validated against a scratch schema built from data-model.md.
- [x] T023 **test-engineer**: Tests for TxRunner: InTx commit, rollback on error and panic, nested InTx joins the outer transaction, Conn falls back to pool, MustTx without tx returns ErrNoTx.
  - Depends on: T022
  - Rules: none (mechanism) | Refs: FR-008, FR-023; US1.10
  - Files (1, ~110 lines): `apps/api/internal/platform/db/tx_test.go`
  - Done command (red): `cd apps/api && go test -race -run Tx ./internal/platform/db/...`
- [x] T024 **backend-dev**: Implement db/tx.go: TxRunner, NewTxRunner, InTx, Conn, MustTx, ErrNoTx.
  - Depends on: T023
  - Rules: none (mechanism) | Refs: FR-008; K9
  - Files (1, ~90 lines): `apps/api/internal/platform/db/tx.go`
  - Done command (green): `cd apps/api && go test -race -run Tx ./internal/platform/db/...`
- [x] T025 [P] **test-engineer**: Tests for audit.Write: row written inside a transaction with actor, entity, before/after JSON; without a transaction returns ErrNoTx; rolled back with the caller.
  - Depends on: T022
  - Rules: none (mechanism) | Refs: FR-020; constitution II
  - Files (1, ~90 lines): `apps/api/internal/platform/audit/audit_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/audit/...`
- [x] T026 **backend-dev**: Add InsertAuditLog to queries/platform.sql and implement audit.Actor, Entry, Write.
  - Depends on: T025, T024
  - Rules: none (mechanism) | Refs: FR-020
  - Files (2, ~80 lines): `apps/api/queries/platform.sql`, `apps/api/internal/platform/audit/audit.go`
  - Done command (green): `cd apps/api && sqlc vet && sqlc generate && go test -race ./internal/platform/audit/...`
- [x] T027 **backend-dev**: queries/scheduler.sql (scheduling and execution): UpsertJob, RescheduleJob, CancelJobByKey, CancelJobsBySubject, ExpireDueJobs, ClaimJob (SKIP LOCKED, lease, token), CompleteJob, FailJob, MarkJobDead, EnsureRecurringJob. All time as $now/$lease parameters.
  - Depends on: T022
  - Rules: BR-IPO-03 | Refs: data-model 1; FR-009-FR-016, FR-032
  - Files (1, ~190 lines): `apps/api/queries/scheduler.sql`
  - Done command (green): `cd apps/api && sqlc vet && sqlc generate && go build ./...`
- [x] T028 **backend-dev**: queries/scheduler_admin.sql: InsertTickRun, FinishTickRun, LastTickRun, ListFailedJobs, CountJobsByState, GetJobForOrg, AdminRetryJob, AdminCancelJob, SelectUnalertedDeadJobs, MarkJobsAlerted, PurgeFinishedJobs, PurgeTickRuns.
  - Depends on: T027
  - Rules: none (mechanism) | Refs: data-model 1, 4; FR-006, FR-019-FR-021, FR-035
  - Files (1, ~170 lines): `apps/api/queries/scheduler_admin.sql`
  - Done command (green): `cd apps/api && sqlc vet && sqlc generate && go build ./...`
  - Note: Split from scheduler.sql in plan to keep each task under 200 lines; same sqlc directory. Query tasks are chained because sqlc generate rewrites shared dbgen files.
- [ ] T029 **backend-dev**: queries/outbox.sql: InsertMessage (ON CONFLICT DO NOTHING), WithdrawByKey, WithdrawBySubject, ExpireDueMessages, ClaimMessage (ordered), MarkMessageSent, FailMessage, MarkMessageDead, GetQuietHours, UpsertQuietHours.
  - Depends on: T028
  - Rules: BR-TASK-03 | Refs: data-model 2-3; FR-023-FR-031
  - Files (1, ~170 lines): `apps/api/queries/outbox.sql`
  - Done command (green): `cd apps/api && sqlc vet && sqlc generate && go build ./...`
- [ ] T030 **backend-dev**: queries/outbox_admin.sql: ListFailedMessages, CountMessagesByState, GetMessageForOrg, AdminRetryMessage, AdminCancelMessage, SelectUnalertedDeadMessages, MarkMessagesAlerted, PurgeFinishedMessages.
  - Depends on: T029
  - Rules: none (mechanism) | Refs: data-model 2; FR-019, FR-020, FR-035
  - Files (1, ~130 lines): `apps/api/queries/outbox_admin.sql`
  - Done command (green): `cd apps/api && sqlc vet && sqlc generate && go build ./...`

## Phase 4 - Outbox module

- [x] T031 [P] **test-engineer**: outboxtest.Sender test double: records envelopes, FailNext(n), FailAlways(), counts deliveries per dedupe key.
  - Depends on: none
  - Rules: none (mechanism) | Refs: FR-029
  - Files (1, ~70 lines): `apps/api/internal/outbox/outboxtest/sender.go`
  - Done command (red): `cd apps/api && gofmt -l internal/outbox/outboxtest/ | (! grep .)`
  - Note (test-engineer): outboxtest.Sender also has OnSend(hook), FailWith(err), Calls(), Count(), Recover(), Reset(). Because it is a non-test file that imports `internal/outbox`, `go build ./...` fails until T036 adds Envelope/Sender; backend tasks whose done command is `go build ./...` before T036 must exclude it (`go build $(go list ./... | grep -v /outboxtest)`).
  - Note: Compiles only after Envelope/Sender exist (T036); gofmt check is the standalone done command, full compile is exercised by T032-T034.
- [x] T032 **test-engineer**: Outbox record/withdraw tests: Add commits with the caller and vanishes on rollback, ErrNoTx, duplicate key ignored (also after sent), withdraw by key and by subject (stop-on-complete), channel-agnostic topics.
  - Depends on: T031, T022
  - Rules: BR-TASK-03 | Refs: US3.1, 3.3, 3.5, 3.7, 8.6; FR-023, FR-024, FR-027
  - Files (1, ~180 lines): `apps/api/internal/outbox/outbox_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestOutbox_BR_TASK_03_(Add|Dedupe|Withdraw|Channel)' ./internal/outbox/...`
  - Note (test-engineer): also owns `apps/api/internal/outbox/helpers_test.go` (shared fixtures; T033, T034, T038 compile against it and against `errChange`/`jsonEqual` in outbox_test.go). All outbox tests are one compile unit: T036 must also provide compiling stubs for Relay, RelayBudget, RelaySummary, SweepDead, CountDead, ListFailedMessages, RetryMessage, CancelMessage, FailedFilter, FailedMessage, Page, DeadGroup, ErrInvalid/ErrNotFound/ErrConflict so that its done command can run before T037/T039. Assumed names are listed at the top of helpers_test.go and admin_test.go.
- [x] T033 **test-engineer**: Outbox relay tests: relay once and sent_at, crash between send and mark resends with same key, no sender fails, retry grows and is re-deferred out of quiet hours, expiry never sent, per-recipient order, overlapping relay never double-sends.
  - Depends on: T031, T022
  - Rules: BR-TASK-03 | Refs: US3.2, 3.4, 3.6, 7.3, 8.7; FR-017, FR-025, FR-026, FR-028, FR-030; SC-008
  - Files (1, ~190 lines): `apps/api/internal/outbox/relay_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestOutbox_BR_TASK_03_Relay' ./internal/outbox/...`
- [x] T034 [P] **test-engineer**: Quiet-hours tests with fake clock: default 21:30-08:00, message inside window available at 08:00, exempt immediate, boundaries, per-org setting applies only to new messages and writes audit_log.
  - Depends on: T022
  - Rules: BR-TASK-03 | Refs: US8.1-8.9; FR-031
  - Files (1, ~150 lines): `apps/api/internal/outbox/quiet_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestQuietHours_BR_TASK_03 ./internal/outbox/...`
  - Note (test-engineer): the file splits by implementation task. `TestQuietHours_BR_TASK_03_*` (settings: QuietHours/SetQuietHours + audit) is what T035's done command runs; `TestQuietHoursAvailability_BR_TASK_03_*` (Add computes available_at, incl. a rapid property for SC-008) needs T036; `TestQuietHoursRelay_BR_TASK_03_*` needs T037. T037's done command (`...|TestQuietHours`) covers all three. T035 cannot compile before the Service type from T036 exists; implement T035 and T036 together.
- [ ] T035 **backend-dev**: Implement outbox/quiet.go: QuietHours(org) with default, SetQuietHours with audit in same transaction, availableAt helper using istdate.QuietHours.Defer.
  - Depends on: T034, T010, T026, T029
  - Rules: BR-TASK-03 | Refs: FR-031
  - Files (1, ~90 lines): `apps/api/internal/outbox/quiet.go`
  - Done command (green): `cd apps/api && go test -race -run TestQuietHours_BR_TASK_03 ./internal/outbox/...`
- [ ] T036 **backend-dev**: Implement outbox service.go and store.go: Options/DefaultOptions, New, types (Message, Recipient, Envelope, Sender), RegisterSender, Add (requires tx, quiet-hours availability), Withdraw, WithdrawSubject.
  - Depends on: T032, T035, T024, T005
  - Rules: BR-TASK-03 | Refs: FR-023, FR-024, FR-027; K9
  - Files (2, ~190 lines): `apps/api/internal/outbox/service.go`, `apps/api/internal/outbox/store.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestOutbox_BR_TASK_03_(Add|Dedupe|Withdraw|Channel)' ./internal/outbox/...`
- [ ] T037 **backend-dev**: Implement outbox/relay.go: Relay with expiry sweep, one-row claims, Send outside a transaction with timeout, fenced MarkMessageSent, FailMessage with backoff and quiet-hours deferral, dead after max attempts, RelayBudget/RelaySummary.
  - Depends on: T033, T036, T012, T030
  - Rules: BR-TASK-03 | Refs: FR-017, FR-025, FR-026, FR-028
  - Files (1, ~190 lines): `apps/api/internal/outbox/relay.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestOutbox_BR_TASK_03_Relay|TestQuietHours' ./internal/outbox/...`
- [x] T038 [P] **test-engineer**: Outbox admin tests: list retrying/dead with required fields and no secrets, retry (fresh attempts + audit), cancel + audit, retry of expired still expires, other org invisible (404), non-dead conflict, dead counts, SweepDead groups per org and excludes admin.* topics.
  - Depends on: T022
  - Rules: none (mechanism) | Refs: US6.4-6.9; FR-019, FR-020; SC-007
  - Files (1, ~190 lines): `apps/api/internal/outbox/admin_test.go`
  - Done command (red): `cd apps/api && go test -race -run Admin ./internal/outbox/...`
- [ ] T039 **backend-dev**: Implement outbox/admin.go: ListFailedMessages, RetryMessage, CancelMessage (audit in same tx), CountDead, SweepDead.
  - Depends on: T038, T037
  - Rules: none (mechanism) | Refs: FR-019, FR-020
  - Files (1, ~170 lines): `apps/api/internal/outbox/admin.go`
  - Done command (green): `cd apps/api && go test -race ./internal/outbox/...`

## Phase 5 - Scheduler module

- [ ] T040 **test-engineer**: schedulertest harness: NewHarness(t) (one dbtest container, org, fake clock, outbox + scheduler services, outboxtest.Sender), RunTicks(from, to, step), Reset() truncating the four tables, helpers to seed jobs and read rows.
  - Depends on: T031
  - Rules: none (mechanism) | Refs: plan: Fixtures; FR-032
  - Files (1, ~160 lines): `apps/api/internal/scheduler/schedulertest/harness.go`
  - Done command (red): `cd apps/api && gofmt -l internal/scheduler/schedulertest/ | (! grep .)`
  - Note: Compiles after T045; full use is exercised by T041-T044.
- [ ] T041 **test-engineer**: Schedule API tests: upsert latest wins, rollback leaves no job, ErrNoTx, ErrUnknownKind, done key makes a new row, reschedule/cancel/CancelSubject commit vs rollback, into the past, no-op on missing key, reschedule while running ends pending at the new time.
  - Depends on: T040, T022
  - Rules: BR-IPO-03 | Refs: US1.9, 1.10, 5.1-5.7; FR-008, FR-009, FR-013
  - Files (1, ~190 lines): `apps/api/internal/scheduler/schedule_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestSchedule_BR_IPO_03 ./internal/scheduler/...`
- [ ] T042 [P] **test-engineer**: Tick tests (happy path): due jobs run once in oldest-first order, none before run_at, per-tick caps and budget, idle tick records a tick_runs row, recurring catch-up runs once, unknown kind fails, status overdue at 15 minutes.
  - Depends on: T040, T022
  - Rules: BR-IPO-03 | Refs: US1.1-1.3, 1.7, 1.8, 6.8, 7.6; FR-005, FR-006, FR-010, FR-015, FR-016, FR-021; TestTick_BR_IPO_03_LateNotSkipped
  - Files (1, ~190 lines): `apps/api/internal/scheduler/tick_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestTick_' ./internal/scheduler/...`
- [ ] T043 [P] **test-engineer**: Tick failure tests: handler error retries with growing waits then dead, panic recovered with sanitised last_error, expiry skips effect while state-change jobs always run, last_error never carries secrets, completion atomic with handler effects.
  - Depends on: T040, T022
  - Rules: BR-IPO-03 | Refs: US6.1-6.3; FR-012, FR-014, FR-017, FR-018
  - Files (1, ~190 lines): `apps/api/internal/scheduler/tick_failure_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestTickFailure_' ./internal/scheduler/...`
- [ ] T044 [P] **test-engineer**: Lease and crash tests: claim via store and never finish is skipped until lease end then rerun; effect written by a separate tx then crash gives one message via dedupe; lost lease rolls back handler effects; stale lock_token cannot complete.
  - Depends on: T040, T022
  - Rules: none (mechanism) | Refs: US1.5, 1.6; FR-011, FR-012
  - Files (1, ~160 lines): `apps/api/internal/scheduler/lease_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestLease_' ./internal/scheduler/...`
- [ ] T045 **backend-dev**: Implement scheduler registry.go and service.go: Options/DefaultOptions, New, Register (panics on duplicate), KindOptions, Job/JobSpec/TickSummary/Status types, sentinel errors.
  - Depends on: T036, T024
  - Rules: none (mechanism) | Refs: FR-008; K9
  - Files (2, ~150 lines): `apps/api/internal/scheduler/registry.go`, `apps/api/internal/scheduler/service.go`
  - Done command (green): `cd apps/api && go build ./internal/scheduler/...`
- [ ] T046 **backend-dev**: Implement scheduler store.go and schedule.go: Schedule (upsert, requires tx, payload <= 8 KB), Reschedule (never creates; rerun when running), Cancel, CancelSubject.
  - Depends on: T041, T045, T027
  - Rules: BR-IPO-03 | Refs: FR-008, FR-009, FR-013; K3
  - Files (2, ~190 lines): `apps/api/internal/scheduler/store.go`, `apps/api/internal/scheduler/schedule.go`
  - Done command (green): `cd apps/api && go test -race -run TestSchedule_BR_IPO_03 ./internal/scheduler/...`
- [ ] T047 **backend-dev**: Implement scheduler run.go: execute one claimed job (handler timeout, panic recovery, InTx with fenced CompleteJob, FailJob with retry.Backoff and sanitised ErrorText, MarkJobDead, lease-expired-too-often dead, lost-lease rollback, cancel_requested).
  - Depends on: T043, T044, T046, T012
  - Rules: BR-IPO-03 | Refs: FR-011, FR-012, FR-017, FR-018
  - Files (1, ~190 lines): `apps/api/internal/scheduler/run.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestTickFailure_|TestLease_' ./internal/scheduler/...`
- [ ] T048 **backend-dev**: Implement scheduler tick.go: Tick orchestration (tick_runs row, EnsureRecurringJob, expiry sweep, claim loop with caps and budgets, outbox.Relay, FinishTickRun, error to 503 semantics, TickSummary, structured logging).
  - Depends on: T042, T047, T037, T028
  - Rules: BR-IPO-03 | Refs: FR-005, FR-006, FR-007, FR-010, FR-015, FR-016
  - Files (1, ~190 lines): `apps/api/internal/scheduler/tick.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestTick_|TestTickFailure_|TestLease_|TestSchedule_' ./internal/scheduler/...`
- [ ] T049 [P] **test-engineer**: Dead-alert tests: one admin.jobs_dead per org per tick with correct payload, not repeated next tick, admin.* dead messages never alert, failed sweep retried next tick, retry then dies again alerts again, other orgs separate.
  - Depends on: T040, T022
  - Rules: none (mechanism) | Refs: contracts/outbox-events.md; K8; FR-019
  - Files (1, ~150 lines): `apps/api/internal/scheduler/alert_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestAlert_ ./internal/scheduler/...`
- [ ] T050 **backend-dev**: Implement scheduler alert.go: dead-item sweep in one transaction (unalerted dead jobs + outbox.SweepDead grouped by org) emitting outbox topic admin.jobs_dead with alerted_at marking; call it from Tick.
  - Depends on: T049, T048, T039
  - Rules: none (mechanism) | Refs: K8; contracts/outbox-events.md
  - Files (2, ~120 lines): `apps/api/internal/scheduler/alert.go`, `apps/api/internal/scheduler/tick.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestAlert_|TestTick_' ./internal/scheduler/...`
- [ ] T051 [P] **test-engineer**: Retention tests: scheduler.retention is recurring daily 03:00 IST per org, purges finished jobs/messages older than 30 days and old tick_runs, keeps dead items, runs once on catch-up.
  - Depends on: T040, T022
  - Rules: none (mechanism) | Refs: FR-035; FR-015
  - Files (1, ~110 lines): `apps/api/internal/scheduler/retention_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestRetention_ ./internal/scheduler/...`
- [ ] T052 **backend-dev**: Implement scheduler retention.go: RetentionHandler and KindOptions.Recurring using istdate.NextDailyAt; purge queries.
  - Depends on: T051, T048
  - Rules: none (mechanism) | Refs: FR-035
  - Files (1, ~90 lines): `apps/api/internal/scheduler/retention.go`
  - Done command (green): `cd apps/api && go test -race -run TestRetention_ ./internal/scheduler/...`
- [ ] T053 [P] **test-engineer**: Scheduler admin and status tests: ListFailedJobs fields and no secrets, RetryJob (fresh attempts + audit), CancelJob + audit, retry of expired still expires, other org invisible, non-dead conflict, Status last tick / overdue / dead counts.
  - Depends on: T040, T022
  - Rules: none (mechanism) | Refs: US6.4-6.9; FR-019-FR-021; SC-007
  - Files (1, ~190 lines): `apps/api/internal/scheduler/admin_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestAdmin_ ./internal/scheduler/...`
- [ ] T054 **backend-dev**: Implement scheduler admin.go: Status, ListFailedJobs, RetryJob, CancelJob (audit in same tx, org-scoped).
  - Depends on: T053, T048
  - Rules: none (mechanism) | Refs: FR-019, FR-020, FR-021
  - Files (1, ~170 lines): `apps/api/internal/scheduler/admin.go`
  - Done command (green): `cd apps/api && go test -race -run TestAdmin_ ./internal/scheduler/...`
- [ ] T055 [P] **test-engineer**: Tick handler tests with humatest: 401 (WWW-Authenticate: Bearer) and 403 do zero work (counting fake ticker), 200 TickSummary shape, 503 on DB error, no token in captured logs, Hidden operation.
  - Depends on: T017
  - Rules: none (mechanism) | Refs: US2.1-2.4, 2.7; FR-002, FR-004, FR-007
  - Files (1, ~150 lines): `apps/api/internal/scheduler/handler_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestHandler_ ./internal/scheduler/...`
- [ ] T056 **backend-dev**: Implement scheduler handler.go: RegisterTick(api, ticker, authenticator) with Hidden:true, auth before any DB work, 90 s write deadline, RFC 9457 errors, log reason only.
  - Depends on: T055, T018, T020, T048
  - Rules: none (mechanism) | Refs: FR-001, FR-002, FR-004, FR-007
  - Files (1, ~130 lines): `apps/api/internal/scheduler/handler.go`
  - Done command (green): `cd apps/api && go test -race -run TestHandler_ ./internal/scheduler/...`

## Phase 6 - Wiring, lint, deploy variables

- [ ] T057 [P] **test-engineer**: Contract test in cmd/swastik: generated OpenAPI contains no /internal path and no scheduler-tick operation; document builds with zero dependencies (no DB, no network).
  - Depends on: none
  - Rules: none (mechanism) | Refs: US2.6; FR-004; constitution V
  - Files (1, ~70 lines): `apps/api/cmd/swastik/main_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestOpenAPI_NoInternalOperations ./cmd/swastik/...`
- [ ] T058 **backend-dev**: Wire cmd/swastik/main.go: buildAPI takes a deps struct, serve builds TxRunner, outbox, scheduler (register scheduler.retention), selectTickAuth (go-oidc bound to serve context; Disabled + WARN when unconfigured), RegisterTick; config errors stop the process.
  - Depends on: T057, T056, T052, T014, T018, T005, T020
  - Rules: none (mechanism) | Refs: K6; plan: Wiring; FR-001-FR-004
  - Files (1, ~150 lines): `apps/api/cmd/swastik/main.go`
  - Done command (green): `cd apps/api && go build ./... && go test -race ./cmd/swastik/... && go run ./cmd/swastik openapi | diff - ../../packages/api-client/openapi.yaml`
- [ ] T059 [P] **backend-dev**: deploy.yml: add TICK_AUDIENCE and TICK_SERVICE_ACCOUNT from repository variables to --set-env-vars (empty values keep the tick disabled).
  - Depends on: T014
  - Rules: none (mechanism) | Refs: plan: Configuration; K6
  - Files (1, ~10 lines): `.github/workflows/deploy.yml`
  - Done command (green): `grep -q 'TICK_AUDIENCE=${{ vars.TICK_AUDIENCE }}' .github/workflows/deploy.yml && grep -q 'TICK_SERVICE_ACCOUNT=${{ vars.TICK_SERVICE_ACCOUNT }}' .github/workflows/deploy.yml && python3 -c "import yaml;yaml.safe_load(open('.github/workflows/deploy.yml'))"`
- [ ] T060 **backend-dev**: Re-run boundary and lint gates with scheduler/outbox present: fix any findings; deliberate-violation check covers all cases.
  - Depends on: T058, T003
  - Rules: none (mechanism) | Refs: constitution III, IV
  - Files (1, ~30 lines): `apps/api/.golangci.yml`
  - Done command (green): `cd apps/api && golangci-lint run ./... && cd .. && bash scripts/check-lint-boundaries.sh`
  - Note: Files listed are the only ones expected to change; most fixes land in the module that violates the rule.

## Phase 7 - Cross-cutting verification, docs, status

- [ ] T061 [P] **test-engineer**: Time-travel lifecycle test: Sun 8 Nov to Thu 19 Nov 2026 in 5-minute ticks with sample IPO step jobs, reminder series (never in quiet hours, stop on done), payment-due job at PaymentDue(13 Nov) runs at the first tick after Tue 17 Nov 23:59 IST; listing moved to Mon 16 Nov on Thu 12 Nov gives Wed 18 Nov 23:59; clock is the only time source; runs < 60 s.
  - Depends on: T048, T050, T052
  - Rules: BR-IPO-03, BR-TASK-03, BR-SET-08, GS-04 | Refs: US9.1-9.3; SC-006
  - Files (1, ~190 lines): `apps/api/internal/scheduler/lifecycle_test.go`
  - Done command (green): `cd apps/api && go test -race -run TestLifecycle_BR_IPO_03_BR_SET_08_GS_04_TwoWeeks -v ./internal/scheduler/...`
- [ ] T062 [P] **test-engineer**: Catch-up tests: 6-hour outage and 500-job backlog drained within 3 ticks, state-change jobs done, stale reminders expire and are never sent, recurring runs once.
  - Depends on: T048, T050, T052
  - Rules: BR-IPO-03, BR-TASK-03 | Refs: US7.1-7.5; SC-003
  - Files (1, ~170 lines): `apps/api/internal/scheduler/catchup_test.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestCatchUp_' ./internal/scheduler/...`
- [ ] T063 [P] **test-engineer**: Overlapping-tick concurrency test: two and five concurrent Tick goroutines at the same fake instant over 10 and 100 due jobs and messages; every effect exactly once, summed counts equal totals.
  - Depends on: T048, T050, T052
  - Rules: none (mechanism) | Refs: US1.4, 3.8; FR-011, FR-026
  - Files (1, ~140 lines): `apps/api/internal/scheduler/concurrency_test.go`
  - Done command (green): `cd apps/api && go test -race -run TestTick_Concurrent -count=20 ./internal/scheduler/...`
- [ ] T064 [P] **test-engineer**: Rapid state-machine model test of the scheduler against an in-memory model (schedule/reschedule/cancel/advance/tick/crash-after-claim/fail): no effect twice, never before run_at, <= 1 active row per key, running implies lease and token, dead iff attempts >= max.
  - Depends on: T048, T050, T052
  - Rules: BR-IPO-03 | Refs: FR-009-FR-016
  - Files (1, ~190 lines): `apps/api/internal/scheduler/model_test.go`
  - Done command (green): `cd apps/api && go test -race -run TestScheduler_BR_IPO_03_ModelProperties -rapid.checks=50 ./internal/scheduler/...`
- [ ] T065 [P] **test-engineer**: Short soak test TestSoak_SC_001: 3-day slice in PR (30 days, 100 clients, 10 IPOs when SWASTIK_SOAK=full) with random duplicate/overlapping/skipped ticks and crashes; skipped under -short.
  - Depends on: T048, T050, T052
  - Rules: BR-IPO-03, BR-TASK-03 | Refs: SC-001
  - Files (1, ~170 lines): `apps/api/internal/scheduler/soak_test.go`
  - Done command (green): `cd apps/api && go test -race -run TestSoak_SC_001 ./internal/scheduler/...`
- [ ] T066 [P] **backend-dev**: nightly.yml: add a job running SWASTIK_SOAK=full go test -run TestSoak_SC_001 -timeout 20m ./internal/scheduler/...
  - Depends on: T065
  - Rules: none (mechanism) | Refs: quickstart section 3
  - Files (1, ~30 lines): `.github/workflows/nightly.yml`
  - Done command (green): `grep -q 'SWASTIK_SOAK=full' .github/workflows/nightly.yml && python3 -c "import yaml;yaml.safe_load(open('.github/workflows/nightly.yml'))"`
- [ ] T067 [P] **backend-dev**: Docs and status: package docs (doc.go) for scheduler and outbox showing how owning features call Schedule/Add inside a transaction, handler idempotency contract; docs/product/modules.md row 002 status moved to match the pipeline.
  - Depends on: T058
  - Rules: none (mechanism) | Refs: CLAUDE.md: update modules.md status
  - Files (3, ~80 lines): `apps/api/internal/scheduler/doc.go`, `apps/api/internal/outbox/doc.go`, `docs/product/modules.md`
  - Done command (green): `cd apps/api && go doc ./internal/scheduler >/dev/null && go doc ./internal/outbox >/dev/null && cd .. && grep -n '^| 002 ' docs/product/modules.md`
  - Note: ADR-0003 Amendment 1 is already committed with the plan; nothing to change. Adding BR-SET-08 to the Rules column of the 002 row is an owner/conductor call (plan, Traceability); left unchanged here.
- [ ] T068 **backend-dev**: Final gate: go mod tidy leaves go.mod/go.sum unchanged, full API suite with -race, golangci-lint, sqlc vet, short tests, golden scenarios GS-01..04, traceability script. Paste output as evidence.
  - Depends on: T058, T060, T061, T062, T063, T064, T065, T059, T066, T067
  - Rules: BR-IPO-03, BR-TASK-03, BR-SET-08, GS-04 | Refs: SC-001-SC-008; quickstart sections 1-4, 6
  - Files (1, ~20 lines): `apps/api/go.mod`
  - Done command (green): `cd apps/api && go mod tidy && git diff --exit-code go.mod go.sum && go test -race ./... && go test -short ./... && golangci-lint run ./... && sqlc vet && cd .. && bash scripts/check-br-traceability.sh`

## Phase B - blocked until 003 merges

Status of every task below: `blocked` (needs 003 auth merged to main; admin role and principal come from 003). Service-level capabilities, audit and tests already ship in Phase A. If 003 lands first these can join the same PR.

- [ ] T069 [P] **test-engineer** (blocked): Admin handler tests for scheduler (humatest): 401/403 without admin role, status, failed-job list filters and cursor, retry/cancel 200/404/409, Idempotency-Key replay returns current item with no second audit row.
  - Depends on: T068 and 003 merged
  - Rules: none (mechanism) | Refs: Phase B; FR-019-FR-022
  - Files (1, ~190 lines): `apps/api/internal/scheduler/admin_handler_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestAdminHTTP_ ./internal/scheduler/...`
- [ ] T070 [P] **test-engineer** (blocked): Admin handler tests for outbox (humatest): same matrix for /v1/admin/outbox/messages list, retry and cancel.
  - Depends on: T068 and 003 merged
  - Rules: none (mechanism) | Refs: Phase B; FR-019-FR-022
  - Files (1, ~170 lines): `apps/api/internal/outbox/admin_handler_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestAdminHTTP_ ./internal/outbox/...`
- [ ] T071 **backend-dev** (blocked): Add FindAuditByIdempotencyKey query and audit lookup for Idempotency-Key replay.
  - Depends on: T068 and 003 merged
  - Rules: none (mechanism) | Refs: Phase B
  - Files (2, ~70 lines): `apps/api/queries/platform.sql`, `apps/api/internal/platform/audit/audit.go`
  - Done command (green): `cd apps/api && sqlc vet && sqlc generate && go test -race ./internal/platform/audit/...`
- [ ] T072 **backend-dev** (blocked): Implement scheduler/admin_handler.go: RegisterAdmin with scheduler-get-status, scheduler-list-failed-jobs, scheduler-retry-job, scheduler-cancel-job; org and actor from 003's principal.
  - Depends on: T069, T071 and 003 merged
  - Rules: none (mechanism) | Refs: Phase B; FR-022
  - Files (1, ~190 lines): `apps/api/internal/scheduler/admin_handler.go`
  - Done command (green): `cd apps/api && go test -race -run TestAdminHTTP_ ./internal/scheduler/...`
- [ ] T073 **backend-dev** (blocked): Implement outbox/admin_handler.go: RegisterAdmin with the four outbox operations.
  - Depends on: T070, T071 and 003 merged
  - Rules: none (mechanism) | Refs: Phase B; FR-022
  - Files (1, ~170 lines): `apps/api/internal/outbox/admin_handler.go`
  - Done command (green): `cd apps/api && go test -race -run TestAdminHTTP_ ./internal/outbox/...`
- [ ] T074 [P] **test-engineer** (blocked): Contract test: admin operations present with security admin; /internal still absent; additive-only OpenAPI diff (oasdiff).
  - Depends on: T068 and 003 merged
  - Rules: none (mechanism) | Refs: constitution V; Phase B
  - Files (1, ~80 lines): `apps/api/cmd/swastik/admin_contract_test.go`
  - Done command (red): `cd apps/api && go test -race -run TestOpenAPI_AdminOperations ./cmd/swastik/...`
- [ ] T075 **backend-dev** (blocked): Wire RegisterAdmin for both modules after 003 middleware, regenerate packages/api-client/openapi.yaml and src/schema.ts.
  - Depends on: T072, T073, T074 and 003 merged
  - Rules: none (mechanism) | Refs: Phase B; constitution V
  - Files (3, ~60 lines): `apps/api/cmd/swastik/main.go`, `packages/api-client/openapi.yaml`, `packages/api-client/src/schema.ts`
  - Done command (green): `cd apps/api && go test -race ./cmd/swastik/... && go run ./cmd/swastik openapi | diff - ../../packages/api-client/openapi.yaml && cd .. && pnpm --filter @swastik/api-client generate && pnpm turbo run typecheck`

## Dependencies and execution order

- T001 (dependencies) and T002 (lint check) start immediately; T003 follows T002.
- Platform packages (Phase 2) are independent of each other except T005 (needs lint exclusions T003), T010 (needs T009), T018 (needs T017, T005).
- Migration T022 gates all store code; query tasks T027-T030 are chained because `sqlc generate` rewrites shared files.
- Outbox (Phase 4) is built before the scheduler because the tick relays through it and the dead sweep emits into it.
- Within each module: tests (TE) then implementation (BE) then wiring (T058).

## Parallel groups (Phase A)

- G1 (no dependencies): T002, T004, T006, T013, T015, T019, T031, T057.
- G2 (after T001): T007, T008, T011, T016, T021. T040 follows T031.
- G3 (implementations, each after its tests): T005, T009, T012, T014, T017, T020.
- G4 (after T022): T025, T034, T038, T041, T042, T043, T044, T049, T051, T053 (test files in distinct files and packages).
- G5 (after T048/T050/T052): T061, T062, T063, T064, T065, plus T059, T066, T067 [P] with them.

## Implementation strategy

1. MVP: Phase 1-2 (pure functions incl. GS-04) is demonstrable alone (`go test ./internal/platform/istdate/...`).
2. Then Phase 3 (data), Phase 4 (outbox), Phase 5 (scheduler), Phase 6 (wiring), Phase 7 (proof).
3. Phase B ships only after 003 merges.
