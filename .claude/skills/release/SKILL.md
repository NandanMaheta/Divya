---

name: release
description: Divya PR, versioning, changelog and release procedure (staging on merge, production on tag with owner approval, APK and OTA updates, rollback). Use when opening PRs or cutting releases.
---

# Release procedure

See `docs/engineering/git-workflow.md` for branch names and the full flow.

**PRs**: base branch `stage` (never `main`, except release and hotfix PRs). Conventional Commit title; body from `.github/pull_request_template.md`; labels `owner-approval` (settlement/ledger/deals/auth/migrations), `preview` (apps changed). Never force-push shared branches. If `gh pr create` is unavailable (GraphQL blocked), use `gh api repos/{owner}/{repo}/pulls -X POST -f base=stage -f head=<branch> ...`.

**Staging**: every merge into `stage` deploys via `deploy.yml` (API to Cloud Run staging, admin/PWA to Cloudflare Pages staging, migrations, smoke tests). The owner runs UAT there.

**Production**: owner says "release" → open PR `stage` → `main` titled `release: vX.Y.Z` with the changelog (release-please draft) → owner merges → `deploy.yml` waits for the `production` environment approval → deploy → smoke tests → tag `vX.Y.Z` on `main` → Sentry release.

**Hotfix**: `hotfix/<issue>-<desc>` from `main` → PR into `main` → production deploy → PR `main` → `stage` so the fix isn't lost.

**Client app**: JS-only changes ship as EAS Update (`staging` channel from `stage`, `production` channel from `main`); native changes bump the version and publish a new signed APK to R2 with the in-app update prompt.

**Rollback**: Cloud Run traffic back to the previous revision; EAS Update rollback; migrations are expand-only so the previous app version still works.
