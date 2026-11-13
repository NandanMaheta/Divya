# Implementation Plan: IPO catalogue (CRUD, mainboard only, APPLY / DO NOT APPLY, lifecycle by dates)

**Branch**: `feat/008-ipo-catalogue` | **Date**: 2026-11-13 | **Spec**: [spec.md](./spec.md) (approved 2026-11-13 with D-01…D-10)

**Input**: Feature specification from `specs/008-ipo-catalogue/spec.md`

**Plan artifacts**:

- [research.md](./research.md) §7 (planning decisions R-01…R-10)
- [data-model.md](./data-model.md)
- [contracts/ipos.openapi.yaml](./contracts/ipos.openapi.yaml)
- [contracts/go-module-api.md](./contracts/go-module-api.md)
- [contracts/error-codes.md](./contracts/error-codes.md)
- [quickstart.md](./quickstart.md)
- ADR [0008](../../docs/adr/0008-in-transaction-module-signals.md) (new, proposed with this plan)

**Rules**: BR-IPO-01, BR-IPO-02, BR-IPO-03. **Golden scenarios**: none directly. GS-01…GS-04 must keep passing. GS-04 later consumes `listing_date` through 018.

## Summary

A new Go module `internal/ipos`, shaped like `internal/health`, owns one table `ipos` (migration `00004_ipos.sql`). It provides:

- **Admin operations** (admin role, 003): create, PUT facts, set flag and note, publish/unpublish, status override with reason, delete never-published drafts, list with filters, and date suggestions.
- **Client operations** (any signed-in user): list and fetch published IPOs only.

The rules are enforced as follows:

- **BR-IPO-01**: the service maps the stated segment (SME → `ipo.sme_not_allowed`). The DB backs it with `board = 'MAINBOARD'` and `exchanges ⊆ {NSE,BSE}` CHECKs.
- **BR-IPO-02**:
  - nullable `apply_flag` while a draft, with a client-visible note;
  - a publish gate (FR-020) in the service and a DB CHECK;
  - `Eligible() = published && APPLY`;
  - `RequireEligible` (FOR SHARE) for 010/012.
- **BR-IPO-03**:
  - Four idempotent step jobs on 002's scheduler: `ipo.open` 10:00, `ipo.close` 17:00, `ipo.allotment` 10:00, `ipo.list` 10:00 IST, keyed `ipo:<id>:<step>`.
  - They are scheduled, moved or cancelled in the transaction of the change that sets the dates.
  - Each step only moves forward, re-reads the current dates, and is suppressed after a manual override by an `armed_at` vs `last_override_at` check (R-02).
  - SETTLED comes only from the admin or from 019 via `MarkSettled`.

Dependents are told through three in-transaction listener interfaces: `DatesChanged`, `EligibilityChanged` and `StatusChanged` (ADR 0008). A listener failure rolls back the change. Every mutation writes exactly one `audit_log` row through 002's `audit.Write`. Errors are RFC 9457 with 003's stable `code`. Mutations accept `Idempotency-Key`, and edits are guarded by `version`.

No new dependency. No outbox event. No UI (screens are 022/023/025). The only frontend changes are the regenerated `@divya/api-client` and IPO constants and error codes in `@divya/domain`.

## Technical Context

**Language/Version**: Go 1.27 (API). TypeScript only for generated client types and `packages/domain` constants.

**Primary Dependencies**: Huma v2.40.0, pgx v5.13.0, sqlc, goose v3.30.1, `github.com/google/uuid` (direct since 002), `pgregory.net/rapid` (test-only, since 002), testcontainers-go. From 002: `scheduler`, `platform/{clock,istdate,db (TxRunner),audit}`. From 003: `platform/authz`, `platform/httpx` (`Problem`, `ErrorModel.code`). **No new module.**

**Storage**: PostgreSQL 18 (Supabase), one new table `ipos` plus a delete-guard trigger. The existing `audit_log` is used. `scheduled_jobs` is used only through 002's Go API.

**Testing**:

- `go test -race`: table tests, rapid properties, `dbtest.New(t)` integration with 002's `schedulertest` harness (fake clock plus real `Tick`), `humatest` handlers with 003's `authz.Install` and a fake verifier.
- `cmd/divya` contract tests, oasdiff in CI.
- vitest for `packages/domain`.

**Target Platform**: Cloud Run (Mumbai), a single `divya serve` binary. Ticks come from Cloud Scheduler every 5 minutes (ADR 0003).

**Project Type**: modular-monolith web service (Go API). The TS packages are generated or constant-only.

**Performance Goals**: p95 under 200 ms in Mumbai for IPO list and detail with 500 stored IPOs (SC-008, constitution VII). A step handler is a single-row transaction.

**Constraints**:

- Money is `int64` paise only. Bounds keep every paise value and `lot × price` below 2^53, so they are JSON-safe for TS.
- IST civil `date` columns. Instants come from `istdate.Date.At`.
- No `time.Now` in business code (002 forbidigo).
- Every mutation is audited in the same transaction.
- Modules do not touch each other's tables.

**Scale/Scope**: about 1 operator, about 100 clients, tens of IPOs a year (a few hundred rows), four scheduler jobs per IPO.

No `NEEDS CLARIFICATION` remains. The spec's Q1–Q6 were settled by D-01…D-10. The technical choices are in research.md §7.

## Constitution Check

*GATE: checked before Phase 0 and re-checked after Phase 1.*

