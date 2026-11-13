---

paths:

- "apps/api/**"

---

# Go API rules

- Copy the shape of `internal/health`: `service.go` (logic behind small interfaces, injected clock), `handler.go` (Huma `Register(api, svc)`), table tests + `humatest` handler tests.
- One module per folder in `internal/`; modules talk via exported interfaces or outbox events, never another module's tables. `internal/platform` imports no business module (depguard).
- SQL lives in `apps/api/queries/*.sql` → `sqlc generate`; no string-built SQL. Migrations: new goose file `migrations/NNNNN_<name>.sql`; never edit an applied migration.
- Integration tests use `dbtest.New(t)` (real PostgreSQL 18). Mark slow tests so `go test -short` skips them.
- Errors: wrap third-party errors with context (`fmt.Errorf("x: %w", err)`); return `huma.ErrorXXX` for HTTP errors (RFC 9457).
- Money: `int64` paise only; rounding only in `internal/settlement`. Time: inject `func() time.Time`; store UTC; business dates in Asia/Kolkata.
- Every mutation writes an `audit_log` row in the same transaction.
- Test names include the business-rule ID they cover, e.g. `TestSettle_BR_SET_05_FixedAllottedProfit`.
