# ADR 0008: In-transaction module signals (listener interfaces)

- Status: Proposed with the 008 plan (2026-11-13). The owner accepts it together with the plan.
- Applies to: every module that must react, in the same transaction, to a change owned by another module. First used by 008 (IPO `DatesChanged`, `EligibilityChanged`, `StatusChanged`). Consumed by 010/011, 012, 013, 018 and 019.

## Context

BR-IPO-03 says "a date change reschedules every dependent job". The 008 spec (FR-024, FR-037, FR-042) requires that dependent features are told about date, eligibility and status changes **within the same all-or-nothing change**, and that a failing dependent undoes the change.

Constitution IV allows modules to communicate only through exported service interfaces or outbox events, and never through each other's tables. ADR 0001 gives one database and one binary, so cross-module transactions are possible. ADR 0003 and feature 002 provide tx-only `Schedule`/`Cancel` calls, so a dependent can move its own jobs inside the owner's transaction.

The outbox (002) is delivered after commit, at least once, and is meant for user-facing messages with quiet hours. It cannot make a dependent's reaction atomic with the cause.

## Decision

1. The **owning module** (here `internal/ipos`) exports one small listener interface per signal (for example `StatusChangedListener{ OnIPOStatusChanged(ctx, StatusChanged) error }`), plain event structs (ids, old/new values, actor, time), and `On<Signal>(listener)` registration methods.
2. **Dependent modules** implement the interfaces. `cmd/swastik` registers them at wiring time, before the server starts. Dependents import the owner; the owner never imports dependents (depguard). There are no import cycles.
3. The owner calls listeners **synchronously, in registration order, inside its transaction** (`ctx` carries the tx; 002 `db.TxRunner` nested calls join), after its own writes, steps and audit entry.
4. A listener's error or panic **rolls back the whole change**. A listener can return a typed rejection with a stable problem code, which the owner surfaces as 409. Any other error surfaces as 500.
5. Listeners write only their own module's tables, schedule or cancel only their own jobs (002), and may add outbox messages. They must not call mutations of the emitting module (no re-entrancy).
6. Signals are in-process only. There is no persistence and no async delivery. A user-facing notification about the same change is a separate outbox message written by whichever module owns it.

## Consequences

- "Same change" guarantees are simple to test: a recording listener and a failing listener per signal, run against a real database.
- Coupling at runtime: a slow or failing dependent blocks the admin's edit or a lifecycle step. That is intended by the spec. A failing listener inside a scheduler job makes 002 retry the job (backoff, dead-letter, admin alert).
- Listener order is deterministic but must not carry meaning. Dependents must not rely on running before or after each other.
- Event structs are a contract. Adding fields is non-breaking; removing or renaming one needs every listener updated in the same PR.
- If the monolith is ever split, these signals become outbox events with idempotent consumers. The interfaces mark the seams.

## Alternatives considered

- **Outbox events**: asynchronous and not atomic with the dependent's reaction, so they violate FR-042 and US5.5. They are reserved for messages.
- **Owner calls dependents directly**: creates import cycles and makes the owner know every consumer.
- **Dependents poll the owner's data**: stale, wasteful, and cannot express "in the same change".
- **Generic in-process event bus with `any` payloads**: loses type safety and hides dependencies. Typed interfaces are smaller and lint-checkable.