| Principle | How this plan complies | Result |
|---|---|---|
| I. Business rules are the source of truth | Every behaviour traces to BR-IPO-01/02/03, FR-001…FR-061 or D-01…D-10. Test names carry `BR_IPO_01/02/03` (table below), so the traceability script passes once 008 is `done`. Quotas were requested in the task brief but are excluded because the approved spec lists them as a non-goal (R-09). | PASS |
| II. Money exact, history immutable | Paise are `bigint`/`int64` with range CHECKs. The minimum application value is an exact integer product with no rounding. No ledger. `ipos` is mutable reference data, and its history is the append-only `audit_log` (one row per mutation, typed before/after). Published IPOs cannot be deleted (service plus trigger, D-08). | PASS |
| III. Test-first, layered, deterministic | `test-engineer` writes the tests first. Layers: static, unit, property (rapid), integration (real PG18, fake clock, real ticks), handler, contract. The clock is injected (`clock.Func`). Time-travel tests cover the 09:59/10:00 and 16:59/17:00 boundaries and catch-up. | PASS |
| IV. Modular monolith, hard boundaries | `internal/ipos` imports only `platform/*`, `scheduler`, huma, uuid, and pgx in `store.go`; a depguard strict allow-list enforces this. Dependents import `ipos`, never the reverse. Signals are exported listener interfaces (ADR 0008). Jobs go only through 002's API. `audit_log` is read only for idempotency replay (it is a platform table). | PASS |
| V. Contract-first APIs | 12 additive operations. RFC 9457 with stable codes. `Idempotency-Key` on every mutation. `version` optimistic locking. OpenAPI and TS client regenerated; oasdiff non-breaking. | PASS |
| VI. Security and privacy by default | Admin operations use `authz.Require(RoleAdmin)`, which means the admin has passed TOTP, because 003 issues admin access tokens only after MFA. Client operations use `authz.Require()`. Drafts look like unknown ids (404). The client DTO is a separate type with no admin fields. URLs are https-only. Problems never echo values. No PII in this table. | PASS |
| VII. Simple, fast, accessible | One table, no joins, keyset pagination, two partial and composite indexes. Indian formatting in messages (`platform/inr`). No new dependency. | PASS |
| Workflow 5/7 | The migration PR needs owner approval (CODEOWNERS). No ledger or auth schema change. No new runtime dependency. | PASS (gated) |

Post-design re-check (after data-model and contracts): no violations. There is one minor deviation from a 002 convention (job payload carries `armed_at`), justified under Complexity Tracking.

## Coordination with 002 (binding) and 003 (in progress)

| # | Topic | Binding source | 008 usage |
|---|---|---|---|
| C-1 | Scheduler | 002 `contracts/go-module-api.md` | `Register(kind, Handler, KindOptions{})`, `Schedule(ctx, JobSpec)` (tx-only upsert), `Cancel`, `CancelSubject`. Handlers run in the job transaction (`db.MustTx`). `ExpiresAt = nil` (state change always runs late). |
| C-2 | Clock and dates | 002 `platform/clock`, `platform/istdate` | `Deps.Now clock.Func`; `istdate.Date.At(h, m)` for step instants; `istdate.AddWeekdays` for suggestions (FR-043); `istdate.ParseDate`. |
| C-3 | Transactions | 002 `db.TxRunner` (`InTx` nested join, `Conn`, `MustTx`) | Every mutation is `InTx`. Listeners and `MarkSettled`/`RequireEligible` join the caller's transaction. |
| C-4 | Audit | 002 `audit.Write(ctx, Entry{OrgID, Actor, Entity, EntityID, Action, Before, After})`, plus 003 C-4's additive `Entry.At` | `At` is set from the clock when available. Tests assert ordering by `id` (uuidv7) and the `after.scheduled_for` contents, so they do not depend on `At` landing. |
| C-5 | AuthZ | 003 `platform/authz`: `Require(roles...)`, `MustPrincipal(ctx)`, `Principal{UserID, OrgID, SessionID uuid.UUID; Role}`, `Principal.AuditActor()`, `AssertDeclared` | Org and actor always come from the principal and never from the request. Admin operation ids are appended to `cmd/divya/testdata/admin-operations.txt` (003 contract walk). |
| C-6 | Errors | 003 P-10 `httpx.Problem(status, code, detail)`, `ErrorModel.code`, `type = urn:divya:problem:<code>`, `Value` stripped | IPO codes are namespaced `ipo.*` (contracts/error-codes.md). The brief's `sme-not-allowed`, `date-order` and `publish-incomplete` become `ipo.sme_not_allowed`, `ipo.date_order` and `ipo.publish_incomplete` to match 003's code style. |
| C-7 | Migration number | 002 = `00002`, 003 = `00003` (003 P-18) | 008 = `00004_ipos.sql`. goose rejects an older unapplied version once a newer one is applied, so **008 merges after 003**. If 003 slips past 008, 008 waits; it does not renumber. |
| C-8 | Shared files | `cmd/divya/main.go`, `.golangci.yml`, `queries/` (002/003 also edit) | 008 adds new files and appends small blocks: one wiring block, one depguard rule and a `platform-is-a-leaf` entry. It rebases onto `stage` before its PR. |

**Merge order**: 002 Phase A → 003 → 008. Phase A of 008 below needs only 002 Phase A, so it can be built and tested while 003 is in review. The 008 PR as a whole merges after 003 (C-7, and the HTTP layer needs `authz`). 002 Phase B is not required.

## Key decisions

