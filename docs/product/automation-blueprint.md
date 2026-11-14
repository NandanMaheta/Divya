# Swastik: automated delivery blueprint (zero → MVP)

> **Superseded in part by delivery pipeline v2** (`docs/engineering/delivery-pipeline.md`, 2026-11-14): risk tiers, one-page feature briefs, a generated code map, 8–20 slices per feature (compile skeleton first), batched test/code passes, shared-Postgres tests, per-role models and metrics in `state.json`. Where this document differs, v2 wins. Quality gates below are unchanged.

Companion to `Product blueprint.md`. It defines how agents build Swastik with minimal human involvement. It covers:

- understanding the business rules
- spec, research and plan
- breaking work into modules and tasks
- implementation and review
- nested automated tests
- manual acceptance
- deployment and the bug loop

Basis: research notes in `research_notes/…/ai_agent_delivery_workflow.md` (Claude Code 2.1.323, Spec Kit 1.4.0, November 2026) and the plugin catalogue checked on 13 Nov 2026.

---

## 0. Design principles (why this pipeline works without babysitting)

1. **Deterministic gates decide "done", not the agent's opinion.** Types, linters, tests, contract checks and E2E flows pass or fail. AI review is a second opinion, never the gate.
2. **Business rules are executable.** Every rule in the catalogue (Section 1) becomes a named test. Your worked examples (person A, ₹86,580) are permanent "golden scenarios" that must always pass.
3. **Small vertical slices.** Product → epics → features → tasks. A task is one agent, one test file and about 200 lines changed. Small tasks are where agents are reliable.
4. **Test first.** For every task, a separate agent writes failing tests before the implementer starts. The implementer may not edit those tests; a hook blocks it.
5. **Fresh-context review.** The reviewer has never seen the implementation conversation. It checks the code against the spec and tasks only.
6. **Parallel but isolated.** Backend, admin, mobile and data work run in separate git worktrees on separate branches, so they cannot overwrite each other.
7. **Humans only where judgement is irreplaceable:** approving the spec pack, acceptance testing, and production deploys. Agents **stop and ask** on money-formula ambiguity, ledger schema changes, new dependencies, security-sensitive changes, or three consecutive failed fix attempts.
8. **Rules are enforced by tooling, not prose.** CLAUDE.md stays short. Anything that must always or never happen lives in hooks, deny rules, CI or architecture lint.

---

## 1. Business-rules catalogue (source of truth for specs and tests)

Agents derive specs from this table, and every rule ID must appear in at least one test name (CI checks this). Notation:

- **I** = issue price × shares (investment)
- **G** = (NSE listing price − issue price) × shares (listing profit; negative on a loss)
- **F** = fixed amount, **B** = brokerage, **p** = profit-share %

