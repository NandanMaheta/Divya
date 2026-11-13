---

name: release
description: Divya PR, versioning, changelog and release procedure (staging on merge, production on tag with owner approval, APK and OTA updates, rollback). Use when opening PRs or cutting releases.
---

# Release procedure

**PRs**: Conventional Commit title; body from `.github/pull_request_template.md`; labels `owner-approval` (settlement/ledger/deals/auth/migrations), `preview` (apps changed). Never force-push shared branches.

**Staging**: every merge to `main` deploys via `deploy.yml` (API to Cloud Run staging, admin/PWA to Cloudflare Pages staging, migrations, smoke tests).

**Production**: owner says "release" → create tag `vX.Y.Z` (semver; release-please drafts notes) → `deploy.yml` waits for the `production` environment approval → deploy → smoke tests → Sentry release.

**Client app**: JS-only changes ship as EAS Update to the `production` channel; native changes (new module, permission, SDK upgrade) bump the version and publish a new signed APK to R2 with the in-app update prompt.

**Rollback**: Cloud Run traffic back to the previous revision; EAS Update rollback; migrations are expand-only so the previous app version still works.
