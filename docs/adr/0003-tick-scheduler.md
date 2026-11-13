# ADR 0003: Tick-driven scheduler on scale-to-zero hosting

- Status: Accepted (2026-11-13)

## Context

Reminders every 1–2 hours, status changes by IPO dates, due dates and overdue escalation — on Cloud Run, which scales to zero, at ₹0/month.

## Decision

Cloud Scheduler calls `POST /internal/tick` every 5 minutes (authenticated with an OIDC token). Each tick claims due rows from `scheduled_jobs` with `FOR UPDATE SKIP LOCKED` and runs them idempotently. Notifications are written to an outbox table in the same transaction as the change that causes them. Business logic receives an injected clock.

## Consequences

±5 minute precision (acceptable). No Redis or always-on worker. The tick keeps the Supabase free project active. If we move to an always-on VM, River can replace the job table without changing module code.
