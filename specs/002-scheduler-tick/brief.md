# Brief: 002 Scheduler tick + scheduled_jobs + outbox  ·  Tier A

**Goal:** One authenticated tick (every 5 min, Cloud Scheduler) runs due jobs and relays outbox messages, so IPO lifecycle jobs and reminders fire late rather than never, with a fake-clock testable core. For the operator (status/dead-item admin) and all later features that schedule work.

**Business rules:** BR-IPO-03 (reschedule/cancel with date change; jobs run late, not never), BR-TASK-03 (quiet hours, de-dup, withdrawal, expiry), BR-SET-08 (IST date helpers). Golden scenarios: GS-04 (date and lifecycle timing).

**Acceptance criteria** (spec.md, FR ids):
- FR-002/003: tick accepts only the scheduler identity (OIDC); dev token only in local/ci; refuse dev auth on Cloud Run.
- FR-009/010/013: one pending job per key, never before run_at, reschedule while running is not lost.
- FR-011: no concurrent run; a crashed run is recovered at the next tick start (cancel > expire > rerun > takeover).
- FR-012/014: at-least-once, idempotent kinds; expired time-sensitive jobs never run.
- FR-017/018/019/020: retry with backoff, dead after max attempts, sanitised errors, admin list/retry/cancel with audit.
- FR-021: last tick and overdue status.

**Public surface:**
- Go: `internal/scheduler` (`Service`, `Schedule`, `Reschedule`, `Cancel`, `Tick`, `RegisterTick`), `internal/outbox`, `internal/platform/tickauth` (see `contracts/`).
- HTTP: hidden `POST /internal/tick` (not in OpenAPI); admin status and failed-item routes.
- DB: migration `00002_scheduler_outbox.sql`: `scheduled_jobs`, `outbox_messages`, `tick_runs`.

**Files to touch:** `apps/api/internal/scheduler/`, `apps/api/internal/outbox/`, `apps/api/queries/scheduler*.sql`, `apps/api/internal/platform/{config,tickauth,retry,httpx}`, `.github/workflows/deploy.yml`.

**Depends on:** foundation (001), `docs/engineering/codemap.md`; later: 003 (admin auth), 008, 013, 022/023.

**Risks and non-goals:** double run on lease loss (fenced Complete, idempotent kinds); unauthenticated tick on Cloud Run (config guards); tick budget 45 s. Non-goals: business job kinds, admin screens (022/023), external uptime alerting (027).
