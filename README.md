# Swastik

**A private IPO operations platform: one operator, about 100 invited clients and their family accounts, every IPO from "apply" to "settled".**

Built by **[Nandan Maheta](https://github.com/NandanMaheta)**. It's an AI-native engineering project: specified, planned, tested and built by a pipeline of Claude Code agents under one owner's direction.

> Internal use only: invite-only users, no public sign-up, no automated IPO applying, no WhatsApp.

---

## 1. The problem

An operator funds IPO applications for a circle of clients, many of whom manage several family demat accounts. Today the operator does it all by hand:

- WhatsApp messages: "apply for X before 5 pm";
- chasing who applied;
- checking allotments one PAN at a time;
- working out each person's share on listing day;
- tracking who has paid.

It's slow and easy to get wrong, and there's no reliable history.

## 2. What Swastik does

```
Admin adds IPO ──► marks APPLY / DO NOT APPLY ──► publishes deals
     │                                              (profit share by default, fixed price optional)
     ├─► Client picks a deal per family account ──► fixed-price picks need admin approval
     ├─► "Apply" task per account (UPI ID + time window) ──► push reminders every 1–2 h until marked applied
     ├─► Allotment day: admin marks allotted / not allotted (bulk)
     ├─► Listing day: admin enters the NSE listing price ──► every amount is calculated automatically
     └─► Client pays (due listing day + 2 weekdays) ──► "Payment done" ──► admin confirms ──► Settled ✅
```

**Deals and settlement.** I = issue price × shares; G = listing gain = (listing price − issue price) × shares.

| Deal                             | Not allotted               | Allotted with profit             | Allotted at a loss                                 |
| -------------------------------- | -------------------------- | -------------------------------- | -------------------------------------------------- |
| **Profit share p%** (default)    | nothing either way         | client returns **I + G − p·G**   | client returns **I + G** (operator bears the loss) |
| **Fixed ₹F** (+ ₹40 brokerage B) | operator pays client **F** | client returns **I + G − F − B** | client returns **I + G − F**                       |

Example: investment ₹15,000, profit ₹3,000. A 30% share means the client returns **₹17,100**; a fixed ₹500 deal means **₹17,460**. All rules live in [`docs/product/business-rules.md`](docs/product/business-rules.md) and are enforced by named tests ("golden scenarios").

**Two apps:**

| Admin web panel (operator)                                                                                                                              | Client app (Android + iPhone web app)                                                                                                                    |
| ------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Today dashboard, approvals queue, IPOs, deals and live fixed-price quotes, task board, allotment, settlements, clients, FY / quarter reports, audit log | Sign-up with email + SMS OTP, festival greetings, IPO list, deals, tasks with reminders, allotment, payments, earnings reports, up to 10 family accounts |

## 3. Architecture

```
  Admin panel (Vite + React) ── Cloudflare Pages + Access ──┐
  Client app (Expo: Android APK + iPhone PWA) ──────────────┤
                                                            ▼
                     ┌──────────────────────────────────────────────┐
                     │ Go API: modular monolith (Cloud Run, Mumbai)   │
                     │ auth · clients · IPOs · deals · tasks ·        │
                     │ allotment · settlement ledger · payments ·     │
                     │ notifications · reports · scheduler            │
                     └──────┬──────────────────────────┬──────────────┘
                            │                          │
  Cloud Scheduler ─ 5 min ─► /internal/tick      PostgreSQL 18 (Supabase, Mumbai)
  (reminders, deadlines,                         append-only ledger + audit log
   status changes, outbox relay)
                            │
  Push (FCM / Web Push) · Email (Resend) · SMS OTP (Firebase) ◄── outbox
  Python worker (FastAPI): IPO data import, prospectus → AI company brief
```

Key design decisions (see [`docs/adr/`](docs/adr/)):

- **Modular monolith in Go:** one binary, hard module boundaries enforced by lint, easy to split later.
- **Money in integer paise, double-entry ledger:** append-only; corrections are reversals; every change is audited.
- **Tick-driven scheduler:** one 5-minute tick drives every reminder and deadline, so it runs on scale-to-zero hosting at about ₹0 a month.
- **One Expo codebase:** the Android app and the iPhone PWA come from the same code.
- **Contract-first API:** the OpenAPI 3.1 spec is generated from Go, and the TypeScript client is generated from the spec.

## 4. Tech stack

| Area              | Technology                                                                                                               |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------ |
| API               | **Go 1.27**, Huma v2 (OpenAPI 3.1, RFC 9457 errors), pgx v5, sqlc, goose                                                 |
| Database          | **PostgreSQL 18** on Supabase (Mumbai), integer-paise ledger, append-only audit                                          |
| Worker            | **Python 3.13**, FastAPI, uv, Ruff, pyright (strict)                                                                     |
| Admin web         | **React 19**, Vite, TanStack Router / Query / Table, Tailwind CSS v4                                                     |
| Client app        | **Expo SDK 58**, React Native 0.88, Expo Router (Android + PWA)                                                          |
| Shared            | pnpm + Turborepo monorepo, TypeScript 7, Biome 2, generated API client, design tokens                                    |
| Auth              | Argon2id, EdDSA JWT + rotating device-bound refresh tokens, admin TOTP 2FA                                               |
| Testing           | Go tests + testcontainers (real Postgres) + property tests (rapid) + mutation tests; Vitest; pytest; Playwright; Maestro |
| Infra (~₹0/month) | Google Cloud Run + Cloud Scheduler (Mumbai), Supabase, Cloudflare (DNS, Pages, Access, R2), Firebase, Resend, Sentry     |
| CI/CD             | GitHub Actions: lint, tests, contract and secret checks; OIDC deploys (no stored cloud keys)                             |

## 5. AI-native delivery workflow

Swastik is built by **Claude Code agents** following **GitHub Spec Kit**. The owner approves; the agents do the rest.

```
idea ─► researcher ─► spec-writer ─► OWNER approves spec ─► architect (plan + ADRs) ─► planner (small tasks)
     ─► test-engineer writes FAILING tests ─► developers make them pass (parallel git worktrees)
     ─► reviewer + security-reviewer (fresh context) ─► PR into stage ─► CI ─► QA agents
     ─► OWNER tests on staging ─► release PR stage → main ─► OWNER approves production deploy
```

- **17 role agents** in [`.claude/agents/`](.claude/agents/): researcher, spec-writer, architect, planner, test-engineer, backend/admin/mobile/data developers, designer, reviewer, security-reviewer, QA web/mobile, release, triage, ops.
- **Guard-rail hooks** in [`.claude/hooks/`](.claude/hooks/):
  - every edit is formatted and type-checked;
  - developers can't edit the tests written for them;
  - secrets, production and force-pushes are blocked;
  - an agent isn't done until its task's tests pass.
- **Constitution** in [`.specify/memory/constitution.md`](.specify/memory/constitution.md): non-negotiable rules (exact money, test-first, module boundaries, security, performance budgets).
- **Traceability:** every business rule has a named test; CI fails if one is missing.
- The full pipeline is described in [`docs/product/automation-blueprint.md`](docs/product/automation-blueprint.md).

## 6. Repository layout

```
apps/api          Go API (one folder per module in internal/; health = reference module)
apps/worker       Python data/AI worker
apps/admin        Admin web panel
apps/client       Client app (Android + web/PWA)
packages/         domain (₹ formatting, statuses) · tokens (design) · api-client (generated)
docs/product      Blueprints, business rules, module map (build order), research
docs/adr          Architecture decisions · docs/design: design direction + screen specs
docs/engineering  Git workflow: branches, PRs, releases, worktrees
specs/NNN-*       Per feature: research, spec, plan, tasks, state (Spec Kit)
.claude/ .specify/  Agents, skills, hooks, rules; Spec Kit + constitution
.github/          CI/CD workflows, PR template, CODEOWNERS
```

## 7. Branches and releases

- `main` = **production**; `stage` = **default working branch** (auto-deploys to staging).
- Work happens on short-lived branches from `stage`: `feat/016-settlement-engine`, `fix/41-otp-resend-timer`, `chore/…`. Changes merge by PR, with CI required.
- Releases are a PR from `stage` → `main`; production deploys wait for the owner's approval.
- Details: [`docs/engineering/git-workflow.md`](docs/engineering/git-workflow.md).

## 8. Run it locally

Prerequisites: Go 1.27, Node 22 + pnpm 10, Python 3.13 + uv, Docker.

```bash
git clone https://github.com/NandanMaheta/Swastik.git swastik && cd swastik && git switch stage
make setup && make db-up && cp .env.example .env && make migrate
make api                               # API on http://localhost:8080 (docs at /docs)
pnpm --filter @swastik/admin dev       # admin panel on http://localhost:5173
pnpm --filter @swastik/client start    # client app (press w for web, or scan with Expo Go)
make test && make lint                 # all tests and linters
```

## 9. Status

| Stage                                                                       | State         |
| --------------------------------------------------------------------------- | ------------- |
| Foundation: monorepo, CI/CD, agent pipeline, design direction               | ✅ Done       |
| 002 Scheduler · 003 Sign-in · 008 IPO catalogue                             | 🛠 In progress |
| MVP: 27 features (see [`docs/product/modules.md`](docs/product/modules.md)) | ⏳ Planned    |

## 10. Key documents

[Product blueprint](docs/product/product-blueprint.md) · [Business rules](docs/product/business-rules.md) · [Module map](docs/product/modules.md) · [Automation blueprint](docs/product/automation-blueprint.md) · [Design direction](docs/design/design-direction.md) · [Git workflow](docs/engineering/git-workflow.md) · [ADRs](docs/adr/)

---

Built by **Nandan Maheta**: product, architecture and direction; implementation by an AI agent pipeline under his review.
Private and proprietary. All rights reserved.
