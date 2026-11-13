# Divya Constitution

Divya is a private, internal IPO operations platform: an admin web panel, a client app (Android app + iPhone PWA from one Expo codebase), a Go API and a Python data worker. These principles bind every spec, plan, task and line of code. When a spec or plan conflicts with them, the constitution wins; change it only through an ADR approved by the owner.

## Core Principles

### I. Business rules are the source of truth (NON-NEGOTIABLE)
Every behaviour traces to a rule in `docs/product/business-rules.md` (IDs like `BR-SET-05`) or to an approved spec. Specs cite rule IDs; every rule ID appears in the name of at least one automated test (CI enforces this). Golden scenarios GS-01…GS-04 must pass on every commit. Agents never invent business behaviour: an ambiguity is a clarification for the owner, not a guess.

### II. Money is exact and history is immutable (NON-NEGOTIABLE)
Amounts are integer paise (`int64` in Go, `Paise` branded numbers in TS) — never floats. Rounding happens in one place (the settlement engine) by an explicit rule. The ledger is double-entry and append-only: every transaction sums to zero, rows are never updated or deleted, and corrections are reversal + replacement entries. Every application stores a snapshot of the deal it was made under; changing defaults never rewrites the past. All mutations write to `audit_log`.

### III. Test-first, layered, and deterministic
Tests are written before implementation, by a different agent, and must fail for the right reason first. Layers: static → unit (incl. property-based for money) → integration on real PostgreSQL (testcontainers) → contract (OpenAPI) → component → E2E (Playwright admin/PWA, Maestro Android) → golden scenarios. Settlement, ledger and deals code: 100% branch coverage and ≥85% mutation score. Time is always injected (a clock), never read directly in business logic, so schedules and due dates are testable.

### IV. Modular monolith with hard boundaries
The Go API is one binary of modules under `apps/api/internal/<module>`, each shaped like the golden reference module `internal/health` (service behind small interfaces, Huma handlers, sqlc queries, tests). Modules communicate through exported service interfaces or domain events in the outbox — never by reading each other's tables. `internal/platform` never imports business modules. Boundaries are enforced by golangci-lint `depguard`. The Python worker only fetches and enriches external data; it never touches money.

### V. Contract-first APIs
The OpenAPI 3.1 document generated from the Go code is the contract. Clients use the generated TypeScript client (`@divya/api-client`) — no hand-written fetch calls. Errors are RFC 9457 problem details. Mutations accept idempotency keys. Breaking changes require a new version path and fail CI (oasdiff) unless explicitly approved.

### VI. Security and privacy by default
PAN and other identifiers are encrypted at rest (AES-GCM, key outside the database) with a blind index for lookup, and masked in every UI (`ABCDE••••F`). Passwords use Argon2id; sessions use short-lived access tokens and rotating, device-bound refresh tokens. The admin panel sits behind Cloudflare Access plus TOTP. No secret ever enters the repository, logs, prompts or chat. Agents hold no production credentials; production changes happen only through CI with OIDC. Logs are retained ≥180 days. Users can export and delete their data.

### VII. Simple, fast, accessible
Prefer the boring, standard solution; every new dependency needs a reason in the plan. Performance budgets: API p95 < 200 ms in Mumbai; client cold start < 2 s on a mid-range Android; Android download < 25 MB; admin initial JS < 300 KB gzip. Every screen handles loading, empty, error and offline states. Indian formatting (₹1,48,500; IST; Apr–Mar financial year) everywhere. WCAG AA contrast, touch targets ≥ 44 pt, dynamic type.

## Technology Constraints

- Go 1.27 (Huma v2, pgx v5, sqlc, goose), PostgreSQL 18, Python 3.13 (FastAPI, uv, Ruff, pyright strict).
- Expo SDK 58 / React Native 0.88 with Expo Router (client); Vite + React 19 + TanStack Router/Query (admin); Tailwind v4 tokens from `@divya/tokens`.
- pnpm + Turborepo; TypeScript 7 for type-checking (TypeScript 6 only where a tool needs the JS compiler API); Biome 2; golangci-lint v2.
- Hosting: Cloud Run (Mumbai) + Supabase Postgres + Cloudflare (Pages, Access, R2). Scheduled work runs through the single `/internal/tick` endpoint and the `scheduled_jobs` / outbox tables.
- Out of scope until an ADR says otherwise: automated IPO applying, WhatsApp, SME IPOs, any broker or payment API that can move money.

## Development Workflow & Quality Gates

1. Feature work follows Spec Kit: specify → clarify → **owner approves spec** → plan → analyze → tasks → test-first implement → independent review → PR. Bugs use the `bug` extension (reproduce → failing test → fix). Ideas use the `assess` extension before any spec.
2. Tasks are small: ≤ ~200 changed lines, ≤ 5 files, one owner agent, one "done" command, rule IDs listed.
3. A change is done only when its done command, the affected suites and the golden scenarios pass, with evidence (command + output) in the PR.
4. Review is by a fresh-context reviewer against the spec plus a security reviewer; AI review is advisory, CI is the gate.
5. PRs touching `internal/settlement`, `internal/ledger`, `internal/deals`, `internal/auth`, or migrations require the owner's approval; other green PRs may auto-merge.
6. Git flow (`docs/engineering/git-workflow.md`): `main` = production, `stage` = default working branch and staging. All work happens on short-lived, human-readable branches cut from `stage` (`feat/<NNN>-<desc>`, `fix/<issue>-<desc>`, `chore/<desc>`, `refactor/<desc>`, `docs/<desc>`, `test/<desc>`; `hotfix/<issue>-<desc>` from `main`) and merges into `stage` by PR. Releases are PRs from `stage` to `main`, tagged `vX.Y.Z`, deployed after owner approval. Conventional Commits; parallel work uses one git worktree per branch.
7. Agents stop and ask the owner on: money-rule ambiguity, ledger or auth schema changes, new runtime dependencies, security-sensitive changes, or three failed attempts at the same fix.

## Governance

This constitution supersedes conflicting guidance in any other document. Amendments require an ADR in `docs/adr/`, owner approval, and a version bump below. Reviewers verify compliance on every PR; violations are blocking findings.

**Version**: 1.1.0 | **Ratified**: 2026-11-13 | **Last Amended**: 2026-11-13 (git flow: stage/main)
