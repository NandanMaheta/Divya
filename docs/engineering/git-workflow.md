# Git workflow

How code moves from an idea to production in Swastik. This applies to people and to AI agents alike.

## Branches at a glance

```
main   ●──────────────────────●───────────────●────────────►  production (live app)
        \                    ↑ release PR     ↑ release PR
stage    ●──●──────●────●────●──────●────●────●────────────►  staging (default branch, all work lands here)
             \    ↑      \   ↑       \   ↑
              feat/002-scheduler-tick  fix/41-otp-resend   chore/upgrade-expo-sdk
```

| Branch        | Purpose                                                                                       | Deploys to                                                   | Who merges into it                                     |
| ------------- | --------------------------------------------------------------------------------------------- | ------------------------------------------------------------ | ------------------------------------------------------ |
| `main`        | What is live for clients. Every commit on `main` is a released version.                       | **Production** (after the `production` environment approval) | Only release PRs from `stage`, and `hotfix/*` PRs      |
| `stage`       | The working branch. Everything is merged and tested here first. **Default branch on GitHub.** | **Staging** (automatically on every merge)                   | Work branches via PR (feature, fix, chore and similar) |
| Work branches | One piece of work each, short-lived (ideally under a week)                                    | Preview builds on the PR                                     | —                                                      |

Never commit directly to `main` or `stage`. Agents are blocked from pushing to them by a hook.

## Naming work branches

Format: `<type>/<id>-<short-description>`. Use lowercase, words joined by hyphens, and a description of 2–5 words that a new engineer understands without opening the PR.

| Type        | Use for                                                       | Id                        | Example                        |
| ----------- | ------------------------------------------------------------- | ------------------------- | ------------------------------ |
| `feat/`     | A feature from `docs/product/modules.md`                      | Feature number (3 digits) | `feat/016-settlement-engine`   |
| `fix/`      | A bug found on `stage` or in production (not urgent)          | GitHub issue number       | `fix/41-otp-resend-timer`      |
| `hotfix/`   | An urgent production bug; branch from `main`                  | Issue number              | `hotfix/57-wrong-due-date`     |
| `chore/`    | Tooling, dependencies, config, CI, agent setup                | — (optional issue)        | `chore/upgrade-expo-sdk-59`    |
| `refactor/` | Code restructuring with no behaviour change                   | —                         | `refactor/split-deals-service` |
| `docs/`     | Documentation only                                            | —                         | `docs/settlement-examples`     |
| `test/`     | Tests only (coverage, flaky-test fixes)                       | —                         | `test/ledger-property-tests`   |
| `spike/`    | Throwaway experiments; never merged                           | —                         | `spike/web-push-on-ios`        |
| `release/`  | Optional, to stabilise a big release before it goes to `main` | Date                      | `release/2026-12-01`           |

Avoid names like `nandan-changes`, `fix-bug`, `new-feature-2`, or anything containing secrets or client names.

## The flow, step by step

1. **Start from `stage`**: `git fetch origin && git switch -c feat/016-settlement-engine origin/stage`.
2. **Commit** with [Conventional Commits](https://www.conventionalcommits.org): `feat(settlement): compute fixed-deal amounts`, `fix(auth): ...`, `test(...)`, `docs(...)`, `chore(...)`, `refactor(...)`.
3. **Open a PR into `stage`.** The template asks for the spec link, rule coverage, test evidence and a UAT checklist. CI must be green.
4. **Merge into `stage`** (squash merge, so each feature is one commit). PRs touching money, auth or migrations need the owner's approval (CODEOWNERS). The merge deploys to **staging** automatically.
5. **Test on staging**: the owner runs the UAT checklist on the staging admin panel and app.
6. **Release**: open a PR from `stage` into `main` titled `release: vX.Y.Z`. After it merges, the deploy workflow waits for the owner's approval on the `production` environment, then deploys. The version is tagged `vX.Y.Z` on `main`.
7. **Clean up**: delete the work branch after merging.

### Hotfixes

For an urgent production bug: branch `hotfix/<issue>-<desc>` from `main` → PR into `main` → deploy → then open a PR from `main` back into `stage` (or cherry-pick) so `stage` doesn't lose the fix.

## Parallel work with worktrees

When several agents (or you and an agent) work at once, give each work branch its own folder with `git worktree` instead of switching branches in one folder:

```bash
# from the main checkout (e.g. ~/code/swastik)
git fetch origin
git worktree add ../swastik-016 -b feat/016-settlement-engine origin/stage
git worktree add ../swastik-41  -b fix/41-otp-resend-timer   origin/stage
git worktree list
# when the branch is merged:
git worktree remove ../swastik-016 && git branch -d feat/016-settlement-engine
```

- Folder name: `swastik-<id>` (`swastik-016`, `swastik-41`), so it's obvious which branch lives where.
- Never check out the same branch in two worktrees.
- Each worktree has its own `node_modules` and build output; run `pnpm install` in it once.
- Claude Code subagents with `isolation: worktree` do this automatically. The conductor (`/ship-feature`) creates one worktree per feature and merges task branches back into it.

## Spec Kit and branch names

Spec Kit finds the feature folder (`specs/016-settlement-engine/`) through `.specify/feature.json` or `SPECIFY_FEATURE_DIRECTORY`, not through the branch name. So `feat/016-settlement-engine` maps to `specs/016-settlement-engine/`. Keep the number and short description identical in both.

## GitHub settings (owner, one-time)

These can only be set in the GitHub web UI:

1. **Settings → General → Default branch**: `stage`.
2. **Settings → Rules → Rulesets** (or Branch protection) for `main` and `stage`:
   - require a pull request before merging;
   - require status checks: `api`, `worker`, `js`, `traceability`, `secrets`, `commits`;
   - require review from Code Owners;
   - block force pushes and deletions;
   - for `main`: allow merges only from `stage` (release PRs) and `hotfix/*`.
3. **Settings → Environments**: `staging` (no approval) and `production` (required reviewer: the owner).
4. **Settings → General → Pull requests**: allow squash merging; automatically delete head branches.
