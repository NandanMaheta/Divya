# Admin: Tasks board

Status: design proposal (Stage 1). Tokens and components: `../design-direction.md`. Admin build pattern: `.claude/skills/admin-page-pattern`. Rules: BR-TASK-01 (one task per account per APPLY IPO, 1 lot, UPI ID and windows), BR-TASK-02 (MISSED, later window reopens, final after close), BR-TASK-03 (reminder intensity, quiet hours, grouping), BR-DEAL-03/04 (deal shown; locked once applied). Blueprint section 4 "Tasks board".

## Purpose

For one IPO, see every family account's apply task at once, spot who hasn't applied, and act in bulk: remind, change UPI, add a window, excuse. Designed for 100 clients × up to 11 accounts (≈ 1,000 rows) with keyboard speed.

## Layout (desktop ≥ 1280 px)

```
┌────────────┬──────────────────────────────────────────────────────────────────────────────────┐
│ sidebar    │ Tasks board                                                                      │  title 24/600
│            │ [ Jio Financial ▾ ]  Window: [ Day 1 · Today 10:00 am – 5:00 pm IST ▾ ]  + Add window │
│            │ Closes in 2 h 15 min                                       Updated 9:42 am IST    │
│            │                                                                                  │
│            │ ( All 260 ) ( ◉ To do 41 ) ( ✓ Applied 212 ) ( ⚠ Missed 7 ) ( ⊖ Not needed 0 )    │  filter chips = counts
│            │ 🔍 Search client, account or PAN last 4      Deal: [Any ▾]  UPI: [Any ▾]          │
│            │ ┌──────────────────────────────────────────────────────────────────────────────┐ │
│            │ │☐│Client       │Account        │Deal              │UPI ID        │Lots│Status    │Last reminder│Applied   │ │  header: surface.sunken, bodySm 600
│            │ ├─┼─────────────┼───────────────┼──────────────────┼──────────────┼────┼──────────┼─────────────┼──────────┤ │
│            │ │☑│Ramesh Shah  │Rajesh ••••4F  │Fixed ₹500 ⏳     │swastik@okhdfc  │ 1  │◉ To do   │9:00 am push │—         │ │  row 44 px, bodySm
│            │ │☑│Ramesh Shah  │Meena ••••7Q   │Profit share 30%  │swastik@okhdfc  │ 1  │◉ To do   │9:00 am push │—         │ │
│            │ │☐│Priya Mehta  │You ••••2K     │Profit share 25%  │swastik2@oksbi  │ 1  │✓ Applied │—            │11:42 am  │ │
│            │ │☐│Anil Patel   │Kiran ••••9D   │Fixed ₹450 ✓      │swastik@okhdfc  │ 1  │⚠ Missed  │8:00 am push │—         │ │
│            │ └──────────────────────────────────────────────────────────────────────────────┘ │
│            │ ┌──────────────────────────────────────────────────────────────────────────────┐ │  bulk bar, appears on selection
│            │ │ 2 selected (1 client)  [Send reminder now] [Change UPI] [Add window] [Excuse] │ │  sticky bottom, elevation 2
│            │ └──────────────────────────────────────────────────────────────────────────────┘ │
└────────────┴──────────────────────────────────────────────────────────────────────────────────┘
```

Rows are grouped visually by client (a 1 px `line.subtle` divider and the client name shown only on the first row of a group; repeated in the accessible name). Default sort: status (Missed, To do, Applied, Not needed), then client name.

## Components

IPO select, window select, filter chips with counts, search, column filters, data table (TanStack Table, virtualised over 100 rows), `StatusChip` (admin size), deal cell (label + approval icon: `hourglass` waiting, `circle-check` approved), sticky bulk action bar, confirm dialogs, side drawer (row detail), toast, `StateBlock`.

## Columns

| Column        | Content                                                                    | Notes                                                                                                         |
| ------------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| Select        | Checkbox                                                                   | Header checkbox selects all **filtered** rows, with "Select all 41 to do" link when only the page is selected |
| Client        | Main member name                                                           | Sortable                                                                                                      |
| Account       | Name + masked PAN `••••4F`                                                 | Main member's own account shows "You" in the client app and "(self)" here                                     |
| Deal          | "Profit share 30%" / "Fixed ₹500" + approval icon; lock icon after Applied | Click opens Deals for this account                                                                            |
| UPI ID        | Assigned UPI                                                               | Editable in bulk only                                                                                         |
| Lots          | Always 1 (BR-TASK-01)                                                      | Kept visible for confidence; narrow                                                                           |
| Status        | `StatusChip`: To do / Applied / Missed / Not needed                        | Missed rows show "Reopens {window}" in caption under the chip if a later window exists                        |
| Last reminder | time + channel (push / email)                                              | "—" if none                                                                                                   |
| Applied       | time IST                                                                   | "—" if not applied                                                                                            |

## Bulk actions

