# Swastik: product blueprint v2 (internal IPO operations)

**Version 2.** This replaces v1 and follows your re-described flow.

**Scope:**

- Mainboard IPOs only, internal use only, nothing published publicly.
- No automated IPO applying for now.
- Push notifications only. No WhatsApp.
- Target running cost: **₹0 a month**.

Research basis: `reports/Multi account IPO platform research.md`, plus the checks done for this version (sources at the end).

---

## 1. The whole system at a glance

```
┌──────────────────── ADMIN (you; staff later) ────────────────────┐
│ Admin web panel (desktop + installable on your phone)            │
│ Home · Approvals · IPOs · Deals · Tasks board · Allotment ·      │
│ Settlements · Clients · Reports · Notifications · Settings       │
└───────────────────────────────┬──────────────────────────────────┘
                                │
                     ┌──────────▼──────────┐   ⏱ Cloud Scheduler "tick" every 5 min
                     │  Go API (Cloud Run) │◄──  (runs reminders, deadlines, status changes)
                     │  PostgreSQL         │──► Push: Expo/FCM (Android) + Web Push (iPhone)
                     │  Python data worker │──► Email: OTPs, password reset, backup alerts
                     └──────────▲──────────┘
                                │
┌───────────────────────────────┴──────────────────────────────────┐
│ CLIENT APP (one codebase)                                        │
│ Android: installable APK          iPhone: web app on Home Screen │
│ Home · IPOs · Tasks · Allotment · Payments · Reports · Inbox ·   │
│ Family members · Profile                                         │
└──────────────────────────────────────────────────────────────────┘
```

**Roles:**

- **Admin:** you. A "Staff" role with limited permissions is designed in for later.
- **Main member:** a client who logs in.
- **Family member:** an account with only a PAN, managed by a main member, with up to 10 per main member. A family member never needs to log in.

---

## 2. Core business flow, end to end

```
 Admin adds IPO ──► marks APPLY / DO NOT APPLY
        │
        ├─► Admin publishes DEALS (profit-share by default; fixed price optional, can come later)
        │        └─► Client picks a deal per family account ──► fixed-price picks need your approval
        │
        ├─► Admin sets APPLY WINDOW(S) (date + time range, UPI ID, lots)
        │        └─► Client gets an "Apply" TASK per account + push reminders
        │                └─► Client marks APPLIED (or it becomes MISSED, then reopens in the next window)
        │
        ├─► IPO closes ──► allotment day: admin marks Allotted / Not allotted (bulk)
        │        └─► Client sees allotment + "sell on listing day" instructions
        │
        ├─► Listing day: admin enters NSE listing price
        │        └─► System calculates every settlement instantly; due = listing day + 2, end of day
        │
        └─► Client taps "Payment done" ──► admin confirms receipt ──► SETTLED ✅
                 (Fixed deal not allotted: you pay ₹F ──► client confirms received)
```

### 2.1 IPO list and the Apply / Do not apply flag

- Only **mainboard** IPOs. SME is excluded at the data level, not just hidden.
- Every IPO has one admin flag: 🟢 **APPLY** or 🔴 **DO NOT APPLY** (plus a note such as "weak GMP").
- Clients see both, so they know you looked at the IPO and decided. Only APPLY IPOs create deals and tasks.

IPO lifecycle (moved automatically by the scheduler; you can override it):

`UPCOMING → OPEN → CLOSED → ALLOTMENT_OUT → LISTED → SETTLED`

Every date drives the reminders. If you change a date, all reminders move with it.

### 2.2 IPO details page

| Section             | Content                                                                                                     | Where it comes from                                                                        |
| ------------------- | ----------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| Key facts           | Dates (open, close, allotment, refund, listing), price band, lot size, issue size, registrar, lead managers | API import (Section 6), editable by you                                                    |
| About the company   | What it does, financials summary, strengths and risks                                                       | **AI summary of the RHP PDF** (Python worker + LLM); you review and edit before publishing |
| Live subscription   | QIB / NII (sNII, bNII) / Retail / Employee / Total, × times subscribed, with "last updated" time            | API import every 30 min while open, or manual entry                                        |
| **Expected profit** | GMP (₹) → expected profit per lot = GMP × lot size, with trend (history of every GMP update)                | **You set it manually**; optional auto-fill from a GMP source that you approve             |
| Your status         | Your deal, your tasks, your allotment for this IPO                                                          | System                                                                                     |

