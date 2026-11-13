# Client: Home

Status: design proposal (Stage 1). Tokens and components: `../design-direction.md`. Rules: BR-TASK-01..03, BR-SET-08/09, BR-PAY-01..03, BR-DEAL-03. Blueprint section 3 "Home".

## Purpose

Answer one question in under three seconds: **"Do I need to do anything right now?"** Show the single most urgent thing as a big Do-now card, then a short summary of tasks, money and the latest allotment. Everything urgent is at most 2 taps from here.

## Wireframe (360 dp wide, light mode)

```
┌──────────────────────────────────────┐
│ Divya                         🔔 2   │  header: wordmark (brand.600), bell with unread count
├──────────────────────────────────────┤
│ Good morning, Ramesh                 │  display 32/700, ink.900
│ Fri, 14 Nov                          │  bodyLg, ink.600
│                                      │
│ ┌──────────────────────────────────┐ │  festival banner (only on scheduled days)
│ │ ✦ Happy Diwali from Divya        │ │  surface.base, gold hairline + motif, ink.900 text
│ └──────────────────────────────────┘ │
│                                      │
│ DO NOW                               │  caption 13/500, ink.600 (the only caps label)
│ ┃┌────────────────────────────────┐  │  DoNowCard, amber left rule
│ ┃│ ◉ To do                        │  │  StatusChip
│ ┃│ Apply for Jio Financial        │  │  heading 20/600
│ ┃│ 3 accounts still to apply      │  │  bodyLg
│ ┃│ Closes in 2 h 15 min           │  │  Countdown, amber bold
│ ┃│ Today, 5:00 pm                 │  │  caption, ink.600
│ ┃│ ┌────────────────────────────┐ │  │
│ ┃│ │       Open tasks (3)       │ │  │  primary button, 52 tall, brand.600
│ ┃│ └────────────────────────────┘ │  │
│ ┃└────────────────────────────────┘  │
│                                      │
│ ┌──────────┐┌──────────┐┌──────────┐ │  summary tiles (3 across ≥ 360 dp, wrap 2+1 below)
│ │ Tasks    ││ To pay   ││ Allotment│ │  caption label
│ │ 3 to do  ││ ₹86,580  ││ 2 of 5   │ │  heading, tabular
│ │          ││ by Tue   ││ Jio Fin. │ │  caption
│ └──────────┘└──────────┘└──────────┘ │
│                                      │
│ Next up                              │  heading
│ ○ Tata Tech opens Mon, 17 Nov  ›     │  list rows, 56 dp, chevron
│ ○ Fixed deal ₹500 for Swiggy   ›     │
├──────────────────────────────────────┤
│  ⌂ Home  ☑ Tasks  ↗ IPOs  ₹ Pay  ⋯   │  bottom tabs, 64 dp + safe area
└──────────────────────────────────────┘
```

## Components

`DoNowCard`, `StatusChip`, `Countdown`, `Amount`, `StateBlock`, `OfflineBanner`, summary tile (a `Pressable` card with label, value, sub-line), festival banner, bottom tab bar with bell in the header.

## Content rules

### Do-now priority (proposed; owner to confirm)

The card shows the **first** match in this order. If several items share a level, the earliest deadline wins.

| #   | Situation                                                                  | Tone  | Title                                                                           | Button (1st tap)                                |
| --- | -------------------------------------------------------------------------- | ----- | ------------------------------------------------------------------------------- | ----------------------------------------------- |
| 1   | A payment is overdue (BR-PAY-03)                                           | red   | "Payment overdue" · "You pay ₹86,580" · "Was due Tue, 18 Nov"                   | **Pay now** → Payments                          |
| 2   | Apply window closes today and accounts are not applied (urgent level, 7.2) | amber | "Apply for Jio Financial" · "3 accounts still to apply" · Countdown             | **Open tasks (3)** → Tasks filtered to that IPO |
| 3   | Payment due today or tomorrow                                              | amber | "Payment due" · "You pay ₹86,580" · Countdown to 11:59 pm                       | **Pay now**                                     |
| 4   | Listing day: sell instructions                                             | amber | "Sell 14 shares of Jio Financial at listing (10:00 am)" · "In Rajesh's account" | **See details** → Allotment                     |
| 5   | Open window, not closing today                                             | amber | "Apply for Tata Tech" · "Window: Today 10:00 am – 5:00 pm"                      | **Open tasks**                                  |
| 6   | We owe the client and marked it paid (BR-PAY-02)                           | blue  | "We paid you ₹1,500" · "Please check your bank and confirm"                     | **Confirm received** → Payments                 |
| 7   | Nothing to do                                                              | green | "You're all set" · "No tasks or payments right now."                            | none                                            |

When only **one** account has a to-do task, item 2/5's button is **Mark applied** and opens the `ConfirmSheet` directly (tap 2). With several accounts it opens Tasks, where each Mark applied is tap 2.

### Summary tiles

