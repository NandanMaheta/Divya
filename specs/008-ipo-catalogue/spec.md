# Feature Specification: IPO catalogue (CRUD, mainboard only, APPLY / DO NOT APPLY, lifecycle by dates)

**Feature Branch**: `feat/008-ipo-catalogue` (folder `specs/008-ipo-catalogue/`)

**Created**: 2026-11-13

**Status**: Approved by owner (2026-11-13)

**Input**: User description: "Feature 008 IPO catalogue: CRUD, mainboard only, APPLY/DO NOT APPLY, lifecycle by dates (BR-IPO-01, BR-IPO-02, BR-IPO-03)."

**Module**: M3 IPOs (`docs/product/modules.md` row 008, depends on 002 scheduler; uses 003 auth for roles). Research: `specs/008-ipo-catalogue/research.md`.

**Business rules implemented**:

| Rule | What this feature delivers | What stays with other features |
| --- | --- | --- |
| BR-IPO-01 | Only mainboard IPOs can be stored. Anything listed on NSE Emerge, BSE SME or an unknown segment is rejected, with a dedicated error, on every create and edit. | 026 import must go through the same rejection (it creates IPOs only through this feature). |
| BR-IPO-02 | Every published IPO carries an admin flag, APPLY or DO NOT APPLY, plus an optional note. Clients see both flags. The feature defines "eligible for deals and tasks" (published and APPLY) and tells dependent features when eligibility changes. | Creating deals (010/011) and tasks (012) for eligible IPOs. |
| BR-IPO-03 | Lifecycle UPCOMING → OPEN → CLOSED → ALLOTMENT_OUT → LISTED → SETTLED moves automatically at fixed IST times on the IPO's dates. The admin can override it. A date change reschedules this feature's lifecycle steps in the same change and tells dependent features through a "dates changed" signal. Every status change sends a "status changed" signal. | Rescheduling reminders, apply windows and due dates (012, 013, 018) when they receive the signal. Automatic SETTLED is requested by 019. |

**Golden scenarios affected**: None directly. No money is computed, stored or moved by this feature. GS-04 depends on it indirectly, because the listing date entered here is the input to 018's due-date calculation (listing day + 2 weekdays). GS-01, GS-02, GS-03 and GS-04 must keep passing.

## Questions for the owner

None are blocking. The conductor defaults and derived defaults below answer every open point from research.md §5 (Q1 to Q6). Agents will use them unless you change them.

**Defaults flagged for one-time confirmation (default, owner may change)**:

- **D-01 Drafts and publishing.** A new IPO is an unpublished draft that only the admin can see. Publishing needs an explicit APPLY or DO NOT APPLY flag (optional note), an open date, a close date, a price band or final price, and a lot size. Clients see only published IPOs, with both flags shown (blueprint §2.1). *(Conductor default; settles research Q1 and Q6.)*
- **D-02 Lifecycle times (IST).** OPEN at 10:00 on the open date. CLOSED at 17:00 on the close date. ALLOTMENT_OUT at 10:00 on the allotment date (the admin can trigger it earlier). LISTED at 10:00 on the listing date. *(Conductor default; settles research Q2.)*
- **D-03 SETTLED.** An IPO becomes SETTLED automatically when feature 019 reports that every application is settled, or manually by the admin with a reason. Time never drives SETTLED. *(Conductor default; settles research Q3.)* Derived: an IPO with no applications, which includes every DO NOT APPLY IPO, stays LISTED until the admin marks it SETTLED.
- **D-04 Overrides.** The admin can move the status forward or backward, and every manual change needs a reason. A backward override never re-fires steps whose time has already passed. Steps whose time is still in the future fire as normal and move the lifecycle forward from the new state. *(Conductor default; settles research Q4. Requiring a reason for every manual change, not only SETTLED, is derived from research §1.3.)*
- **D-05 Validation.** These are hard blocks: any non-mainboard segment (BR-IPO-01) and wrong date order. One check is only a soft warning: a minimum application value (1 lot at the upper price) outside ₹10,000 to ₹20,000. *(Conductor default; settles research Q5.)*
- **D-06 Date suggestions.** When a close date T is entered, the system suggests allotment T+1, refund and demat credit T+2, and listing T+3. It counts weekdays only and does not know about exchange holidays. These are suggestions only and are never saved automatically. The admin's dates win. *(Conductor default; research §1.2.)*
- **D-07 Lifecycle runs for drafts too.** Status follows the dates whether or not the IPO is published, because it describes the market and not Divya's offer. Publishing only controls client visibility and eligibility. *(Derived from D-01 and D-02 so that publishing late never needs a separate catch-up.)*
- **D-08 Deleting.** A draft that was never published can be deleted. An IPO that has ever been published cannot be deleted, because later features reference it. It can only be unpublished. *(Derived; protects history per constitution II.)*
- **D-09 No "withdrawn" status.** If an issuer withdraws or postpones an IPO indefinitely, the admin unpublishes it or sets DO NOT APPLY with a note. The six statuses of BR-IPO-03 stay as they are. *(Derived; BR-IPO-03 lists exactly six statuses.)*
- **D-10 Note is client-visible.** The optional flag note is shown to clients next to the flag. Do not put private remarks in it. *(Derived from blueprint §2.1.)*

## Clarifications

### Session 2026-11-13

All answers come from the business rules, the blueprints, research.md or the conductor defaults. No owner input was needed.

