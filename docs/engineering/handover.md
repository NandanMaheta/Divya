# Handover

Read this first in every new conductor session (with `CLAUDE.md` and `docs/engineering/delivery-pipeline.md`). Keep it short and current: update it whenever a feature merges or a decision is made.

_Last updated: 2026-11-14_

## Owner and setup

- Owner: Nandan Maheta. Repo: `NandanMaheta/Swastik`. Default branch `stage` (staging), `main` = production. Branch protection and the `staging` / `production` environments are configured.
- Accounts ready:
  - domain `swastikapp.co.in` (Cloudflare, Active; email via Resend on `mail.swastikapp.co.in`)
  - Google Cloud project `swastik-4fcc9`
  - Supabase staging `hddzoasgsdtvdzauxfbo` and prod `dxymdmefbgztygbxqcbb` (Mumbai)
  - Firebase (Blaze, phone auth, India-only SMS, Android app `com.nandanmaheta.swastik`)
  - Expo `nandy99`
  - Sentry project `swastik-api`
- Secrets are added only when a feature needs them. Never ask for secret values in chat; give the exact GitHub/Secret Manager names.

## Decisions to remember

- Product renamed from Divya to Swastik. The GitHub API needs the repo attached as `NandanMaheta/Swastik`.
- Owner prefers: plain, non-AI-sounding docs and README; short replies; the accounts and status tables only when asked.
- Libraries approved:
  - 002: go-oidc v3, uuid, rapid
  - 003: go-jose v4, pquerna/otp, x/crypto, x/term, x/text
- Pipeline v2 (risk tiers, briefs, slices, batched passes) applies to all work from 2026-11-14.

## Feature status

| Feature                | Branch                    | State                                                                                                                                               |
| ---------------------- | ------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| 002 Scheduler + outbox | `feat/002-scheduler-tick` | Phases 1–4 implemented and green; Phase 5–6 tests written (RED); next: one code pass for Phases 5–6, then Phase 7 + review + PR                     |
| 003 Auth core          | `feat/003-auth-core`      | Spec/plan approved; T001 + Phase 2 tests written (RED); next: re-slice remaining tasks into v2 slices, Phase 2 code pass; the rest needs 002 merged |
| 008 IPO catalogue      | `feat/008-ipo-catalogue`  | Spec/plan approved; 68 tasks planned; next: re-slice into v2 slices; build after 002 (core) and 003 (HTTP)                                          |
| Others                 | —                         | Not started; see `docs/product/modules.md` (Tier column)                                                                                            |

## Next steps

1. Merge the pipeline v2 PR into `stage`; merge `stage` into the 002/003/008 branches.
2. Re-slice 003 and 008 (planner) into 8–20 slices each.
3. 002: code pass for Phases 5–6 → Phase 7 → review → PR (owner merges).
4. Bring hosting (027) forward after 002/003 so staging is live.
