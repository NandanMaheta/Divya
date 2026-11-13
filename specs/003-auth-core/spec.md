# Feature Specification: Auth core (users, passwords, sessions, refresh rotation, device binding, admin TOTP)

**Feature Branch**: `feat/003-auth-core`

**Feature folder**: `specs/003-auth-core/`

**Created**: 2026-11-13

**Status**: Draft (awaiting owner approval)

**Module**: M1 Auth, row 003 in `docs/product/modules.md` (depends on 001; consumed by 004, 005, 007, 022, 024)

**Input**: User description: "Auth core: users, Argon2id passwords, sessions, refresh rotation, device binding, admin TOTP"

---

## Questions for the owner

Only one product decision is still open. The conductor default has been applied provisionally so the spec is complete. The plan must not start until you answer.

1. **Email and mobile in readable form vs. constitution VI.** Constitution VI says "PAN and other identifiers are encrypted at rest … with a blind index", and `.claude/rules/sql.md` says "PAN and similar identifiers … never plaintext". This spec follows the conductor default: email and mobile are stored normalised and readable, so sign-in can look them up and later features (004, 014) can send email and SMS. 003 never stores PAN. PAN stays encrypted with a blind index wherever it is stored (feature 006). The constitution overrides any spec, so please choose:
   - **A (default applied):** "Other identifiers" means government and financial IDs such as PAN, not contact details. This is recorded as a constitution clarification through an ADR.
   - **B:** Email and mobile must also be encrypted with a blind index. Users see the same behaviour, but 003 needs more work and later features must decrypt contact details before sending anything.

---

## Business rules, golden scenarios and boundaries

| Item | How 003 relates to it |
|---|---|
| **BR-CLI-04** (implemented, gate part) | Sign-in is allowed only when the user's status is `active`. A client reaches `active` only after email and mobile are verified and the admin approves. 003 owns the status model and enforces the gate. 004 delivers the OTPs and 005 runs the approval queue; both call 003's capabilities (FR-050 to FR-054). Tests for the gate carry `BR_CLI_04` in their names. |
| **BR-CLI-05** (not implemented here; boundary) | Changing email or mobile (OTP to the new contact, admin approval, notice to the old contact) belongs to 007. 003 only provides the capability to replace a stored contact once 007 has approved it (FR-055). 003 has no screen or operation that lets anyone change contacts. |
| **BR-CLI-01** (related; owned by 005) | A client user in 003 always has a name, an email and a mobile. 005 and 006 hold PAN. |
| **Golden scenarios GS-01 to GS-04** | **None affected.** 003 does not touch money, deals, settlement or due dates. All four must still pass on every commit. |
| **Blueprint §5** (`docs/product/product-blueprint.md`) | Source for: sign-in with mobile or email plus password; 5 failed attempts give a 15-minute lock; "log out all devices"; the admin can revoke any device; forgot-password logs out other devices; admin sign-in is email + password + authenticator app. |
| **ADR 0004** (`docs/adr/0004-auth.md`) | Self-built auth: short-lived access plus rotating, device-bound refresh, lockout, admin TOTP. |
| **Constitution VI** | Passwords use Argon2id. Short-lived access plus rotating, device-bound refresh. Admin sits behind Cloudflare Access plus TOTP. No secrets in logs. |

---

## Clarifications

### Session 2026-11-13

Each answer below was settled by a cited source or by a conductor default; none was guessed. The one question that only the owner can answer is listed under "Questions for the owner" at the top.

