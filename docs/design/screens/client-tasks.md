# Client: Tasks

Status: design proposal (Stage 1). Tokens and components: `../design-direction.md`. Rules: BR-TASK-01 (one task per account per APPLY IPO, 1 lot, UPI ID and window set by admin), BR-TASK-02 (MISSED, reopens in a later window, final after close), BR-TASK-03 (reminders stop when done), BR-DEAL-04 (deal locks when Applied). Blueprint section 3 "Tasks".

## Purpose

The screen where most taps happen. For each family account and each APPLY IPO, show exactly what to do (amount, UPI ID, window), and let the client confirm "I applied" in two taps or fewer.

## Wireframe (360 dp, light mode)

```
┌──────────────────────────────────────┐
│ Tasks                         🔔     │  title 24/600
│ ┌─────────┬─────────┬─────────┐      │  segmented filter, 48 dp
│ │ To do 3 │ Done 4  │  All    │      │
│ └─────────┴─────────┴─────────┘      │
├──────────────────────────────────────┤
│ Jio Financial                        │  IPO group header, heading 20/600
│ Closes in 2 h 15 min · Today 5:00 pm │  Countdown (amber bold) + caption
│ UPI ID  swastik@okhdfc       [Copy]    │  CopyField, shared by the group when all rows use it
│                                      │
│ ┌──────────────────────────────────┐ │  TaskCard
│ │ Rajesh · PAN ••••4F   ◉ To do    │ │  AccountName (bodyLg 600) + StatusChip
│ │ 1 lot (14 shares) · ₹14,850      │ │  bodyLg, Amount tabular
│ │ Window: Today 10:00 am – 5:00 pm │ │  body, ink.600
│ │ Deal: Fixed ₹500 · Waiting       │ │  body; links to deal pick
│ │ ┌──────────────────────────────┐ │ │
│ │ │        Mark applied          │ │ │  primary, 52 tall
│ │ └──────────────────────────────┘ │ │
│ └──────────────────────────────────┘ │
│ ┌──────────────────────────────────┐ │
│ │ You · PAN ••••2K     ✓ Applied   │ │  green chip, no button
│ │ Applied today, 11:42 am          │ │  caption
│ │ Deal: Profit share 30% · Locked  │ │
│ └──────────────────────────────────┘ │
│ ┌──────────────────────────────────┐ │
│ │ Meena · PAN ••••7Q   ⚠ Missed    │ │  red chip
│ │ Next window: Mon, 17 Nov,        │ │
│ │ 10:00 am – 5:00 pm               │ │  bodyLg, ink.900
│ └──────────────────────────────────┘ │
├──────────────────────────────────────┤
│ ┌──────────────────────────────────┐ │  StickyActionBar (only when ≥ 2 to-do in view)
│ │   Mark all applied (2) ...       │ │  secondary (outlined brand) button
│ └──────────────────────────────────┘ │
│  ⌂ Home  ☑ Tasks  ↗ IPOs  ₹ Pay  ⋯   │
└──────────────────────────────────────┘
```

## Components

Segmented filter, IPO group header, `Countdown`, `CopyField`, `TaskCard`, `StatusChip`, `AccountName`, `Amount`, `StickyActionBar`, `ConfirmSheet`, `StateBlock`, `OfflineBanner`, toast.

## TaskCard states

| Task state                    | Chip                             | Body                                                  | Action                                                     |
| ----------------------------- | -------------------------------- | ----------------------------------------------------- | ---------------------------------------------------------- |
| To do, window open            | amber `circle-dot` "To do"       | lot, amount, window, deal                             | **Mark applied** (primary)                                 |
| To do, window not open yet    | amber "To do"                    | "Window opens Mon, 17 Nov, 10:00 am"                  | See open question 1                                        |
| Applied                       | green `circle-check` "Applied"   | "Applied {relative time}"                             | none; card is quieter (`ink.600` body)                     |
| Missed, next window exists    | red `circle-alert` "Missed"      | "Next window: {date}, {range}"                        | none until the next window opens; then it returns to To do |
| Missed, final (IPO closed)    | red "Missed"                     | "This IPO has closed."                                | none                                                       |
| Not needed (excused by admin) | grey `circle-minus` "Not needed" | "{operator} marked this not needed." if a note exists | none                                                       |

UPI ID: shown once in the group header when all tasks in the IPO share it; otherwise inside each card. Amount = issue price × lot (shown as given by the API; no math in the client).

## Interactions