| ID | Decision | Detail |
|---|---|---|
| K-01 | One wide `ipos` row. Nullable facts until known. Publish gate in the service plus a DB CHECK. | data-model §1, §4 |
| K-02 | Segment recognised only by the stated segment. API strings mapped by the service (no schema enum) to get `ipo.sme_not_allowed` / `ipo.invalid_segment`. Storage `board` + `exchanges` CHECKs. | R-05, FR-010…012 |
| K-03 | Four step kinds. Handler: lock, recompute instant from the current date, forward-only, single step, no-op writes nothing. Ordering across steps comes from 002's `run_at` claim order and strictly increasing instants. | R-01, data-model §3 |
| K-04 | FR-036 suppression: payload `armed_at` vs `ipos.last_override_at`. A date change re-arms. Race-free against a step already waiting on the row lock. | R-02 |
| K-05 | Signals are typed listener interfaces called in the transaction, in registration order. An error or panic rolls back. `RejectedError{Code}` → 409. | R-03, ADR 0008 |
| K-06 | Idempotency: create uses the `create_idempotency_key` unique index. Other mutations lock the row, then look up the audit row by key and action. Replay returns current state (200, or 204 for delete). | R-04 |
| K-07 | PUT replaces the facts (all keys required, values nullable). Separate operations for flag, publish, unpublish, status and delete. A PUT or flag call that changes nothing is a 200 with no audit. Publish when published, unpublish when draft, or override to the same status gives `ipo.no_change`. | R-06 |
| K-08 | Date order is checked over the transitive closure of FR-040 for the dates that are set. DB CHECKs cover the direct pairs. | R-07 |
| K-09 | `internal/platform/inr.Format(paise)` for Indian amounts in `detail`. Warnings also carry raw paise `params`. | R-08 |
| K-10 | Hard delete of never-published drafts, with `CancelSubject`, an audit snapshot, and a trigger guard. | R-10 |
| K-11 | Lifecycle runs regardless of `published` (D-07). Publishing never touches steps. | D-07 |
| K-12 | `MarkSettled` (019): LISTED → SETTLED, source `settlement`, actor system. Already SETTLED is a no-op. Otherwise `NotListedError`. | FR-033 |
| K-13 | 026 support is limited to the columns (`source`, `source_ref`, `last_synced_at`, `field_sources`) and the unique index. `Upsert` is 026's task, built on the same validation and steps. | spec Non-goals |

## Module placement and dependencies

```text
cmd/divya ──> internal/ipos ──> internal/scheduler (002: Schedule/Cancel/CancelSubject/Register types)
                           ──> internal/platform/{db, db/dbgen, audit, clock, istdate, authz (003), httpx (003), inr (new)}
          ──> ipos.RegisterJobs(sch, ipoSvc); ipos.Register(api, ipoSvc)   (after authz.Install, before AssertDeclared)
future 009/010/011/012/013/018/019/026 ──> internal/ipos (Get, RequireEligible, MarkSettled, On*Changed listeners)
internal/platform/* ──X──> internal/ipos           internal/scheduler, internal/outbox ──X──> internal/ipos
```

depguard additions to `apps/api/.golangci.yml`, applied on top of 002 and 003:

```yaml
platform-is-a-leaf:            # append
  deny: [ { pkg: "github.com/NandanMaheta/divya/apps/api/internal/ipos", desc: platform packages must not import business modules } ]
ipos-boundaries:
  list-mode: strict
  files: ["**/internal/ipos/**", "!$test"]
  allow:
    - $gostd
    - github.com/danielgtaylor/huma/v2
    - github.com/google/uuid
    - github.com/jackc/pgx/v5            # narrowed by the next rule
    - github.com/NandanMaheta/divya/apps/api/internal/platform
    - github.com/NandanMaheta/divya/apps/api/internal/scheduler
ipos-pgx-only-in-store:
  files: ["**/internal/ipos/**", "!**/internal/ipos/store.go", "!$test"]
  deny: [ { pkg: "github.com/jackc/pgx/v5", desc: pgx types stay in store.go (go-module-pattern) } ]
scheduler-does-not-know-owners:
  files: ["**/internal/scheduler/**", "**/internal/outbox/**"]
  deny: [ { pkg: "github.com/NandanMaheta/divya/apps/api/internal/ipos", desc: owners depend on the scheduler, never the reverse } ]
```

The implementer proves each rule with a deliberate violation that fails `golangci-lint run ./...` and is then reverted (same practice as 002). Convention, checked by review: `internal/ipos` calls only `queries/ipos.sql` queries plus 002's audit writer.

## Data model changes

Full design: [data-model.md](./data-model.md).

| Object | Kind | Highlights |
|---|---|---|
| `ipos` | new table | `org_id` FK; `board CHECK = 'MAINBOARD'`; `exchanges text[]` CHECK ⊆ {NSE,BSE}, 1–2 values, distinct; paise `bigint` with range CHECKs; band both-or-neither and `floor <= cap`; `lot_size` 1–1,000,000; six IST `date` columns with direct-order CHECKs; `apply_flag` CHECK (nullable), `flag_note` ≤ 500 chars and only with a flag; `published` and `first_published_at` with a publish-complete CHECK; `status` CHECK of six values, `status_source` CHECK, `status_changed_at`, `last_override_at`; `source`/`source_ref`/`last_synced_at`/`field_sources jsonb` with consistency CHECKs; `create_idempotency_key`; `version` |
| `ipos_admin_list_idx`, `ipos_published_list_idx` (partial `WHERE published`) | indexes | `(org_id, open_date DESC NULLS FIRST, id DESC)` keyset |
| `ipos_source_ref_uq`, `ipos_create_idem_uq` | partial unique | 026 upserts; create replay |
| `ipos_forbid_published_delete()` + trigger | function and trigger | D-08 defence in depth |
| append-only triggers | none new | `ipos` is mutable. History lives in `audit_log` (existing `forbid_mutation()`) |

The migration is expand-only and backward compatible: a new table that the previous app version does not read. Queries are in `apps/api/queries/ipos.sql` (data-model §6). After editing: `sqlc generate && sqlc vet`.

## API operations (OpenAPI diff)