| ID                                      | Rule                                                                                                                                                                                                                         |
| --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **IPO**                                 |                                                                                                                                                                                                                              |
| BR-IPO-01                               | Only mainboard IPOs exist in the system (SME rejected at the API).                                                                                                                                                           |
| BR-IPO-02                               | Every IPO has an admin flag: APPLY or DO NOT APPLY, plus an optional note. Only APPLY IPOs get deals and tasks.                                                                                                              |
| BR-IPO-03                               | Lifecycle UPCOMING → OPEN → CLOSED → ALLOTMENT_OUT → LISTED → SETTLED is time-driven by IPO dates; the admin can override. A date change reschedules every dependent job.                                                    |
| BR-IPO-04                               | Expected profit = admin GMP × lot size. Every GMP change is stored with time and author; clients see the latest value plus its "updated at" time.                                                                            |
| **Clients and family accounts**         |                                                                                                                                                                                                                              |
| BR-CLI-01                               | A main member has name, PAN, mobile and email (all required, PAN unique).                                                                                                                                                    |
| BR-CLI-02                               | A main member can manage up to 10 family accounts (name + PAN). The main member's own PAN is also an account.                                                                                                                |
| BR-CLI-03                               | Adding, editing or deleting a family account creates an approval request. It takes effect only after admin approval, and the admin is notified either way.                                                                   |
| BR-CLI-04                               | New registrations need email OTP + mobile OTP verification, then admin approval, before login is allowed.                                                                                                                    |
| BR-CLI-05                               | Changing email or mobile needs an OTP to the new contact plus admin approval. The old contact is notified.                                                                                                                   |
| **Deals**                               |                                                                                                                                                                                                                              |
| BR-DEAL-01                              | The default deal for every account is profit share at the client's default p (20–40%), overridable per IPO by the admin.                                                                                                     |
| BR-DEAL-02                              | A fixed offer is a quote: price F, audience (all, group or chosen clients) and validity window. Quotes may differ per client and change over time.                                                                           |
| BR-DEAL-03                              | When a client submits a fixed pick, the quote valid at that instant is frozen (price + timestamp) and goes to admin approval. A rejected pick reverts to profit share.                                                       |
| BR-DEAL-04                              | A deal can be changed by the client only until that account is marked APPLIED. After that only the admin can change it, with an audit entry.                                                                                 |
| BR-DEAL-05                              | A deal snapshot is stored per application. Later changes to defaults never alter past applications.                                                                                                                          |
| **Tasks**                               |                                                                                                                                                                                                                              |
| BR-TASK-01                              | One "apply" task per account per APPLY IPO, always 1 lot, with the UPI ID and apply window(s) set by the admin.                                                                                                              |
| BR-TASK-02                              | A task not marked APPLIED by the end of its window becomes MISSED. If the admin defined a later window, it reopens there; after IPO close it is final.                                                                       |
| BR-TASK-03                              | Reminders follow intensity levels (gentle, standard every 2 h, urgent every 1 h), respect quiet hours, are grouped per client, and stop as soon as the task completes.                                                       |
| **Settlement**                          |                                                                                                                                                                                                                              |
| BR-SET-01                               | Profit share, not allotted: 0 either way.                                                                                                                                                                                    |
| BR-SET-02                               | Profit share, allotted, G > 0: client pays I + G − p·G.                                                                                                                                                                      |
| BR-SET-03                               | Profit share, allotted, G ≤ 0: client pays I + G.                                                                                                                                                                            |
| BR-SET-04                               | Fixed, not allotted: operator pays the client F.                                                                                                                                                                             |
| BR-SET-05                               | Fixed, allotted, G > 0: client pays I + G − F − B. B defaults to ₹40 and can be set in Settings and per IPO.                                                                                                                 |
| BR-SET-06                               | Fixed, allotted, G ≤ 0: client pays I + G − F (no B). If the result is negative, the operator pays the absolute value.                                                                                                       |
| BR-SET-07                               | Amounts are integer paise, rounded half-up to the rupee (configurable). One running balance per main member, across all their accounts and IPOs.                                                                             |
| BR-SET-08                               | Due date = listing day + 2 weekdays (skip Saturday/Sunday), at 23:59 IST.                                                                                                                                                    |
| BR-SET-09                               | Before listing, show estimates using the current GMP; entering the listing price makes the amounts final and notifies clients.                                                                                               |
| BR-SET-10                               | Corrections (listing price, allotment) post reversal and replacement ledger entries. Ledger rows are never updated or deleted.                                                                                               |
| BR-SET-11                               | Earnings for reports:<br>• client earned = F (fixed) or p·max(G, 0) (profit share)<br>• operator earned = G − F − B (fixed, profit), G − F (fixed, loss), −F (fixed, not allotted), G − p·max(G, 0) (profit share, allotted) |
| **Payments**                            |                                                                                                                                                                                                                              |
| BR-PAY-01                               | Client marks "Payment done" (optional UTR or screenshot) → admin confirms → SETTLED.                                                                                                                                         |
| BR-PAY-02                               | When the operator owes money, the admin marks it paid with a UTR and the client confirms receipt.                                                                                                                            |
| BR-PAY-03                               | Overdue (after the due time) escalates reminders and raises an admin warning.                                                                                                                                                |
| **Golden scenarios (must always pass)** |                                                                                                                                                                                                                              |
| GS-01                                   | Person A, 5 accounts allotted, I = 15,000, G = 3,000 each; 3 fixed at F = 500, B = 40; 2 profit share at 30% → owes **₹86,580**.                                                                                             |
| GS-02                                   | Same, none allotted → operator owes **₹1,500**.                                                                                                                                                                              |
| GS-03                                   | Fixed at a loss: I = 15,000, G = −3,000, F = 500 → client pays **₹11,500**.                                                                                                                                                  |
| GS-04                                   | Listing on Friday → due **Tuesday** 23:59; Thursday → **Monday**.                                                                                                                                                            |

**Defaults the agents will use** (flagged in specs for your one-time confirmation):

- A registrar rejection is recorded by the admin as NOT_ALLOTTED.
- A fixed pick still pending approval at the time of applying stays pending; its frozen price stands.
- Partial payments reduce the balance.
- An account deleted while it has open tasks is blocked until those settle.

---

## 2. The lifecycle: zero → MVP

```
STAGE 0  Bootstrap            repo, toolchain, agents, CI skeleton, environments        (agents; you create accounts)
STAGE 1  Foundation pack      constitution, domain model, architecture, ADRs, design     (agents → YOU approve once)
STAGE 2  Module loop × 15     spec → research → plan → tasks → TDD build → review → CI   (agents; you skim specs)
STAGE 3  Integration          cross-module scenarios, golden tests, E2E suites, perf/sec (agents)
STAGE 4  Acceptance (UAT)     preview builds + checklist on your phone                   (YOU, ~1–2 h total)
STAGE 5  Release              staging → production, APK + PWA publish, backups verified  (agents → YOU one click)
STAGE 6  Operate              error triage → failing test → fix PR; weekly maintenance   (agents; you merge)
```

### 2.1 Decomposition: blueprint → modules → features → tasks

**Modules (epics) and the order they're built in.** Arrows show dependencies; items on the same line can run in parallel.

