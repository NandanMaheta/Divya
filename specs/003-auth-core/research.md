# Research: 003 Auth core

Date: 2026-11-13. Scope: Go `internal/auth` (users, Argon2id, login, access JWT + rotating device-bound refresh, lockout, password change, admin TOTP, Huma authn/authz middleware). Out of scope: OTP delivery (004), registration/approval (005), client screens (024). Business behaviour: BR-CLI-04 (login only after email OTP + mobile OTP + admin approval, so `status = active` is the login gate) and blueprint section 5 (5 failed attempts = 15-min lock, logout-all, admin revokes any device, forgot-password logs out other devices, admin = email + password + TOTP). BR-CLI-05 (contact change) belongs to 007, not here.

Items marked **[unverified]** could not be fetched in this sandbox (hosts blocked) and need a re-check before the plan is final. A host that is not on the allowlist: `huma.rocks`, `cheatsheetseries.owasp.org`, `www.rfc-editor.org`, `docs.cloud.google.com`.

## 1. Versions to use (checked on proxy.golang.org, 2026-11-13)

| Module | Latest | Date | Status in repo |
|---|---|---|---|
| github.com/danielgtaylor/huma/v2 | v2.40.0 | 2026-10-27 | direct, already pinned |
| golang.org/x/crypto (argon2) | v0.59.0 | 2026-11-11 | indirect at v0.58.0; becomes direct |
| github.com/golang-jwt/jwt/v5 | v5.3.1 | 2026-01-28 | NEW |
| github.com/pquerna/otp | v1.5.0 | 2024-12-31 | NEW |
| github.com/alexedwards/argon2id | v1.0.0 | 2023-10-21 | not recommended (see 2) |
| github.com/lestrrat-go/jwx/v3 | v3.4.0 | 2026-10-23 | rejected |
| github.com/tink-crypto/tink-go/v2 | v2.9.0 | 2026-10-15 | rejected |
| github.com/nyaruka/phonenumbers | v1.8.1 | 2026-07-15 | rejected (see 7) |

New runtime dependencies (`golang-jwt/jwt/v5`, `pquerna/otp`, promoting `x/crypto`) need owner approval per CLAUDE.md. Everything else is stdlib.

## 2. Argon2id