### 2.3 Deals (the calculation you described, made exact)

**Deal types per IPO:**

- **Profit share (default):** the client gets **p%** of the listing profit, only if allotted. p comes from the client's default % (20–40%), and you can override it per IPO. If a client never picks a deal, the account stays on profit share.
- **Fixed price (optional):** the client gets **₹F per application, allotted or not**.
  - On an allotted account **with a profit**, a brokerage deduction **₹B** applies (default **₹40**, editable in Settings and per IPO).
  - The offer can be published any time, for example 10 days before the IPO, or not at all.

**Fixed prices are live quotes, not one number.** ₹F can differ **per client** and can **change during the day**, for example ₹500 at 10 am and ₹450 from 2 pm.

|                      | How it works                                                                                                                                                           |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **You publish**      | A fixed offer = price + who it's for (everyone, a group, or chosen clients) + valid from–to. You can bulk-update prices at any time ("₹500 → ₹450 for all, from now"). |
| **The client sees**  | "Fixed deal: **₹500 per account** · price valid until **4:00 pm**" with a countdown. A price change sends a gentle notification, at most one every 2 hours.            |
| **The client locks** | The price shown at the moment they tap **Submit** is frozen as their quote, with a timestamp, and sent to you for approval.                                            |
| **You approve**      | One tap per pick or in bulk. If you reject, the account goes back to profit share and the client is notified.                                                          |
| **History**          | Every price change is logged with who, when and for whom, and is visible in the admin panel.                                                                           |

**How a client chooses:**

1. The client opens the IPO and sees "Deals available".
2. For each family account they pick **Profit share** or **Fixed ₹500**.
3. They tap **Submit**. Fixed-price picks go to your **Approvals** page. You approve or reject each pick, and the client is notified either way.
4. **Deal cut-off = the moment that account is marked "Applied".** After that the deal for that account is locked. Only you can change it, and each change is logged. A fixed pick still waiting for your approval stays valid after applying; you can still approve or reject it.

**"How deals work" explainer (shown in the app on the Deals screen):**

> **Profit share (default)**
> If your account gets shares, you keep **30%** of the listing profit. If it doesn't get shares, nothing is owed either way. If the shares list at a loss, the loss is ours. You only return what the shares were worth at listing.
>
> **Fixed deal**
> You get a fixed **₹500** for that account **whether or not it gets shares**.
> • Not allotted: we pay you ₹500.
> • Allotted with profit: you keep ₹500 (₹40 brokerage is adjusted).
> • Allotted at a loss: you still keep ₹500, and the loss is ours.
>
> **Example** (investment ₹15,000, listing profit ₹3,000):
> • Profit share 30%: you return ₹15,000 + ₹3,000 − ₹900 = **₹17,100**
> • Fixed ₹500: you return ₹15,000 + ₹3,000 − ₹500 − ₹40 = **₹17,460**
>
> You can change your choice until you mark the account as **Applied**.

**Settlement formulas.** Definitions:

- **I** = investment = issue price × shares allotted.
- **G** = listing profit = (NSE listing price − issue price) × shares allotted. G is negative on a loss.

| Deal                | Not allotted          | Allotted, profit (G > 0)      | Allotted, loss (G ≤ 0)                                               |
| ------------------- | --------------------- | ----------------------------- | -------------------------------------------------------------------- |
| **Profit share p%** | Nothing either way    | Client pays **I + G − p×G**   | Client pays **I + G** (you bear the loss)                            |
| **Fixed ₹F**        | **You pay client ₹F** | Client pays **I + G − F − B** | Client pays **I + G − F** (no brokerage on a loss, per your example) |

Loss example, fixed deal: I = ₹15,000, loss ₹3,000, F = ₹500 → the client returns 15,000 − 3,000 − 500 = **₹11,500**. If the result is ever negative (a very large loss), the direction flips and **you** pay the client.

**Payment due date = listing day + 2 weekdays, by 11:59 pm.** Saturdays and Sundays are skipped:

| Listing day | Due by    |
| ----------- | --------- |
| Monday      | Wednesday |
| Tuesday     | Thursday  |
| Wednesday   | Friday    |
| Thursday    | Monday    |
| Friday      | Tuesday   |

Exchange holidays are not skipped. This is a setting you can change later.

**Your worked example**, person A with 5 accounts, all allotted, I = ₹15,000, G = ₹3,000 each:

