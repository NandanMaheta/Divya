# Data model: 008 IPO catalogue

Migration: `apps/api/migrations/00004_ipos.sql`. It uses goose and is expand-only: one new table, one trigger function and one trigger. Existing tables do not change. It runs after 002 (`00002_scheduler_outbox.sql`) and 003 (`00003_auth.sql`). Queries live in `apps/api/queries/ipos.sql` and generate into `internal/platform/db/dbgen`, the existing sqlc package, with 002's `uuid` override.

Conventions follow `.claude/rules/sql.md`:

- `uuidv7()` keys.
- `timestamptz` (UTC) for instants and `date` for IST civil dates.
- `bigint` paise.
- CHECK constraints for every enum and state.
- `org_id` FK to `orgs(id)`.

Instants (`status_changed_at`, `first_published_at`, `last_override_at`, `created_at`, `updated_at`) are written from the injected clock as query parameters. DB defaults are only a safety net.

`ipos` is mutable reference data, not an append-only table. Its history is the append-only `audit_log` (001, `forbid_mutation()`), written in the same transaction as every change (FR-060).

## 1. `ipos` (owned by `internal/ipos`)

| Column | Type | Null | Default | Notes / CHECK |
|---|---|---|---|---|
| `id` | uuid | no | `uuidv7()` | PK |
| `org_id` | uuid | no | | FK `orgs(id)` |
| `name` | text | no | | `char_length(btrim(name)) BETWEEN 1 AND 200`; not unique (US1.10) |
| `board` | text | no | `'MAINBOARD'` | `CHECK (board = 'MAINBOARD')`. BR-IPO-01 defence in depth (FR-011) |
| `exchanges` | text[] | no | | `cardinality(exchanges) BETWEEN 1 AND 2 AND exchanges <@ ARRAY['NSE','BSE']::text[] AND (cardinality(exchanges) = 1 OR exchanges[1] <> exchanges[2])`. The service stores them sorted (`{BSE,NSE}`). API `segments` `NSE_MAINBOARD`/`BSE_MAINBOARD` map to `NSE`/`BSE` |
| `nse_symbol` | text | yes | | `nse_symbol ~ '^[A-Z0-9&-]{1,20}$'` |
| `issue_size_paise` | bigint | yes | | `BETWEEN 1 AND 1000000000000000` (≤ ₹10 lakh crore; below 2^53 so it is JSON-safe for TS) |
| `price_band_floor_paise` | bigint | yes | | `BETWEEN 1 AND 1000000000` (≤ ₹1 crore per share) |
| `price_band_cap_paise` | bigint | yes | | same range; `(price_band_floor_paise IS NULL) = (price_band_cap_paise IS NULL)`; `price_band_floor_paise <= price_band_cap_paise` (floor = cap allowed, US1.7) |
| `issue_price_paise` | bigint | yes | | final price; `BETWEEN 1 AND 1000000000`; may be outside the band (spec Edge Cases) |
| `lot_size` | integer | yes | | shares; `BETWEEN 1 AND 1000000`. Lot × price ≤ 10^15 paise, so no overflow and JSON-safe |
| `open_date` | date | yes | | IST civil date |
| `close_date` | date | yes | | `open_date <= close_date` |
| `allotment_date` | date | yes | | `close_date < allotment_date` |
| `refund_date` | date | yes | | `allotment_date <= refund_date`, `refund_date <= listing_date` |
| `demat_credit_date` | date | yes | | `allotment_date <= demat_credit_date`, `demat_credit_date <= listing_date` |
| `listing_date` | date | yes | | `allotment_date < listing_date` |
| `registrar` | text | yes | | `char_length(registrar) BETWEEN 1 AND 200` |
| `lead_managers` | text[] | no | `'{}'` | `cardinality(lead_managers) <= 20`; element length 1–200 checked by the service |
| `offer_document_url` | text | yes | | `offer_document_url ~ '^https://' AND char_length(offer_document_url) <= 2000` |
| `apply_flag` | text | yes | | `apply_flag IN ('APPLY','DO_NOT_APPLY')`; NULL = not set (drafts only, D-01) |
| `flag_note` | text | yes | | `char_length(flag_note) BETWEEN 1 AND 500`; `apply_flag IS NOT NULL OR flag_note IS NULL`. Client-visible (D-10) |
| `published` | boolean | no | `false` | |
| `first_published_at` | timestamptz | yes | | set on the first publish and never cleared. "Ever published" = NOT NULL (D-08); `NOT published OR first_published_at IS NOT NULL` |
| `status` | text | no | `'UPCOMING'` | `IN ('UPCOMING','OPEN','CLOSED','ALLOTMENT_OUT','LISTED','SETTLED')` (FR-030) |
| `status_source` | text | no | `'schedule'` | `IN ('schedule','admin','settlement')`: who set the current status (US6.9). `schedule` also covers the initial UPCOMING |
| `status_changed_at` | timestamptz | no | | clock |
| `last_override_at` | timestamptz | yes | | moment of the latest manual status change. Drives FR-036 suppression (research R-02). Never cleared |
| `source` | text | no | `'manual'` | `source = 'manual' OR source ~ '^import:[a-z0-9_]{1,32}$'` (kept for 026) |
| `source_ref` | text | yes | | `char_length BETWEEN 1 AND 200`; `(source = 'manual') = (source_ref IS NULL)` |
| `last_synced_at` | timestamptz | yes | | `source <> 'manual' OR last_synced_at IS NULL` |
| `field_sources` | jsonb | no | `'{}'` | `jsonb_typeof(field_sources) = 'object'`; per-field origin for 026; 008 leaves it `{}` |
| `create_idempotency_key` | text | yes | | `char_length BETWEEN 8 AND 200` (research R-04) |
| `version` | integer | no | `1` | `version >= 1`; +1 on every committed mutation (FR-006) |
| `created_at` | timestamptz | no | `now()` | written from the clock |
| `updated_at` | timestamptz | no | `now()` | written from the clock |

