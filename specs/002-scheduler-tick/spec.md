# Feature Specification: Scheduler tick + scheduled jobs + outbox (fake-clock testable)

**Feature Branch**: `feat/002-scheduler-tick` (folder `specs/002-scheduler-tick/`)

**Created**: 2026-11-13

**Status**: Draft (clarified; awaiting owner approval)

**Input**: User description: "Feature 002 Scheduler tick + scheduled_jobs + outbox (fake-clock testable) (M9 Scheduler; BR-IPO-03, BR-TASK-03, BR-SET-08 date helpers, GS-04)."

**Module**: M9 Scheduler (`docs/product/modules.md` row 002, depends on 001).

**Business rules implemented (mechanism only)**:

| Rule | What this feature provides | What stays with the owning feature |
| --- | --- | --- |
| BR-IPO-03 | Every dependent job can be rescheduled or cancelled in the same change as an IPO date change; lifecycle jobs run late rather than never. | The lifecycle states and which jobs an IPO has (008). |
| BR-TASK-03 | Quiet-hours aware "available at" timing, per-message de-duplication, withdrawal of undelivered messages ("stop on completion"), and expiry so stale reminders are never sent. | Intensity levels, cadence, grouping per client, which messages exist (013); delivery to devices (014). |
| BR-SET-08 | IST business-date helpers and the due-date calculator (listing day + 2 weekdays, 23:59 IST). | Applying the due date to settlements (018); overdue escalation, BR-PAY-03 (019). |

**Golden scenarios affected**: GS-04 (due-date helper must satisfy it here and is reused by 018). GS-01, GS-02 and GS-03 are not affected (no money is computed, stored or moved by this feature) and must keep passing.

## Questions for the owner

1. **Alert when something gives up.** When a scheduled job or a message exhausts its retries and becomes "dead", should you get an admin alert (push to your phone + the bell), or is it enough that it appears in the admin "failed jobs" list and as a count on the admin Home warning? Proposed default if you have no preference: one grouped admin alert per tick that produced new dead items ("2 scheduled jobs failed permanently"), plus the list and Home count. (Not settled by the blueprint: section 4 only promises alerts for client actions, section 7.1 describes the admin alert channel.)

**Defaults flagged for one-time confirmation** (agents will use these unless you say otherwise; each is an interpretation of existing text, not new behaviour):

- D1. "23:59 IST" in BR-SET-08 means 23:59:00 IST; "after the due time" (BR-PAY-03) means strictly later than that instant.
- D2. If a listing day were ever a Saturday or Sunday, the 2 weekdays are counted from the next weekdays (Sat or Sun listing → Tuesday). Listings normally happen on weekdays, so this is a safety rule only.
- D3. Quiet hours apply every day of the week, including weekends and exchange holidays.

## Clarifications

### Session 2026-11-13

Answered from settled owner decisions and documents (no owner input needed):

- Q: Does the due-date helper skip exchange holidays? → A: No. Listing day + 2 weekdays, skipping only Saturday and Sunday, at 23:59 IST. Source: BR-SET-08, GS-04, blueprint section 13 decision 3; owner confirmed that exchange holidays are not skipped.
- Q: Which reminder rules does this feature implement? → A: None of the rules themselves. This feature provides the scheduling and outbox mechanism plus quiet-hour aware timing. Intensity levels (gentle once then once a day; standard every 2 h inside the window; urgent every 1 h with the last one 30 min before cut-off), grouping per client and stop-on-completion are feature 013; push delivery is feature 014. Source: blueprint section 7.2; `docs/product/modules.md` rows 013 and 014.
- Q: What are the quiet hours and which messages may ignore them? → A: 21:30 to 08:00 IST by default, editable later in admin Settings. Messages respect quiet hours unless the owning feature marks them exempt; per blueprint 7.2 the only planned exemption is payment-overdue reminders when the admin enables it (feature 019). Source: blueprint sections 4 (Settings: quiet hours) and 7.2.
- Q: Are the scheduled-jobs queue and the outbox append-only like the ledger and audit log? → A: No. They are operational queues whose status changes as work progresses; they are not part of the ledger or audit history. Business state changes caused by jobs still write `audit_log` in their own features. Admin actions on jobs in this feature (retry, cancel) are mutations and write `audit_log` (constitution principle II). Source: owner decision for this feature; constitution II.
- Q: What timing precision is required? → A: Plus or minus 5 minutes is acceptable; work runs on the first tick at or after its due time. Source: ADR-0003 (Consequences).
- Q: What happens to work that became due during downtime? → A: Jobs that change business state (for example IPO status by dates) always run late, never skipped; time-sensitive jobs and messages whose expiry has passed are skipped and recorded as expired, never delivered. Source: owner direction for this feature; blueprint 7.2 ("never the same reminder twice"); research.md section 3.
- Q: Who gets to see and retry failed work, and where? → A: The admin (operator). This feature provides the capability; the screens come with features 022/023, after admin authentication (003). A future Staff role is out of scope. Source: blueprint sections 1 (roles) and 4; `docs/product/modules.md`.
- Q: What volume must the mechanism handle? → A: One operator and about 100 invited clients (each with up to 10 family accounts), mainboard IPOs only; on the order of hundreds of jobs and messages per day, with bursts on IPO closing and listing days. Source: CLAUDE.md (overview), BR-CLI-02, BR-IPO-01, blueprint section 9 (free-tier database).
- Q: Who notices if ticks stop altogether (the tick cannot report its own absence)? → A: This feature records every tick and exposes "last tick / overdue" status (FR-021); alerting from outside the system (uptime monitoring) belongs to Release & ops, feature 027. Source: `docs/product/modules.md` row 027 ("monitoring"); blueprint section 9 (UptimeRobot).
- Q: Are the tick authentication method, the job-queue technology, the database connection mode and the hosting access model part of this spec? → A: No. They are plan-level decisions recorded under "Notes for the architect". Source: owner direction for this feature; spec-writer rules (no implementation detail).

