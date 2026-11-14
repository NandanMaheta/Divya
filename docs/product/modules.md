# Swastik module map and build order

The conductor (`/ship-module`) works through this file top to bottom. Each feature becomes one Spec Kit feature folder `specs/NNN-<slug>/`. Tier A = money, ledger, deals, auth, PII (full flow); Tier B = everything else (combined spec + plan) — see `docs/engineering/delivery-pipeline.md`. Status values: `todo`, `spec`, `building`, `review`, `uat`, `done`. Agents update the Status column; the owner can reorder rows.

Rule IDs refer to `docs/product/business-rules.md`. Parallel tracks are marked; a feature never starts before its dependencies are `done`.

| #   | Feature                                                                                                   | Module           | Rules                            | Depends on | Track | Status         | Tier |
| --- | --------------------------------------------------------------------------------------------------------- | ---------------- | -------------------------------- | ---------- | ----- | -------------- | ---- |
| 001 | Platform: orgs, audit log, health, CI, deploy skeleton                                                    | M0 Platform      | —                                | —          | core  | done (Stage 0) | B    |
| 002 | Scheduler tick + `scheduled_jobs` + outbox (fake-clock testable)                                          | M9 Scheduler     | BR-IPO-03, BR-TASK-03, BR-SET-08 | 001        | core  | building       | B    |
| 003 | Auth core: users, Argon2id passwords, sessions, refresh rotation, device binding, admin TOTP              | M1 Auth          | BR-CLI-04                        | 001        | core  | todo           | A    |
| 004 | OTP delivery: email OTP (Brevo/Resend), SMS verification via Firebase, rate limits                        | M1 Auth          | BR-CLI-04, BR-CLI-05             | 003        | core  | todo           | A    |
| 005 | Registration + approval queue (generic approvals module)                                                  | M2 Clients       | BR-CLI-01, BR-CLI-04             | 003, 004   | core  | todo           | A    |
| 006 | Family accounts (≤10, name + PAN, encrypted PAN + blind index) with approvals                             | M2 Clients       | BR-CLI-02, BR-CLI-03             | 005        | core  | todo           | A    |
| 007 | Contact-detail changes with OTP + approval                                                                | M2 Clients       | BR-CLI-05                        | 005        | core  | todo           | A    |
| 008 | IPO catalogue: CRUD, mainboard only, APPLY/DO NOT APPLY, lifecycle by dates                               | M3 IPOs          | BR-IPO-01, -02, -03              | 002        | ipo   | todo           | B    |
| 009 | GMP / expected profit with history; subscription snapshot fields                                          | M3 IPOs          | BR-IPO-04                        | 008        | ipo   | todo           | B    |
| 010 | Deals: default profit-share %, per-IPO override, deal snapshot per application                            | M4 Deals         | BR-DEAL-01, -05                  | 006, 008   | money | todo           | A    |
| 011 | Fixed-price live quotes: audience, validity windows, freeze on submit, approval                           | M4 Deals         | BR-DEAL-02, -03, -04             | 010        | money | todo           | A    |
| 012 | Apply windows + tasks per account (1 lot, UPI ID), applied / missed / reopen                              | M5 Tasks         | BR-TASK-01, -02                  | 008, 006   | ipo   | todo           | B    |
| 013 | Reminder engine: intensity levels, quiet hours, grouping, stop-on-done                                    | M9 Notifications | BR-TASK-03                       | 002, 012   | core  | todo           | B    |
| 014 | Push delivery: Expo/FCM (Android), Web Push (iPhone PWA), email fallback, inbox                           | M9 Notifications | BR-TASK-03                       | 013        | core  | todo           | B    |
| 015 | Allotment entry (bulk) + listing price entry                                                              | M6 Allotment     | BR-SET-09                        | 012        | money | todo           | A    |
| 016 | Settlement formula engine (pure functions, property tests, golden scenarios)                              | M7 Settlement    | BR-SET-01…-07, -11, GS-01…-03    | 010        | money | todo           | A    |
| 017 | Ledger: double-entry postings, balances per main member, reversals                                        | M7 Settlement    | BR-SET-07, -10                   | 016        | money | todo           | A    |
| 018 | Settlement on listing + estimates from GMP + due-date calculator                                          | M7 Settlement    | BR-SET-08, -09, GS-04            | 015, 017   | money | todo           | A    |
| 019 | Payments: client "payment done", admin confirm, payouts, overdue escalation                               | M8 Payments      | BR-PAY-01…-03                    | 018, 013   | money | todo           | A    |
| 020 | Reports: client (quarter/FY/custom, per member) + admin (earnings, outstanding, hit rate), exports        | M10 Reports      | BR-SET-11                        | 019        | money | todo           | B    |
| 021 | Greetings, festival banners, broadcasts                                                                   | M14 Engagement   | —                                | 014        | ui    | todo           | B    |
| 022 | Admin panel: shell, auth, Home/Today, Approvals                                                           | M12 Admin        | —                                | 005        | ui    | todo           | B    |
| 023 | Admin panel: IPOs, Deals, Tasks board, Allotment, Settlements, Clients, Reports, Settings, Audit          | M12 Admin        | (per page)                       | 008–020    | ui    | todo           | B    |
| 024 | Client app: shell, auth flows (sign up, OTP, login, forgot password, biometric unlock)                    | M11 Client       | BR-CLI-04                        | 004        | ui    | todo           | B    |
| 025 | Client app: Home, IPOs + Deals, Tasks, Allotment, Payments, Reports, Inbox, Family, Profile               | M11 Client       | (per screen)                     | 008–020    | ui    | todo           | B    |
| 026 | Data worker: IPO import, subscription refresh, RHP → AI brief, GMP suggestion                             | M13 Data         | BR-IPO-04                        | 008        | data  | todo           | B    |
| 027 | Release & ops: Cloud Run + Pages + Access via OpenTofu, APK pipeline, backups + restore drill, monitoring | M15 Ops          | —                                | 001        | ops   | todo           | B    |

Notes:

- UI features (022–025) are built per backend feature as it lands; the conductor may split them into one spec per screen group.
- Feature 016 is the riskiest; it uses the strongest available model for spec, plan and review, and its PRs always need owner approval.
