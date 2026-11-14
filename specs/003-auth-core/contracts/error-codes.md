# Problem codes (FR-093)

Every non-2xx response is RFC 9457 `application/problem+json` with these fields:

- `code`: stable and language-neutral; UIs translate it.
- `type`: `urn:swastik:problem:<code>`.
- `title`, `status` and `detail`: English, for developers only.
- `errors[]`: never contains `value` (plan P-10).

The UI wording comes from `packages/domain/src/auth-error-codes.ts` (English first; Gujarati and Hindi added by 022 and 024). Adding a code is non-breaking. Renaming or removing one is breaking.

## Platform (all features)

| code | HTTP | When |
|---|---|---|
| bad_request | 400 | Unparseable body (no echo of the body) |
| validation_failed | 422 | Schema validation failed (fields in `errors[].location`) |
| not_found | 404 | Route or resource not found (generic) |
| unsupported_media_type | 415 | Body content type is not JSON |
| rate_limited | 429 | Per-IP limiter on public credential operations |
| internal | 500 | Unexpected error (no details) |
| platform.security_undeclared | 500 | An operation (documented or hidden) registered without `Security`. Documented operations are caught at startup by `authz.AssertDeclared`; this response is the fail-closed backstop |

## Auth (003)

| code | HTTP | When | Spec |
|---|---|---|---|
| auth.invalid_credentials | 401 | **Generic sign-in failure.** Wrong identifier, wrong password, locked, `pending_verification`, `pending_approval`, `disabled`, admin using a mobile number, wrong TOTP or recovery code at sign-in. Identical body every time. | D-09, FR-021, US-1, US-4, US-5 |
| auth.identifier_invalid | 422 | The identifier is neither a valid email nor a valid Indian mobile number | D-09, US-1 s11 |
| auth.unauthenticated | 401 | Missing, invalid, expired or tampered access token; revoked or expired session; user not active; refresh token or challenge used as an access token; invalid, expired, used or wrong-purpose challenge. Sent with `WWW-Authenticate: Bearer`. | FR-031, FR-035, US-2 |
| auth.forbidden | 403 | The caller's role is not allowed by the operation's declared roles | FR-032, US-2 s3, US-8 s7 |
| auth.refresh_rejected | 401 | Every refresh failure: unknown, used (reuse), device mismatch, transport mismatch, expired, session ended, user not active. One code, so reuse detection is not revealed. | FR-040…043, US-3 |
| auth.csrf_rejected | 403 | Cookie refresh, or an operation that sets the refresh cookie (`client_type=web` sign-in, mfa-verify, enrolment-confirm), with a missing, `null` or non-allowlisted Origin, without `X-Swastik-CSRF: 1`, or with a non-JSON content type | Plan P-04 |
| auth.reauth_failed | 403 | Wrong current password, TOTP code or recovery code on change-password, recovery-code regeneration or authenticator replacement; also returned while the account is locked | D-08, FR-070, US-7 s2 |
| auth.password_too_short | 422 | Fewer than 10 characters (after NFC) | D-07, US-7 s3 |
| auth.password_too_long | 422 | More than 256 characters | D-07, US-7 s6 |
| auth.password_matches_identifier | 422 | Equals the user's own email or mobile | D-07, US-7 s5 |
| auth.password_too_common | 422 | On the 10k common list (case-insensitive) | D-07, US-7 s4 |
| auth.password_unchanged | 422 | The new password equals the current one | D-07, US-7 s5 |
| auth.device_not_found | 404 | Session not found, already ended, or owned by another user (no difference revealed) | FR-048, US-6 s5 |
| auth.user_not_found | 404 | Admin operation on a user who does not exist or belongs to another org | FR-049 |
| auth.mfa_no_pending_secret | 409 | Enrolment or replacement confirm without a pending secret | P-05 |
| auth.busy | 503 | Argon2 concurrency queue wait timed out (independent of the account) | P-06 |
| auth.not_configured | 503 | No organisation exists yet (run `swastik admin create`) | P-12 |

## Go capability errors (not HTTP; for 004/005/007 to map)

| Sentinel | code (for callers that surface it) | When |
|---|---|---|
| `auth.ErrEmailTaken` | auth.email_taken | Duplicate normalised email (FR-004) |
| `auth.ErrMobileTaken` | auth.mobile_taken | Duplicate normalised mobile (FR-004) |
| `auth.ErrInvalidTransition` | auth.status_transition_invalid | For example approving a `pending_verification` user (US-10 s4) |
| `auth.ErrLastAdmin` | auth.last_admin | Disabling the last active admin (D-19) |
| `auth.ErrUserNotFound` | auth.user_not_found | Unknown user id |
| `*auth.PolicyError{Code}` | auth.password_* | Password policy failure on create or reset |
| `auth.ErrInvalidIdentifier` | auth.identifier_invalid | Email or mobile does not normalise |

## Bootstrap CLI (`swastik admin create`; exit status 1, code printed on stderr)

| code | When |
|---|---|
| bootstrap.admin_exists | An admin already exists in the org (US-9 s2) |
| bootstrap.org_ambiguous | More than one org exists and `--org-id` was not given |
| bootstrap.password_mismatch | The two TTY entries differ |
| bootstrap.no_password | Empty stdin or prompt |
| auth.email_taken / auth.mobile_taken | Contact data already used (US-9 s4) |
| auth.password_* | Policy failure, naming the rule (US-9 s3) |
| auth.identifier_invalid | Bad email or mobile |