## User Scenarios & Testing *(mandatory)*

Actors: the **operator/admin** (sees and repairs failed work), **clients** (indirectly: they receive messages at sensible times and never get stale or duplicate ones), **owning features** (IPOs, tasks, reminders, settlement, payments) that schedule work and emit messages, and the **trusted cloud scheduler** that calls the tick every 5 minutes. All times below are IST unless marked otherwise.

### User Story 1 - Due work runs once in effect on every tick (Priority: P1)

Every 5 minutes the trusted scheduler triggers a tick. The tick finds every job whose due time has arrived, runs it, and records the outcome. Ticks may be late, skipped, duplicated or overlapping; whatever happens, each job's business effect happens exactly once.

**Why this priority**: Every time-driven behaviour in Divya (IPO status by dates, apply windows, reminders, due dates, overdue escalation) depends on this. Nothing else in the module works without it.

**Independent Test**: With a fake clock and sample job kinds, schedule jobs at different times, run ticks at chosen instants (including two ticks at the same instant and one tick that "crashes" mid-job) and check each job's recorded outcome and side effects.

**Acceptance Scenarios**:

1. **Given** a job due at 10:00 and the clock at 10:02, **When** a tick runs, **Then** the job runs once and is recorded as done with the time it finished.
2. **Given** a job due at 10:00 and the clock at 09:58, **When** a tick runs, **Then** the job does not run and remains pending.
3. **Given** a job due at 10:00, **When** the 10:00 tick is skipped entirely and the next tick runs at 10:05, **Then** the job runs at 10:05 (due means "due time has passed", not "due within the last 5 minutes").
4. **Given** 10 due jobs and two ticks that start at the same instant (overlap or duplicate trigger), **When** both ticks finish, **Then** each job ran exactly once and the combined counts of the two ticks add up to 10.
5. **Given** a tick that took a job and stopped before recording its outcome (simulated crash), **When** the job's allowed run time has passed and a later tick runs, **Then** the job is run again; no other tick ran it while its allowed run time was still open.
6. **Given** a job whose effect was applied but whose completion was not recorded (crash after the effect), **When** it is run again, **Then** the business state and the messages it produced are identical to a single run (no second message, no second state change).
7. **Given** no jobs are due, **When** a tick runs, **Then** it completes successfully, does no business work, and still records that the tick happened (this also keeps the hosted database active, ADR-0003).
8. **Given** more due jobs than one tick is allowed to process, **When** consecutive ticks run, **Then** jobs are processed oldest due time first and the backlog drains over the following ticks without any job running twice.
9. **Given** an owning feature schedules a job with the same business key as an existing pending job (for example "IPO 42 opens"), **When** it is scheduled again, **Then** there is still only one pending job for that key, carrying the latest due time and details.
10. **Given** a job is scheduled as part of a business change, **When** that business change is rolled back, **Then** no job exists; **When** it is committed, **Then** the job exists.

---

### User Story 2 - Only the trusted scheduler can trigger a tick (Priority: P1)

The tick entry point is reachable on the internet but must do nothing for anyone except the configured cloud scheduler identity.

**Why this priority**: An open tick could be used to force early deadlines, flood clients with messages or load the free database. Security is non-negotiable (constitution VI).

**Independent Test**: Call the tick entry point with no credentials, with malformed, expired, wrong-audience and wrong-identity credentials, and with valid credentials; check responses and that work ran only in the last case.