- Q: Must an IPO carry a flag from the moment it is created? → A: No. A draft may have no flag yet. Publishing needs an explicit APPLY or DO NOT APPLY, and a published IPO can never lose its flag. Source: conductor default D-01; BR-IPO-02 ("every IPO has an admin flag" applies to IPOs that exist for clients, i.e. published ones).
- Q: Do clients see DO NOT APPLY IPOs? → A: Yes. Clients see both flags and the note, so they know the operator looked at the IPO and decided. Source: blueprint §2.1 ("Clients see both").
- Q: At what instant does each automatic status step happen, and how precisely? → A: At 10:00, 17:00, 10:00 and 10:00 IST on the open, close, allotment and listing dates (D-02). Each step runs on the first scheduler tick at or after that instant, so it is accurate to about 5 minutes. Steps whose time passed during downtime still run, late. Source: conductor default; 002 spec clarifications ("±5 minutes", "state-change work always runs late"); ADR-0003.
- Q: Does "a date change reschedules every dependent job" mean this feature moves reminders, tasks and due dates itself? → A: No. This feature reschedules its own lifecycle steps. It tells dependent features (012 tasks, 013 reminders, 018 due dates) about the date change in the same all-or-nothing change, and they move their own work. Modules never touch each other's data. Source: constitution IV; conductor default; 002 contract (scheduling calls require the caller's transaction).
- Q: Do T+n date suggestions skip exchange holidays? → A: No. They skip Saturday and Sunday only, like the payment due date. They are suggestions, and the admin types the official dates. Source: conductor default D-06; BR-SET-08 weekday convention; research §1.2.
- Q: How is an SME IPO recognised? → A: By the listing segment the admin (or a later import) states: NSE Emerge, BSE SME or any unknown segment is rejected. Price, size, ISIN or name are not used to recognise SME issues, because SME rules are changing. Source: BR-IPO-01; blueprint §2.1 ("excluded at the data level"); research §1.4.
- Q: Who may use these operations? → A: Creating, editing, publishing, flagging, overriding and deleting are admin-only (admin with second factor, per 003). Signed-in clients can only read published IPOs. Source: conductor default; 003 spec D-03 and FR-032.
- Q: Are the SEBI timing details in research.md marked [verify] (4 pm / 5 pm split, ICDR open-days and band-width limits) open questions? → A: No. They are assumptions. None of them is a validation rule in this feature. Source: conductor instruction.
- Q: Is the flag note shown to clients, or is it private to the admin? → A: It is shown to clients with the flag, because it explains the decision ("weak GMP"). Source: blueprint §2.1 (flag "plus a note such as 'weak GMP'"; "Clients see both, so they know you looked at the IPO and decided"); recorded as D-10.
- Q: Are dependent features told when the status changes, including admin overrides? → A: Yes. Every status change, automatic or manual, sends one "status changed" signal (from, to, actor) inside the same change. Tasks need it because BR-TASK-02 makes a missed task final after IPO close, and an admin can close an IPO early. Source: BR-TASK-02, BR-IPO-03; constitution IV (modules learn about each other's changes only through exported interfaces or events).
- Q: How are errors and warnings reported? → A: Every rejection and warning carries a stable, language-neutral code (for example "SME not allowed", "missing for publish", "date order", "stale version", "no change"), and the details name the fields concerned. Amounts in messages use Indian formatting (₹14,700) and times are IST. Source: constitution V (RFC 9457 problem details) and VII (Indian formatting); 003 spec FR-093 precedent.
- Q: What speed is required? → A: The platform budget applies: API responses p95 under 200 ms in Mumbai for IPO list and detail, at the expected volume (tens of IPOs a year, a few hundred stored). Source: constitution VII; CLAUDE.md (one operator, ~100 clients).

## User Scenarios & Testing *(mandatory)*

Actors: the **admin** (operator), **clients** (signed-in main members on the Android app or the iPhone PWA; both use the same read-only data), the **scheduler** (feature 002, which runs due lifecycle steps on each tick about every 5 minutes) and **dependent features** (010, 011, 012, 013, 018, 019, 026) that react to IPO changes. All times are IST. Amounts are shown in rupees and stored as exact paise.

Example IPO used below, unless a scenario says otherwise: "Acme Ltd", NSE and BSE mainboard, price band ₹1,000 to ₹1,050, lot size 14 shares (minimum application value ₹14,700). Dates: open Wed 11 Nov 2026, close Fri 13 Nov 2026, allotment Mon 16 Nov, refund and demat credit Tue 17 Nov, listing Wed 18 Nov.

### User Story 1 - Admin creates and edits an IPO draft (Priority: P1)

The admin records a new mainboard IPO as soon as it is announced, with whatever facts are known, and fills in the rest as documents arrive. Nothing is visible to clients yet.

**Why this priority**: Every later feature (deals, tasks, allotment, settlement) hangs off an IPO record. Without it nothing else can be built.

**Independent Test**: As admin, create a draft with only a name and segment, then edit fields one at a time. Check the stored values, warnings, errors and audit entries.

**Acceptance Scenarios** (BR-IPO-01, BR-IPO-03):

1. **Given** a signed-in admin, **When** they create an IPO with name "Acme Ltd" and segment NSE mainboard + BSE mainboard, and no other fields, **Then** the IPO is stored as an unpublished draft with status UPCOMING and no flag, and one audit entry records the creation with the admin as actor.
2. **Given** a draft, **When** the admin sets the price band ₹1,000 to ₹1,050 and lot size 14, **Then** the values are stored exactly (as paise and shares), the minimum application value shows ₹14,700, no warning is returned, and one audit entry records the old and new values of the changed fields.
3. **Given** a draft, **When** the admin enters lot size 50 with price band ₹480 to ₹500 (minimum application value ₹25,000), **Then** the save succeeds and returns the soft warning "minimum application value ₹25,000 is outside ₹10,000–₹20,000".
4. **Given** minimum application values of exactly ₹10,000 and exactly ₹20,000, **When** saved, **Then** no warning is returned. **Given** ₹9,999 or ₹20,001, **Then** the warning is returned and the save still succeeds.
5. **Given** a draft, **When** the admin sets the price band floor ₹1,050 and cap ₹1,000 (floor above cap), or only one end of the band, **Then** the save is rejected with a validation error naming the field, and nothing changes.
6. **Given** a draft, **When** the admin enters lot size 0, a negative price, or an amount with fractions of a paisa, **Then** the save is rejected and nothing changes.
7. **Given** a fixed-price issue, **When** the admin sets floor = cap = ₹500, **Then** it is accepted.
8. **Given** a draft created with idempotency key K, **When** the same create request with key K is repeated, **Then** exactly one IPO exists and the repeat returns the same IPO.
9. **Given** the admin loaded the IPO at version 3 on two devices and saved from one (now version 4), **When** they save from the other device based on version 3, **Then** that save is rejected as "changed since you loaded it", and the first save's values remain.
10. **Given** two IPOs with the same name, **When** the second is created, **Then** it is accepted (names are not unique; issuers can return years later).
11. **Given** an admin creating or editing an IPO, **Then** the record shows its data source "manual" and no last-synced time (fields kept for feature 026).

---

### User Story 2 - SME and other non-mainboard IPOs are rejected (Priority: P1)

Divya handles only mainboard IPOs. An SME IPO must never enter the system, whether typed in by hand or (later) imported.

**Why this priority**: BR-IPO-01 and the constitution's out-of-scope list. An SME record would let deals, tasks and money flows start for an excluded product.

**Independent Test**: Try to create and edit IPOs with every segment value, then check responses, error codes and that nothing was stored.

**Acceptance Scenarios** (BR-IPO-01):

1. **Given** a signed-in admin, **When** they create an IPO with segment NSE Emerge, **Then** it is rejected with the dedicated error "SME IPOs are not allowed", nothing is stored, and no lifecycle step is scheduled.
2. **Given** the same, **When** the segment is BSE SME, or NSE mainboard together with BSE SME, **Then** it is rejected the same way.
3. **Given** a segment value the system does not recognise, or no segment at all, **When** the admin creates the IPO, **Then** it is rejected (unknown never defaults to mainboard).
4. **Given** an existing mainboard IPO, **When** the admin edits its segment to NSE Emerge, **Then** the edit is rejected with the same error and the IPO is unchanged.
5. **Given** segments NSE mainboard only, BSE mainboard only, or both, **When** the admin creates the IPO, **Then** it is accepted.
6. **Given** any stored IPO, **When** the catalogue is inspected, **Then** no IPO has a non-mainboard segment. The storage itself refuses such a value even if the validation were bypassed.
7. **Given** an IPO name containing "SME" but with segment NSE mainboard, **When** saved, **Then** it is accepted. Recognition is by segment only.

---

### User Story 3 - Admin publishes with an APPLY or DO NOT APPLY decision (Priority: P1)

When the key facts are known, the admin decides APPLY or DO NOT APPLY, optionally adds a note such as "weak GMP", and publishes. From then on clients see the IPO and the decision. Only published APPLY IPOs are eligible for deals and tasks.

**Why this priority**: BR-IPO-02 is the operator's main decision per IPO, and publishing is the moment clients first see it.

**Independent Test**: Publish drafts with and without each required field and flag, flip flags, unpublish. Check the results, eligibility, signals to dependents, and audit entries.

**Acceptance Scenarios** (BR-IPO-02):

1. **Given** a draft with open and close dates, price band and lot size but no flag, **When** the admin publishes, **Then** publishing is rejected with "flag required", and the IPO stays a draft.
2. **Given** a draft with a flag but missing any one of open date, close date, (price band or final price), lot size, **When** the admin publishes, **Then** publishing is rejected with a list of every missing item, and the IPO stays a draft.
3. **Given** a draft with flag APPLY, note "strong anchor book", open and close dates, final price ₹1,050 (no band) and lot size 14, **When** the admin publishes, **Then** it is published, it becomes eligible for deals and tasks, dependent features are told "eligibility changed: now eligible" in the same change, and one audit entry records the publish.
4. **Given** a complete draft with flag DO NOT APPLY and no note, **When** published, **Then** it is published, visible to clients with the DO NOT APPLY flag, and not eligible for deals and tasks.
5. **Given** a published APPLY IPO, **When** the admin changes the flag to DO NOT APPLY with note "GMP collapsed", **Then** the flag and note change, eligibility becomes "not eligible", dependents are told in the same change, and one audit entry records the old and new flag and note.
6. **Given** a published IPO, **When** the admin edits only the note, **Then** the note changes, eligibility is unchanged, no eligibility signal is sent, and an audit entry is written.
7. **Given** a published IPO, **When** the admin tries to clear its flag, or clear its open date, close date, lot size or both price fields, **Then** the edit is rejected (a published IPO must keep meeting the publish requirements).
8. **Given** a published APPLY IPO, **When** the admin unpublishes it, **Then** clients stop seeing it on their next request, it is no longer eligible, dependents are told in the same change, and an audit entry records the unpublish. Its status and lifecycle steps are unaffected.
9. **Given** a draft whose minimum application value triggers the soft warning, **When** it is published, **Then** publishing succeeds and the warning is returned.
10. **Given** a dependent feature that fails while handling the eligibility signal, **When** the admin publishes, **Then** the whole publish is undone: the IPO stays a draft, no audit entry remains, and the admin gets an error.

---

### User Story 4 - Lifecycle moves automatically by date and time (Priority: P1)

Once an IPO has dates, its status changes on its own at the right IST times, with no admin action, even if the system was briefly down.

**Why this priority**: BR-IPO-03. Clients' "Open / Upcoming / Closed" views, task windows and listing-day actions all depend on the status being right.

**Independent Test**: With a fake clock and real scheduler ticks, step through the example IPO's dates and check the status, the actor and time of each change, and the audit entries.

**Acceptance Scenarios** (BR-IPO-03):

1. **Given** the example IPO in UPCOMING and the clock at Wed 11 Nov 09:59, **When** a tick runs, **Then** the status stays UPCOMING.
2. **Given** the clock at Wed 11 Nov 10:00 (or 10:04 with the 10:00 tick missed), **When** a tick runs, **Then** the status becomes OPEN, and the audit entry shows actor "system", from UPCOMING to OPEN, and the scheduled instant 11 Nov 10:00.
3. **Given** OPEN, **When** ticks run at Fri 13 Nov 16:59 and then 17:00, **Then** the status is still OPEN after the first and CLOSED after the second.
4. **Given** CLOSED, **When** a tick runs at Mon 16 Nov 10:00, **Then** the status becomes ALLOTMENT_OUT. **Given** ALLOTMENT_OUT, **When** a tick runs at Wed 18 Nov 10:00, **Then** it becomes LISTED.
5. **Given** LISTED, **When** ticks run on any later day, **Then** the status stays LISTED (time never sets SETTLED).
6. **Given** no ticks from Tue 10 Nov 09:00 until Mon 16 Nov 11:00, **When** ticks resume, **Then** the status ends at ALLOTMENT_OUT, the audit trail shows UPCOMING→OPEN, OPEN→CLOSED and CLOSED→ALLOTMENT_OUT in that order, and each step ran exactly once.
7. **Given** an IPO with open and close dates but no allotment date, **When** the clock passes the close time, **Then** it becomes CLOSED and stays CLOSED until an allotment date is entered or the admin overrides.
8. **Given** an unpublished draft with dates, **When** its dates pass, **Then** its status advances in the same way (D-07), and it is still invisible to clients.
9. **Given** the same lifecycle step is triggered twice (duplicate or overlapping ticks), **When** both finish, **Then** the status changed once and there is one audit entry.
10. **Given** a newly created IPO whose open date and 10:00 are already in the past, **When** the next tick runs, **Then** it becomes OPEN (and further steps follow if their times have passed).
11. **Given** a recording listener for the "status changed" signal, **When** any status change happens (automatic step or admin override), **Then** the listener receives exactly one signal with the IPO, from, to and actor inside the same change. A step that makes no change sends no signal. If the listener fails, the status change is undone and the step is retried by the scheduler (or the override returns an error).

---

### User Story 5 - Correcting dates reschedules everything that depends on them (Priority: P1)

Dates change: close dates get extended, listing moves by a day, or the admin fixes a typo. A date change must move this feature's lifecycle steps and tell every dependent feature, in the same change.

**Why this priority**: BR-IPO-03 ("A date change reschedules every dependent job"). Wrong dates mean wrong reminders, wrong task windows and wrong due dates.

**Independent Test**: With a fake clock, change dates before and after their steps ran (both committed and rolled back), with a recording listener for the "dates changed" signal. Check step times, status outcomes, signals and audit entries.

**Acceptance Scenarios** (BR-IPO-03):

1. **Given** the example IPO in UPCOMING on Tue 10 Nov, **When** the admin moves the open date to Thu 12 Nov, **Then** the OPEN step is due 12 Nov 10:00, a tick at 11 Nov 10:05 leaves it UPCOMING, and a tick at 12 Nov 10:00 opens it.
2. **Given** the example IPO OPEN on Fri 13 Nov 12:00, **When** the admin extends the close date to Tue 17 Nov (and moves the later dates accordingly), **Then** a tick at 13 Nov 17:05 leaves it OPEN, and a tick at 17 Nov 17:00 closes it.
3. **Given** an IPO OPEN at Fri 13 Nov 12:00, **When** the admin moves the close date to Thu 12 Nov (already past), **Then** the next tick closes it.
4. **Given** any edit that changes at least one of the six dates (open, close, allotment, refund, demat credit, listing), **When** it commits, **Then** dependent features receive one "dates changed" signal with the IPO and the old and new values of all six dates, inside the same change. Edits that change no date send no such signal.
5. **Given** a date change and a dependent feature that fails while handling the "dates changed" signal, **When** the admin saves, **Then** the whole edit is undone: old dates, old step times, no audit entry, and an error for the admin.
6. **Given** a date change whose save fails for any other reason, **When** it rolls back, **Then** the lifecycle steps keep their old times.
7. **Given** an IPO in OPEN, **When** the admin moves its open date into the future (postponement after opening), **Then** the status stays OPEN (automatic changes never move backwards). The admin can override it back to UPCOMING (User Story 6), and the open step then fires on the new date.
8. **Given** an IPO with an allotment date, **When** the admin clears the allotment date, **Then** the ALLOTMENT_OUT step is cancelled, and no status change happens on the old date.
9. **Given** dates out of order (for example open after close, allotment on the close date, or listing before allotment), **When** the admin saves, **Then** the save is rejected with an error naming the conflicting dates, and nothing changes. Open and close on the same day is accepted.
10. **Given** a date change, **When** it commits, **Then** one audit entry records the old and new values of every changed date.
11. **Given** an edit to a non-date field only, **When** it commits, **Then** no lifecycle step is rescheduled.

---

### User Story 6 - Admin overrides the lifecycle (Priority: P2)

Sometimes reality runs ahead of the dates (the registrar publishes allotment early) or a status was wrong. The admin can set any status with a reason, and the schedule then carries on sensibly.

**Why this priority**: BR-IPO-03 ("the admin can override"). It is needed less often than the automatic flow, but without it a wrong status could not be fixed.

**Independent Test**: With a fake clock, apply forward and backward overrides at chosen instants. Then run ticks and check status, steps that fire or do not fire, and audit entries.

**Acceptance Scenarios** (BR-IPO-03):

1. **Given** the example IPO CLOSED on Fri 13 Nov 19:00, **When** the admin sets ALLOTMENT_OUT with reason "registrar published early", **Then** the status is ALLOTMENT_OUT immediately, marked as set by the admin, with the reason in the audit entry. The 16 Nov 10:00 step then makes no change and writes no audit entry.
2. **Given** an IPO OPEN on Thu 12 Nov 12:00 (open step ran on 11 Nov), **When** the admin sets UPCOMING with reason "opened by mistake", **Then** ticks on 12 Nov leave it UPCOMING (the past open step does not re-fire), and the close step at Fri 13 Nov 17:00 moves it to CLOSED.
3. **Given** the backward override above, **When** the admin then moves the open date to Fri 13 Nov (a future date), **Then** the open step fires at 13 Nov 10:00 and the status becomes OPEN.
4. **Given** a manual status change without a reason, **When** submitted, **Then** it is rejected.
5. **Given** a manual change to the status the IPO already has, **When** submitted, **Then** it is rejected as "no change", and no audit entry is written.
6. **Given** an IPO LISTED, **When** the admin marks it SETTLED with reason "all payments received", **Then** it becomes SETTLED, with an audit entry carrying the reason.
7. **Given** a LISTED IPO, **When** feature 019 reports that every application of the IPO is settled, **Then** the IPO becomes SETTLED with actor "system" and an audit entry. **Given** an IPO not yet LISTED, **When** 019 makes that report, **Then** the status does not change and 019 is told the IPO is not listed yet.
8. **Given** a LISTED IPO with no applications (for example DO NOT APPLY), **When** time passes, **Then** it stays LISTED until the admin marks it SETTLED (D-03).
9. **Given** the admin list, **When** it shows an IPO, **Then** it shows whether the current status came from the schedule or from an admin override.

---

### User Story 7 - Clients see only published IPOs (Priority: P1)

A signed-in client, on the Android app or the iPhone PWA, can browse published IPOs (open, upcoming, closed and so on) with the APPLY or DO NOT APPLY decision and key facts. They never see drafts or admin-only details, and they cannot change anything.

**Why this priority**: This is the client-facing half of BR-IPO-02, and it is a privacy boundary.

**Independent Test**: Seed drafts and published IPOs. As a client, list and fetch them, then call every admin operation. Check the results.

**Acceptance Scenarios** (BR-IPO-02):

1. **Given** 3 published IPOs (APPLY OPEN, DO NOT APPLY UPCOMING, APPLY LISTED) and 2 drafts, **When** a signed-in client lists IPOs, **Then** exactly the 3 published IPOs are returned, each with name, segment, dates, price band or final price, lot size, minimum application value, status, flag and note.
2. **Given** the same, **When** the client filters by status OPEN, **Then** only the APPLY OPEN IPO is returned.
3. **Given** a draft's identifier, **When** a client requests it, **Then** the response is "not found", the same as for an identifier that does not exist (existence is not revealed).
4. **Given** a published IPO, **When** a client fetches it, **Then** admin-only details (data source, source reference, last-synced time, audit history, override reasons) are not included.
5. **Given** an IPO a client fetched while it was published, **When** the admin unpublishes it, **Then** the client's next list or fetch does not return it.
6. **Given** a signed-in client, **When** they call any create, edit, publish, unpublish, flag, override, delete or date-suggestion operation, **Then** it is rejected as forbidden, and nothing changes.
7. **Given** no sign-in, **When** anyone calls any IPO operation, **Then** it is rejected as unauthenticated.
8. **Given** the same client on Android and on the iPhone PWA, **When** they list IPOs at the same moment, **Then** both receive identical data (no device-specific behaviour in this feature).

---

### User Story 8 - Admin gets date suggestions from the close date (Priority: P3)

When the admin enters the close date T, the system offers the usual T+1 / T+2 / T+3 dates to save typing. The admin accepts, edits or ignores them.

**Why this priority**: It is a convenience. Official dates always come from exchange notices.

**Independent Test**: Request suggestions for close dates on each weekday and check the results and that nothing was saved.

**Acceptance Scenarios** (BR-IPO-03):

1. **Given** close date Fri 13 Nov 2026, **When** the admin asks for suggestions, **Then** they get allotment Mon 16 Nov, refund and demat credit Tue 17 Nov, listing Wed 18 Nov, together with the notice "weekdays only; check exchange holidays".
2. **Given** close date Mon 9 Nov 2026, **Then** the suggestions are allotment Tue 10, refund and credit Wed 11, and listing Thu 12 Nov.
3. **Given** close date Wed 11 Nov 2026, **Then** the suggestions are allotment Thu 12, refund and credit Fri 13, and listing Mon 16 Nov.
4. **Given** a suggestion request, **When** it returns, **Then** the IPO is unchanged, no lifecycle step is scheduled and no audit entry is written.
5. **Given** suggestions that the admin overrides with other valid dates, **When** saved, **Then** the admin's dates are stored.

---

### User Story 9 - Admin lists, filters and deletes IPOs (Priority: P2)

The admin needs to find IPOs quickly by status, publication, flag and month, and to remove drafts that were created by mistake.

**Why this priority**: This is basic catalogue management. The screens are in feature 023, but the capabilities are here.

**Independent Test**: Seed a mix of IPOs, run each filter, delete drafts and published IPOs, and check results and audit entries.

**Acceptance Scenarios** (BR-IPO-02, BR-IPO-03):

1. **Given** drafts and published IPOs across statuses, **When** the admin filters by status CLOSED, by published or draft, by flag APPLY / DO NOT APPLY / not set, or by open-date month (IST) November 2026, **Then** each filter returns exactly the matching IPOs. The default order is open date, newest first, with IPOs that have no open date first.
2. **Given** a never-published draft with scheduled lifecycle steps, **When** the admin deletes it, **Then** it disappears from all lists, all its lifecycle steps are cancelled, and an audit entry keeps a record of what was deleted.
3. **Given** an IPO that is or was ever published, **When** the admin tries to delete it, **Then** the deletion is rejected with "published IPOs can only be unpublished" (D-08).

---

### Edge Cases

- **Time-window boundaries**: steps fire at the exact instant (10:00:00 or 17:00:00 IST) or on the first tick after it. A tick one minute before does nothing (US4.1, US4.3). Steps are tied to the IST calendar date and never shift by a day for evening UTC times.
- **Several steps passed at once** (downtime, a newly created past IPO, or a date moved into the past): steps run in order and each is audited once (US4.6, US4.10, US5.3).
- **Date moved later after its step already ran**: status never moves backwards automatically. The admin overrides if needed (US5.7).
- **Close date extended while OPEN**: closing moves to the new date (US5.2).
- **Missing later dates**: the lifecycle waits in the last reachable status (US4.7).
- **Backward override**: past steps do not re-fire. Future steps do (US6.2). Changing a date re-arms that step (US6.3).
- **Forward override ahead of schedule**: the later automatic step makes no change and writes no audit entry (US6.1).
- **Approvals pending**: none in this feature. Fixed-pick approvals belong to 011 and are not affected by IPO edits here, except through the signals.
- **Partial payments**: not in this feature. Automatic SETTLED waits for 019's "every application settled" report. Partial payments never trigger it (US6.7).
- **IPO with no applications / DO NOT APPLY**: stays LISTED until the admin settles it manually (US6.8).
- **iPhone vs Android**: no difference. Both clients read the same data (US7.8). Screens are out of scope.
- **Corrections**: flag correction (US3.5), date correction (US5), status correction (US6), and stale edits from a second device are rejected (US1.9).
- **Fixed-price issue**: floor = cap is valid (US1.7). A final price outside the band is accepted (no rule forbids it here).
- **Unpublish after clients saw it**: hidden on the next read (US7.5). What happens to existing deals and tasks is decided by 010/012 when they receive the eligibility signal.
- **Issuer withdraws the IPO**: unpublish or DO NOT APPLY with a note. There is no extra status (D-09).
- **Weekend or holiday dates typed by the admin**: accepted. Only the order of dates is validated.
- **Duplicate names**: allowed (US1.10).

## Requirements *(mandatory)*

### Functional Requirements

**Catalogue records**

- **FR-001**: The admin MUST be able to create an IPO with only a name and a listing segment. Every other field is optional until publishing (FR-020).
- **FR-002**: An IPO MUST hold: name; listing segment (NSE mainboard, BSE mainboard, or both); optional NSE symbol; issue size; price band floor and cap; final issue price; lot size (shares); open, close, allotment, refund, demat credit and listing dates (IST calendar dates); registrar; lead managers; offer-document link; flag (APPLY, DO NOT APPLY, or not set while a draft); flag note; published or draft; whether it was ever published; status; whether the status was set by the schedule or by the admin; data source (manual by default), source reference and last-synced time (kept for 026); and a version for detecting stale edits.
- **FR-003**: All amounts MUST be exact whole paise and never fractional. Price band floor ≤ cap, and both ends are set together or not at all. Prices, issue size and lot size MUST be positive.
- **FR-004**: The system MUST compute the minimum application value as lot size × final price, or lot size × price band cap when there is no final price, exactly in paise. If neither price is set, the value is not shown.
- **FR-005**: When the minimum application value is below ₹10,000 or above ₹20,000 (bounds inclusive = no warning), a save or publish MUST succeed and return a soft warning stating the value and the range. This is the only soft warning in this feature.
- **FR-006**: An edit based on an out-of-date version MUST be rejected without changing anything.
- **FR-007**: Create and other mutations MUST accept an idempotency key. A repeated request with the same key MUST NOT create or apply a second time.
- **FR-008**: The admin MUST be able to delete a never-published draft. Deleting cancels all its lifecycle steps. An IPO that has ever been published MUST NOT be deletable.
- **FR-009**: The admin MUST be able to list IPOs filtered by status, published or draft, flag (including not set) and open-date month (IST), ordered by open date newest first with undated IPOs first, and to fetch any single IPO.

**Mainboard only (BR-IPO-01)**

- **FR-010**: The system MUST reject, with a dedicated "SME not allowed" error, any create or edit whose segment includes NSE Emerge or BSE SME. It MUST reject any unknown or missing segment with a validation error. Nothing is stored in either case.
- **FR-011**: Stored data MUST be unable to hold a non-mainboard segment, even if the request validation were bypassed.
- **FR-012**: SME recognition MUST rely on the stated segment only, never on price, size, name, symbol or ISIN.

**Flag and publishing (BR-IPO-02)**

- **FR-020**: Publishing MUST require all of: a flag (APPLY or DO NOT APPLY), an open date, a close date, a price band (both ends) or a final price, and a lot size. A rejection MUST list every missing item.
- **FR-021**: While published, an IPO MUST keep meeting FR-020. Edits that would break it are rejected.
- **FR-022**: The admin MUST be able to set or change the flag and its note (up to 500 characters) on drafts and published IPOs, and to unpublish a published IPO.
- **FR-023**: An IPO is "eligible for deals and tasks" exactly when it is published and flagged APPLY. Dependent features MUST be able to ask whether an IPO is eligible.
- **FR-024**: Whenever eligibility changes (publish, unpublish or flag change), dependent features MUST be told, within the same all-or-nothing change, with the IPO and its new eligibility. If a dependent fails, the whole change is undone.

**Lifecycle (BR-IPO-03)**

- **FR-030**: Status MUST be one of UPCOMING, OPEN, CLOSED, ALLOTMENT_OUT, LISTED, SETTLED, in that order. A new IPO starts UPCOMING.
- **FR-031**: The system MUST move status automatically: to OPEN at 10:00 IST on the open date, to CLOSED at 17:00 IST on the close date, to ALLOTMENT_OUT at 10:00 IST on the allotment date, and to LISTED at 10:00 IST on the listing date. Each step runs on the first scheduler tick at or after its instant, including late after downtime. Steps whose date is not set do not run.
- **FR-032**: An automatic step MUST only move status forward. It sets the step's target status when the current status is earlier, and otherwise does nothing (no change, no audit entry). It MUST read the IPO's current dates and status when it runs, and do nothing if its date has since moved later.
- **FR-033**: Time MUST NOT move an IPO to SETTLED. SETTLED is set (a) when feature 019 reports that every application of a LISTED IPO is settled, or (b) by the admin with a reason. A 019 report for an IPO that is not LISTED MUST change nothing and say why.
- **FR-034**: Status MUST advance by dates for drafts and published IPOs alike (D-07).
- **FR-035**: The admin MUST be able to set any status, forward or backward, with a mandatory reason. Setting the current status is rejected as "no change".
- **FR-036**: After a manual status change, steps whose instant is at or before the moment of the override MUST NOT run again unless their date is later changed. Steps whose instant is later stay scheduled and fire under FR-032.
- **FR-037**: Every status change (automatic or manual) MUST send dependent features one "status changed" signal with the IPO, from, to and actor, within the same all-or-nothing change. If a dependent fails, the status change is undone.

**Dates and rescheduling (BR-IPO-03)**

- **FR-040**: Considering only the dates that are set, they MUST follow this order: open ≤ close < allotment ≤ refund ≤ listing, allotment ≤ demat credit ≤ listing, and allotment < listing. Violations are rejected with an error naming the dates. No other date checks apply (weekends and holidays are allowed).
- **FR-041**: When an edit changes a date, in the same all-or-nothing change, the system MUST: schedule the matching lifecycle step at its new instant (it runs on the next tick if that instant is already past); cancel the step when its date is cleared; leave the steps of unchanged dates alone; and write the audit entry. Refund and demat credit dates have no lifecycle step.
- **FR-042**: When an edit changes any of the six dates, dependent features MUST receive one "dates changed" signal with the IPO and the old and new values of all six dates, within the same all-or-nothing change. If a dependent fails, the whole edit is undone. Creating an IPO does not send this signal.
- **FR-043**: The admin MUST be able to request date suggestions for a close date T: allotment T+1, refund T+2, demat credit T+2, listing T+3, counting weekdays only (Saturday and Sunday skipped), with a "check exchange holidays" notice. The request MUST NOT change anything.

**Access**

- **FR-050**: Create, edit, delete, publish, unpublish, flag, status override, list-all and date-suggestion operations MUST be admin-only (admin who completed the second factor, per 003). Everyone else is rejected as forbidden or unauthenticated.
- **FR-051**: Signed-in clients MUST be able to list published IPOs (filterable by status) and fetch a published IPO. An unpublished or unknown identifier MUST return "not found". Client responses MUST exclude data source, source reference, last-synced time, override reasons and audit history.

**Audit**

- **FR-060**: Every create, edit, delete, publish, unpublish, flag or note change, and status change MUST write an audit entry in the same change, recording actor (admin or system), time, action, and the before and after values of the changed fields. Automatic status changes record the step and its scheduled instant. Manual status changes record the reason. A step that makes no change writes no entry.

**Errors and warnings**

- **FR-061**: Every rejection and soft warning MUST carry a stable, language-neutral code and name the fields concerned. At minimum: SME not allowed, invalid segment, invalid value, date order, missing for publish, published IPO cannot be deleted, stale version, no change, not listed yet, forbidden, unauthenticated, not found. Amounts in messages use Indian formatting and times are IST.

### Key Entities

- **IPO**: one mainboard public issue. It holds the identity and segment, offer and price facts, the six key dates, the admin flag and note, publication state, lifecycle status and its source, data provenance, and a version. Referenced later by deals, tasks, allotments, settlements and GMP history.
- **Lifecycle step**: the planned automatic move to OPEN, CLOSED, ALLOTMENT_OUT or LISTED for one IPO, due at a fixed IST time on one of its dates. There is at most one pending step per IPO and target. Steps are held by the scheduler (002).
- **Status change**: one move of an IPO's status (from, to, actor, time, scheduled instant or reason). It is recorded in the audit log.
- **Dependent-feature signals**: "dates changed" (old and new dates), "eligibility changed" (published and APPLY or not) and "status changed" (from, to, actor), delivered inside the change that causes them.
- **Date suggestion**: a computed, unsaved set of allotment, refund, credit and listing dates from a close date.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 0 IPOs with a non-mainboard segment can be stored. Every SME and unknown-segment test case is rejected, on both create and edit.
- **SC-002**: In time-travel tests over a full example lifecycle, 100% of automatic status changes happen on the first tick at or after their IST instant (within 5 minutes), and none happens before it.
- **SC-003**: After any date correction, 100% of this feature's lifecycle steps match the new dates, and every registered dependent received exactly one "dates changed" signal in the same change. After a failed or rolled-back change, 0 steps moved.
- **SC-004**: 0 unpublished IPOs are returned to clients, across every client list, filter and fetch in the test suite.
- **SC-005**: 100% of mutations in the test suite (create, edit, delete, publish, unpublish, flag, status change) have exactly one matching audit entry. No-op steps have none.
- **SC-006**: The admin can create an IPO from an RHP and publish it with a flag in under 3 minutes of data entry, using the date suggestions.
- **SC-007**: 100% of admin-only operations reject client and unauthenticated callers.
- **SC-008**: IPO list and detail requests for admin and clients meet the platform budget (constitution VII: p95 under 200 ms in Mumbai) with 500 IPOs stored.

## Non-goals

- GMP, expected profit and its history; subscription figures (feature 009).
- Automated IPO import, subscription refresh and AI company briefs (feature 026). Only the data-source, source-reference and last-synced fields are kept.
- Deals and quotes (010, 011); apply windows and tasks (012); reminders and notifications, including "new IPO announced" (013, 014); allotment and listing price entry (015); settlement and due dates (016 to 018); payments (019).
- All screens: admin IPO pages (022, 023) and client IPO screens (025).
- SME IPOs of any kind; automated applying; WhatsApp; any broker or payment API.
- An exchange-holiday calendar; bulk editing; a "withdrawn" status; staff roles; quotas, company financials and other descriptive fields beyond FR-002.

## Assumptions

- Feature 002 (scheduler: keyed steps, reschedule and cancel within the caller's change, catch-up of state-change work, about 5-minute ticks) and feature 003 (roles `admin` and `client`, admin second factor) are available before implementation.
- One operator and about 100 clients. Tens of IPOs a year, so volume is not a concern.
- SEBI and exchange timing details marked [verify] in research.md are assumptions. They do not drive any validation here.
- The schema change adds new data only. It does not change ledger or auth data, and is reviewed with the PR as for every migration.

## Notes for the architect (plan-level, not requirements)

- Migration number: the next free one after 00003, i.e. 00004.
- Scheduler API: read-only reference `/home/user/divya-002/specs/002-scheduler-tick/contracts/go-module-api.md` (keyed Schedule / Cancel / CancelSubject inside the caller's transaction; state-change jobs without expiry).
- Expose the "dates changed" signal as a `DatesChanged` hook (plus eligibility-changed and status-changed hooks) that 010/011, 012, 013, 018 and 019 register for, called in-transaction.
- Research §1.3 to §1.4 holds the recommended step-handler shape, SME defence in depth and the rejected alternatives.

## Owner approval

Summary for approval (10 lines):

1. 008 builds the IPO catalogue: create, edit, delete drafts, publish/unpublish, APPLY / DO NOT APPLY + note (BR-IPO-02).
2. Only mainboard: NSE Emerge, BSE SME or unknown segments are rejected on every save (BR-IPO-01).
3. New IPOs are admin-only drafts; publishing needs a flag, open and close dates, price band or final price, and lot size.
4. Clients see only published IPOs, with both flags and the note; drafts look "not found" to them.
5. Status moves itself (IST): OPEN 10:00 on open date, CLOSED 17:00 on close date, ALLOTMENT_OUT 10:00, LISTED 10:00 (BR-IPO-03).
6. SETTLED never by time: automatic when 019 reports all applications settled, or by you with a reason.
7. You can override any status with a reason; past steps never re-fire, future ones carry on.
8. A date change moves the lifecycle steps and tells tasks, reminders and due dates (012/013/018) in the same change; status and APPLY changes are signalled too.
9. Only hard checks: mainboard and date order. Soft warning only: minimum application outside ₹10,000–₹20,000. T+1/T+2/T+3 date suggestions skip weekends only.
10. Everything is audited. No owner questions are open; please confirm defaults D-01 to D-10 or change any.

## Owner approval

- **Approved**: 2026-11-13 by the owner (gate A), with defaults D-01…D-10 as proposed.
