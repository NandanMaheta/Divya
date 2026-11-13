# Quickstart: validate 003 Auth core

Run commands from `apps/api` unless noted. Integration tests need Docker (testcontainers, `postgres:18-alpine`); `-short` skips them. Design references: [plan.md](./plan.md), [data-model.md](./data-model.md), [contracts/](./contracts/).

## 0. Prerequisites

- Feature 002 Phase A is merged and this branch is rebased on it (plan C-1…C-12): `migrations/00002_scheduler_outbox.sql`, `internal/platform/{clock,db/tx.go,audit,istdate}` and `internal/scheduler` exist.
- The owner has approved the dependencies in plan "Dependencies requiring owner approval"; `go.mod` lists `github.com/go-jose/go-jose/v4`, `github.com/pquerna/otp` (or the stdlib fallback is in `internal/auth/totp.go`), `golang.org/x/crypto`, `golang.org/x/term` and `golang.org/x/text` as direct requirements.
- Go 1.27.4, Docker, `sqlc` v1.31.1, `golangci-lint` v2.

## 1. Static checks

```bash
cd apps/api
sqlc generate && sqlc vet && git diff --exit-code   # generated code current (uuid override from 002)
golangci-lint run ./...                              # depguard P-19, forbidigo (002), gosec
```

Expected: no findings. Boundary self-test (revert afterwards): add `import _ "github.com/NandanMaheta/divya/apps/api/internal/auth"` to any file in `internal/platform/authz`, or import `github.com/go-jose/go-jose/v4` from `internal/health`; `golangci-lint run ./...` must fail with the `platform-is-a-leaf` or `jose-only-in-auth-and-tickauth` rule.

## 2. Unit and property tests (no Docker)

```bash
go test -short -race ./internal/auth/... ./internal/platform/authz/... ./internal/platform/crypto/... \
  ./internal/platform/ratelimit/... ./internal/platform/httpx/... ./internal/platform/config/...
go test -short -race -run 'BR_CLI_04' ./internal/auth/...
go test -short -race -run 'TestLockout|TestAccessToken|TestTotp|TestMfaChallenge' ./internal/auth/...
```