**Acceptance Scenarios**:

1. **Given** a request with no credentials, **When** it calls the tick, **Then** it is rejected as unauthenticated, no job runs, no message is relayed, and the rejection is logged without any credential content.
2. **Given** a request with credentials that are malformed, expired, issued for a different audience or not issued by the trusted identity provider, **When** it calls the tick, **Then** it is rejected as unauthenticated and no work is done.
3. **Given** valid credentials that belong to a different identity than the configured scheduler identity, **When** it calls the tick, **Then** it is rejected as forbidden and no work is done.
4. **Given** valid credentials for the configured scheduler identity, **When** it calls the tick, **Then** due work runs.
5. **Given** a staging or production environment, **When** the service is configured with the local/test-only way of triggering ticks, **Then** the service refuses to start (fails closed).
6. **Given** the public API description used to generate client code, **When** it is generated, **Then** the tick entry point and any other internal-only entry points do not appear in it.
7. **Given** individual jobs fail during an authenticated tick, **When** the tick finishes, **Then** it reports success to the caller (failures are retried by the queue itself); it reports failure only when it could not process at all (for example the database is unreachable).

---

### User Story 3 - Messages are recorded with the change that causes them and relayed by the tick (Priority: P1)

When a feature changes business state and something must be told to a client or the admin, the message is recorded in the outbox in the same all-or-nothing change. Each tick relays messages that are available, through a sender registered for the message's topic, and records the result.

**Why this priority**: Blueprint 7.3: "a reminder is never lost and never sent twice". Reminders (013), push (014), payments (019) and admin alerts all depend on it.

**Independent Test**: With a fake sender, perform business changes that commit and that roll back, run ticks, and check exactly which messages were handed to the sender and how often.

**Acceptance Scenarios**:

1. **Given** a business change that records a message, **When** the change commits, **Then** the message exists as pending; **When** the change rolls back, **Then** no message exists.
2. **Given** a pending message that is available now, **When** a tick runs, **Then** it is handed to the sender for its topic once and marked sent with the time.
3. **Given** two messages with the same recipient, topic and de-duplication key (for example "reminder for task 7, slot 14 Nov 10:00"), **When** both are recorded, **Then** only the first is kept and the second is silently ignored; the client receives it at most once.
4. **Given** a message that was handed to the sender but whose "sent" mark was lost (crash), **When** a later tick relays it again, **Then** it carries the same de-duplication key so the delivery side can recognise the repeat (delivery is at-least-once; collapsing repeats on the device is feature 014).
5. **Given** a pending, not yet sent message for a task reminder, **When** the owning feature withdraws messages for that task in the same change that marks the task done, **Then** the message is never sent and is recorded as withdrawn (supports BR-TASK-03 "stop as soon as the task completes").
6. **Given** a message whose topic has no registered sender, **When** a tick tries to relay it, **Then** it is treated as a failed attempt (User Story 6), never as sent.
7. **Given** messages for Android users and iPhone (home-screen web app) users, **When** they are recorded and relayed, **Then** the outbox treats them identically; choosing push, web push or email fallback per device is feature 014.
8. **Given** two ticks overlapping, **When** both relay messages, **Then** no message is handed to the sender twice by them.

---

### User Story 4 - IST business dates and the payment due date (Priority: P1)

Owning features need one trusted way to answer "what business date is this instant in India?", "what instant is 10:00 IST on this date?", "what is the date N weekdays later?" and "when is payment due for this listing day?".

**Why this priority**: GS-04 must always pass, and every schedule (windows, reminders, due dates) is expressed in IST. Mistakes here move money deadlines.

**Independent Test**: Table-driven checks of each helper with fixed instants, independent of the machine's time zone.

**Acceptance Scenarios** (BR-SET-08, GS-04):

1. **Given** listing day Friday 13 Nov 2026, **When** the due date is calculated, **Then** it is Tuesday 17 Nov 2026 at 23:59 IST (GS-04).
2. **Given** listing day Thursday 12 Nov 2026, **When** the due date is calculated, **Then** it is Monday 16 Nov 2026 at 23:59 IST (GS-04).
3. **Given** listing days Monday, Tuesday and Wednesday, **When** due dates are calculated, **Then** they are Wednesday, Thursday and Friday of the same week at 23:59 IST.
4. **Given** listing day Thursday 31 Dec 2026, **When** the due date is calculated, **Then** it is Monday 4 Jan 2027 at 23:59 IST (year end; 1 Jan is not skipped).
5. **Given** listing day Wednesday and an exchange holiday on Thursday, **When** the due date is calculated, **Then** it is Friday at 23:59 IST (exchange holidays are not skipped; owner decision).
6. **Given** listing day Friday 27 Feb 2026, **When** the due date is calculated, **Then** it is Tuesday 3 Mar 2026 at 23:59 IST (month end).
7. **Given** a listing day that is a Saturday or Sunday (defensive case, default D2), **When** the due date is calculated, **Then** it is the following Tuesday at 23:59 IST.
8. **Given** the instant 13 Nov 2026 19:00 UTC, **When** its IST business date is asked, **Then** it is 14 Nov 2026 (00:30 IST), regardless of the server's own time zone.
9. **Given** a business date and a wall-clock time such as 09:15, **When** the IST instant is asked, **Then** the result is that date at 09:15 IST (03:45 UTC).
10. **Given** a date and a number of weekdays N (0 to 10), **When** N weekdays are added, **Then** Saturdays and Sundays are skipped and nothing else is.