| Account                     | Deal                      | Calculation               | Client pays you                          |
| --------------------------- | ------------------------- | ------------------------- | ---------------------------------------- |
| A1, A2, A3                  | Fixed ₹500, brokerage ₹40 | 15,000 + 3,000 − 500 − 40 | ₹17,460 × 3 = **₹52,380**                |
| A4, A5                      | Profit share 30%          | 15,000 + 3,000 − 900      | ₹17,100 × 2 = **₹34,200**                |
| **Total due from person A** |                           |                           | **₹86,580** by listing day + 2, 11:59 pm |

If none of the five were allotted, you owe person A ₹1,500 (3 × ₹500), and the profit-share accounts owe nothing.

**What the client sees.** For each account they see a clear breakdown card: Investment, + Profit, − Your share, − Brokerage = **To pay**. Before listing, the card shows an **estimate** based on the current expected profit (GMP). After you enter the listing price, it switches to **final** and the client gets a notification.

**Rules built into the engine:**

- All money is in paise (integers). Rounding is to the nearest rupee and is one setting.
- Every application stores a **snapshot** of its deal. Later changes to defaults never rewrite past IPOs.
- Correcting a listing price or allotment posts reversal and new entries, and clients are re-notified. History is never edited.
- One running **balance per main member** across all their family accounts. One payment can settle several accounts or IPOs, and partial payments are supported.

### 2.4 Payment flow

- **Client owes you:**
  1. The Payments tab shows the total due, the due date and a countdown.
  2. **Pay** opens their UPI app with your UPI ID, the amount and a reference already filled in.
  3. They tap **Payment done**, with an optional UTR or screenshot.
  4. It goes to your Approvals page. You confirm when the money is in your bank.
  5. The account shows a green **Settled ✅** badge, and the client is notified.
- **You owe the client** (fixed deal not allotted, or a loss case): you mark it paid with the UTR, and the client taps **Received**.
- **Overdue:** reminders escalate after the due date, and a warning appears on your Home page.

---

## 3. Client app (Android app + iPhone web app, one codebase)

| Screen                                | What's on it                                                                                                                                                                                                                                                                                   | Notes                                                                         |
| ------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| **Sign up / Login / Forgot password** | See Section 5                                                                                                                                                                                                                                                                                  | After the first login, fingerprint or face unlock                             |
| **Home**                              | Greeting ("Good morning, Ramesh"), with **festival theme and banner** on festival days; **"Do now" card** for the most urgent task; summary chips (open tasks, amount due, latest allotment)                                                                                                   | The festival banner is scheduled by you in the admin panel                    |
| **IPOs**                              | Open / Upcoming / Closed tabs; each card shows the 🟢 APPLY / 🔴 DO NOT APPLY flag, dates, price, lot, expected profit                                                                                                                                                                         | Detail page as in 2.2, plus a **Deals** section                               |
| **Tasks**                             | One task per family account per IPO: "Apply for **Jio Financial**: **Rajesh (PAN ••••4F)**: 1 lot (₹14,850) · UPI `abc@okhdfc` [Copy] · Window: **Today 10:00 am – 5:00 pm**" → **[Mark applied]**. Bulk "Mark all applied". States: To do · Applied · Missed (next window shown) · Not needed | The core screen; most taps happen here                                        |
| **Allotment**                         | Per IPO, per account: Allotted (X shares) / Not allotted / Awaiting; on listing day: "**Sell X shares of Jio at listing (10:00 am)** in Rajesh's account"; after listing: listing price + settlement card                                                                                      | You set all allotment data from the admin panel                               |
| **Payments**                          | Total due / to receive, due date, per-account breakdown cards, Pay, Payment done, history                                                                                                                                                                                                      | Shows the Settled ✅ badge                                                    |
| **Reports**                           | Earnings for this quarter, FY, or a custom date range; filter by family member and by IPO; applications vs allotted (hit rate); export PDF                                                                                                                                                     | Indian FY (Apr–Mar) and quarters                                              |
| **Inbox (bell icon)**                 | Every notification, with unread badge; tap to jump to the screen                                                                                                                                                                                                                               | The source of truth if a push is missed                                       |
| **Family members**                    | List (up to 10), add / edit / delete; each change shows "Pending approval" until you approve                                                                                                                                                                                                   | Fields: **name + PAN only**. The main member has name, PAN, mobile and email. |
| **Profile & settings**                | Contact details (change needs OTP + your approval), notification preferences (cannot switch off task reminders), language, logout from all devices                                                                                                                                             |                                                                               |