```
M0 Platform (repo, CI, auth skeleton, DB, design tokens, app shells)
 ├─► M1 Identity & auth ─► M2 Clients, family accounts & approvals
 ├─► M3 IPO catalogue (+ GMP history)
 │       └─► M4 Deals & live quotes ─► M5 Apply windows & tasks
 │                                          └─► M6 Allotment & listing ─► M7 Settlement engine & ledger ─► M8 Payments
 ├─► M9 Scheduler & notifications (tick, outbox, intensity, push, email)   ← used by M3–M8
 ├─► M10 Reports (client + admin, FY/quarter/custom)                       ← after M7/M8
 ├─► M11 Client app screens   (built alongside each backend module)
 ├─► M12 Admin panel screens  (built alongside each backend module)
 ├─► M13 Data worker (Python): IPO import, subscription refresh, RHP brief, GMP suggestion
 ├─► M14 Engagement: greetings, banners, broadcasts
 └─► M15 Release & ops: Cloud Run/Pages deploy, APK pipeline, PWA push, backups, monitoring
```

**Feature level.** Each module splits into 2–6 Spec Kit features. Example, M7 Settlement:

| Feature | Contents                                |
| ------- | --------------------------------------- |
| F7.1    | Formula engine (pure functions)         |
| F7.2    | Ledger schema + posting rules           |
| F7.3    | Compute on listing + estimates from GMP |
| F7.4    | Corrections and reversals               |
| F7.5    | Due-date calculator                     |
| F7.6    | Settlement API + admin/client views     |

**Task level (v2: slices).** 8–20 slices per feature, compile skeleton first; see `docs/engineering/delivery-pipeline.md` §3. Rules for slices:

- At most about 200 lines changed and at most 5 files per task.
- Exactly one owner agent and one "done" command (e.g. `go test ./internal/settlement/...`).
- Tasks are tagged `[P]` when they can run in parallel and declare dependencies on other task IDs.
- Every task lists the business-rule IDs it implements.
- Order is: tests task → implementation task → wiring task.

### 2.2 The per-feature loop (runs automatically; one feature at a time per module track)

| #   | Step              | Who                                                                                                      | Tool / command                                                               | Output                                                                                                   | Gate                                                                                    |
| --- | ----------------- | -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| 1   | **Research**      | `researcher`                                                                                             | Context7 docs, web search, existing code map                                 | `research.md` (library APIs, versions, pitfalls, prior art in repo)                                      | Sources cited                                                                           |
| 2   | **Specify**       | `spec-writer`                                                                                            | `/speckit-specify`                                                           | `spec.md` with user stories, acceptance criteria (Given/When/Then), BR IDs, edge cases, non-goals        | —                                                                                       |
| 3   | **Clarify**       | `spec-writer`                                                                                            | `/speckit-clarify`                                                           | Open questions. Answered from the blueprint and catalogue where possible; the rest are batched to you    | **You** answer only if blocked                                                          |
| 4   | **Spec approval** | You                                                                                                      | Notification with a 1-page summary                                           | Approved spec                                                                                            | **Human gate A** (≈5 min). Auto-approve option for pure-UI features once trust is built |
| 5   | **Plan**          | `architect`                                                                                              | `/speckit-plan`                                                              | `plan.md`: data model diff, API contract (OpenAPI diff), module boundaries, test strategy, ADR if needed | Architecture rules pass                                                                 |
| 6   | **Analyze**       | `architect`                                                                                              | `/speckit-analyze`                                                           | Consistency report across spec, plan and tasks                                                           | No critical findings                                                                    |
| 7   | **Tasks**         | `planner`                                                                                                | `/speckit-tasks`                                                             | `tasks.md` (small tasks, `[P]` parallel tags, BR IDs, done commands)                                     | Task-size lint passes                                                                   |
| 8   | **Test first**    | `test-engineer`                                                                                          | Superpowers TDD skill                                                        | Failing unit/integration tests per task (committed separately)                                           | Tests fail for the right reason                                                         |
| 9   | **Implement**     | `backend-dev` / `admin-dev` / `mobile-dev` / `data-dev` (worktrees, parallel where `[P]`)                | `/speckit-implement`, Superpowers executing-plans                            | Code + generated clients                                                                                 | Hooks: format, lint, typecheck on every edit. Stop hook: task's done command must pass  |
| 10  | **Verify**        | Same implementer                                                                                         | verification-before-completion skill                                         | Full affected suite + golden scenarios run                                                               | All green with evidence pasted                                                          |
| 11  | **Review**        | `reviewer` (fresh context) + `security-reviewer` + `/code-review` plugin                                 | Diff vs spec/tasks, architecture, duplication, naming, performance, security | Findings → fix tasks (max 2 rounds, then escalate)                                                       | No high-severity findings                                                               |
| 12  | **PR + CI**       | `release`                                                                                                | `gh pr create`, GitHub Actions                                               | PR with spec link, BR coverage table, test evidence, screenshots                                         | All required checks green                                                               |
| 13  | **QA agents**     | `qa-web` (Playwright MCP), `qa-mobile` (Maestro MCP)                                                     | Run E2E flows on preview builds; explore new screens; screenshots            | QA report on the PR                                                                                      | E2E green, no visual regressions                                                        |
| 14  | **Merge**         | Auto-merge when 12–13 are green **and** the change is not in a protected area (ledger, auth, migrations) | GitHub auto-merge                                                            | main updated → staging auto-deploy                                                                       | Protected areas need **your** approve click                                             |

