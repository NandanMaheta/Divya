# Problem codes and warnings: 008 IPO catalogue (FR-061)

The format is 003's (plan P-10, `specs/003-auth-core/contracts/error-codes.md`). Responses are RFC 9457 `application/problem+json`. `code` is stable and language-neutral, and `type` is `urn:divya:problem:<code>`. `errors[]` holds `{location, message}` and never `value`. `title` and `detail` are English, for developers. Amounts in `detail` use Indian formatting (`internal/platform/inr`) and times are IST.

Domain errors are built with 003's `httpx.Problem(status, code, detail)`. The field list is added as `errors[].location` (`body.<field>`). The codes are mirrored in `packages/domain/src/ipo.ts` for UI translation (022/023/025). Adding a code is non-breaking; renaming or removing one is breaking.

## Errors

| code | HTTP | When | Spec |
|---|---|---|---|
| `ipo.sme_not_allowed` | 422 | `segments` contains `NSE_EMERGE` or `BSE_SME` (alone or mixed). `errors[0].location = body.segments` | BR-IPO-01, FR-010, US2.1/2.2/2.4 |
| `ipo.invalid_segment` | 422 | `segments` contains an unknown value or a duplicate | FR-010, US2.3 |
| `validation_failed` | 422 | Schema checks by Huma (platform code): missing required key (including `segments`), wrong type, a non-integer paise value (fraction of a paisa), `minimum`/`maximum`, `maxLength`, bad `format: date`/`uuid`/`uri`, bad `month` pattern, bad cursor | US1.6, US2.3 (missing) |
| `ipo.invalid_value` | 422 | Cross-field and trimmed-value rules: only one end of the price band; floor > cap; blank name (after trimming); lead manager entry empty or longer than 200 characters; flag note without a flag; blank override reason (after trimming) | FR-003, FR-022, FR-035, US1.5, US6.4 |
| `ipo.date_order` | 422 | A related pair of set dates is out of order (research R-07). One `errors[]` item per field of each violated pair | FR-040, US5.9 |
| `ipo.publish_incomplete` | 422 | Publish with items missing, or an edit or flag clear on a published IPO that would break FR-020. One `errors[]` item per missing item: `body.apply_flag` ("flag required"), `body.open_date`, `body.close_date`, `body.price` (band or final price), `body.lot_size` | FR-020, FR-021, US3.1/3.2/3.7 |
| `ipo.stale_version` | 409 | `version` differs from the stored version | FR-006, US1.9 |
| `ipo.no_change` | 409 | Override to the current status; publish when already published; unpublish a draft | FR-035, US6.5 |
| `ipo.published_not_deletable` | 409 | Delete of an IPO that is or was ever published | FR-008, D-08, US9.3 |
| *listener code* | 409 | A dependent listener returned `*ipos.RejectedError{Code}`; the whole change was rolled back | FR-024, FR-037, FR-042 |
| `not_found` | 404 | Unknown id, another org's id, or (client operations) an unpublished IPO. The body is identical in each case | FR-051, US7.3 |
| `auth.unauthenticated` | 401 | 003 middleware | FR-050, US7.7 |
| `auth.forbidden` | 403 | 003 middleware: a client calls an admin operation | FR-050, US7.6 |
| `internal` | 500 | Unexpected error, including a listener's non-`RejectedError` failure (no details) | US3.10, US5.5 |

Not an HTTP error: `ipos.ErrNotListed` (`ipo.not_listed_yet`) is returned to 019 by `MarkSettled` (FR-033, US6.7).

## Warnings (success responses only)

Mutation responses (`IPOMutationResult`) carry `warnings: Warning[]`, where `Warning = {code, fields[], detail, params}`.

| code | When | params | Spec |
|---|---|---|---|
| `ipo.min_application_out_of_range` | `MinApplicationPaise()` is known and `< 1,000,000` or `> 2,000,000` paise. Bounds are inclusive (no warning at exactly ₹10,000 or ₹20,000) | `value_paise`, `min_paise` (1000000), `max_paise` (2000000) | FR-005, US1.3/1.4, US3.9 |

Example `detail`: `minimum application value ₹25,000 is outside ₹10,000–₹20,000`.

## Notice (date suggestions)

`DateSuggestion.notice_code = "ipo.weekdays_only"`, `notice = "weekdays only; check exchange holidays"` (FR-043).