- OWASP minimum: **m=19456 KiB (19 MiB), t=2, p=1**. Equivalent alternatives: m=47104/t=1/p=1, m=12288/t=3, m=9216/t=4, m=7168/t=5. OWASP says to benchmark on the target and that raising parallelism does not guarantee slower guessing. A pepper is optional defence in depth, kept outside the DB (secrets vault); changing it forces resets. [OWASP Password Storage](https://raw.githubusercontent.com/OWASP/CheatSheetSeries/master/cheatsheets/Password_Storage_Cheat_Sheet.md) (rendered at cheatsheetseries.owasp.org).
- **Recommend**: m=19456, t=2, p=1, 16-byte salt, 32-byte key via `argon2.IDKey`. Start here, then benchmark on Cloud Run (the target of 300 ms is a ceiling; this setting normally lands far below that on a shared vCPU, **[unverified]**, measure it in a `-run Benchmark` test and in staging). Store params in the PHC string so they can be raised later with rehash-on-login.
- **Library**: use `golang.org/x/crypto/argon2` directly plus about 60 lines of PHC encode/decode (`$argon2id$v=19$m=..,t=..,p=..$salt$hash`) with `subtle.ConstantTimeCompare`. Rationale: x/crypto is already in go.sum, so no extra dependency, and the PHC format is the same one alexedwards/argon2id uses ([README](https://raw.githubusercontent.com/alexedwards/argon2id/master/README.md)). Rejected: `alexedwards/argon2id` v1.0.0 works, but has had no release since 2023 and it adds a dependency for a trivial wrapper.
- Pitfalls:
  - Memory: each concurrent hash allocates 19 MiB. Cap concurrency with a semaphore (for example 4) so a login burst cannot OOM a small Cloud Run instance. Size the container memory accordingly.
  - Unknown user: run a dummy verify against a fixed hash so response time does not reveal whether an account exists, and return one generic error for wrong user, wrong password, locked, disabled and pending. [OWASP Authentication CS](https://raw.githubusercontent.com/OWASP/CheatSheetSeries/master/cheatsheets/Authentication_Cheat_Sheet.md)
  - Password policy: allow at least 64 chars, do not truncate, enforce a hard cap (for example 256 bytes) against hash DoS. NIST via OWASP: under 8 chars is weak with MFA, under 15 without MFA. Clients have no MFA, so the minimum length is an owner decision (suggest 10, no composition rules).
  - Cost: CPU time only. Cloud Run bills request-based CPU, so do hashing inside the request, never in background goroutines **[unverified, docs.cloud.google.com blocked]**.

## 3. Access token format

**Recommend: JWT (as ADR 0004), `golang-jwt/jwt/v5` v5.3.1, EdDSA (Ed25519) signing**, 15-min lifetime.

- Claims: `iss`, `aud` (single API audience), `sub` (user id), `sid` (session/device-session id), `role`, `iat`, `exp`, `jti`. Verify with `jwt.WithValidMethods([]string{"EdDSA"})`, `WithAudience`, `WithIssuer`, `WithLeeway(30*time.Second)` for clock skew, `WithExpirationRequired()`. ASVS 5.0 V9: signature before trust (9.1.1), algorithm allowlist excluding `none` (9.1.2), keys only from pre-configured sources, never from `jku`/`jwk` headers (9.1.3), `exp`/`nbf` (9.2.1), `aud` allowlist (9.2.3). [ASVS V9](https://raw.githubusercontent.com/OWASP/ASVS/master/5.0/en/0x18-V9-Self-contained-Tokens.md)
- **Key rotation**: key ring in config/Secret Manager: one active private key plus N previous public keys, each with `kid`. Sign with active; on verify select by `kid`, reject unknown kid. Rotate by adding a new key as active and keeping the old public key for 15 min plus leeway. Single API means no JWKS endpoint is needed.
- **Revocation (important)**: ASVS 7.4.1 requires that a terminated session stops working; for self-contained tokens it names a terminated-token list, a per-user "issued-before" cutoff, or per-user key rotation. [ASVS V7](https://raw.githubusercontent.com/OWASP/ASVS/master/5.0/en/0x16-V7-Session-Management.md) Recommend the middleware does one primary-key lookup on `sid` (session row plus user status/role) per authenticated request. With about 100 users that is negligible and makes logout, revoke-device, disable-user and role change immediate. Without it, revocation lags up to 15 min. This makes the JWT mostly a signed carrier of `sid`; say so in the plan so the owner can choose lookup-per-request (recommended) or pure stateless with a 15-min lag (admin routes should always look up).
- Rejected: **PASETO v4** (safer defaults, but a small ecosystem, no Huma/OpenAPI `bearerFormat: JWT` fit, and no benefit once algorithm is pinned). **Opaque tokens only** (simplest and instantly revocable; contradicts accepted ADR 0004, so not chosen unless the owner reopens it). **lestrrat-go/jwx v3.4.0**: full JOSE/JWKS suite, larger than needed for one issuer and one verifier.

## 4. Refresh tokens, rotation, reuse detection, device binding

- Token: 32 random bytes (CSPRNG, 256 bits; ASVS 7.2.3 needs at least 128), base64url. Store only `SHA-256(token)` (a fast hash is right for high-entropy tokens; Argon2 is unnecessary).
- Tables (new goose migration `00002_auth.sql`, owner approval needed for auth schema): `users`, `devices` (user_id, device_id supplied by client, name, platform, created/last_seen, revoked_at), `refresh_tokens` (id, family_id = device session, token_hash unique, parent_id, issued_at, expires_at, used_at, replaced_by). Add `org_id` on each (SQL rules) and an `audit_log` row in the same tx for every mutation (API rules).
- Rotate in one transaction: `SELECT ... FOR UPDATE` the row by hash, check not expired, device not revoked and user active; if `used_at IS NULL` mark used and insert the child; if `used_at` is set (**reuse**) revoke the entire family/device session, audit `auth.refresh_reuse`, return the generic 401. Concurrency: the row lock serialises two parallel refreshes, so the loser is treated as reuse. Pitfall: a mobile client that loses the response to a refresh and retries will trip reuse detection and be logged out. Mitigations: client persists the new token before using it; optionally a short grace window (for example 5 to 10 s) that only returns 401 without killing the family. Decide in spec (suggest strict, no grace, at first).
- Lifetimes (owner decision): idle timeout and absolute maximum (for example 30 days idle / 90 days absolute); ASVS 7.1.1, 7.3.1, 7.3.2 require these to be documented and enforced. [ASVS V7](https://raw.githubusercontent.com/OWASP/ASVS/master/5.0/en/0x16-V7-Session-Management.md)
- Required operations map directly to ASVS V7: new token on (re)authentication (7.2.4); logout and logout-all; revoke any device incl. admin revoking a client's devices (7.4.5, 7.5.2); on password change/reset terminate all other sessions (7.4.3); on user disable terminate all (7.4.2); re-authenticate with password before changing sensitive data such as password or MFA (7.5.1).
- **Device binding**: baseline is a client-generated random `device_id` (UUID stored in expo-secure-store next to the refresh token) bound to the token family, with name/platform/last IP shown in the device list. The refresh request must present both token and device_id; mismatch means revoke family. Optional hardening (proof-of-possession with a device keypair, DPoP-style) needs a hardware-backed key API in the Expo client; I could not verify Expo support, so treat as a later feature, not 003. Biometric unlock is purely client side (feature 024) and the API never sees it. RFC 9700 (OAuth security BCP) endorses either sender-constraining or rotation with reuse detection for public-client refresh tokens **[unverified, rfc-editor.org blocked]**.

### Admin browser SPA (Cloudflare Pages) calling API (Cloud Run)

- Recommended: access token held **in memory only**; refresh token in an **httpOnly, Secure, SameSite=Strict cookie** scoped to `Path=/v1/auth` on the API host. Page reload calls refresh to get a new access token. This keeps tokens out of JS-readable storage (XSS cannot steal the refresh token) and the access token goes in the `Authorization` header, which is not sent automatically, so normal API calls are not CSRF-able.
- Cross-origin caveat: SameSite is about *site* (registrable domain), not origin. Cookies work with `SameSite=Strict` only if SPA and API share a registrable domain (for example `admin.example.com` and `api.example.com`). If the SPA stays on `*.pages.dev` and the API on `*.run.app` the cookie is third-party and will be blocked or need `SameSite=None`; require custom domains on one parent domain (put in the spec as an infra prerequisite).
- CSRF on cookie endpoints (`/refresh`, `/logout`): (1) exact-origin CORS allowlist with credentials, never `*`, no regex over subdomains; (2) require a custom header (for example `X-Requested-With`) so a cross-site request needs preflight; (3) verify `Origin` equals the configured admin origin, and reject missing or `null` origins; (4) accept only `application/json`. The OWASP CSRF cheat sheet states SameSite is defence in depth only, sibling subdomains count as same-site, and preflight is not a complete defence. [OWASP CSRF](https://raw.githubusercontent.com/OWASP/CheatSheetSeries/master/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.md)
- API design consequence: the login/refresh request carries `client_type` (`web` or `mobile`). For `web` the refresh token is only set as a cookie and never returned in the body; for `mobile` it is returned in the body and read from the body on refresh.
- Rejected: refresh token in localStorage or sessionStorage (XSS-readable); full session cookie for every API call (needs CSRF tokens on every mutation and does not suit the shared mobile API).
- Cloudflare Access sits in front of the admin SPA only; the API on Cloud Run is called directly, so the API must not rely on Access for its own authorisation. Optionally validating the Access JWT header on admin routes would add a layer **[unverified]**; not in 003.

## 5. Admin TOTP

- `github.com/pquerna/otp` v1.5.0 (RFC 6238 TOTP, RFC 4226 HOTP, QR code generation; README says store the secret server side, gives nothing on encryption, skew or replay). [README](https://raw.githubusercontent.com/pquerna/otp/master/README.md) Last release Dec 2024: stable but quiet, which is acceptable because TOTP is a frozen standard. Fallback if ever abandoned: about 40 lines of stdlib `crypto/hmac` + `crypto/sha1`.
- Use defaults for authenticator-app compatibility: SHA1, 6 digits, 30 s period, skew of 1 step. The library does not prevent replay, so store `last_used_step` per user and reject any code whose step is not greater.
- Secret: 20 bytes random, encrypted with AES-256-GCM (section 7) with AAD = user id, stored `totp_secret_enc`. Enrolment is pending until the first valid code confirms it; only then set `totp_enabled_at`.
- Recovery codes: 10 codes, at least 80 bits random each (for example 16 base32 chars), shown once, stored as hashes (SHA-256 or HMAC), single use, usable once in place of TOTP; regenerating invalidates the old set; audit each use.
- Login flow for admin: password OK, then issue a short-lived (5 min), single-purpose `mfa_pending` token (separate `aud`/`typ`, not accepted as an access token), then `POST /auth/mfa` with the code yields the real session. TOTP attempts use the same lockout counters (section 6). Admin accounts are created by an operator command (for example `divya admin create`, with enrolment on first login), since registration (005) is for clients only.
- ASVS 7.5.1: changing MFA settings requires re-authentication.

## 6. Lockout and rate limiting without Redis

- Cloud Run can run several instances, so in-memory limiters are not shared. Use PostgreSQL counters with atomic upserts.
- Account lockout per blueprint: 5 consecutive failures lock for 15 min. Implement as `UPDATE users SET failed_attempts = failed_attempts + 1, locked_until = CASE WHEN failed_attempts + 1 >= 5 THEN now() + interval '15 minutes' END ... RETURNING` in one statement; reset on success. Counter is per account (not per IP), as OWASP advises; the same guide warns lockout can be abused as DoS and suggests the forgot-password flow as the unlock path. [OWASP Authentication CS](https://raw.githubusercontent.com/OWASP/CheatSheetSeries/master/cheatsheets/Authentication_Cheat_Sheet.md)
- Unknown identifiers: count failures in a `rate_limits(key_hash, window_start, count)` table keyed by HMAC of the normalised identifier so lock behaviour is indistinguishable for nonexistent accounts (anti-enumeration), plus a coarse per-IP bucket (use the client IP from `X-Forwarded-For`, taking the entry added by Google's front end, not the first value, **[unverified]**).
- Locked response must look like a normal failure externally (same status and body); optionally add `Retry-After`.
- Housekeeping: delete expired counters from `/internal/tick` (existing scheduled-work design), not from goroutines.
- Same table design will be reused by 004 for OTP rate limits; keep it generic (`internal/auth` exports an interface, or place in platform without business imports).
- Rejected: Redis/Memorystore (cost and ops, violates the Rs 0 budget); in-memory `x/time/rate` alone (per-instance only; still fine as a cheap secondary per-IP guard).

## 7. Huma v2 (v2.40.0): security schemes, middleware, errors

Verified against the module source in `/root/go/pkg/mod/github.com/danielgtaylor/huma/v2@v2.40.0` and Huma docs on GitHub.

- Declare the scheme once on the config (OpenAPI `components.securitySchemes`): `cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{"bearer": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"}}`. The repo builds the config in `httpx.NewAPI` (`apps/api/internal/platform/httpx/httpx.go`), so add it there; platform stays free of business imports. [Huma OpenAPI docs](https://raw.githubusercontent.com/danielgtaylor/huma/main/docs/docs/features/openapi-generation.md)
- Mark operations with `huma.Operation.Security = []map[string][]string{{"bearer": {}}}`; this feeds the OpenAPI doc so the generated `@divya/api-client` knows which calls need a token. Roles can be expressed as scopes (`{"bearer": {"admin"}}`) or via a helper.
- Middleware: signature `func(ctx huma.Context, next func(huma.Context))`; global via `api.UseMiddleware(...)` (must be called before registering operations) or per operation via `huma.Operation.Middlewares`. Put a value on the context with `huma.WithValue(ctx, key, v)` and read it in handlers with `ctx.Value(key)`; abort with `huma.WriteErr(api, ctx, status, msg, errs...)`, which respects content negotiation. [Huma middleware docs](https://raw.githubusercontent.com/danielgtaylor/huma/main/docs/docs/features/middleware.md) `huma.Context.Operation()` (api.go:71), `WithValue` (api.go:173) and `WriteErr` (error.go:272) exist in v2.40.0.
- **Recommended design**: one global middleware created by `auth.NewMiddleware(api, svc)`. It reads `ctx.Operation().Security`; if empty it calls `next`; otherwise it parses the bearer token, checks the session, checks role against the scheme scopes (403 on mismatch), stores a `Principal{UserID, Role, SessionID}` with `huma.WithValue`, and exposes `auth.PrincipalFrom(ctx)` for handlers. Fail-closed convention: other modules call a helper `auth.Secured(op, roles...)` so nobody has to remember both Security and role. Global approach is safer than per-operation middlewares because forgetting one fails closed only if the middleware default denies unlisted routes; add a test that iterates `api.OpenAPI().Paths` and asserts every non-allowlisted operation has `Security`.
- Errors: 401 with `WWW-Authenticate: Bearer` header, 403 for role, 423/429 avoided for login (use generic 401 to prevent enumeration). Huma's default error model is RFC 9457 `application/problem+json`, as already used in the repo. (RFC 9457 text itself could not be fetched, **[unverified]**.)
- Pitfall: `cmd/divya/main.go` calls `buildAPI(nil)` for `divya openapi` with no DB, so `auth.Register` and the middleware must construct without live dependencies (inject interfaces, as `health` does).
- Auth-endpoint handlers that set cookies need `Set-Cookie` header fields in the output struct (`SetCookie http.Cookie` with `header:"Set-Cookie"`) and the cookie in input via `cookie:"..."` tags. I did not fetch docs for this; check Huma source/tests when planning.

## 8. Identifiers: normalisation, uniqueness, PII at rest

- **Email**: trim, parse with `net/mail`, lowercase the whole address (pragmatic; case-sensitive local parts are practically unused), reject display names. Store `email_norm` (lookup) and unique index.
- **Mobile**: Indian users only, so normalise with a small stdlib function to E.164 `+91XXXXXXXXXX` (accept 10 digits, leading 0, `91`, `+91`; require first digit 6 to 9, length 10). Rejected: `nyaruka/phonenumbers` v1.8.1 (a large metadata-heavy port of libphonenumber, overkill and adds a dependency; revisit if international numbers are ever allowed). Firebase returns E.164, so 004 maps cleanly.
- Uniqueness: unique indexes on the normalised/blind value, partial `WHERE status <> 'deleted'` if soft-deletes exist. Race-safe by constraint, mapped to a 409 (or a generic reply if enumeration matters; registration flow is 005's call).
- Encryption (constitution VI: PAN and "other identifiers" encrypted, key outside DB, blind index): **stdlib `crypto/aes` + `cipher.NewGCM`**, random 12-byte nonce per value, envelope `version | key_id | nonce | ciphertext`, AAD = `table.column.row_id` to prevent value swapping, 32-byte keys from env/Secret Manager in a key ring (rotate by adding a key and re-encrypting lazily). Blind index = `HMAC-SHA256(hmac_key, normalised_value)` with a **separate** key (derive both from one master via HKDF, or hold two secrets). HMAC is deterministic so equality lookup and unique index work, but it leaks equality and enables dictionary attack if the key leaks, so low-entropy values (mobile numbers: only ~4 billion possibilities) depend entirely on the key staying secret. Column naming per SQL rules: `*_enc bytea`, `*_hash bytea`.
- Rejected: **tink-go v2.9.0**: gives keysets and rotation primitives and misuse-resistant APIs but is a heavy dependency with its own keyset format and learning curve; stdlib covers one AEAD and one MAC in about 100 lines. Reconsider if multi-key envelope encryption grows.
- **Open question for owner**: constitution VI mandates encryption for PAN and "other identifiers" but 003 needs email/mobile for login lookup and for sending email. Do email and mobile get encrypted with blind index (needs decrypt to send mail), or stay plaintext with unique index while only PAN (feature 006) is encrypted? Recommend: build the crypto helper in 003 (reusable by 006) and encrypt contact data too, unless the owner rules otherwise.

## 9. Existing code to reuse

- Golden module: `/home/user/divya-003/apps/api/internal/health/` (`health.go` service with injected clock and small interfaces, `handler.go` `Register(api, svc)`, `health_test.go`). Copy shape: `service.go`, `handler.go`, tests with `humatest`.
- Wiring: `/home/user/divya-003/apps/api/cmd/divya/main.go` `buildAPI` (add `auth.Register`, middleware, and nil-safe construction); `serve` already handles graceful shutdown.
- `/home/user/divya-003/apps/api/internal/platform/httpx/httpx.go` (`NewAPI`: add security scheme and CORS; `NewServer`; `logRequests` logs only method, path and status, so keep tokens and request bodies out of logs).
- `/home/user/divya-003/apps/api/internal/platform/config/config.go` (add env: JWT key ring, PII encryption and HMAC keys, admin origin, cookie domain; validate required in staging/production; never log values).
- `/home/user/divya-003/apps/api/internal/platform/db/`, `dbgen/`, and `/home/user/divya-003/apps/api/internal/platform/dbtest/dbtest.go` (`dbtest.New(t)` for integration tests on real PostgreSQL 18).
- `/home/user/divya-003/apps/api/migrations/00001_platform.sql`: `orgs`, `audit_log` (actor_kind admin/client/system, org_id required), `forbid_mutation()` trigger (reuse for an append-only `auth_events` table if added), `uuidv7()` default. Next migration `00002_auth.sql`. Queries go in `apps/api/queries/*.sql` for sqlc (`sqlc.yaml` exists).
- Test naming must include rule IDs (for example `TestLogin_BR_CLI_04_PendingApprovalCannotLogin`).

## 10. Risks, limits, and decisions for the owner

1. New dependencies: `golang-jwt/jwt/v5`, `pquerna/otp`, promote `x/crypto` (CLAUDE.md requires owner approval).
2. Auth schema and `internal/auth` PRs need owner approval plus security review (ADR 0004, constitution workflow).
3. Per-request session lookup (recommended) vs stateless JWT with 15-min revocation lag.
4. Idle and absolute refresh lifetimes; password minimum length; strict reuse vs short grace.
5. Admin and API on a shared registrable domain (infra prerequisite for the cookie design).
6. Encrypt email/mobile at rest or only PAN.
7. Admin bootstrap command and TOTP enrolment UX.
8. Boundary to 004/005: 003 exposes service methods (create user in `pending_*` states, set status, mark contact verified, `RevokeAllSessions(user)` for password reset) but no OTP or registration endpoints.
9. Free-tier: everything is Postgres and CPU on existing infrastructure; no extra service cost. Argon2 memory and concurrency cap must fit the Cloud Run instance size.
10. iPhone PWA: the refresh token in the PWA must live in browser storage because expo-secure-store is not available on the web target; this is a weaker store than the Android secure store. I could not verify how feature 024 handles this. Raise it in 024's spec (it may argue for using the cookie flow for the PWA).

## Sources

- OWASP Password Storage CS: https://raw.githubusercontent.com/OWASP/CheatSheetSeries/master/cheatsheets/Password_Storage_Cheat_Sheet.md
- OWASP Authentication CS: https://raw.githubusercontent.com/OWASP/CheatSheetSeries/master/cheatsheets/Authentication_Cheat_Sheet.md
- OWASP CSRF Prevention CS: https://raw.githubusercontent.com/OWASP/CheatSheetSeries/master/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.md
- OWASP ASVS 5.0 V7 Session Management: https://raw.githubusercontent.com/OWASP/ASVS/master/5.0/en/0x16-V7-Session-Management.md
- OWASP ASVS 5.0 V9 Self-contained Tokens: https://raw.githubusercontent.com/OWASP/ASVS/master/5.0/en/0x18-V9-Self-contained-Tokens.md
- Huma middleware docs: https://raw.githubusercontent.com/danielgtaylor/huma/main/docs/docs/features/middleware.md
- Huma OpenAPI/security docs: https://raw.githubusercontent.com/danielgtaylor/huma/main/docs/docs/features/openapi-generation.md
- Huma v2.40.0 source (local module cache): api.go, error.go, huma.go
- pquerna/otp README: https://raw.githubusercontent.com/pquerna/otp/master/README.md
- alexedwards/argon2id README: https://raw.githubusercontent.com/alexedwards/argon2id/master/README.md
- Versions: https://proxy.golang.org/github.com/golang-jwt/jwt/v5/@latest, https://proxy.golang.org/github.com/pquerna/otp/@latest, https://proxy.golang.org/golang.org/x/crypto/@latest, https://proxy.golang.org/github.com/alexedwards/argon2id/@latest, https://proxy.golang.org/github.com/danielgtaylor/huma/v2/@latest, https://proxy.golang.org/github.com/tink-crypto/tink-go/v2/@latest, https://proxy.golang.org/github.com/nyaruka/phonenumbers/@latest, https://proxy.golang.org/github.com/lestrrat-go/jwx/v3/@latest
- Repo docs: `/home/user/divya-003/docs/adr/0004-auth.md`, `/home/user/divya-003/docs/product/product-blueprint.md` section 5, `/home/user/divya-003/docs/product/business-rules.md` BR-CLI-04/05, `/home/user/divya-003/.specify/memory/constitution.md` principle VI

## 11. Plan-stage decisions (Phase 0 of /speckit-plan, 2026-11-13)

Inputs: owner approval (Option A, sessions 90d/30d and 7d/12h, D-01…D-22). Section 10's open items are resolved below. Each item gives Decision, Rationale and Alternatives. The detail lives in `plan.md` (P-xx).

| # | Topic | Decision | Rationale | Alternatives rejected |
|---|---|---|---|---|
| 11.1 | Access credential (§3, §10.3) | JWT EdDSA, 15 min, `kid` ring, **plus a primary-key session lookup on every request** (P-02) | D-05 requires revocation on the next request. About 1–3 ms per request at this scale. | Stateless JWT (up to 15 min revocation lag, violates D-05); opaque-only tokens (contradicts ADR 0004) |
| 11.2 | JWT leeway | 0 s, with `WithTimeFunc(clock)` | Issuer and verifier are the same service; SC-004 needs exact boundaries | 30 s leeway (blurs fake-clock boundary tests) |
| 11.3 | Refresh (§4, §10.4) | Opaque `drt_` + 256-bit, SHA-256 at rest, one transaction, lock order users → device_sessions → refresh_tokens, strict reuse (D-10), no grace, no idempotent replay (P-03, P-16) | Spec D-10 and D-11; the row locks serialise concurrent refreshes deterministically | Grace window (the spec forbids it) |
| 11.4 | Admin web transport (§4, §10.5) | `client_type=web`: refresh only in a `__Secure-divya_rt` cookie (HttpOnly, Secure, SameSite=Strict, Path=/v1/auth/refresh, host-only), access token in memory; Origin allowlist + `X-Divya-CSRF` + JSON-only + exact CORS (P-04) | OWASP CSRF defence in depth; XSS cannot read the refresh credential | localStorage (readable by XSS); a cookie for every call (CSRF on every mutation) |
| 11.5 | TOTP library (§5) | Standard library RFC 6238 (about 40 lines), RFC vectors in tests; `pquerna/otp` not recommended (P-05) | Replay protection needs to know the matched step, which `pquerna/otp`'s `Validate` does not return; avoids `boombuler/barcode` | pquerna/otp (available as a drop-in if the owner prefers) |
| 11.6 | Interim proof (§5) | Opaque `dmc_` token, SHA-256 in `mfa_challenges`, purpose `verify` or `enrol`, 5 min, single use | Single use needs database state anyway; an opaque token can never be mistaken for an access JWT (FR-035) | Separate-audience JWT (still needs a database row for single use) |
| 11.7 | Lockout (§6) | A sliding window exactly as D-08, implemented as a pure `Apply` function plus `auth_throttles` rows with an HMAC subject; per account (user subject) when an account matches, per identifier otherwise; the counter is cleared only after the full sign-in, including the second factor (P-07) | D-08 says "any 15-minute window", and FR-045 says the lock is per account; clearing at the password step would allow unlimited TOTP guesses | Consecutive-failures counter on `users` (§6 sketch; does not match "any 15-minute window" and has no row for unknown identifiers) |
| 11.8 | Per-IP limiting (§6) | In-memory per-instance fixed window, 30/min/IP, public credential operations only (P-17) | Cheap defence against Argon2 CPU exhaustion; reveals nothing about accounts | `x/time/rate` (a new dependency for about 40 lines); a Postgres-backed per-IP limiter (not needed at this scale) |
| 11.9 | Client IP (§6 unverified) | `TRUSTED_PROXY_HOPS` config; take the `X-Forwarded-For` entry at `len-hops`; used only for audit and the limiter | The Cloud Run layout is unverified; making it configurable keeps authorisation independent of it | Trusting the first `X-Forwarded-For` entry (spoofable) |
| 11.10 | Contact data (§8, §10.6) | Readable and normalised; **ADR 0007** records owner Option A | Owner decision | Encrypted with blind index (Option B) |
| 11.11 | Crypto helpers (§8) | `internal/platform/crypto`: AES-256-GCM envelope `0x01 \| key_id uint16 \| nonce12 \| ct+tag`, AAD bound to table.column:row; HMAC-SHA-256 blind index with purpose separation; separate keys from env (P-14) | Standard library only; reusable by 006 | tink-go (heavy); HKDF from one master key (two secrets are simpler to reason about) |
| 11.12 | Password normalisation | NFC via `golang.org/x/text/unicode/norm` (promotion needs approval) | Gujarati and Hindi passphrases (D-07) must hash the same from different input methods | No normalisation |
| 11.13 | Argon2 cost (§2 unverified) | m=19456 KiB, t=2, p=1; semaphore 4; benchmark in CI plus a staging measurement; tunable through PHC rehash on login | The OWASP minimum; cost on Cloud Run is still to be measured (risk R-6) | m=47104/t=1 (more memory per hash under the cap) |
| 11.14 | Huma body echo (new finding) | The global `huma.NewError` override drops `ErrorDetail.Value` | Huma v2.40.0 `validateBody` puts `Value: string(body)` into parse errors, which would return passwords and tokens in 400 responses (FR-091) | Per-operation custom error handling (easy to forget) |
| 11.15 | Error codes (FR-093) | `ErrorModel.code` string + `type: urn:divya:problem:<code>`; the catalogue is in contracts/error-codes.md | Stable i18n keys; an additive OpenAPI change | `enum` on `code` (later additions would be breaking for oasdiff) |
| 11.16 | Migration number (§9, coordination with 002) | `00003_auth.sql`; rule: first migration = feature number; later-merging PRs renumber above main's highest; CI guard (P-18) | goose rejects out-of-order versions; avoids collision with 002 being planned in parallel | Timestamp versions (still out-of-order on a late merge; breaks the existing `NNNNN` convention) |
| 11.17 | Bootstrap (§5, §10.7) | `divya admin create` with an `x/term` no-echo prompt or a stdin pipe; advisory lock; creates the org if none exists; DB-only config (P-13) | D-15; no organisation is seeded by 001 | Password flag or env var (leaks into shell history or process environment) |
| 11.18 | Scheduler and outbox | None in 003; purge job is a follow-up once 002 lands (P-20) | 003 depends only on 001 | – |
| 11.19 | iPhone PWA transport (§10.10) | Both transports exist; 024 decides | Spec edge case says 024 decides | – |

Still unverified, carried as plan risks: Argon2 timing on Cloud Run (R-6), the `X-Forwarded-For` layout on Cloud Run (R-7), and the depguard longest-match semantics (P-19 uses explicit lists instead).