Per module, after its last feature: an `integration` pass runs cross-module scenarios, and the module is marked "ready for UAT".

### 2.3 Human time budget

| Touchpoint                                                                           | When               | Time         |
| ------------------------------------------------------------------------------------ | ------------------ | ------------ |
| Create accounts + paste secrets (Section 8)                                          | Once, Stage 0      | ~1–2 h       |
| Approve foundation pack (constitution, domain model, architecture, design direction) | Once, Stage 1      | ~45 min      |
| Spec approvals (gate A)                                                              | ~40 features       | ~5 min each  |
| Answer batched clarifications                                                        | As needed          | Minutes      |
| Approve PRs touching ledger / auth / migrations                                      | ~10–15 PRs         | ~5 min each  |
| UAT with checklist on your phone + admin                                             | Per module + final | ~1–2 h total |
| Production release click                                                             | Each release       | 1 min        |

---

## 3. Agent roster (`.claude/agents/*.md`)

The main Claude Code session is the **conductor**. It runs `/ship-feature` and `/ship-module` (custom skills), dispatches subagents, tracks `tasks.md`, and escalates to you.

| Agent               | Model / effort      | Tools allowed                                                                                    | MCP                                        | Isolation     | Writes                                |
| ------------------- | ------------------- | ------------------------------------------------------------------------------------------------ | ------------------------------------------ | ------------- | ------------------------------------- |
| `researcher`        | Sonnet 5.5 / medium | Read, Grep, WebSearch, WebFetch                                                                  | Context7                                   | —             | `specs/<f>/research.md`               |
| `spec-writer`       | Opus 5.5 / high     | Read, Write (`specs/` only)                                                                      | —                                          | —             | `spec.md`, clarifications             |
| `architect`         | Opus 5.5 / high     | Read, Write (`specs/`, `docs/adr/`)                                                              | Context7, Supabase (read-only, dev)        | —             | `plan.md`, ADRs, OpenAPI diff         |
| `planner`           | Sonnet 5.5 / medium | Read, Write (`specs/`)                                                                           | —                                          | —             | `tasks.md`                            |
| `test-engineer`     | Sonnet 5.5 / high   | Edit `**/*_test.go`, `**/*.test.ts(x)`, `**/tests/**`, `.maestro/**`, `e2e/**`                   | Playwright, Maestro                        | worktree      | Tests only                            |
| `backend-dev`       | Sonnet 5.5 / high   | Edit `apps/api/**`, Bash (`go`, `sqlc`, `goose`, `make`) — **not** test files authored in step 8 | Context7, Postgres (local)                 | worktree      | Go code, SQL, migrations              |
| `data-dev`          | Sonnet 5.5 / medium | Edit `apps/worker/**`, Bash (`uv`, `pytest`)                                                     | Context7                                   | worktree      | Python worker                         |
| `admin-dev`         | Sonnet 5.5 / high   | Edit `apps/admin/**`, `packages/**`, Bash (`pnpm`)                                               | Context7, Playwright                       | worktree      | React admin                           |
| `mobile-dev`        | Sonnet 5.5 / high   | Edit `apps/client/**`, `packages/**`, Bash (`pnpm`, `npx expo`)                                  | Context7, Expo, Maestro                    | worktree      | Expo app                              |
| `designer`          | Opus 5.5 / medium   | Edit `packages/tokens/**`, `docs/design/**`                                                      | — (frontend-design + Design plugin skills) | —             | Tokens, screen specs, UX copy         |
| `reviewer`          | Opus 5.5 / high     | **Read-only** + Bash (tests, linters)                                                            | —                                          | fresh context | Review report                         |
| `security-reviewer` | Opus 5.5 / high     | Read-only + `/security-review`                                                                   | —                                          | fresh context | Security report                       |
| `qa-web`            | Sonnet 5.5 / medium | Bash (Playwright)                                                                                | Playwright                                 | —             | QA report + screenshots               |
| `qa-mobile`         | Sonnet 5.5 / medium | Bash (Maestro, adb)                                                                              | Maestro, Expo                              | —             | QA report + screenshots               |
| `release`           | Sonnet 5.5 / low    | Bash (`gh`, `git`, `eas`, `gcloud` deploy only via CI)                                           | GitHub                                     | —             | PRs, changelog, release notes         |
| `triage`            | Haiku 5.5 / low     | Read, Bash (`gh issue`)                                                                          | Sentry, GitHub                             | —             | Issues with repro hints               |
| `ops`               | Haiku 5.5 / low     | Read, Bash (`gh`)                                                                                | GitHub                                     | —             | Weekly digest issue, Renovate fix PRs |

Rules for all agents:

- Project agents only, because hooks and `mcpServers` in plugin-shipped agents are ignored.
- Every agent prompt starts with: read `CLAUDE.md`, the relevant `.claude/rules/*`, the feature's `spec.md`/`tasks.md`, and the golden reference module.
- No agent has production credentials. Deploys happen only in CI through OIDC.

Fable 5.2 can replace Opus for `architect` and `reviewer` on the riskiest modules (M7 settlement, M1 auth) if your plan includes it.

---

## 4. Repository layout for automation

