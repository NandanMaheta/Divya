# Brief: 008 IPO catalogue  ·  Tier B

**Goal:** the admin lists mainboard IPOs, publishes them with an APPLY / DO NOT APPLY flag and note, and the status moves by date (IST) with admin override. Clients see published IPOs only. Dependent features (009-019, 026) get in-transaction signals (ADR 0008) and an eligibility check.

**Business rules:** BR-IPO-01 (mainboard only), BR-IPO-02 (flag, publish, eligible = published and APPLY), BR-IPO-03 (lifecycle by dates, reschedule). Golden scenarios: none driven; GS-01..04 must keep passing (GS-04 consumes the listing date).

**Acceptance criteria (spec.md):**
- US1 create/edit draft with exact paise, soft warning outside Rs 10,000-20,000, stale version and idempotent create rejected/replayed.
- US2 NSE Emerge, BSE SME, unknown or duplicate segment rejected on create and edit; DB CHECK backs it up.
- US3 publish needs flag, open, close, band or final price, lot size; unpublish; eligibility signal in the same tx.
- US4 OPEN 10:00 / CLOSED 17:00 / ALLOTMENT_OUT 10:00 / LISTED 10:00 IST; catch-up after downtime; never SETTLED by time.
- US5 date edits reschedule steps and send one DatesChanged signal atomically.
- US6 override any status with reason; past steps never re-fire; MarkSettled for 019.
- US7 clients list/get published only; drafts look unknown (identical 404).
- US8 weekday date suggestions T+1/T+2/T+2/T+3. US9 admin list filters, delete never-published drafts.

**Public surface:**
- Go: package `internal/ipos`: `NewService(Deps)`, `RegisterJobs`, `Register`, `OnDatesChanged/OnEligibilityChanged/OnStatusChanged`, `RequireEligible`, `MarkSettled`, `RejectedError` (see `contracts/go-module-api.md`); `internal/platform/inr.Format`.
- HTTP: 12 operations `ipos-*` under `/v1/ipos` (see `contracts/ipos.openapi.yaml`, `contracts/error-codes.md`); admin-only except `ipos-list`, `ipos-get`.
- DB: migration `00004_ipos.sql`: table `ipos` with CHECKs, partial unique indexes, published-delete trigger.

**Files to touch:** `apps/api/internal/ipos/**`, `apps/api/internal/platform/inr/`, `apps/api/queries/ipos.sql`, `apps/api/migrations/00004_ipos.sql`, `apps/api/.golangci.yml`, `apps/api/cmd/swastik/main.go`, `packages/api-client`, `packages/domain/src/ipo.ts`, ADR 0008, `docs/product/modules.md`.

**Depends on:** 002 (scheduler, clock, istdate, audit, db; not yet on stage) and 003 (authz, httpx.Problem) for the HTTP layer. See `docs/engineering/codemap.md`.

**Risks and non-goals:** migration and ADR 0008 need owner approval; 003 numbering (C-7); listener failure must roll back the whole change; no money computed (min application value only, integer paise). Non-goals: GMP (009), deals, tasks, reminders, import, SME support, UI.