UX rules:

- Everything urgent is reachable in two taps from Home.
- Large numbers with Indian formatting (₹1,48,500).
- Status colours are the same everywhere: amber = to do, blue = waiting, green = done or allotted, red = missed or overdue, grey = not allotted.
- Dark mode. English first, with Gujarati and Hindi ready.

---

## 4. Admin panel

| Page              | Purpose                                                                                                                                                                                                                                                                                               |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Home**          | Today's actions:<br>• pending approvals<br>• per open IPO, **who has / hasn't applied** (live)<br>• allotments to enter<br>• listings today<br>• **payments overdue ⚠️** and due soon<br>• amounts you owe<br>• recent GMP and deal changes<br>• FY earnings widget<br>• calendar of the next 14 days |
| **Approvals**     | One queue for:<br>• new registrations<br>• add, edit or delete family member<br>• contact-detail changes<br>• fixed-deal selections<br>• "Payment done" claims<br>• payout confirmations<br>Approve or reject one at a time or in bulk, with an optional reason.                                      |
| **IPOs**          | Create, import or edit; APPLY / DO NOT APPLY flag; company brief (AI draft → you edit → publish); **GMP / expected profit updates** with history; subscription data; dates                                                                                                                            |
| **Deals**         | Per IPO: profit-share % (default from each client, with override), publish or update a fixed-price offer (₹F, ₹B, cut-off), see who picked what, approve fixed picks                                                                                                                                  |
| **Tasks board**   | Per IPO grid of every account: deal, UPI assigned, lots, task status, last reminder, applied time. Bulk actions:<br>• send reminder now<br>• change UPI for selected rows<br>• add an apply window (e.g. day 2)<br>• excuse an account                                                                |
| **Allotment**     | Per IPO: tick or paste Allotted / Not allotted plus shares for every account; enter the **NSE listing price** → settlements calculate instantly                                                                                                                                                       |
| **Settlements**   | Per IPO and per client: amounts, due dates, status (Due / Claimed paid / Settled / Overdue), confirm received, record payouts                                                                                                                                                                         |
| **Clients**       | Main members, their family accounts (PAN masked), default %, balance, app installed or not, devices, activity history; you can add a client yourself                                                                                                                                                  |
| **Reports**       | FY, quarter or custom period; by client, family account, IPO or deal type; your earnings, client earnings, outstanding amounts, allotment hit rate; export CSV, Excel, PDF                                                                                                                            |
| **Notifications** | Templates, reminder intensity rules, festival greetings with banner and schedule, broadcast message, delivery and read stats                                                                                                                                                                          |
| **Settings**      | Your UPI IDs (several, default per IPO), default brokerage ₹B, payment due rule (listing + 2 days), quiet hours, rounding, festival calendar, staff users and roles (later)                                                                                                                           |
| **Audit log**     | Every change by anyone, with before and after values                                                                                                                                                                                                                                                  |

You get notified (push to your phone, plus the admin bell) for every client action: registration, family-member change, deal pick, applied, payment claim.

---

## 5. Login and registration (standard and secure, ₹0)

| Flow                       | Steps                                                                                                                                                                                    |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Sign up**                | Name, mobile, email, PAN, password → **email OTP** (6 digits, 10 min) → **mobile OTP** (SMS) → status **Pending approval** → you approve → welcome notification → login                  |
| **Login**                  | Mobile or email + password → after the first login on a device, unlock with fingerprint or face (PIN fallback)                                                                           |
| **Forgot password**        | Enter mobile or email → OTP → set new password → all other devices logged out                                                                                                            |
| **Change email or mobile** | OTP to the new contact → your approval → old contact notified                                                                                                                            |
| **Security**               | Passwords hashed with Argon2id; short-lived access token + rotating refresh token bound to the device; 5 failed attempts = 15-min lock; "log out all devices"; you can revoke any device |
| **Admin login**            | Email + password + authenticator app (TOTP 2FA); the admin site also sits behind Cloudflare Access (free)                                                                                |

OTP delivery at ₹0:

- **Email OTP:** free through Brevo (300 emails a day) or Resend (3,000 a month, needs a domain).
- **Mobile OTP:** Firebase phone verification. The first **10 SMS a day are free**, then about $0.07 per SMS. Use it only at sign-up and when a mobile number changes; at about 100 users you will almost never exceed 10 a day.