---

### User Story 5 - An IPO date change reschedules or cancels every dependent job (Priority: P2)

When the admin changes an IPO date (open, close, allotment or listing), all jobs that depended on the old date move to the new time or are cancelled, in the same change. If the change fails, nothing moves.

**Why this priority**: BR-IPO-03 states it directly. Without it, clients get "IPO opens today" on the wrong day and statuses flip at the wrong time. The IPO module (008) depends on this capability.

**Independent Test**: Schedule sample "IPO opens", "IPO closes" and reminder jobs for one IPO, change dates (commit and rollback), and check due times and tick outcomes with a fake clock.

**Acceptance Scenarios** (BR-IPO-03):

1. **Given** a pending job "IPO 42 opens" due 10 Nov 10:00, **When** the open date changes to 12 Nov 10:00 and the change commits, **Then** the job is due 12 Nov 10:00, and a tick on 10 Nov 10:05 does not run it.
2. **Given** the same date change, **When** it rolls back, **Then** the job is still due 10 Nov 10:00.
3. **Given** pending jobs for IPO 42 that no longer apply after a change (for example its apply-window reminders after the IPO is marked DO NOT APPLY), **When** the owning feature cancels the jobs for that IPO, **Then** they are recorded as cancelled and never run.
4. **Given** a date moved earlier to a time that has already passed, **When** the next tick runs, **Then** the job runs on that tick.
5. **Given** a job that is running at the moment its date is changed, **When** the run finishes, **Then** the new schedule is not lost: the job is pending again for the new time (and the run that was in progress used the current IPO data, not the data copied when the job was first scheduled).
6. **Given** a job for the same key that is already done (the IPO already opened), **When** the owning feature schedules the key again for a new date, **Then** a new pending job is created and the done job stays as history.
7. **Given** a reschedule or cancel request for a key that has no pending job, **When** it is made, **Then** nothing fails and nothing is created unless the owning feature asked for scheduling.

---

### User Story 6 - Failed work retries, then stops in a dead state the admin can see and retry (Priority: P2)

A job or message that fails is tried again later with increasing delays. After a maximum number of attempts it stops in a "dead" state. The admin can list failed and dead items, see why they failed, retry them, or cancel them.

**Why this priority**: The operator runs Divya alone; silent failures would mean missed reminders or IPO statuses that never move. The screens come in 022/023, but the capability must exist now.

**Independent Test**: Use sample job kinds and a fake sender that fail on demand; advance the fake clock through the retry schedule; then perform admin retry and cancel and check outcomes and audit entries.

**Acceptance Scenarios**:

1. **Given** a job that fails on its first attempt at 10:00, **When** the failure is recorded, **Then** it is pending again with a later due time, its attempt count is 1 and its last error is stored.
2. **Given** a job that keeps failing, **When** successive retries happen, **Then** each wait is longer than the previous one (up to a cap) and no retry happens before its due time.
3. **Given** a job that has failed its maximum number of attempts, **When** the last attempt fails, **Then** it becomes dead and no tick runs it again on its own.
4. **Given** dead and retrying jobs and messages in the admin's organisation, **When** the admin asks for the failed-work list, **Then** each item shows its kind or topic, the business item it concerns (for example IPO or task reference), attempts, first and last attempt times, next attempt time (if any), and the last error; it never shows secrets, tokens or unmasked PAN.
5. **Given** a dead job, **When** the admin retries it, **Then** it becomes pending with a fresh attempt count, runs on the next tick, and an audit entry records who retried it and when.
6. **Given** a dead job, **When** the admin cancels it, **Then** it is recorded as cancelled, never runs, and an audit entry records who cancelled it and when.
7. **Given** a dead time-sensitive job whose expiry has passed, **When** the admin retries it, **Then** the expiry still applies: it is recorded as expired, not delivered (the admin uses "send reminder now" on the Tasks board, feature 023, to send something fresh).
8. **Given** the scheduler has stopped (no tick recorded for more than 15 minutes), **When** the admin checks scheduler status, **Then** it shows the time of the last tick and that it is overdue.
9. **Given** items from another organisation, **When** the admin lists failed work, **Then** they are not shown.

