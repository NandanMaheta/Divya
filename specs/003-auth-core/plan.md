# Implementation Plan: Auth core (users, passwords, sessions, refresh rotation, device binding, admin TOTP)

**Branch**: `feat/003-auth-core` | **Date**: 2026-11-13 | **Spec**: [spec.md](./spec.md) (owner-approved 2026-11-13: Option A, sessions 90 d / 30 d client and 7 d / 12 h admin with TOTP at each sign-in, D-01…D-22)

**Input**: Feature specification from `specs/003-auth-core/spec.md`, research in [research.md](./research.md) (sections 1–10 from the spec stage, section 11 added by this plan).

**Plan artifacts**: [data-model.md](./data-model.md) · [contracts/openapi-auth.yaml](./contracts/openapi-auth.yaml) · [contracts/error-codes.md](./contracts/error-codes.md) · [contracts/go-capabilities.md](./contracts/go-capabilities.md) · [contracts/cli.md](./contracts/cli.md) · [quickstart.md](./quickstart.md) · ADR [0007](../../docs/adr/0007-contact-data-at-rest.md) (new) · ADR [0004 Amendment 1](../../docs/adr/0004-auth.md) (proposed with this plan)

**Process note**: `.specify/scripts/bash/setup-plan.sh --json` ran in the worktree; `plan.md` already existed from an interrupted run, so the template was not copied again and this file was completed in place. `.specify/extensions.yml` has no `before_plan` / `after_plan` hooks. Feature 002's plan is final and is treated as an upstream contract (read-only): `specs/002-scheduler-tick/{plan.md, data-model.md, contracts/go-module-api.md}` on branch `feat/002-scheduler-tick`.

## Summary

003 adds sign-in, sessions and authorisation to the Go API. Every later feature depends on it.

