# Implementation Plan: Auth core (users, passwords, sessions, refresh rotation, device binding, admin TOTP)

**Branch**: `feat/003-auth-core` | **Date**: 2026-11-13 | **Spec**: [spec.md](./spec.md) (owner-approved 2026-11-13, Option A, D-01…D-22)

**Input**: Feature specification from `specs/003-auth-core/spec.md`, research in [research.md](./research.md) (sections 1–10 from the spec stage, section 11 added by this plan).

**Plan artifacts**: [data-model.md](./data-model.md) · [contracts/openapi-auth.yaml](./contracts/openapi-auth.yaml) · [contracts/error-codes.md](./contracts/error-codes.md) · [contracts/go-capabilities.md](./contracts/go-capabilities.md) · [contracts/cli.md](./contracts/cli.md) · [quickstart.md](./quickstart.md) · ADR [0007](../../docs/adr/0007-contact-data-at-rest.md)

**Process note**: `.specify/scripts/bash/setup-plan.sh --json` ran successfully in the worktree and copied the template. `.specify/extensions.yml` has no `before_plan` / `after_plan` hooks. `research.md` already existed from the spec stage; Phase 0 appends section 11 and leaves sections 1–10 unchanged.

## Summary

003 adds sign-in, sessions and authorisation to the Go API. Everything later depends on it.

- A new business module `internal/auth` owns users, password hashing, the lockout counters, device sessions with rotating refresh credentials, admin TOTP with recovery codes, account capabilities for 004/005/007, and the `divya admin create` bootstrap command.
- Three new platform packages stay free of business imports: `internal/platform/authz` (one global, fail-closed Huma middleware driven by each operation's declared `Security`, plus the principal in context), `internal/platform/crypto` (AES-256-GCM envelope with a key ring, HMAC-SHA-256 blind index, random tokens; 006 reuses it for PAN), and `internal/platform/audit` (writes `audit_log` rows inside the caller's transaction). Two existing platform packages are extended: `internal/platform/db` (transaction runner that a nested call joins) and `internal/platform/httpx` (RFC 9457 problems with a stable `code`, CORS, client IP, a coarse per-IP limiter).
- **Access credential**: a JWT signed with EdDSA (Ed25519), carrying a `kid` header so keys can rotate, valid for 15 minutes. On every authenticated request the middleware also does one primary-key lookup of the device session and the user. That lookup is what makes revocation take effect on the next request (D-05).
- **Refresh credential**: 256 random bits, opaque, stored only as a SHA-256 digest. It is single use and rotated in one transaction under row locks. Reuse is detected strictly, and the device session ends on any reuse or `device_id` mismatch (D-10, D-11).
- **Transport by `client_type`**: `mobile` gets the refresh credential in the response body. `web` (the admin panel) gets it only in an `HttpOnly; Secure; SameSite=Strict` cookie scoped to the refresh path, with CSRF defences on that path. In both cases the access token stays in memory.
- **Lockout**: a sliding 15-minute window kept in PostgreSQL rows keyed by an HMAC subject, so it works the same across instances and for identifiers that have no account.
- **Passwords**: Argon2id (m=19 MiB, t=2, p=1) in a PHC string, rehashed on login when the parameters change, at most N hashes at once per instance, and a dummy verify when the account is unknown.
- **Migration**: `00003_auth.sql`. Version 00002 is left for feature 002 (see "Migration numbering rule").

## Technical Context

**Language/Version**: Go 1.27.4 (toolchain pinned in `apps/api/go.mod`).

**Primary Dependencies**: Existing ones are Huma v2.40.0, pgx v5.13.0, goose v3.30.1, sqlc v1.31.1 (CI) and testcontainers-go v0.45.0. **New runtime dependencies (owner approval required; not added to go.mod yet)**: `github.com/golang-jwt/jwt/v5` v5.3.1. **Promoted from indirect to direct (owner approval required)**: `golang.org/x/crypto` (argon2), `golang.org/x/term` (password prompt with no echo), and `golang.org/x/text` (NFC password normalisation). `github.com/pquerna/otp` is **not** recommended; TOTP is implemented on the standard library (see "Dependencies requiring owner approval"). Test-only: `pgregory.net/rapid` v1.2.0 (already in go.sum; named by `.claude/rules/tests.md`).

**Storage**: PostgreSQL 18 (Supabase in staging and production; `postgres:18-alpine` locally and in tests). Seven new tables plus one new audit-log query file; no change to `audit_log` columns. See [data-model.md](./data-model.md).

**Testing**: `go test -race ./...`, with real PostgreSQL 18 through `dbtest.New(t)` (skipped with `-short`). Handler tests use `humatest`. Property tests use `pgregory.net/rapid`. `golangci-lint` v2 with depguard. `sqlc vet`. CI diffs the OpenAPI document and runs oasdiff.

**Target Platform**: Cloud Run (asia-south1, Mumbai), Linux container. The CLI subcommand `divya admin create` runs on the owner's machine against the target database.

**Project Type**: Web service (modular-monolith Go API) plus one operator CLI subcommand.

**Performance Goals**: API p95 under 200 ms in Mumbai (constitution VII, SC-007). Expected costs:

- Each authenticated request adds one primary-key lookup and one Ed25519 verify (~1–3 ms).
- Sign-in costs one Argon2id hash (~40–70 ms on one Cloud Run vCPU; measured by a benchmark plus a staging check) and one short transaction.
- Refresh is one transaction with no hashing beyond SHA-256.

**Constraints**:

- Argon2 memory is about 19 MiB per hash in flight, capped by a semaphore (default 4, so about 76 MiB). The pgx pool has `MaxConns=5`, so Argon2 always runs **outside** any transaction.
- `divya openapi` must build the API with no database, no keys and no config.
- No Redis and no background goroutines for business work.
- Time comes only from the injected clock. SQL uses `@now` parameters, never `now()`, for anything that affects behaviour.

**Scale/Scope**: One organisation, one admin, about 100 clients, 1–3 devices each. Every list in 003 is unpaginated (at most a handful of devices per user).

## Constitution Check

*GATE: checked before Phase 0 and re-checked after Phase 1. Result: **PASS with two documented deviations** (Complexity Tracking).*