Additive only: 12 operations, tag `IPOs`, schemas `IPOStatus`, `ApplyFlag`, `StatusSource`, `IPODates`, `IPODraft`, `IPOFacts`, `AdminIPO`, `ClientIPO`, `Warning`, `IPOMutationResult`, `AdminIPOPage`, `ClientIPOPage`, `DateSuggestion`, `VersionBody`. Full contract: [contracts/ipos.openapi.yaml](./contracts/ipos.openapi.yaml). Codes: [contracts/error-codes.md](./contracts/error-codes.md).

| operationId | Method and path | Security | Request | Success | Errors (`code`) | Idempotency |
|---|---|---|---|---|---|---|
| ipos-create | POST /v1/admin/ipos | bearer [admin] | `IPODraft` (name and segments required) | 201 `IPOMutationResult` (200 on replay) | 401, 403, 422 `validation_failed` / `ipo.sme_not_allowed` / `ipo.invalid_segment` / `ipo.invalid_value` / `ipo.date_order` | `Idempotency-Key`; unique `(org, key)` |
| ipos-admin-list | GET /v1/admin/ipos?status&published&flag&month&limit&cursor | bearer [admin] | query | 200 `AdminIPOPage` | 401, 403, 422 | n/a |
| ipos-admin-get | GET /v1/admin/ipos/{ipoId} | bearer [admin] | | 200 `AdminIPO` | 401, 403, 404 | n/a |
| ipos-update | PUT /v1/admin/ipos/{ipoId} | bearer [admin] | `IPODraft` + `version`, all keys required | 200 `IPOMutationResult` | 401, 403, 404, 409 `ipo.stale_version` / listener, 422 (+ `ipo.publish_incomplete`), 500 | key; audit replay |
| ipos-set-flag | PUT /v1/admin/ipos/{ipoId}/flag | bearer [admin] | `{version, apply_flag\|null, flag_note\|null}` | 200 | 401, 403, 404, 409, 422 `ipo.invalid_value` / `ipo.publish_incomplete`, 500 | key; audit replay |
| ipos-publish | POST /v1/admin/ipos/{ipoId}/publish | bearer [admin] | `{version}` | 200 (warnings possible, US3.9) | 401, 403, 404, 409 `ipo.no_change` / `ipo.stale_version` / listener, 422 `ipo.publish_incomplete`, 500 | key; audit replay |
| ipos-unpublish | POST /v1/admin/ipos/{ipoId}/unpublish | bearer [admin] | `{version}` | 200 | 401, 403, 404, 409, 500 | key; audit replay |
| ipos-override-status | POST /v1/admin/ipos/{ipoId}/status | bearer [admin] | `{version, status, reason (1–500)}` | 200 | 401, 403, 404, 409 `ipo.no_change` / `ipo.stale_version` / listener, 422, 500 | key; audit replay |
| ipos-delete | DELETE /v1/admin/ipos/{ipoId}?version= | bearer [admin] | | 204 | 401, 403, 404, 409 `ipo.published_not_deletable` / `ipo.stale_version` | key; audit replay → 204 |
| ipos-suggest-dates | GET /v1/admin/ipos/date-suggestions?close_date= | bearer [admin] | query `date` | 200 `DateSuggestion` | 401, 403, 422 | read-only |
| ipos-list | GET /v1/ipos?status&limit&cursor | bearer [] (any signed-in) | query | 200 `ClientIPOPage` (published only) | 401, 422 | n/a |
| ipos-get | GET /v1/ipos/{ipoId} | bearer [] | | 200 `ClientIPO` | 401, 404 `not_found` (draft = unknown) | n/a |

Notes:

- Go 1.22+ `ServeMux` precedence routes the literal `/v1/admin/ipos/date-suggestions` before `/{ipoId}`. A handler test pins this.
- The cursor is opaque: base64url of `{open_date|null, id}`. A malformed cursor gives 422 `validation_failed`. `limit` defaults to 50, maximum 200.
- Admin lists order by `open_date DESC NULLS FIRST, id DESC` (FR-009). The client list uses the same order.
- Steps after the code lands: `go run ./cmd/divya openapi > packages/api-client/openapi.yaml`, then `pnpm --filter @divya/api-client generate`. Commit both. oasdiff must be non-breaking.

## Scheduler jobs and outbox events

| Kind | Key / subject | Fires at (IST) | Registered by | Handler effect |
|---|---|---|---|---|
| `ipo.open` | `ipo:<id>:open` / `ipo:<id>` | `open_date` 10:00 | `ipos.RegisterJobs` | → OPEN if rules pass (data-model §3) |
| `ipo.close` | `ipo:<id>:close` | `close_date` 17:00 | same | → CLOSED |
| `ipo.allotment` | `ipo:<id>:allotment` | `allotment_date` 10:00 | same | → ALLOTMENT_OUT |
| `ipo.list` | `ipo:<id>:list` | `listing_date` 10:00 | same | → LISTED |

- `KindOptions{}`: 8 attempts, not recurring. `ExpiresAt = nil`.
- Payload: `{ipo_id, armed_at}`.
- Precision: the first tick at or after the instant, about 5 minutes (ADR 0003).
- Handler failure (DB or listener): 002 retries with backoff. Then the job goes `dead` and the existing `admin.jobs_dead` alert fires.
- **Outbox events: none.** "New IPO" or "IPO opened" notifications belong to 013/014, which can add them from a `StatusChanged` or `EligibilityChanged` listener.

## Signals to dependent features (in-transaction, ADR 0008)

| Signal | Fired when | Payload | Expected consumers |
|---|---|---|---|
| `DatesChanged` | an update changed at least one of the six dates; never on create | org, IPO id, old and new `Dates`, actor, at | 012 (apply windows), 013 (reminders), 018 (due dates) |
| `EligibilityChanged` | `published && APPLY` flips (publish, unpublish, flag) | org, IPO id, new value, cause, actor, at | 010/011 (deals), 012 (tasks) |
| `StatusChanged` | every real status change (step, override, 019) | org, IPO id, from, to, source, actor, at, scheduled_for | 012 (BR-TASK-02 final after close), 013, 018, 019 |