- A new business module `internal/auth` owns users, password hashing, lockout counters, device sessions with rotating refresh credentials, admin TOTP with recovery codes, the account capabilities for 004/005/007, the housekeeping job handler, and the `swastik admin create` bootstrap command.
- **Reused from 002 (no duplicates)**: `internal/platform/clock` (`clock.Func`, `clock.Fake`), `internal/platform/db` (`TxRunner` carrying the transaction in `context`, `MustTx`, `ErrNoTx`), `internal/platform/audit` (`audit.Write` inside the caller's transaction), `internal/platform/istdate` (`NextDailyAt` for the job schedule), 002's scheduler job registry, `github.com/google/uuid` and the sqlc `uuid` override.
- **New platform packages, free of business imports**: `internal/platform/authz` (one global, fail-closed Huma middleware driven by each operation's declared `Security`; it depends only on a `Verifier` interface that `internal/auth` implements), `internal/platform/crypto` (AES-256-GCM key ring, HMAC-SHA-256 blind index, random tokens; 006 reuses it for PAN) and `internal/platform/ratelimit` (coarse per-IP limiter). `internal/platform/httpx` gains RFC 9457 problems with a stable `code`, CORS and client-IP helpers. `internal/platform/audit` gets one additive extension (explicit timestamp, nil-UUID to NULL).
- **Access credential**: a JWT signed with EdDSA (Ed25519) with a `kid` header for key rotation, valid for 15 minutes, built and verified with `github.com/go-jose/go-jose/v4` (already in the module graph through 002's owner-approved go-oidc). Every authenticated request also does one primary-key lookup of the device session and user, which is what makes revocation take effect on the next request (D-05).
- **Refresh credential**: 256 random bits, opaque, stored only as a SHA-256 digest. Each device session is one refresh-token family. Rotation happens in one transaction under row locks; strict reuse detection and device binding end the whole family on any reuse or `device_id` mismatch (D-10, D-11).
- **Transport by `client_type`**: `mobile` gets the refresh credential in the body. `web` (admin panel) gets it only in an `HttpOnly; Secure; SameSite=Strict` host-only cookie scoped to the refresh path, with Origin, custom-header, JSON-only and exact-CORS CSRF defences. This needs the admin panel and API on one registrable domain (D-21, feature 027).
- **Lockout**: a sliding 15-minute window in PostgreSQL rows keyed by an HMAC subject, identical across instances and for identifiers with no account. Stale rows are purged by a recurring scheduler job kind `auth.housekeeping` registered with 002's registry.
- **Passwords**: Argon2id (m=19 MiB, t=2, p=1) in a PHC string, rehashed on login when parameters change, at most N hashes in flight per instance, a dummy verify for unknown accounts, and an embedded 10k common-password list.
- **Admin TOTP**: RFC 6238 (SHA-1, 6 digits, 30 s, ±1 step) with a strictly increasing `last_used_step` replay guard and 10 hashed single-use recovery codes.
- **Migration**: `00003_auth.sql` (002 owns `00002_scheduler_outbox.sql`; 003 merges after 002).

## Technical Context

**Language/Version**: Go 1.27.4 (toolchain pinned in `apps/api/go.mod`).

**Primary Dependencies**: Existing: Huma v2.40.0, pgx v5.13.0, goose v3.30.1, sqlc v1.31.1 (CI), testcontainers-go v0.45.0. Arriving with 002 (owner-approved there): `github.com/coreos/go-oidc/v3` v3.21.0 with `github.com/go-jose/go-jose/v4`, and `github.com/google/uuid` v1.6.0 as a direct requirement. **Owner approval required by 003** (see the last section; nothing is added to `go.mod` before approval): `go-jose/v4` used directly for JWTs (indirect to direct, no new module), `github.com/pquerna/otp` v1.5.0 (new, with `github.com/boombuler/barcode`), `golang.org/x/crypto` (indirect to direct, argon2), `golang.org/x/term` (indirect to direct, no-echo prompt), `golang.org/x/text` (indirect to direct, NFC). Test-only: `pgregory.net/rapid` v1.2.0 (already in go.sum; 002 also uses it).

**Storage**: PostgreSQL 18 (Supabase in staging and production; `postgres:18-alpine` locally and in tests). Seven new tables; no change to `audit_log` columns. See [data-model.md](./data-model.md).

**Testing**: `go test -race ./...`; real PostgreSQL 18 through `dbtest.New(t)` (skipped with `-short`); `humatest` for handlers; `pgregory.net/rapid` for properties; 002's `clock.Fake` for every time-based rule; `golangci-lint` v2 (depguard, forbidigo, gosec); `sqlc vet`; CI OpenAPI diff plus oasdiff.

**Target Platform**: Cloud Run (asia-south1, Mumbai), Linux container. `swastik admin create` runs on the owner's machine against the target database.

**Project Type**: Web service (Go modular monolith) plus one operator CLI subcommand. No UI in 003 (screens are 022 and 024).

**Performance Goals**: API p95 under 200 ms in Mumbai (constitution VII, SC-007). An authenticated request adds one Ed25519 verify (~50 µs) and one primary-key join (~1–3 ms). Sign-in costs one Argon2id (~40–70 ms per vCPU, to be measured, R-6) plus one short transaction. Refresh is one transaction with SHA-256 only.

**Constraints**:

- Argon2 memory is ~19 MiB per hash in flight, capped by a semaphore (default 4, ~76 MiB). The pgx pool has `MaxConns=5`, so Argon2 always runs **outside** any transaction.
- `swastik openapi` must build the API with no database, no keys and no config.
- No Redis, no background goroutines for business work; housekeeping runs only through 002's `/internal/tick`.
- Time comes only from the injected `clock.Func`; SQL receives `@now`, never `now()`, for anything behavioural (002's forbidigo rule enforces `time.Now`/`time.Since`/`time.Until`/`time.Local`).

**Scale/Scope**: One organisation, one admin, about 100 clients, 1–3 devices each. Every list in 003 is unpaginated.

No open NEEDS CLARIFICATION items: every architect note in the spec is resolved in research §11.

## Constitution Check

*GATE: checked before Phase 0 and re-checked after Phase 1. Result: **PASS with two documented deviations** (Complexity Tracking).*

| Principle | How the plan complies | Status |
|---|---|---|
| I. Business rules are the source of truth | Every behaviour traces to the spec (D-01…D-22, FR-001…FR-093) or BR-CLI-04. BR-CLI-05 stays in 007; 003 provides only FR-055. One reading is applied from D-08's own text and flagged for the owner: because D-08 counts "wrong current password on change-password" as a failed attempt and says "attempts during the lock fail", a re-authentication (change password, regenerate recovery codes, replace authenticator) is refused while the account is locked; the existing session keeps working. Tests carry `BR_CLI_04` wherever the status gate is exercised. | PASS |
| II. Money exact, history immutable | No money. Every mutation writes exactly one `audit_log` row in the same transaction through 002's `audit.Write`, which refuses to write without a transaction (`db.ErrNoTx`). `audit_log` stays append-only (001 trigger). The housekeeping job writes one summary audit row per run when it deletes anything. | PASS |
| III. Test-first, layered, deterministic | `test-engineer` writes tests first (strategy below). 002's `clock.Fake` drives the service, JWT validation (`jwt.Expected.Time`), TOTP, lockout, challenge expiry and the housekeeping job; SQL takes `@now`; audit rows take `At` from the clock. | PASS |
| IV. Modular monolith, hard boundaries | `internal/auth` is a business module. `platform/authz`, `platform/crypto` and `platform/ratelimit` import no business module; `authz` depends only on its own `Verifier` interface, implemented in `internal/auth`. depguard's `platform-is-a-leaf` list gains `internal/auth`; go-jose, argon2 and pquerna/otp are confined to `internal/auth` (P-19). The scheduler job is wired in `cmd/swastik` through an adapter, so `auth` does not import `scheduler`. | PASS |
| V. Contract-first APIs | Every operation declares `Security`; a static and runtime contract walk fails the build on undeclared or unlisted public operations (FR-034). Errors are RFC 9457 with a stable `code` member (FR-093). The OpenAPI diff is additive; the TS client is regenerated. **Deviation 1**: credential operations do not accept `Idempotency-Key`. | PASS (justified) |
| VI. Security and privacy by default | Argon2id; 15-minute access tokens; rotating, device-bound refresh families; admin TOTP; AES-GCM for the TOTP secret with the key outside the database; HMAC blind-index helper ready for PAN (006). Contact data readable per **ADR 0007** (owner Option A). No secrets in logs, audit or responses (scan test; Huma's echo of raw input is stripped). Keys from Secret Manager in staging/production. **Deviation 2**: local and CI generate ephemeral keys when none are set. | PASS (justified) |
| VII. Simple, fast, accessible | One genuinely new third-party module pair (pquerna/otp + barcode), one indirect-to-direct JOSE promotion that adds no module, three `golang.org/x` promotions. CORS, PHC encoding, mobile normalisation and the rate limiter use the standard library. p95 analysed below. Error codes let UIs translate (English, Gujarati, Hindi). | PASS |
| Workflow 5 / 7 | Auth schema, `internal/auth`, `platform/authz`, `platform/crypto` and the migration need owner approval and a security review; new dependencies are listed for approval and **not** added yet. | PASS (gated) |

Post-design re-check (after data-model, contracts and the 002 coordination below): no new violations; the two deviations are unchanged.

## Coordination with feature 002 (scheduler tick)

002's plan is final; 003 adapts to it. **003's implementation PR merges after 002 Phase A is on `main`** and is rebased onto it.

| # | Topic | Agreement | 003 work |
|---|---|---|---|
| C-1 | Migration numbering | 002 owns `00002_scheduler_outbox.sql`; 003 uses `00003_auth.sql`. Because 003 merges after 002, versions are in order and nobody renumbers. | None beyond the file name. If the merge order ever flipped, 003 would wait rather than renumber. |
| C-2 | Clock | `clock.Func` (`func() time.Time`), `clock.Real` wired only in `cmd/swastik`, `clock.Fake` (`NewFake`, `Now`, `Set`, `Advance`) in tests. | `auth.Deps.Now clock.Func`; no `authtest.Clock`. |
| C-3 | Transactions | `db.TxRunner` (struct) with `InTx` (nested calls join), `Conn(ctx)`, `db.MustTx(ctx)`, `db.ErrNoTx`. | The auth store builds `dbgen.New(txr.Conn(ctx))`; account capabilities join a caller's transaction (005 registration). No new `tx.go`. |
| C-4 | Audit writer | `audit.Write(ctx, audit.Entry{OrgID, Actor{ID, Kind}, Entity, EntityID, Action, Before, After})` requires a transaction. | **Additive extension in 003's PR** (with tests), unless 002's code already has it: `Entry.At time.Time` (zero keeps the column default, 003 always sets it from the clock, so US-4 s1 "audit entry at T+4m" is testable) and `uuid.Nil` for `EntityID` / `Actor.ID` stored as SQL NULL (failures for unknown identifiers, system actor). 002's `InsertAuditLog` query gains `COALESCE(sqlc.narg('created_at'), now())`. 002's existing calls keep working unchanged. |
| C-5 | IST dates | `istdate.NextDailyAt(now, WallClock)`. | Used for the `auth.housekeeping` schedule only; auth times are UTC and rendered in IST by UIs. |
| C-6 | IDs | `github.com/google/uuid` direct, sqlc override `uuid → uuid.UUID` / `uuid.NullUUID`. | All 003 Go signatures use `uuid.UUID` (the earlier draft's "UUID strings" decision is withdrawn). |
| C-7 | Scheduler job | 002's `Service.Register(kind, Handler, KindOptions{Recurring})`; handlers run inside the job transaction. | Kind `auth.housekeeping` (P-20), registered in `cmd/swastik` through an adapter. |
| C-8 | Hidden tick operation | 002 registers `POST /internal/tick` as a `Hidden` Huma operation with no `Security`; its handler authenticates with `platform/tickauth`. Huma applies global middlewares to hidden operations too, so 003's fail-closed middleware would answer it with 500. | 003's PR adds one line to 002's tick registration: `Security: authz.SelfAuthenticated()`. That declaration means the operation authenticates itself and is valid **only** on `Hidden` operations: `authz.AssertDeclared` panics at startup if a documented operation carries it, and the middleware answers 500 if a non-hidden operation does. A wiring test proves `POST /internal/tick` without credentials still returns tickauth's 401 and does no work. |
| C-9 | 002 Phase B admin operations | 002's `/v1/admin/scheduler/*` and `/v1/admin/outbox/*` use `bearer [admin]`, org and actor from 003's principal, and `Idempotency-Key` replay via an audit lookup. | `authz.Require(authz.RoleAdmin)`, `authz.MustPrincipal(ctx)` and `Principal.AuditActor()` serve them; Phase B appends its operation ids to `cmd/swastik/testdata/admin-operations.txt`. |
| C-10 | Lint configuration | 002 extends `platform-is-a-leaf` (health, scheduler, outbox), adds `oidc-only-in-tickauth` and the forbidigo time rules. | 003 appends `internal/auth` and adds the JOSE, argon2 and OTP confinement rules (P-19). |
| C-11 | Shared files | Both features edit `platform/config/config.go`, `platform/httpx/httpx.go`, `cmd/swastik/main.go`, `.golangci.yml`, `sqlc.yaml` (002 only) and `queries/platform.sql`. | 003 puts new code in new files (`httpx/errors.go`, `cors.go`, `clientip.go`; `config/auth.go`) and rebases. |
| C-12 | JWT library | go-oidc brings `go-jose/v4`; 003 evaluated it against `golang-jwt/jwt/v5` (P-02). | Recommend go-jose/v4; owner approves the direct use. |

## Key decisions

Decisions numbered P-xx are referenced by data-model, contracts and tasks.

### P-01 Module layout and dependency direction

```text
cmd/swastik ──> internal/auth ──> internal/platform/{authz, crypto, ratelimit, audit, db, db/dbgen, clock, httpx, config}
          ──> internal/scheduler (002) ── Register("auth.housekeeping", adapter → auth.Service.Housekeep)
          ──> internal/health ─> huma only
internal/platform/* ──X──> any business module (depguard)
internal/platform/authz ──> Verifier interface (its own) <── implemented by internal/auth (bearer, mfaChallenge)
future modules (004, 005, 007, 022, 002 Phase B …) ──> internal/auth (Accounts, Throttle interfaces)
                                                   ──> internal/platform/authz (Require, PrincipalFrom)
```

- `internal/auth` has no HTTP types in service signatures and no pgx types outside `store_pg.go`. IDs are `uuid.UUID` (C-6).
- `platform/authz` defines `Principal{UserID, OrgID, SessionID uuid.UUID; Role}`, `Principal.AuditActor() audit.Actor`, the `Verifier` interface, `Install(api, map[scheme]Verifier)`, `Require(roles...)`, `Public()`, `ChallengeOnly()`, `SelfAuthenticated()`, `PrincipalFrom(ctx)`, `MustPrincipal(ctx)` and `AssertDeclared(api)`. It never parses tokens itself. Contract: [contracts/go-capabilities.md](./contracts/go-capabilities.md).

### P-02 Access credential: EdDSA JWT with `kid` rotation plus a per-request session lookup

- **Token**: header `{"alg":"EdDSA","typ":"at+jwt","kid":"<kid>"}`; claims `iss="swastik-api"`, `aud=["swastik-api"]`, `sub=user_id`, `sid=session_id`, `org=org_id`, `role`, `iat`, `exp`, `jti` (128-bit random). `exp = min(now+15m, session.absolute_expires_at)`, truncated to whole seconds (NumericDate).
- **Library**: `github.com/go-jose/go-jose/v4` (`jose` + `jose/jwt`), confined to `internal/auth/tokens.go` by depguard.
  - Sign: `jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: jose.JSONWebKey{Key: ed25519.PrivateKey, KeyID: kid}}, (&jose.SignerOptions{}).WithType("at+jwt"))`, then `jwt.Signed(signer).Claims(std).Claims(custom).Serialize()`.
  - Verify, in this order (each failure is `auth.unauthenticated`):
    1. `jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.EdDSA})`: the v4 API requires the algorithm allowlist, so `none`, HS256 and RS256 are rejected before any key is touched.
    2. Exactly one signature header; `Headers[0].KeyID` must be in the configured ring (unknown kid rejects; `jku`, `jwk` and `x5u` headers are never consulted).
    3. `Headers[0].ExtraHeaders["typ"] == "at+jwt"` (go-jose does not check `typ`).
    4. `tok.Claims(publicKey, &std, &custom)` verifies the signature.
    5. `std.ValidateWithLeeway(jwt.Expected{Issuer: "swastik-api", AnyAudience: {"swastik-api"}, Time: now()}, 0)`; `Expected.Time` is always set from the clock (go-jose falls back to `time.Now()` when it is zero, and a unit test pins this).
    6. Explicit checks that go-jose leaves to the caller: `std.Expiry != nil`, `std.IssuedAt != nil`, and **`now < exp`**. go-jose treats a token as still valid at exactly `exp` (`validationTime.After(exp)`); the spec's edge cases make limits inclusive of expiry, so the extra check rejects at `exp`.
- **Per-request lookup**: after the signature check the bearer verifier runs `GetSessionPrincipal(sid)` (one join of `device_sessions` and `users` by primary key). The request is rejected as `auth.unauthenticated` unless `ended_at IS NULL`, `now < idle_expires_at`, `now < absolute_expires_at`, `users.status = 'active'`, `users.role = role claim` and `user_id = sub`.
- **Trade-off, stated explicitly**: with the lookup the JWT is, in practice, a signed carrier for `sid`. The lookup costs ~1–3 ms per request; without it sign-out, revoke, disable and password change would lag by up to 15 minutes, violating D-05. ADR 0004 (JWT, 15 min) still holds and is amended to record the lookup (ADR 0004 Amendment 1). The JWT still gives cheap rejection of forged or expired tokens before any database access and a standard `bearerFormat: JWT` in the contract.
- **Key ring and rotation**: `JWT_SIGNING_KEYS="kid1:<base64 32-byte Ed25519 seed>,kid2:…"` and `JWT_ACTIVE_KID`. Every key in the ring verifies; only the active key signs. Rotation is a two-deploy procedure (add, then activate, then remove after 15 minutes plus rollout); see [quickstart.md](./quickstart.md#key-rotation).

### P-03 Refresh-token families, rotation, strict reuse detection, device binding

- **Family = device session.** `refresh_tokens.session_id` is the family id; a family has exactly one unused token at a time (partial unique index), and every earlier token stays as a used row so reuse is recognisable.
- **Format**: `drt_` + base64url(32 bytes from `crypto/rand`). Stored as `sha256(raw)` (32 bytes). The prefix helps secret scanners and the FR-091 scan test. `crypto.NewToken(prefix)` returns `(plain, digest)`.
- **Rotation algorithm** (one transaction, READ COMMITTED; lock order `users` → `device_sessions` → `refresh_tokens`):
  1. Look up the token row by digest without a lock. Not found: `auth.refresh_rejected`, no audit (nothing to attribute).
  2. `SELECT … FROM users WHERE id=$u FOR UPDATE`, then `device_sessions … FOR UPDATE`, then `refresh_tokens … FOR UPDATE`; re-read after the locks are held.
  3. Session already ended: reject, no change.
  4. `used_at IS NOT NULL`: **reuse**. End the session (`end_reason='refresh_reuse'`), audit `auth.session.refresh_reuse_detected`, commit, return `auth.refresh_rejected`.
  5. Request `device_id` differs from the session's: **device mismatch**. End the session (`end_reason='device_mismatch'`), audit `auth.session.device_mismatch`, commit, reject.
  6. Transport differs from the session's `client_type` (cookie for a `mobile` session, body for a `web` session): reject, no change (cannot happen legitimately).
  7. `now >= idle_expires_at` or `now >= absolute_expires_at`: reject (expired; computed, not written).
  8. `users.status <> 'active'`: reject.
  9. Otherwise: set `used_at=now`, `replaced_by` on the old row; insert the new row; set `last_active_at=now`, `idle_expires_at=now+idle(role)`; audit `auth.session.refreshed`; commit; then sign the access JWT.
  10. **A lock does not block refresh** (D-08, FR-043): the throttle table is not read here.
- **Concurrency (D-10)**: two refreshes presenting R1 both block on the `users` row lock; the second re-reads after the first commits, sees `used_at`, takes the reuse branch and ends the family. Exactly one succeeds. Tested with a start barrier and N=2…8 goroutines.
- **No grace window and no idempotent replay** (P-16). Lost-response recovery is a client concern: 022 and 024 keep one refresh in flight and persist the new credential before using it (R-5).
- **Same installation signs in again (D-11)**: the success transaction locks the user row, ends any live session with the same `(user_id, device_id)` (`end_reason='replaced'`) and inserts the new one; backed by the partial unique index `device_sessions(user_id, device_id) WHERE ended_at IS NULL`.

### P-04 Admin browser cookie flow, CSRF defences, shared registrable domain

- `client_type` is required on sign-in (`mobile` or `web`) and stored on the session. `platform` (`android`, `iphone`, `web`) is informational.
- **mobile**: `refresh_token` in the JSON body; the device keeps it in secure storage (024); refresh sends `{device_id, refresh_token}`.
- **web**: the body **never** contains `refresh_token`. The response sets
  `Set-Cookie: __Secure-swastik_rt=<token>; Path=/v1/auth/refresh; HttpOnly; Secure; SameSite=Strict; Max-Age=<seconds to absolute expiry>`
  - No `Domain` attribute: host-only on the API host. `__Secure-` prefix forces `Secure`.
  - The access token lives only in SPA memory; a reload calls refresh.
  - Sign-out, sign-out-everywhere and self-revocation of the current web session clear the cookie (`Max-Age=0`, same Path).
- **CSRF defences**, applied to refresh when the credential comes from the cookie, and to every operation that sets the cookie (`client_type=web` sign-in, mfa-verify, enrolment-confirm):
  1. `Origin` must exactly equal one entry of `WEB_ORIGINS`; missing or `null` is rejected.
  2. Header `X-Swastik-CSRF: 1` is required (forces a CORS preflight; a form cannot set it).
  3. `Content-Type: application/json` only (Huma returns 415 otherwise; the handler re-checks).
  4. CORS: exact-origin allowlist with `Access-Control-Allow-Credentials: true`; never `*`, never a pattern; `Vary: Origin`.
  5. `SameSite=Strict` as defence in depth (OWASP: sibling subdomains are same-site, so it is not relied upon alone).

  Failure: 403 `auth.csrf_rejected`. Ordinary API calls carry the access token in `Authorization`, which browsers never attach automatically, so they need no CSRF token.
- **Shared registrable domain (D-21, dependency on 027)**: staging and production serve the admin panel and API under one registrable domain (for example `admin.<domain>` and `api.<domain>`), so the API cookie is first-party for the panel. Substitutes until then:
  - Local: the Vite dev server proxies `/v1` to `:8080`, so panel and API are same-origin on `http://localhost` (Chromium and Firefox treat localhost as a secure context, so `__Secure-` cookies work; local Safari unsupported).
  - Pull-request previews (`*.pages.dev` with `*.run.app`): the cookie cannot work cross-site. 022 either uses a same-origin proxy (027) or signs in with `client_type=mobile` and keeps both tokens in memory only. The API does not restrict `client_type` by role, so this needs no API change.
- Cloudflare Access fronts only the admin SPA (027); the API never relies on it for authorisation. The iPhone PWA transport is decided in 024 (both transports exist).

### P-05 Admin second factor: interim proof, TOTP with replay protection, recovery codes

- After a correct password for an `active`, unlocked admin, sign-in returns `next_step=mfa_required` (TOTP confirmed) or `mfa_enrolment_required` (none confirmed) and a `challenge`. **No session is created.**
- **Challenge token** (interim proof): `dmc_` + base64url(32 random bytes), stored as SHA-256 in `mfa_challenges` with `purpose` (`verify` or `enrol`), `expires_at = now+5m` (expired at exactly 5 minutes), `consumed_at` and the device details from sign-in. Sent as `Authorization: Bearer dmc_…` only to operations declared `authz.ChallengeOnly()`. It is opaque, so the bearer (JWT) verifier rejects it, and the challenge verifier rejects JWTs and `drt_` tokens because their digests never exist in `mfa_challenges` (FR-035). Wrong purpose: `auth.unauthenticated`. Single use: `consumed_at` is set in the transaction that creates the session.
- **Lockout across factors**: the counter is cleared only after the second factor succeeds (clearing at the password step would allow unlimited code guesses). Wrong TOTP or recovery codes count on the same `user` subject (D-08); while locked, verify answers `auth.invalid_credentials` even for a correct code.
- **TOTP** (library `github.com/pquerna/otp` v1.5.0, pending approval; stdlib fallback below):
  - Enrolment: `totp.Generate(GenerateOpts{Issuer: "Swastik", AccountName: email, SecretSize: 20, Algorithm: SHA1, Digits: 6, Period: 30, Rand: crypto/rand.Reader})` gives the base32 secret (no padding) and the `otpauth://totp/Swastik:<email>?…` URI, returned once. The panel draws the QR code client-side (022); `Key.Image()` is never called.
  - Verify: `N = floor(now/30)`; for each candidate step `s ∈ {N−1, N, N+1}` with `s > admin_mfa.last_used_step`, call `hotp.ValidateCustom(code, s, secret, {Digits: 6, Algorithm: SHA1})` (constant-time compare inside). All candidates are evaluated; the matched step is written as the new `last_used_step` under `SELECT … FOR UPDATE` on `admin_mfa`, so two parallel submissions of one code give exactly one success and no code from the same or an earlier step is ever accepted again (FR-061, US-5 s4–s5). `totp.Validate` is not used because it does not report the matched step.
  - Fallback if the owner declines the library: ~40 lines of RFC 6238 on `crypto/hmac` + `crypto/sha1` + `encoding/base32` in `totp.go`; the tests (RFC 6238 Appendix B vectors, replay, skew) are identical.
- **Secret at rest**: `crypto.Seal(secret, aad="admin_mfa.secret:"+user_id)`. Enrolment and replacement keep a pending secret (`pending_secret_enc`, AAD `admin_mfa.pending_secret:<user_id>`) until a code from it is confirmed; calling start again replaces it.
- **Recovery codes**: 10 codes of 80 random bits each, Crockford base32 shown as `XXXX-XXXX-XXXX-XXXX`, returned once at enrolment or regeneration. Input is normalised (uppercase, hyphens and spaces removed, Crockford aliases `O→0`, `I/L→1`) and stored as SHA-256 (80 bits of entropy makes a fast hash sufficient). Single use (`used_at`); regeneration (password + current code) sets `revoked_at` on the previous batch.
- **Replacing the authenticator**: start needs password plus a current code or an unused recovery code (consumed and audited `auth.mfa.recovery_code_used`); confirm needs a code from the new secret, swaps the secrets, sets `last_used_step` from the new secret's matched step, ends all **other** admin sessions (`end_reason='mfa_replaced'`) and audits `auth.mfa.replaced`. Recovery codes are not regenerated by a replacement.
- Clients are never asked for TOTP (FR-065): a client's correct password always leads to `signed_in`.

### P-06 Passwords: policy, Argon2id, PHC, rehash, concurrency cap, dummy verify

- **Policy (D-07)**: NFC-normalise with `golang.org/x/text/unicode/norm` so Gujarati and Hindi passphrases typed with different input methods hash the same; count Unicode code points; check in this order and return the first failing rule's code (FR-011, FR-093):
  1. under 10: `auth.password_too_short`
  2. over 256: `auth.password_too_long`
  3. equals own email (trimmed, case-insensitive) or normalises to own mobile: `auth.password_matches_identifier`
  4. on the common list (case-insensitive): `auth.password_too_common`
  5. on change, equals the current password: `auth.password_unchanged` (checked by verifying the new password against the stored hash, outside the transaction)

  No truncation, no composition rules. Request schemas cap raw input at 1024 bytes (current/sign-in) and 2048 bytes (new password) so a 256-code-point Devanagari passphrase (up to 768 bytes) fits while hash work stays bounded.
- **Common list**: `internal/auth/commonpasswords/10k.txt`, embedded with `go:embed`. Source: SecLists `Passwords/Common-Credentials/10k-most-common.txt` (MIT), lowercased and de-duplicated when committed. `SOURCE.md` records URL, retrieval date and SHA-256; a unit test pins line count and SHA-256 so changes are deliberate. Loaded once into a `map[string]struct{}` (~10k entries, <1 MiB).
- **Hash**: Argon2id (`golang.org/x/crypto/argon2.IDKey`), m=19456 KiB, t=2, p=1, 16-byte salt, 32-byte key. PHC encoding `$argon2id$v=19$m=19456,t=2,p=1$<b64salt>$<b64hash>` (raw standard base64, same as alexedwards/argon2id). Decoding rejects unknown versions and parameters outside sane bounds (m ≤ 256 MiB, t ≤ 10, p ≤ 4) so a tampered hash cannot cause a memory blow-up. Comparison with `subtle.ConstantTimeCompare`.
- **Rehash on login**: if the stored PHC parameters differ from the current ones, a new hash is computed after a successful verify (outside the transaction) and written in the success transaction with `WHERE password_hash = @old`.
- **Concurrency cap**: a buffered-channel semaphore of size `ARGON2_MAX_CONCURRENCY` (default 4). Waiting is bounded by the request context and 2 seconds; timeout returns 503 `auth.busy`, independent of the account.
- **Dummy verify**: when no account matches, the presented password is verified against a dummy PHC hash computed once (`sync.Once`) with the current parameters. Every sign-in attempt therefore performs **exactly one** Argon2 verify whether the account is unknown, the password wrong, the account locked or not active (SC-003).
- **Never inside a transaction** (pool of 5).
- **Change password**: verify current and hash new outside the transaction; then lock the user row, check `password_hash` still equals the verified hash (else retry once, then fail), update hash and `password_changed_at`, end all **other** live sessions (`end_reason='password_changed'`), audit `auth.password.changed`.
  - Wrong current password: 403 `auth.reauth_failed`, counted (D-08). While locked: 403 `auth.reauth_failed` without verifying (Constitution Check I).
  - **Safe retry**: if the current password fails but the presented `new_password` verifies against the stored hash, the change already happened (lost response): return 204 without counting. One extra Argon2 verify, only on this path.

### P-07 Lockout (D-08), generic and shared across instances

- **Table** `auth_throttles(org_id, scope, subject, failures timestamptz[], locked_until, updated_at)`, primary key `(org_id, scope, subject)`.
- **Subject** = `BlindIndexer.Index("throttle", scope|kind|value)`:
  - `user|<user_id>` when an account matches the identifier and role (the lock is per account, so a client's email and mobile share a counter);
  - `email|<normalised>` or `mobile|<normalised>` when none matches (including an admin's mobile, because admins sign in with email only).
- **Pure function** `throttle.Apply(state, now, policy) (state, startedLock bool)`:
  - locked iff `now < locked_until`;
  - a failure during a lock changes nothing (no extension);
  - if a lock has ended (`locked_until <= now`), `failures` is cleared first ("the end of the lock clears the counter");
  - otherwise keep failures with `now - f < 15m`, append `now`, keep at most 5;
  - at 5: `locked_until = now + 15m`, `failures = []`, `startedLock = true`.

  A rapid property test compares it with a reference model that keeps every failure. Note for the owner: under this exact sliding window, US-4 scenario 5 has 2 failures in the window at T+17m (T+3m and T+17m), not the "1" its wording says; the outcome (not locked) is the same and tests assert the outcome.
- **Writes**: on failure `INSERT … ON CONFLICT DO NOTHING`, `SELECT … FOR UPDATE`, apply, `UPDATE`, all in the failure audit's transaction. On success, `DELETE` the subject row. On password reset (FR-053), `DELETE` the user subject.
- **Generic for 004**: `scope` is CHECK-listed (`'signin'` now; 004's migration extends it). 004 uses the exported `auth.Throttle` interface, never the table.
- **Housekeeping**: stale rows are deleted by the `auth.housekeeping` job (P-20). Correctness never depends on it: `Apply` ignores stale failures and expired locks.

### P-08 Sign-in flow (client and admin)

1. **Validate shape.** An identifier containing `@` is an email: trim, lowercase, `net/mail` parse without display name, ≤254 characters. Otherwise a mobile: strip spaces and hyphens, accept `[6-9]\d{9}` with optional prefix `0`, `91` or `+91`, normalise to `+91XXXXXXXXXX`. Anything else: 422 `auth.identifier_invalid` (D-09 allows this). Also validate `device{id uuid, name 1..100, platform}` and `client_type`.
2. **Read without locks**: the default org (P-12), the user by email or mobile (a mobile identifier matching an admin counts as no match), the throttle state.
3. **Exactly one Argon2 verify** (real or dummy), outside any transaction; rehash if needed.
4. **Decide**: no match or wrong password → failure, counted; locked → failure, not counted; correct password but not `active` → failure, not counted (BR-CLI-04); client → success; admin → challenge.
5. **One short transaction**: failure → throttle apply (when counted), audit `auth.sign_in.failed` with an internal `reason`, plus `auth.lockout.started` when a lock starts. Client success → lock the user row, re-check status and lock (the state may have changed during hashing), end the same-device session, insert session and refresh token, update `last_sign_in_at`, apply the rehash, delete the throttle row, audit `auth.sign_in.succeeded`. Admin → insert `mfa_challenges`, audit `auth.mfa.challenge_issued`.
6. **Respond**: every failure is the same 401 `auth.invalid_credentials` body byte-for-byte, with no `Retry-After`.

### P-09 Every operation declares Security (FR-030…FR-035)

- **Security schemes** registered by `authz.Install` in `components.securitySchemes`: `bearer` `{type: http, scheme: bearer, bearerFormat: JWT}` (requirement scopes carry role names, which OpenAPI 3.1 allows for non-OAuth schemes) and `mfaChallenge` `{type: http, scheme: bearer, bearerFormat: opaque}`.
- **Declaration helpers**: `authz.Require()` → `[{bearer: []}]` (any signed-in role); `authz.Require(authz.RoleAdmin)` → `[{bearer: ["admin"]}]`; `authz.ChallengeOnly()` → `[{mfaChallenge: []}]`; `authz.Public()` → `[]` (explicitly empty; Huma serialises it because `security` is `omitNil`, verified in v2.40.0 `openapi.go`); `authz.SelfAuthenticated()` → `[]` plus `Metadata["authz"]="self"`, allowed only on `Hidden` operations (C-8).
- **Middleware**, installed with `api.UseMiddleware` *before* any `huma.Register` (Huma captures global middlewares at registration, and applies them to hidden operations too):
  - `op.Security == nil` → 500 `platform.security_undeclared` (fail-closed; the startup guard keeps this out of production).
  - empty → `next`.
  - otherwise (exactly one requirement object, enforced at startup): pick the verifier by scheme name (unknown scheme panics at startup), extract `Authorization: Bearer`, verify; error → 401 `auth.unauthenticated` with `WWW-Authenticate: Bearer`; role scopes present and principal's role not listed → 403 `auth.forbidden`; else `huma.WithValue(ctx, principalKey, p)`.
  - It runs before body parsing and validation, so unauthenticated calls never reach validation or handlers.
- **Startup guard**: `authz.AssertDeclared(api)` at the end of `buildAPI` panics if any documented operation has nil `Security`, names an unregistered scheme, or uses `SelfAuthenticated` without `Hidden`. `swastik openapi`, `serve` and tests fail fast. Hidden operations are not in `Paths`; for them the runtime 500 is the fail-closed backstop, and C-8's wiring test covers the tick.
- **Contract-walk tests** (`cmd/swastik/contract_test.go`):
  - (a) static walk over `buildAPI(deps{}).OpenAPI().Paths`: every operation declares Security; operations with empty Security are exactly `{health-live, health-ready, auth-sign-in, auth-refresh}`; `mfaChallenge` operations are exactly `{auth-mfa-verify, auth-mfa-enrolment-start, auth-mfa-enrolment-confirm}`; every admin-scoped operation id is listed in `testdata/admin-operations.txt` (and vice versa); no operation id contains an FR-050…055 capability name.
  - (b) runtime walk: for every non-public operation send no credential, a tampered JWT, an expired JWT, a JWT for a revoked session, a refresh token, a challenge token, and (admin operations) a valid client JWT. Assert 401/403 with the right `code` and, via a recording fake store, that nothing was written. Path parameters get random UUIDs, bodies `{}`.

  The walk covers every future feature's operations automatically.

### P-10 RFC 9457 errors with stable codes (FR-093)

- `httpx` defines `type ErrorModel struct { huma.ErrorModel; Code string }` with the JSON tag `json:"code"` on `Code`; the schema name stays `ErrorModel`, so the diff is additive.
- `httpx.InstallErrors()` overrides `huma.NewError` / `huma.NewErrorWithContext` once (idempotent): every problem gets `code` (default from status: `validation_failed`, `bad_request`, `not_found`, `unsupported_media_type`, `rate_limited`, `internal`) and `type = urn:swastik:problem:<code>`; **`ErrorDetail.Value` is always dropped**, because Huma v2.40.0 echoes the raw body on parse errors (`validateBody`: `Value: string(body)`) and field values on validation errors, which would put passwords and tokens into responses (FR-091).
- Domain errors use `httpx.Problem(status, code, detail)`. `code` is a plain string in the schema (an enum would make later additions breaking for oasdiff). Catalogue: [contracts/error-codes.md](./contracts/error-codes.md), mirrored in `packages/domain/src/auth-error-codes.ts` for UI translation (022, 024).

### P-11 Audit in the same transaction (FR-090, FR-091)

- 001's `audit_log`, written only through 002's `audit.Write` (C-4). No new audit table and no column change.
- Mapping: `entity='user'`, `entity_id` = affected user (NULL via `uuid.Nil` for unknown identifiers); `Actor` from US-11 (system actor uses `uuid.Nil` → NULL); `action` from the catalogue in [data-model.md](./data-model.md); `Before`/`After` are typed Go structs, never maps built from request input; `After` carries context (`session_id`, `platform`, `ip`, `reason`, …); `At` = clock time.
- Unknown identifiers: only `identifier_kind` and `subject_ref` (first 8 bytes of the throttle HMAC, hex) are stored, so attempts correlate without storing what was typed.
- **No-secrets scan test (SC-006)**: `authtest.Secrets` records every password, TOTP code, recovery code, TOTP secret (base32 and hex), access, refresh and challenge token, and unmatched identifier used in the acceptance suite. At the end it scans every `audit_log` row cast to text, the captured slog output (JSON handler into a buffer through `httpx.NewServer`), and every response body except the one-time displays (enrolment secret, recovery codes at issue, tokens in their own issuing response). Any hit fails.

### P-12 Organisation resolution

- Users are globally unique by email and mobile (D-18), so lookup needs no org; the org is still needed for audit and throttle rows when nothing matches. `store.DefaultOrg(ctx)` returns the single `orgs` row (cached after the first success); none → 503 `auth.not_configured`; more than one → error until multi-org sign-in has its own feature.
- `swastik admin create` creates the org when none exists (`--org-name`, default "Swastik"), reuses it when exactly one exists, and requires `--org-id` otherwise.

### P-13 Bootstrap command `swastik admin create` (D-15, US-9)

- Flags `--email`, `--name` (required), `--mobile`, `--org-name`, `--org-id`. Password: TTY → prompted twice with `golang.org/x/term.ReadPassword` (no echo); otherwise exactly one line from stdin. Never a flag or environment variable.
- Config: `config.LoadDB()` (only `DATABASE_URL` and optional `APP_ENV`), so bootstrapping needs no JWT or PII keys.
- Policy check and hashing first; then one transaction: `pg_advisory_xact_lock(<const>)` → resolve or create org → refuse if an admin exists (`bootstrap.admin_exists`) → refuse taken email/mobile → insert admin (`active`, `email_verified_at=now`, no `admin_mfa` row) → audit `auth.admin.bootstrapped` (actor `system`). Two concurrent runs: exactly one succeeds.
- Output: success prints id and normalised email; refusals print a stable code and English message to stderr, exit 1; usage errors exit 2. No `--verbose`. The owner runs it against staging/production from their own machine; agents never do. Contract: [contracts/cli.md](./contracts/cli.md).

### P-14 Configuration and secrets (local vs Secret Manager)

| Env var | Format | local / ci | staging / production |
|---|---|---|---|
| `JWT_SIGNING_KEYS` | `kid:base64(32-byte Ed25519 seed)[,…]` | Ephemeral key generated at startup if empty (WARN logged, no values) | **Required**; Secret Manager `jwt-signing-keys` |
| `JWT_ACTIVE_KID` | a kid in the ring | ephemeral kid | Required; repository variable (not secret) |
| `PII_ENCRYPTION_KEYS` | `id:base64(32 bytes)[,…]`, id 1–65535 | Ephemeral if empty (a TOTP enrolled locally is unreadable after restart; documented) | **Required**; Secret Manager `pii-encryption-keys` |
| `PII_ACTIVE_KEY_ID` | an id in the ring | ephemeral id | Required; repository variable |
| `BLIND_INDEX_KEY` | base64(32 bytes) | Ephemeral if empty | **Required**; Secret Manager `blind-index-key`; not rotated casually (R-8) |
| `WEB_ORIGINS` | comma-separated exact origins | `http://localhost:5173` | Required; repository variable (027 supplies the admin origin) |
| `TRUSTED_PROXY_HOPS` | int ≥ 0 | `0` (RemoteAddr) | Set after 027 confirms Cloud Run's `X-Forwarded-For` layout (R-7) |
| `ARGON2_MAX_CONCURRENCY` | int 1–16 | 4 | 4 (re-tuned after the staging benchmark) |

- Code lives in a new `config/auth.go` (C-11). `config.Config` implements `slog.LogValuer` and redacts every key field; key material is decoded once into typed rings (`crypto.Keyring`, `crypto.BlindIndexer`, `auth.SigningKeys`) and not kept as strings.
- Validation rejects wrong key lengths, an active id missing from its ring, duplicate ids, `BLIND_INDEX_KEY` equal to any PII key, and missing keys when `APP_ENV` is `staging` or `production` (fail closed: the service refuses to start).
- `.github/workflows/deploy.yml` gains `--set-secrets JWT_SIGNING_KEYS=jwt-signing-keys:latest,PII_ENCRYPTION_KEYS=pii-encryption-keys:latest,BLIND_INDEX_KEY=blind-index-key:latest` and the three repository variables, next to the existing `DATABASE_URL` secret. **Owner action before merge**: create the three secrets and variables (agents hold no production credentials). 027 later moves this into OpenTofu.
- `.env.example` must list these names; agents may not read or edit `.env*` (CLAUDE.md), so that update is an owner action.
- Rotation procedures per key class: [quickstart.md](./quickstart.md#key-rotation).

### P-15 Transactions and the shared unit of work

- 002's `db.TxRunner` (C-3). `auth.Service` takes it in `Deps`; every write path is `txr.InTx(ctx, func(ctx) error { … })`, and queries use `dbgen.New(txr.Conn(ctx))`. A nested call joins, so `Accounts.CreateClient` participates in 005's registration transaction, and the housekeeping handler runs inside the job transaction 002 provides (`db.MustTx`).
- Argon2 and other slow work happen before `InTx` is entered.

### P-16 Idempotency

- Credential operations **do not accept** `Idempotency-Key`: sign-in, mfa-verify, enrolment start/confirm, refresh, password change, recovery-code regeneration, authenticator replace start/confirm. Replaying a stored response would hand freshly issued tokens or codes to whoever holds the key, which is the grace window the spec forbids. Each already has defined replay semantics (single-use credentials; P-06 safe retry).
- Session-ending operations (sign-out, sign-out-everywhere, device revoke, admin revoke and end-all) are **naturally idempotent**: a repeat returns 204 (or 404 for an already-ended device) with the same end state.
- The repository's idempotency pattern arrives with 002 Phase B (per-operation `Idempotency-Key`, replay via an audit lookup). 003's operations are marked `Metadata["idempotency"]="exempt"` so a later contract check can require the header on every other mutation. This is Deviation 1.

### P-17 Client IP and the coarse per-IP limiter

- `httpx.ClientIP(r, hops)`: `hops=0` → `RemoteAddr`; otherwise the `X-Forwarded-For` entry at `len-hops` (the first value is never trusted). Stored only in audit `after.ip`.
- `internal/platform/ratelimit`: in-memory, per-instance fixed window keyed by IP, standard library only, size-capped eviction. Applied to sign-in, refresh, mfa-verify and enrolment: 30 requests/minute/IP → 429 `rate_limited`. Defence in depth against Argon2 CPU exhaustion; account-independent, so it reveals nothing.

### P-18 Migration numbering (C-1)

- 002 owns version `00002` (`00002_scheduler_outbox.sql`); 003 adds `00003_auth.sql`. 003 merges after 002, so goose sees increasing versions on every database. No renumbering and no extra CI script.
- Suggested rule for `.claude/rules/sql.md` (owner action): "a feature's migration uses the next free version on `main` at the time its plan is approved; features planned in parallel agree numbers in their plans."

### P-19 depguard and lint (`apps/api/.golangci.yml`, on top of 002's version)

```yaml
depguard:
  rules:
    platform-is-a-leaf:               # 002 lists health, scheduler, outbox; 003 appends:
      files: ["**/internal/platform/**"]
      deny:
        - pkg: "github.com/NandanMaheta/swastik/apps/api/internal/auth"
          desc: platform packages must not import business modules
    jose-only-in-auth-and-tickauth:
      files: ["$all", "!**/internal/auth/**", "!**/internal/platform/tickauth/**"]
      deny:
        - pkg: "github.com/go-jose/go-jose/v4"
          desc: access tokens are issued and verified only in internal/auth (tickauth uses it via go-oidc and in tests)
    password-and-otp-primitives-in-auth:
      files: ["$all", "!**/internal/auth/**"]
      deny:
        - pkg: "golang.org/x/crypto/argon2"
          desc: password hashing lives only in internal/auth
        - pkg: "github.com/pquerna/otp"
          desc: TOTP lives only in internal/auth
# forbidigo time rules: provided by 002 (time.Now/Since/Until, time.Local); no change.
```

- Every later module appends its path to `platform-is-a-leaf` in the PR that creates it (planner adds that line per module).
- The explicit list is used because depguard's longest-match semantics for a prefix deny with a platform allow were not verified.
- Verification: add a forbidden import temporarily (for example `internal/auth` from `platform/authz`), run `golangci-lint run ./...`, see it fail ([quickstart.md](./quickstart.md)).

### P-20 Scheduler jobs and outbox events

- **Job kind `auth.housekeeping`** (recurring, per org), registered in `cmd/swastik/main.go` with 002's registry:
  `sch.Register(auth.HousekeepingKind, func(ctx context.Context, job scheduler.Job) error { _, err := authSvc.Housekeep(ctx, job.OrgID); return err }, scheduler.KindOptions{Recurring: auth.HousekeepingNext})`
  where `auth.HousekeepingNext(now) = istdate.NextDailyAt(now, istdate.WallClock{Hour: 3, Minute: 30})` (after 002's `scheduler.retention` at 03:00 IST). `internal/auth` does not import `internal/scheduler`; the adapter lives in `cmd`.
- `Housekeep(ctx, orgID)` runs in the job's transaction (`db.MustTx`), with `@now` from the clock, at most 1,000 rows per table per run:
  - `auth_throttles` rows whose last failure is outside the window and whose lock has ended: `updated_at <= @now - 15m AND (locked_until IS NULL OR locked_until <= @now)` (lockout-counter cleanup);
  - `mfa_challenges` with `expires_at <= @now`;
  - `refresh_tokens` of sessions that ended, or passed absolute expiry, more than 30 days ago (bounds the ~100k rows/year growth). Outcome is unchanged: such a token is rejected as `auth.refresh_rejected` before and after deletion, with no audit either way.
  - One audit entry `auth.housekeeping.purged` (actor `system`, entity `auth`, entity_id NULL, counts in `after`) when any count is non-zero.
- Idempotent and behaviour-neutral: a rerun deletes nothing more; tests run the same lockout and refresh scenarios with and without the job and compare outcomes.
- **Outbox events: none.** No consumer needs auth events yet (004 and 005 call capabilities directly; no spec asks for "new sign-in" notifications). A later feature that wants them adds topics through 002's `outbox.Add` in the same transactions.

## Data model changes

Full design in [data-model.md](./data-model.md); migration `apps/api/migrations/00003_auth.sql` (expand-only; owner approval and security review required).

| Table | Purpose | Key constraints |
|---|---|---|
| `users` | User, role, status machine, contacts (readable, ADR 0007), PHC hash | `org_id` FK; CHECK role/status/`status_before_disabled`; email lowercase CHECK; mobile `^\+91[6-9][0-9]{9}$`; clients require mobile; `UNIQUE(email)`, partial `UNIQUE(mobile)` |
| `device_sessions` | Device session = refresh family; lifetimes; end reason | partial `UNIQUE(user_id, device_id) WHERE ended_at IS NULL`; CHECK platform/client_type/end_reason; `(ended_at IS NULL) = (end_reason IS NULL)` |
| `refresh_tokens` | Single-use refresh credentials (SHA-256) | `UNIQUE(token_hash)`; partial `UNIQUE(session_id) WHERE used_at IS NULL`; `octet_length = 32` |
| `admin_mfa` | Encrypted TOTP secret, pending secret, `last_used_step` | PK `user_id`; envelope CHECKs |
| `mfa_recovery_codes` | Hashed single-use recovery codes in batches | `UNIQUE(user_id, code_hash)`; usable partial index |
| `mfa_challenges` | Interim proof, 5 min, single use | `UNIQUE(token_hash)`; CHECK purpose |
| `auth_throttles` | Sliding-window lockout per HMAC subject | PK `(org_id, scope, subject)`; `cardinality(failures) <= 5`; no FK to users by design |

Every table has `org_id uuid NOT NULL REFERENCES orgs(id)`, `uuidv7()` keys where it has its own id, `timestamptz` columns written from `@now`. **No new append-only table**: these are mutable security state; their history is the append-only `audit_log` (existing `forbid_mutation()` trigger). Queries in `apps/api/queries/auth.sql`; generated with 002's uuid override; `sqlc generate && sqlc vet`.

## API operations (OpenAPI diff)

Full schemas: [contracts/openapi-auth.yaml](./contracts/openapi-auth.yaml). Expected generated diff, **additive only** (oasdiff non-breaking): 16 operations under `/v1/auth/*` and `/v1/admin/users/*`; `components.securitySchemes.{bearer, mfaChallenge}`; `ErrorModel.code`; `security: []` on `health-live` and `health-ready`. The hidden `/internal/tick` (002) stays out of the document.

| operationId | Method and path | Security | Request | Success | Errors (`code`) |
|---|---|---|---|---|---|
| auth-sign-in | POST /v1/auth/sign-in | Public | `SignInRequest{identifier, password, client_type, device{id,name,platform}}`; Origin + `X-Swastik-CSRF` when web | 200 `SignInResponse{next_step, session?, challenge?}` + Set-Cookie (web) | 401 invalid_credentials · 422 identifier_invalid / validation_failed · 403 csrf_rejected · 429 rate_limited · 503 busy / not_configured |
| auth-mfa-verify | POST /v1/auth/mfa/verify | mfaChallenge | `MfaVerifyRequest{code}` xor `{recovery_code}`; web headers | 200 `SessionTokens` + Set-Cookie (web) | 401 unauthenticated (challenge) / invalid_credentials (code, locked) · 403 csrf_rejected · 422 · 429 |
| auth-mfa-enrolment-start | POST /v1/auth/mfa/enrolment | mfaChallenge (enrol) | – | 200 `TotpSecret{secret, otpauth_uri}` | 401 · 429 |
| auth-mfa-enrolment-confirm | POST /v1/auth/mfa/enrolment/confirm | mfaChallenge (enrol) | `TotpCodeRequest{code}`; web headers | 200 `EnrolmentResult{session, recovery_codes[10]}` + Set-Cookie (web) | 401 unauthenticated / invalid_credentials · 403 csrf_rejected · 409 mfa_no_pending_secret · 422 |
| auth-refresh | POST /v1/auth/refresh | Public | `RefreshRequest{device_id, refresh_token?}` + cookie `__Secure-swastik_rt`; Origin + `X-Swastik-CSRF` on the cookie path | 200 `SessionTokens` (+ Set-Cookie on the cookie path) | 401 refresh_rejected · 403 csrf_rejected · 422 · 429 |
| auth-sign-out | POST /v1/auth/sign-out | bearer (any) | – | 204 (+ clearing Set-Cookie) | 401 |
| auth-sign-out-everywhere | POST /v1/auth/sign-out-everywhere | bearer (any) | – | 204 (+ clearing Set-Cookie) | 401 |
| auth-devices-list | GET /v1/auth/devices | bearer (any) | – | 200 `DeviceList{items[]}` | 401 |
| auth-device-revoke | DELETE /v1/auth/devices/{sessionId} | bearer (any) | path uuid | 204 | 401 · 404 device_not_found |
| auth-password-change | POST /v1/auth/password/change | bearer (any) | `ChangePasswordRequest{current_password, new_password}` | 204 | 401 · 403 reauth_failed · 422 password_* · 503 busy |
| auth-recovery-codes-regenerate | POST /v1/auth/mfa/recovery-codes | bearer [admin] | `ReauthWithCode{password, code}` | 200 `RecoveryCodes{recovery_codes[10]}` | 401 · 403 forbidden / reauth_failed · 422 |
| auth-authenticator-replace-start | POST /v1/auth/mfa/authenticator | bearer [admin] | `ReauthWithCodeOrRecovery{password, code \| recovery_code}` | 200 `TotpSecret` | 401 · 403 forbidden / reauth_failed · 422 |
| auth-authenticator-replace-confirm | POST /v1/auth/mfa/authenticator/confirm | bearer [admin] | `TotpCodeRequest{code}` | 204 | 401 · 403 forbidden / reauth_failed · 409 mfa_no_pending_secret · 422 |
| auth-admin-user-devices-list | GET /v1/admin/users/{userId}/devices | bearer [admin] | path uuid | 200 `DeviceList` | 401 · 403 · 404 user_not_found |
| auth-admin-user-device-revoke | DELETE /v1/admin/users/{userId}/devices/{sessionId} | bearer [admin] | path uuids | 204 | 401 · 403 · 404 user_not_found / device_not_found |
| auth-admin-user-sessions-end-all | POST /v1/admin/users/{userId}/sessions/end-all | bearer [admin] | path uuid | 204 | 401 · 403 · 404 user_not_found |

Idempotency per P-16 (none accept `Idempotency-Key`; session-ending operations are naturally idempotent). The account capabilities FR-050…FR-055 are **Go interfaces only**, with no HTTP operation (US-10 s7): [contracts/go-capabilities.md](./contracts/go-capabilities.md). After the code lands: `go run ./cmd/swastik openapi > packages/api-client/openapi.yaml` and `pnpm --filter @swastik/api-client generate`.

## Project Structure

### Documentation (this feature)

```text
specs/003-auth-core/
├── spec.md              # approved spec
├── research.md          # §1–10 spec stage; §11 plan decisions (Phase 0)
├── plan.md              # this file
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1 (validation guide + key rotation)
├── contracts/
│   ├── openapi-auth.yaml    # intended OpenAPI fragment (generated doc is authoritative)
│   ├── error-codes.md       # FR-093 catalogue
│   ├── go-capabilities.md   # exported Go interfaces (authz, Accounts, Throttle, crypto, housekeeping)
│   └── cli.md               # `swastik admin create`
├── checklists/requirements.md
└── tasks.md             # produced by the planner (/speckit-tasks), not by this plan
```

### Source Code

```text
apps/api/
├── cmd/swastik/
│   ├── main.go                 # buildAPI(deps) nil-safe; httpx.InstallErrors; authz.Install before Register; AssertDeclared;
│   │                           # auth.Register; sch.Register("auth.housekeeping", …); `admin create` subcommand
│   ├── admin.go                # bootstrap CLI (flags, TTY prompt, exit codes)
│   ├── contract_test.go        # static + runtime security walk (FR-034, SC-001)
│   ├── wiring_test.go          # tick declared SelfAuthenticated still 401s; housekeeping kind registered
│   ├── openapi_test.go         # `swastik openapi` builds with zero deps
│   ├── admin_test.go           # CLI integration tests
│   └── testdata/admin-operations.txt
├── internal/auth/
│   ├── service.go              # Service, Deps{Store, Now clock.Func, Tx db.TxRunner, Keys, Hasher, BlindIndexer, Keyring}
│   ├── store.go  store_pg.go   # small Store interface; sqlc adapter (only file with pgx types)
│   ├── normalize.go            # email / mobile / identifier kind
│   ├── password.go             # policy, NFC, Argon2id, PHC, semaphore, dummy verify
│   ├── commonpasswords/        # 10k.txt + SOURCE.md + embed.go
│   ├── throttle.go             # pure Apply + Throttle implementation
│   ├── tokens.go               # go-jose EdDSA issue/verify, SigningKeys ring
│   ├── signin.go  refresh.go  sessions.go  mfa.go  totp.go  recovery.go
│   ├── accounts.go             # FR-050…055 capabilities (exported Accounts)
│   ├── bootstrap.go            # FR-080 service part
│   ├── housekeeping.go         # Housekeep, HousekeepingKind, HousekeepingNext (P-20)
│   ├── verifiers.go            # authz.Verifier impls: bearer, mfaChallenge
│   ├── handler.go  handler_admin.go  cookies.go
│   ├── errors.go  audit.go     # domain errors → problems; audit action catalogue + typed payloads
│   ├── authtest/               # fixtures (users per status, key rings), Secrets registry
│   └── *_test.go
├── internal/platform/
│   ├── authz/{authz.go, authz_test.go}                         # new
│   ├── crypto/{aead.go, blindindex.go, token.go, *_test.go}    # new
│   ├── ratelimit/{ratelimit.go, ratelimit_test.go}             # new
│   ├── audit/audit.go (+At, Nil→NULL), audit_test.go (+)       # 002's package, additive (C-4)
│   ├── httpx/{errors.go, cors.go, clientip.go, *_test.go}      # new files in 002-extended package
│   └── config/{auth.go, auth_test.go}                          # new files
├── internal/scheduler/handler.go       # 002's file: one line, Security: authz.SelfAuthenticated() (C-8)
├── internal/health/handler.go          # Security: authz.Public() on both operations
├── migrations/00003_auth.sql
├── queries/auth.sql  queries/platform.sql (InsertAuditLog created_at param, C-4)
├── internal/platform/db/dbgen/*        # regenerated
└── .golangci.yml                       # P-19
.github/workflows/deploy.yml            # auth secrets + variables (P-14)
packages/api-client/{openapi.yaml, src/schema.ts}   # regenerated
packages/domain/src/auth-error-codes.ts             # code list for UI translation (FR-093)
docs/adr/0007-contact-data-at-rest.md               # new (this plan)
docs/adr/0004-auth.md                               # Amendment 1 (this plan)
```

**Structure decision**: one business module (`internal/auth`), three new platform packages (`authz`, `crypto`, `ratelimit`), additive changes to 002's `audit` and to `httpx` and `config`, and a one-line declaration on 002's tick operation. This follows the go-module-pattern skill. No TypeScript app code changes except the regenerated client and the code list in `packages/domain`.

## Test strategy

All tests are written first by `test-engineer`; implementers never edit them. Time is 002's `clock.Fake` (`Set`, `Advance`); real waits are forbidden. "Int" = integration on `dbtest.New(t)`, skipped by `-short`. Rule IDs: BR-CLI-04 is the only business rule in scope (traceability script requires `BR_CLI_04` in test names); GS-01…04 are unaffected and must keep passing.

| Layer | What | Where | Covers |
|---|---|---|---|
| Static | golangci-lint (depguard P-19, forbidigo from 002, gosec), `sqlc vet`, generated code current, OpenAPI diff + oasdiff, BR traceability script | CI | Constitution IV, V, I |
| Unit | Email and mobile normalisation tables (all D-17 forms, rejections) | `normalize_test.go` | FR-003, D-16, D-17, US-1 s1–2, s11 |
| Unit | Password policy rule order and codes; 64-char Gujarati/Hindi passphrase accepted; 257 rejected; NFC equivalence; common list pinned (count + SHA-256); `1234567890`, `QWERTYUIOP` rejected | `password_test.go` | FR-011, D-07, US-7 s3–6 |
| Unit | PHC encode/decode, bounds on decoded parameters, rehash detection, dummy verify always runs once, semaphore saturation returns `ErrBusy` | `password_test.go` | FR-010, FR-012, P-06 |
| Property | PHC round-trip; normalisation idempotent; mobile normaliser accepts exactly the D-17 grammar | `*_prop_test.go` (rapid) | D-16, D-17 |
| Unit + property (fake clock) | `throttle.Apply` vs reference model; `TestLockout_LocksAt5thFailureUntilPlus15m` (T..T+4m → locked until T+19m), `TestLockout_LockedAt18m59s_UnlockedAt19m`, `TestLockout_NoExtensionDuringLock`, `TestLockout_SlidingWindowNotLocked` (US-4 s5), end of lock clears | `throttle_test.go` | D-08, US-4 s1–5, SC-004 |
| Unit (fake clock) | TOTP: RFC 6238 Appendix B SHA-1 vectors; N−1/N/N+1 accepted, N±2 rejected; `step <= last_used_step` rejected; matched step returned | `totp_test.go` | FR-061, US-5 s4–5 |
| Unit (fake clock) | JWT: valid; `TestAccessToken_ExpiredExactlyAtExp`; missing `exp`/`iat`; `alg:none`; HS256 with the public key as secret; RS256; wrong/unknown kid; `jwk`/`jku` header ignored; wrong `aud`/`iss`/`typ`; one-character tamper; refresh or challenge token as JWT; `Expected.Time` never zero | `tokens_test.go` | US-2 s5–6, s10, FR-035, P-02 |
| Unit + property | `crypto`: Seal/Open round trip; any byte tamper fails; wrong AAD fails; old key id opens after rotation; BlindIndex deterministic and purpose-separated; NewToken prefix/entropy/digest | `platform/crypto/*_test.go` | FR-064, FR-092, P-14 |
| Unit | Sign-in decision table (status × password × lock × role × identifier kind) with fakes: `TestSignIn_BR_CLI_04_PendingVerificationGenericFailureNotCounted`, `TestSignIn_BR_CLI_04_PendingApprovalGenericFailure`, `TestSignIn_BR_CLI_04_DisabledGenericFailure`, `TestSignIn_BR_CLI_04_ActiveSucceeds`, admin by mobile = generic failure, client never gets MFA | `signin_test.go` | **BR-CLI-04**, D-09, FR-020…023, FR-065, US-1 s4–10 |
| Unit | Accounts status machine: `TestAccounts_BR_CLI_04_ActiveOnlyAfterBothVerifiedAndApproved`, `TestAccounts_BR_CLI_04_VerificationOrderIndependent`, `TestAccounts_BR_CLI_04_ApproveRefusedFromPendingVerification`, `TestAccounts_BR_CLI_04_EnableRestoresPendingApproval`; idempotent marks; last admin | `accounts_test.go` | **BR-CLI-04**, FR-050…055, US-8 s5–6, US-10 |
| Unit | authz middleware: nil Security → 500 (documented and hidden ops); unknown scheme panics at startup; `SelfAuthenticated` on a documented op panics in `AssertDeclared` and is refused at runtime; role scopes; principal in context; handler never invoked on failure | `platform/authz/authz_test.go` | FR-031, FR-032, C-8 |
| Unit | httpx errors: `code` present, `Value` stripped (malformed JSON containing a password not echoed), `type` URN; CORS exact match and `Vary`; ClientIP hops; ratelimit window | `platform/httpx/*_test.go`, `platform/ratelimit/*_test.go` | FR-091, FR-093, P-04, P-17 |
| Int | Store: unique email/mobile under concurrent creates (exactly one wins); partial unique indexes; CHECK constraints reject bad states | `store_test.go` | FR-004, D-18 |
| Int | audit extension: `At` stored as `created_at`; `uuid.Nil` → NULL; write without tx → `db.ErrNoTx`; rollback leaves no row; UPDATE/DELETE refused | `platform/audit/audit_test.go` (+) | FR-090, US-11 s3, C-4 |
| Int (fake clock) | Refresh: R1→R2; reuse ends the family (R2 and access dead); device mismatch; transport mismatch; `TestRefresh_ClientIdle30d_Boundary`, `TestRefresh_ClientAbsolute90d_Boundary`, `TestRefresh_AdminIdle12h_Boundary`, `TestRefresh_AdminAbsolute7d_Boundary` (±1 minute); `TestRefresh_BR_CLI_04_DisabledUserRejected`; locked account can refresh | `refresh_test.go` | US-3 s1–2, s4–10, FR-040…043, SC-004 |
| Int (concurrency) | Barrier + N goroutines: same R1 → exactly 1 success, family ended, one reuse audit; parallel TOTP of one code; parallel bootstrap; parallel disable of the last two admins; parallel sign-in from one device | `concurrency_test.go` | D-10, US-3 s3, FR-061, US-9 edge, D-19, D-11 |
| Int (fake clock) | Sessions: sign-out, everywhere, list (current flag; ended/expired excluded), revoke own/other (404), admin list/revoke/end-all; `TestAuthz_BR_CLI_04_DisabledUserNextRequestRejected`; next request rejected at T+1s | `sessions_test.go` | US-2 s7–8, US-6, US-8, D-12, SC-002 |
| Int (fake clock) | Lockout through HTTP; unknown identifier ×6 identical to locked account; locked account's other session keeps working; reset clears; admin wrong codes lock | `lockout_test.go` | US-4 s6–9, US-3 s10 |
| Int (fake clock) | Admin MFA: enrolment, verify, recovery codes (single use, regenerate invalidates), replace (other sessions end), `TestMfaChallenge_ExpiredAt5m`, single use, wrong purpose, enrolment cannot be skipped, TOTP required again after sign-out | `mfa_test.go` | US-5 s1–13, FR-060…065, US-2 s9 |
| Int | Password change (others end, current kept, safe retry, locked → reauth_failed); reset capability (all end, lock cleared) | `password_flow_test.go` | US-7, US-10 s5, D-13 |
| Int | Audit: exactly one entry per US-11 event with actor kind, actor, user, session, action and clock time | `audit_events_test.go` | US-11 s1, FR-090 |
| Int (fake clock, 002 harness) | `auth.housekeeping`: registered as recurring; advancing to 03:30 IST and ticking purges stale throttles, expired challenges and old refresh tokens only; summary audit row; rerun purges nothing; lockout/refresh outcomes identical with and without the job | `housekeeping_test.go`, `cmd/swastik/wiring_test.go` | P-20, FR-045, C-7 |
| Int (security) | No-secrets scan over audit rows, logs and responses for the whole suite | `secretscan_test.go` | FR-091, SC-006, US-11 s2 |
| Int (security) | CSRF: cookie refresh and web sign-in without Origin, with `null`, a foreign or sibling origin, without `X-Swastik-CSRF`, or with a form content type → 403 `csrf_rejected`; cookie attributes exact; body never carries `refresh_token` for web | `csrf_test.go` | P-04 |
| Contract | Static + runtime security walk; `swastik openapi` builds with zero deps; `code` on every non-2xx in the walk; no FR-050…055 operation; `/internal/tick` absent and still 401 without credentials | `cmd/swastik/contract_test.go`, `openapi_test.go`, `wiring_test.go` | FR-030, FR-034, SC-001, US-2 s1–4, US-10 s7, C-8 |
| CLI (Int) | `swastik admin create`: success, admin exists, policy failure names the rule, email taken, two concurrent runs, password absent from stdout/stderr/logs/audit (stdin pipe mode) | `cmd/swastik/admin_test.go` | US-9, FR-080 |
| Timing (nightly) | SC-003: 50 attempts × 6 failure cases, medians within 10%, identical status and body bytes; `nightly.yml -run Timing` (CI runners are noisy for PRs) | `timing_test.go` | D-09, SC-003, US-1 s5, US-4 s6 |
| Benchmark | Argon2id cost on the CI runner; staging measurement reported in the PR | `password_bench_test.go` | SC-007, R-6 |
| Component / E2E | None in 003 (no UI). 022 (Playwright) and 024 (Maestro) test the flows against these operations, including the cookie flow on the shared domain. | – | – |
| Golden | GS-01…04 unaffected; run on every commit | existing suite | Constitution I |

## Performance notes

- **Authenticated request**: Ed25519 verify (~50 µs) plus one primary-key join (~1–3 ms in-region). No writes on reads (`last_active_at` changes only on sign-in and refresh).
- **Sign-in**: one Argon2id (budget 40–70 ms; benchmark warns above 150 ms), three reads, one transaction of at most seven statements; p95 well under 200 ms at this scale.
- **Change password**: two Argon2 runs (three on the safe-retry path); under 200 ms p95 expected; benchmark checks it.
- **Refresh**: one transaction (three locked reads, three writes, one audit insert), no Argon2.
- **Pool and memory**: `MaxConns=5`; Argon2 outside transactions keeps connections held for milliseconds; Argon2 memory ≤ 4 × 19 MiB; 512 MiB Cloud Run memory is enough (027).
- **Data growth**: ~100k `refresh_tokens` and ~100k audit rows a year; the housekeeping job bounds `refresh_tokens`, `auth_throttles` and `mfa_challenges`. Housekeeping adds three bounded deletes once a day per org to a tick.

## Security notes

- **Threats covered**: credential stuffing (lockout, per-IP limiter, Argon2 cap); account enumeration (generic failure, dummy verify, identical bodies, no `Retry-After`); stolen refresh token (rotation, strict reuse detection, device binding); stolen access token (15-minute lifetime, revocation on the next request); XSS on the admin panel (refresh in an HttpOnly cookie, access in memory); CSRF (P-04); JWT algorithm confusion (mandatory `EdDSA` allowlist at parse, keys only by kid from config, `jku`/`jwk` ignored, explicit `typ`); TOTP replay (`last_used_step`); TOTP brute force (shared lockout, counter not cleared at the password step); database read (Argon2id passwords, SHA-256 tokens and recovery codes, AES-GCM TOTP secret with the key outside the database); log and response leakage (FR-091 scan, Huma `Value` stripped, typed audit payloads, `slog.LogValuer` redaction); bootstrap race (advisory lock); tampered PHC parameters (decode bounds).
- **Accepted residual risks**: a lost refresh response signs the device out (D-10; 022/024 mitigate); locking out new sign-ins by knowing only an identifier (D-08; existing sessions unaffected); contact data readable at rest (ADR 0007); iPhone PWA storage weakness (decided in 024).
- **Out of scope**: Cloudflare Access configuration and break-glass admin recovery (027, D-22); proof-of-possession / DPoP binding (later).
- **Reviews**: `security-reviewer` reviews the migration, `internal/auth`, `platform/authz`, `platform/crypto` and the `audit` extension; the owner approves the PR (constitution workflow 5).

## Owner actions (not dependencies)

1. Create Secret Manager secrets `jwt-signing-keys`, `pii-encryption-keys`, `blind-index-key` and repository variables `JWT_ACTIVE_KID`, `PII_ACTIVE_KEY_ID`, `WEB_ORIGINS` for staging and production before 003 merges (P-14).
2. Update `.env.example` with the P-14 names (agents may not touch `.env*`).
3. Optionally add to `.claude/rules/sql.md`: "Contact details (email, mobile) are readable; see ADR 0007" and the migration-numbering line from P-18; record a PATCH clarification in the constitution's version line (ADR 0007).
4. Accept ADR 0004 Amendment 1 with this plan.
5. Confirm the two readings flagged in this plan: re-authentication refused during a lock (Constitution Check I) and the US-4 s5 wording (P-07).

## Risks and coordination

| ID | Risk | Mitigation |
|---|---|---|
| R-1 | 002 slips; 003 depends on its platform packages and registry | 003's implementation tasks for clock/tx/audit/scheduler wiring are blocked on 002 Phase A merged; pure units (normalise, password, throttle, TOTP, tokens, crypto) can be built first |
| R-2 | 002's hidden tick would hit 003's fail-closed middleware | C-8: `authz.SelfAuthenticated()` on the tick, allowed only for hidden operations; wiring test proves tickauth still guards it |
| R-3 | 002's `audit.Write` lacks a timestamp and stores `uuid.Nil` literally | C-4 additive extension with tests; existing 002 calls unchanged |
| R-4 | Cookie flow needs a shared registrable domain (D-21, 027) | Local Vite proxy; previews use a proxy or memory-only mobile mode (P-04); staging/production admin sign-in blocked on 027 only |
| R-5 | Strict reuse (D-10) signs users out after lost responses or parallel tabs | 022 and 024 keep a single refresh in flight and persist before use; carried into their specs |
| R-6 | Argon2 cost on Cloud Run unverified | Benchmark + staging measurement before production; parameters and `ARGON2_MAX_CONCURRENCY` tunable without a migration (PHC + rehash on login) |
| R-7 | `X-Forwarded-For` layout on Cloud Run unverified | `TRUSTED_PROXY_HOPS` configurable; affects only audit IP and the limiter, never authorisation |
| R-8 | Blind-index key rotation is costly once 006 stores PAN hashes | 003 uses it only for ephemeral throttle subjects; 006 must store a key version with `*_hash` (note for 006) |
| R-9 | Auth operations without `Idempotency-Key` deviate from constitution V's letter | Deviation 1; owner confirms with this plan |
| R-10 | Owner rejects go-oidc in 002 (go-jose would leave the graph) | go-jose/v4 has zero dependencies and could be required directly; or switch to golang-jwt/v5. Only `tokens.go` and its tests change |
| R-11 | Owner declines pquerna/otp | Stdlib RFC 6238 fallback (P-05); only `totp.go` changes; tests identical |
| R-12 | Merge conflicts in shared files (C-11) | New code in new files; 003 rebases after 002 |

## Complexity Tracking

| Violation | Why needed | Simpler alternative rejected because |
|---|---|---|
| **Deviation 1**: credential-issuing auth operations do not accept `Idempotency-Key` (constitution V "Mutations accept idempotency keys") | Replaying a stored response would re-deliver access tokens, refresh tokens or recovery codes to whoever holds the key: the grace window the spec forbids (D-10, spec note "Idempotency"). These operations define replay safely (single-use credentials, P-06 safe retry); session-ending operations are naturally idempotent. | Accepting the key and replaying responses weakens D-10 and turns the idempotency store into a token store; accepting and ignoring it is misleading. |
| **Deviation 2**: in `local` and `ci`, missing key variables produce ephemeral random keys instead of failing | Lets `make api`, tests and previews run without anyone handling secrets; no key ever enters the repository or `.env` templates. Staging and production refuse to start without real keys. | Committing development keys violates "no secret ever enters the repository"; requiring every developer and agent to generate keys is impossible for agents (no `.env*` access). |

## Dependencies requiring owner approval

Nothing below is added to `go.mod` by this plan; implementation tasks that need an item are blocked until the owner approves it. Versions checked on proxy.golang.org on 2026-11-13.

| Module | Kind | Used for | Why | Alternatives considered |
|---|---|---|---|---|
| **JWT library choice**: `github.com/go-jose/go-jose/v4` (v4.1.9; go-oidc requires ≥ v4.1.4) — **recommended** | Indirect (via 002's owner-approved go-oidc) promoted to **direct**; **no new module**; go-jose itself has zero requirements | Signing and verifying EdDSA access JWTs with `kid` (P-02) | Already compiled into the binary for 002's tick verification, so one JOSE stack serves both features and receives security fixes once. The v4 API forces an algorithm allowlist at parse time (algorithm-confusion defence by construction), supports Ed25519 and `kid`, and publishes a threat model. **Costs, handled in `tokens.go` and pinned by tests**: lower-level validation; `exp` is accepted at exactly `exp` (we add `now < exp`), `exp`/`iat` presence is not required (we check), `typ` is not checked (we check), and a zero `Expected.Time` silently means `time.Now()` (we always pass the clock). Larger API surface (JWE, JWK) that we do not use. | `github.com/golang-jwt/jwt/v5` v5.3.1: friendlier validator (`WithExpirationRequired`, `WithIssuedAt`, `WithTimeFunc`, exp rejected at exactly `exp`), zero dependencies, but a **new module** and a second JOSE stack in the binary; good fallback if go-oidc is rejected (R-10). `lestrrat-go/jwx/v3` (large). Hand-rolled JWS on the standard library (owning parsing edge cases is riskier). PASETO (contradicts ADR 0004). |
| `github.com/pquerna/otp` v1.5.0 (+ `github.com/boombuler/barcode`, zero further requirements) — **recommended** | **New** runtime (two modules) | TOTP secret and `otpauth://` URI generation (`totp.Generate`), step-aware code checks (`hotp.ValidateCustom` per candidate step) (P-05) | Widely used, interoperable with Google Authenticator and others, constant-time comparison, stable for a frozen standard (RFC 6238); avoids owning HOTP truncation, base32 and URI-escaping code in a security-critical module whose every PR needs owner review. Replay protection stays ours (`last_used_step`), using per-step validation. Matches research §5. | Standard library RFC 6238 (~40 lines, RFC vectors in tests; zero dependencies) is a fully specified fallback if the owner prefers no new module (R-11). Downsides of the library: last release Dec 2024; its root package imports `boombuler/barcode` for `Key.Image()`, which we never call but which is compiled in (a small binary-size cost). |
| `golang.org/x/crypto` v0.58.0 (already in go.sum) | Indirect → **direct** | `argon2.IDKey` (P-06) | Argon2id is mandated by constitution VI and is not in the standard library; maintained by the Go team. Stay on v0.58.0 (v0.59.0 would pull `x/net` v0.62.0 and `x/term` v0.48.0 into the graph); bump in a routine dependency update. | `alexedwards/argon2id` (thin wrapper, no release since 2023, adds a module) |
| `golang.org/x/term` v0.47.0 (already in go.sum) | Indirect → **direct** | `ReadPassword` without echo in `swastik admin create` (P-13) | D-15 requires no echo; the standard library cannot disable terminal echo portably; linked only into the CLI path. | Echoing stdin (violates D-15 on a TTY); shelling out to `stty -echo` (not portable) |
| `golang.org/x/text` v0.44.0 (already in go.sum) | Indirect → **direct** | `unicode/norm` NFC for passwords (P-06) | D-07 accepts Gujarati and Hindi passphrases; without normalisation the same passphrase from a different input method can produce different bytes and fail to sign in (NIST SP 800-63B recommends normalisation). | No normalisation (real lockout risk); hand-rolled normalisation (not feasible) |
| `github.com/google/uuid` v1.6.0 | Indirect → direct, **approved via 002** | Typed ids (C-6) | Listed for completeness; 003 adds nothing. | – |
| `pgregory.net/rapid` v1.2.0 | **Test-only** (already in go.sum; 002 also uses it) | Property tests | Named by `.claude/rules/tests.md`; not linked into the binary. | – |

## Owner approval of dependencies

- **Approved 2026-11-14 by the owner**: github.com/go-jose/go-jose/v4 (direct), github.com/pquerna/otp v1.5.0 (+ boombuler/barcode), golang.org/x/crypto, golang.org/x/term, golang.org/x/text (direct).