| Tile                          | Value                                                                                                                      | Sub-line                          | Tap goes to            |
| ----------------------------- | -------------------------------------------------------------------------------------------------------------------------- | --------------------------------- | ---------------------- |
| Tasks                         | "3 to do" (amber icon) or "All done" (green icon)                                                                          | "1 missed" in red if any          | Tasks                  |
| To pay / To receive / Settled | `DirectionAmount` short form: "₹86,580", sub "You pay · by Tue"; or "₹1,500", sub "We pay you"; or "₹0", sub "All settled" | "Estimate" chip if before listing | Payments               |
| Allotment                     | "2 of 5" allotted for the latest IPO                                                                                       | IPO short name                    | Allotment for that IPO |

Tiles hide when they have nothing to say for a new client (no IPO history yet); the row then shows only Tasks.

## States

| State                           | What shows                                                                                                                                                                                           |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Loading (first)                 | Greeting renders immediately from the cached profile. Skeleton of Do-now card (one tall block) and three tile blocks on `surface.sunken`, pulsing. Screen reader: "Loading your home".               |
| Refresh                         | Pull to refresh; cached content stays. "Updated 2:15 pm" in caption under the date after a refresh.                                                                                                  |
| Empty (new client, nothing yet) | Do-now card in green "You're all set". Below it a `StateBlock`: "When we pick an IPO for you, your tasks will show here. We'll also send you a reminder."                                            |
| Error                           | Greeting stays. In place of the Do-now card: `StateBlock` error "We couldn't load your tasks and payments. Check your internet and try again." **Try again**. Reference ID in caption.               |
| Partial error                   | If only one source fails (e.g. payments), that tile shows "Couldn't load" + retry icon button (label "Retry payments"); the rest renders.                                                            |
| Offline                         | `OfflineBanner`. Cached Do-now card shown with "Last updated 2:15 pm". Its button still navigates (Tasks and Payments work from cache); actions that change data are disabled there with the reason. |

## Interactions

- Tap Do-now button: as in the priority table. Tap anywhere else on the card does the same (one large target).
- Tap bell: Inbox. Unread badge shows a number up to 9, then "9+".
- Festival banner: tap opens the greeting full screen (decorative); has a Close (×) icon button "Close greeting" that hides it for the day.
- Pull to refresh on the whole screen.
- Countdown: per-minute update; when it hits zero the card re-evaluates priority (the task becomes Missed or the next item takes over) with a 200 ms cross-fade.

## Copy

| Element       | Copy                                                                                                                                           |
| ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| Greeting      | "Good morning, {firstName}" (5:00 am–11:59 am) · "Good afternoon, {firstName}" (12:00–4:59 pm) · "Good evening, {firstName}" (5:00 pm onwards) |
| Section label | "Do now" · "Next up"                                                                                                                           |
| All set       | Title "You're all set" · Body "No tasks or payments right now."                                                                                |
| Overdue       | Title "Payment overdue" · Body "You pay {amount}. It was due {date}." · Button "Pay now"                                                       |
| Apply         | Title "Apply for {ipo}" · Body "{n} accounts still to apply" / "Rajesh still to apply" · Button "Open tasks ({n})" / "Mark applied"            |
| We paid       | Title "We paid you {amount}" · Body "Please check your bank and confirm." · Button "Confirm received"                                          |
| Error         | "We couldn't load your tasks and payments. Check your internet and try again." · "Try again"                                                   |
| Offline       | "You're offline. Showing what we saved at {time}."                                                                                             |

## Accessibility

- Reading order: greeting, banner, Do-now card, tiles, Next up. The Do-now card is one accessible element: "Do now. To do. Apply for Jio Financial. 3 accounts still to apply. Window closes at 5:00 pm today, in 2 hours 15 minutes. Button, Open tasks."
- Tiles: role button, label combines label + value + sub-line ("To pay, rupees 86,580, due Tuesday 18 November").
- Bell: "Inbox, 2 unread".
- At 200% text, tiles stack one per row; the Do-now amount never shrinks below `amount` × `amountMaxFontScale`.
- Dark mode: card left rule uses `tone.*` dark values; festival motif uses dark `gold`.

## Android vs iPhone PWA

|               | Android                                                                                                      | iPhone PWA                                                                                                                                                                      |
| ------------- | ------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Header        | Status bar tinted `surface.muted`, dark/light icons per scheme                                               | `apple-mobile-web-app-status-bar-style` = `default`; top safe-area padding                                                                                                      |
| Back          | System back on Home asks nothing; it leaves the app (standard)                                               | No back on Home                                                                                                                                                                 |
| Push prompt   | Asked after first login, on the Do-now context ("Get reminders when an IPO opens?")                          | If opened in Safari (not installed): a dismissible card above Do-now: "Add Divya to your Home Screen to get reminders" → guide sheet. Push permission asked only after install. |
| Battery guide | One-time card on Xiaomi/Vivo/Oppo/Realme: "Allow Divya to run in the background so reminders arrive" → guide | n/a                                                                                                                                                                             |

## Open questions (owner)

1. Confirm the Do-now priority order above (especially overdue payment above a closing-today apply window).