```
swastik/
├─ CLAUDE.md                     ≤150 lines: what Swastik is, commands per app, where rules live, escalation policy
├─ .claude/
│  ├─ settings.json              permissions (allow/deny), hooks, env, model defaults
│  ├─ agents/                    the 16 agents above
│  ├─ skills/
│  │  ├─ ship-feature/           conductor: runs steps 1–14 for one feature, resumable via specs/<f>/state.json
│  │  ├─ ship-module/            loops ship-feature over a module's features + integration pass
│  │  ├─ business-rules/         the BR catalogue + golden scenarios (loaded by spec/test/review agents)
│  │  ├─ go-module-pattern/      how a module is built (handler → service → sqlc repo → events), points at internal/ipos
│  │  ├─ rn-screen-pattern/      screen + query hook + states (loading/empty/error) + Maestro flow template
│  │  ├─ admin-page-pattern/     TanStack route + table + bulk actions + Playwright test template
│  │  ├─ money-safety/           paise, rounding, ledger posting checklist
│  │  ├─ uat-checklist/          generates the human test script for a PR/module
│  │  └─ release/                versioning, changelog, deploy, APK publish, rollback
│  ├─ rules/                     path-scoped: api.md (apps/api/**), client.md, admin.md, worker.md, sql.md, tests.md
│  └─ hooks/                     scripts used by settings.json
├─ .mcp.json                     project MCP servers (scoped per agent in frontmatter)
├─ .specify/                     Spec Kit 1.4.x (pinned) + constitution.md + extensions (bug, assess)
├─ specs/<NNN-feature>/          research.md, spec.md, plan.md, tasks.md, state.json, uat.md
├─ docs/adr/ docs/design/ docs/runbooks/
├─ apps/ api · worker · client · admin
├─ packages/ api-client · tokens · ui-native · domain · config
├─ infra/ tofu/ (GCP, Cloudflare) · docker/ · scripts/
└─ .github/ workflows/ · CODEOWNERS · pull_request_template.md · renovate.json
```

### 4.1 Hooks (`.claude/settings.json`)