---

### User Story 7 - Catch-up after downtime does not flood clients with stale reminders (Priority: P2)

If ticks stop for hours (cloud outage, deploy problem), the first ticks afterwards bring business state up to date but do not send reminders that are no longer true or useful.

**Why this priority**: A burst of "IPO closes in 1 hour" messages after the IPO closed would destroy client trust and break "never the same reminder twice" (blueprint 7.2).

**Independent Test**: Schedule a mix of state-change jobs, time-sensitive reminder jobs (each with an expiry) and messages; advance the fake clock 6 hours without ticks; run ticks and check what ran, what expired, and what was sent.

**Acceptance Scenarios**:

1. **Given** ticks stopped from 09:00 to 15:00 and an "IPO 42 closes" state-change job was due at 13:00, **When** ticks resume at 15:00, **Then** the job runs (late, not skipped).
2. **Given** hourly reminder jobs for slots 10:00 to 14:00, each expiring when the next slot starts, and ticks stopped from 09:00 to 14:30, **When** ticks resume at 14:30, **Then** only the 14:00 slot job runs; the 10:00 to 13:00 jobs are recorded as expired and produce no messages.
3. **Given** a pending message with expiry 12:00 that was never relayed because of the outage, **When** ticks resume at 15:00, **Then** it is recorded as expired and never handed to the sender.
4. **Given** a message or job with no expiry (for example an informational "new IPO announced" message), **When** ticks resume, **Then** it is relayed or run late.
5. **Given** a backlog larger than one tick may process, **When** ticks resume, **Then** state-change work and still-valid messages drain over the following ticks, oldest first, and each tick stays within its time budget.
6. **Given** a recurring job (for example "send due reminders"), **When** several of its runs were missed, **Then** it runs once on catch-up, not once per missed run.

---

### User Story 8 - Messages to clients respect quiet hours (Priority: P2)

Messages that respect quiet hours are never made available for delivery between 21:30 and 08:00 IST; they become available at the next 08:00 IST. Exempt messages are available immediately.

**Why this priority**: BR-TASK-03 requires reminders to respect quiet hours. Feature 013 decides which reminders exist; this feature guarantees the timing for every message.

**Independent Test**: Record messages at boundary instants with a fake clock and check their "available at" time and whether a tick relays them.

**Acceptance Scenarios** (BR-TASK-03, default quiet hours 21:30 to 08:00 IST):

1. **Given** a message that respects quiet hours recorded at 21:29, **When** the next tick runs, **Then** it is relayed.
2. **Given** such a message recorded at 21:30 Friday, **When** it is recorded, **Then** it is available from 08:00 Saturday, and ticks before then do not relay it.
3. **Given** such a message recorded at 02:00, **When** it is recorded, **Then** it is available from 08:00 the same day.
4. **Given** such a message recorded at exactly 08:00, **When** the next tick runs, **Then** it is relayed.
5. **Given** a message the owning feature marked exempt from quiet hours, recorded at 23:00, **When** the next tick runs, **Then** it is relayed.
6. **Given** a quiet-hours message deferred to 08:00 whose task was completed at 23:15, **When** the owning feature withdraws the task's messages, **Then** nothing is sent at 08:00.
7. **Given** a quiet-hours message deferred to 08:00 with an expiry of 07:00, **When** 08:00 arrives, **Then** it is recorded as expired and not sent.
8. **Given** the admin later changes quiet hours in Settings (UI in a later feature), **When** new messages are recorded, **Then** the new window applies; messages already deferred keep their computed time.
9. **Given** a job that is due during quiet hours (for example an IPO status change at 23:00), **When** its tick runs, **Then** the job runs; quiet hours delay only messages, never state changes.

---

### User Story 9 - Tests can time-travel through a whole IPO lifecycle (Priority: P1)

Every decision about "now" comes from one clock that tests can set and move. A test can walk an IPO from UPCOMING to SETTLED over two simulated weeks in seconds, running ticks at chosen instants.

**Why this priority**: Constitution III requires injected time. Every later feature (008, 012, 013, 018, 019) will prove its schedules with this harness; the automation blueprint's L2 test layer names it explicitly.

**Independent Test**: The time-travel scenario below runs in the automated suite against a real database without any real waiting.

**Acceptance Scenarios** (BR-IPO-03, BR-SET-08, GS-04):