Order inside a mutation transaction:

1. Lock the row (`FOR UPDATE`).
2. Idempotency check.
3. Validate.
4. Write the row (version+1).
5. Step diff (`Schedule`/`Cancel`).
6. `audit.Write`.
7. Listeners: DatesChanged → EligibilityChanged → StatusChanged.
8. Commit.

## Validation and errors (summary)

Hard checks (422):

- segment (`ipo.sme_not_allowed` / `ipo.invalid_segment`);
- schema (`validation_failed`);
- cross-field values (`ipo.invalid_value`);
- date-order closure (`ipo.date_order`);
- publish completeness, on publish and on any edit or flag change while published (`ipo.publish_incomplete`, listing every missing item).

Conflicts (409): `ipo.stale_version`, `ipo.no_change`, `ipo.published_not_deletable`, listener rejection.

The only soft warning is `ipo.min_application_out_of_range` (outside ₹10,000–₹20,000, bounds inclusive). It is returned on create, update and publish, and never blocks.

Validation is pure (`validate.go`) so it can be unit- and property-tested. The service maps typed errors to problems in `problems.go`.

## Test strategy

`test-engineer` writes all tests first. Implementers do not edit them. Test names include rule IDs. Integration tests use one container per top-level test, a **fresh org per subtest** (org-scoped queries give isolation), skip under `-short`, and use 002's `schedulertest.NewHarness(t)` (fake clock, real `scheduler.Tick`). The `internal/ipos/ipostest` package (non-test, reusable by 010/012/013/018/019) provides `NewService(h)`, `RecordingListener` (records events and the transaction-visible row state), `FailingListener` (error, panic or `RejectedError`), and an `ExampleIPO` builder for the spec's "Acme Ltd" (open Wed 11 Nov 2026, close Fri 13 Nov, allotment Mon 16 Nov, refund and credit Tue 17 Nov, listing Wed 18 Nov).