Expected: all pass with no real waiting (002's `clock.Fake`). The lockout boundaries (locked at T+18m59s, open at T+19m, no extension during a lock, sliding window), the access token rejected at exactly `exp`, the TOTP RFC 6238 vectors with ±1 step and no replay, and the password policy codes all hold. `rapid` properties use 100 checks by default (`-rapid.checks=10000` for a deeper run).

## 3. Integration tests (real PostgreSQL 18)

```bash
go test -race ./internal/auth/... ./internal/platform/audit/... ./cmd/divya/...
go test -race -run 'TestRefresh_' -v ./internal/auth/...            # rotation, reuse, device binding, lifetimes ±1 min
go test -race -run 'Concurrent' -count=20 ./internal/auth/...        # parallel refresh / TOTP / bootstrap: exactly one wins
go test -race -run 'TestSecretScan' ./internal/auth/...              # SC-006: no secret in audit, logs or responses
go test -race -run 'TestHousekeeping' ./internal/auth/... ./cmd/divya/...  # auth.housekeeping via 002's tick harness
```

Expected: all pass. The secret scan reports the number of audit rows, log lines and responses scanned and zero hits.

Nightly (noisy on PR runners), SC-003 timing:

```bash
go test -race -run 'Timing' -count=1 ./internal/auth/...
```

Expected: the six D-09 failure cases return identical status and body bytes; median times within 10%.

## 4. Contract checks

```bash
go test -run 'TestContract|TestOpenAPI|TestWiring' ./cmd/divya/...
go run ./cmd/divya openapi > /tmp/openapi.yaml && diff -u ../../packages/api-client/openapi.yaml /tmp/openapi.yaml
```

Expected:

- The static walk finds Security on every operation; the public set is exactly `health-live`, `health-ready`, `auth-sign-in`, `auth-refresh`; the challenge set is exactly the three MFA operations; admin operations match `cmd/divya/testdata/admin-operations.txt`.
- The runtime walk gets 401/403 with the right `code` for every non-public operation and records zero writes.
- `POST /internal/tick` (hidden, 002) without credentials answers tickauth's 401, not 500.
- Before regeneration the diff shows only the additive changes listed in plan "API operations"; after `go run ./cmd/divya openapi > ../../packages/api-client/openapi.yaml && pnpm --filter @divya/api-client generate` it is empty and oasdiff reports no breaking change.

## 5. Manual smoke on a local database

```bash
make db-up && make migrate
cd apps/api
export APP_ENV=local DATABASE_URL=<local url from infra/docker/compose.yaml>
printf '%s\n' 'correct horse battery staple' | go run ./cmd/divya admin create --email owner@example.com --name Owner
# stdout: admin created: id=<uuid> email=owner@example.com   (no password anywhere)
printf '%s\n' 'another long passphrase 42' | go run ./cmd/divya admin create --email other@example.com --name Other; echo "exit=$?"
# stderr: error: bootstrap.admin_exists: ...   exit=1   (US-9 s2; nothing changed)
go run ./cmd/divya serve      # keys unset: ephemeral keys + one WARN line, no key values
```

In a second shell (mobile transport, so curl needs no cookie handling):

```bash
curl -s localhost:8080/v1/auth/devices                      # 401, code auth.unauthenticated, WWW-Authenticate: Bearer
curl -s -X POST localhost:8080/v1/auth/sign-in -H 'Content-Type: application/json' -d '{
  "identifier":"Owner@Example.com","password":"wrong password 123","client_type":"mobile",
  "device":{"id":"0193a1c0-0000-7000-8000-000000000001","name":"curl","platform":"android"}}'
# 401, code auth.invalid_credentials (same body for an unknown identifier)
curl -s -X POST localhost:8080/v1/auth/sign-in -H 'Content-Type: application/json' -d '{
  "identifier":"owner@example.com","password":"correct horse battery staple","client_type":"mobile",
  "device":{"id":"0193a1c0-0000-7000-8000-000000000001","name":"curl","platform":"android"}}'
# 200, next_step=mfa_enrolment_required, challenge.challenge_token=dmc_…
curl -s -X POST localhost:8080/v1/auth/mfa/enrolment -H "Authorization: Bearer dmc_…"
# 200, secret + otpauth_uri: add it to an authenticator app (or: oathtool --totp -b <secret>)
curl -s -X POST localhost:8080/v1/auth/mfa/enrolment/confirm -H "Authorization: Bearer dmc_…" \
  -H 'Content-Type: application/json' -d '{"code":"<6 digits>"}'
# 200, session (access_token, refresh_token drt_…) + 10 recovery codes shown once
curl -s localhost:8080/v1/auth/devices -H "Authorization: Bearer <access_token>"     # 200, one device, is_current=true
curl -s -X POST localhost:8080/v1/auth/refresh -H 'Content-Type: application/json' \
  -d '{"device_id":"0193a1c0-0000-7000-8000-000000000001","refresh_token":"drt_…"}'  # 200, new pair
# repeat the same refresh call with the OLD drt_…: 401 auth.refresh_rejected, and the new pair stops working too (D-10)
```

Then in `psql`: `SELECT action, actor_kind, after FROM audit_log ORDER BY created_at;` shows bootstrap, failure, challenge, enrolment, sign-in, refresh and reuse entries, with no password, code, secret or token in any row. `SELECT kind, run_at FROM scheduled_jobs WHERE kind = 'auth.housekeeping';` shows one pending row per org at the next 03:30 IST once a tick has run (002's dev tick, see 002 quickstart §5).

Cookie (web) flow: exercised by `csrf_test.go`, and end to end by 022's Playwright suite through the Vite proxy (same-origin `http://localhost`, plan P-04).

Fail-closed check:

```bash
APP_ENV=staging DATABASE_URL=... go run ./cmd/divya serve   # exits non-zero: JWT_SIGNING_KEYS / PII keys / BLIND_INDEX_KEY required
```

## Key generation

Each key is 32 random bytes, base64-encoded. Generate locally only when you want stable keys across restarts; never commit them or paste them into chat.

```bash
openssl rand -base64 32     # one Ed25519 seed, one PII key, or the blind-index key
export JWT_SIGNING_KEYS="k1:<seed>" JWT_ACTIVE_KID=k1
export PII_ENCRYPTION_KEYS="1:<key>" PII_ACTIVE_KEY_ID=1 BLIND_INDEX_KEY="<key>"
```

Staging and production read the three secret values from Secret Manager (`jwt-signing-keys`, `pii-encryption-keys`, `blind-index-key`) and the active ids and `WEB_ORIGINS` from repository variables (plan P-14). Only the owner creates or changes them.

## Key rotation

**JWT signing keys** (planned rotation, no user impact):

1. Add `k2:<new seed>` to `jwt-signing-keys`, keeping `k1`. Deploy (k1 still signs; every instance now verifies both).
2. Set `JWT_ACTIVE_KID=k2`. Deploy.
3. After the rollout plus 15 minutes (the longest access-token lifetime), remove `k1`. Deploy.

Emergency (key leaked): do all three steps in one deploy. Tokens signed with `k1` are rejected; apps receive 401, call refresh (refresh tokens are opaque and unaffected) and continue. Nobody is signed out.

**PII encryption keys** (TOTP secrets today; PAN from 006):

1. Add `2:<new key>` to `pii-encryption-keys`, deploy; then set `PII_ACTIVE_KEY_ID=2`, deploy. New seals use key 2; old envelopes still open with key 1.
2. Keep key 1 until nothing references it: `SELECT count(*) FROM admin_mfa WHERE substring(secret_enc FROM 2 FOR 2) = '\x0001'::bytea OR substring(pending_secret_enc FROM 2 FOR 2) = '\x0001'::bytea;` returns 0. In 003 an authenticator replacement re-seals the secret; bulk re-encryption is 006's concern.

**Blind-index key**: do not rotate casually. In 003 it keys only lockout subjects, so rotation resets every lockout counter (an attacker regains one fresh 5-attempt window). Once 006 stores PAN hashes, rotation needs versioned hashes (plan R-8).

## Done criteria for the feature

- Sections 1–4 green in CI (`pr.yml`), the nightly timing test green, and `scripts/check-br-traceability.sh` finds `BR_CLI_04` in test names; GS-01…04 still pass.
- The owner has created the staging secrets and variables before merge (plan "Owner actions"), and the staging deploy starts (it refuses to start without them).
- Argon2 benchmark and staging sign-in p95 are reported in the PR (SC-007, R-6).
