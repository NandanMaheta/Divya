# Quickstart: validate 008 IPO catalogue

How to prove the feature works once it is built. Details are in [plan.md](./plan.md), [data-model.md](./data-model.md) and [contracts/](./contracts/).

## Prerequisites

- 002 Phase A is merged: `scheduler`, `outbox`, `platform/{clock,istdate,db tx,audit}` and migration `00002`.
- 003 is merged: `platform/authz`, `httpx.Problem`/`ErrorModel.code`, audit `At`, and migration `00003`. The HTTP checks below need it.
- Docker is running, so `dbtest.New(t)` can start PostgreSQL 18.

## 1. Static checks and generated code

```bash
cd apps/api
sqlc generate && sqlc vet                      # queries/ipos.sql compiles against migrations 00001–00004
golangci-lint run ./...                        # depguard: internal/ipos imports only platform/*, scheduler, huma, uuid, pgx (store.go)
git diff --exit-code internal/platform/db/dbgen
```

## 2. Unit and property tests (fast, no DB)

```bash
cd apps/api
go test -short -race ./internal/ipos/... ./internal/platform/inr/...
```

Expected:

- `BR_IPO_01` segment cases pass.
- `BR_IPO_03` date-order closure and the step-decision table pass.
- Min-application warning bounds pass (₹9,999 / ₹10,000 / ₹20,000 / ₹20,001).
- Suggestions for Fri 13, Mon 9 and Wed 11 Nov 2026 pass.
- The rapid properties pass.

## 3. Integration tests (real PostgreSQL, fake clock, real ticks)

```bash
cd apps/api
go test -race -run 'BR_IPO_0[123]|SC00[1-8]' ./internal/ipos/...
go test -race ./...                              # whole API suite incl. 002/003 and golden scenarios
```

Expected: the lifecycle walk for the spec's example IPO passes:

| Time (IST) | Status |
|---|---|
| Wed 11 Nov 09:59 | UPCOMING |
| Wed 11 Nov 10:00 | OPEN |
| Fri 13 Nov 16:59 | OPEN |
| Fri 13 Nov 17:00 | CLOSED |
| Mon 16 Nov 10:00 | ALLOTMENT_OUT |
| Wed 18 Nov 10:00 | LISTED |
| any later day | still LISTED |

The downtime catch-up writes three audit rows in order. Overrides, reschedules, signal rollbacks, idempotency and stale versions all pass.

## 4. Contract

```bash
cd apps/api && go run ./cmd/divya openapi > ../../packages/api-client/openapi.yaml
pnpm --filter @divya/api-client generate && pnpm turbo run typecheck test
git diff --stat packages/api-client             # only additive IPO paths/schemas
```

CI runs oasdiff. It must report no breaking change. The `cmd/divya` contract test asserts the following:

- Every `ipos-*` operation declares security: admin operations use `bearer [admin]` and `ipos-list`/`ipos-get` use `bearer []`.
- Every mutation has the `Idempotency-Key` header.
- `ClientIPO` has no `source`, `source_ref`, `last_synced_at`, `status_source` or `version`.

## 5. Manual smoke (local, after `make db-up && make migrate`, with an admin token from 003's `divya admin create` + sign-in)

1. `POST /v1/admin/ipos` with `{"name":"Acme Ltd","segments":["NSE_MAINBOARD","BSE_MAINBOARD"]}` returns 201, `status: UPCOMING`, `published: false`, `apply_flag: null`, `source: manual`.
2. The same call with `"segments":["NSE_EMERGE"]` returns 422 `ipo.sme_not_allowed`.
3. `GET /v1/admin/ipos/date-suggestions?close_date=2026-11-13` returns allotment 2026-11-16, refund and credit 2026-11-17, listing 2026-11-18, plus the notice.
4. `PUT` the facts (band 100000–105000 paise, lot 14, dates), set the flag to APPLY, then publish. `GET /v1/ipos` with a client token now lists it, with `min_application_paise: 1470000`.
5. `POST /v1/admin/ipos/{id}/status` `{"status":"OPEN","reason":"smoke","version":n}` returns 200. Sending it again with the same status returns 409 `ipo.no_change`.
