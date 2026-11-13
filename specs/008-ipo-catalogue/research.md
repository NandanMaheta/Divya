# Research: 008 IPO catalogue (CRUD, mainboard only, APPLY / DO NOT APPLY, lifecycle by dates)

Rules: BR-IPO-01, BR-IPO-02, BR-IPO-03 (`docs/product/business-rules.md`). Builds on 002 (scheduler, outbox, clock, `istdate`) per `/home/user/divya-002/specs/002-scheduler-tick/{plan,data-model}.md` and `contracts/go-module-api.md` (002's `plan.md` is still the empty template, so the binding API is `go-module-api.md`). Feeds 009 (GMP history) and 026 (worker import).

Research limits: this session could only reach the web search tool. `taxguru.in` and `ipomarket.in` could not be fetched (host blocked), so SEBI/exchange claims rest on search snippets and the repo's own `research-report.md`. Claims marked **[verify]** must be checked against the ICDR text or exchange circulars before they become hard validation. Nothing here needs a new library: no external dependency is required for 008 (Go, pgx, sqlc, Huma and TanStack are already in the stack, so no versions to pin).

## 1. External needs and domain facts

### 1.1 Fields of a mainboard IPO record, and when each is known

| Group | Field (column) | Type | Known at |
|---|---|---|---|
| Identity | `name`, `slug` | text | DRHP |
| | `issuer_cin` (optional) | text | DRHP |
| | `exchanges` (`NSE`, `BSE`, or both) | text[] CHECK subset | DRHP (proposed), RHP (final) |
| | `symbol` | text null | exchange circular, shortly before listing |
| | `isin` | text null, CHECK `^IN[A-Z0-9]{10}$` | RHP / allotment |
| | `board` | text CHECK `= 'MAINBOARD'` | always (see 1.4) |
| Offer | `fresh_issue_paise`, `ofs_paise` (bigint paise, either may be 0) | bigint | DRHP (approx), RHP (final) |
| | `issue_size_paise` | generated or checked `= fresh + ofs` | RHP |
| | `face_value_paise` | bigint | RHP |
| Price | `price_band_floor_paise`, `price_band_cap_paise` | bigint | price band announcement (at least 2 working days before open, see 1.2) |
| | `issue_price_paise` (final, cut-off) | bigint null | at/after close (basis of allotment) |
| | `lot_size` (shares) | int null | price band announcement |
| | derived, not stored: `retail_min_lots = 1`, `retail_max_lots = floor(2,00,000 / (lot_size x cap))`, lot value at cap | | |
| Dates (IST civil `date`) | `open_date`, `close_date` | date null | RHP / price band announcement |
| | `allotment_date` (basis of allotment), `refund_date` (ASBA unblock / refund), `demat_credit_date`, `listing_date` | date null | RHP "indicative timetable" (suggested from T), confirmed in announcements |
| | `bid_close_time` (optional override, default 17:00 IST) | time null | RHP |
| Quotas (basis points of the offer, not floats) | `qib_bps`, `retail_bps`, `snii_bps`, `bnii_bps`, `employee_bps`, `shareholder_bps` (or shares count for employee/shareholder) | smallint null | RHP |
| Parties | `registrar`, `lead_managers` (text[]), `rhp_url`, `drhp_url`, `exchange_notice_url` | text | DRHP (managers, registrar), RHP (RHP link) |
| Company | `about` (markdown), `strengths`, `risks`, `financial_summary` (jsonb) | | filled by admin now; 026 AI brief later |
| Admin flag (BR-IPO-02) | `apply_flag` enum `APPLY` / `DO_NOT_APPLY` (NOT NULL, default `DO_NOT_APPLY`), `apply_note` text null | | admin decision; default must be explicit, see open question Q1 |
| Lifecycle (BR-IPO-03) | `status` (6 values), `status_changed_at`, `status_source` (`schedule` / `admin`) | | system |
| Provenance (for 026) | `source` (`manual`, `import:<provider>`), `source_ref`, `last_synced_at`, `field_sources` jsonb (per-field origin) | | system |
| Housekeeping | `org_id`, `created_at`, `updated_at`, `version` (optimistic lock) | | |

Mapping to rules: quota names come from the standard ICDR split (at least 35% retail, at least 15% NII, at most 50% QIB, anchors up to 60% of QIB), see [finowings summary of SEBI IPO rules](https://www.finowings.com/trending/sebi-new-ipo-rules-investors/) and the [JSA ICDR amendments note](https://www.jsalaw.com/wp-content/uploads/2025/03/JSA-Prism-Capital-Markets-and-Securities-ICDR-Amendments.March-2025.Final_.pdf). The sNII/bNII boundaries (sNII: above ₹2 lakh up to ₹10 lakh, one third of the NII portion; bNII: above ₹10 lakh, two thirds) were only partly confirmed by search ([search hit citing a one-third NII sub-quota for ₹2-10 lakh](https://www.sebi.gov.in/sebi_data/meetingfiles/jan-2022/1642414722087_1.pdf)) **[verify]**. The Apr 2025 proposals to cut the retail quota above ₹5,000 crore were reported as dropped ([Stocktwits](https://stocktwits.com/news-articles/markets/equity/sebi-drops-proposal-to-cut-retail-quota-in-large-ipos-keeps-35-percent-limit/chsgXBxRdgL)); because quotas vary per issue, store them per IPO and do not hard-code 35/15/50.

Not needed in 008: subscription multiples (009), GMP (009), allotment per account (016), listing price (017). Keep them out of this table; 009/017 add their own tables or columns by expand-only migration.

Partial fill: only `name`, `board`, `exchanges` and `apply_flag` are required at create. Everything else is nullable. A completeness check (service function, not a DB constraint) returns the list of missing fields required for a given status (e.g. to leave UPCOMING-with-dates for scheduling: `open_date`, `close_date`; to be offered for deals (011): price band, lot size). Data arrives roughly in this order: DRHP (name, size, managers, registrar) -> RHP (dates, quotas, RHP link, timetable) -> price band announcement (band, lot size; at least 2 working days before open: [SEBI March 2025 change merging pre-issue and price-band ads](https://www.jsalaw.com/wp-content/uploads/2025/03/JSA-Prism-Capital-Markets-and-Securities-ICDR-Amendments.March-2025.Final_.pdf)) -> close (final issue price) -> allotment (ISIN, symbol, credit) -> listing.

### 1.2 T+3 timeline and defaults (T = close date)

SEBI circular SEBI/HO/CFD/TPD1/CIR/P/2023/140 (9 Aug 2023) cut listing from T+6 to T+3, voluntary from 1 Sep 2023 and mandatory for issues opening after 1 Dec 2023 ([Business Standard](https://www.business-standard.com/amp/markets/news/sebi-reduces-timeline-for-listing-shares-to-t-3-from-t-6-from-december-1-123080900726_1.html), [IIFL](https://www.indiainfoline.com/news/markets/sebi-shortens-the-listing-period-from-t6-to-t3-and-makes-it-mandatory-starting-on-december-1), [TaxGuru](https://taxguru.in/sebi/sebi-reduces-listing-timeline-public-issue-shares-t6-days-t3-days.html)). Per the repo's own research ([research-report.md](../../docs/product/research-report.md), citing TaxGuru and Rupeezy): bids and UPI mandates close 5 pm on T, allotment T+1, unblock and share credit T+2, listing 10 am on T+3. A search snippet adds UPI mandate acceptance at 5 pm on T, with QIB/NII issue closure at 4 pm and retail/reserved at 5 pm, and exchange trading notice on T+2 ([search summary of the circular](https://www.unionbankofindia.co.in/pdf/New%20SEBI%20Guidlines-7.pdf)) **[verify the 4 pm / 5 pm split]**. The "T+1 unblock" claim is not confirmed by that source; the repo's T+2 unblock/credit is the working assumption.

Defaults the admin form should suggest when `close_date` (T) is entered (suggestions only, never silently saved):

| Field | Suggestion | Note |
|---|---|---|
| `bid_close_time` | 17:00 IST | retail/UPI cut-off; QIB/NII may close earlier **[verify]** |
| `allotment_date` | T+1 working day | basis of allotment |
| `refund_date`, `demat_credit_date` | T+2 working day | same day under T+3 |
| `listing_date` | T+3 working day | trading at ~10:00 IST |

"Working day" for T+n means exchange trading days, so Saturday, Sunday and exchange holidays (and occasional special sessions) are skipped. The repo's `istdate.AddWeekdays` (002 contract) skips only Saturday/Sunday. Decision: use `AddWeekdays` for the suggestion only (matches the owner's weekdays-only choice for payment due dates, BR-SET-08), show a non-blocking warning "check exchange holidays" when the suggested date was computed, and **always store the official dates the admin types**. Validation is soft for calendar quirks and hard for ordering: `open_date <= close_date <= allotment_date <= refund_date <= listing_date` (equal allowed for refund/credit). Soft warnings: weekend date; `listing - close` differs from 3 weekdays; issue open fewer than 3 or more than 10 working days **[verify ICDR]**; `cap > 1.2 x floor` **[verify ICDR]**. Do not build a holiday table in 008 (YAGNI; a later feature can add one).

### 1.3 Lifecycle state machine

`UPCOMING -> OPEN -> CLOSED -> ALLOTMENT_OUT -> LISTED -> SETTLED`

| Transition | Driver | Trigger instant (IST) | Needs |
|---|---|---|---|
| UPCOMING -> OPEN | time | `open_date` 10:00 (bidding window start, constant `OpenAt`) **[verify 10:00]** | `open_date` |
| OPEN -> CLOSED | time | `close_date` + `bid_close_time` (default 17:00) | `close_date` |
| CLOSED -> ALLOTMENT_OUT | time | `allotment_date` 00:00 by default? See Q2: recommended 10:00 on `allotment_date` | `allotment_date` |
| ALLOTMENT_OUT -> LISTED | time | `listing_date` 10:00 | `listing_date` |
| LISTED -> SETTLED | **not time-driven**; system event when settlement completes (019/018), or admin | n/a | exposed as `ipo.Service.MarkSettled` for later features; 008 only enforces order |
| any -> any | admin override with mandatory reason | immediate | audit_log |

BR-IPO-03 says "time-driven by IPO dates"; SETTLED depends on money being settled (BR-PAY-01), so time cannot drive it. Flag as Q3.

Interaction with 002 (Schedule/Reschedule/Cancel/CancelSubject are tx-only and keyed, see `go-module-api.md`):

- Kinds registered by 008: `ipo.open`, `ipo.close`, `ipo.allotment`, `ipo.list` (002's own examples use `ipo.open` and key `ipo:<uuid>:open`). Key `ipo:<id>:<step>`, subject `ipo:<id>`, payload ids only, `ExpiresAt = nil` (state-change work must run however late; FR-014 in 002).
- Handler is idempotent and reads current data: "advance to X only if current rank < rank(X) and the IPO's current date for X is <= now". If a date was moved later after scheduling, the handler is a no-op (and the reschedule has already created a new job). It never moves status backwards.
- On create/update, inside the same transaction as the row write and the audit entry, call `Schedule` for every step whose date is set, and `Cancel` (or `Schedule` replaced) for steps whose date was cleared. `Schedule` is an upsert on `(org, kind, key)`, so a date correction is just calling it again with the new `RunAt` (a `done` job's key is outside the partial unique index, so a fresh pending row is created, which is exactly what is needed when a corrected date lands in the future). If the new date is already past, the job runs on the next tick; this is also the catch-up path after downtime.
- Admin override of status: write status with `status_source='admin'`, audit with reason, leave later jobs in place. Because handlers are monotone, an admin who moved status forward is not regressed, and one who moved it back (rare, to fix a mistake) will see already-`done` jobs not re-fire; to re-arm, the override service call re-`Schedule`s steps whose target rank is above the new status and whose dates are in the future. Decide whether a past-date step should auto-fire after a backward override (recommended: no, admin-overridden status is sticky until the next date change; Q4).
- "A date change reschedules every dependent job": 008 owns only its four lifecycle jobs. Reminders and tasks (012/013) and settlement due dates (018) are other modules' jobs; they must not be touched via tables. Recommendation: 008 exports an in-process `DatesChanged` callback interface (`OnIPODatesChanged(ctx, IPO, old, new)`), called inside the same tx; 012/013/018 register listeners that `Reschedule`/`CancelSubject` their own subjects. Alternative (outbox event) is rejected for this purpose because outbox is for user-facing messages with quiet hours, not in-tx coordination.
- `CancelSubject("ipo:<id>")` on delete or on flipping to a terminal admin decision; deletion should be soft (`archived_at`) once any deal/task references the IPO.
- Time injection: the service takes `clock.Func`; the trigger instant is `istdate.Date.At(h, m)` (UTC instant of an IST wall time). No `time.Now()` in business code.
- Test with `clock.Fake` and a real tick (`scheduler.Tick`), as in 002's design.

### 1.4 Rejecting SME IPOs (BR-IPO-01)

SME issues list on NSE Emerge or BSE SME, are exchange-reviewed, have no QIB quota, require a market maker and a higher minimum application (about ₹2 lakh / two lots) versus roughly ₹14-15k per mainboard lot ([IPOMarket comparison via search](https://www.ipomarket.in/news/mainboard-vs-sme-ipo-differences), [NSE Emerge brochure](https://nsearchives.nseindia.com/web/sites/default/files/inline-files/NSE%20Emerge%20Brochure_1.pdf), [Moneylife on SEBI SME proposals](https://www.moneylife.in/article/sebi-proposes-sweeping-overhaul-of-sme-ipo-framework/81956.html)). Minimum SME application sizes are in flux (SEBI proposal to halve it), so do not rely on size.

Layered rejection, strongest first:

1. **Explicit input**: the create/update body has a required `segment` enum with values `NSE_MAINBOARD`, `BSE_MAINBOARD`, `NSE_EMERGE`, `BSE_SME`. Only the first two (or an `exchanges` set drawn from `NSE`,`BSE` plus `board` = `MAINBOARD`) are accepted; the SME values are listed in the schema on purpose and return 422 with a dedicated problem type (`urn:divya:problem:sme-not-allowed`, RFC 9457), so BR-IPO-01 has a named test. The DB also has `CHECK (board = 'MAINBOARD')` and `CHECK (exchanges <@ ARRAY['NSE','BSE'])` as defence in depth; no SME value can be stored.
2. **Import (026)**: the importer must map provider "type"/"segment" to this enum and drop (log and count) anything not mainboard; unknown segment means skip, never default to mainboard.
3. **Soft heuristics (warnings, not rejections)**: lot value at cap above a configurable limit (suggest ₹20,000 as a starting warning level, owner to confirm, Q5), issue name containing "SME"/"Emerge", or a listing venue text matching Emerge/SME. A heuristic must never auto-accept; it only prompts the admin.

Do not use ISIN or symbol format (SME ISINs look like mainboard ones) or issue size alone as the discriminator.

### 1.5 Admin UX references

- List: status tabs (All, Upcoming, Open, Closed, Allotment out, Listed, Settled), plus filters for APPLY flag and a month picker (by `open_date` month; IST month boundaries computed with `istdate`), default sort `open_date` desc with nulls first. Keep filter state in the URL via TanStack Router search params with schema validation ([TanStack: validate search params](https://tanstack.com/router/latest/docs/framework/react/how-to/validate-search-params), [search params guide](https://tanstack.com/router/latest/docs/guide/search-params)). Server-side filtering with cursor pagination (`(open_date, id)`); ~100 IPOs a year, so no virtualisation.
- Row actions: inline APPLY / DO NOT APPLY toggle with note popover (the one frequent action, audited); status badge showing "scheduled" vs "admin override".
- Bulk edit: not in v1 (rarely needed, doubles validation surface); allow multi-select only for setting the APPLY flag if the owner asks. Everything else is per-record.
- Form: sectioned (Basics, Offer, Price and lot, Dates, Quotas, Parties and links, Company), save-as-you-go; a "missing for next stage" checklist; date section with a "Suggest from close date" button; inline soft warnings (amber) vs hard errors (red); optimistic locking on `version` with a 409 "changed by someone else" problem. Indian formatting (₹ lakh/crore, IST) from `packages/domain`.

### 1.6 Data-entry helpers legal in v1 and room for 026

Per the product blueprint section 6 and research report: no official feed; NSE's terms forbid scraping ([NSE ToU](https://www.nseindia.com/nse-terms-of-use)); v1 is manual entry from offer documents and circulars, optionally ipoalerts' free tier (25 requests/day, 750/month; [ipoalerts](https://ipoalerts.in/ipo-data-api); its field schema could not be confirmed from search, so 026 must inspect a real response). Helpers 008 can ship without any external call:

- **Paste key facts**: a textarea where the admin pastes text from an RHP/announcement; a pure parser extracts candidate dates, price band, lot size, then shows them as proposed values the admin accepts per field. No network, no LLM in 008 (LLM brief is 026). Optional for the MVP of 008; the plan can defer it to a follow-up task if it threatens size limits.
- **Date suggestion from T** (1.2) and **derived read-only values** (retail max lots, lot value at cap, offer split).
- **Link-outs** (`rhp_url`, exchange notice URL) stored as plain links; no fetching.

Leave room for 026 now (cheap, avoids a later breaking migration): columns `source`, `source_ref`, `last_synced_at`, `field_sources`; a unique partial index on `(org_id, source, source_ref)` for idempotent upserts; a service method `Upsert(ctx, ImportedIPO)` that fills only fields where `field_sources[f]` is not `manual` (manual always wins, unless the admin clicks "accept suggestion"), writes audit with actor `system`, and re-runs scheduling; an `imported_pending` review state is rejected for now (use the existing admin review of the record; the blueprint says "import + you confirm", so imported rows are created UPCOMING with `apply_flag = DO_NOT_APPLY` and nothing is offered to clients until the admin sets APPLY).

## 2. Recommended approach and rejected alternatives

Recommended: one `ipos` table (single wide row, nullable until known), a `internal/ipos` module copied from `internal/health` (service + store behind small interfaces, Huma handlers, sqlc), four idempotent step jobs on 002's scheduler, an in-tx `DatesChanged` listener hook, status ranks in code with CHECK constraint in SQL, audit on every mutation. Dates stored as IST `date`; trigger instants built with `istdate`; money in paise.

Rejected alternatives:

1. **One generic "advance" polling job per tick that scans all IPOs** by date: simple, but bypasses the 002 reschedule/dedupe model, makes BR-IPO-03's "date change reschedules jobs" untestable, and scans every tick.
2. **Compute status only on read from dates (no stored status)**: cannot represent admin override or SETTLED, and downstream modules need an auditable status-change moment (and later hooks) rather than a read-time derivation.
3. **Separate `ipo_dates` / `ipo_quotas` tables**: more joins for a ~100 row/year dataset; revisit only if 026 needs per-field history (use `audit_log` instead).

## 3. Pitfalls, limits, costs

- Dates are civil IST dates; never store them as UTC midnight (off-by-one for the evening in UTC). Store `date`, convert via `istdate.Date.At`.
- A corrected date that is now in the past triggers immediate (next tick) status advancement, possibly several steps at once; the handler design (advance only to what the dates allow) must handle multi-step catch-up in order. Test: missed ticks, date moved earlier, date moved later after the job ran.
- Tick cadence is whatever 027 configures (002 overdue threshold 15 min), so status can lag the trigger instant by up to one tick interval; do not promise second precision to clients.
- Exchange holidays and special sessions make T+n differ from weekdays; hence admin-entered official dates win and defaults are labelled suggestions.
- Retail cap per application (₹2 lakh) and lot size interplay changes with cap price; derive, never store.
- SME rules are changing (SEBI consultation); do not encode size thresholds as rules.
- ISIN/symbol appear late; do not make them unique-required early (unique partial index `WHERE isin IS NOT NULL`).
- Free-tier data: Supabase free DB 500 MB (stated in 002 retention notes); IPO rows are tiny, no concern. Scheduler cost is four jobs per IPO.
- Migration is a schema addition only (no change to ledger/auth tables), but per the constitution migrations need owner approval on the PR.
- Quotas as basis points avoid float math; the sum of quotas is a soft check (may not equal 10000 when employee/shareholder reservations are carved out) **[verify]**.

## 4. Existing code to reuse

- Golden reference module: `/home/user/divya-008/apps/api/internal/health/` (`health.go` service, `handler.go` Huma `Register(api, svc)`, `health_test.go`); copy the shape per `.claude/rules/api.md`.
- Platform: `apps/api/internal/platform/{config,db,dbtest,httpx}`; migration style `apps/api/migrations/00001_platform.sql` (uuidv7 keys, `orgs` FK, `audit_log`, `forbid_mutation()`); sqlc config `apps/api/sqlc.yaml`, queries in `apps/api/queries/`. Next migration number: 002 uses `00002_scheduler_outbox.sql`, so 008 should take `00003_ipos.sql` (confirm at merge to avoid collisions).
- From 002 (not merged yet; read-only reference at `/home/user/divya-002/specs/002-scheduler-tick/contracts/go-module-api.md`): `scheduler.Service.Register/Schedule/Reschedule/Cancel/CancelSubject`, `db.TxRunner.InTx/MustTx`, `audit.Write`, `clock.Func`/`clock.Fake`, `istdate.Date/AddWeekdays/At`. 008 must wait for 002 (modules.md dependency) and import only these exported interfaces (depguard).
- Frontend: `apps/admin` (TanStack Router/Query), `packages/domain` for ₹/IST formatting and enums (add `IpoStatus`, `ApplyFlag`), `packages/api-client` regenerated from OpenAPI.

## 5. Open questions for the owner (do not guess)

- Q1: Default for `apply_flag` on a new IPO: `DO_NOT_APPLY` (safe; nothing is offered) or a required explicit choice with no default? Recommended: required explicit choice in the form, API default `DO_NOT_APPLY` for imports.
- Q2: At what instant does CLOSED -> ALLOTMENT_OUT fire (BR-IPO-03 says "by IPO dates")? Recommended: `allotment_date` 10:00 IST; the admin can override earlier once the registrar publishes.
- Q3: SETTLED is not time-driven; confirm it is set by settlement completion (019) or admin only.
- Q4: After an admin backward override, should passed-date steps re-fire? Recommended: no.
- Q5: Mainboard sanity warning thresholds (lot value limit, price band cap ratio).
- Q6: Are IPOs with no dates visible to clients at all (UPCOMING with only a name)? Not covered by the rules; affects whether a `published` flag is needed.

## 6. Sources

- Repo: `docs/product/business-rules.md` (BR-IPO-01..03, BR-SET-08), `docs/product/product-blueprint.md` sections 2.1, 2.2, 6, `docs/product/research-report.md` (IPO data sources), `.specify/memory/constitution.md`, `/home/user/divya-002/specs/002-scheduler-tick/{data-model.md,contracts/go-module-api.md}`.
- T+3: [Business Standard](https://www.business-standard.com/amp/markets/news/sebi-reduces-timeline-for-listing-shares-to-t-3-from-t-6-from-december-1-123080900726_1.html), [IIFL](https://www.indiainfoline.com/news/markets/sebi-shortens-the-listing-period-from-t6-to-t3-and-makes-it-mandatory-starting-on-december-1), [TaxGuru](https://taxguru.in/sebi/sebi-reduces-listing-timeline-public-issue-shares-t6-days-t3-days.html), [Union Bank ASBA guideline PDF](https://www.unionbankofindia.co.in/pdf/New%20SEBI%20Guidlines-7.pdf), [SEBI UPI/ASBA circular via MSEI](https://www.msei.in/SX-Content/Company-Circulars/2021/June/SEBI-02-Jun-2021-STREAMLINING-THE-PROCESS-OF-IPOS-WITH-UPI-IN-ASBA-AND-REDRESSAL-OF-INVESTOR-GRIEVANCES..pdf).
- Quotas and price band ad: [finowings](https://www.finowings.com/trending/sebi-new-ipo-rules-investors/), [JSA ICDR amendments March 2025](https://www.jsalaw.com/wp-content/uploads/2025/03/JSA-Prism-Capital-Markets-and-Securities-ICDR-Amendments.March-2025.Final_.pdf), [Stocktwits on 35% retail quota](https://stocktwits.com/news-articles/markets/equity/sebi-drops-proposal-to-cut-retail-quota-in-large-ipos-keeps-35-percent-limit/chsgXBxRdgL), [SEBI board papers Jan 2022](https://www.sebi.gov.in/sebi_data/meetingfiles/jan-2022/1642414722087_1.pdf).
- SME vs mainboard: [IPOMarket](https://www.ipomarket.in/news/mainboard-vs-sme-ipo-differences), [NSE Emerge brochure](https://nsearchives.nseindia.com/web/sites/default/files/inline-files/NSE%20Emerge%20Brochure_1.pdf), [Moneylife](https://www.moneylife.in/article/sebi-proposes-sweeping-overhaul-of-sme-ipo-framework/81956.html), [Kotak SME IPO guide](https://www.kotaksecurities.com/investing-guide/ipos/how-to-apply-sme-ipo).
- Data sources: [NSE terms of use](https://www.nseindia.com/nse-terms-of-use), [ipoalerts](https://ipoalerts.in/ipo-data-api), [Upstox get IPOs (status values reference)](https://upstox.com/developer/api-documentation/get-ipos).
- Admin UX: [TanStack Router validate search params](https://tanstack.com/router/latest/docs/framework/react/how-to/validate-search-params), [search params guide](https://tanstack.com/router/latest/docs/guide/search-params).