**Publish invariant (FR-020/FR-021, defence in depth)**:

```
CHECK (NOT published OR (apply_flag IS NOT NULL AND open_date IS NOT NULL AND close_date IS NOT NULL
       AND lot_size IS NOT NULL AND (issue_price_paise IS NOT NULL OR price_band_cap_paise IS NOT NULL)))
```

The service checks this first and returns `ipo.publish_incomplete` with every missing item listed. The CHECK only catches a bypass.

**Indexes**

| Name | Definition | Purpose |
|---|---|---|
| `ipos_pkey` | PK `(id)` | |
| `ipos_admin_list_idx` | `(org_id, open_date DESC NULLS FIRST, id DESC)` | admin list order and keyset (FR-009); month filter is a range on `open_date` |
| `ipos_published_list_idx` | `(org_id, open_date DESC NULLS FIRST, id DESC) WHERE published` | client list (FR-051, SC-004, SC-008) |
| `ipos_source_ref_uq` | UNIQUE `(org_id, source, source_ref) WHERE source_ref IS NOT NULL` | idempotent import upserts for 026 |
| `ipos_create_idem_uq` | UNIQUE `(org_id, create_idempotency_key) WHERE create_idempotency_key IS NOT NULL` | create replay (FR-007, US1.8) |

Status and flag filters are not indexed. There are a few hundred rows per org, and the list index already restricts the scan by org (SC-008: 500 rows).

**Trigger**: `ipos_forbid_published_delete BEFORE DELETE ON ipos FOR EACH ROW` runs `ipos_forbid_published_delete()`. It raises `'published IPOs can only be unpublished'` when `OLD.first_published_at IS NOT NULL` (D-08, FR-008 defence in depth; the service returns `ipo.published_not_deletable` first).

**Down migration**: drop the trigger, the function and the table, in that order.

## 2. Value types (Go, `internal/ipos`)

