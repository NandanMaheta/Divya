# Tasks: IPO catalogue (CRUD, mainboard only, APPLY / DO NOT APPLY, lifecycle by dates)

**Feature**: 008-ipo-catalogue | **Branch**: `feat/008-ipo-catalogue` | **Inputs**: spec.md, plan.md, data-model.md, contracts/*, research.md, quickstart.md, ADR 0008
**Business rules covered**: BR-IPO-01, BR-IPO-02, BR-IPO-03. GS-01..GS-04 must keep passing (no golden scenario is driven by 008).

## Format

`- [ ] T### [P] Owner: title` followed by Depends on, Rules, Files, Done command.

- **[P]**: parallel-safe once its dependencies are met (different files, no shared in-flight edits).
- **Owner**: exactly one of `test-engineer` or `backend-dev`. Implementers never edit tests written by `test-engineer`.
- **Expect**: `red` = the done command must FAIL for the right reason (assertion or 404, never a compile error) until the paired implementation task lands; `green` = must pass. A test task is done when its tests are written, fail for the right reason and are committed.
- All done commands run from the worktree root `/home/user/divya-008`. Integration tests need Docker and skip under `-short`. Generated code (`dbgen`, `openapi.yaml`, `schema.ts`) is not counted in the size budget.
- **Gates**: `002-merged` = feature 002 Phase A on `stage` (scheduler, outbox, platform clock/istdate/db/audit, schedulertest, migration 00002). `003-merged` = feature 003 on `stage` (authz, httpx.Problem, audit At, migration 00003). A task whose Depends on lists a gate has status `blocked` in state.json until the gate is met. 008 merges after 003 (C-7); never renumber `00004`.
- **Why a skeleton phase**: tests in one Go package break each other's compilation. Phase 4 adds stubs that compile and panic, so every test task fails at runtime for the right reason and each implementation task can go green on its own `-run` subset.
- Owner approvals on the PR: T006 (migration, CODEOWNERS), ADR 0008 acceptance (adr task). No new dependency, no ledger or auth schema change.
- Security-sensitive: handler tasks (authz, draft privacy). Ask for a security reviewer on that PR.


## Phase 1 - Guard rails (lint boundaries)

- [ ] T001 [P] **test-engineer**: Extend the deliberate-violation check with the ipos rules: platform importing ipos, scheduler/outbox importing ipos, ipos importing a sibling module, ipos importing pgx outside store.go, time.Now in ipos. Each must make golangci-lint report depguard/forbidigo; script cleans up.
  - Depends on: 002-merged
  - Rules: none (mechanism) | Refs: plan: Module placement and dependencies; constitution IV
  - Files (1, ~70 lines): `scripts/check-lint-boundaries.sh`
  - Done command (red): `bash scripts/check-lint-boundaries.sh`
- [ ] T002 **backend-dev**: Add depguard rules to apps/api/.golangci.yml: append ipos to platform-is-a-leaf, add ipos-boundaries (strict allow-list), ipos-pgx-only-in-store, scheduler-does-not-know-owners.
  - Depends on: T001, 002-merged
  - Rules: none (mechanism) | Refs: plan: depguard additions
  - Files (1, ~40 lines): `apps/api/.golangci.yml`
  - Done command (green): `bash scripts/check-lint-boundaries.sh`

## Phase 2 - Indian rupee formatter

- [ ] T003 [P] **test-engineer**: Table tests for internal/platform/inr.Format: ₹14,700, ₹1,48,500, ₹10,00,00,000, ₹0.50, ₹1,000.05, negative, math.MaxInt64 (no float math).
  - Depends on: none
  - Rules: BR-IPO-02 | Refs: FR-061; constitution VII; test TestFormat_*
  - Files (1, ~70 lines): `apps/api/internal/platform/inr/inr_test.go`
  - Done command (red): `cd apps/api && go test -race ./internal/platform/inr/...`
- [ ] T004 **backend-dev**: Implement inr.Format(paise int64) string with Indian digit grouping, integer arithmetic only, stdlib only.
  - Depends on: T003
  - Rules: BR-IPO-02 | Refs: FR-061; K-09
  - Files (1, ~60 lines): `apps/api/internal/platform/inr/inr.go`
  - Done command (green): `cd apps/api && go test -race ./internal/platform/inr/...`

## Phase 3 - Schema and queries (migration needs owner approval on the PR)

- [ ] T005 [P] **test-engineer**: Schema tests in package internal/ipos/schema (own package so unrelated red tests cannot break compilation): board/exchanges CHECKs reject SME and duplicate exchange via raw SQL (TestSchema_BR_IPO_01_DBRejectsNonMainboard), published-without-flag CHECK, date-order CHECKs, band both-or-neither and floor<=cap, source consistency, delete trigger blocks ever-published rows, https-only offer URL, note-without-flag.
  - Depends on: 002-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: data-model §1; FR-008, FR-011; plan: Integration (DB)
  - Files (1, ~190 lines): `apps/api/internal/ipos/schema/schema_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestSchema_' ./internal/ipos/schema/...`
- [ ] T006 **backend-dev**: Write migrations/00004_ipos.sql (goose, expand-only): ipos table with every CHECK, partial unique indexes, admin and published list indexes, ipos_forbid_published_delete() trigger.
  - Depends on: T005, 002-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: data-model §1-2; C-7 (merges after 003, never renumber); OWNER APPROVAL (migration PR)
  - Files (1, ~150 lines): `apps/api/migrations/00004_ipos.sql`
  - Done command (green): `cd apps/api && go test -race -run 'TestSchema_' ./internal/ipos/schema/...`
- [ ] T007 [P] **test-engineer**: Query tests (TestQueries_*) against dbgen in the schema package: keyset list order (NULL open dates first, stable pages), IST month boundaries 2026-11-01/30, status/published/flag filters including NONE, org isolation, version guard on update, DeleteIPO guard, FindIPOAuditByIdempotencyKey, create-key conflict.
  - Depends on: T006, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: data-model §6; FR-009; US9.1
  - Files (1, ~190 lines): `apps/api/internal/ipos/schema/queries_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestQueries_' ./internal/ipos/schema/...`
- [ ] T008 **backend-dev**: Write queries/ipos.sql (14 queries from data-model §6, every query filters org_id), run sqlc generate and sqlc vet, commit generated dbgen.
  - Depends on: T007, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: data-model §6 (generated code not counted)
  - Files (2, ~190 lines): `apps/api/queries/ipos.sql`, `apps/api/internal/platform/db/dbgen/ipos.sql.go`
  - Done command (green): `cd apps/api && sqlc generate && sqlc vet && go test -race ./internal/ipos/schema/...`

## Phase 4 - Compile-only skeleton (stubs panic; lets tests compile and fail for the right reason)

- [ ] T009 **backend-dev**: Skeleton: exported types and signatures from contracts/go-module-api.md (Status/Rank, StatusSource, ApplyFlag, Exchange, Dates, IPO, Draft, Warning, Result, Page, filters), error types and sentinels, signal event structs, listener interfaces and On*Changed registration (stub bodies).
  - Depends on: 002-merged
  - Rules: none (mechanism) | Refs: contracts/go-module-api.md
  - Files (4, ~190 lines): `apps/api/internal/ipos/doc.go`, `apps/api/internal/ipos/model.go`, `apps/api/internal/ipos/errors.go`, `apps/api/internal/ipos/signals.go`
  - Done command (green): `cd apps/api && go build ./... && go vet ./internal/ipos/...`
  - Note: No behaviour; bodies are panic("not implemented"). Gives test-engineer the exact names.
- [ ] T010 **backend-dev**: Skeleton: validate.go (Validate, segment mapping, date closure, publish completeness, warnings), suggest.go (SuggestDates), lifecycle steps (stepInstant, stepDecision, stepDiff) as panic stubs with the signatures named in plan.md.
  - Depends on: T009, 002-merged
  - Rules: none (mechanism) | Refs: plan: Test strategy names
  - Files (3, ~90 lines): `apps/api/internal/ipos/validate.go`, `apps/api/internal/ipos/suggest.go`, `apps/api/internal/ipos/lifecycle_steps.go`
  - Done command (green): `cd apps/api && go build ./... && go vet ./internal/ipos/...`
- [ ] T011 **backend-dev**: Skeleton: service.go (Deps, Service, NewService, every method of the contract as panic stub, RegisterJobs) and store.go (Store type with constructor only).
  - Depends on: T010, T008, 002-merged
  - Rules: none (mechanism) | Refs: contracts/go-module-api.md: Construction
  - Files (3, ~120 lines): `apps/api/internal/ipos/service.go`, `apps/api/internal/ipos/store.go`, `apps/api/internal/ipos/lifecycle.go`
  - Done command (green): `cd apps/api && go build ./... && go vet ./internal/ipos/...`
- [ ] T012 **backend-dev**: Skeleton: ipostest package signatures (NewService(h), RecordingListener, FailingListener with error/panic/RejectedError modes and a barrier mode, ExampleIPO builder for Acme Ltd) as stubs; confirm 002 schedulertest harness is importable.
  - Depends on: T011, 002-merged
  - Rules: none (mechanism) | Refs: plan: Test strategy (ipostest)
  - Files (1, ~100 lines): `apps/api/internal/ipos/ipostest/ipostest.go`
  - Done command (green): `cd apps/api && go build ./... && go vet ./internal/ipos/...`

## Phase 5 - Tests first for Phase A (all red, parallel; written against the skeleton)

- [ ] T013 [P] **test-engineer**: model_test.go: TestMinApplication_FR004 (final price beats cap, none unknown), TestWarning_FR005_Bounds (₹9,999 / 10,000 / 20,000 / 20,001 / 25,000 with detail text), TestEligible_BR_IPO_02, TestStatusRank.
  - Depends on: T010, T004, 002-merged
  - Rules: BR-IPO-02 | Refs: FR-004, FR-005, FR-023; US1.2-1.4
  - Files (1, ~120 lines): `apps/api/internal/ipos/model_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestMinApplication_FR004|TestWarning_FR005|TestEligible_BR_IPO_02|TestStatusRank' ./internal/ipos/`
- [ ] T014 [P] **test-engineer**: validate_test.go (segments and values): BR_IPO_01 rejects NSE_EMERGE, BSE_SME and mixed; unknown/duplicate/missing segment; mainboard combos accepted; name containing SME accepted; price band half/floor>cap/floor=cap; blank name; lead manager limits; flag note without flag.
  - Depends on: T010, 002-merged
  - Rules: BR-IPO-01 | Refs: FR-003, FR-010, FR-012; US1.5-1.7, US2.*
  - Files (1, ~170 lines): `apps/api/internal/ipos/validate_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestValidate_BR_IPO_01|TestValidate_PriceBand|TestValidate_Values' ./internal/ipos/`
- [ ] T015 [P] **test-engineer**: validate_dates_test.go: TestValidate_BR_IPO_03_DateOrder table (open=close ok, allotment=close rejected, listing before allotment, open+listing without close via closure, errors name only violated fields); TestValidate_BR_IPO_02_PublishRequirements (every missing item listed, final price without band ok); FR-021 edit that breaks a published IPO.
  - Depends on: T010, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: FR-020, FR-021, FR-040; US3.1-3.2, US5.9
  - Files (1, ~180 lines): `apps/api/internal/ipos/validate_dates_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestValidate_BR_IPO_03|TestValidate_BR_IPO_02' ./internal/ipos/`
- [ ] T016 [P] **test-engineer**: validate_prop_test.go (rapid): TestDateOrder_BR_IPO_03_Properties vs brute-force closure model; TestMinApplication_Properties (exact lot x price, below 2^53); TestSegments_BR_IPO_01_Properties (any SME token rejected with sme_not_allowed).
  - Depends on: T010, 002-merged
  - Rules: BR-IPO-01, BR-IPO-03 | Refs: FR-003, FR-010, FR-040
  - Files (1, ~150 lines): `apps/api/internal/ipos/validate_prop_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestDateOrder_BR_IPO_03_Properties|TestMinApplication_Properties|TestSegments_BR_IPO_01_Properties' ./internal/ipos/`
- [ ] T017 [P] **test-engineer**: suggest_test.go: TestSuggestDates_BR_IPO_03_WeekdaysOnly (Fri 13 -> Mon 16/Tue 17/Wed 18; Mon 9 -> Tue 10/Wed 11/Thu 12; Wed 11 -> Thu 12/Fri 13/Mon 16; notice code ipo.weekdays_only; pure, no I/O).
  - Depends on: T010, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-043; US8.1-8.3
  - Files (1, ~70 lines): `apps/api/internal/ipos/suggest_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestSuggestDates_BR_IPO_03' ./internal/ipos/`
- [ ] T018 [P] **test-engineer**: step_test.go (pure): TestStepInstant_BR_IPO_03_ISTTimes (10:00/17:00 IST = 04:30/11:30 UTC, never day-shifted), TestStepDecision_BR_IPO_03 table over (status, date, now, armed_at, last_override_at): forward only, nil date, instant in future, override suppression and re-arm, at or past target; TestStepDiff_BR_IPO_03 (changed Schedule, cleared Cancel, unchanged none, refund/credit none).
  - Depends on: T010, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-031, FR-032, FR-036, FR-041
  - Files (1, ~180 lines): `apps/api/internal/ipos/step_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestStepInstant_BR_IPO_03|TestStepDecision_BR_IPO_03$|TestStepDiff_BR_IPO_03' ./internal/ipos/`
- [ ] T019 [P] **test-engineer**: step_prop_test.go (rapid): TestStepDecision_BR_IPO_03_Properties: never lowers rank, idempotent, final status from applying due steps in instant order equals highest step with instant <= now when no override, never SETTLED.
  - Depends on: T010, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-032, FR-033
  - Files (1, ~110 lines): `apps/api/internal/ipos/step_prop_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestStepDecision_BR_IPO_03_Properties' ./internal/ipos/`
- [ ] T020 [P] **test-engineer**: service_create_test.go: create draft (UPCOMING, no flag, unpublished, one audit, source manual), exact paise and audit snapshot, warning on create, duplicate names allowed, TestCreate_BR_IPO_01_SMEStoresNothingAndSchedulesNothing, TestCreate_FR007_IdempotentConcurrent (two goroutines one key -> one row), steps scheduled for set dates only.
  - Depends on: T012, T004, 002-merged
  - Rules: BR-IPO-01, BR-IPO-03 | Refs: FR-001..007, FR-010, FR-060; US1.1-1.4, US1.10, US2.1; SC-001
  - Files (1, ~190 lines): `apps/api/internal/ipos/service_create_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestCreate_' ./internal/ipos/`
- [ ] T021 [P] **test-engineer**: service_update_test.go: TestUpdate_FR006_StaleVersion, TestUpdate_BR_IPO_01_EditToSMERejected (nothing changes), no-op PUT writes no audit and no version bump, edit breaking a published IPO rejected (FR-021), audit before/after contains only changed fields, PUT with warnings.
  - Depends on: T012, T004, 002-merged
  - Rules: BR-IPO-01, BR-IPO-02 | Refs: FR-006, FR-021, FR-060; US1.9, US2.4
  - Files (1, ~170 lines): `apps/api/internal/ipos/service_update_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestUpdate_' ./internal/ipos/`
- [ ] T022 [P] **test-engineer**: service_read_test.go: TestList_FR009_Filters (status, published, flag incl. NONE, month, order, cursor pages), Get/GetPublished (draft = ErrNotFound), Delete never-published (steps cancelled, audit snapshot), TestDelete_FR008_PublishedNotDeletable and after unpublish, org isolation.
  - Depends on: T012, T004, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: FR-008, FR-009; US9.1-9.3, US7.3
  - Files (1, ~190 lines): `apps/api/internal/ipos/service_read_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestList_FR009|TestGet_|TestDelete_' ./internal/ipos/`
- [ ] T023 [P] **test-engineer**: service_publish_test.go: TestPublish_BR_IPO_02_* (APPLY, DO NOT APPLY, missing items listed, already published = ErrNoChange, first_published_at kept), TestSetFlag_BR_IPO_02_* (draft null flag ok, published cannot clear, note only), TestUnpublish_BR_IPO_02_HiddenStatusUnaffected, publish with out-of-range warning.
  - Depends on: T012, T004, 002-merged
  - Rules: BR-IPO-02 | Refs: FR-020..024; US3.1-3.9
  - Files (1, ~190 lines): `apps/api/internal/ipos/service_publish_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestPublish_BR_IPO_02|TestSetFlag_BR_IPO_02|TestUnpublish_BR_IPO_02' ./internal/ipos/`
- [ ] T024 [P] **test-engineer**: eligible_test.go: TestRequireEligible_BR_IPO_02 (draft, DO NOT APPLY, APPLY), db.ErrNoTx outside a transaction, a concurrent unpublish waits for the FOR SHARE holder.
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-02 | Refs: FR-023
  - Files (1, ~90 lines): `apps/api/internal/ipos/eligible_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestRequireEligible_BR_IPO_02' ./internal/ipos/`
- [ ] T025 [P] **test-engineer**: signals_test.go: TestSignals_FireOrder (registration order, panic becomes error), TestSignals_BR_IPO_03_DatesChangedOncePerEdit (old/new of all six; none on create or non-date edit), TestSignals_BR_IPO_03_DatesChangedFailureRollsBack (old dates, old step run_at, no audit), TestSignals_BR_IPO_02_EligibilityChanged (publish APPLY true, DNA none, flag flip false, note-only none, unpublish false), TestSignals_BR_IPO_02_FailureRollsBackPublish (error and panic), TestSignals_RejectedErrorMapsTo409 (typed error carries code).
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: FR-024, FR-042; US3.10, US5.5; ADR 0008
  - Files (1, ~200 lines): `apps/api/internal/ipos/signals_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestSignals_FireOrder|TestSignals_BR_IPO_03_DatesChanged|TestSignals_BR_IPO_02|TestSignals_RejectedError' ./internal/ipos/`
- [ ] T026 [P] **test-engineer**: signals_status_test.go: TestSignals_BR_IPO_03_StatusChangedExactlyOnce (step, override, MarkSettled; no-op sends none; listener sees new row inside the tx), TestSignals_BR_IPO_03_StepListenerFailureRetries (002 retries; status unchanged until success), listener panic inside a step job.
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-037; US4.11
  - Files (1, ~130 lines): `apps/api/internal/ipos/signals_status_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestSignals_BR_IPO_03_Status|TestSignals_BR_IPO_03_StepListener' ./internal/ipos/`
- [ ] T027 [P] **test-engineer**: lifecycle_test.go (fake clock, real Tick): TestLifecycle_BR_IPO_03_OpenBoundary (Wed 11 Nov 09:59 UPCOMING, 10:00 OPEN, tick missed variant 10:04, audit actor system with from/to and scheduled_for), CloseBoundary (Fri 13 Nov 16:59 OPEN, 17:00 CLOSED), AllotmentAndListing (Mon 16 / Wed 18 Nov 10:00), NeverSettledByTime (30 days), DraftsAdvance (D-07, still 404 to clients), PastDatesOnCreate (US4.10).
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-031, FR-033, FR-034; US4.1-4.10; SC-002
  - Files (1, ~200 lines): `apps/api/internal/ipos/lifecycle_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestLifecycle_BR_IPO_03_(OpenBoundary|CloseBoundary|AllotmentAndListing|NeverSettledByTime|DraftsAdvance|PastDatesOnCreate)' ./internal/ipos/`
- [ ] T028 [P] **test-engineer**: lifecycle_catchup_test.go: TestLifecycle_BR_IPO_03_CatchUpAfterDowntime (no ticks 10 Nov 09:00 to 16 Nov 11:00 -> exactly three audit rows UPCOMING>OPEN>CLOSED>ALLOTMENT_OUT in id order, each step once), MissingAllotmentWaitsInClosed, DuplicateRunIsNoop (handler twice and two concurrent ticks -> one change, one audit), RegisterJobs registers the four kinds with KindOptions{}.
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-031, FR-032; US4.5-4.9
  - Files (1, ~150 lines): `apps/api/internal/ipos/lifecycle_catchup_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestLifecycle_BR_IPO_03_(CatchUp|MissingAllotment|DuplicateRun)|TestRegisterJobs' ./internal/ipos/`
- [ ] T029 [P] **test-engineer**: reschedule_test.go: TestReschedule_BR_IPO_03_* for US5.1 (open moved later), 5.2 (close extended while OPEN), 5.3 (close into the past, next tick closes), 5.4, 5.6 (failing save keeps old run_at), 5.7, 5.8 (allotment cleared cancels step), 5.10 (one audit with old/new), 5.11 (non-date edit makes no Schedule call, spy over real scheduler).
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-041; US5.*; SC-003
  - Files (1, ~200 lines): `apps/api/internal/ipos/reschedule_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestReschedule_BR_IPO_03' ./internal/ipos/`
- [ ] T030 [P] **test-engineer**: override_test.go: TestOverride_BR_IPO_03_* for US6.1 forward override then 16 Nov step no-op, 6.2 backward to UPCOMING then 13 Nov 17:00 closes, 6.3 open date re-armed fires, 6.4 missing reason, 6.5 same status ErrNoChange without audit, 6.6 admin SETTLED with reason, 6.8 LISTED stays, 6.9 status_source exposed.
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-033, FR-035, FR-036; US6.*
  - Files (1, ~180 lines): `apps/api/internal/ipos/override_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestOverride_BR_IPO_03' ./internal/ipos/`
- [ ] T031 [P] **test-engineer**: override_race_test.go: TestOverride_BR_IPO_03_RaceWithRunningStep (override commits while the step waits on the row lock -> step is a no-op; barrier listener), TestMarkSettled_BR_IPO_03 (LISTED -> SETTLED actor system source settlement; already SETTLED no-op; CLOSED -> ErrNotListed, nothing changes).
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-033, FR-036; US6.7
  - Files (1, ~120 lines): `apps/api/internal/ipos/override_race_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestOverride_BR_IPO_03_Race|TestMarkSettled_BR_IPO_03' ./internal/ipos/`
- [ ] T032 [P] **test-engineer**: idempotency_test.go: replay of update/flag/publish/unpublish/override returns current state with no second audit row (TestIdempotency_FR007_Replay*), delete replay, same key concurrent on update, TestAudit_SC005_OneEntryPerMutation (every mutation once; no-op none; replay none).
  - Depends on: T012, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: FR-007, FR-060; SC-005
  - Files (1, ~180 lines): `apps/api/internal/ipos/idempotency_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestIdempotency_FR007|TestAudit_SC005' ./internal/ipos/`

## Phase 6 - Implementation of Phase A (service, lifecycle, signals)

- [ ] T033 **backend-dev**: Implement model.go behaviour: Status.Rank, Eligible, MinApplicationPaise (exact int64), warning builder using inr.
  - Depends on: T013, 002-merged
  - Rules: BR-IPO-02 | Refs: FR-004, FR-005, FR-023
  - Files (1, ~70 lines): `apps/api/internal/ipos/model.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestMinApplication_FR004|TestWarning_FR005|TestEligible_BR_IPO_02|TestStatusRank' ./internal/ipos/`
- [ ] T034 **backend-dev**: Implement validate.go: segment mapping (SME -> ipo.sme_not_allowed, unknown/duplicate -> ipo.invalid_segment), value checks, price band, name and manager limits, publish completeness listing every missing item.
  - Depends on: T014, T015, T016, T033, 002-merged
  - Rules: BR-IPO-01, BR-IPO-02 | Refs: FR-003, FR-010..012, FR-020, FR-021
  - Files (1, ~190 lines): `apps/api/internal/ipos/validate.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestValidate_BR_IPO_01|TestValidate_PriceBand|TestValidate_Values|TestValidate_BR_IPO_02|TestSegments_BR_IPO_01_Properties' ./internal/ipos/`
- [ ] T035 **backend-dev**: Implement validate_dates.go: date-order check over the transitive closure of FR-040 for set dates, error names fields of violated pairs only.
  - Depends on: T034, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-040; K-08
  - Files (1, ~90 lines): `apps/api/internal/ipos/validate_dates.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestValidate_BR_IPO_03|TestDateOrder_BR_IPO_03_Properties|TestMinApplication_Properties' ./internal/ipos/`
- [ ] T036 [P] **backend-dev**: Implement suggest.go: SuggestDates via istdate.AddWeekdays (T+1, T+2, T+2, T+3) plus notice code and text.
  - Depends on: T017, T033, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-043
  - Files (1, ~50 lines): `apps/api/internal/ipos/suggest.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestSuggestDates_BR_IPO_03' ./internal/ipos/`
- [ ] T037 [P] **backend-dev**: Implement lifecycle_steps.go: step table, stepInstant (istdate At 10:00/17:00), stepDecision (forward only, re-read dates, armed_at vs last_override_at), stepDiff, job payload builder.
  - Depends on: T018, T019, T033, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-031, FR-032, FR-036, FR-041; K-03, K-04
  - Files (1, ~150 lines): `apps/api/internal/ipos/lifecycle_steps.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestStepInstant_BR_IPO_03|TestStepDecision_BR_IPO_03|TestStepDiff_BR_IPO_03' ./internal/ipos/`
- [ ] T038 **backend-dev**: Implement store.go over dbgen: row mapping to IPO, one method per query, pgx types only in this file. Covered by service tests below; here proven by build and lint.
  - Depends on: T008, T011, T002, 002-merged
  - Rules: BR-IPO-02 | Refs: data-model §6; plan: depguard ipos-pgx-only-in-store
  - Files (1, ~190 lines): `apps/api/internal/ipos/store.go`
  - Done command (green): `cd apps/api && go build ./... && golangci-lint run ./internal/ipos/...`
- [ ] T039 **backend-dev**: Implement service.go core: NewService, in-transaction helper over db.TxRunner, audit write helper (clock At), idempotency lookup via audit, Get, List, ListPublished, GetPublished, cursor type.
  - Depends on: T038, T022, T034, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: FR-009, FR-051; K-06
  - Files (1, ~190 lines): `apps/api/internal/ipos/service.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestList_FR009|TestGet_' ./internal/ipos/`
- [ ] T040 **backend-dev**: Implement signals.go: listener registration, fire in order DatesChanged > EligibilityChanged > StatusChanged, panic recovery to error, RejectedError passthrough.
  - Depends on: T025, T039, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: ADR 0008; K-05
  - Files (1, ~90 lines): `apps/api/internal/ipos/signals.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestSignals_FireOrder' ./internal/ipos/`
- [ ] T041 **backend-dev**: Implement ipostest: NewService(harness), RecordingListener (records events and tx-visible row), FailingListener (error, panic, RejectedError, barrier), ExampleIPO builder, re-entrancy guard.
  - Depends on: T040, T012, 002-merged
  - Rules: none (mechanism) | Refs: plan: Test strategy; R-4
  - Files (1, ~170 lines): `apps/api/internal/ipos/ipostest/ipostest.go`
  - Done command (green): `cd apps/api && go build ./... && go vet ./internal/ipos/...`
- [ ] T042 **backend-dev**: Implement Create in service_create.go: idempotent insert on create key, validate, schedule steps for set dates in the same tx, audit ipo.created, warnings, replay.
  - Depends on: T020, T041, T035, T037, 002-merged
  - Rules: BR-IPO-01, BR-IPO-03 | Refs: FR-001..007, FR-041; K-06
  - Files (1, ~150 lines): `apps/api/internal/ipos/service_create.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestCreate_' ./internal/ipos/`
- [ ] T043 **backend-dev**: Implement Update in service_update.go: lock, version guard, validate, no-op detection, facts write, step diff (Schedule/Cancel), audit ipo.updated, DatesChanged signal, replay via audit.
  - Depends on: T021, T029, T042, 002-merged
  - Rules: BR-IPO-01, BR-IPO-03 | Refs: FR-006, FR-021, FR-041, FR-042; K-07
  - Files (1, ~190 lines): `apps/api/internal/ipos/service_update.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestUpdate_|TestReschedule_BR_IPO_03|TestSignals_BR_IPO_03_DatesChanged' ./internal/ipos/`
- [ ] T044 **backend-dev**: Implement SetFlag, Publish, Unpublish and RequireEligible in service_publish.go with EligibilityChanged on flips, ipo.no_change rules and FOR SHARE lock.
  - Depends on: T023, T024, T043, 002-merged
  - Rules: BR-IPO-02 | Refs: FR-020..024; K-07, K-11
  - Files (1, ~190 lines): `apps/api/internal/ipos/service_publish.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestPublish_BR_IPO_02|TestSetFlag_BR_IPO_02|TestUnpublish_BR_IPO_02|TestRequireEligible_BR_IPO_02|TestSignals_BR_IPO_02' ./internal/ipos/`
- [ ] T045 **backend-dev**: Implement lifecycle.go job handlers and RegisterJobs (four kinds) plus status_apply.go shared helper (SetIPOStatus, audit ipo.status_changed with step and scheduled_for, StatusChanged signal); handler locks, applies stepDecision, no-op writes nothing.
  - Depends on: T027, T028, T026, T044, T037, 002-merged
  - Rules: BR-IPO-03 | Refs: FR-031, FR-032, FR-034, FR-037; K-03, K-11; US4.*
  - Files (2, ~190 lines): `apps/api/internal/ipos/lifecycle.go`, `apps/api/internal/ipos/status_apply.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestLifecycle_BR_IPO_03|TestRegisterJobs|TestSignals_BR_IPO_03_Status|TestSignals_BR_IPO_03_StepListener' ./internal/ipos/`
- [ ] T046 **backend-dev**: Implement OverrideStatus, MarkSettled and Delete in service_status.go: reason required, ipo.no_change, last_override_at, status_source, NotListedError, delete with CancelSubject and audit snapshot, replay.
  - Depends on: T030, T031, T022, T045, 002-merged
  - Rules: BR-IPO-03, BR-IPO-02 | Refs: FR-008, FR-033, FR-035, FR-036; K-10, K-12
  - Files (1, ~190 lines): `apps/api/internal/ipos/service_status.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestOverride_BR_IPO_03|TestMarkSettled_BR_IPO_03|TestDelete_|TestList_FR009' ./internal/ipos/`
- [ ] T047 **backend-dev**: Close idempotency and audit gaps found by idempotency_test.go (replay for every mutation, one audit row per mutation) in the files that own them; no new files.
  - Depends on: T032, T046, 002-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: FR-007, FR-060; SC-005
  - Files (3, ~80 lines): `apps/api/internal/ipos/service_update.go`, `apps/api/internal/ipos/service_publish.go`, `apps/api/internal/ipos/service_status.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestIdempotency_FR007|TestAudit_SC005' ./internal/ipos/`
  - Note: Skipped if idT already passes after sv5 (then mark done with the command output).

## Phase 7 - Wiring and Phase A gate

- [ ] T048 **backend-dev**: Wire in cmd/swastik/main.go: ipos.NewService with Deps, ipos.RegisterJobs on the scheduler service (no HTTP yet; listener registration point documented).
  - Depends on: T047, T045, 002-merged
  - Rules: BR-IPO-03 | Refs: plan: Module placement; C-8 (rebase onto stage first)
  - Files (1, ~30 lines): `apps/api/cmd/swastik/main.go`
  - Done command (green): `cd apps/api && go build ./... && go test -race -short ./cmd/...`
- [ ] T049 **test-engineer**: Phase A gate: whole API suite incl. GS-01..04, integration BR_IPO tests, lint boundaries, sqlc up to date, coverage of internal/ipos >= 90 percent.
  - Depends on: T048, T004, T002, 002-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: quickstart §1-3; evidence only, no file edits
  - Files (0, ~0 lines): none (verification only)
  - Done command (green): `cd apps/api && go test -race ./... && golangci-lint run ./... && sqlc generate && sqlc vet && git diff --exit-code internal/platform/db/dbgen && go test -race -coverprofile=$(mktemp) ./internal/ipos/... && cd ../.. && bash scripts/check-lint-boundaries.sh`

## Phase 8 - HTTP layer tests first (needs 003)

- [ ] T050 **backend-dev**: Skeleton: handler.go Register(api, svc) calling three empty functions registerAdmin, registerActions, registerClient (handler_actions.go, handler_client.go) so handler tests compile and fail with 404.
  - Depends on: T049, 002-merged, 003-merged
  - Rules: none (mechanism) | Refs: plan: Build phases (Phase B)
  - Files (3, ~40 lines): `apps/api/internal/ipos/handler.go`, `apps/api/internal/ipos/handler_actions.go`, `apps/api/internal/ipos/handler_client.go`
  - Done command (green): `cd apps/api && go build ./... && go vet ./internal/ipos/...`
- [ ] T051 [P] **test-engineer**: handler_admin_test.go (humatest, authz.Install, fake verifier): create/get/list/update/flag status codes and error codes (ipo.sme_not_allowed, ipo.invalid_segment, validation_failed, ipo.invalid_value, ipo.date_order, ipo.publish_incomplete, ipo.stale_version), segments mapping, Idempotency-Key replay 201 to 200, Indian-formatted warning detail, route precedence of /date-suggestions over /{ipoId}.
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: FR-007, FR-010, FR-061; error-codes.md
  - Files (1, ~200 lines): `apps/api/internal/ipos/handler_admin_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestAdmin_' ./internal/ipos/`
- [ ] T052 [P] **test-engineer**: handler_actions_test.go: publish, unpublish, status override, delete (204 and replay 204), ipo.no_change, ipo.published_not_deletable, TestSuggest_US8_4_NoSideEffects, listener RejectedError -> 409 with its code, other listener error -> generic 500, TestAdminOps_FR050_ClientForbidden for every admin operation (nothing written).
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: FR-050, FR-035, FR-043; US6.4-6.5, US7.6, US8.4
  - Files (1, ~200 lines): `apps/api/internal/ipos/handler_actions_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestActions_|TestSuggest_|TestAdminOps_FR050' ./internal/ipos/`
- [ ] T053 [P] **test-engineer**: handler_client_test.go: TestClient_BR_IPO_02_OnlyPublished (3 of 5 listed, status filter), DraftLooksUnknown (byte-identical 404 for draft and random id), TestClient_FR051_NoAdminFields (JSON key set), UnpublishHidesNextRead, malformed cursor 422, limit default 50 and max 200, unauthenticated 401.
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-02 | Refs: FR-051; US7.1-7.5
  - Files (1, ~170 lines): `apps/api/internal/ipos/handler_client_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestClient_' ./internal/ipos/`
- [ ] T054 [P] **test-engineer**: problems_test.go: every code in contracts/error-codes.md maps to the documented HTTP status and urn type, FieldIssue to errors[].location body.<field>, values never echoed, listener RejectedError and unknown error cases. Parses error-codes.md so a new code without a mapping fails.
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: FR-061; constitution V
  - Files (1, ~120 lines): `apps/api/internal/ipos/problems_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestProblems_' ./internal/ipos/`
- [ ] T055 [P] **test-engineer**: cmd/swastik/ipos_contract_test.go: 12 operation ids exist with exact security (admin ops bearer [admin], ipos-list/ipos-get bearer []), every non-GET has Idempotency-Key, ClientIPO excludes source, source_ref, last_synced_at, status_source, version, published, segments items have no enum, PUT body requires all keys and accepts null, committed openapi.yaml is current.
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-02 | Refs: plan: Contract; FR-050, FR-051; US7.7, SC-007
  - Files (1, ~170 lines): `apps/api/cmd/swastik/ipos_contract_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestIPOContract_' ./cmd/swastik/...`
- [ ] T056 [P] **test-engineer**: perf_test.go: TestList_SC008_Budget with 500 seeded IPOs, 200 admin and 200 client list/detail calls, local p95 < 50 ms, EXPLAIN uses ipos_published_list_idx and ipos_admin_list_idx; skipped under -short.
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-02 | Refs: SC-008; constitution VII
  - Files (1, ~110 lines): `apps/api/internal/ipos/perf_test.go`
  - Done command (red): `cd apps/api && go test -race -run 'TestList_SC008' ./internal/ipos/`
- [ ] T057 [P] **test-engineer**: packages/domain/src/ipo.test.ts: IPO_STATUSES order matches the generated IPOStatus (compile-time satisfies), every ipo.* code from error-codes.md exists in ipoErrorCodes, ApplyFlag and segment constants.
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: plan: Contract (TS); constitution V
  - Files (1, ~70 lines): `packages/domain/src/ipo.test.ts`
  - Done command (red): `pnpm --filter @swastik/domain test`

## Phase 9 - HTTP implementation and wiring

- [ ] T058 [P] **backend-dev**: Implement api_types.go: Huma DTOs (IPODraft, IPOFacts, AdminIPO, ClientIPO as a separate type, Warning, IPOMutationResult, pages, DateSuggestion, VersionBody) and mapping from IPO; https-only URL tag, nullable and required tags per contract.
  - Depends on: T050, 002-merged, 003-merged
  - Rules: BR-IPO-02 | Refs: contracts/ipos.openapi.yaml; R-5
  - Files (1, ~190 lines): `apps/api/internal/ipos/api_types.go`
  - Done command (green): `cd apps/api && go build ./... && go vet ./internal/ipos/...`
- [ ] T059 **backend-dev**: Implement problems.go: error to httpx.Problem mapping with ipo.* codes, errors[] locations, Indian-formatted warning details via inr, RejectedError code passthrough, generic 500.
  - Depends on: T054, T058, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: error-codes.md; C-6
  - Files (1, ~150 lines): `apps/api/internal/ipos/problems.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestProblems_' ./internal/ipos/`
- [ ] T060 **backend-dev**: Implement registerAdmin in handler.go: ipos-create, ipos-admin-list, ipos-admin-get, ipos-update, ipos-set-flag, ipos-suggest-dates with authz.Require(RoleAdmin), principal org and actor, Idempotency-Key header, route order.
  - Depends on: T051, T059, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: plan: API operations; FR-050
  - Files (1, ~190 lines): `apps/api/internal/ipos/handler.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestAdmin_|TestSuggest_' ./internal/ipos/`
- [ ] T061 **backend-dev**: Implement registerActions in handler_actions.go: ipos-publish, ipos-unpublish, ipos-override-status, ipos-delete (204, version query) with idempotency replay.
  - Depends on: T052, T060, 002-merged, 003-merged
  - Rules: BR-IPO-02, BR-IPO-03 | Refs: plan: API operations
  - Files (1, ~150 lines): `apps/api/internal/ipos/handler_actions.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestActions_|TestAdminOps_FR050' ./internal/ipos/`
- [ ] T062 [P] **backend-dev**: Implement registerClient in handler_client.go: ipos-list and ipos-get (published only, identical 404), cursor encode/decode, limit bounds.
  - Depends on: T053, T060, 002-merged, 003-merged
  - Rules: BR-IPO-02 | Refs: FR-051; US7.*
  - Files (1, ~150 lines): `apps/api/internal/ipos/handler_client.go`
  - Done command (green): `cd apps/api && go test -race -run 'TestClient_' ./internal/ipos/`
- [ ] T063 **backend-dev**: Wire ipos.Register(api, svc) in cmd/swastik/main.go after authz.Install and before AssertDeclared; append the 10 admin operation ids to cmd/swastik/testdata/admin-operations.txt.
  - Depends on: T061, T062, T056, T048, 002-merged, 003-merged
  - Rules: BR-IPO-02 | Refs: C-5, C-8
  - Files (2, ~25 lines): `apps/api/cmd/swastik/main.go`, `apps/api/cmd/swastik/testdata/admin-operations.txt`
  - Done command (green): `cd apps/api && go test -race ./cmd/... && go test -race -run 'TestList_SC008' ./internal/ipos/`
- [ ] T064 **backend-dev**: Regenerate OpenAPI and TS client (go run ./cmd/swastik openapi > packages/api-client/openapi.yaml; pnpm --filter @swastik/api-client generate) and commit; diff must be additive IPO paths and schemas only.
  - Depends on: T063, T055, 002-merged, 003-merged
  - Rules: BR-IPO-02 | Refs: quickstart §4; oasdiff in CI (generated output not counted)
  - Files (2, ~10 lines): `packages/api-client/openapi.yaml`, `packages/api-client/src/schema.ts`
  - Done command (green): `cd apps/api && go test -race -run 'TestIPOContract_' ./cmd/swastik/... && cd ../.. && pnpm --filter @swastik/api-client generate && git diff --exit-code packages/api-client`
- [ ] T065 **backend-dev**: Add packages/domain/src/ipo.ts (IPO_STATUSES, ApplyFlag, segments, ipoErrorCodes) and export from index.ts.
  - Depends on: T057, T064, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: error-codes.md; constitution V
  - Files (2, ~80 lines): `packages/domain/src/ipo.ts`, `packages/domain/src/index.ts`
  - Done command (green): `pnpm --filter @swastik/domain test && pnpm turbo run typecheck`

## Phase 10 - Docs and final gate

- [ ] T066 [P] **backend-dev**: Accept ADR 0008: change its Status line from Proposed to Accepted once the owner has approved the plan (do not edit before approval), and link it from docs/adr index if one exists.
  - Depends on: T049, 002-merged
  - Rules: none (mechanism) | Refs: plan: Dependencies requiring owner approval (ADR 0008 acceptance)
  - Files (1, ~3 lines): `docs/adr/0008-in-transaction-module-signals.md`
  - Done command (green): `grep -q 'Status: Accepted' docs/adr/0008-in-transaction-module-signals.md`
  - Note: Needs explicit owner approval message before starting.
- [ ] T067 **test-engineer**: Final gate: quickstart §1-4 end to end. Whole suite incl. GS-01..04, lint and boundaries, sqlc and dbgen current, OpenAPI/TS client current and typecheck, coverage >= 90 percent, traceability script (BR-IPO-01/02/03 each have a named test).
  - Depends on: T064, T065, T066, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: quickstart; paste output as evidence; evidence only, no file edits
  - Files (0, ~0 lines): none (verification only)
  - Done command (green): `cd apps/api && go test -race ./... && golangci-lint run ./... && sqlc generate && sqlc vet && git diff --exit-code internal/platform/db/dbgen && go test -race -coverprofile=$(mktemp) ./internal/ipos/... && cd ../.. && bash scripts/check-lint-boundaries.sh && bash scripts/check-br-traceability.sh && pnpm turbo run typecheck test build`
- [ ] T068 **backend-dev**: Update docs/product/modules.md: 008 status to done after the final gate passes (building while in progress). Depends-on cell (003) is the owner's call per plan R-7; do not edit it.
  - Depends on: T067, 002-merged, 003-merged
  - Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 | Refs: CLAUDE.md: update modules.md status; plan R-7
  - Files (1, ~2 lines): `docs/product/modules.md`
  - Done command (green): `bash scripts/check-br-traceability.sh`
