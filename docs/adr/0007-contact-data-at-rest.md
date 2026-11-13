# ADR 0007: Contact details stay readable; government and financial identifiers are encrypted

- Status: Accepted (2026-11-13, owner decision "Option A" in `specs/003-auth-core/spec.md`, Owner approval)
- Type: Clarification of constitution principle VI (no change to its wording or intent)
- Related: ADR 0004 (auth), feature 003 (auth core), feature 006 (family accounts and PAN), `.claude/rules/sql.md`

## Context

Constitution VI says: "PAN and other identifiers are encrypted at rest (AES-GCM, key outside the database) with a blind index for lookup, and masked in every UI". `.claude/rules/sql.md` says: "PAN and similar identifiers: store `*_enc bytea` + `*_hash bytea` (blind index); never plaintext".

Feature 003 stores each user's email and mobile number. They are needed in readable form:

- Sign-in looks a user up by email or mobile (D-04), with case and format normalisation (D-16, D-17).
- Features 004 (OTP), 013 and 014 (reminders, email fallback) send messages to them.
- The admin sees them on the Clients page and searches by them.

Encrypting them with a blind index would still allow exact lookup, but every sender would have to decrypt, the admin search could not do partial matching, and a 10-digit Indian mobile number has only about 4 x 10^9 possible values, so its blind index protects it only as long as the HMAC key stays secret. The protection gained is small compared with the extra moving parts.

The phrase "other identifiers" was ambiguous, so the spec asked the owner. The owner chose Option A.

## Decision

1. In constitution VI, "PAN and other identifiers" means **government-issued and financial account identifiers**: PAN, and any later identifier of the same kind (for example Aadhaar, bank account numbers, demat or BO IDs) if a future feature ever stores one. These are always stored as `*_enc bytea` (AES-256-GCM envelope, key outside the database) plus `*_hash bytea` (HMAC-SHA-256 blind index), never in plaintext, and masked in every UI.
2. **Contact details** (email address, mobile number) are not "identifiers" in that sense. They are stored normalised and readable (`users.email`, `users.mobile`) with unique indexes, so sign-in and message delivery work without decryption.
3. Contact details are still personal data. They must never appear in application logs, error messages, or audit entries for identifiers that do not match an account (spec FR-091). They are covered by the data export and deletion promise in constitution VI.
4. Whether any **other** field (for example a UPI ID, which often embeds a mobile number) counts as a financial identifier is decided in the spec of the feature that introduces it. This ADR does not classify it.
5. Feature 003 builds the reusable encryption and blind-index helpers (`apps/api/internal/platform/crypto`) and uses them for the admin TOTP secret and for lockout keys. Feature 006 uses the same helpers for PAN.

## Consequences

- Sign-in uses a plain unique-index lookup; no decryption is needed on the hot path.
- A database dump exposes email addresses and mobile numbers in clear. The mitigations are the existing ones: Supabase encryption at rest, least-privilege database credentials, no production credentials for agents, and backups handled by 027.
- If the owner later decides contact details must be encrypted, the change is expand-then-contract: add `email_enc/email_hash` and `mobile_enc/mobile_hash`, backfill, switch lookups, then drop the plaintext columns. The `platform/crypto` helpers already support this.
- Suggested follow-up for the owner (not done by agents, because it edits project rules): add one line to `.claude/rules/sql.md` ("Contact details (email, mobile) are readable; see ADR 0007") and record a PATCH clarification in the constitution's version line.
