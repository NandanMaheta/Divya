---

name: ship-feature
description: Conductor for one Swastik feature from docs/product/modules.md using delivery pipeline v2 (risk tiers, one-page brief, slices with a compile skeleton, batched test and code passes, review, PR). Resumable from specs/<feature>/state.json. Use when asked to build, continue or ship a feature by number (e.g. "/ship-feature 016").
---

# Ship one feature (pipeline v2)

Canonical rules: `docs/engineering/delivery-pipeline.md`. You coordinate; you do not write production code. Keep prompts short: point agents at files instead of restating them. Keep your own messages short.

## 0. Load state

1. Read the feature row (incl. **Tier**) in `docs/product/modules.md`. Refuse if a dependency is not `done` (unless only its _build_ depends on it — specs and plans may run ahead).
2. Resume from `specs/<NNN>-*/state.json` if it exists. Otherwise create the worktree from `stage`: `git fetch origin && git worktree add ../<repo>-<NNN> -b feat/<NNN>-<short-desc> origin/stage`, and set `.specify/feature.json` to `specs/<NNN>-<short-desc>`.

## 1. Specify

- **Tier A:** `researcher` (only if new external tech) → `spec-writer` (Opus) writes `spec.md` (≤150 lines) → **OWNER approves** → `architect` (Opus) writes `plan.md` (≤200 lines), contracts and `brief.md`.
- **Tier B:** `architect` (Opus) writes `spec.md` + `plan.md` + `brief.md` in **one run** → **OWNER approves once**.
- Owner questions are batched; answers are recorded in the spec's "Owner approval" section.
- New runtime dependencies or ADRs always need owner approval.

## 2. Slice

`planner` writes `tasks.md` and `state.json` with **8–20 slices**: slice 1 = compile skeleton (exported types and signatures returning `errNotImplemented`), then test-first pairs ordered by dependency, last = gate (`go mod tidy` clean, full suite, lint, BR traceability, modules.md status). Then `architect` runs `/speckit-analyze` only for Tier A.

## 3. Build (batched passes)

1. **Test pass:** one `test-engineer` run writes tests for all ready slices (Sonnet; effort high for Tier A, medium for Tier B) → each RED for the right reason → listed as `protected_tests`.
2. **Code pass:** one developer run (`backend-dev`/`admin-dev`/`mobile-dev`/`data-dev`, same model rules) implements the skeleton and all ready slices → each slice's done command green → full affected suite once.
3. Large features may use two test/code passes. Parallel slices in different packages may use separate worktrees and run concurrently.
4. After 3 failed attempts on one slice: stop and report to the owner.
5. **Pipelining:** while the code pass runs, the next feature's test pass may start in its own worktree.

## 4. Review

`reviewer` (fresh context, reads brief + diff; checks test-before-code order in `git log`). Tier A, or any feature touching endpoints/auth/PII: also `security-reviewer`. Findings become fix slices (max 2 rounds, then escalate).

## 5. PR and close

1. `release` (Haiku) opens the PR into `stage` with evidence. Tier A PRs need the owner's approval; Tier B may auto-merge when green.
2. After merge: regenerate `docs/engineering/codemap.md` (`scripts/gen-codemap.sh`), update `docs/engineering/handover.md`, mark the feature `done` in modules.md, remove the worktree.

## Metrics (every agent run)

Append to `state.json` → `metrics`: `{"agent", "model", "tokens", "minutes", "slices"}` from the task notification. At review add `review_findings`; after CI add `ci_failures`. Targets: ≤ 0.7M tokens and ≤ 1.5 h per standard feature with no drop in quality signals.