| Layer | Where | What | Rule / spec IDs |
|---|---|---|---|
| Static | repo, CI | golangci-lint (depguard rules above, forbidigo `time.Now`, exhaustive on `Status`), `sqlc vet`, generated code current, OpenAPI diff plus oasdiff, `scripts/check-br-traceability.sh` | IV, V, I |
| Unit | `ipos/validate_test.go` | `TestValidate_BR_IPO_01_RejectsSMESegments` (NSE_EMERGE, BSE_SME, mixed with mainboard); `TestValidate_BR_IPO_01_UnknownOrDuplicateSegment`; `TestValidate_BR_IPO_01_AcceptsMainboardCombos`; `TestValidate_BR_IPO_01_NameWithSMEIsAccepted`; `TestValidate_PriceBand` (half band, floor > cap, floor = cap fixed price); `TestValidate_BR_IPO_03_DateOrder` (table incl. open = close OK, allotment = close rejected, listing before allotment, closure open+listing without close); `TestValidate_BR_IPO_02_PublishRequirements` (each missing item listed; final price without band OK) | BR-IPO-01/02/03; FR-003, FR-010, FR-012, FR-020, FR-040; US1.5–1.7, US2.*, US3.1–3.2, US5.9 |
| Unit | `ipos/model_test.go` | `TestMinApplication_FR004` (final price wins over cap; none → unknown); `TestWarning_FR005_Bounds` (₹9,999 / ₹10,000 / ₹20,000 / ₹20,001 / ₹25,000 detail text); `TestEligible_BR_IPO_02`; `TestStatusRank` | BR-IPO-02; FR-004/005/023; US1.2–1.4 |
| Unit | `ipos/lifecycle_test.go` | `TestStepInstant_BR_IPO_03_ISTTimes` (10:00/17:00 IST → UTC 04:30/11:30; never day-shifted); `TestStepDecision_BR_IPO_03` table over (status, date, now, armed_at, last_override_at): forward only, date nil, instant in future, override suppression and re-arm, already at or past target; `TestStepDiff_BR_IPO_03` (changed → Schedule, cleared → Cancel, unchanged → none, refund/credit → none) | BR-IPO-03; FR-031/032/036/041 |
| Unit | `ipos/suggest_test.go` | `TestSuggestDates_BR_IPO_03_WeekdaysOnly` (Fri 13 → Mon 16/Tue 17/Wed 18; Mon 9 → Tue 10/Wed 11/Thu 12; Wed 11 → Thu 12/Fri 13/Mon 16; notice present) | BR-IPO-03; FR-043; US8.1–8.3 |
| Unit | `platform/inr/inr_test.go` | `₹14,700`, `₹1,48,500`, `₹10,00,00,000`, `₹0.50`, `₹1,000.05`, negative, `math.MaxInt64` | FR-061, constitution VII |
| Property (rapid) | `ipos/validate_prop_test.go` | `TestDateOrder_BR_IPO_03_Properties`: on random partial date sets, the validator accepts iff a brute-force closure model accepts, and rejection names only fields of violated pairs. `TestMinApplication_Properties`: exact `lot × price`, no overflow within bounds, value ≤ 2^53. `TestSegments_BR_IPO_01_Properties`: any input containing an SME token is rejected with `sme_not_allowed`, never stored | BR-IPO-01/03 |
| Property (rapid) | `ipos/lifecycle_prop_test.go` | `TestStepDecision_BR_IPO_03_Properties`: never lowers rank; applying a decision twice equals once; with no override, the final status after applying all due steps in instant order equals the highest step whose instant ≤ now (status-from-dates oracle); never SETTLED | BR-IPO-03; FR-032/033 |
| Integration (DB) | `ipos/store_test.go` | `TestStore_BR_IPO_01_DBRejectsNonMainboard` (raw SQL with `board='SME'`, `exchanges={'NSE_EMERGE'}`, duplicate exchange fail on CHECK); `TestStore_BR_IPO_02_PublishCheck` (published without flag fails); `TestStore_DeleteTriggerBlocksEverPublished`; `TestStore_DateChecks`; `TestStore_SourceConsistency`; `TestStore_KeysetList` (NULL open dates first, stable pages, month filter on IST civil boundaries 2026-11-01/2026-11-30) | BR-IPO-01/02; FR-008/009/011 |
| Integration (service) | `ipos/service_test.go` | Create draft (US1.1: UPCOMING, no flag, unpublished, one audit, source manual); exact paise and audit diff (US1.2); `TestCreate_FR007_IdempotentConcurrent` (two goroutines with the same key → one row, both get it); `TestUpdate_FR006_StaleVersion` (US1.9); duplicate names (US1.10); `TestCreate_BR_IPO_01_SMEStoresNothingAndSchedulesNothing` (US2.1); `TestUpdate_BR_IPO_01_EditToSMERejected` (US2.4); `TestPublish_BR_IPO_02_*` (US3.1–3.4, 3.9); `TestSetFlag_BR_IPO_02_*` (US3.5–3.7); `TestUnpublish_BR_IPO_02_HiddenStatusUnaffected` (US3.8); `TestDelete_FR008_*` (US9.2 steps cancelled and audit snapshot; US9.3); `TestList_FR009_Filters` (US9.1); `TestAudit_SC005_OneEntryPerMutation` (every mutation once; no-op none; replay none) | BR-IPO-01/02; FR-001–009, FR-020–024, FR-060; SC-001, SC-005 |
| Integration (time travel) | `ipos/lifecycle_test.go` (DB part) | `TestLifecycle_BR_IPO_03_OpenBoundary` (09:59 UPCOMING; 10:00 OPEN; variant 10:04 with the 10:00 tick missed; audit actor system, from/to, `scheduled_for` 11 Nov 10:00 IST); `TestLifecycle_BR_IPO_03_CloseBoundary` (16:59 OPEN, 17:00 CLOSED); `TestLifecycle_BR_IPO_03_AllotmentAndListing` (16 Nov 10:00, 18 Nov 10:00); `TestLifecycle_BR_IPO_03_NeverSettledByTime` (ticks for 30 days); `TestLifecycle_BR_IPO_03_CatchUpAfterDowntime` (no ticks 10 Nov 09:00 → 16 Nov 11:00; exactly three audit rows UPCOMING→OPEN→CLOSED→ALLOTMENT_OUT in order, each step once); `TestLifecycle_BR_IPO_03_MissingAllotmentWaitsInClosed`; `TestLifecycle_BR_IPO_03_DraftsAdvance` (D-07, still 404 for clients); `TestLifecycle_BR_IPO_03_DuplicateRunIsNoop` (handler invoked twice and two concurrent ticks → one change, one audit); `TestLifecycle_BR_IPO_03_PastDatesOnCreate` (US4.10) | BR-IPO-03; FR-031–034; US4.1–4.10; SC-002 |
| Integration (reschedule) | `ipos/reschedule_test.go` | US5.1 open moved later (11 Nov 10:05 still UPCOMING; 12 Nov 10:00 OPEN); US5.2 close extended while OPEN; US5.3 close moved into the past → next tick closes; US5.6 failing save rolls back and steps keep old `run_at` (read via 002 test helpers); US5.7 open moved later while OPEN stays OPEN; US5.8 allotment cleared → step cancelled, nothing on the old date; US5.10 one audit with old/new dates; US5.11 non-date edit → no `Schedule` call (spy wrapper around the real scheduler) | BR-IPO-03; FR-041; SC-003 |
| Integration (override) | `ipos/override_test.go` | US6.1 forward override then the 16 Nov step is a no-op (no audit); US6.2 backward to UPCOMING then 12 Nov ticks stay UPCOMING and 13 Nov 17:00 → CLOSED; US6.3 open date re-armed to 13 Nov fires; `TestOverride_BR_IPO_03_RaceWithRunningStep` (override commits while the step handler waits on the row lock → no-op; uses a barrier listener); US6.4 missing reason (handler 422); US6.5 same status → `ipo.no_change`, no audit; US6.6 admin SETTLED with reason; US6.7 `MarkSettled` from LISTED (actor system) and from CLOSED → `ErrNotListed`, nothing changes; US6.8 LISTED stays without admin action; US6.9 `status_source` exposed | BR-IPO-03; FR-033/035/036 |
| Integration (signals) | `ipos/signals_test.go` | `TestSignals_BR_IPO_03_DatesChangedOncePerEdit` (old and new of all six; none on create or non-date edit); `TestSignals_BR_IPO_03_DatesChangedFailureRollsBack` (US5.5: old dates, old step `run_at`, no audit, error); `TestSignals_BR_IPO_02_EligibilityChanged` (publish APPLY → true; DNA publish → none; flag flip → false; note-only → none; unpublish → false); `TestSignals_BR_IPO_02_FailureRollsBackPublish` (US3.10, both error and panic); `TestSignals_BR_IPO_03_StatusChangedExactlyOnce` (step, override, MarkSettled; no-op none; listener sees the new row inside the transaction); `TestSignals_BR_IPO_03_StepListenerFailureRetries` (job retried by 002, status unchanged until success); `TestSignals_RejectedErrorMapsTo409` | BR-IPO-02/03; FR-024/037/042; US4.11 |
| Integration (eligibility) | `ipos/eligible_test.go` | `TestRequireEligible_BR_IPO_02` (draft, DNA, APPLY); requires a transaction (`db.ErrNoTx`); a concurrent unpublish waits for the FOR SHARE holder | BR-IPO-02; FR-023 |
| Handler (humatest) | `ipos/handler_test.go` | `authz.Install` with a fake verifier (admin and client principals). Status codes and `code` for each error in error-codes.md; `segments` mapping; `Idempotency-Key` replay statuses (201 → 200, 204 → 204); `TestClient_BR_IPO_02_OnlyPublished` (US7.1 exactly 3 of 5; US7.2 status filter); `TestClient_BR_IPO_02_DraftLooksUnknown` (US7.3: byte-identical 404 bodies for draft and random id); `TestClient_FR051_NoAdminFields` (US7.4, JSON key set assertion); `TestClient_UnpublishHidesNextRead` (US7.5); `TestAdminOps_FR050_ClientForbidden` (US7.6, every admin op → 403, nothing written); `TestSuggest_US8_4_NoSideEffects`; date-suggestions route precedence; warning payload and Indian-formatted `detail` | BR-IPO-02; FR-050/051/061; SC-004, SC-007 |
| Contract | `cmd/divya/contract_test.go` (003's walk) and a 008 addition `cmd/divya/ipos_contract_test.go` | 003's walk automatically covers 401/403 for all 12 operations (US7.7, SC-007) once the admin ids are in `testdata/admin-operations.txt`. 008 asserts: the 12 operation ids exist with exact security; every non-GET has an `Idempotency-Key` header parameter; `ClientIPO` properties exclude `source`, `source_ref`, `last_synced_at`, `status_source`, `version`, `published`; `segments` items have no enum (R-05); generated `openapi.yaml` is committed and current; oasdiff non-breaking (CI) | V; FR-050/051/061 |
| Contract (TS) | `packages/domain/src/ipo.test.ts` | `IPO_STATUSES` order matches `IPOStatus` from `@divya/api-client` schema types (compile-time `satisfies`); every `ipo.*` code in error-codes.md exists in `ipoErrorCodes` | V |
| Component / E2E | none | No screens in 008 (022/023/025 own them). 023 and 025 Playwright/Maestro flows will cover IPO pages | n/a |
| Performance | `ipos/perf_test.go` (integration, `-short` skip) | `TestList_SC008_Budget`: 500 seeded IPOs; 200 admin and 200 client list/detail calls through humatest plus a real DB; local p95 < 50 ms (headroom for the 200 ms Mumbai budget); `EXPLAIN` uses `ipos_published_list_idx` / `ipos_admin_list_idx` | SC-008, VII |
| Golden | existing suites | `go test -race ./...` incl. GS-01…GS-04; unchanged | GS-01…04 |

SC-006 (under 3 minutes of data entry) is a UAT item for 023 and is not automated here. Coverage target: `internal/ipos` ≥ 90% statements and 100% branches of `validate.go` and the step decision (`lifecycle.go`), because they guard BR-IPO-01/03.

## Performance notes

- List: one indexed range scan on `(org_id, open_date DESC NULLS FIRST, id DESC)` (partial index for clients), `LIMIT n+1`, no joins, no N+1. Status, flag and month filters apply to at most a few hundred rows. Detail: a PK lookup. Expected DB time is well under 5 ms. 003's per-request session lookup adds about 1–3 ms.
- Mutations: one row lock, one update, at most four `scheduled_jobs` upserts, one audit insert, plus listeners (none registered in 008). A step handler is a single small transaction.
- Payload size: about 1 KB per IPO, 50 per page by default.
- No caching. The data is tiny, and the spec requires that unpublish hides the IPO on the next read (US7.5).
- Free-tier impact: four job rows per IPO and one audit row per mutation are negligible next to the 500 MB Supabase budget.

## Security notes

- AuthZ: 003's `Require(RoleAdmin)` on 10 admin operations. Admin tokens exist only after TOTP (003 P-05/P-08), which satisfies "admin with second factor" (FR-050). `Require()` covers the two client reads. `AssertDeclared` plus 003's contract walk fail the build on a missing declaration.
- Org isolation: the org always comes from the principal (or `job.OrgID`). Every query filters by `org_id`. A cross-org id gives 404.
- Draft privacy: client operations query `WHERE published` only. 404 bodies are identical for drafts and unknown ids (no existence oracle). `ClientIPO` is a distinct Go type, so admin fields cannot leak through a struct tag mistake. Tests assert the JSON key set.
- Input: `offer_document_url` must be https (schema, service and CHECK) so UIs never render `javascript:` or `data:` links. Lengths are bounded everywhere. No string-built SQL (sqlc). Problems never echo submitted values (003 P-10).
- Client-visible note (D-10): documented in the API description. The 023 form must label it as "visible to clients".
- Audit rows hold typed structs of the changed fields only. No PII exists in this table.
- Idempotency keys are bounded (8–200 characters, safe charset) and stored in the audit `after` and in `create_idempotency_key`. They are not secrets.
- Listener isolation: a panic in a listener is recovered by `InTx` and rolls back. The response is a generic 500.

## Dependencies requiring owner approval

**None.** Every import already exists in `go.mod` after 002 (uuid direct, rapid test-only) and 003. `internal/platform/inr` is in-repo and standard library only. No TS dependency is added: `packages/domain` gets constants, and `packages/api-client` is regenerated with existing tools.

Owner approvals that still apply: the migration PR (CODEOWNERS, constitution workflow 5) and acceptance of ADR 0008.

## Risks

| # | Risk | Impact | Mitigation |
|---|---|---|---|
| R-1 | 003 merges late or renumbers its migration | 008 cannot merge (goose order, `authz` missing) | Build Phase A (below) on 002 alone. The PR waits for 003. Never renumber an applied migration; if 003 changes its number before merging, 008 follows. |
| R-2 | 002 or 003 audit `Entry` shape diverges (002 `uuid` vs early 003 string draft; 003 C-4 resolved to 002's uuid shape plus `At`) | compile errors at rebase | 008 uses only `audit.Write(ctx, audit.Entry{…})` with uuid fields. Tests do not rely on `At`. |
| R-3 | A step retry runs after a later step | one combined audited jump instead of two steps | Documented (R-01). Listeners must accept non-adjacent transitions. Covered by the property oracle. |
| R-4 | Listener misuse (re-entrant mutation, slow I/O) in later features | deadlocks and latency | ADR 0008 rules. The `ipostest` harness offers a re-entrancy guard (a context marker; a mutation inside a listener returns an error in tests). |
| R-5 | Huma pointer and required-nullable schema details differ from the contract | client type mismatch | The contract test asserts the PUT body requires all keys and null is accepted. Fix with `nullable:"true"` / `required:"true"` tags. |
| R-6 | Quotas requested by the brief but excluded by the spec | stakeholder expectation gap | Flagged in the report. A later expand-only migration (009/026) adds basis-point `smallint` columns. |
| R-7 | `modules.md` lists only 002 as 008's dependency, but the spec and this plan need 003 | conductor may start the build before 003 is done | Owner or conductor adds 003 to the "Depends on" cell (not changed here; the owner controls ordering). |

## Build phases

- **Phase A (needs 002 Phase A on `stage`)**: migration and sqlc, `platform/inr`, model, validate, suggest, store, service, lifecycle, signals, `ipostest`, `RegisterJobs` wiring, depguard rules, every unit, property and integration test above. No HTTP yet. The public OpenAPI is unchanged.
- **Phase B (needs 003 on `stage`)**: `handler.go`, `api_types.go`, `problems.go`, `Register` wiring after `authz.Install`, `admin-operations.txt`, OpenAPI and TS regeneration, `packages/domain/src/ipo.ts`, handler, contract and perf tests, quickstart smoke.
- **Single PR**: it merges after 003. If 003 is already merged when tasks start, the two phases simply run back to back.

## Project Structure

### Documentation (this feature)

```text
specs/008-ipo-catalogue/
├── spec.md                 # approved
├── research.md             # research + §7 planning decisions
├── plan.md                 # this file
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── ipos.openapi.yaml   # expected additive OpenAPI diff
│   ├── go-module-api.md    # exported Go API + listener contract for dependents
│   └── error-codes.md      # problem codes and warnings
├── checklists/requirements.md
└── tasks.md                # /speckit-tasks (not created by this plan)
docs/adr/0008-in-transaction-module-signals.md   # new (proposed)
```

### Source Code (repository root)

```text
apps/api/
├── migrations/00004_ipos.sql                        # new
├── queries/ipos.sql                                  # new
├── internal/platform/db/dbgen/ipos.sql.go, models.go # generated
├── internal/platform/inr/inr.go, inr_test.go         # new (stdlib only)
├── internal/ipos/
│   ├── doc.go            # package doc: shape, signals, step rules
│   ├── model.go          # Status/Rank, ApplyFlag, Exchange, Dates, IPO, Draft, Warning, Eligible, MinApplicationPaise
│   ├── validate.go       # pure validation: segments, values, date closure, publish completeness, warnings
│   ├── suggest.go        # SuggestDates (istdate.AddWeekdays)
│   ├── lifecycle.go      # steps table, instants, stepDecision, stepDiff, job handler, RegisterJobs
│   ├── signals.go        # listener interfaces, event structs, registration, fire in order
│   ├── errors.go         # sentinels, ValidationError, NotListedError, RejectedError
│   ├── service.go        # Service, Deps, mutations, reads, MarkSettled, RequireEligible, idempotency
│   ├── store.go          # Store over dbgen (only file importing pgx types)
│   ├── api_types.go      # Huma DTOs (AdminIPO, ClientIPO, IPODraft …) and mapping
│   ├── problems.go       # error → httpx.Problem mapping
│   ├── handler.go        # Register(api, svc): 12 operations
│   ├── ipostest/         # NewService(harness), RecordingListener, FailingListener, ExampleIPO
│   └── *_test.go         # per the test table
├── cmd/divya/main.go                       # wiring: ipos.NewService, RegisterJobs, Register
├── cmd/divya/ipos_contract_test.go         # new
├── cmd/divya/testdata/admin-operations.txt # + 10 admin operation ids (003 file)
└── .golangci.yml                           # depguard rules above
packages/api-client/openapi.yaml, src/schema.ts   # regenerated
packages/domain/src/ipo.ts, ipo.test.ts, index.ts # statuses, flags, segments, ipoErrorCodes
```

**Structure decision**: one Go module folder following the golden pattern (`go-module-pattern` skill). No new app. TS changes are limited to generated types and shared constants. `docs/product/modules.md` status moves to `building` when tasks start (conductor).

## Complexity Tracking

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| Job payload carries `armed_at` besides `ipo_id` (002 convention: "ids only") | FR-036 must hold even when an override commits while a step is already claimed. The handler needs to know whether the job was armed before or after the override (R-02). It is scheduling metadata, not copied business data. The handler still reads dates and status from the row. | Cancelling steps on override is racy (a running job only gets `cancel_requested`). Four `*_armed_at` columns on `ipos` store the same fact in a less local place. |
| `internal/ipos` reads the platform `audit_log` for idempotency replay (`FindIPOAuditByIdempotencyKey`) | It follows the repository's replay-via-audit pattern (002 Phase B) without waiting for 002's shared query. The row is locked first, so replay is race-free. | A generic idempotency/response table means new schema plus storing response bodies. Relying on optimistic versions alone turns honest retries into false 409s. |
