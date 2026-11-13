# Divya

Private IPO operations platform for one operator and ~100 invited clients: the operator lists mainboard IPOs, publishes deals, assigns "apply" tasks per family account, records allotments and the NSE listing price, and settles money with clients. No public users. No automated IPO applying.

## Read first

- Business rules (source of truth): `docs/product/business-rules.md`. Golden scenarios GS-01…04 must always pass.
- Constitution: `.specify/memory/constitution.md`. Module map and build order: `docs/product/modules.md`.
- Product and pipeline blueprints: `docs/product/`. Decisions: `docs/adr/`.
- Path rules load automatically from `.claude/rules/` for the files you touch.

## Layout

- `apps/api` Go 1.27 modular monolith (Huma, pgx, sqlc, goose). Golden reference module: `apps/api/internal/health`.
- `apps/worker` Python 3.13 FastAPI data/AI worker (uv).
- `apps/admin` Vite + React 19 + TanStack Router/Query admin panel.
- `apps/client` Expo SDK 58 app (Android + iPhone PWA). Read `apps/client/AGENTS.md` before touching it.
- `packages/` `domain` (formatters, enums), `tokens` (design tokens), `api-client` (generated from OpenAPI).

## Commands

- Setup: `make setup` · DB: `make db-up` / `make migrate`
- Go: `cd apps/api && go test -race ./...` · `golangci-lint run ./...` · `sqlc generate && sqlc vet` · short tests: `go test -short ./...`
- Python: `cd apps/worker && uv run pytest -q && uv run ruff check . && uv run pyright`
- JS: `pnpm turbo run typecheck test build` · lint: `pnpm lint`
- Regenerate TS client after API changes: `pnpm --filter @divya/api-client generate`
- Expo deps: always `npx expo install <pkg>` inside `apps/client`.

## How work happens

- Features: `/ship-feature <NNN>` (Spec Kit flow with gates). Modules: `/ship-module <M#>`. Bugs: `/speckit-bug-assess` → `/speckit-bug-fix`. Ideas: `/speckit-assess-intake`.
- Tests first, by `test-engineer`; implementers never edit those tests.
- Done = the task's done command + affected suites + golden scenarios pass, with the output pasted as evidence.

## Git workflow

- `main` = production, `stage` = default working branch (staging). Never commit to either directly.
- Branch from `origin/stage`: `feat/<NNN>-<desc>` (feature number from modules.md), `fix/<issue>-<desc>`, `chore/`, `refactor/`, `docs/`, `test/`; `hotfix/<issue>-<desc>` from `main`. Lowercase, hyphens, 2–5 descriptive words.
- PRs target `stage`; releases are `stage` → `main` PRs. Parallel work: one worktree per branch, folder `../divya-<id>`.
- Full guide: `docs/engineering/git-workflow.md`.

## Hard rules

- Money is integer paise; ledger and audit tables are append-only; never float math on money.
- Never read or write `.env*`, secrets, or production resources. Deploys happen only in CI.
- Stop and ask the owner on: money-rule ambiguity, ledger/auth schema changes, new runtime dependencies, security-sensitive changes, or 3 failed attempts.
- Conventional Commits. Small PRs into `stage`. Update `docs/product/modules.md` status when a feature moves.