| Principle | How the plan complies | Status |
|---|---|---|
| I. Business rules are the source of truth | Every behaviour traces to the spec (D-01…D-22, FR-001…FR-093) or BR-CLI-04. BR-CLI-05 stays in 007; 003 provides only FR-055. The only interpretation added: a lock also refuses password and code re-checks on change-password, recovery-code regeneration and authenticator replacement. This follows D-08, which counts wrong current passwords and says "attempts during the lock fail". Tests carry `BR_CLI_04` where the status gate is exercised. | PASS |
| II. Money exact, history immutable | No money. Every mutation writes exactly one `audit_log` row in the same transaction through `platform/audit`, which refuses to write when no transaction is on the context. `audit_log` stays append-only (001 trigger). No new append-only tables are needed (see data-model). | PASS |
| III. Test-first, layered, deterministic | Tests are written first by `test-engineer` (strategy below). A fake clock is injected into the service, JWT validation (`jwt.WithTimeFunc`), TOTP and lockout. SQL takes `@now`. A forbidigo lint forbids `time.Now` in `internal/**` except `platform/httpx` (request timing). | PASS |
| IV. Modular monolith, hard boundaries | `internal/auth` is a business module. `platform/authz`, `platform/crypto`, `platform/audit` and `platform/db` import no business module. The depguard rule is widened to deny *every* `internal/<module>` from platform, not only `health`. Other modules use only `auth`'s exported capability interface and `authz.PrincipalFrom`. | PASS |
| V. Contract-first APIs | Every operation declares `Security`. A contract-walk test fails the build on undeclared or unlisted public operations (FR-034). Errors are RFC 9457 with a stable `code` member (FR-093). The TS client is regenerated. The OpenAPI diff is additive only. **Deviation 1**: credential-issuing auth operations do not accept `Idempotency-Key` (see Complexity Tracking). | PASS (justified) |
| VI. Security and privacy by default | Argon2id. 15-minute access tokens. Rotating, device-bound refresh credentials. Admin TOTP. AES-GCM for the TOTP secret with the key outside the database. HMAC blind-index helper ready for PAN (006). Contact data readable per **ADR 0007** (owner Option A). No secrets in logs, audit or responses (scan test; Huma's echo of the raw body is stripped). Keys come from env and Secret Manager, never the repository. **Deviation 2**: local and CI generate ephemeral keys when none are set. | PASS (justified) |
| VII. Simple, fast, accessible | One new third-party module (golang-jwt, zero transitive deps). Three `golang.org/x` promotions. TOTP, PHC encoding, mobile normalisation, CORS and the rate limiter use the standard library. p95 budget analysed below. Error codes let UIs translate into English, Gujarati and Hindi. | PASS |
| Workflow 5 / 7 | Auth schema, `internal/auth` and the migration need owner approval and a security review. New dependencies are listed for approval below and are **not** added yet. | PASS (gated) |

Post-design re-check (after data-model and contracts): no new violations. The two deviations are unchanged.

## Key decisions

Decisions numbered P-xx are referenced by data-model, contracts and tasks.

### P-01 Module layout and dependency direction

```text
cmd/divya ──> internal/auth ──> internal/platform/{authz,crypto,audit,db,db/dbgen,httpx,config}
          ──> internal/health ─> (huma only)
          ──> internal/platform/*            (wiring)
internal/platform/* ──X──> any business module (depguard)
future modules (004, 005, 007, 022…) ──> internal/auth (exported Accounts interface only)
                                    ──> internal/platform/authz (Require, PrincipalFrom)
```

- `internal/auth` has no HTTP types in service signatures and no pgx types outside `store_pg.go`. IDs cross the service boundary as canonical UUID strings. Huma validates `format:"uuid"`, and the store converts to `pgtype.UUID`. This avoids promoting `github.com/google/uuid`.
- `platform/authz` defines `Principal{UserID, OrgID, Role, SessionID string}`, the `Verifier` interface, `Install(api, map[scheme]Verifier)`, `Require(roles...)`, `Public()`, `ChallengeOnly()`, `PrincipalFrom(ctx)` and `AssertDeclared(api)`. It never parses JWTs itself. `internal/auth` supplies the verifiers.

### P-02 Access credential: JWT EdDSA plus a per-request session lookup

- **Token shape**: header `{"alg":"EdDSA","typ":"at+jwt","kid":"<kid>"}`. Claims: `iss="divya-api"`, `aud="divya-api"`, `sub=user_id`, `sid=session_id`, `org=org_id`, `role`, `iat`, `exp`, `jti` (128-bit random).
- **Lifetime**: `exp = min(now+15m, session.absolute_expires_at)`.
- **Verification** uses golang-jwt v5 with:
  - `WithValidMethods(["EdDSA"])`, `WithIssuer`, `WithAudience`, `WithExpirationRequired`, `WithIssuedAt`
  - `WithTimeFunc(clock)` and **leeway 0** (issuer and verifier are the same service on the same clock, and SC-004 needs exact boundaries)
  - a key chosen only by `kid` from the configured ring (unknown kid means reject), and a `typ == at+jwt` check.
- **Per-request lookup**: after the signature check, the middleware runs one query, `GetSessionPrincipal(sid)`, which joins `device_sessions` and `users` on the primary key. The request is rejected as `auth.unauthenticated` unless all of these hold:
  - `ended_at IS NULL`
  - `@now < idle_expires_at` and `@now < absolute_expires_at`
  - `users.status = 'active'`
  - `users.role` equals the `role` claim
  - `user_id` equals `sub`
- **Trade-off, stated explicitly**: the JWT is in practice a signed carrier for `sid`. The lookup costs about 1–3 ms per request, against D-05's requirement that revocation work on the next request. Without the lookup, sign-out, revoke and disable would lag by up to 15 minutes. ADR 0004 (JWT, 15 min) is unchanged. The JWT still gives cheap rejection of forged or expired tokens before any database access, and a standard `bearerFormat: JWT` in the contract.
- **Keys**: `JWT_SIGNING_KEYS="kid1:<base64 32-byte Ed25519 seed>,kid2:…"` and `JWT_ACTIVE_KID=kid2`. Every key in the ring can verify; only the active key signs. Rotation is in [quickstart.md](./quickstart.md#key-rotation).

### P-03 Refresh credential, rotation, reuse detection, device binding

- **Format**: `drt_` followed by base64url(32 bytes from `crypto/rand`). Stored as `sha256(raw)` in `refresh_tokens.token_hash` (32 bytes). The prefix makes leaked tokens easy to recognise for secret scanners and the FR-091 scan test. `crypto.NewToken(prefix)` returns `(plain, hash)`.
- **One live credential per session**: a partial unique index `refresh_tokens(session_id) WHERE used_at IS NULL`.
- **Rotation algorithm.** The whole operation is one transaction at READ COMMITTED. Lock order is `users`, then `device_sessions`, then `refresh_tokens`.
  1. Look up the token row by hash without a lock. Not found means `auth.refresh_rejected`, with no audit entry (nothing to attribute).
  2. `SELECT … FROM users WHERE id=$u FOR UPDATE`, then `device_sessions WHERE id=$s FOR UPDATE`, then `refresh_tokens WHERE id=$t FOR UPDATE`. Re-read after the locks are held.
  3. Session already ended means reject (no further change).
  4. `used_at IS NOT NULL` means **reuse**: end the session (`end_reason='refresh_reuse'`), write audit `auth.session.refresh_reuse_detected`, commit, and return `auth.refresh_rejected`.
  5. `device_id` in the request is not the session's `device_id` means **device mismatch**: end the session (`end_reason='device_mismatch'`), write audit `auth.session.device_mismatch`, commit, reject.
  6. The transport does not match the session's `client_type` (cookie for a `mobile` session, or body for a `web` session) means reject with no state change. This cannot happen legitimately.
  7. `@now >= idle_expires_at` or `@now >= absolute_expires_at` means reject (expired; no mutation, see data-model "expiry is computed, not stored").
  8. `users.status <> 'active'` means reject.
  9. Otherwise: set `used_at=@now` and `replaced_by` on the old row, insert the new token row, set `last_active_at=@now` and `idle_expires_at=@now+idle(role)` on the session, write audit `auth.session.refreshed`, commit, then sign a new access JWT.
  10. **A lock does not block refresh** (D-08, FR-043): the throttle table is not consulted here.
- **Concurrency (D-10)**: two refreshes presenting R1 both block on the `users` row lock. The second re-reads after the first commits, sees `used_at` set, takes the reuse branch, and ends the session. Exactly one succeeds. The integration test uses a start barrier and N=2…8 goroutines.
- **No grace window and no idempotent replay** (spec note "Idempotency"). Lost-response recovery is a client concern (022/024 keep a single refresh in flight and persist the new credential before using it).
- **Sign-in on the same installation (D-11)**: the success transaction locks the user row, ends any live session with the same `(user_id, device_id)` (`end_reason='replaced'`), and inserts the new one. The partial unique index `device_sessions(user_id, device_id) WHERE ended_at IS NULL` backs this.

### P-04 Browser vs mobile transport (`client_type`) and CSRF

- `client_type` is required on sign-in: `mobile` or `web`. It is stored on the session. `platform` (`android`, `iphone`, `web`) is informational and independent.
- **mobile**: `refresh_token` is returned in the JSON body. The device stores it in secure storage (024). Refresh sends `{device_id, refresh_token}` in the body.
- **web**: the body **never** contains `refresh_token`. The response sets:
  `Set-Cookie: __Secure-divya_rt=<token>; Path=/v1/auth/refresh; HttpOnly; Secure; SameSite=Strict; Max-Age=<seconds to absolute expiry>`
  - There is no `Domain` attribute, so the cookie is host-only on the API host.
  - The access token stays in SPA memory only. A page reload calls refresh to get a new one.
  - Sign-out, sign-out-everywhere and any web-session revocation that the caller performs on its own session clear the cookie (`Max-Age=0`, same Path).
- **CSRF defences on the cookie path**. These apply when the refresh credential comes from the cookie, and to `client_type=web` sign-in.
  1. `Origin` must exactly equal one of `WEB_ORIGINS`. A missing Origin or `null` is rejected.
  2. The custom header `X-Divya-CSRF: 1` is required, which forces a CORS preflight.
  3. `Content-Type: application/json` is required (Huma returns 415 for other types; the handler also checks).
  4. CORS uses an exact-origin allowlist with `Access-Control-Allow-Credentials: true`. There is never a `*` and never a pattern.
  5. `SameSite=Strict` is kept as defence in depth.

  A failure returns 403 `auth.csrf_rejected`.
- **Domain prerequisite (D-21)**: in staging and production the admin panel and the API share a registrable domain (027). Substitutes:
  - Local: the Vite dev server proxies `/v1` to `:8080`, so the panel and API are same-origin on `http://localhost`. Chromium and Firefox treat localhost as a secure context, so `Secure` and `__Secure-` cookies work. Local Safari is unsupported.
  - Pull-request previews (`*.pages.dev` with `*.run.app`): the cookie cannot work cross-site. 022 either uses a Pages Function same-origin proxy (027) or signs in with `client_type=mobile` and keeps both tokens in memory only (the user signs in again after a reload). The API does not restrict `client_type` per role, so this needs no API change.
- The iPhone PWA transport is decided in 024 (research §10.10). Both transports are available.

### P-05 Admin second factor and the interim proof

- After a correct password for an `active` admin who is not locked, the API returns `next_step=mfa_required` (TOTP confirmed) or `mfa_enrolment_required` (no confirmed TOTP), plus `challenge_token`. **No session is created.**
- **Challenge token**: `dmc_` followed by base64url(32 random bytes), stored as SHA-256 in `mfa_challenges`. It carries `purpose` (`verify` or `enrol`), `expires_at = now+5m` (expired at exactly 5 minutes), `consumed_at`, and the device details captured at sign-in.
- It is presented as `Authorization: Bearer dmc_…` on operations declared with the `mfaChallenge` security scheme only. It is opaque and not a JWT, so the bearer verifier rejects it (FR-035). The challenge verifier rejects JWTs and `drt_` tokens because their hashes never exist in `mfa_challenges`.
- A purpose that does not match the operation (for example `verify` used on enrolment) is rejected as `auth.unauthenticated`.
- The challenge is **single use**: `consumed_at` is set in the transaction that creates the session.
- **The counter is cleared only after the second factor succeeds.** Clearing it on the password step would let someone who knows the password make unlimited code guesses. Wrong TOTP or recovery codes count as failures on the same `user` subject (D-08). While locked, verify returns `auth.invalid_credentials` even when the code is correct.
- **TOTP**: RFC 6238 with HMAC-SHA1, 6 digits, a 30-second step, and acceptance of steps N−1, N and N+1. A step is accepted only if `step > admin_mfa.last_used_step`. Comparison is constant-time. The update of `last_used_step` runs under `FOR UPDATE` on `admin_mfa`, so two submissions of one code in parallel give exactly one success.
- **Secret**: 20 random bytes, base32 without padding, returned once with an `otpauth://totp/Divya:<email>?secret=…&issuer=Divya&algorithm=SHA1&digits=6&period=30` URI. The panel draws the QR code client-side (022), so the server needs no QR or image dependency. Stored as `crypto.Seal(secret, aad="admin_mfa.secret:"+user_id)`.
- **Pending secrets**: enrolment and replacement keep a pending secret in `pending_secret_enc` until a code from it is confirmed. Calling "start" again replaces the pending secret.
- **Recovery codes**: 10 codes, each 80 bits, Crockford base32 shown as `XXXX-XXXX-XXXX-XXXX`. Input is normalised (uppercase, hyphens and spaces removed) and stored as SHA-256. A code is single use (`used_at`). Regeneration sets `revoked_at` on the whole previous batch.
- **Replacing the authenticator**: start needs password plus a current code or an unused recovery code (which is consumed and audited `auth.mfa.recovery_code_used`). Confirm needs a code from the new secret; it swaps the secrets, sets `last_used_step` from the new secret's matched step, ends all **other** admin sessions (`end_reason='mfa_replaced'`), and writes audit `auth.mfa.replaced`. Recovery codes are not regenerated by a replacement.

### P-06 Passwords

- **Policy (D-07)**: NFC-normalise with `golang.org/x/text/unicode/norm`, so Gujarati and Hindi passphrases typed on different keyboards hash the same. Count Unicode code points. The rules are checked in this order, and the first failing rule's code is returned (FR-011, FR-093):
  1. under 10 code points: `auth.password_too_short`
  2. over 256 code points: `auth.password_too_long`
  3. equal to own email (case-insensitive, trimmed), or normalises to own mobile: `auth.password_matches_identifier`
  4. on the common list (case-insensitive): `auth.password_too_common`
  5. on change, equal to the current password: `auth.password_unchanged`. This is checked by verifying the new password against the stored hash, outside the transaction.

  No truncation, and no composition rules.
- **Common list**: `internal/auth/commonpasswords/10k.txt`, embedded with `go:embed`. The source is SecLists `Passwords/Common-Credentials/10k-most-common.txt` (MIT). It is lowercased and deduplicated at build time. A header file records the source URL, retrieval date and SHA-256; a unit test pins the line count and SHA-256 so changes are deliberate. It is loaded once into a `map[string]struct{}`.
- **Hash**: Argon2id with m=19456 KiB, t=2, p=1, a 16-byte salt and a 32-byte key. The encoding is PHC: `$argon2id$v=19$m=19456,t=2,p=1$<b64salt>$<b64hash>` (raw std base64, the same format as alexedwards/argon2id). Comparison uses `subtle.ConstantTimeCompare`.
- **Rehash on login**: if the parameters in the stored PHC string differ from the current ones, a new hash is computed after a successful verify, still outside the transaction. It is written in the success transaction with `WHERE password_hash = @old`.
- **Concurrency cap**: a buffered-channel semaphore of size `ARGON2_MAX_CONCURRENCY` (default 4). Waiting is bounded by the request context and a 2-second cap. If the wait times out, the API returns 503 `auth.busy`. That response is independent of whether the account exists.
- **Dummy verify**: when no account matches, the API verifies the presented password against a dummy PHC hash. The dummy is computed once (`sync.Once`) with the current parameters. Every sign-in attempt therefore performs **exactly one** Argon2 verify, whether the account is unknown, the password is wrong, the account is locked, or the account is not active. This supports SC-003.
- **Hashing never runs inside a database transaction** (pool of 5 connections).
- **Change password**: the current password is verified and the new one hashed outside the transaction. The transaction then locks the user row, checks that `password_hash` still equals the hash that was verified (otherwise it retries once, then fails), updates the hash and `password_changed_at`, ends all **other** live sessions (`end_reason='password_changed'`), and writes audit `auth.password.changed`.
  - A wrong current password returns 403 `auth.reauth_failed` and counts as a failure (D-08).
  - **Safe retry**: if the current password fails but the presented `new_password` verifies against the stored hash, the change already happened (for example the first response was lost). The API returns 204 without counting a failure. This costs one extra Argon2 verify and only applies when the presented new password is in fact the stored one.

### P-07 Lockout (D-08), generic and shared across instances

- **Table** `auth_throttles(org_id, scope, subject, failures timestamptz[], locked_until, updated_at)`. The primary key is `(org_id, scope, subject)`.
- **Subject** = `crypto.BlindIndex("throttle", scope|kind|value)`, with kinds:
  - `user|<user_id>` when an account matches the identifier and role (FR-045 says the lock is per account, so a client's email and mobile share one counter)
  - `email|<normalised>` or `mobile|<normalised>` when no account matches. This includes an admin's mobile, because admins sign in with email only.
- **Pure function** `throttle.Apply(state, now, policy) (state, startedLock bool)`:
  - `locked(now)` is `now < locked_until`.
  - A failure during a lock changes nothing. The lock is not extended.
  - If a lock has ended (`locked_until <= now`), `failures` is cleared first (D-08: "the end of the lock clears the counter").
  - Otherwise, keep the failures `f` where `now - f < 15m`, append `now`, and keep at most 5.
  - At 5 failures: `locked_until = now + 15m`, `failures = []`, `startedLock = true`.

  A property test compares it with a reference model that keeps every failure. Note: under this exact sliding-window definition, US-4 scenario 5 has 2 failures in the window at T+17m (T+3m and T+17m), not the "1" its wording says. The outcome (not locked) is the same, and the tests assert the outcome.
- **Writes**: on failure, `INSERT … ON CONFLICT DO NOTHING`, then `SELECT … FOR UPDATE`, apply, `UPDATE`, all in the failure audit's transaction. On success, `DELETE` the subject row. On password reset (FR-053), `DELETE` the user subject.
- **Reuse by 004**: `scope` is free text (CHECK on a known list, extended by 004's migration). 004 calls `auth.Throttle` through the exported interface (contracts/go-capabilities.md). It does not touch the table directly.
- **Housekeeping**: rows are tiny and few. Purging stale rows is a follow-up job registered on 002's scheduler after both features are merged. 003 does **not** depend on 002.

### P-08 Sign-in flow (client and admin)

Phases (the transaction boundaries matter):

1. **Validate shape.**
   - An identifier containing `@` is treated as an email: trim and lowercase, `net/mail` parse with no display name, at most 254 characters.
   - Otherwise it is treated as a mobile: strip spaces and hyphens, accept `[6-9]\d{9}` with an optional prefix `0`, `91` or `+91`, and store as `+91XXXXXXXXXX`.
   - Anything else returns 422 `auth.identifier_invalid` (D-09 allows this; it reveals nothing).
   - Also validate `device{id uuid, name 1..100, platform}` and `client_type`. A `password` longer than 1024 bytes returns 422 `validation_failed`, which bounds the work.
2. **Read without locks**: the default org (P-12), the user by email or mobile (a mobile identifier with `role=admin` counts as no match), and the throttle state.
3. **Exactly one Argon2 verify**, against the real hash or the dummy, outside any transaction. Then a rehash if needed.
4. **Decide**:
   - no match, or wrong password: failure, counted
   - locked: failure, not counted
   - correct password but status is not `active`: failure, not counted, BR-CLI-04
   - client: success
   - admin: challenge
5. **One short transaction**:
   - Failure: throttle apply (when counted), audit `auth.sign_in.failed` with an internal `reason`, and also `auth.lockout.started` when a lock starts.
   - Client success: lock the user row, re-check status and lock (the state may have changed during the hash), end the session for the same device, insert a session and refresh token, update `last_sign_in_at`, apply the rehash, delete the throttle row, audit `auth.sign_in.succeeded`.
   - Admin: insert `mfa_challenges`, audit `auth.mfa.challenge_issued`.
6. **Respond**. Every failure is the same 401 `auth.invalid_credentials` body, byte-for-byte, with no `Retry-After`.

### P-09 Every operation declares Security (FR-030…FR-035)

- **Security schemes**, registered by `authz.Install` in `components.securitySchemes`:
  - `bearer`: `{type: http, scheme: bearer, bearerFormat: JWT}`. Scopes carry role names, which OpenAPI 3.1 allows for non-OAuth schemes.
  - `mfaChallenge`: `{type: http, scheme: bearer, bearerFormat: opaque}`.
- **Declaration helpers** for modules:
  - `authz.Require()`: any signed-in role, `[{bearer: []}]`
  - `authz.Require(authz.RoleAdmin)`: `[{bearer: ["admin"]}]`
  - `authz.ChallengeOnly()`: `[{mfaChallenge: []}]`
  - `authz.Public()`: `[]`, an explicitly empty list
- **Middleware**, installed with `api.UseMiddleware` *before* any `huma.Register` (Huma captures global middlewares at registration):
  - `op.Security == nil`: deny with 500 `platform.security_undeclared`. This is fail-closed, and the next point stops it from ever reaching production.
  - Empty: call `next`.
  - Otherwise: for the one requirement object (more than one is not supported, enforced at startup), find the verifier by scheme name. An unknown scheme is a startup panic. Extract `Authorization: Bearer`. Verify; on error, 401 `auth.unauthenticated` with `WWW-Authenticate: Bearer`. If role scopes are present and the principal's role is not among them, 403 `auth.forbidden`. Then `huma.WithValue(ctx, principalKey, p)`.
  - The middleware runs **before** body parsing and validation, so unauthenticated calls never reach validation or handlers. No data is returned and nothing changes.
- **Startup guard**: `authz.AssertDeclared(api)` is called at the end of `buildAPI`. It panics if any operation has nil `Security` or names an unregistered scheme. As a result `divya openapi`, `serve` and the tests all fail fast.
- **Contract-walk tests** in `cmd/divya/contract_test.go`:
  - (a) the static walk over `buildAPI(deps{}).OpenAPI().Paths`: every operation declares Security; the operations with empty Security are exactly `{health-live, health-ready, auth-sign-in, auth-refresh}`; the `mfaChallenge`-only operations are exactly `{auth-mfa-verify, auth-mfa-enrolment-start, auth-mfa-enrolment-confirm}`; every admin-scoped operation id is listed in a golden file `testdata/admin-operations.txt`.
  - (b) the runtime walk: for every non-public operation, send the request with no credential, a tampered JWT, an expired JWT, a revoked session, a refresh token, and a challenge token. For admin operations also send a valid client JWT. Assert 401 or 403 with the right `code`, and assert through a recording fake store that nothing was written. Path parameters are filled with random UUIDs and bodies with `{}`.

  The walk covers every future feature's operations automatically.
- **Later features**: 002's `/internal/tick` must register its own scheme (for example `schedulerOidc`) with `authz.Install` and add its operation to the contract test's allowlist. This plan tells 002 (Risks R-2).

### P-10 RFC 9457 errors with stable codes (FR-093)

- `httpx` defines `type ErrorModel struct { huma.ErrorModel; Code string \`json:"code"\` }`. The type name stays `ErrorModel`, so the OpenAPI schema name does not change and the diff is additive.
- `httpx.InstallErrors()` sets `huma.NewError` (and `NewErrorWithContext`) once, idempotently:
  - Every problem gets `code`. The default is derived from status: `validation_failed`, `bad_request`, `not_found`, `unsupported_media_type`, `rate_limited`, `internal`.
  - `type` = `urn:divya:problem:<code>`.
  - **`ErrorDetail.Value` is always dropped.** Huma otherwise echoes the raw request body on parse errors (`huma.go` validateBody: `Value: string(body)`) and field values on validation errors. That would put passwords and tokens in responses (FR-091).
- Domain errors use `httpx.Problem(status, code, detail)`.
- `code` stays a plain `string` in the schema, not an enum: adding enum values later would be a breaking change for oasdiff. The catalogue is in [contracts/error-codes.md](./contracts/error-codes.md) and mirrored in `packages/domain` for UI translation by 022 and 024.

### P-11 Audit (FR-090, FR-091)

- Audit entries reuse 001's `audit_log` (spec Key Entities). **There is no new audit table and no change to `audit_log` columns.**
- Column mapping:
  - `entity='user'`, `entity_id` = the affected user. It is NULL for failures on unknown identifiers.
  - `actor_kind`, `actor_id` as in US-11.
  - `action` is from the fixed catalogue (data-model).
  - `before` and `after` are typed Go structs marshalled to JSON. They are never maps built from request input.
  - `after` carries the event context (`session_id`, `platform`, `ip`, `reason`, `replaced_session_id`, …).
  - `created_at` = `@now` from the clock.
- For unknown identifiers the entry stores only `identifier_kind` and `subject_ref`: the first 8 bytes of the throttle HMAC in hex. Repeated attempts can be correlated without storing the identifier as typed.
- `platform/audit.Writer.Write(ctx, Entry)` returns an error when the context has no transaction. Audit writes therefore cannot happen outside the unit of work.
- **Scan test (SC-006)**: `authtest.Secrets` records every password, code, recovery code, TOTP secret (base32 and raw hex), access, refresh and challenge token, and unmatched identifier used by the acceptance suite. At the end, the scan checks:
  - every `audit_log` row cast to text
  - the captured slog output (a JSON handler writing to a buffer, wired through `httpx.NewServer` in tests)
  - every response body except the one-time displays (enrolment secret, recovery codes at issue, tokens in their own issuing response)

  No recorded secret may appear in any of them.

### P-12 Organisation resolution

- Users are globally unique by email and mobile (D-18), so lookup needs no org. The org is still needed for audit and throttle rows when no account matches.
- `store.DefaultOrg(ctx)` returns the single row in `orgs` and is cached after the first success. If there are no orgs it returns 503 `auth.not_configured`. More than one org is an error until multi-org sign-in has its own feature.
- The org is created by `divya admin create` when none exists (`--org-name`, default "Divya"). If exactly one exists it is reused. If more than one exists, `--org-id` is required.

### P-13 Bootstrap command `divya admin create` (D-15, US-9)

- Flags: `--email` (required), `--name` (required), `--mobile` (optional), `--org-name`, `--org-id`.
- Password input:
  - If stdin is a TTY: prompted twice with `golang.org/x/term.ReadPassword` (no echo).
  - Otherwise: one line is read from stdin, for scripted local use.

  The password is never accepted as a flag or an environment variable.
- **Config**: needs only `DATABASE_URL`. It uses `config.LoadDB()`, a subset, so the owner does not need JWT or PII keys to bootstrap.
- **Transaction**:
  1. `pg_advisory_xact_lock(<const auth bootstrap key>)`
  2. resolve or create the org
  3. refuse if an admin exists in the org (`bootstrap.admin_exists`)
  4. refuse if the email or mobile is taken (`auth.email_taken`, `auth.mobile_taken`)
  5. insert the admin: `active`, `email_verified_at=@now`, no `admin_mfa` row
  6. write audit `auth.admin.bootstrapped` with `actor_kind='system'`

  Policy validation and hashing run before the transaction.
- **Output**: refusals print a stable code and an English message to stderr and exit with status 1. Success prints the user id and email only. The command has no `--verbose` mode that could print input.
- **Production runbook**: the owner runs it from their own machine with the Supabase connection string. Agents never do. Contract: [contracts/cli.md](./contracts/cli.md).

### P-14 Configuration, secrets and key rotation

| Env var | Format | local / ci | staging / production |
|---|---|---|---|
| `JWT_SIGNING_KEYS` | `kid:base64(32-byte Ed25519 seed)[,…]` | If empty, an ephemeral key is generated at startup and a warning logged (no values) | Required; Secret Manager |
| `JWT_ACTIVE_KID` | kid present in the ring | Ephemeral kid | Required |
| `PII_ENCRYPTION_KEYS` | `id:base64(32 bytes)[,…]`, id 1–65535 | Ephemeral if empty (a TOTP enrolled locally becomes unreadable after a restart; documented) | Required; Secret Manager |
| `PII_ACTIVE_KEY_ID` | id present in the ring | Ephemeral id | Required |
| `BLIND_INDEX_KEY` | base64(32 bytes) | Ephemeral if empty | Required; Secret Manager; **never rotated casually** (see quickstart) |
| `WEB_ORIGINS` | comma-separated exact origins | `http://localhost:5173` | Required (027 supplies the admin origin) |
| `TRUSTED_PROXY_HOPS` | int ≥ 0 | `0` (use RemoteAddr) | Set by 027 after confirming Cloud Run's `X-Forwarded-For` layout (research §6, unverified) |
| `ARGON2_MAX_CONCURRENCY` | int 1–16 | 4 | 4 (re-tuned after the staging benchmark) |

- `config.Config` implements `slog.LogValuer` and redacts every key field. Key material is decoded once into typed rings (`crypto.Keyring`, `auth.SigningKeys`) and is never kept as a string after `Load`.
- Validation rejects:
  - keys of the wrong length
  - an active id that is not in its ring
  - duplicate ids
  - `BLIND_INDEX_KEY` equal to any PII key
- `.env.example` currently lists `PII_ENCRYPTION_KEY` and `JWT_SIGNING_KEY` (single, HMAC-style). It must be updated to the names above. **Agents may not edit `.env*` (CLAUDE.md), so this is an owner action** (see the report).
- **Production**: 027 maps the Secret Manager secrets to Cloud Run environment variables. Rotation procedures for each key class are in [quickstart.md](./quickstart.md#key-rotation).

### P-15 Transactions and the shared unit of work

- `internal/platform/db` gains `TxRunner.InTx(ctx, fn func(ctx) error) error` (READ COMMITTED). The `pgx.Tx` is stored on the context. A nested `InTx` **joins** the outer transaction, so 005 can call `auth.Accounts.CreateClient` inside its own registration transaction.
- `db.Queries(ctx, pool) dbgen.Querier` returns tx-bound queries when the context carries a transaction, and pool-bound ones otherwise.
- Coordination: 002 (outbox in the same transaction) may build the same helper in parallel. Whichever merges first owns `platform/db/tx.go`; the other rebases onto it. The interface above is proposed for both (Risk R-3).

### P-16 Idempotency

- Credential operations **do not accept** `Idempotency-Key`: sign-in, mfa-verify, enrolment start and confirm, refresh, password change, recovery-code regeneration, and authenticator replace start and confirm.
  - Replaying a stored response would hand freshly issued tokens or codes to anyone who holds the key. That is a grace window, which the spec forbids.
  - Each of these operations already has defined replay semantics: single-use credentials, and the safe retry for password change in P-06.
- The session-ending operations (sign-out, sign-out-everywhere, revoke device, admin revoke and end-all) are **naturally idempotent**: the end state is the same and a repeat returns 204, or 404 for an already-ended device. They need no key.
- No generic idempotency middleware exists in the repository yet. When one is built (it should belong to 002 or a platform follow-up), 003's operations stay exempt by declaration (`Metadata["idempotency"]="exempt"`), and the contract walk can then check that every other mutation accepts the header. This is Deviation 1.

### P-17 Client IP and the coarse per-IP limiter

- `httpx.ClientIP(r, hops)`:
  - With `hops=0`, use `RemoteAddr`.
  - Otherwise use the `X-Forwarded-For` entry at position `len-hops`. The first value is never trusted.
  - The result is stored in audit `after.ip` only, never in `device_sessions`.
- `internal/platform/ratelimit` is an in-memory, per-instance, fixed-window limiter keyed by IP. It uses the standard library, needs no `x/time/rate`, and evicts entries on a size cap.
- It applies only to the public credential operations: sign-in, refresh, mfa-verify and enrolment. The limit is 30 requests per minute per IP. Over the limit returns 429 `rate_limited`.
- It is defence in depth against Argon2 CPU exhaustion. It does not depend on accounts, so it reveals nothing about them.

### P-18 Migration numbering rule (coordination with 002)

- **Rule**: a feature's first migration uses its feature number as the goose version (`000NN_<slug>.sql`). Feature 003 therefore uses **`00003_auth.sql`**, and 002 uses `00002_*`. A feature that needs more than one migration takes the next number above the highest version on `main` at merge time.
- **Out-of-order guard**: goose rejects a migration whose version is lower than one already applied, and `db.Migrate` does not allow missing migrations. So when a PR is rebased for merge, every **new** migration it adds must have a version greater than the highest version already on `main`. If 003 merges before 002, 002 renumbers `00002_*` to the next free number during its rebase (a file rename; nothing is applied anywhere before merge).
- A small CI script `scripts/check-migration-order.sh` (P2 task) compares the PR's new migration versions with `origin/main` and fails with that instruction. The rule is also recommended for `.claude/rules/sql.md` (owner action; agents do not edit rules).
- Feature numbers below 100 cannot collide with each other's first migration, and the guard catches any late merge.

### P-19 Depguard and lint changes (`.golangci.yml`)

```yaml
depguard:
  rules:
    platform-is-a-leaf:            # widened: any business module, not only health
      files: ["**/internal/platform/**"]
      deny:
        - pkg: "github.com/NandanMaheta/divya/apps/api/internal/health"
          desc: platform packages must not import business modules
        - pkg: "github.com/NandanMaheta/divya/apps/api/internal/auth"
          desc: platform packages must not import business modules
    crypto-primitives-stay-in-place:
      files: ["**/internal/**", "!**/internal/auth/**", "!**/internal/platform/crypto/**"]
      deny:
        - pkg: "github.com/golang-jwt/jwt/v5"
          desc: tokens are issued and verified only by internal/auth
        - pkg: "golang.org/x/crypto/argon2"
          desc: password hashing lives only in internal/auth
    no-legacy-libs: (unchanged)
forbidigo:
  forbid:
    - pattern: '^time\.Now$'
      msg: inject a clock (constitution III)
  # exclusions: cmd/, internal/platform/httpx/ (request duration logging), *_test.go
```

- Every later module appends its own path to `platform-is-a-leaf` in the same PR that creates the module. Planner tasks include that line for each new module.
- A prefix deny on `…/internal` combined with an allow on `…/internal/platform` would need depguard's longest-match semantics, which the plan has not verified. The explicit list is the reliable form.
- Verification: temporarily add a forbidden import, run `golangci-lint run ./...`, and see it fail (quickstart).

### P-20 Scheduler jobs and outbox events

**None in 003.** The feature depends only on 001. Session expiry is computed at read time, so no job ends sessions. A later follow-up, after 002 is merged, may register `auth.purge_stale` (throttle rows, expired challenges, used refresh tokens of sessions past their absolute expiry). 003 publishes no domain events: no consumer needs them yet, and 004's OTP and 005's approval flows call the capabilities directly.

## API operations (OpenAPI diff)

The full schemas are in [contracts/openapi-auth.yaml](./contracts/openapi-auth.yaml). The expected generated diff is **additive only**:

- 16 new operations under `/v1/auth/*` and `/v1/admin/users/*`
- `components.securitySchemes.{bearer, mfaChallenge}`
- an `ErrorModel.code` property
- `security: []` on `health-live` and `health-ready`

| operationId | Method and path | Security | Request | Success | Errors (`code`) |
|---|---|---|---|---|---|
| auth-sign-in | POST /v1/auth/sign-in | Public | `SignInRequest{identifier, password, client_type, device{id,name,platform}}` + headers Origin, X-Divya-CSRF (web) | 200 `SignInResponse{next_step, session?, challenge?}` + Set-Cookie (web) | 401 invalid_credentials · 422 identifier_invalid / validation_failed · 403 csrf_rejected · 429 rate_limited · 503 busy / not_configured |
| auth-mfa-verify | POST /v1/auth/mfa/verify | mfaChallenge | `{code}` xor `{recovery_code}` + web headers | 200 `SessionResponse` + Set-Cookie (web) | 401 unauthenticated (bad challenge) / invalid_credentials (wrong code, locked) · 422 · 429 |
| auth-mfa-enrolment-start | POST /v1/auth/mfa/enrolment | mfaChallenge (purpose=enrol) | – | 200 `TotpSecretResponse{secret, otpauth_uri}` | 401 · 429 |
| auth-mfa-enrolment-confirm | POST /v1/auth/mfa/enrolment/confirm | mfaChallenge (purpose=enrol) | `{code}` + web headers | 200 `EnrolmentResponse{session, recovery_codes[10]}` + Set-Cookie | 401 · 409 mfa_no_pending_secret · 422 |
| auth-refresh | POST /v1/auth/refresh | Public | `{device_id, refresh_token?}` + cookie `__Secure-divya_rt` + Origin, X-Divya-CSRF (cookie path) | 200 `SessionResponse` (+ Set-Cookie when cookie path) | 401 refresh_rejected · 403 csrf_rejected · 429 |
| auth-sign-out | POST /v1/auth/sign-out | bearer (any) | – | 204 (+ clearing Set-Cookie) | 401 |
| auth-sign-out-everywhere | POST /v1/auth/sign-out-everywhere | bearer (any) | – | 204 (+ clearing Set-Cookie) | 401 |
| auth-devices-list | GET /v1/auth/devices | bearer (any) | – | 200 `DeviceList{items[]}` | 401 |
| auth-device-revoke | DELETE /v1/auth/devices/{sessionId} | bearer (any) | path uuid | 204 | 401 · 404 device_not_found |
| auth-password-change | POST /v1/auth/password/change | bearer (any) | `{current_password, new_password}` | 204 | 401 · 403 reauth_failed · 422 password_* |
| auth-recovery-codes-regenerate | POST /v1/auth/mfa/recovery-codes | bearer [admin] | `{password, code}` | 200 `{recovery_codes[10]}` | 401 · 403 forbidden / reauth_failed |
| auth-authenticator-replace-start | POST /v1/auth/mfa/authenticator | bearer [admin] | `{password, code}` xor `{password, recovery_code}` | 200 `TotpSecretResponse` | 401 · 403 · 422 |
| auth-authenticator-replace-confirm | POST /v1/auth/mfa/authenticator/confirm | bearer [admin] | `{code}` | 204 | 401 · 403 · 409 mfa_no_pending_secret · 422 |
| auth-admin-user-devices-list | GET /v1/admin/users/{userId}/devices | bearer [admin] | path uuid | 200 `DeviceList` | 401 · 403 · 404 user_not_found |
| auth-admin-user-device-revoke | DELETE /v1/admin/users/{userId}/devices/{sessionId} | bearer [admin] | path uuids | 204 | 401 · 403 · 404 user_not_found / device_not_found |
| auth-admin-user-sessions-end-all | POST /v1/admin/users/{userId}/sessions/end-all | bearer [admin] | path uuid | 204 | 401 · 403 · 404 user_not_found |

The account capabilities FR-050…FR-055 (create, verify contacts, approve, disable, enable, reset password, end all, replace contact) are **Go interfaces only**, with no HTTP operation (US-10 scenario 7). See [contracts/go-capabilities.md](./contracts/go-capabilities.md).

## Project Structure

### Documentation (this feature)

```text
specs/003-auth-core/
├── spec.md              # approved spec
├── research.md          # §1–10 spec stage; §11 plan decisions (Phase 0)
├── plan.md              # this file
├── data-model.md        # Phase 1
├── quickstart.md        # Phase 1
├── contracts/
│   ├── openapi-auth.yaml    # intended OpenAPI fragment (generated doc is authoritative)
│   ├── error-codes.md       # FR-093 catalogue
│   ├── go-capabilities.md   # exported Go interfaces for 004/005/007/022
│   └── cli.md               # `divya admin create`
├── checklists/requirements.md
└── tasks.md             # produced by planner (/speckit-tasks), not by this plan
```

### Source Code

```text
apps/api/
├── cmd/divya/
│   ├── main.go                 # buildAPI(deps) nil-safe; authz.Install before Register; AssertDeclared; `admin create`
│   ├── admin.go                # bootstrap CLI (flags, TTY prompt, exit codes)
│   ├── contract_test.go        # static + runtime security walk (FR-034, SC-001)
│   ├── openapi_test.go         # `divya openapi` builds with zero deps
│   └── testdata/admin-operations.txt
├── internal/auth/
│   ├── service.go              # Service, Deps (Store, Clock, Keys, Hasher, TxRunner, Audit), constructor
│   ├── store.go                # Store interface (small, per use case)
│   ├── store_pg.go             # sqlc adapter (only file with pgx types)
│   ├── normalize.go            # email / mobile / identifier kind
│   ├── password.go             # policy, NFC, Argon2id, PHC, semaphore, dummy verify
│   ├── commonpasswords/        # 10k.txt + SOURCE.md + embed.go
│   ├── throttle.go             # pure Apply + store glue
│   ├── tokens.go               # JWT issue/verify, key ring, refresh/challenge token helpers
│   ├── signin.go  refresh.go  sessions.go  mfa.go  totp.go
│   ├── accounts.go             # FR-050…055 capabilities (exported interface)
│   ├── bootstrap.go            # FR-080 service part
│   ├── verifiers.go            # authz.Verifier impls: bearer, mfaChallenge
│   ├── handler.go              # Register(api, svc, cookieCfg): self-service + MFA ops
│   ├── handler_admin.go        # admin ops
│   ├── errors.go  audit.go     # domain errors → problems; audit action catalogue + builders
│   ├── authtest/               # test helpers: fake clock, key gen, fixtures, Secrets registry
│   └── *_test.go               # see Test strategy
├── internal/platform/
│   ├── authz/{authz.go, authz_test.go}
│   ├── crypto/{aead.go, blindindex.go, token.go, keyring.go, *_test.go}
│   ├── audit/{audit.go, audit_integration_test.go}
│   ├── db/{tx.go, tx_integration_test.go}
│   ├── ratelimit/{ratelimit.go, ratelimit_test.go}
│   ├── httpx/{httpx.go(+), errors.go, cors.go, clientip.go, *_test.go}
│   └── config/{config.go(+), config_test.go(+)}
├── migrations/00003_auth.sql
├── queries/{auth.sql, audit.sql}
├── internal/platform/db/dbgen/*      # regenerated by sqlc
└── .golangci.yml                     # P-19
internal/health/handler.go            # add Security: authz.Public() to both ops
packages/api-client/{openapi.yaml, src/schema.ts}   # regenerated
packages/domain/src/auth-error-codes.ts              # code list for UI translation (FR-093)
scripts/check-migration-order.sh  + .github/workflows/pr.yml step (P-18)
docs/adr/0007-contact-data-at-rest.md                # written by this plan
```

**Structure decision**: one business module (`internal/auth`) and five platform packages: three new ones (`authz`, `crypto`, `audit`) plus `ratelimit`, and extensions to `db`, `httpx` and `config`. This follows the go-module-pattern skill. The admin and client screens are 022 and 024; 003 changes no TypeScript app code except the regenerated client and a code list in `packages/domain`.

## Test strategy

All tests are written first by `test-engineer`, and implementers never edit them. Time is controlled by `authtest.Clock` (settable and advanceable). Real-time waits are forbidden. "Int" means integration on `dbtest.New(t)`, skipped by `-short`.

| Layer | What | Where | Covers |
|---|---|---|---|
| Static | golangci-lint (depguard P-19, forbidigo `time.Now`, gosec), `sqlc vet`, generated code current, OpenAPI diff, oasdiff | CI | Constitution IV, V |
| Unit | Email and mobile normalisation tables (all D-17 forms, rejections) | `normalize_test.go` | FR-003, D-16, D-17, US-1 s1–2, s11 |
| Unit | Password policy rule order and codes; 64-char Gujarati and Hindi passphrase; 257 rejected; NFC equivalence; common list pinned (count + SHA-256) | `password_test.go` | FR-011, D-07, US-7 s3–6 |
| Unit | PHC encode/decode, parameter-change rehash detection, dummy verify, semaphore saturation returns `ErrBusy` | `password_test.go` | FR-010, FR-012 |
| Property | PHC round-trip; normalisation idempotent; mobile normaliser accepts exactly the D-17 grammar | `*_prop_test.go` (rapid) | D-16, D-17 |
| Unit + Property | `throttle.Apply` against a reference model; boundaries T+18m59s locked, T+19m unlocked; no extension during a lock; end of lock clears | `throttle_test.go` | D-08, US-4 s1–5, SC-004 |
| Unit | TOTP: RFC 6238 Appendix B SHA-1 vectors (6-digit truncation), ±1 step accepted, ±2 rejected, `step <= last_used_step` rejected | `totp_test.go` | FR-061, US-5 s4–5 |
| Unit | JWT: valid; expired at exp (leeway 0); `alg:none`; HS256 with the public key as secret; wrong kid; unknown kid; wrong aud, iss or typ; one-char tamper; refresh or challenge token as JWT | `tokens_test.go` | US-2 s5–6, s10, FR-035 |
| Unit + Property | `crypto`: Seal/Open round trip; tamper of any byte fails; wrong AAD fails; old key id still opens after rotation; BlindIndex deterministic and purpose-separated | `platform/crypto/*_test.go` | FR-064, P-14 |
| Unit | Service with fakes: sign-in decision table across status × password × lock × role × identifier kind | `signin_test.go` | **BR-CLI-04**, D-09, FR-020…023 (e.g. `TestSignIn_BR_CLI_04_PendingApprovalGenericFailure`, `TestSignIn_BR_CLI_04_PendingVerificationNotCounted`) |
| Unit | Accounts capabilities: status machine (both verification orders, idempotent marks, approve guard, disable and enable restore, last admin) | `accounts_test.go` | **BR-CLI-04** (`TestAccounts_BR_CLI_04_ActiveOnlyAfterBothVerifiedAndApproved`), FR-050…055, US-8 s5–6, US-10 |
| Unit | authz middleware: nil Security means 500; unknown scheme panics at Install; role scopes; principal in context; handler never invoked on failure | `platform/authz/authz_test.go` | FR-031, FR-032 |
| Unit | httpx errors: `code` present, `Value` stripped (malformed JSON containing a password is not echoed), `type` URN; CORS exact match; ClientIP hops | `platform/httpx/*_test.go` | FR-091, FR-093, P-04 |
| Int | Store: unique email and mobile under concurrent creates (exactly one wins); partial unique indexes; CHECK constraints reject bad states | `store_test.go` | FR-004, D-18 |
| Int | Refresh rotation: R1 to R2, reuse ends the session (R2 dead, access dead), device mismatch, transport mismatch, idle and absolute boundaries for both roles at ±1 minute | `refresh_test.go` | US-3 s1–2, s4–9, FR-040…043, SC-004 |
| Int (concurrency) | N goroutines with a barrier refresh the same R1: exactly 1 success, session ended, one reuse audit entry. Same for parallel TOTP submission of one code, parallel bootstrap, parallel disable of the last two admins, parallel sign-in from one device | `concurrency_test.go` | D-10, US-3 s3, FR-061, US-9 edge, D-19 |
| Int | Sessions: sign-out, everywhere, list (current flag, ended and expired excluded), revoke own and other (404), admin list, revoke and end-all; next request rejected 1 s after (fake clock) | `sessions_test.go` | US-6, US-8, D-12, SC-002 |
| Int | Lockout end-to-end through the HTTP handler; locked account's existing session keeps working and can refresh; reset clears | `lockout_test.go` | US-4 s6–9, US-3 s10 |
| Int | Admin MFA: enrol flow, verify, recovery codes (single use, regenerate invalidates), replace (other sessions end), challenge expiry at 5m and single use, enrolment cannot be skipped | `mfa_test.go` | US-5 s1–13, FR-060…065 |
| Int | Password change (others end, current kept, safe retry), reset capability (all end, lock cleared) | `password_flow_test.go` | US-7, US-10 s5, D-13 |
| Int | Audit: one entry per US-11 event with the right actor kind and fields; audit write without a transaction errors; transaction rollback leaves no audit row; `audit_log` UPDATE and DELETE refused | `audit_test.go`, `platform/audit/*` | US-11 s1, s3, FR-090 |
| Int (security) | Secret scan over audit rows, logs and responses for the full suite | `secretscan_test.go` | FR-091, SC-006, US-11 s2 |
| Int (security) | CSRF: cookie refresh without Origin, with `null`, a foreign or sibling origin, no `X-Divya-CSRF`, or a form content type gets 403 `csrf_rejected`; cookie attributes exact | `csrf_test.go` | P-04 |
| Contract | Static and runtime security walk; `divya openapi` builds with zero deps; error `code` present on every non-2xx in the walk; no FR-050…055 operation exists | `cmd/divya/contract_test.go`, `openapi_test.go` | FR-030, FR-034, SC-001, US-2, US-10 s7 |
| Int (timing) | SC-003: 50 attempts × 6 failure cases, medians within 10%, identical status and body bytes. Runs in `nightly.yml` (`-run Timing`), not on every PR, because CI runners are noisy | `timing_test.go` | D-09, SC-003, US-1 s5, US-4 s6 |
| Benchmark | Argon2id cost on the CI runner; staging check reported in the PR | `password_bench_test.go` | SC-007 |
| CLI | `divya admin create`: success, admin exists, policy failure, email taken, password absent from stdout, stderr, logs and audit (stdin pipe mode) | `cmd/divya/admin_test.go` (Int) | US-9, FR-080 |
| Component / E2E | None in 003 (no UI). Admin and client flows are tested in 022 and 024 against these operations. | – | – |
| Golden | GS-01…04 unaffected; they must still pass (none touch auth) | existing suite | Constitution I |

## Performance notes

- **Authenticated request**: Ed25519 verify (~50 µs) plus one primary-key join (`device_sessions` by PK, `users` by PK), about 1–3 ms to Supabase in the same region. There are no writes on reads (`last_active_at` changes only on sign-in and refresh).
- **Sign-in**: one Argon2id (budget 40–70 ms; the benchmark gates a warning above 150 ms), 3 reads, and one transaction of at most 7 statements. The expected p95 is well under 200 ms at this scale.
- **Change password**: 2 Argon2 runs (verify current and hash new), plus a third only in the safe-retry path. Under 200 ms p95 is expected; the benchmark checks it.
- **Refresh**: one transaction (3 locked reads, 3 writes, 1 audit insert), with no Argon2.
- **Pool and memory**: the pool has `MaxConns=5` and Argon2 runs outside transactions, so connections are held only for milliseconds. Argon2 memory is capped at 4 × 19 MiB. Cloud Run memory is set by 027 (512 MiB is enough).
- **Data growth**: about 100 clients × 1–3 refreshes a day gives roughly 100k `refresh_tokens` rows and 100k audit rows a year. Both fit the Supabase free tier. A purge job is a follow-up (P-20).

## Security notes

- **Threats covered**:
  - credential stuffing (lockout, per-IP limiter, Argon2 cap)
  - account enumeration (generic failure, dummy verify, identical bodies, no `Retry-After`)
  - stolen refresh token (rotation, strict reuse detection, device binding)
  - stolen access token (15-minute lifetime and revocation on the next request)
  - XSS on the admin panel (refresh in an HttpOnly cookie, access in memory)
  - CSRF (P-04)
  - JWT algorithm confusion (allowlist `EdDSA`, keys chosen by kid from config only, `jku`/`jwk` headers never used)
  - TOTP replay (`last_used_step`)
  - TOTP brute force (shared lockout; the counter is not cleared at the password step)
  - database read (passwords Argon2id; tokens and recovery codes SHA-256; TOTP secret AES-GCM with the key outside the database)
  - log and response leakage (FR-091 scan, Huma `Value` stripped, typed audit payloads)
  - bootstrap race (advisory lock)
- **Accepted residual risks**:
  - A lost refresh response signs the device out (D-10). This is accepted by the spec and mitigated by 022 and 024.
  - Locking out new sign-ins by knowing only an identifier (D-08). This is accepted; existing sessions are unaffected.
  - Contact data is readable at rest (ADR 0007).
  - The iPhone PWA storage weakness (decided in 024).
- **Out of scope here**: Cloudflare Access configuration (027), break-glass admin recovery (D-22, runbook in 027), and DPoP (Device-bound Proof of Possession) / proof-of-possession binding (later).
- **Reviews**: `security-reviewer` must review the migration, `internal/auth`, `platform/authz` and `platform/crypto`. The owner approves the PR (constitution workflow 5).

## Dependencies requiring owner approval

None of these is added to `go.mod` by this plan. Implementation tasks that need them are blocked on approval.

| Module | Kind | Used for | Why | Alternatives considered |
|---|---|---|---|---|
| `github.com/golang-jwt/jwt/v5` v5.3.1 | **New** runtime | Signing and verifying access JWTs (EdDSA) | ADR 0004 requires JWT. It is the most widely used Go JWT library, has **no transitive dependencies**, and supports an algorithm allowlist, `WithTimeFunc`, audience and issuer checks. Hand-rolled JWS parsing is a classic source of vulnerabilities. | `lestrrat-go/jwx/v3` (full JOSE suite, much larger, not needed for one issuer and one verifier); hand-rolled Ed25519 JWS on the standard library (~100 lines, but owning the parsing edge cases is riskier than the dependency); PASETO (contradicts ADR 0004, no `bearerFormat: JWT` fit) |
| `golang.org/x/crypto` (v0.58.0 already in go.sum; bump to v0.59.0) | Promote indirect to **direct** | `argon2.IDKey` | Argon2id is mandated by constitution VI and is not in the standard library. Maintained by the Go team. | `alexedwards/argon2id` (a thin wrapper, no release since 2023, adds a module) |
| `golang.org/x/term` (v0.47.0 already in go.sum) | Promote indirect to **direct** | `ReadPassword` with no echo in `divya admin create` | D-15 requires input without echo. The standard library cannot turn off terminal echo portably. Linked only into the CLI path. | Read from stdin only, with echo (violates D-15 on a TTY); shelling out to `stty -echo` (not portable, fragile) |
| `golang.org/x/text` (v0.44.0 already in go.sum) | Promote indirect to **direct** | `unicode/norm` NFC for passwords | D-07 accepts Gujarati and Hindi passphrases. Without normalisation, the same passphrase typed with a different input method can produce different bytes and fail to sign in. NIST SP 800-63B recommends normalisation. | No normalisation (real lockout risk for non-English passphrases); hand-rolled normalisation (not feasible) |
| `github.com/pquerna/otp` v1.5.0 | New runtime: **NOT recommended** | – | The plan implements RFC 6238 on the standard library (`crypto/hmac`, `crypto/sha1`, about 40 lines) and verifies it against the RFC test vectors. The library's `Validate` does not report which step matched, which replay protection needs, so its main helper would go unused. It also pulls in `boombuler/barcode` for QR images, which the server does not need (the panel draws the QR). | If the owner prefers a library, only `internal/auth/totp.go` changes. The tests (RFC vectors and replay) are identical either way. |
| `pgregory.net/rapid` v1.2.0 | **Test-only** (already in go.sum) | Property tests | Named by `.claude/rules/tests.md`. Not linked into the binary. Listed for transparency. | – |

## Risks and coordination

| ID | Risk | Mitigation |
|---|---|---|
| R-1 | Migration version collision or out-of-order merge with 002 | P-18: 003 uses `00003`, 002 uses `00002`; whichever merges later renumbers if needed; CI script `check-migration-order.sh` |
| R-2 | 002's `/internal/tick` operation must fit the fail-closed authz (otherwise `AssertDeclared` panics) | 002 registers its own scheme with `authz.Install` and is added to the contract-test allowlist. If 002 merges first, 003's PR adds `Security` to the tick operation while wiring authz. The conductor should pass this note to 002's architect. |
| R-3 | 002 and 003 both add `platform/db` transaction helpers or an audit writer | P-15 interface proposed for both; the first to merge owns it; the second rebases |
| R-4 | Cookie flow needs a shared registrable domain (D-21, 027) | Local Vite proxy; previews use a proxy or memory-only mobile mode (P-04); staging and production blocked on 027 for the admin panel only |
| R-5 | Strict reuse (D-10) signs users out after lost responses or parallel tabs | 022 and 024 must keep a single refresh in flight; note carried into their specs |
| R-6 | The Argon2 cost on Cloud Run is unverified (research §2) | Benchmark plus a staging measurement before production; `ARGON2_MAX_CONCURRENCY` and the parameters can be tuned without a migration (PHC plus rehash on login) |
| R-7 | `X-Forwarded-For` layout on Cloud Run is unverified | `TRUSTED_PROXY_HOPS` is configurable; affects only the audit IP and the per-IP limiter, never authorisation |
| R-8 | Blind-index key rotation is costly once 006 stores PAN hashes | 003 uses it only for ephemeral throttle subjects. 006 must store a key version alongside `*_hash` (note for 006). |
| R-9 | Auth operations without `Idempotency-Key` deviate from the letter of constitution V | Deviation 1 below; owner confirms with this plan |
| R-10 | `.env.example` and `.claude/rules/sql.md` cannot be edited by agents | Listed as owner actions in the report |
| R-11 | ADR number 0007 may collide with a parallel ADR from 002 | Rename at merge if needed (documentation only) |

## Complexity Tracking

| Violation | Why needed | Simpler alternative rejected because |
|---|---|---|
| **Deviation 1**: credential-issuing auth operations do not accept `Idempotency-Key` (constitution V "Mutations accept idempotency keys") | Replaying a stored response would re-deliver access tokens, refresh tokens or recovery codes to whoever holds the key. That is the grace window the spec forbids (D-10, spec note "Idempotency"). These operations already define replay safely: single-use credentials, and the safe retry for password change. Session-ending operations are naturally idempotent. | Accepting the key and replaying responses weakens D-10 and turns the idempotency store into a token store. Accepting the key and ignoring it is misleading. |
| **Deviation 2**: in `local` and `ci`, missing key environment variables produce ephemeral random keys instead of failing (constitution VI keeps secrets out of the repository) | Lets `make api`, tests and previews run without anyone handling secrets, and no key ever enters the repository or `.env` templates. Staging and production fail to start without real keys. | Committing development keys (violates "no secret ever enters the repository"); requiring every developer and agent to generate keys (agents may not write `.env*`) |