Auth is written yourself in Go (good learning and interview material), with Firebase used only to send and verify the SMS.

---

## 6. IPO data: where it can come from

Nothing official exists as an API. NSE publishes subscription ("bid details") and offer documents, but its terms of use **forbid scraping**. IPO Premium does not disclose where its data comes from; it most likely aggregates exchange bid data and market GMP reports.

What you can use legitimately or cheaply:

| Data                       | Source options                                                                                                                                                                                                                                                                                                                | Plan                                                                                              |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| IPO calendar and key facts | Exchange circulars and offer documents (manual); **IPO Guru developer API** (documented fields: dates, price band, issue size, mainboard/SME, subscription by QIB/NII/Retail, GMP with timestamp; pricing not visible from here); Apify "India IPO Tracker" actor (~$0.003 per row, i.e. a few rupees a month; scraper-based) | Start: **import + you confirm**; manual edit always possible                                      |
| Company details            | The **RHP PDF**, which is public on SEBI and exchange sites                                                                                                                                                                                                                                                                   | Python worker downloads it → LLM writes a plain-language brief → **you review** → publish         |
| Live subscription          | Same API or actor, pulled every 30 min on open days                                                                                                                                                                                                                                                                           | Shows "updated hh:mm"; manual override                                                            |
| GMP / expected profit      | No licensed source exists; sites such as IPO Market publish free averages                                                                                                                                                                                                                                                     | **Manual by you** (as you asked), with an optional "suggested GMP" import you accept with one tap |
| NSE listing price          | Your broker app on listing day (manual, one field); later your own Kite Connect key (₹500/month)                                                                                                                                                                                                                              | Manual at first                                                                                   |

Caveat: the API providers above repackage exchange and grey-market data, so their licensing is unclear. That is fine for a private internal tool, but keep the source and "last updated" visible and never present GMP as official. I could not open ipoguru.in from this session (network-blocked), so check its pricing and terms yourself.

---

## 7. Notifications and scheduled jobs

### 7.1 Channels (₹0)

- **Android app:** Expo Push / FCM. Notification channels let the client tune sounds:
  - "**Tasks (urgent)**": sound plus heads-up.
  - "**Updates**": normal.
  - "**Greetings**": silent.
- **iPhone (web app on Home Screen):** Web Push, which works on iOS 16.4+ **only after "Add to Home Screen"**. Delivery is less reliable after restarts, so the inbox and an **email fallback** for critical items (payment due, deadline today) cover iPhone users.
- **In-app inbox** with unread badge, so nothing is lost if a push doesn't arrive.
- **Admin alerts:** push to your phone (admin panel installed as an app) plus the bell in the panel.

**On Xiaomi, Vivo, Oppo and Realme phones**, the app shows a one-time "Allow background / autostart" guide, because these phones' battery savers suppress pushes otherwise.

### 7.2 Reminder intensity (you choose per task type; defaults below)

| Level       | Cadence                                               | Used for                                                                |
| ----------- | ----------------------------------------------------- | ----------------------------------------------------------------------- |
| 🙂 Gentle   | Once, then once a day                                 | New IPO announced, fixed deal available, greetings, report ready        |
| 🔔 Standard | **Every 2 hours** inside the apply window until done  | Apply task (normal day)                                                 |
| 🚨 Urgent   | **Every 1 hour**, last reminder 30 min before cut-off | Apply task on closing day; payment due today; overdue payments (2×/day) |

Rules:

- Quiet hours 9:30 pm – 8:00 am. Payment-overdue reminders skip quiet hours only if you enable it.
- Reminders **stop instantly** when the task is done.
- Notifications are grouped, e.g. "3 accounts still to apply for Jio", not 3 separate pushes.
- Deduplicated: never the same reminder twice.

### 7.3 Are cron jobs needed? Yes. One "tick" runs them all.

Cloud Scheduler calls `POST /internal/tick` **every 5 minutes**; this fits its free tier. Each tick runs the due jobs from a `scheduled_jobs` table, locking rows so no job runs twice.