- Q: Should a client whose account is pending approval or disabled see a different sign-in message from someone with a wrong password? → A: No. All non-success cases share one generic failure; a correct password for a non-active account is not counted as a failed attempt. Source: conductor default (generic errors that don't reveal whether an account exists); research §2. Applied in D-09, FR-021, US-1.
- Q: Must sign-in and password messages be translatable into Gujarati and Hindi? → A: Yes. Every failure and validation outcome carries a stable, language-neutral code so each UI can show translated wording. English comes first, with Gujarati and Hindi ready. Source: blueprint §3 UX rules ("English first, with Gujarati and Hindi ready"). Applied in FR-093.
- Q: What scale should the auth design and its tests assume? → A: One organisation, one admin, about 100 clients, and a few devices per client. Source: CLAUDE.md ("one operator and ~100 invited clients"); blueprint §1. Applied in Assumptions and SC-007.
- Q: How long are security audit entries kept, and can they be removed? → A: Audit entries are append-only and never changed or deleted. Application logs are kept for at least 180 days. Source: constitution II and VI; migration 00001 (append-only `audit_log`). Applied in FR-090.
- Q: May email and mobile be stored readable for sign-in lookup, given constitution VI? → A: **Not settled by the documents.** Constitution VI and `.claude/rules/sql.md` can be read either way, and the constitution overrides specs, so this is owner Question 1. The conductor default (readable, normalised) is applied provisionally in D-16.

---

## Decisions (default, owner may change)

These came from the conductor or from the cited documents. They are recorded here so you can confirm them once.

| ID | Decision | Source |
|---|---|---|
| D-01 | **Scope.** 003 provides: users; statuses; roles; sign-in; refresh; sign-out; sign-out everywhere; device list and revoke; change password; lockout; admin TOTP with recovery codes; the first-admin bootstrap command; and authentication and authorisation on every API operation. 003 does **not** provide OTP sending (004), self-registration or the approval queue (005), contact changes (007), admin screens (022) or client screens (024). For those features it exposes service capabilities (FR-050 to FR-055) and no public operations. | Conductor; modules.md; research §10.8 |
| D-02 | **Statuses.** `pending_verification` → `pending_approval` (once email **and** mobile are verified, in either order) → `active` (after admin approval). `disabled` can be set from any status. Re-enabling restores the status the user had before being disabled. | Conductor; BR-CLI-04 |
| D-03 | **Roles.** `admin` and `client` only. A "Staff" role comes later. | Conductor; blueprint §1, §11 |
| D-04 | **Sign-in identifier.** Clients sign in with email **or** mobile. Admins sign in with email only. | Blueprint §5 ("Login", "Admin login") |
| D-05 | **Revocation is immediate.** A revoked device session or a disabled user loses access on the very next request. There is no grace period while a short-lived access credential runs out. | Conductor; ASVS 7.4 (research §3) |
| D-06 | **Session lifetimes.** Client device session: 90 days absolute, 30 days idle. Admin device session: 7 days absolute, 12 hours idle. Each new admin sign-in needs TOTP; refreshing within these lifetimes does not. "Idle" is measured from the last sign-in or refresh on that device session. Biometric unlock on the client is local to the device (024); the server never sees it. | Conductor |
| D-07 | **Password policy.** At least 10 characters; at least 64 accepted; anything over 256 rejected; no truncation; no composition rules (any characters, including spaces and non-English letters). Rejected: the 10,000 most common passwords (case-insensitive match), passwords equal to the user's own email or mobile (normalised), and, when changing, a new password equal to the current one. | Conductor; research §2 |
| D-08 | **Lockout.** 5 failed attempts within any 15-minute window lock sign-in for 15 minutes from the 5th failure. Failed attempts are: wrong password; wrong TOTP code; wrong recovery code; wrong current password on change-password. Attempts during the lock fail but do **not** extend it. A lock blocks new sign-ins only; device sessions that already exist keep working. A successful sign-in, the end of the lock, or a password reset (FR-053) clears the counter and the lock. Failures against identifiers with no account behave identically. | Conductor; blueprint §5; research §6 |
| D-09 | **One generic failure.** Wrong identifier, wrong password, locked, `pending_verification`, `pending_approval` and `disabled` all return the same failure, with the same status and the same wording, in comparable time. Example wording: "Those details didn't work. Check them and try again, or reset your password." A correct password for an account that is not `active` does not count as a failed attempt. Badly formed input, such as a mobile number that is not 10 digits, may get a plain validation message, because that reveals nothing about whether an account exists. | Conductor; research §2 |
| D-10 | **Strict refresh reuse.** If a refresh credential that was already used is presented again, the whole device session it belongs to ends at once. There is no grace window. This also applies to two simultaneous refreshes that present the same credential: one succeeds and the other counts as reuse. | Conductor |
| D-11 | **Device binding.** Every sign-in creates a device session tied to the identity of the installation that signed in. A refresh credential works only together with that same device identity. A mismatch is treated like reuse: the device session ends. If the same installation signs in again for the same user, its previous device session is replaced. | Research §4; constitution VI |
| D-12 | **Sign out everywhere** ends every device session of the user, **including the current one**. | Blueprint §3 (Profile: "logout from all devices") |
| D-13 | **Password changes.** Changing the password while signed in ends all **other** device sessions and keeps the current one. A password reset (forgot-password, via 004) ends **all** device sessions. | Conductor; blueprint §5 |
| D-14 | **Admin TOTP.** The admin enrols at the first sign-in after bootstrap and must finish enrolment before any admin operation. Codes are standard authenticator-app codes (6 digits, 30-second step). A code from one step before or after the current step is accepted. Each code works once, and no code from the same or an earlier step than the last accepted one works again. On enrolment the admin gets 10 single-use recovery codes, shown once. Recovery codes can be regenerated (password + current code required), which invalidates the old set. The authenticator can be replaced (password + current code or an unused recovery code required), which ends other admin device sessions. The interim proof after a correct password is valid for 5 minutes, works for one second-factor completion only, and opens nothing else. | Conductor; blueprint §5; research §5 |
| D-15 | **First-admin bootstrap.** An operator command (not an API operation) creates the first admin of the organisation. It refuses to run if an admin already exists in that organisation. The password is entered without echo and never appears in output, logs or audit. The new admin is `active`, with email treated as verified and TOTP not yet enrolled. A mobile number is optional for admins. Adding more admins later is a separate feature. | Conductor; research §5 |
| D-16 | **Personal data.** Email is stored trimmed and lowercased. Mobile is stored as `+91` followed by 10 digits, the first of which is 6 to 9. Both are readable so they can be looked up (subject to **Question 1**). 003 stores no PAN. Passwords, refresh credentials, TOTP secrets and recovery codes are never stored in a form that can be read back or used directly. | Conductor; constitution VI |
| D-17 | **Mobile numbers are Indian only.** Accepted input forms: `9876543210`, `09876543210`, `919876543210`, `+919876543210`, with optional spaces or hyphens. Anything else is invalid input. | Research §8; blueprint (India-only product) |
| D-18 | **Uniqueness.** One email belongs to at most one user, and one mobile to at most one user, across the whole system and both roles. Comparison uses the normalised value. | Needed for unambiguous sign-in (D-04) |
| D-19 | **Disabling.** Disabling ends every device session of the user. Re-enabling does not bring those sessions back. The last `active` admin of an organisation cannot be disabled. | Conductor (D-05); safety default |
| D-20 | **Audit.** Every security-relevant event and every state change in this feature writes one audit entry (see US-11). Refreshes are included because they change state (constitution II: "All mutations write to audit_log"). No audit entry ever contains a password, code, credential, secret, or an identifier as the person typed it when no matching account exists. | Conductor; constitution II |
| D-21 | **Shared domain.** The admin panel and the API must be served from one registrable domain (custom domain) in staging and production. This is a dependency on feature 027. | Conductor; research §4 |
| D-22 | **Admin unlock and break-glass recovery are out of scope.** The admin cannot unlock a locked client by hand; the lock expires on its own or the client resets their password through 004. If the only admin loses both the authenticator and every recovery code, recovery is an operations runbook item for 027, not a product feature here. | Conductor scope list; no source requires either |

---

## User Scenarios & Testing *(mandatory)*

Notation: "generic failure" means the single failure response defined in D-09. "Fake clock" means time is injected by the test (constitution III). Times are given as T plus an offset. Platforms: **Android** = Android app, **iPhone** = iPhone web app (PWA), **Web** = admin panel in a browser.

### User Story 1 - Client signs in with email or mobile (Priority: P1)

An approved client opens the app on an Android phone or the iPhone web app and signs in with either their email or their mobile number plus their password. Clients who are not yet approved, or who have been disabled, cannot get in, and the app does not reveal whether an account exists.

**Why this priority**: Nothing else in the client app works without sign-in. It also enforces the sign-in gate in BR-CLI-04.

**Independent Test**: Seed users in each status. Sign in with every identifier form and every failure case, then check the outcomes and the device sessions created.

**Acceptance Scenarios**:

1. **Given** an `active` client with email `asha@example.com` and password P, **When** they sign in from a new Android installation with identifier `  Asha@Example.COM ` and P, **Then** sign-in succeeds. One new device session exists for that installation with platform Android, and the client receives a short-lived access credential and a refresh credential.
2. **Given** an `active` client with mobile `+919876543210` and password P, **When** they sign in with each of `9876543210`, `09876543210`, `919876543210`, `+91 98765-43210` and P, **Then** every attempt succeeds and is matched to the same user.
3. **Given** an `active` client, **When** they sign in from the iPhone web app, **Then** sign-in succeeds exactly as on Android and the device session is recorded with platform iPhone.
4. **Given** an `active` client, **When** they sign in with the correct identifier and a wrong password, **Then** they get the generic failure, no device session is created, and one failed attempt is counted.
5. **Given** no user has email `nobody@example.com` or mobile `+919000000000`, **When** someone signs in with either identifier, **Then** the response is identical in status, body and wording to scenario 4.
6. **Given** a client in `pending_verification` who enters the correct password, **When** they sign in, **Then** they get the generic failure, no device session is created, and no failed attempt is counted (BR-CLI-04).
7. **Given** a client in `pending_approval` (both contacts verified, admin not yet approved) who enters the correct password, **When** they sign in, **Then** they get the generic failure and no device session is created (BR-CLI-04).
8. **Given** a `disabled` client who enters the correct password, **When** they sign in, **Then** they get the generic failure and no device session is created.
9. **Given** an `active` client with 3 failed attempts in the last 15 minutes, **When** they sign in correctly, **Then** sign-in succeeds and the failed-attempt counter is reset to 0.
10. **Given** an `active` admin with mobile `+919811111111` on record, **When** they try to sign in with that mobile number, **Then** they get the generic failure, because admins sign in with email only (D-04).
11. **Given** the identifier `12345`, **When** someone signs in, **Then** they get a validation message saying the input is not a valid email or 10-digit mobile number. The message never says whether an account exists.

---

### User Story 2 - Every operation enforces who may call it (Priority: P1)

Every API operation, now and in later features, can be called only by a signed-in user with the right role, except a short, explicit public list. Clients can never call admin operations. When a session is revoked or a user is disabled, access stops on the very next request.

**Why this priority**: Every other feature relies on this guarantee. If the check is missing, any data could leak.

**Independent Test**: Go through every operation in the API contract and call each one without a credential, with an expired, tampered or revoked credential, and as a client on admin operations. Check that each is rejected with no side effects.

**Acceptance Scenarios**:

1. **Given** the full list of operations in the API contract, **When** each operation that is not on the public list (FR-030) is called without any credential, **Then** every call is rejected as unauthenticated, with no data returned and no state changed.
2. **Given** a test that reads the API contract, **When** it runs in CI, **Then** it fails if any operation is neither marked as requiring authentication nor on the public list.
3. **Given** a signed-in client, **When** they call any admin-only operation, such as listing another user's devices, **Then** the call is rejected as forbidden, with no data returned and no state changed.
4. **Given** a signed-in admin who has completed TOTP, **When** they call an admin-only operation, **Then** it succeeds.
5. **Given** an access credential, **When** the fake clock moves past its expiry and it is used, **Then** the call is rejected as unauthenticated.
6. **Given** an access credential altered by even one character, or signed by anyone other than the system, **When** it is used, **Then** the call is rejected as unauthenticated.
7. **Given** a client's device session that is revoked at time T while its access credential is still unexpired, **When** that credential is used at T + 1 second, **Then** the call is rejected as unauthenticated (D-05).
8. **Given** a client disabled at time T while one of their access credentials is still unexpired, **When** that credential is used at T + 1 second, **Then** the call is rejected as unauthenticated (D-05, D-19).
9. **Given** an admin who has entered the correct password but not yet the TOTP code, **When** the interim proof is used to call any operation other than completing the second factor or enrolment, **Then** the call is rejected as unauthenticated.
10. **Given** a refresh credential, **When** it is presented as an access credential on any operation, **Then** the call is rejected as unauthenticated.

---

### User Story 3 - Staying signed in safely: refresh rotation, reuse detection, device binding (Priority: P1)

A signed-in device keeps its session alive by exchanging its refresh credential for a new one. Each refresh credential works once. If a stolen or replayed one turns up, the whole device session is shut down. Sessions expire after the idle and absolute limits for the user's role.

**Why this priority**: Clients are meant to stay signed in for weeks (D-06), so refresh is the main way they keep access. Reuse detection is the main protection against stolen refresh credentials.

**Independent Test**: Use a fake clock. Sign in, refresh repeatedly, replay old credentials, present credentials from another device identity, and step the clock across the idle and absolute limits.

**Acceptance Scenarios**:

1. **Given** a client device session holding refresh credential R1, **When** the same installation refreshes with R1, **Then** it receives a new access credential and a new refresh credential R2, R1 is marked used, and a later refresh with R2 succeeds.
2. **Given** R1 has already been exchanged for R2, **When** R1 is presented again from any installation, **Then** the refresh is rejected, the **entire** device session ends (R2 stops working and that session's access credentials are rejected on the next request), and a "refresh reuse detected" audit entry is written. The same user's sessions on other devices keep working.
3. **Given** a device session holding R1, **When** two refreshes with R1 arrive at the same moment, **Then** exactly one succeeds, the other counts as reuse, and the device session ends (D-10).
4. **Given** a device session bound to installation X, **When** its current refresh credential is presented with a different installation identity Y, **Then** the refresh is rejected, the device session ends, and the event is audited as reuse or device mismatch.
5. **Given** a client whose last sign-in or refresh was at T, **When** they refresh at T + 29 days 23 hours 59 minutes, **Then** it succeeds. **When** instead they first refresh at T + 30 days + 1 minute, **Then** it fails and they must sign in again (30-day idle limit).
6. **Given** a client device session started at S and refreshed every day, **When** they refresh at S + 90 days + 1 minute, **Then** it fails and they must sign in again (90-day absolute limit). A refresh at S + 89 days 23 hours succeeds.
7. **Given** an admin whose last sign-in or refresh was at T, **When** they refresh at T + 11 hours 59 minutes, **Then** it succeeds without a TOTP code. **When** instead they first refresh at T + 12 hours + 1 minute, **Then** it fails (12-hour idle limit).
8. **Given** an admin device session started at S and refreshed every hour, **When** they refresh at S + 7 days + 1 minute, **Then** it fails (7-day absolute limit).
9. **Given** a device session that was revoked or ended, or whose user was disabled, **When** its refresh credential is presented, **Then** the refresh is rejected.
10. **Given** a client whose account is currently locked (US-4) but who has an existing device session, **When** that device refreshes, **Then** it succeeds, because a lock blocks only new sign-ins (D-08).
11. **Given** an installation that already has a device session for client C, **When** C signs in again from the same installation, **Then** the earlier device session ends and only the new one appears in C's device list (D-11).

---

### User Story 4 - Lockout after repeated failures (Priority: P1)

After 5 failed attempts within 15 minutes, sign-in for that account is locked for 15 minutes. While the lock lasts, every attempt, even with the right password, gets the same generic failure, so an attacker learns nothing.

**Why this priority**: This is the blueprint's main defence against password guessing, and the client side has no second factor.

**Independent Test**: Use a fake clock. Make failed attempts at controlled times and check whether sign-in is locked or allowed at the exact boundaries.

**Acceptance Scenarios**:

1. **Given** an `active` client and a fake clock at T, **When** wrong passwords are entered at T, T+1m, T+2m, T+3m and T+4m, **Then** the account is locked until T+19m, and a "lockout" audit entry is written at T+4m.
2. **Given** that lock, **When** the correct password is entered at T+18m59s, **Then** the response is the generic failure, identical to a wrong password, and no device session is created.
3. **Given** that lock, **When** the correct password is entered at T+19m, **Then** sign-in succeeds and the counter is reset (unlock after 15 minutes).
4. **Given** that lock, **When** further wrong attempts are made at T+10m and T+15m, **Then** the lock still ends at T+19m (failed attempts during a lock do not extend it).
5. **Given** an `active` client, **When** wrong passwords are entered at T, T+1m, T+2m and T+3m and again at T+17m, **Then** the account is **not** locked, because only 1 failure falls in the last 15 minutes, and the correct password at T+17m30s succeeds.
6. **Given** an identifier with no account, **When** it is tried 6 times within a minute, **Then** all 6 responses are identical to those for a real, locked account, in status, body, wording and comparable time.
7. **Given** a locked client with an existing device session on another phone, **When** that phone calls any operation or refreshes, **Then** it keeps working.
8. **Given** an admin with a correct password, **When** they enter wrong TOTP codes or wrong recovery codes 5 times within 15 minutes, **Then** the admin account is locked for 15 minutes exactly as for passwords.
9. **Given** a locked client, **When** their password is set through the reset capability (FR-053), **Then** the lock and the counter are cleared and sign-in with the new password succeeds at once.

---

### User Story 5 - Admin signs in with an authenticator app (Priority: P1)

The admin signs in with email and password, then enters a 6-digit code from an authenticator app. The first time, they enrol the app and receive recovery codes. Codes cannot be replayed, and each recovery code works once.

**Why this priority**: The admin can see and change everything. Blueprint §5 and constitution VI require TOTP for admin sign-in.

**Independent Test**: Use a bootstrapped admin and a fake clock. Enrol, sign in with correct, wrong, stale, replayed and recovery codes, and check the outcomes and audit entries.

**Acceptance Scenarios**:

1. **Given** an `active` admin with TOTP enrolled, **When** they enter the correct email and password, **Then** no admin session is created yet. They receive an interim proof that is valid for 5 minutes and usable only to complete the second factor.
2. **Given** that interim proof and the code currently shown by the admin's authenticator, **When** they submit the code, **Then** an admin device session is created and a "sign-in success" audit entry is written.
3. **Given** that interim proof, **When** they submit a wrong code, **Then** they get the generic failure, no session is created, and one failed attempt is counted toward lockout.
4. **Given** the fake clock in step N, **When** the admin submits the code for step N−1, **Then** it is accepted. **When** they instead submit the code for step N−2, **Then** it is rejected.
5. **Given** code C was accepted for step N, **When** C is submitted again in step N, for example from a second browser, **Then** it is rejected. **And when** the code for step N−1 is submitted after C was accepted, **Then** it is also rejected (no replay of the same or an earlier step).
6. **Given** an admin with 10 unused recovery codes, **When** they submit recovery code K instead of a TOTP code, **Then** sign-in succeeds, K is used up, 9 remain, and a "recovery code used" audit entry is written. **When** K is submitted again on a later sign-in, **Then** it is rejected.
7. **Given** an interim proof issued at T, **When** a correct code is submitted at T+5m01s, **Then** it is rejected and the admin must enter the password again. **And given** an interim proof that has already completed a sign-in, **When** it is used a second time, **Then** it is rejected.
8. **Given** a freshly bootstrapped admin with no TOTP enrolled, **When** they enter the correct email and password, **Then** they are taken into enrolment: they receive a new authenticator secret, which can be shown as a QR code, and no admin session exists yet. **When** they confirm with a valid code from that secret, **Then** enrolment completes, 10 recovery codes are shown once, an admin device session is created, and a "TOTP enrolled" audit entry is written.
9. **Given** an admin part-way through enrolment, before confirming, **When** they try any admin operation, **Then** it is rejected as unauthenticated (enrolment cannot be skipped).
10. **Given** a signed-in admin, **When** they regenerate recovery codes after giving their password and a current TOTP code, **Then** 10 new codes are shown once, every old code stops working, and the change is audited.
11. **Given** a signed-in admin, **When** they replace their authenticator after giving their password and either a current TOTP code or an unused recovery code, and then confirm a code from the new secret, **Then** the old secret's codes stop working, all **other** admin device sessions end, and the change is audited.
12. **Given** an admin with a valid device session, **When** they sign in again from the same browser after signing out, **Then** a TOTP code is required again, because each new admin sign-in needs one.
13. **Given** any client, **When** they sign in, **Then** they are never asked for a TOTP code.

---

### User Story 6 - Sign out, sign out everywhere, see and revoke my devices (Priority: P2)

A user can sign out of this device, sign out of every device, see the devices signed in to their account, and revoke any of them.

**Why this priority**: These are needed for a lost phone and for the "log out from all devices" item in the blueprint Profile page. P2 because sign-in and enforcement must exist first.

**Independent Test**: Sign one user in on three installations (Android, iPhone, and a second Android). Exercise sign-out, sign-out everywhere, list and revoke, and check each device on its next request.

**Acceptance Scenarios**:

1. **Given** a client signed in on devices A (Android), B (iPhone) and C (Android), **When** they sign out on A, **Then** A's access and refresh credentials stop working on the next request, B and C keep working, and a "signed out" audit entry is written.
2. **Given** the same three devices, **When** they choose "sign out everywhere" on A, **Then** A, B and C all lose access on their next request (D-12) and the event is audited.
3. **Given** the same three devices, **When** the client lists their devices from A, **Then** they see exactly three entries. Each shows the device name, the platform (Android, iPhone or Web), when it signed in, and when it was last active (last sign-in or refresh). A is marked "this device". Sessions that have ended are not listed.
4. **Given** the same three devices, **When** the client revokes B from A, **Then** B loses access on its next request, A and C keep working, B disappears from the list, and a "device revoked" audit entry records the client as the actor.
5. **Given** a device-session reference that belongs to another user, **When** a client tries to revoke it, **Then** they get "not found", nothing changes, and nothing reveals that the reference exists.
6. **Given** a client who signed out on A, **When** they sign in again on A, **Then** a new device session is created and the list shows A once.
7. **Given** a client who revokes their own current device A, **When** A makes its next request, **Then** it is rejected, exactly as after signing out.

---

### User Story 7 - Change password (Priority: P2)

A signed-in user changes their password by giving the current one. Every other device is signed out; the device they used stays signed in.

**Why this priority**: This is basic account hygiene and is required by ASVS. The forgot-password flow itself is in 004.

**Independent Test**: Sign a user in on three devices, change the password on one of them, then check every device and both passwords.

**Acceptance Scenarios**:

1. **Given** a client signed in on A, B and C with password P1, **When** on A they give current password P1 and a valid new password P2, **Then** the change succeeds; B and C lose access on their next request; A stays signed in; signing in with P1 fails and with P2 succeeds; a "password changed" audit entry is written (D-13).
2. **Given** a signed-in client, **When** they give a wrong current password, **Then** the change is rejected, nothing changes, and one failed attempt is counted toward lockout (D-08).
3. **Given** a signed-in client, **When** the new password is 9 characters long, **Then** it is rejected with a message stating the 10-character minimum.
4. **Given** a signed-in client, **When** the new password is on the 10,000-common-passwords list, for example `1234567890` or `QWERTYUIOP` (in any letter case), **Then** it is rejected with a message saying the password is too common.
5. **Given** a signed-in client, **When** the new password equals the current password, or equals their email or mobile, **Then** it is rejected.
6. **Given** a signed-in client, **When** the new password is a 64-character passphrase containing spaces and Gujarati or Hindi letters, **Then** it is accepted. **When** the new password is 257 characters long, **Then** it is rejected.
7. **Given** a signed-in admin with two admin device sessions, **When** they change their password on one, **Then** the other admin session ends and the current one continues.

---

### User Story 8 - Admin controls client sessions and account status (Priority: P2)

The admin can see any user's devices, revoke any of them or all of them, and (through the account capability used by client management) disable and re-enable a user, with immediate effect.

**Why this priority**: Blueprint §5 says "you can revoke any device", and the Clients page shows devices. It is P2 because it is administrative, not part of the daily sign-in path.

**Independent Test**: Sign a client in on two devices. As admin, list, revoke one, revoke all, disable and re-enable, then check the client's devices after each step.

**Acceptance Scenarios**:

1. **Given** a client signed in on two devices, **When** the admin lists that client's devices, **Then** both appear, with the same details as in US-6 scenario 3.
2. **Given** that, **When** the admin revokes one of the client's devices, **Then** that device loses access on its next request, the other keeps working, and a "device revoked" audit entry records the admin as the actor.
3. **Given** that, **When** the admin ends all sessions for the client, **Then** both devices lose access on their next request and the event is audited.
4. **Given** an `active` client with two device sessions, **When** the client is disabled through the account capability, **Then** the status becomes `disabled`, both sessions end, the next request from either device is rejected, sign-in gives the generic failure, and the audit entry records the status before and after.
5. **Given** a client who was `active` and then disabled, **When** they are re-enabled, **Then** the status returns to `active`, the old sessions stay ended, and a new sign-in succeeds. **Given** a client who was `pending_approval` and then disabled, **When** they are re-enabled, **Then** the status returns to `pending_approval` and sign-in still fails (BR-CLI-04).
6. **Given** an organisation with exactly one `active` admin, **When** anyone tries to disable that admin, **Then** the request is refused and nothing changes.
7. **Given** a signed-in client, **When** they call any operation in this story, **Then** it is rejected as forbidden.

---

### User Story 9 - First-admin bootstrap (Priority: P1)

On a fresh installation, the owner runs one command to create the first admin account. They then sign in to the admin panel and enrol their authenticator.

**Why this priority**: Without it nobody can ever sign in as admin.

**Independent Test**: Run the command against an empty organisation, then against one that already has an admin, then sign in as the new admin.

**Acceptance Scenarios**:

1. **Given** an organisation with no admin, **When** the operator runs the bootstrap command with email `owner@example.com`, a name, and a valid password typed without echo, **Then** an `active` admin with that email exists, TOTP is not yet enrolled, the command exits successfully, and an "admin bootstrapped" audit entry is written with actor kind `system`.
2. **Given** an organisation that already has an admin, **When** the command is run again, **Then** it refuses, exits with failure, and changes nothing.
3. **Given** a password that breaks the policy (D-07), **When** the command is run, **Then** it refuses and says which rule failed.
4. **Given** an email already used by a client, **When** the command is run with that email, **Then** it refuses and changes nothing.
5. **Given** any run of the command, **When** its output, the application logs and the audit entry are inspected, **Then** the password appears in none of them.
6. **Given** a freshly bootstrapped admin, **When** they sign in, **Then** US-5 scenario 8 (enrolment) applies.

---

### User Story 10 - Account capabilities for features 004, 005 and 007 (Priority: P2)

Later features need to create pending client users, record contact verification, approve users, set a password after a reset and replace a contact. 003 provides these as internal capabilities with no public operations, so those features can never bypass the status gate.

**Why this priority**: These capabilities are needed before 004 and 005 can start, but no user touches them directly in 003.

**Independent Test**: Call each capability directly in service-level tests and check statuses, uniqueness, session effects and audit entries.

**Acceptance Scenarios**:

1. **Given** no user with email `ravi@example.com` or mobile `+919812345678`, **When** a client user is created with name "Ravi", email `Ravi@Example.com`, mobile `98123 45678` and a valid password, **Then** the user exists with role `client`, status `pending_verification`, the email stored as `ravi@example.com`, the mobile stored as `+919812345678`, and an audit entry.
2. **Given** an existing user with that email or that mobile (in any input form), **When** another user is created with it, **Then** creation is rejected as a duplicate and nothing is stored. How 005 shows this to the person registering is up to 005.
3. **Given** a `pending_verification` client, **When** the mobile is marked verified and then the email, **Then** the status becomes `pending_approval` only after the second mark. Marking the same contact twice changes nothing more. The order does not matter (BR-CLI-04).
4. **Given** a `pending_approval` client, **When** they are approved, **Then** the status becomes `active` and sign-in succeeds (BR-CLI-04). **Given** a `pending_verification` client, **When** approval is attempted, **Then** it is refused.
5. **Given** a client with device sessions on two devices and a lock in place, **When** a new valid password is set through the reset capability (after 004 has verified the person), **Then** both sessions end, the lock and counter clear, the old password stops working, the new one works, and a "password reset" audit entry is written (D-13). Passwords that break the policy are rejected.
6. **Given** an `active` client, **When** their mobile is replaced through the contact-replacement capability after 007's approval, **Then** the new normalised mobile is stored, the status is unchanged, uniqueness is enforced, sign-in works with the new mobile and fails with the old one, and the before and after values are audited (BR-CLI-05 is enforced by 007, not here).
7. **Given** the API contract after 003, **When** it is inspected, **Then** none of the capabilities in this story is reachable as a public operation.

---

### User Story 11 - Security audit trail (Priority: P1)

Every security-relevant event leaves an audit entry, so the owner can see who signed in, from where, and what was revoked. No entry ever contains a secret.

**Why this priority**: Constitution II requires an audit entry for every mutation, and the blueprint's Audit log page depends on these entries.

**Independent Test**: Trigger each event below and check that exactly one matching audit entry exists with the right actor, then scan all entries for secrets.

**Acceptance Scenarios**:

1. **Given** each event in the table below, **When** it happens, **Then** exactly one audit entry exists with the listed actor kind, the actor (when known), the affected user, the device session (when there is one), the action name and the time.

   | Event | Actor kind |
   |---|---|
   | Sign-in success (client; admin after second factor) | client / admin |
   | Sign-in failure (wrong password, wrong code, wrong recovery code, not-active status, locked) | system (user referenced when the account exists) |
   | Sign-in failure for an identifier with no account | system (identifier not stored as typed) |
   | Lockout started | system |
   | Session refreshed | client / admin |
   | Refresh reuse detected or device mismatch, with the device session ended | system |
   | Signed out; signed out everywhere | client / admin |
   | Device revoked (by the user, or by the admin) | client / admin |
   | All sessions ended by the admin | admin |
   | Password changed; password reset | client / admin; system for reset |
   | TOTP enrolled; authenticator replaced; recovery codes regenerated; recovery code used | admin |
   | User created; contact marked verified; status changed (approved, disabled, re-enabled); contact replaced | system or admin, as the calling feature specifies |
   | Admin bootstrapped | system |

2. **Given** every audit entry produced by the full acceptance suite of this feature, **When** they are scanned, **Then** none contains a password, a TOTP code, a recovery code, an authenticator secret, an access or refresh credential, or an unmatched identifier as typed.
3. **Given** an existing audit entry, **When** anyone tries to change or delete it, **Then** the change is refused (existing platform guarantee from 001).

---

### Edge Cases

- **Time windows (fake clock, exact boundaries).** At exactly 15 minutes after the 5th failure the lock has ended. At exactly 30 days idle, 90 days absolute, 12 hours idle and 7 days absolute, the session has expired (the limit is inclusive of expiry). Each scenario above tests one minute either side. An interim proof expires at exactly 5 minutes. TOTP tolerance is one 30-second step either side and no more.
- **Clock changes.** Server time is the only time used. A client device with a wrong clock can still sign in, because client clocks are never trusted. An admin whose phone clock is off by more than one step gets the generic failure. Their recovery codes remain the way in.
- **Approvals pending.** `pending_verification` and `pending_approval` users always get the generic failure, even with the right password (US-1 scenarios 6 and 7). Re-enabling a disabled user who had not been approved returns them to the pending status, not to `active` (US-8 scenario 5).
- **Partial payments.** Not applicable. 003 has no money. Nothing in 003 depends on a client's balance; a client who owes money signs in like any other.
- **iPhone vs Android.** Behaviour is identical. Only the recorded platform differs. On the iPhone web app, if the browser's stored data is cleared, or the app is used from a Safari tab as well as from the Home Screen icon, each counts as a separate installation, so a separate device session appears in the list. Old ones end through the idle limit or by being revoked. How the iPhone web app stores its refresh credential is decided in 024 (research §10.10).
- **Lost refresh response.** If the server rotates a credential but the response never reaches the device, the device's next refresh uses an already-used credential. Under D-10 this ends the device session and the user must sign in again. This is accepted; apps must keep it rare (see Notes for the architect).
- **Several admin browser tabs.** Tabs share one device session. Two tabs refreshing at the same moment would count as reuse and sign the admin out (D-10). The admin panel (022) must make sure only one refresh happens at a time.
- **Corrections.** A device revoked by mistake: the user signs in again. A user disabled by mistake: re-enable (US-8 scenario 5); sessions are not restored. A wrong email or mobile: corrected through 007 using the contact-replacement capability (US-10 scenario 6). A wrong admin email typed at bootstrap: the bootstrap command cannot be rerun once an admin exists (US-9 scenario 2), so the email is corrected with the contact-replacement capability (FR-055), which an admin-facing operation will expose in 022/023.
- **Identifier collisions.** The same email typed with different case or spacing, or the same mobile in different formats, is one identifier (D-16, D-17). An email cannot belong to a client and an admin at the same time (D-18).
- **Lock vs existing sessions.** A lock never ends existing sessions (US-4 scenario 7). Someone who knows only the identifier can lock out new sign-ins for 15 minutes at a time but cannot sign a user out.
- **Revocation during a request.** A request already being processed when its session is revoked may finish. Every request that starts afterwards is rejected.
- **Bootstrap race.** If two bootstrap commands run at once against an empty organisation, exactly one succeeds.

---

## Requirements *(mandatory)*

### Functional Requirements

**Users, roles and statuses**

- **FR-001**: The system MUST hold, for each user: organisation, role (`admin` or `client`), status, name, normalised email, normalised mobile (required for clients, optional for admins), whether email and mobile have been verified, password, and timestamps for creation and last sign-in.
- **FR-002**: The system MUST support exactly the statuses `pending_verification`, `pending_approval`, `active` and `disabled`, with only the transitions in D-02. Every transition MUST be audited.
- **FR-003**: The system MUST normalise email and mobile as in D-16 and D-17, and MUST reject a mobile number that does not normalise to `+91` followed by 10 digits, the first of which is 6 to 9.
- **FR-004**: The system MUST enforce uniqueness of normalised email and normalised mobile across all users (D-18), including when two creations race.

**Passwords**

- **FR-010**: Passwords MUST be stored only as one-way hashes using the algorithm required by constitution VI, never in readable or reversible form, and MUST never be logged.
- **FR-011**: Every way of setting a password (bootstrap, user creation, change, reset) MUST apply the policy in D-07 and say which rule failed.
- **FR-012**: Checking a password MUST take comparable time whether or not the account exists (D-09).

**Sign-in**

- **FR-020**: The system MUST let a user sign in with identifier plus password: clients by email or mobile, admins by email only (D-04).
- **FR-021**: Sign-in MUST succeed only for `active` users who are not locked. In every other case it MUST return the single generic failure (D-09). This is the gate for BR-CLI-04.
- **FR-022**: A successful client sign-in MUST create a device session for the presenting installation, recording its device identity, device name and platform (Android, iPhone or Web). It MUST end any earlier session of that same user on that same installation (D-11).
- **FR-023**: For admins, a correct password MUST result only in an interim proof (D-14). The device session MUST be created only after a valid TOTP code or unused recovery code, or after enrolment is completed.

**Authentication and authorisation on every operation**

- **FR-030**: The only operations reachable without a signed-in session MUST be: the health check (001); sign-in; refresh; and, with a valid interim proof only, admin second-factor completion and first-time TOTP enrolment. Later features may add public operations only by listing them in their own approved spec.
- **FR-031**: Every other operation MUST reject a call that has no valid, unexpired, untampered access credential, or whose device session has ended, or whose user is not `active`. The rejection MUST be "unauthenticated", MUST return no data and MUST cause no side effects.
- **FR-032**: Operations MUST declare which roles may call them. A caller whose role is not allowed MUST be rejected as "forbidden", with no data and no side effects. Admin-only operations MUST require an admin who completed the second factor.
- **FR-033**: Revocation, sign-out, disabling and password-change effects MUST apply to the very next request (D-05).
- **FR-034**: The API contract MUST show, for each operation, whether it needs authentication and which roles may call it. An automated check MUST fail the build if any operation outside the FR-030 list lacks this (US-2 scenario 2).
- **FR-035**: Interim proofs and refresh credentials MUST NOT be accepted as access credentials (US-2 scenarios 9 and 10).

**Refresh, rotation and device binding**

- **FR-040**: Refreshing MUST require the current refresh credential together with the device identity of the session's installation. On success it MUST issue a new access credential and a new refresh credential and mark the old refresh credential used.
- **FR-041**: Presenting a used refresh credential, or presenting a refresh credential with a device identity that does not match, MUST end the entire device session and be audited (D-10, D-11). Concurrent use of one credential MUST let at most one refresh succeed.
- **FR-042**: Device sessions MUST expire by the idle and absolute limits for the user's role (D-06), measured on server time.
- **FR-043**: Refresh MUST fail for ended or expired sessions and for users who are not `active`. Refresh MUST NOT be blocked by a sign-in lock (D-08).

**Lockout**

- **FR-045**: The system MUST apply the lockout in D-08 per account, counting the failure types listed there. It MUST behave identically for identifiers that have no account, and it MUST work correctly when several server instances run at once.
- **FR-046**: A lock MUST clear automatically 15 minutes after the 5th failure, and MUST clear after a password reset.

**Sign-out and devices**

- **FR-047**: A signed-in user MUST be able to sign out of the current device session and to sign out everywhere (D-12).
- **FR-048**: A signed-in user MUST be able to list their own live device sessions with the fields in US-6 scenario 3, and revoke any of them. Requests for another user's session MUST get "not found".
- **FR-049**: An admin MUST be able to list any user's live device sessions, revoke any one of them, and end all of a user's sessions.

**Capabilities for other features (internal, not public operations)**

- **FR-050**: Create a client user in `pending_verification` (US-10 scenarios 1 and 2).
- **FR-051**: Mark email verified and mark mobile verified. When both are verified, the status moves to `pending_approval` (US-10 scenario 3).
- **FR-052**: Approve a `pending_approval` user (status becomes `active`); disable any user; re-enable a disabled user to their previous status; refuse to disable the last `active` admin (D-19).
- **FR-053**: Set a new password after a verified reset, ending all sessions and clearing any lock (D-13).
- **FR-054**: End all device sessions of a user, for use by other features (for example 005 on rejection, or 007 after a contact change, if their specs require it).
- **FR-055**: Replace a user's email or mobile, normalised, unique and audited, without changing status (used by 007 after BR-CLI-05 approval).

**Admin TOTP**

- **FR-060**: Admins MUST complete TOTP enrolment before any admin operation, and MUST give a valid TOTP or recovery code at every new sign-in (D-14).
- **FR-061**: TOTP codes MUST be accepted only within one step of the current step and never for a step at or before the last accepted step (no replay).
- **FR-062**: On enrolment the system MUST issue 10 single-use recovery codes, shown once and never readable again. Regeneration (password + current code) MUST invalidate the previous set.
- **FR-063**: Replacing the authenticator MUST require the password plus a current code or an unused recovery code, MUST invalidate the old secret, and MUST end the admin's other device sessions.
- **FR-064**: Authenticator secrets MUST be stored only in protected form, never logged and never shown again after enrolment.
- **FR-065**: Clients MUST never be asked for TOTP.

**Change password**

- **FR-070**: A signed-in user MUST be able to change their password by giving the current password and a new one that meets the policy. On success all other device sessions MUST end and the current one MUST continue (D-13). A wrong current password MUST count as a failed attempt.

**Bootstrap**

- **FR-080**: An operator command MUST create the first admin of an organisation as in D-15 and US-9, refusing if an admin already exists, and never exposing the password.

**Audit and privacy**

- **FR-090**: Every event in US-11 scenario 1 MUST write exactly one audit entry in the same unit of work as the change it records. Audit entries are never changed or deleted (constitution II). Application logs for these events are kept for at least 180 days (constitution VI).
- **FR-091**: No audit entry, log line, error message or API response MUST ever contain a password, TOTP code, recovery code (except the one-time display at issue), authenticator secret (except the one-time display at enrolment), access or refresh credential, or an unmatched identifier as typed.
- **FR-092**: Access, refresh and interim credentials MUST be unguessable, and the server MUST NOT keep them in a form that could be used if the database were read.
- **FR-093**: Every failure and validation outcome in this feature (the generic sign-in failure, each password-policy rule, invalid identifier, unauthenticated, forbidden, not found, bootstrap refusals) MUST carry a stable, language-neutral code. UIs show the wording in English first, with Gujarati and Hindi ready (blueprint §3).

### Key Entities *(include if feature involves data)*

- **User**: A person who can sign in. Belongs to one organisation. Holds role, status, name, normalised email and mobile, verification flags, protected password, and sign-in counters. Clients map to "main members" (BR-CLI-01). Family members never sign in and are not users.
- **Device session**: One signed-in installation of one user. Holds device identity, name, platform, sign-in time, last-active time, absolute expiry and end reason (signed out, revoked by user, revoked by admin, reuse detected, password changed, password reset, disabled, expired, replaced).
- **Refresh credential**: A single-use credential belonging to one device session. Each one is replaced by its successor at refresh. Any used one that is presented again ends the session.
- **Interim second-factor proof**: Short-lived (5 minutes), single-use proof that an admin's password was correct. It opens only second-factor completion or enrolment.
- **Authenticator enrolment**: An admin's protected TOTP secret, its state (pending or confirmed), and the last accepted step.
- **Recovery code set**: An admin's 10 single-use codes. Each code is either unused or used, with the time of use.
- **Failed-attempt record**: Failures per normalised identifier within the 15-minute window, plus any current lock end. It exists for identifiers without accounts too, and never stores them as typed.
- **Audit entry**: The existing append-only record from 001, written for every event in US-11.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of operations outside the FR-030 public list reject unauthenticated calls, and 100% of admin-only operations reject client calls, as verified by the automated contract walk on every build.
- **SC-002**: After any revocation, sign-out, disabling or password change, 0 subsequent requests from the affected device sessions succeed. This holds in the acceptance suite with requests made 1 second after the event.
- **SC-003**: For the 6 failure cases in D-09, the sign-in responses cannot be told apart by status, body or wording. Median response times differ by less than 10% over 50 attempts each in the integration environment.
- **SC-004**: All time-based rules (lock at 15 minutes; idle limits of 30 days and 12 hours; absolute limits of 90 days and 7 days; 5-minute interim proof; one TOTP step) pass their fake-clock boundary tests at exactly ±1 minute (±1 step for TOTP) with no real waiting.
- **SC-005**: In the replay tests, 0 replayed refresh credentials, 0 replayed TOTP codes and 0 reused recovery codes are accepted.
- **SC-006**: 0 audit entries, log lines or responses in the full acceptance suite contain a secret listed in FR-091, as checked by an automated scan.
- **SC-007**: Every operation in this feature, sign-in included, meets the constitution VII budget (p95 under 200 ms, Mumbai, at the current scale of about 100 clients).
- **SC-008**: A client who signs in once on a phone and opens the app at least once a month stays signed in for up to 90 days without typing the password again.
- **SC-009**: The owner can go from an empty installation to a signed-in admin session with TOTP enrolled in under 5 minutes, using only the bootstrap command and the admin panel.

---

## Non-goals

- Sending or verifying OTPs by email or SMS, and the forgot-password screens (004, 024).
- Self-registration and the approval queue (005). Family accounts and PAN storage (006).
- Contact-detail changes with OTP and approval, BR-CLI-05 (007). 003 provides only the replacement capability (FR-055).
- Client and admin screens (024, 022). Biometric or PIN unlock on the device (024; client side only).
- A Staff role or any roles beyond `admin` and `client`. Creating second and later admins.
- Second-factor for clients. Passkeys, social sign-in or single sign-on.
- Unlock of locked accounts by the admin, and break-glass recovery for an admin who has lost both the authenticator and all recovery codes (D-22; runbook in 027).
- Device-bound proof-of-possession keys (later hardening; research §4).
- Per-IP or geographic blocking as a product feature (the architect may add defence-in-depth limits; see Notes).
- User data export and deletion (constitution VI; a later feature).
- Infrastructure: custom domains and Cloudflare Access configuration (027).
- Automated IPO applying, WhatsApp, SME IPOs, any money movement (constitution: out of scope).

---

## Assumptions and dependencies

- **Feature 001** (done) provides organisations, the append-only audit log and the health check. One organisation exists today. Every user belongs to it.
- **Scale**: one organisation, one admin, about 100 clients, and a few devices per client (CLAUDE.md; blueprint §1). Performance and lockout tests assume this scale.
- **Feature 027** must give the admin panel and the API one registrable domain (custom domain) in staging and production, and must put Cloudflare Access in front of the admin panel (D-21; constitution VI). Local development and preview environments may use a documented substitute chosen in the plan.
- **Features 004, 005, 007, 022 and 024** consume the capabilities and operations defined here and must not reimplement them.
- Server time (UTC) is authoritative. Times shown to people are rendered in IST by the UIs.
- Device name and platform are reported by the app at sign-in and are informational only. The device identity is generated by the app per installation and kept with its refresh credential.
- The list of 10,000 common passwords is a fixed, versioned list shipped with the application.

---

## Notes for the architect (not requirements)

These are technical options from `research.md`. Decide them in the plan, not here.

- **Access credential format.** ADR 0004 says JWT (research §3 suggests EdDSA, golang-jwt/jwt/v5). D-05 (immediate revocation) means each authenticated request must check that the device session is still live and the user is still `active`; research §3 recommends one primary-key lookup per request. State the trade-off explicitly.
- **Admin web refresh transport.** Research §4 compares an httpOnly, SameSite=Strict cookie scoped to the auth path with CSRF defences against returning the credential in the response body for mobile. Choose per client type. D-21 is the domain prerequisite.
- **Libraries and new runtime dependencies** (golang-jwt/jwt/v5, pquerna/otp, promoting x/crypto to a direct dependency) need owner approval (CLAUDE.md; research §10.1).
- **Huma.** One global, fail-closed authentication and authorisation middleware driven by each operation's declared security, plus the contract-walk test for FR-034 (research §7). `divya openapi` must build without live dependencies.
- **Argon2id.** Parameters, rehash-on-login and a concurrency cap so a burst of sign-ins cannot exhaust memory on Cloud Run. Show that SC-007 holds (research §2).
- **Lockout and rate limits.** Lockout counters in PostgreSQL so they work across instances, kept generic so 004 can reuse them for OTP limits. Optional coarse per-IP limiting as defence in depth. Housekeeping runs through `/internal/tick` (research §6; ADR 0003).
- **Storage.** Refresh-credential families per device session, stored only as digests. TOTP secret encrypted at rest. Recovery codes hashed. Blind-index helper reusable by 006. The auth migration and the `internal/auth` PRs need owner approval and a security review (constitution workflow item 5).
- **Idempotency.** Idempotency keys (constitution V) on refresh must not act as a back-door grace window that weakens D-10.
- **Single refresh at a time.** Apps (024) and the admin panel (022) must make sure only one refresh is in flight at a time and must store the new credential before using it, so that D-10 does not sign people out by accident.
- **Client IP for audit and limits.** Take it from the trusted proxy hop, not the first `X-Forwarded-For` value (research §6, unverified).
- **Test names** carry `BR_CLI_04` where the gate is exercised.

---

## Owner summary (for approval)

1. 003 builds sign-in for everyone: clients use email or mobile plus password; the admin uses email, password and an authenticator-app code.
2. Only approved (`active`) clients can sign in; pending and disabled users get the same "details didn't work" message (BR-CLI-04).
3. Clients stay signed in on their phone for up to 90 days (30 days without opening the app ends it); admin sessions last 7 days, or 12 hours idle.
4. Each phone or browser is a "device": users can see and revoke their devices or sign out everywhere; you can revoke any client's device.
5. Revoking a device, disabling a user or changing the password cuts access on the very next request.
6. 5 wrong attempts within 15 minutes lock sign-in for 15 minutes; messages never reveal whether an account exists.
7. A stolen or replayed refresh credential signs that device out at once; admin codes cannot be replayed, and recovery codes work once.
8. A one-time command creates your first admin account; every security event is written to the audit log with no secrets.
9. Not in 003: OTP sending (004), registration and approvals (005), contact changes (007), screens (022/024), staff roles, client 2FA.
10. Please answer Question 1 (email and mobile readable, PAN always encrypted) and confirm defaults D-01 to D-22; then the plan can start.
