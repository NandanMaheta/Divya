# Quickstart: validate 002 Scheduler tick + scheduled jobs + outbox

Run everything from `apps/api` unless noted. Integration tests need Docker (testcontainers, `postgres:18-alpine`); `-short` skips them.

## 1. Static checks

```bash
cd apps/api
sqlc vet && sqlc generate && git diff --exit-code   # generated code current
golangci-lint run ./...                              # depguard boundaries, forbidigo (no time.Now / time.Local outside clock, httpx, cmd)
```

Expected: no findings. A deliberate `import ".../internal/scheduler"` inside `internal/platform/...` or `internal/outbox/...` must fail depguard.

## 2. Unit + property tests (fast, no Docker)

```bash
go test -short -race ./internal/platform/istdate/... ./internal/platform/clock/... \
  ./internal/platform/retry/... ./internal/platform/tickauth/... ./internal/platform/config/...
go test -short -race -run 'GS_04|BR_SET_08' ./internal/platform/istdate/...
```

Expected: GS-04 (Fri 13 Nov 2026 → Tue 17 Nov 23:59 IST; Thu 12 Nov → Mon 16 Nov 23:59 IST) and every US4 case pass, also with `time.Local` forced to a non-IST zone; rapid properties pass (default 100 checks; `-rapid.checks=10000` for a deeper run). Tick auth tests cover missing / malformed / expired / wrong-audience / wrong-issuer / wrong-key / wrong-email tokens with a local JWKS server, no network.

## 3. Integration tests (real PostgreSQL 18)

```bash
go test -race ./internal/scheduler/... ./internal/outbox/... ./internal/platform/db/... ./internal/platform/audit/...
go test -race -run 'BR_IPO_03' ./internal/scheduler/...
go test -race -run 'BR_TASK_03' ./internal/outbox/...
go test -race -run 'TestLifecycle' -v ./internal/scheduler/...       # two-week IPO time travel, must finish < 60 s (SC-006)
go test -race -run 'TestTick_Concurrent' -count=20 ./internal/scheduler/...   # overlapping ticks, flake hunt
```

Expected: all pass; the lifecycle test logs its elapsed time; no real sleeps anywhere (fake clock).

Nightly / on demand soak (SC-001, 30 simulated days, 100 clients, 10 IPOs, random duplicate/overlapping/skipped ticks and crashes):

```bash
SWASTIK_SOAK=full go test -race -run 'TestSoak_SC_001' -timeout 20m ./internal/scheduler/...
```

(PR CI runs the same test with a 3-day slice.)

## 4. Contract check

```bash
go test -run 'TestOpenAPI_NoInternalOperations' ./cmd/swastik/...
go run ./cmd/swastik openapi > /tmp/openapi.yaml && diff -u ../../packages/api-client/openapi.yaml /tmp/openapi.yaml
```

Expected (Phase A): no `/internal` path in the document and no diff at all, because the tick is hidden and admin operations are not registered yet. Phase B (after 003): the diff shows only the additive `/v1/admin/scheduler/*` and `/v1/admin/outbox/*` paths from `contracts/admin-scheduler.openapi.yaml`; regenerate with `pnpm --filter @swastik/api-client generate`.

## 5. Manual smoke on a local database

```bash
make db-up && make migrate
cd apps/api
APP_ENV=local TICK_AUTH=dev TICK_DEV_TOKEN=local-dev-token-123 DATABASE_URL=<local url from infra/docker/compose.yaml> \
  go run ./cmd/swastik serve
# another shell:
curl -s -X POST localhost:8080/internal/tick                                              # 401, generic problem body
curl -s -X POST -H 'Authorization: Bearer wrong' localhost:8080/internal/tick             # 401
curl -s -X POST -H 'Authorization: Bearer local-dev-token-123' localhost:8080/internal/tick  # 200 TickSummary, all zeros
```

Then in `psql`: `SELECT started_at, status FROM tick_runs ORDER BY started_at DESC LIMIT 1;` shows the tick, and one pending `scheduler.retention` job per org exists with `run_at` at the next 03:00 IST.

Fail-closed check:

```bash
APP_ENV=staging TICK_AUTH=dev DATABASE_URL=... go run ./cmd/swastik serve   # exits non-zero: dev tick auth not allowed in staging
```

## 6. Done criteria for the feature

- Sections 1–4 green in CI (`pr.yml` api job), plus `scripts/check-br-traceability.sh` shows tests named with BR_IPO_03, BR_TASK_03, BR_SET_08 and GS_04.
- Logs of a tick contain `tick_id`, counts and `took_ms`, and never an `Authorization` value.
