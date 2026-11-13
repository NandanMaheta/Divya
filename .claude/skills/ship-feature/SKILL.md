---

name: ship-feature
description: Conductor for one Divya feature from docs/product/modules.md — research, spec, owner approval, plan, tasks, test-first implementation by parallel agents, review, QA and PR. Resumable from specs/<feature>/state.json. Use when asked to build, continue or ship a feature by number (e.g. "/ship-feature 016").
---

# Ship one feature

You are the conductor. You coordinate agents; you do not write production code yourself. Keep the owner's involvement to the gates marked **OWNER**.

## 0. Load state

1. Find the feature row `<NNN>` in `docs/product/modules.md`. Refuse if a dependency is not `done`.
2. If `specs/<NNN>-*/state.json` exists, resume from its `phase`. Otherwise create the feature branch `feat/<NNN>-<slug>` from `main`.

## 1. Research → `researcher`

Produce `research.md`. Skip only for trivial UI-only features.

## 2. Specify → `spec-writer`

`/speckit-specify` + `/speckit-clarify`. If the spec has "Questions for the owner", send them (Notification) and stop with phase `awaiting-owner`.

## 3. **OWNER gate A — spec approval**

Show the 10-line summary and the path to `spec.md`. Wait for "approved" (or edits). Record approval time in `state.json`.

## 4. Plan → `architect`, then tasks → `planner`, then analyze → `architect`

`/speckit-plan` → `/speckit-tasks` → `/speckit-analyze`. Fix critical findings before continuing. If the plan adds an ADR, a runtime dependency, or changes ledger/auth schema, stop for owner approval.

## 5. Build loop (repeat per task, respecting dependencies; run `[P]` tasks in parallel)

For each task in order:

1. `test-engineer` writes failing tests (protected) → evidence of failure.
2. Owner agent (`backend-dev` / `data-dev` / `admin-dev` / `mobile-dev` / `designer`) implements until the done command passes (Stop hook enforces it).
3. Update `state.json` task status. After 3 failed attempts on one task: stop and report to the owner.
   Run parallel tasks as background subagents in worktrees; merge their branches back into the feature branch in dependency order and re-run affected tests after each merge.

## 6. Verify

On the feature branch run: Go `go test -race ./...` + `golangci-lint run`, worker checks, `pnpm turbo run typecheck test build`, `pnpm lint`, `scripts/check-br-traceability.sh`. All must pass.

## 7. Review → `reviewer` + `security-reviewer` (in parallel, fresh context)

If CHANGES: turn findings into fix tasks and loop through step 5 (max 2 rounds, then escalate to the owner).

## 8. PR → `release`

Open the PR with evidence. CI runs. When green and a preview exists, run `qa-web` and/or `qa-mobile`. Fix any bugs found via step 5.

## 9. **OWNER gate B — UAT / approval**

Post the UAT checklist (`uat-checklist` skill) on the PR. Money/auth/migration PRs need the owner's approval; others auto-merge when green.

## 10. Close out

Mark the feature `done` in `docs/product/modules.md`, set `state.json` phase `done`, and summarise for the owner in ≤8 lines: what shipped, evidence, anything deferred.
