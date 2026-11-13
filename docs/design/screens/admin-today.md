# Admin: Today (Home)

Status: design proposal (Stage 1). Tokens and components: `../design-direction.md`. Rules: BR-TASK-02, BR-SET-08, BR-SET-11, BR-PAY-01..03, BR-CLI-03..05, BR-DEAL-03, BR-IPO-04. Blueprint section 4 "Home".

## Purpose

The operator's morning page: **what needs me today, in order of urgency**, with one click into the page that resolves it. Not an analytics dashboard: counts and lists first, one small earnings widget last.

## Layout (desktop ≥ 1280 px, light mode)

```
┌────────────┬──────────────────────────────────────────────────────────────────────────┐
│ Swastik      │ Today · Fri, 14 Nov 2026                     Updated 9:42 am IST  🔔 4  │  header 64, surface.base
│            ├──────────────────────────────────────────────────────────────────────────┤
│ ▸ Today    │ ┌─────────────┐┌─────────────┐┌─────────────┐┌─────────────┐┌──────────┐ │  attention strip: 5 tiles
│   Approvals│ │⚠ Overdue    ││⏳ Approvals ││◉ Allotments ││◉ Listing    ││◉ You owe │ │  icon + label (bodySm 600)
│   IPOs     │ │ 3 payments  ││ 7 waiting   ││ 1 IPO to    ││ today       ││ ₹2,000   │ │  value: heading 20/600
│   Deals    │ │ ₹1,04,040   ││             ││ enter       ││ Jio Fin.    ││ 4 clients│ │
│   Tasks    │ └─────────────┘└─────────────┘└─────────────┘└─────────────┘└──────────┘ │
│   Allotment│                                                                          │
│   Settle.  │ Open IPOs: who has applied                         ┌──────────────────┐  │
│   Clients  │ ┌───────────────────────────────────────────────┐  │ Next 14 days     │  │
│   Reports  │ │ Jio Financial   Closes today 5:00 pm IST      │  │ Fri 14  Jio list │  │
│   Notific. │ │ ████████████████████░░░░░░  212 / 260 applied │  │ Mon 17  Tata open│  │
│   Settings │ │ ✓ 212 applied  ◉ 41 to do  ⚠ 7 missed         │  │ Tue 18  Jio due  │  │
│   Audit    │ │ [Open tasks board]  [Remind 41 now]           │  │ …                │  │
│            │ └───────────────────────────────────────────────┘  └──────────────────┘  │
│            │                                                                          │
│            │ Payments                                          ┌──────────────────┐   │
│            │ ┌───────────────────────────────────────────────┐ │ FY 2026–27       │   │
│            │ │ ⚠ Overdue   Ramesh Shah   ₹86,580  since 18 Nov│ │ You earned       │   │
│            │ │ ◉ Due today Priya Mehta   ₹34,920  11:59 pm    │ │ ₹4,82,300        │   │
│            │ │ ⏳ Claimed   Anil Patel    ₹17,460  UTR …8890   │ │ Q3 ₹1,12,600     │   │
│            │ │ [All settlements]                              │ │ (link: Reports)  │   │
│            │ └───────────────────────────────────────────────┘ └──────────────────┘   │
│            │                                                                          │
│            │ Recent changes (GMP, deals)                                              │
│            │ 9:15 am  Jio GMP ₹48 → ₹52 (you)                                         │
│            │ 9:02 am  Fixed offer Swiggy ₹500 → ₹450 for all, from 2:00 pm            │
└────────────┴──────────────────────────────────────────────────────────────────────────┘
```

