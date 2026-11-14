# Slices: Auth core (003) - pipeline v2, Tier A

**Branch** `feat/003-auth-core` | **Brief** `brief.md` | **Rules** BR-CLI-04, BR-CLI-05 | GS-01..04 must keep passing.

Legend: `[P]` parallel-safe (flat `internal/auth` package: develop as separate worktrees and merge, done commands use `-run`). `red->green`: tests already written and protected, implementation is the next step. `blocked`: needs `002-merged`. Each slice = one `test(...)` then one `feat(...)` commit pair. All done commands run from `/home/user/divya-003`. Tests-before-code: every implementation slice follows its test slice (or has tests written already). Owner gates: S07 (auth migration), S19 (`deploy.yml`), security review before the PR (crypto, authz, audit, handlers). Done T001 (dependencies) is kept; `replaces` in state.json maps to the old T-ids.

- [ ] S01 **backend-dev**: Compile skeleton: exported types and signatures returning errNotImplemented for internal/auth, platform/crypto, authz, httpx (errors, cors, clientip), ratelimit, config (auth, LoadDB), auth/commonpasswords, so every existing red test compiles. Includes Service/Store/Throttle/Deps type shapes from go-capabilities.
  - Depends on: T001-done | Rules: none (mechanism) | Expected: green | Replaces: T001 done (deps)
  - Done command: `cd apps/api && go build ./... && go vet ./internal/auth/... ./internal/platform/...`
- [ ] S02 [P] **backend-dev**: Implement platform/crypto (Keyring AES-256-GCM envelope, BlindIndexer, NewToken, Digest), platform/ratelimit, and config/auth.go + config/db.go. Tests already written (protected).
  - Depends on: S01 | Rules: none (mechanism) | Expected: red->green | Replaces: T002,T003,T010-T013 (tests done)
  - Protected tests (written, do not edit): `apps/api/internal/platform/crypto/aead_test.go`, `apps/api/internal/platform/crypto/blindindex_test.go`, `apps/api/internal/platform/crypto/token_test.go`, `apps/api/internal/platform/ratelimit/ratelimit_test.go`, `apps/api/internal/platform/config/auth_test.go`, `apps/api/internal/platform/config/db_test.go`
  - Done command: `cd apps/api && go test -race ./internal/platform/crypto/... ./internal/platform/ratelimit/... ./internal/platform/config/...`
- [ ] S03 [P] **backend-dev**: Implement platform/authz (Role, Principal, Verifier, Install, Require, Public, ChallengeOnly, SelfAuthenticated, AssertDeclared) and httpx errors, CORS, ClientIP. Tests already written (protected).
  - Depends on: S01 | Rules: none (mechanism) | Expected: red->green | Replaces: T004-T009 (tests done)
  - Protected tests (written, do not edit): `apps/api/internal/platform/authz/authz_test.go`, `apps/api/internal/platform/httpx/errors_test.go`, `apps/api/internal/platform/httpx/cors_test.go`, `apps/api/internal/platform/httpx/clientip_test.go`
  - Done command: `cd apps/api && go test -race ./internal/platform/authz/... ./internal/platform/httpx/...`
- [ ] S04 [P] **backend-dev**: Implement auth pure units A: normalize.go, commonpasswords list (10k.txt via proxy, SOURCE.md, embed.go), password_policy.go, hasher.go (Argon2id, PHC, semaphore, dummy verify), throttle_apply.go. Tests already written (protected). [P] with S05 as a pair in separate worktrees (flat package).
  - Depends on: S01 | Rules: BR-CLI-04 | Expected: red->green | Replaces: T014-T023 (tests done)
  - Protected tests (written, do not edit): `apps/api/internal/auth/normalize_test.go`, `apps/api/internal/auth/normalize_prop_test.go`, `apps/api/internal/auth/commonpasswords/commonpasswords_test.go`, `apps/api/internal/auth/password_policy_test.go`, `apps/api/internal/auth/hasher_test.go`, `apps/api/internal/auth/hasher_prop_test.go`, `apps/api/internal/auth/throttle_apply_test.go`, `apps/api/internal/auth/throttle_apply_prop_test.go`
  - Done command: `cd apps/api && go test -race ./internal/auth/commonpasswords/... && go test -race -run 'TestNormalize|TestKindOf|TestPolicy|TestHasher|TestLockout|TestThrottleApply' ./internal/auth/`
  - Note: Lockout rules (D-08) are covered by TestLockout.
- [ ] S05 [P] **backend-dev**: Implement auth pure units B: tokens.go (go-jose EdDSA, kid ring, P-02 verify order), totp.go (pquerna/otp, N-1..N+1, last_used_step), recovery.go. Tests already written (protected).
  - Depends on: S01, S02 | Rules: none (mechanism) | Expected: red->green | Replaces: T024-T030 (tests done)
  - Protected tests (written, do not edit): `apps/api/internal/auth/tokens_test.go`, `apps/api/internal/auth/tokens_reject_test.go`, `apps/api/internal/auth/totp_test.go`, `apps/api/internal/auth/recovery_test.go`
  - Done command: `cd apps/api && go test -race -run 'TestTokens|TestTotp|TestRecovery' ./internal/auth/`
