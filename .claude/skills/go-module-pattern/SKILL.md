---

name: go-module-pattern
description: How to build a new Divya Go API module (folder layout, service/handler/sqlc/test shape, wiring, OpenAPI regeneration). Use when creating or extending anything under apps/api/internal.
---

# Go module pattern

Copy `apps/api/internal/health`.

```
apps/api/internal/<module>/
  service.go        // types + Service with small interface deps (Store, Clock, Outbox); no HTTP or pgx types in signatures
  store.go          // adapter: implements Store using sqlc dbgen.Queries (pgx tx passed in)
  handler.go        // Register(api huma.API, svc *Service): one huma.Register per operation, typed In/Out structs
  service_test.go   // table tests with fakes; rapid property tests where invariants exist
  store_test.go     // integration via dbtest.New(t)
  handler_test.go   // humatest
apps/api/queries/<module>.sql      // sqlc queries (-- name: X :one|:many|:exec)
apps/api/migrations/NNNNN_<module>.sql
```

Steps:

1. Migration (expand-only) → `sqlc generate && sqlc vet`.
2. Service with injected `Clock func() time.Time` and interfaces; transactions are opened by the service via a `TxRunner` from `internal/platform/db`.
3. Handlers: operation IDs `<module>-<verb>`, tags = module name, auth requirement declared, RFC 9457 errors via `huma.ErrorXXX`, idempotency key header on mutations.
4. Wire in `cmd/divya/main.go` `buildAPI`.
5. `pnpm --filter @divya/api-client generate` and commit `openapi.yaml` + `schema.ts`.
6. Tests named with rule IDs; `go test -race ./internal/<module>/...` is the usual done command.
