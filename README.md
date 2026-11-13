# Divya

Private IPO operations platform: admin web panel, client app (Android + iPhone PWA), Go API and Python data worker. Built through a spec-driven, multi-agent pipeline (Spec Kit + Claude Code).

| Path                  | What                                                                   |
| --------------------- | ---------------------------------------------------------------------- |
| `apps/api`            | Go 1.27 modular monolith (Huma v2, pgx v5, sqlc, goose), PostgreSQL 18 |
| `apps/worker`         | Python 3.13 FastAPI worker (IPO data, subscription, prospectus briefs) |
| `apps/admin`          | Vite + React 19 + TanStack Router/Query + Tailwind v4                  |
| `apps/client`         | Expo SDK 58 / React Native 0.88, Expo Router (Android + web/PWA)       |
| `packages/*`          | `domain`, `tokens`, `api-client` (generated from OpenAPI)              |
| `docs/product`        | Product + automation blueprints, business rules, module map            |
| `docs/adr`            | Architecture decisions                                                 |
| `.claude`, `.specify` | Agents, skills, hooks, rules; Spec Kit config and constitution         |

## Quick start

```bash
make setup      # pnpm install, go mod download, uv sync
make db-up      # PostgreSQL 18 in Docker
cp .env.example .env
make migrate
make api        # http://localhost:8080/docs
pnpm --filter @divya/admin dev
pnpm --filter @divya/client start
```

## Building features with agents

```text
/ship-feature 002      # one feature: research → spec → (you approve) → plan → tasks → TDD build → review → PR
/ship-module M7        # every ready feature of a module, with batched spec approvals
/speckit-bug-assess    # bugs: reproduce → failing test → fix
```

See `docs/product/automation-blueprint.md` for the full pipeline and `docs/product/modules.md` for the build order.