| Event                      | Hook               | Effect                                                                                                                                                                                                   |
| -------------------------- | ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PostToolUse` (Edit/Write) | `fmt-and-check.sh` | gofmt/goimports, Biome, Ruff format on the file; fast typecheck of the package (`go vet` / `tsc --noEmit -p` / `pyright`); exit 2 on failure so the agent fixes immediately                              |
| `PreToolUse` (Edit/Write)  | `guard-paths.sh`   | Blocks:<br>• implementers editing tests authored by `test-engineer`<br>• editing applied migrations<br>• touching `.env*`, `infra/tofu/*.tfstate`, `.github/workflows/deploy*` without the release agent |
| `PreToolUse` (Bash)        | `guard-bash.sh`    | Blocks `git push --force`, `rm -rf` outside the worktree, production `gcloud`/`supabase` commands, `curl` to non-allowlisted hosts                                                                       |
| `Stop` / `SubagentStop`    | `done-check.sh`    | Runs the task's done command (from `state.json`); if red, returns exit 2 so the agent keeps working (max 3 loops, then escalates)                                                                        |
| `SessionStart`             | `context.sh`       | Prints current feature, task, branch and last CI status                                                                                                                                                  |
| `UserPromptSubmit`         | `inject-rules.sh`  | Adds the business-rules summary when prompts mention money, deal, settlement or ledger                                                                                                                   |
| `Notification`             | `notify.sh`        | Pushes "needs your input" to your phone (Claude app / ntfy)                                                                                                                                              |

### 4.2 Permissions

- **Allow:** `go *`, `pnpm *`, `uv *`, `sqlc *`, `goose * (local DB)`, `maestro *`, `npx playwright *`, `gh pr *`, `gh issue *`, `git` (non-force).
- **Deny:** `Read(.env*)`, `Bash(gcloud * --project=swastik-prod*)`, `Bash(supabase db push --linked*)` outside CI, any MCP write tool against production.

---

## 5. Nested automated testing

Testing happens in layers, from fastest to slowest. Each layer runs at a defined moment.

| Layer             | What                                                                                                                        | Tools                                                                                                                                                                                                                                                                                  | Runs                               |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------- |
| L0 Static         | Format, lint, types, architecture boundaries, SQL vet, migration lint, secrets                                              | golangci-lint v2 (+ depguard boundaries), `sqlc vet`, squawk, Biome, `tsc`, Ruff + pyright, dependency-cruiser, gitleaks                                                                                                                                                               | Every edit (hook) + every PR       |
| L1 Unit           | Pure logic: formulas, due dates, quote validity, reminder schedules, formatters                                             | Go table tests; **property tests (rapid)**: ledger always sums to zero, settlement symmetric under reversal, results never change when unrelated defaults change; Vitest; pytest                                                                                                       | Every task (Stop hook) + PR        |
| L1.5 Mutation     | Proves the settlement tests actually catch bugs                                                                             | Gremlins (Go mutation testing) on `internal/settlement`, `internal/ledger`; target ≥ 85% killed                                                                                                                                                                                        | Nightly + PRs touching those paths |
| L2 Integration    | Real Postgres: repositories, transactions, outbox, scheduler with a **fake clock** ("time travel" through an IPO lifecycle) | testcontainers-go (Postgres 17/18), golden-file JSON                                                                                                                                                                                                                                   | PR                                 |
| L3 Contract       | API matches OpenAPI; no breaking changes; generated TS client compiles                                                      | oasdiff (breaking-change check), Spectral lint, schemathesis (fuzz the API from the spec)                                                                                                                                                                                              | PR                                 |
| L4 Component      | Screens render each state (loading/empty/error/data)                                                                        | React Native Testing Library, Vitest + Testing Library (admin), Storybook interaction tests                                                                                                                                                                                            | PR (affected apps)                 |
| L5 E2E            | Full user journeys                                                                                                          | **Playwright**: admin creates IPO → publishes deal → tasks board → allotment → listing → confirms payment. **Maestro** on Android emulator: client signs up → OTP → approved → picks deal → marks applied → sees allotment → pays. **Playwright (mobile viewport)** for the iPhone PWA | PR (smoke) + nightly (full)        |
| L6 Scenario       | **Golden scenarios GS-01…GS-04** + multi-client end-to-end business simulation across API + scheduler + notifications       | Go scenario tests against a running API with seeded data                                                                                                                                                                                                                               | Every PR + nightly                 |
| L7 Non-functional | Load (100 users × reminder burst), security, accessibility, bundle/APK size budget                                          | k6, gosec, Semgrep, OWASP ZAP baseline, axe (admin), APK size check (< 25 MB per device)                                                                                                                                                                                               | Nightly / pre-release              |

Coverage policy:

- `internal/settlement`, `internal/ledger`, `internal/deals`: **100% branch coverage** plus mutation score ≥ 85%.
- Other Go modules ≥ 80%; frontend ≥ 70% lines on logic files.
- **BR traceability check:** a script fails CI if any BR ID has no test whose name contains it.

---

## 6. CI/CD workflows (`.github/workflows/`)

| Workflow          | Trigger                                                                                                                             | Steps                                                                                                                                                                                                                                                                                        |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `pr.yml`          | PR                                                                                                                                  | Path-filtered (Turborepo affected + Go/Python change detection) → L0–L4, L6 → OpenAPI diff comment → BR traceability → APK size → status checks                                                                                                                                              |
| `e2e.yml`         | PR (smoke), nightly (full)                                                                                                          | Spin up API + Postgres in services, build admin, Android emulator (`reactivecircus/android-emulator-runner`) → Maestro + Playwright → upload screenshots/videos                                                                                                                              |
| `preview.yml`     | PR labelled `preview` or touching apps                                                                                              | Admin → Cloudflare Pages preview URL; API → Cloud Run tagged revision on **staging**; Android **preview APK** (Gradle on Actions or `eas build --local`) → PR comment with QR codes + generated `uat.md` checklist                                                                           |
| `claude.yml`      | `@claude` mentions, PR opened                                                                                                       | `anthropics/claude-code-action@v1`: advisory review with the code-review plugin; implements small requested changes on the PR branch                                                                                                                                                         |
| `agent-build.yml` | Issue labelled `agent:build` or `agent:fix`                                                                                         | Headless Claude Code (`claude -p`, `--max-turns`, `--max-budget-usd`) runs `/ship-feature` or the bug extension on a branch → opens PR                                                                                                                                                       |
| `deploy.yml`      | Merge into `stage` → **staging**; merge into `main` (release PR) → **production** (GitHub Environment with required reviewer = you) | Build distroless Go + Python images → Artifact Registry → Cloud Run deploy via **Workload Identity Federation** (no keys) → run migrations (goose) → smoke tests → admin/PWA to Cloudflare Pages → EAS Update for JS-only client changes / new APK to R2 for native changes → Sentry release |
| `nightly.yml`     | 02:00 IST                                                                                                                           | Full E2E, mutation tests, ZAP, k6, **backup + restore drill** (pg_dump prod → R2 → restore into a temp DB → row counts match)                                                                                                                                                                |
| `triage.yml`      | Daily 09:00 IST                                                                                                                     | `triage` agent: new Sentry issues → GitHub issues with severity and repro hints                                                                                                                                                                                                              |
| `ops.yml`         | Weekly                                                                                                                              | Dependency PR fixes (Renovate), tech-debt + metrics digest, release-notes draft                                                                                                                                                                                                              |

**Environments:**

| Environment  | Database                 | Compute and web                        | Data                               |
| ------------ | ------------------------ | -------------------------------------- | ---------------------------------- |
| `local`      | Docker Compose Postgres  | —                                      | Seed script with demo IPOs/clients |
| `ci`         | Ephemeral containers     | —                                      | —                                  |
| `staging`    | Supabase free project #2 | Cloud Run `api-staging`, Pages preview | Seeded demo data                   |
| `production` | Supabase free project #1 | Cloud Run `api`, Pages prod            | Real data                          |

Supabase's free plan allows exactly two projects, which covers staging and production.

**Branching:** `main` = production, `stage` = default working branch and staging. Work branches come from `stage` with human-readable names (`feat/<NNN>-<desc>`, `fix/<issue>-<desc>`, `chore/<desc>`, `refactor/<desc>`, `docs/<desc>`, `test/<desc>`; `hotfix/<issue>-<desc>` from `main`) and merge into `stage` by PR; releases are `stage` → `main` PRs, tagged `vX.Y.Z`. Parallel work uses one worktree per branch (`../swastik-<id>`). Full guide: `docs/engineering/git-workflow.md`.

---

## 7. Plugins, connectors and MCP servers

Available in your Claude plugin directory (checked today):

| Plugin / MCP                  | Publisher      | Used by                                | Purpose                                                                                                                         | Scope / risk control                                                                                                                         |
| ----------------------------- | -------------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| **Superpowers** (v7)          | obra (partner) | conductor, test-engineer, implementers | Brainstorming, writing plans, TDD, worktrees, subagent-driven development, verification-before-completion, systematic debugging | Use its execution skills inside Spec Kit's implement phase; disable its brainstorming when a Spec Kit spec exists to avoid duplicate process |
| **code-review**               | Anthropic      | reviewer, CI                           | Multi-agent confidence-scored PR review                                                                                         | Advisory only                                                                                                                                |
| **GitHub** (MCP)              | GitHub         | release, triage, ops                   | Issues, PRs, reviews                                                                                                            | Fine-grained token: this repo only; `gh` CLI preferred for scripts                                                                           |
| **Expo** (skills + MCP)       | Expo           | mobile-dev, qa-mobile                  | Expo Router, UI, EAS builds/updates/workflows, upgrade guides                                                                   | MCP needs a paid EAS plan; skills alone are free and enough at first                                                                         |
| **Context7**                  | Upstash        | researcher, all devs                   | Version-correct docs (Go libs, Expo SDK 58, TanStack, Huma, sqlc)                                                               | Read-only                                                                                                                                    |
| **Playwright** (MCP)          | Microsoft      | qa-web, admin-dev                      | Drive the admin panel/PWA in a browser, screenshots                                                                             | Local/preview URLs only                                                                                                                      |
| **Sentry** (skills + MCP)     | Sentry         | triage, devs                           | Instrumentation, debugging issues, release setup                                                                                | Read scopes for agents                                                                                                                       |
| **Supabase** (skills + MCP)   | Supabase       | architect (read), release (CI only)    | Postgres best practices, project management                                                                                     | Read-only on staging; no MCP access to production                                                                                            |
| **Cloudflare** (skills + MCP) | Cloudflare     | release, ops                           | Pages, Access (Zero Trust), R2, DNS                                                                                             | CI-only for writes                                                                                                                           |
| **frontend-design**           | Anthropic      | designer, admin-dev, mobile-dev        | Production-grade, distinctive UI                                                                                                | —                                                                                                                                            |
| **Design** (skills)           | Anthropic      | designer                               | UX copy, accessibility review, design-system audit, handoff                                                                     | Its bundled SaaS connectors aren't needed                                                                                                    |

Not in the directory but used as CLIs or project MCPs:

- **Maestro MCP** (`maestro mcp`, built into the Maestro CLI; Java 17+)
- **Postgres MCP** against the local DB only
- **gcloud**, **OpenTofu**, **eas-cli**, **wrangler**, **supabase CLI** (CI only for remote)

Deliberately **not** connected: broker or payment MCPs (Kite, Razorpay), WhatsApp, and anything that can move money or message clients. They stay out of agent reach.

**Connector note for this cloud workspace:** plugin MCP servers can't connect from this cloud session (the proxy refuses their tunnels). They will work in Claude Code on your laptop, or in a cloud environment whose network settings allow those hosts. Skills-only plugins (Superpowers, frontend-design, code-review, Design) work everywhere.

---

## 8. Stage 0 bootstrap: what agents do vs what you do

**You do once.** Agents cannot create accounts or enter card details.

1. Create an empty **private GitHub repo** `swastik`; give this session access (add_repo) and enable Actions.
2. Create accounts or projects:
   - **Google Cloud** project (billing card, ₹1 budget alert)
   - **Supabase** (2 free projects, Mumbai region if offered)
   - **Cloudflare** (domain + Pages + Access + R2)
   - **Firebase** project (phone OTP + FCM)
   - **Expo** account
   - **Sentry**
   - **Brevo or Resend** (email)
3. Buy the domain (for example a `swastik` .in or .app name; plain `swastik.in` is likely taken) on Cloudflare Registrar.
4. Paste secrets into **GitHub Environments** (`staging`, `production`). Agents give you an exact checklist; values never go into chat.
5. Install the plugins above in Claude Code (one-click cards below).

**Agents then do, in order** (Stage 0 runbook, about 1 day of agent time):

1. Initialise the monorepo:
   - pnpm workspace + Turborepo
   - Go module (`apps/api`) with the Huma skeleton, health endpoint, slog, OTel
   - Python worker (`uv init`, FastAPI health)
   - Expo SDK 58 app (`apps/client`) with Expo Router + Uniwind + web output
   - Vite admin with TanStack Router + shadcn
   - Shared packages
2. Toolchain: TypeScript 7, Biome, golangci-lint, Ruff/pyright, sqlc, goose, Docker Compose for local Postgres, `make dev` / `make test`.
3. `.claude/`: agents, skills, rules, hooks, settings, `.mcp.json`; `CLAUDE.md`.
4. `specify init` (Spec Kit 1.4.x pinned) + constitution drafted from both blueprints + BR catalogue skill.
5. CI workflows (pr, e2e, preview, deploy, nightly, triage, ops) + CODEOWNERS + PR template + Renovate.
6. OpenTofu for GCP (Cloud Run services, Artifact Registry, Scheduler tick, Workload Identity Federation) and Cloudflare (Pages, Access app for admin, R2 buckets).
7. First deploy of the "hello" stack to staging; tick job firing every 5 min; backup job running.
8. Report: what's live, which secrets are missing, and the Stage 1 foundation pack for your approval.

---

## 9. Stage 1 foundation pack (you approve once)

| Artifact          | Contents                                                                                                                                                                                                                                     |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `constitution.md` | Principles: money in paise, ledger append-only, test-first, BR traceability, module boundaries, accessibility, performance budgets (API p95 < 200 ms in Mumbai; app cold start < 2 s on a mid-range Android; APK < 25 MB), security baseline |
| Domain model      | ER diagram + state machines (IPO, task, deal pick, application, settlement, payment, approval)                                                                                                                                               |
| Architecture      | Module map, API style guide (REST, OpenAPI 3.1, RFC 9457 errors, idempotency keys, cursor pagination, ETags), event and outbox design, scheduler design                                                                                      |
| ADRs              | 001 Go modular monolith · 002 Postgres ledger in paise · 003 tick-based scheduler on Cloud Run · 004 self-built auth + Firebase SMS · 005 Expo one codebase (Android + PWA) · 006 Spec Kit + agent pipeline                                  |
| Design direction  | Tokens (colour, type, spacing, radius, status colours), 6 key screen mock specs (client Home, Tasks, Deal pick, Payments; admin Home, Tasks board), UX copy for deals explainer, empty/error states                                          |

---

## 10. MVP schedule (agent-driven, with your light touch)

| Week | Modules in flight (parallel tracks)                          | Milestone                                           |
| ---- | ------------------------------------------------------------ | --------------------------------------------------- |
| 1    | Stage 0 + Stage 1                                            | Staging live, foundation pack approved              |
| 2    | M1 Auth · M3 IPO catalogue · M9a tick + outbox               | Admin can log in and manage IPOs                    |
| 3    | M2 Clients & approvals · M4 Deals & quotes · M12 admin pages | Clients register (OTP) → you approve; deals publish |
| 4    | M5 Tasks · M9b reminders + push · M11 client core screens    | Clients get tasks + 2-hourly pushes; mark applied   |
| 5    | M6 Allotment & listing · **M7 Settlement & ledger**          | Golden scenarios pass end-to-end                    |
| 6    | M8 Payments · M10 Reports · M14 Greetings                    | Full money cycle + FY reports                       |
| 7    | M13 Data worker · M15 hardening · Stage 3 integration        | Data import, AI briefs, nightly suites green        |
| 8    | Stage 4 UAT · Stage 5 release                                | **MVP live**: APK to clients, PWA for iPhones       |

---

## 11. Operating after launch

- **Bug report path.** Two inputs feed the bug loop:
  - Client "Report a problem" button → GitHub issue (device, app version, screen, logs).
  - Sentry error → `triage` agent → issue.
- **Bug loop.** You add the `agent:fix` label → bug extension: reproduce → failing test → fix → PR → CI → your merge.
- **New ideas.** Write one line in a GitHub issue with the `idea` label → `spec-writer` runs the Spec Kit `assess` extension → you approve → it enters the feature loop.
- **Weekly `ops` digest:** dependency updates, error trends, slowest endpoints, test flakiness, cost check (GCP/Supabase usage vs free limits).

---

## 12. Failure modes and safeguards

| Risk                                     | Safeguard                                                                                                                                    |
| ---------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Agent "passes" by weakening tests        | Tests authored in a separate step; implementers can't edit them (hook); reviewer diffs test files; mutation testing on money code            |
| Spec drift (code no longer matches spec) | BR traceability check in CI; reviewer checks against the spec; spec updated in the same PR when behaviour changes                            |
| Money bugs                               | Pure formula engine + property tests + golden scenarios + 100% branch coverage + mutation tests + your approval on ledger PRs                |
| Flaky E2E                                | Retries only in nightly runs; quarantine label; ops digest lists flaky tests for fixing                                                      |
| Cost overrun (Claude usage)              | `--max-budget-usd` on headless runs; Sonnet for implementation, Opus only for spec/plan/review, Haiku for chores; weekly usage in the digest |
| Prompt injection via web/MCP content     | Read-only MCPs for agents; no production credentials anywhere except CI OIDC; deny rules on dangerous commands                               |
| Context bloat                            | Short CLAUDE.md, path-scoped rules, skills loaded on demand, MCP tool search, one feature per session with `state.json` handoff              |
| Free-tier surprises                      | Budget alerts at ₹1; ops digest tracks Supabase DB size, Cloud Run usage, email/SMS counts                                                   |