| Type | Values / shape | Notes |
|---|---|---|
| `Status` | `UPCOMING`(0) `OPEN`(1) `CLOSED`(2) `ALLOTMENT_OUT`(3) `LISTED`(4) `SETTLED`(5) | `Rank()`; exhaustive switch (lint `exhaustive`) |
| `StatusSource` | `schedule`, `admin`, `settlement` | |
| `ApplyFlag` | `APPLY`, `DO_NOT_APPLY` | `*ApplyFlag` nil = not set |
| `Segment` (API) | `NSE_MAINBOARD`, `BSE_MAINBOARD` accepted; `NSE_EMERGE`, `BSE_SME` → `ipo.sme_not_allowed`; anything else → `ipo.invalid_segment` | research R-05 |
| `Dates` | `Open, Close, Allotment, Refund, DematCredit, Listing *istdate.Date` | |
| `IPO` | all columns above as Go types (`int64` paise, `*istdate.Date`, `uuid.UUID`) | `Eligible() = Published && Flag == APPLY` (FR-023); `MinApplicationPaise() (int64, bool)` = `lot × (issue_price ?? cap)` (FR-004) |
| `Draft` | the editable facts (create/PUT input) | |
| `Warning` | `{Code, Fields []string, Detail string, Params map[string]int64}` | only `ipo.min_application_out_of_range` (FR-005): value outside [1,000,000; 2,000,000] paise, inclusive bounds = no warning |
| `DateSuggestion` | `{Close, Allotment, Refund, DematCredit, Listing istdate.Date; Notice}` | `istdate.AddWeekdays(T, 1/2/2/3)` (FR-043) |

## 3. State machine (status)

```
           ipo.open (open_date 10:00 IST)   ipo.close (close_date 17:00)   ipo.allotment (allotment_date 10:00)   ipo.list (listing_date 10:00)
 UPCOMING ───────────────────────────► OPEN ──────────────────────► CLOSED ───────────────────────► ALLOTMENT_OUT ─────────────────────► LISTED
                                                                                                                                          │ MarkSettled (019, actor system)
                                                                                                                                          ▼  only from LISTED (else ErrNotListed)
                                                                                                                                       SETTLED
 any ── OverrideStatus(to ≠ current, reason) ──► any   (actor admin, status_source=admin, last_override_at=now)
```

Step handler rule (research R-01, R-02; FR-031, FR-032, FR-036): lock the row. Then do nothing if any of these holds:

- the row is missing;
- the date is NULL;
- `instant(date) > now`;
- `last_override_at IS NOT NULL AND payload.armed_at <= last_override_at AND instant <= last_override_at`;
- `rank(status) >= rank(target)`.

Otherwise set `status = target`, `status_source = 'schedule'`, `status_changed_at = now`, `version+1`, write the audit row and call the StatusChanged listeners. A no-op writes nothing (FR-060, US4.9, US6.1).

Steps are created, moved and cancelled by the service in the transaction of the change (FR-041):

| Kind | Dedupe key | Subject | Target | Instant | `ExpiresAt` |
|---|---|---|---|---|---|
| `ipo.open` | `ipo:<id>:open` | `ipo:<id>` | OPEN | `open_date.At(10,0)` | nil (state change always runs late, 002 FR-014) |
| `ipo.close` | `ipo:<id>:close` | `ipo:<id>` | CLOSED | `close_date.At(17,0)` | nil |
| `ipo.allotment` | `ipo:<id>:allotment` | `ipo:<id>` | ALLOTMENT_OUT | `allotment_date.At(10,0)` | nil |
| `ipo.list` | `ipo:<id>:list` | `ipo:<id>` | LISTED | `listing_date.At(10,0)` | nil |

Payload: `{"ipo_id": "<uuid>", "armed_at": "<RFC 3339 UTC>"}`.

| Change | Effect on steps |
|---|---|
| create | `Schedule` for each set date |
| a date set or changed | `Schedule` (upsert; a `done` row stays as history and a new pending row is created) with a fresh `armed_at` |
| a date cleared | `Cancel(org, kind, key)` |
| a date unchanged, or a non-date edit | nothing (FR-041, US5.11) |
| refund or demat credit date | no step |
| override | nothing (suppression is decided in the handler) |
| delete | `CancelSubject(org, "ipo:<id>")` |

