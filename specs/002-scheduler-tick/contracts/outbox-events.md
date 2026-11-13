# Contract: outbox events emitted by 002

## `admin.jobs_dead` (owner decision Q1: one grouped admin alert per tick)

Emitted by the tick's dead-alert sweep: after the jobs and messages phases, in one transaction, the tick selects every `dead` job and message with `alerted_at IS NULL` (messages whose topic starts with `admin.` are excluded and marked alerted, so an undeliverable alert can never trigger another alert), groups them per organisation, inserts one `admin.jobs_dead` message per organisation and sets `alerted_at = now` on the included rows. If that transaction fails, nothing is marked and the next tick retries the sweep, so an alert is neither lost nor duplicated. Admin retry clears `alerted_at`, so an item that dies again alerts again.

| Field | Value |
|---|---|
| `topic` | `admin.jobs_dead` |
| `recipient` | `{kind: "admin", id: null}` (every admin of the org; resolved by 014) |
| `subject` | `scheduler:dead` |
| `dedupe_key` | `jobs_dead:<tick_id>` (one per org per tick) |
| quiet hours | respected (spec Clarifications: the only planned exemption is payment-overdue reminders, 019) |
| `expires_at` | none |
| max attempts | default (8) |

Payload (JSON Schema; content reference only, no secrets, no PAN, no error texts):

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["tick_id", "jobs_dead", "messages_dead", "kinds", "topics"],
  "properties": {
    "tick_id": { "type": "string", "format": "uuid" },
    "jobs_dead": { "type": "integer", "minimum": 0 },
    "messages_dead": { "type": "integer", "minimum": 0 },
    "kinds": { "type": "array", "items": { "type": "string" }, "maxItems": 10, "description": "distinct job kinds, sorted" },
    "topics": { "type": "array", "items": { "type": "string" }, "maxItems": 10, "description": "distinct message topics, sorted" }
  }
}
```

Consumer: feature 014 registers a `Sender` for `admin.jobs_dead` that renders e.g. "2 scheduled jobs failed permanently" as a push to the admin's devices plus a bell (inbox) entry, deep-linking to the failed-work list (022/023). Until 014 is merged no sender is registered, so these messages fail as "no sender" (FR-025) and end `dead`; they stay visible through the admin list and the dead counts in `scheduler-get-status`, which is the other channel the owner chose.

## Topic naming for owning features

`<audience>.<event>` in lower snake case: `task.reminder`, `ipo.status_changed`, `payment.overdue`, `admin.*` for operator alerts. Dedupe keys must identify the business fact plus slot (for example `task:<id>:slot:2026-11-14T10:00`), never a random value.