- [ ] S06 [P] **test-engineer**: Schema tests (real PostgreSQL via dbtest) for all seven auth tables: unique/CHECK/partial-unique/FK constraints and indexes from data-model.md.
  - Depends on: S01 | Rules: none (mechanism) | Expected: red | Replaces: T031,T032
  - Done command: `cd apps/api && go test -race -run 'TestSchema' ./internal/auth/`
- [ ] S07 **backend-dev**: Write migrations/00003_auth.sql (Up and Down) with every table, constraint and index in data-model.md. OWNER GATE: auth schema approval and security review.
  - Depends on: S06 | Rules: none (mechanism) | Expected: green | Replaces: T033,T034
  - Done command: `cd apps/api && go test -race -run 'TestSchema' ./internal/auth/ && go test -race ./internal/platform/db/...`
  - Note: Owner approval + security-reviewer (auth schema).
- [ ] S08 **test-engineer** [blocked: 002-merged]: Tests for 002 extensions and store: lint-boundary cases (scripts/check-lint-boundaries.sh), audit Entry.At / Nil-to-NULL and Principal.AuditActor, authtest fixtures package, store integration tests (users/org, sessions/refresh, mfa/throttles, concurrent CreateUser, lock order).
  - Depends on: S07, 002-merged | Rules: none (mechanism) | Expected: red | Replaces: T035,T037,T042-T045
  - Done command: `cd apps/api && go test -race -run 'TestStore|TestAuditExt|TestAuthtest' ./internal/auth/... ./internal/platform/audit/... && bash ../../scripts/check-lint-boundaries.sh`
- [ ] S09 **backend-dev** [blocked: 002-merged]: golangci depguard rules (P-19), audit extension (Entry.At, Nil to NULL, InsertAuditLog created_at), queries/auth.sql (all parts, @now params), sqlc generate+vet, Principal.AuditActor.
  - Depends on: S08, 002-merged | Rules: none (mechanism) | Expected: green | Replaces: T036,T038,T039-T041
  - Done command: `cd apps/api && sqlc generate && sqlc vet && go test -race -run 'TestAuditExt' ./internal/platform/... && golangci-lint run ./internal/platform/... && bash ../../scripts/check-lint-boundaries.sh`
  - Note: Generated dbgen not counted toward size.
- [ ] S10 **backend-dev** [blocked: 002-merged]: store.go interface and sentinel errors, store_pg.go (users/org, sessions/refresh, mfa/recovery/challenges/throttles; only file with pgx types), Service skeleton (service.go Deps, errors.go problem mapping, audit.go catalogue).
  - Depends on: S09, 002-merged | Rules: none (mechanism) | Expected: green | Replaces: T046-T049
  - Done command: `cd apps/api && go test -race -run 'TestStore|TestAuthtest' ./internal/auth/...`
- [ ] S11 **test-engineer** [blocked: 002-merged]: Service tests A (fake clock, real Store): accounts status machine (BR_CLI_04), CreateClient/ResetPassword/Replace*/EndAllSessions, throttle service, client sign-in, sign-in gate generic failures, lockout through service.
  - Depends on: S10, 002-merged | Rules: BR-CLI-04, BR-CLI-05 | Expected: red | Replaces: T050,T051,T054,T056,T057,T066
  - Done command: `cd apps/api && go test -race -run 'TestAccounts|TestThrottle|TestSignIn|TestLockoutService' ./internal/auth/`
- [ ] S12 **backend-dev** [blocked: 002-merged]: accounts.go, accounts_more.go, throttle.go, signin.go (one Argon2 verify, short tx), lockout application in sign-in.
  - Depends on: S11 | Rules: BR-CLI-04, BR-CLI-05 | Expected: green | Replaces: T052,T053,T055,T058,T067
  - Done command: `cd apps/api && go test -race -run 'TestAccounts|TestThrottle|TestSignIn|TestLockoutService' ./internal/auth/`
- [ ] S13 **test-engineer** [blocked: 002-merged]: Service tests B: refresh rotation/reuse/device mismatch/expiry boundaries, concurrency (N goroutines one R1; parallel sign-in), sessions, bearer and challenge verifiers, password change.
  - Depends on: S12 | Rules: BR-CLI-04 | Expected: red | Replaces: T059,T060,T062,T064,T068
  - Done command: `cd apps/api && go test -race -run 'TestRefresh|TestConcurrency|TestSessions|TestVerifier|TestAuthz|TestPasswordChange' ./internal/auth/`