1. **Given** a sample IPO with open Mon 9 Nov 10:00, close Wed 11 Nov 17:00, allotment Thu 12 Nov, listing Fri 13 Nov 10:00, and sample jobs for each step, a sample reminder series and a payment-due job at the calculated due date, **When** the test advances the clock in 5-minute ticks from Sun 8 Nov to Thu 19 Nov, **Then** each step job runs exactly once at the first tick at or after its time, the sample reminder messages never appear in quiet hours, and the payment-due job runs at the first tick after Tue 17 Nov 23:59 IST.
2. **Given** the same scenario, **When** the listing date is changed to Mon 16 Nov 10:00 on Thu 12 Nov, **Then** the listing job does not run on 13 Nov, runs on 16 Nov, and the payment-due job moves to Wed 18 Nov 23:59 IST and runs at the first tick after it.
3. **Given** a fake clock set to an instant, **When** any part of the scheduler, outbox or date helpers asks for the current time, **Then** it receives the fake clock's time; no part reads the machine clock.
4. **Given** the full scenario, **When** it runs in CI, **Then** it finishes in under 60 seconds.

---

### Edge Cases

- **Time windows and boundaries**: job due exactly at the tick instant runs on that tick; quiet-hours start (21:30) is inclusive and end (08:00) is exclusive; IST date changes at 00:00 IST, which is 18:30 UTC of the previous day; the server time zone never affects results (India has no daylight saving).
- **Ticks**: skipped, late, duplicated and overlapping ticks (User Story 1); a tick that runs past its time budget stops taking new work and leaves the rest for the next tick; a tick interrupted by a shutdown leaves its in-progress jobs to be recovered after their allowed run time.
- **Corrections**: date moved later, earlier, into the past, or changed twice before the job runs (only the latest time counts); admin retry of a dead job; admin cancel; reschedule while running (User Story 5).
- **Stop on completion**: task completed while its reminder is deferred by quiet hours or waiting for retry (withdrawn, never sent).
- **Downtime**: expired time-sensitive work is skipped and recorded as expired; state-change work always runs late; recurring work runs once, not once per missed run.
- **Failures**: unknown job kind or topic, sender unavailable, database unavailable during a tick (tick reports failure; nothing is half-recorded).
- **iPhone vs Android**: the outbox is channel-agnostic; per-device delivery and the iPhone email fallback are feature 014. Nothing in this feature depends on the client platform.
- **Multi-organisation**: every job and message belongs to one organisation; listing, retrying and cancelling never cross organisations.
- **Not applicable to this feature**: approvals pending and partial payments (no approvals or money are handled here; their owning features use the same mechanism).

## Requirements *(mandatory)*

### Functional Requirements

**Tick**

- **FR-001**: The system MUST expose one internal tick entry point that the trusted cloud scheduler triggers every 5 minutes (constitution, Technology Constraints; ADR-0003).
- **FR-002**: The tick MUST accept only callers that present valid credentials for the configured scheduler identity; it MUST reject missing or invalid credentials as unauthenticated and a different valid identity as forbidden, do no work for rejected calls, and never log credential contents.
- **FR-003**: A local/test-only way of triggering ticks MAY exist for development and automated tests; the service MUST refuse to start if it is configured in staging or production.
- **FR-004**: Internal entry points MUST NOT appear in the public API description or the generated client.
- **FR-005**: Each tick MUST run due jobs and then relay available messages, within a bounded time budget and a bounded amount of work per tick; remaining work waits for the next tick.
- **FR-006**: Each tick MUST be recorded (start, end, counts of jobs run, failed, expired; messages sent, failed, expired) so the admin can see the last tick time; every tick MUST touch the database even when idle (keeps the free database active, ADR-0003).
- **FR-007**: A tick MUST report success to its caller when it processed work (including recording individual failures) and failure only when it could not process at all.

**Scheduled jobs**

- **FR-008**: Owning features MUST be able to schedule a job of a registered kind, for one organisation, with a business key (for example "ipo:42:open"), a due time, optional details and an optional expiry, as part of their own all-or-nothing business change.
- **FR-009**: Scheduling a key that already has a pending job MUST update that job (latest due time and details win) instead of creating a second one.
- **FR-010**: A job MUST become eligible to run at the first tick at or after its due time and MUST NOT run before it.
- **FR-011**: A job MUST NOT be run by two ticks at the same time; if a tick stops before recording a job's outcome, the job MUST become eligible again after its allowed run time.
- **FR-012**: The mechanism MUST be at-least-once, and every job kind MUST be idempotent so that a repeated run has the same business effect as one run; jobs MUST read current business data at run time rather than relying on details copied when they were scheduled.
- **FR-013**: Owning features MUST be able to reschedule or cancel pending jobs by key, or by all keys for one business item (for example all jobs of IPO 42), within the same business change (BR-IPO-03). A reschedule that arrives while the job is running MUST NOT be lost.
- **FR-014**: A job whose expiry has passed when it would run MUST be recorded as expired and MUST NOT perform its effect; jobs without an expiry (state changes) MUST always run, however late.
- **FR-015**: Recurring work MUST run at most once per tick on catch-up, never once per missed occurrence.
- **FR-016**: Due jobs MUST be processed oldest due time first.