## 4. Publication and eligibility

| Operation | Precondition | Effect | Eligibility signal |
|---|---|---|---|
| publish | draft and FR-020 complete | `published=true`, `first_published_at = COALESCE(first_published_at, now)` | if `flag = APPLY` (false → true) |
| unpublish | published | `published=false` | if `flag = APPLY` (true → false) |
| set flag/note | draft: any (NULL allowed); published: non-NULL flag | flag, note | when `Eligible()` changes |
| PUT facts | published: result still meets FR-020 | facts | none |

"Already in that state" for publish or unpublish gives `ipo.no_change` (409). A flag or PUT with identical values returns 200, writes no audit row and does not bump the version (research R-06).

## 5. `audit_log` usage (existing table)

`entity = 'ipo'` and `entity_id = ipo id`. The actor is the admin (`Principal.AuditActor()`, 003) or `system` (steps, 019). `At` is the clock (003 C-4). `before` and `after` are typed Go structs holding only the changed fields. `after.idempotency_key` is present when a key was sent.

| Action | When | before → after |
|---|---|---|
| `ipo.created` | create | `null` → full snapshot |
| `ipo.updated` | PUT that changed anything | changed facts (old) → changed facts (new). Includes the six dates when they changed (US5.10) |
| `ipo.flag_set` | flag and/or note changed | `{apply_flag, flag_note}` → `{apply_flag, flag_note, eligible}` |
| `ipo.published` / `ipo.unpublished` | publish / unpublish | `{published, eligible}` → `{published, eligible}` |
| `ipo.status_changed` | step, override, 019 | `{status, status_source}` → `{status, status_source, step?, scheduled_for?, reason?}`. Step: `step`, `scheduled_for` (US4.2). Override: `reason` (FR-035). 019: neither |
| `ipo.deleted` | delete | full snapshot → `null` |

Each mutation writes exactly one row (SC-005). Idempotency replay lookup (research R-04) uses the existing `audit_log_entity_idx (org_id, entity, entity_id, created_at DESC)`.

## 6. Queries (`apps/api/queries/ipos.sql`)

| Name | Kind | Notes |
|---|---|---|
| `InsertIPO` | `:one` | `ON CONFLICT (org_id, create_idempotency_key) WHERE create_idempotency_key IS NOT NULL DO NOTHING RETURNING *` |
| `GetIPOByCreateKey` | `:one` | create replay |
| `GetIPO` | `:one` | `WHERE org_id = $1 AND id = $2` |
| `GetIPOForUpdate` | `:one` | `… FOR UPDATE` (every mutation and step handler) |
| `GetIPOForShare` | `:one` | `… FOR SHARE` (`RequireEligible` for 010/012) |
| `GetPublishedIPO` | `:one` | `… AND published` (client fetch) |
| `UpdateIPOFacts` | `:one` | `WHERE org_id, id AND version = @version`, `version = version + 1` |
| `SetIPOFlag` | `:one` | same version guard |
| `SetIPOPublished` | `:one` | same version guard; `first_published_at = COALESCE(first_published_at, @now)` |
| `SetIPOStatus` | `:one` | sets `status`, `status_source`, `status_changed_at`, `last_override_at = COALESCE(sqlc.narg(override_at), last_override_at)`, version+1 |
| `DeleteIPO` | `:execrows` | `WHERE org_id, id AND version AND first_published_at IS NULL` |
| `ListIPOsAdmin` | `:many` | optional filters `status`, `published`, `flag` (`'NONE'` → `apply_flag IS NULL`), `month_start`/`month_end`; keyset `(open_date NULLS FIRST, id)` DESC; `LIMIT @limit + 1` |
| `ListPublishedIPOs` | `:many` | `published` and optional `status`; same keyset |
| `FindIPOAuditByIdempotencyKey` | `:one` | `SELECT id FROM audit_log WHERE org_id AND entity='ipo' AND entity_id AND action AND after->>'idempotency_key' = @key LIMIT 1` |

All writes take `@now` from the clock. There is no string-built SQL. Every query filters by `org_id`.
