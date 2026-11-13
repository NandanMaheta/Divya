# Divya

**Divya is a private operations platform for managing IPO applications across many family accounts.** One operator lists upcoming mainboard IPOs, decides which to apply for, offers each client a deal (profit share or fixed amount), reminds every account to apply on time, records allotments and the listing price, and settles the money — with a complete history and financial-year reports for everyone involved.

It replaces a manual process of WhatsApp messages, spreadsheets and mental arithmetic with:

- an **admin web panel** for the operator (approvals, IPOs, deals, task board, allotment, settlements, reports);
- a **client app** — Android app and iPhone web app from one codebase — where clients and their family accounts see tasks, deals, allotments, amounts due and earnings;
- a **Go API** with a double-entry ledger, a 5-minute scheduler for reminders and deadlines, and strict audit history;
- a small **Python worker** for IPO data and AI-written company summaries.

> Internal use only: invited users, no public sign-up, no automated IPO applying, no WhatsApp. See `docs/product/` for the full product and legal context.

## Status

| Stage                                               | State       |
| --------------------------------------------------- | ----------- |
| Stage 0 — repository, toolchain, agent pipeline, CI | ✅ Done     |
| Features 002 (scheduler) and 003 (sign-in)          | 🛠 Planning  |
| MVP (27 features, see `docs/product/modules.md`)    | ⏳ ~8 weeks |

## How it works (business flow)

```
Operator adds IPO ─► marks APPLY / DO NOT APPLY ─► publishes deals (profit share by default, fixed price optional)
     │
     ├─► clients pick a deal per family account (fixed picks need operator approval)
     ├─► "Apply" tasks with UPI ID + time window → push reminders every 1–2 h until marked applied
     ├─► allotment day: operator marks allotted / not allotted
     ├─► listing day: operator enters the NSE listing price → every amount is calculated automatically
     └─► client pays (due listing day + 2 weekdays) → taps "Payment done" → operator confirms → settled ✅
```

The exact rules (formulas, due dates, approvals) live in [`docs/product/business-rules.md`](docs/product/business-rules.md) and are enforced by named tests.

## Tech stack

| Area                      | Choice                                                                                              |
| ------------------------- | --------------------------------------------------------------------------------------------------- |
| API                       | Go 1.27 · Huma v2 (OpenAPI 3.1) · pgx v5 · sqlc · goose                                             |
| Database                  | PostgreSQL 18 (Supabase), integer-paise double-entry ledger, append-only audit log                  |
| Worker                    | Python 3.13 · FastAPI · uv · Ruff · pyright (strict)                                                |
| Admin web                 | Vite · React 19 · TanStack Router/Query/Table · Tailwind v4                                         |
| Client app                | Expo SDK 58 · React Native 0.88 · Expo Router (Android APK + iPhone PWA)                            |
| Shared                    | pnpm + Turborepo · TypeScript 7 · Biome 2 · generated TypeScript API client                         |
| Testing                   | Go tests + testcontainers (real Postgres) + property tests · Vitest · pytest · Playwright · Maestro |
| Hosting (₹0/month target) | Google Cloud Run (Mumbai) · Cloud Scheduler · Supabase · Cloudflare Pages, Access and R2            |
| Delivery                  | Spec Kit + Claude Code agents · GitHub Actions                                                      |

## Repository layout

```
apps/
  api/        Go modular monolith — one folder per module under internal/ (health is the reference module)
  worker/     Python data/AI worker
  admin/      Admin web panel
  client/     Client app (Android + web/PWA)
packages/
  domain/     Shared formatting (₹ in paise, Indian grouping) and status vocabulary
  tokens/     Design tokens (colours, radius, spacing) for web and mobile
  api-client/ TypeScript client generated from the API's OpenAPI spec
docs/
  product/    Product blueprint, automation blueprint, business rules, module map, research report
  engineering/git-workflow.md   Branches, naming, releases, worktrees
  adr/        Architecture decision records
specs/        One folder per feature: research, spec, plan, tasks (Spec Kit)
.claude/      AI agents, skills, hooks and path rules
.specify/     Spec Kit config and the project constitution
.github/      CI/CD workflows, PR template, CODEOWNERS
infra/        Docker Compose for local Postgres; infrastructure-as-code (later)
```

## Getting started

Prerequisites: Go 1.27, Node 22 + pnpm 10, Python 3.13 + uv, Docker.

```bash
git clone https://github.com/NandanMaheta/Divya.git divya && cd divya
git switch stage                       # stage is the working branch
make setup                             # pnpm install, go mod download, uv sync
make db-up                             # PostgreSQL 18 in Docker
cp .env.example .env                   # local settings only; never commit real secrets
make migrate
make api                               # API on http://localhost:8080 (docs at /docs)
pnpm --filter @divya/admin dev         # admin panel on http://localhost:5173
pnpm --filter @divya/client start      # client app (press w for web, a for Android)
```

Checks before pushing:

```bash
make test          # Go (incl. Postgres integration), Python, JS tests
make lint          # golangci-lint, Ruff, Biome
pnpm turbo run typecheck build
scripts/check-br-traceability.sh
```

## Branches and releases

`main` is **production**, `stage` is the **default working branch** (auto-deployed to staging). All work happens on short-lived branches from `stage` with readable names — `feat/016-settlement-engine`, `fix/41-otp-resend-timer`, `chore/upgrade-expo-sdk-59` — merged by pull request. Releases are pull requests from `stage` to `main`. Details, hotfixes and parallel work with git worktrees: [`docs/engineering/git-workflow.md`](docs/engineering/git-workflow.md).

## How features get built

Every feature goes through the same pipeline, run by Claude Code agents with the owner approving at a few gates:

1. **Research** → 2. **Spec** (user stories, acceptance criteria, business-rule IDs) → **owner approves** → 3. **Plan** (data model, API contract, test strategy) → 4. **Tasks** (small, parallelisable) → 5. **Tests first**, then implementation in parallel worktrees → 6. **Independent review** + security review → 7. **PR into `stage`**, CI, QA agents → **owner UAT on staging** → 8. **Release** to `main`.

```text
/ship-feature 016      # one feature end to end
/ship-module M7        # every ready feature of a module, with batched approvals
/speckit-bug-assess    # bugs: reproduce → failing test → fix
```

Read [`docs/product/automation-blueprint.md`](docs/product/automation-blueprint.md) for the full pipeline and [`.specify/memory/constitution.md`](.specify/memory/constitution.md) for the non-negotiable engineering rules.

## Key documents

- [Product blueprint](docs/product/product-blueprint.md) — screens, flows, settlement maths, hosting
- [Business rules](docs/product/business-rules.md) — the source of truth for behaviour
- [Module map](docs/product/modules.md) — the 27 features and their build order
- [Automation blueprint](docs/product/automation-blueprint.md) — agents, gates, testing, CI/CD
- [Git workflow](docs/engineering/git-workflow.md) — branches, naming, releases
- [ADRs](docs/adr/) — architecture decisions

## License

Private and proprietary. All rights reserved.