- **Mark applied (tap 1)** opens a `ConfirmSheet`:
  - Title "Did you apply for Rajesh?"
  - Body "Jio Financial · 1 lot · ₹14,850 · UPI swastik@okhdfc"
  - Note (`ink.600`): "After this, Rajesh's deal is locked and reminders stop."
  - Buttons: **Yes, I applied** (primary, tap 2) · **Not yet** (text button).
  - The sheet exists because marking applied locks the deal (BR-DEAL-04); it keeps the action at 2 taps.
- On success: chip cross-fades to green "Applied", tick draws (320 ms, Android haptic), card moves to the bottom of the group after 600 ms. Toast "Marked applied for Rajesh." No Undo (see open question 2).
- **Mark all applied (n)** opens a sheet listing every to-do account in that IPO with checkboxes (all ticked, 48 dp rows). Button "Mark {k} applied". Untick to exclude.
- Tap the "Deal: …" line: opens `client-deal-pick.md` for that IPO, scrolled to the account. Locked deals open read-only.
- Copy UPI ID: toast "UPI ID copied".
- Pull to refresh. Filter remembers last choice for the session; default is **To do** when there are to-do tasks, otherwise **All**.
- Deep links from push ("3 accounts still to apply for Jio") open this screen filtered to the IPO with To do selected.

## States

| State                         | What shows                                                                                                                                                                                                                           |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Loading                       | Filter renders; 3 skeleton TaskCards (chip, 3 lines, button block). Screen reader: "Loading tasks".                                                                                                                                  |
| Empty: no tasks at all        | `StateBlock`: "No tasks right now. We'll remind you when an IPO opens."                                                                                                                                                              |
| Empty: To do filter, all done | `StateBlock` with green tick: "All done. Nothing left to apply for." Link "See done tasks".                                                                                                                                          |
| Error (load)                  | `StateBlock` error: "We couldn't load your tasks. Check your internet and try again." **Try again**.                                                                                                                                 |
| Error (Mark applied fails)    | Sheet stays open, inline red message above buttons: "That didn't go through. Please try again." Button re-enabled. If the window closed meanwhile: "The window closed at 5:00 pm, so this is now missed. {Next window line if any}." |
| Offline                       | `OfflineBanner`; cached tasks visible with "Last updated 2:15 pm". **Mark applied** is disabled and its helper text reads "Connect to the internet to mark applied." (No offline queueing unless the feature spec adds it.)          |

## Copy

| Element          | Copy                                                                                |
| ---------------- | ----------------------------------------------------------------------------------- |
| Filters          | "To do" · "Done" · "All"                                                            |
| Lot line         | "1 lot ({shares} shares) · {amount}"                                                |
| Window           | "Window: Today 10:00 am – 5:00 pm" / "Window: Mon, 17 Nov, 10:00 am – 5:00 pm"      |
| Countdown        | "Closes in 2 h 15 min" · final minute "Closes in 45 sec"                            |
| Deal line        | "Deal: Profit share 30%" · "Deal: Fixed ₹500 · Waiting for approval" · "… · Locked" |
| Applied          | "Applied today, 11:42 am"                                                           |
| Missed with next | "Next window: Mon, 17 Nov, 10:00 am – 5:00 pm"                                      |
| Missed final     | "This IPO has closed."                                                              |
| Toast            | "Marked applied for {name}." · bulk "Marked applied for {k} accounts."              |

## Accessibility

- Each TaskCard is a group; its heading is the account name. Chip reads "Status: To do". Countdown label "Window closes at 5:00 pm today, in 2 hours 15 minutes".
- Mark applied button label includes the account: "Mark applied for Rajesh".
- The status change is announced politely: "Rajesh, applied".
- Sheet traps focus; focus returns to the card heading after close.
- 200% text: card lines wrap; the chip moves below the name.

## Android vs iPhone PWA

|                  | Android                           | iPhone PWA                                              |
| ---------------- | --------------------------------- | ------------------------------------------------------- |
| Sheet dismiss    | System back closes the sheet      | Swipe down or "Not yet"                                 |
| Haptics          | Light tick on applied and on copy | none                                                    |
| Copy             | `expo-clipboard`                  | `navigator.clipboard` (user gesture: yes)               |
| Notification tap | Opens the deep link               | Opens the PWA at the link if installed; otherwise Inbox |

## Open questions (owner)

1. Can a client mark a task applied **before** its window opens (e.g. applied early via their bank)? Default in this design: button is shown but disabled with "Window opens {time}".
2. Can a client **undo** "Mark applied" (e.g. tapped by mistake)? BR-DEAL-04 locks the deal at Applied and the rules list no undo, so this design has none and relies on the confirm sheet; the client would contact the operator.