**Failures, dead state and admin capability**

- **FR-017**: A failed job or message MUST be retried later with increasing delays up to a cap, and MUST become dead after a maximum number of attempts; dead items MUST NOT be retried automatically.
- **FR-018**: For each failed or dead item the system MUST keep the attempt count, first and last attempt times, next attempt time and last error; errors MUST NOT contain secrets, tokens or unmasked PAN (constitution VI).
- **FR-019**: The admin MUST be able to list retrying and dead jobs and messages of their own organisation, filter by kind/topic and state, and see a count of dead items (for the Home warning in 022).
- **FR-020**: The admin MUST be able to retry a dead item (fresh attempt count, eligible on the next tick) or cancel it; each admin action MUST write an `audit_log` entry with the actor, time, item and before/after state (constitution II). Expiry still applies on retry.
- **FR-021**: The admin MUST be able to see the time of the last tick and whether it is overdue (more than 15 minutes, three missed ticks).
- **FR-022**: Admin access to these capabilities MUST be limited to the admin role once authentication exists (003); the screens are delivered in 022/023.

**Outbox**

- **FR-023**: Owning features MUST be able to record a message (organisation, topic, recipient, content reference, de-duplication key, quiet-hours flag, optional expiry) within their own all-or-nothing business change; if the change rolls back, the message MUST NOT exist.
- **FR-024**: A message with the same organisation, topic and de-duplication key as one already recorded MUST be ignored (blueprint 7.2 "never the same reminder twice"), for at least as long as messages are retained.
- **FR-025**: Each tick MUST relay available pending messages to the sender registered for their topic, mark them sent on success, and treat failures (including no registered sender) per FR-017.
- **FR-026**: Relay MUST be at-least-once, with the de-duplication key passed to the sender so that delivery can collapse repeats; overlapping ticks MUST NOT hand the same message to a sender at the same time.
- **FR-027**: Owning features MUST be able to withdraw not-yet-sent messages by key or by business item in the same change that makes them unnecessary (BR-TASK-03 stop on completion).
- **FR-028**: A message whose expiry has passed before it is sent MUST be recorded as expired and never handed to a sender.
- **FR-029**: This feature MUST ship the sender contract and a test sender only; real push, web push and email senders are feature 014.
- **FR-030**: Ordering is best effort: messages for one recipient are relayed in the order recorded within a tick; no stricter ordering is guaranteed.

**Quiet hours and time**

- **FR-031**: Each organisation MUST have quiet hours, defaulting to 21:30 to 08:00 IST every day (blueprint 7.2; default D3); a message that respects quiet hours and is recorded inside them MUST be available from the next end of quiet hours; exempt messages are available immediately. Quiet hours MUST NOT delay jobs.
- **FR-032**: All "now" decisions in the scheduler, outbox and date helpers MUST come from one injected clock that tests can set and advance (constitution III); no part may read the machine clock or the database clock for scheduling decisions.
- **FR-033**: The system MUST provide IST helpers: business date of an instant; instant of a date at an IST wall-clock time; add N weekdays (skip Saturday and Sunday only); and payment due instant = listing day + 2 weekdays at 23:59 IST (BR-SET-08, GS-04, defaults D1 and D2). Results MUST NOT depend on the server's time zone.
- **FR-034**: Every job and message MUST belong to exactly one organisation (multi-tenant ready, blueprint section 9).
- **FR-035**: Completed jobs and sent, expired or withdrawn messages MUST be cleaned up after a retention period (to fit the free database); dead items MUST be kept until the admin retries or cancels them.

### Key Entities