| Job                                                                       | When                              |
| ------------------------------------------------------------------------- | --------------------------------- |
| Move IPO status (open, closed, allotment, listed)                         | By IPO dates and times            |
| Open and close apply windows; mark tasks MISSED; open the next-day window | Window start and end              |
| Send task reminders by intensity level                                    | Every tick, for due reminders     |
| Lock deal choices at cut-off                                              | Deal cut-off time                 |
| Refresh subscription and IPO data (Python worker)                         | Every 30 min while an IPO is open |
| Listing-day "sell at listing" reminder                                    | Listing day 9:15 am               |
| Payment due countdown, due-today, overdue escalation                      | Daily 10 am + hourly on due day   |
| Festival greetings and scheduled broadcasts                               | Your scheduled time               |
| Clean dead push tokens (from delivery receipts)                           | Hourly                            |
| **Nightly database backup** to Cloudflare R2                              | 2 am (GitHub Actions cron)        |
| Monthly/FY report snapshots                                               | 1st of month, 1 April             |

Notifications are written to an **outbox** in the same database transaction as the event that causes them, so a reminder is never lost and never sent twice.

---

## 8. Tech stack (final)

| Layer            | Choice                                                                                                                                                    | New for your resume |
| ---------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------- |
| Core API         | **Go 1.27**: net/http + Huma v2 (OpenAPI 3.1), pgx v5 + **sqlc**, goose migrations, slog + OpenTelemetry                                                  | ✅ Go               |
| Data & AI worker | **Python 3.13 + FastAPI**, managed with `uv` and checked with Ruff: IPO data import, RHP → company brief with an LLM, GMP suggestion                      | ✅ Python, LLM      |
| Database         | **PostgreSQL** (Supabase free tier as plain Postgres); integer-paise ledger; uuidv7 IDs                                                                   | Deeper Postgres     |
| Client app       | **Expo SDK 58** (React Native 0.88, New Architecture, React Compiler) + **Expo Router**; the same code builds the Android app and the iPhone web app      | Upgrade             |
| Mobile UI        | **Uniwind** (Tailwind v4 for RN) + React Native Reusables, Reanimated 4, FlashList v2, expo-image                                                         | ✅                  |
| Client data      | TanStack Query (cached to MMKV), Zustand, React Hook Form + Zod                                                                                           |                     |
| Admin panel      | **Vite + React 19 + TanStack Router / Query / Table + shadcn/ui + Tailwind v4**, installable as an app                                                    | Upgrade             |
| Shared types     | OpenAPI → generated TypeScript client (openapi-typescript + openapi-fetch)                                                                                | ✅                  |
| Monorepo         | **pnpm + Turborepo**; TypeScript 7; Biome 2; golangci-lint v2                                                                                             | ✅                  |
| Testing          | Go tests + **testcontainers** (real Postgres) + property tests for settlement maths; pytest; Vitest; **Maestro** (mobile E2E); **Playwright** (admin E2E) | ✅                  |
| Infra as code    | **OpenTofu (Terraform)** for Cloud Run, Scheduler and Cloudflare                                                                                          | ✅ Terraform        |
| CI/CD            | GitHub Actions + Claude Code agents (Section 10)                                                                                                          |                     |

Monorepo:

```
swastik/
├─ apps/
│  ├─ api/        Go modular monolith: internal/{auth,clients,members,ipos,deals,tasks,allotment,settlement,ledger,notify,reports,approvals,scheduler}
│  ├─ worker/     Python FastAPI: ipo_import, rhp_brief, gmp_suggest
│  ├─ client/     Expo app (Android + web/PWA for iPhone)
│  └─ admin/      Vite React admin panel
├─ packages/      api-client · tokens (design tokens) · ui-native · domain (formatters, enums, Zod)
├─ infra/         OpenTofu, Dockerfiles, backup scripts
├─ specs/         Spec Kit specs/plans/tasks
└─ .claude/       agents, skills, hooks, rules
```

---

## 9. Hosting at ₹0 a month