- [ ] S14 **backend-dev** [blocked: 002-merged]: refresh.go (P-03 algorithm), sessions.go, verifiers.go, password_change.go.
  - Depends on: S13 | Rules: BR-CLI-04 | Expected: green | Replaces: T061,T063,T065,T069
  - Done command: `cd apps/api && go test -race -run 'TestRefresh|TestConcurrency|TestSessions|TestVerifier|TestAuthz|TestPasswordChange' ./internal/auth/`
  - Note: Security review: crypto, authz.
- [ ] S15 **test-engineer** [blocked: 002-merged]: Tests: admin MFA flow and management, TOTP/admin-removal concurrency, bootstrap service, CLI `swastik admin create`, housekeeping.
  - Depends on: S14 | Rules: none (mechanism) | Expected: red | Replaces: T070-T072,T075,T077,T079
  - Done command: `cd apps/api && go test -race -run 'TestMFA|TestBootstrap|TestAdminCreate|TestHousekeep' ./internal/auth/ ./cmd/swastik/`
- [ ] S16 **backend-dev** [blocked: 002-merged]: mfa.go, mfa_manage.go, bootstrap.go (advisory lock, audit), cmd/swastik/admin.go, housekeeping.go.
  - Depends on: S15 | Rules: none (mechanism) | Expected: green | Replaces: T073,T074,T076,T078,T080
  - Done command: `cd apps/api && go test -race -run 'TestMFA|TestBootstrap|TestAdminCreate|TestHousekeep' ./internal/auth/ ./cmd/swastik/`
  - Note: Security review: TOTP, bootstrap.
- [ ] S17 **test-engineer** [blocked: 002-merged]: Tests: CSRF, handlers (sessions, MFA, password, admin), static and runtime contract walk, openapi/wiring tests, packages/domain auth-error-codes test.
  - Depends on: S16 | Rules: none (mechanism) | Expected: red | Replaces: T081-T084,T089-T091,T098
  - Done command: `cd apps/api && go test -race -run 'TestCSRF|TestHandler|TestContractWalk|TestOpenAPI|TestWiring' ./internal/auth/ ./cmd/swastik/ && pnpm --filter @swastik/domain test`
- [ ] S18 **backend-dev** [blocked: 002-merged]: cookies.go, handler.go, handler_mfa.go, handler_admin.go (Security declared per operation, errors and rate limits).
  - Depends on: S17 | Rules: none (mechanism) | Expected: green | Replaces: T085-T088
  - Done command: `cd apps/api && go test -race -run 'TestCSRF|TestHandler' ./internal/auth/`
  - Note: Security review: handlers.
- [ ] S19 **backend-dev** [blocked: 002-merged]: Wire buildAPI and serve (InstallErrors, authz.Install, auth.Register, AssertDeclared, scheduler registration, CORS, rate limit), regenerate OpenAPI and TS client, packages/domain auth-error-codes.ts, deploy.yml secrets (OWNER GATE, no values), modules.md status.
  - Depends on: S18 | Rules: none (mechanism) | Expected: green | Replaces: T092,T093,T097,T099,T100,T101
  - Done command: `cd apps/api && go test -race -run 'TestContractWalk|TestOpenAPI|TestWiring' ./cmd/swastik/ && cd ../.. && pnpm --filter @swastik/domain test && pnpm --filter @swastik/api-client generate && python3 -c "import yaml;yaml.safe_load(open('.github/workflows/deploy.yml'))"`
  - Note: Owner gate: deploy.yml (security-sensitive).
- [ ] S20 **test-engineer** [blocked: 002-merged]: Verification tests (no-secrets scan, audit events per US-11, timing SC-003 and Argon2id benchmark) then final gate: tidy, full API -race, golangci-lint, sqlc vet, generated code current, BR traceability, JS typecheck/test/build, GS-01..04.
  - Depends on: S19 | Rules: BR-CLI-04 | Expected: green | Replaces: T094-T096,T102
  - Done command: `cd apps/api && go mod tidy && git diff --exit-code go.mod go.sum && go test -race ./... && golangci-lint run ./... && sqlc generate && sqlc vet && git diff --exit-code internal/platform/db/dbgen && cd ../.. && bash scripts/check-br-traceability.sh && pnpm turbo run typecheck test build`
  - Note: Final gate. Security review before PR.

## Order

- Start now: S01 (skeleton), then in parallel S02, S03, S04, S05 (tests already written) and S06 (schema tests); then S07 (migration, owner gate).
- Blocked on 002-merged: S08 to S20 (S08 needs 002 for audit/dbtest/TxRunner).
- Critical path: S01 -> S06 -> S07 -> S08 -> S09 -> S10 -> S11 -> S12 -> S13 -> S14 -> S15 -> S16 -> S17 -> S18 -> S19 -> S20.