- **Scheduled job**: a unit of time-driven work. Organisation, kind, business key, details, due time, optional expiry, state (pending, running, done, failed-retrying, dead, cancelled, expired), attempt count, attempt times, last error, finished time.
- **Outbox message**: a notification or domain event waiting to be relayed. Organisation, topic, recipient, content reference, de-duplication key, quiet-hours flag, available-from time, optional expiry, state (pending, sent, failed-retrying, dead, withdrawn, expired), attempts, last error, sent time.
- **Tick record**: one run of the tick. Start and end time, counts of work done, failed and expired; used for "last tick" status.
- **Quiet-hours setting**: per organisation start and end (IST); default 21:30 to 08:00.
- **Clock**: the single source of "now" for scheduling; real time in production, settable and advanceable in tests.
- **Admin action on failed work**: retry or cancel, recorded in the existing `audit_log`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: In a simulated 30-day run with 100 clients, 10 IPOs and random duplicate, overlapping and skipped ticks plus injected crashes, zero jobs take effect twice, zero messages reach the sender twice except after a simulated crash between sending and recording, and zero due jobs are left unrun.
- **SC-002**: Under normal operation every due job runs within one tick interval (5 minutes) plus the tick's own duration of its due time.
- **SC-003**: After a simulated 6-hour outage with a backlog of 500 jobs, zero expired time-sensitive messages are sent, and every state-change job has run within 3 ticks (15 minutes) of ticks resuming.
- **SC-004**: 100% of tick calls without valid scheduler credentials are rejected and cause no work.
- **SC-005**: GS-04 and every due-date and IST helper case in User Story 4 pass on every commit.
- **SC-006**: The full IPO lifecycle time-travel scenario (two simulated weeks) completes in under 60 seconds in CI with no real waiting.
- **SC-007**: The admin can find every dead item with its reason in one list and requeue it; a requeued item runs on the next tick, and each such action appears in the audit log.
- **SC-008**: Zero quiet-hours messages become available for delivery between 21:30 and 08:00 IST.
- **SC-009**: Running the tick adds no hosting cost (stays inside the free tiers assumed in ADR-0003 and blueprint section 9).

## Non-goals

- Reminder rules: intensity levels, cadence, grouping per client, which reminders exist (feature 013).
- Delivery: Expo/FCM push, iPhone Web Push, email fallback, in-app inbox, notification preferences (feature 014).
- IPO lifecycle rules and admin override (feature 008), apply windows and MISSED tasks (012), deal cut-off locking (011), settlement due-date application (018), overdue escalation and admin overdue warning (019), festival greetings and broadcasts (021), data-worker refresh scheduling (026).
- Admin screens for failed jobs, scheduler status and quiet-hours settings (022/023); this feature provides the capabilities only.
- An exchange-holiday calendar (owner decision: weekdays only).
- Sub-5-minute precision and strict global message ordering.
- The nightly database backup (runs as a CI cron, blueprint 7.3), infrastructure provisioning of the scheduler job, and external uptime alerting (027).
- A Staff role with limited permissions.
- Constitution-wide exclusions: automated IPO applying, WhatsApp, SME IPOs, any broker or payment API that can move money.

## Assumptions

- Scale: about 100 clients with up to 10 accounts each; hundreds of jobs and messages per day, peaking on closing and listing days (see Clarifications).
- Alerting when ticks stop entirely is external monitoring in feature 027; this feature provides the "last tick" status it can check.
- Ticks are triggered by an external cloud scheduler that is at-least-once: it may skip, delay or duplicate calls (research.md section 2.2).
- Owning features name their own job kinds, topics, business keys and expiries; this feature enforces the mechanics only.
- Retry limits (default maximum attempts, delays and cap), the per-tick work budget, the allowed run time per job and the retention period are operational settings chosen in the plan; the spec requires only the behaviour.
- Feature 001 provides organisations and the append-only `audit_log` used for admin actions.
- Admin authentication (003) is not yet built; the admin capabilities are verified at service level until 022/023 expose them.
- The in-app inbox (014) is the client-facing history of notifications; the outbox is transport, not history.

## Notes for the architect (plan-level, not requirements)

Settled by the owner as plan decisions; record them in `plan.md`, not as spec requirements:

- Job-queue technology: in-repo table with row claiming (`FOR UPDATE SKIP LOCKED`) and leases versus River (research.md 2.3 recommends own table; ADR-0003 wording suggestion in research.md section 6).
- Tick authentication: OIDC token verification library choice (`google.golang.org/api/idtoken` vs `coreos/go-oidc`); a new runtime dependency needs owner approval per CLAUDE.md (research.md 2.1, open question 1).
- Hosting access model: public Cloud Run service with app-level OIDC checks on `/internal/*` versus IAM invoker-only (research.md 2.1, open question 5).
- Database pooler mode (Supabase session 5432 vs transaction 6543) and its effect on prepared statements (research.md 2.3, open question 3).
- Confirm in the plan that `scheduled_jobs` and `outbox` are exempt from `forbid_mutation()` (owner decision recorded above).
- Fake clock: research recommends no third-party clock library (research.md 2.5); IST loading via embedded tz data or fixed offset.
- Items marked VERIFY in research.md (Scheduler attempt deadline, Cloud Run timeout, Supabase connection limits, token claims) need checking before the plan is final.