| Need                         | Free option                                                                                                        | Free limits                                                                   | Watch-outs                                                                                                                                                  |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go API + Python worker       | **Google Cloud Run, Mumbai** (asia-south1), scale to zero                                                          | ~180k–240k vCPU-seconds + 2M requests a month (official figures vary by page) | Needs a billing account (card); set a **₹1 budget alert**; Go cold start is ~0.2–0.5 s                                                                      |
| Cron                         | **Cloud Scheduler**                                                                                                | **3 jobs free** per billing account (we need 1–2)                             |                                                                                                                                                             |
| Database                     | **Supabase Free** (used as Postgres)                                                                               | 500 MB DB, 1 GB files, 2 projects                                             | Pauses after a week of low activity; the 5-min tick keeps it active. **No backups on free**, so we take our own nightly. Confirm Mumbai region at creation. |
| Admin panel + iPhone web app | **Cloudflare Pages**                                                                                               | Unlimited static                                                              |                                                                                                                                                             |
| Admin protection             | **Cloudflare Access**                                                                                              | Free up to 50 users                                                           |                                                                                                                                                             |
| Backups + images (banners)   | **Cloudflare R2**                                                                                                  | 10 GB, no egress fees                                                         |                                                                                                                                                             |
| Push                         | Expo Push / FCM / Web Push                                                                                         | Free                                                                          |                                                                                                                                                             |
| Email (OTP, reset, fallback) | Brevo (300/day) or Resend (3,000/month)                                                                            | Free                                                                          | Resend needs your own domain                                                                                                                                |
| SMS OTP                      | Firebase phone verification                                                                                        | 10 SMS a day free                                                             | Then ~$0.07 each                                                                                                                                            |
| Errors / uptime              | Sentry free, UptimeRobot free                                                                                      |                                                                               |                                                                                                                                                             |
| Android distribution         | **Direct APK** (download link + in-app "update available") + EAS Update for instant fixes (free up to 1,000 users) | Free                                                                          | Google's sideloading developer verification goes global in 2027; plan to verify then                                                                        |
| iPhone                       | **Web app (PWA)**                                                                                                  | Free                                                                          | Native iPhone app needs Apple Developer $99/yr; skip for now                                                                                                |
| AI company briefs            | LLM API                                                                                                            | Pay per use, about ₹2–10 per IPO                                              | Optional; manual writing is the ₹0 path                                                                                                                     |

**Totals:**

- **₹0 a month** for infrastructure.
- Optional extras: a domain (~₹800/year, recommended for email deliverability and a clean URL), Google Play ₹2,100 one-time (only if you want Play internal testing instead of APK), AI briefs at a few rupees per IPO.

**When you outgrow free** (more users, or wanting zero cold starts): move Postgres to Supabase Pro ($25/month) or move everything to one Lightsail Mumbai VM (~₹1,130/month). Because the infrastructure is written in OpenTofu, that is a config change, not a rewrite.

**Selling it as a product later:** every table carries `org_id` from day one (multi-tenant ready), roles are Owner / Staff / Client, and the admin panel is already separate.

---

## 10. AI-agent build pipeline (unchanged from v1, summary)

1. **Idea → spec:** `/speckit-specify`, then you approve the spec.
2. **Plan:** `/speckit-plan`, then you approve the plan.
3. **Build:** `/speckit-tasks`, then backend, admin and mobile agents work in parallel worktrees.
4. **Gates on every edit:** hooks run format, lint and typecheck. A Stop hook keeps the agent working until tests pass.
5. **Review:** an independent reviewer agent plus a security reviewer.
6. **Ship:** PR → CI gates → preview APK and admin preview URL → your 10-minute check → merge → auto-deploy.

**Agents:** product-spec, architect, backend-dev (Go), data-dev (Python), admin-dev, mobile-dev, test-engineer, reviewer, security-reviewer, release, weekly ops.

**MCP servers:** GitHub, Context7 (docs), Maestro, Playwright, Postgres (dev only), Sentry.

---

## 11. Roadmap

| Phase                   | Weeks | Delivers                                                                                                                                                                                                                                                                                                |
| ----------------------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0. Foundation           | 1     | Monorepo, CI, Spec Kit constitution, agents, design tokens, Cloud Run + Supabase + Cloudflare (OpenTofu), auth skeleton                                                                                                                                                                                 |
| 1. Admin core           | 2–4   | IPOs (APPLY / DO NOT APPLY, details, manual GMP), clients + family accounts, approvals queue, deals (live fixed quotes per client and time), apply windows + tasks board, allotment + listing price, **settlement engine + ledger**, payments confirm, reports, audit log. Fresh start, no data import. |
| 2. Client app           | 4–7   | Sign up / login / reset with OTPs, Home + greetings, IPOs, Tasks, Allotment, Payments, Reports, Inbox, Family members; Android APK + iPhone web app                                                                                                                                                     |
| 3. Notifications engine | 6–8   | Tick scheduler, reminder intensity levels, quiet hours, grouping, email fallback, festival greetings, admin alerts                                                                                                                                                                                      |
| 4. Data + AI            | 8–10  | Python worker: IPO import, subscription refresh, RHP → AI company brief, GMP suggestions                                                                                                                                                                                                                |
| Later                   |       | Staff roles, Kite Connect listing price, native iPhone app, multi-tenant product, automated applying (with a broker, after legal review)                                                                                                                                                                |

