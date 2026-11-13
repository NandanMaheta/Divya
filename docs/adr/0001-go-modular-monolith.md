# ADR 0001: Go modular monolith for the API

- Status: Accepted (2026-11-13)

## Context

One operator, ~100 users, many tightly related workflows (deals → tasks → allotment → settlement → payments). The owner wants an in-demand, fast, cheap backend that AI agents can work on safely.

## Decision

A single Go 1.27 binary with modules under `apps/api/internal/<module>`, following `internal/health`. HTTP via Huma v2 on the standard library mux (OpenAPI 3.1 generated from code). Data access via pgx v5 + sqlc. Boundaries enforced with golangci-lint depguard.

## Consequences

One deployable, one database, transactional consistency across modules; scales well past 10,000 users on Cloud Run. Splitting into services later is possible along module lines. Compiler + linters give agents tight feedback.
