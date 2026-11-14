# Brief: 003 Auth core  ·  Tier A

**Goal:** Users, Argon2id passwords, sessions, refresh rotation with reuse detection, device binding, admin TOTP, lockout, first-admin bootstrap CLI, and the fail-closed `authz` layer every later feature relies on. No registration or OTP delivery (004/005).

**Business rules:** BR-CLI-04 (sign-in gate: only `active` users sign in, status model; tests carry `BR_CLI_04`), BR-CLI-05 (boundary only: capability to replace a contact). GS-01..04 unaffected, must keep passing. Everything else is mechanism (no rule ID).

**Acceptance criteria (spec.md user stories):**
- US-1 client sign-in by email or mobile, generic failure, no enumeration
- US-2 every operation declares Security; fail-closed contract walk
- US-3 refresh rotation, strict reuse detection, device binding
- US-4 lockout: 5 failures, 15 min, generic, shared across instances
- US-5 admin TOTP + recovery codes, replay protection
- US-6 sign-out, sign-out-everywhere, device list/revoke
- US-7 password change; US-8 admin session/status control
- US-9 `swastik admin create` bootstrap; US-10 account capabilities for 004/005/007
- US-11 audit trail in the same transaction, no secrets in logs/audit

**Public surface:**
- Go: `internal/auth` (`Service`, `Store`, `Throttle`, `Register`, `HousekeepingKind`); platform: `crypto`, `authz`, `httpx` (errors, CORS, ClientIP), `ratelimit`, `config` (auth, LoadDB). See `contracts/go-capabilities.md`.
- HTTP: 16 operations `auth-*` (sign-in, mfa-verify, enrolment, refresh, sign-out, devices, password-change, recovery codes, authenticator replace, admin user devices/sessions). See `contracts/openapi-auth.yaml`, `contracts/error-codes.md`.
- CLI: `swastik admin create` (`contracts/cli.md`).
- DB: `migrations/00003_auth.sql`: users, device_sessions, refresh_tokens, admin_mfa, mfa_recovery_codes, mfa_challenges, auth_throttles.

**Files to touch:** `apps/api/internal/auth/**`, `apps/api/internal/platform/{crypto,authz,httpx,ratelimit,config}`, `apps/api/queries/auth.sql`, `apps/api/migrations/00003_auth.sql`, `apps/api/cmd/swastik/*`, `apps/api/.golangci.yml`, `scripts/check-lint-boundaries.sh`, `packages/domain`, `.github/workflows/deploy.yml`.

**Depends on:** 001 (merged); 002 platform (clock, db.TxRunner, audit, istdate, scheduler, uuid sqlc override) for everything marked `002-merged`.

**Risks and non-goals:** Owner gates: auth migration, `deploy.yml`, security review (crypto, authz, audit, handlers). Non-goals: OTP delivery, registration, approval queue, social login, client-side screens. Main risk: 002 slip blocks 12 of 20 slices; pure units and schema proceed now.