---

## 12. Python and new-tech suggestion for your resume

Your resume today: React, React Native, Next.js, TypeScript, Node/Express, PostgreSQL. Gaps employers ask about in 2026: a typed, compiled backend language, Python, AI/LLM integration, cloud and IaC.

**Recommendation: Go + Python, each where it fits best.**

- **Go** for the core API. Ledger, auth, scheduling and settlement read as serious backend engineering, and Go is used at Indian fintechs (Zerodha, Razorpay, PhonePe).
- **Python (FastAPI)** for the data and AI worker. Python job volume is the largest of any language, and "built an LLM pipeline that turns 400-page IPO prospectuses into reviewed company briefs" is a strong 2026 resume line.
- **Plus:** OpenTofu/Terraform on Google Cloud, OpenTelemetry, testcontainers, Maestro and Playwright E2E, Spec-Kit-driven AI development (already on your resume; now with a multi-agent pipeline).

Suggested resume line once built:

> _Swastik: IPO operations platform (Go, Python/FastAPI, PostgreSQL, Expo/React Native, React, GCP Cloud Run, OpenTofu). Designed a double-entry settlement ledger and event-driven reminder scheduler; built an LLM pipeline summarising IPO prospectuses; shipped Android + PWA clients from one codebase; ran delivery through a multi-agent Claude Code + Spec Kit pipeline with automated test gates._

If you prefer only **one** new language: pick **Python/FastAPI** if you are aiming at AI-product or full-stack roles, or **Go** if you are aiming at backend or fintech roles. Doing both, as above, costs little because the Python part is small and isolated.

---

## 13. Decisions confirmed

| #   | Decision                                                                                                               |
| --- | ---------------------------------------------------------------------------------------------------------------------- |
| 1   | Fixed deal at a loss: the client still keeps ₹F and returns **I + G − F**. No brokerage on a loss.                     |
| 2   | Brokerage ₹B: **₹40 default**, editable in admin Settings and per IPO.                                                 |
| 3   | Due date: **listing day + 2 weekdays** (skip Sat/Sun), 11:59 pm.                                                       |
| 4   | Deal cut-off: **when that account is marked Applied**; profit share is the default if nothing is picked.               |
| 5   | Fixed ₹F: **per client and time-varying** (live quotes with validity windows; the client's price is frozen at submit). |
| 6   | **1 lot per account.**                                                                                                 |
| 7   | Family member: **name + PAN**. Main member: name, PAN, mobile, email.                                                  |
| 8   | Domain: approved (~₹800/year).                                                                                         |
| 9   | No past data. Fresh start.                                                                                             |

One small thing to double-check: in your profit example the ₹40 brokerage is deducted, but in your loss example it isn't. I've built it exactly that way; brokerage only on allotted, profitable fixed-deal accounts. If brokerage should also apply on a loss, it's one setting.

---

### Sources for v2 checks

- [Firebase / Identity Platform pricing (Logto summary, 10 free SMS/day, India ~$0.07)](https://blog.logto.io/firebase-authentication-pricing)
- [Identity Platform pricing (official)](https://cloud.google.com/identity-platform/pricing)
- [Supabase: free project pausing (official FAQ)](https://supabase.com/kb/faqs/platform/free-project-pausing)
- [Supabase free-tier limits 2026](https://dev.to/mahdi_benrhouma_fe1c6005/supabase-free-tier-limits-in-2026-what-you-actually-get-and-when-you-will-hit-them-4mhe)
- [Cloud Scheduler pricing (3 free jobs)](https://cloud.google.com/scheduler/pricing)
- [Cloud Run pricing](https://cloud.google.com/run/pricing)
- [iOS PWA push limitations](https://www.magicbell.com/blog/pwa-ios-limitations-safari-support-complete-guide)
- [iOS web push requirements](https://pushpad.xyz/blog/ios-special-requirements-for-web-push-notifications)
- [IPO Guru developer API](https://www.ipoguru.in/ipo-gmp-details-developer-api)
- [Apify India IPO Tracker](https://apify.com/data_daemon/india-ipo-tracker)
- [IPO Market (GMP methodology)](https://www.ipomarket.in/about)