At 1024–1279 px the right column (calendar, FY) moves below the main column. Below 1024 px (owner's phone, installed admin PWA) the page is a single column: attention strip becomes a 2-column grid, tables become stacked rows; it is designed for glancing and approving, not data entry.

## Components

Sidebar nav (240 px, active item `brand.50` fill + `brand.900` text + 3 px `brand.600` left rule), header with an "Updated {time} IST" stamp and bell, attention tile, IPO progress card (stacked bar + counts), payments list, mini calendar list, FY widget, change feed, `StatusChip`, `Amount`, `StateBlock`, offline banner.

## Content rules

### Attention strip (order fixed, left to right by urgency)

| Tile                         | Tone  | Value                                                                                                     | Click →                       |
| ---------------------------- | ----- | --------------------------------------------------------------------------------------------------------- | ----------------------------- |
| Overdue payments (BR-PAY-03) | red   | count + total                                                                                             | Settlements filtered Overdue  |
| Approvals waiting            | blue  | count (registrations, family changes, contact changes, fixed picks, payment claims, payout confirmations) | Approvals                     |
| Allotments to enter          | amber | IPOs with ALLOTMENT_OUT and missing data                                                                  | Allotment for the first IPO   |
| Listing today                | amber | IPO names; "Enter listing price" if missing                                                               | Allotment listing price field |
| You owe                      | blue  | total + clients count (BR-SET-04/06)                                                                      | Settlements filtered "We owe" |

A tile with nothing shows grey "None" with a green tick, and stays in place (no layout shift day to day).

### Open IPO progress

- One card per OPEN IPO (and CLOSED today). Stacked bar: green applied, amber to do, red missed, grey not needed. The bar is decorative; counts are written beside it with icons.
- Buttons: **Open tasks board** (filtered to the IPO) and **Remind {n} now** (opens the same confirm dialog as on the tasks board).
- Live: refetch every 30 s while the page is visible (exact mechanism is a plan decision); the "Updated" stamp changes, numbers cross-fade, nothing reorders.

### Payments list

Top 5 rows sorted: overdue (oldest first), due today, claimed (waiting for confirmation), due soon. Each row: status chip, client name, amount (right, tabular), detail. Click row → client settlement.

### FY widget

"You earned" for the current FY (BR-SET-11 operator earnings) plus the current quarter. Numbers only; no chart here. Links to Reports.

## States

| State             | What shows                                                                                                                                              |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Loading           | Each section has its own skeleton (5 tiles, 1 IPO card, 5 list rows). Sections load independently.                                                      |
| Empty (quiet day) | Tiles show "None". IPO section: "No IPOs are open today. Next: Tata Technologies opens Mon, 17 Nov." Payments: "No payments due or overdue."            |
| Error (section)   | That section shows an inline error: "Couldn't load payments. [Try again]" with reference ID. Other sections stay.                                       |
| Error (all)       | Full-width `StateBlock`: "We couldn't reach the server. Your data is safe. Try again in a moment."                                                      |
| Offline           | Top banner "You're offline. Showing data from 9:42 am. Actions are paused." All buttons that change data are disabled with a tooltip giving the reason. |

## Interactions

- Every tile, card and row is a link (real `<a>`; middle-click opens a new tab).
- Keyboard: `g t` Today, `g a` Approvals, `g k` Tasks board (shortcuts listed under `?`).
- **Remind {n} now**: confirm dialog "Send a reminder now to 41 accounts (18 clients) for Jio Financial?" Note if in quiet hours: "It's quiet hours (9:30 pm – 8:00 am). Reminders will wait until 8:00 am." Buttons **Send reminders** · **Cancel**. Toast "Reminders queued for 18 clients."
- Bell: admin inbox of client actions (blueprint section 4), unread count.

## Copy

| Element        | Copy                                                                          |
| -------------- | ----------------------------------------------------------------------------- |
| Page title     | "Today"                                                                       |
| Tile labels    | "Overdue" · "Approvals" · "Allotments to enter" · "Listing today" · "You owe" |
| Empty tile     | "None"                                                                        |
| IPO counts     | "{a} applied · {t} to do · {m} missed" + "{x} not needed" when > 0            |
| Payments empty | "No payments due or overdue."                                                 |
| FY widget      | "FY 2026–27 · You earned" · "Q3 (Oct–Dec)"                                    |

## Accessibility

- Landmarks: `nav` (sidebar), `main`, sections with `h2` headings. Skip link "Skip to main content".
- Tiles: each a link with full name, e.g. "Overdue: 3 payments, rupees 1,04,040".
- The progress bar has `role="img"` with `aria-label="212 of 260 applied, 41 to do, 7 missed"`; counts also present as text.
- Focus ring: 2 px `brand.500`, 2 px offset, visible on every interactive element.
- Updates from polling are not announced (no live region) except new overdue items, announced politely once.
- Works at 200% browser zoom (falls to the 1024 layout).

## Platforms

Desktop browsers (Chrome, Edge, Safari) are primary. Installed admin PWA on the owner's Android or iPhone is secondary: single column, push alerts open the related item, safe-area padding on iPhone. No Android app for admin.