| Action                | Dialog                                                                                                                                                                                                                                                                                | Result                                                                                       |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **Send reminder now** | "Send a reminder now to {n} accounts ({c} clients)?" Shows grouping note "Each client gets one message listing their accounts." Quiet-hours warning when applicable (BR-TASK-03). Disabled for rows already Applied or Not needed (count shown: "3 already applied will be skipped"). | Toast "Reminders queued for {c} clients." Last reminder column updates.                      |
| **Change UPI**        | Select from Settings UPI IDs; preview "{n} accounts will use swastik2@oksbi."                                                                                                                                                                                                         | Clients see the new UPI on their task; a gentle update notification (per notification spec). |
| **Add window**        | Date, start time, end time (IST); applies to selected accounts or "everyone not applied". Validates: inside IPO open–close dates, no overlap.                                                                                                                                         | Missed tasks in scope show "Reopens …"; clients notified per reminder rules.                 |
| **Excuse**            | Required reason (shown to client? see open question). "Excuse {n} accounts from applying for Jio Financial?"                                                                                                                                                                          | Status → grey "Not needed"; reminders stop.                                                  |

All bulk dialogs: title states the action and count; primary button repeats the verb ("Send reminders", "Change UPI", "Add window", "Excuse 2 accounts"); Escape cancels. Each action writes to the audit log (shown in row drawer history).

## Row drawer (click a row or press Enter)

Right drawer 420 px: account, PAN masked, client contact (tap-to-copy mobile), deal with snapshot and history, task timeline (created, reminders sent with channel, applied/missed), notes. Actions for this one row: Send reminder, Change UPI, Excuse, and **Mark applied on behalf** (see open question 2).

## States

| State                       | What shows                                                                                                                                                                                                                                                           |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Loading                     | Header controls render; table shows 10 skeleton rows; counts show "–".                                                                                                                                                                                               |
| No IPO selected / none open | `StateBlock`: "No IPO is open for applying. Choose an IPO to see its tasks." with the IPO select focused.                                                                                                                                                            |
| Empty filter                | In table: "No accounts match these filters." **Clear filters**                                                                                                                                                                                                       |
| IPO is DO NOT APPLY         | "Jio Financial is marked Do not apply, so it has no tasks." Link to IPO.                                                                                                                                                                                             |
| Error (load)                | Inline error card: "We couldn't load tasks for Jio Financial. [Try again]" + reference ID.                                                                                                                                                                           |
| Error (bulk action)         | Dialog stays open with message: "Reminders weren't sent. Nothing changed. Try again." Partial success is reported per row: "38 sent, 3 failed (show)".                                                                                                               |
| Offline                     | Banner "You're offline. Showing data from 9:42 am. Actions are paused." Bulk bar buttons disabled with tooltip.                                                                                                                                                      |
| Live update                 | Refetch about every 30 s while visible (plan decides mechanism). Changed rows flash `brand.50` for 1 s (no flash with reduced motion); selection and scroll are preserved; sort does not reshuffle rows under the cursor until the user re-sorts or after 10 s idle. |

## Interactions and keyboard

- Arrow keys move between cells; Space toggles the row; Shift+Space or Shift+click selects a range; Ctrl/Cmd+A selects all filtered rows; Enter opens the drawer; Escape clears selection / closes drawer.
- `/` focuses search. `1`–`5` switch status filter chips.
- Filter state lives in the URL (shareable, back button works).
- Window select switches the board between windows (Day 1, Day 2); "All windows" shows the latest status per account.

## Copy

| Element        | Copy                                                         |
| -------------- | ------------------------------------------------------------ |
| Filters        | "All" · "To do" · "Applied" · "Missed" · "Not needed"        |
| Bulk bar       | "{n} selected ({c} clients)"                                 |
| Buttons        | "Send reminder now" · "Change UPI" · "Add window" · "Excuse" |
| Missed caption | "Reopens Mon, 17 Nov, 10:00 am" · final: "Final: IPO closed" |
| Empty filter   | "No accounts match these filters."                           |

## Accessibility

- Table uses `role="grid"` with `aria-rowcount` for virtualisation, `aria-selected` on rows, `aria-sort` on headers.
- Checkboxes have names: "Select Rajesh, Ramesh Shah".
- Status chips have text; the stacked colours are never the only signal.
- Bulk bar announces "2 selected" politely when selection changes.
- Dialogs: focus trapped, initial focus on the least destructive control for Excuse, on the primary for others; focus returns to the bulk bar.
- Row height 44 px meets the admin target minimum; checkbox hit area is the whole cell.

## Platforms

Desktop first (≥ 1280 px; 1024 px hides Last reminder and Lots columns behind a "Columns" menu). On the owner's phone (admin PWA) the board becomes a read-mostly list grouped by client with status chips and a single **Remind all to do** action; bulk UPI and window changes are desktop-only.

## Open questions (owner)

1. Is the Excuse reason shown to the client, or internal only? Default: internal only; the client sees "Not needed".
2. Can the operator mark an account Applied on the client's behalf (e.g. client applied but phoned instead of tapping)? Not covered by the rules; the drawer action is shown as a placeholder only and must be confirmed (it would lock the deal under BR-DEAL-04).
