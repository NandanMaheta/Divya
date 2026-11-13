# Client: Payments

Status: design proposal (Stage 1). Tokens and components: `../design-direction.md`. Rules: BR-SET-01..09 (formulas, rounding, one running balance per main member, due date, estimates), BR-PAY-01..03. Blueprint 2.3 "What the client sees", 2.4. Golden scenarios GS-01, GS-02, GS-03 are the reference examples below.

## Purpose

Show, in one glance, **how much moves, in which direction, by when**, and how every rupee was worked out. Let the client pay by UPI, tell us they paid, or confirm they received our payment.

All amounts come from the API (settlement engine). The client app never computes money; it only formats paise with `formatRupees`.

## Wireframe (360 dp, light mode, GS-01: owes ₹86,580)

```
┌──────────────────────────────────────┐
│ Payments                      🔔     │
├──────────────────────────────────────┤
│ ┌──────────────────────────────────┐ │  Balance card, radius xl
│ │ You pay                          │ │  bodyLg 600, ink.600
│ │ ₹86,580                          │ │  amount 40/700, ink.900, tabular
│ │ ◉ Due Tue, 18 Nov, 11:59 pm      │ │  amber chip
│ │ Due in 2 days                    │ │  Countdown
│ │ ┌──────────────────────────────┐ │ │
│ │ │        Pay via UPI           │ │ │  primary 52
│ │ └──────────────────────────────┘ │ │
│ │ ┌──────────────────────────────┐ │ │
│ │ │       I've paid ...          │ │ │  secondary (outlined)
│ │ └──────────────────────────────┘ │ │
│ │ UPI ID  divya@okhdfc     [Copy]  │ │  CopyField
│ │ Amount  ₹86,580          [Copy]  │ │  CopyField (copies digits only: 86580)
│ └──────────────────────────────────┘ │
│                                      │
│ Jio Financial · Listed 14 Nov        │  heading + caption
│ 5 accounts · ₹86,580                 │  bodyLg
│ ┌──────────────────────────────────┐ │  BreakdownCard (collapsed rows expand on tap)
│ │ Rajesh · Fixed ₹500   ✓ Allotted │ │
│ │ ┌──────────────────────────────┐ │ │  well: surface.sunken, radius md
│ │ │ Investment           ₹15,000 │ │ │  bodyLg, label ink.600, amount ink.900 right
│ │ │ + Profit              ₹3,000 │ │ │
│ │ │ − Your share            ₹500 │ │ │
│ │ │ − Brokerage              ₹40 │ │ │
│ │ │ ──────────────────────────── │ │ │  line.strong rule
│ │ │ To pay              ₹17,460  │ │ │  heading 20/600 (card hero) right
│ │ └──────────────────────────────┘ │ │
│ └──────────────────────────────────┘ │
│ ┌──────────────────────────────────┐ │
│ │ Meena · Profit share 30%  ✓ Allot│ │
│ │ Investment ₹15,000 · + Profit    │ │
│ │ ₹3,000 · − Your share ₹900       │ │  (expanded identically; shown short here)
│ │ To pay ₹17,100                   │ │
│ └──────────────────────────────────┘ │
│ … 3 more accounts                    │
│                                      │
│ History                            › │  list of past payments and settlements
├──────────────────────────────────────┤
│  ⌂ Home  ☑ Tasks  ↗ IPOs  ₹ Pay  ⋯   │
└──────────────────────────────────────┘
```

## Balance card variants

The balance is one running total per main member across all accounts and IPOs (BR-SET-07).

| Situation                                     | Label                                      | Amount tone | Chip                                                                   | Actions                                                           |
| --------------------------------------------- | ------------------------------------------ | ----------- | ---------------------------------------------------------------------- | ----------------------------------------------------------------- |
| Client owes, not yet due                      | "You pay"                                  | ink.900     | amber "Due Tue, 18 Nov, 11:59 pm"                                      | Pay via UPI · I've paid                                           |
| Client owes, due today                        | "You pay"                                  | ink.900     | amber "Due today, 11:59 pm" + Countdown                                | same                                                              |
| Overdue (BR-PAY-03)                           | "You pay"                                  | ink.900     | red `circle-alert` "Overdue since Tue, 18 Nov"                         | same                                                              |
| Client claimed payment, awaiting confirmation | "You paid"                                 | ink.900     | blue "Waiting for us to confirm"                                       | none (History shows claim)                                        |
| Partial payment received                      | "You pay" + line "Paid ₹50,000 of ₹86,580" | ink.900     | per due state                                                          | same                                                              |
| We owe the client (GS-02)                     | "We pay you"                               | ink.900     | blue "We'll pay by {date}" if a date exists, else "We'll pay you soon" | none                                                              |
| We marked it paid (BR-PAY-02)                 | "We paid you" + "UTR 4321…8890"            | ink.900     | blue "Please confirm"                                                  | **Yes, I received it** (primary) · "I haven't received it" (text) |
| Nothing owed either way                       | "All settled"                              | —           | green "Settled"                                                        | none                                                              |
| Before listing (estimate, BR-SET-09)          | "You'll pay about"                         | ink.900     | blue "Estimate"                                                        | none; caption "Final after listing on {date}"                     |

Amounts are never shown red or green. Direction is the word; status is the chip.

## BreakdownCard

Order is fixed: **Investment + Profit − Your share − Brokerage = To pay**. Labels left, amounts right, tabular, `bodyLg`. Minus signs are U+2212. Result row uses `heading` and the direction word.

| Case (rule)                                                   | Rows                                                                                        | Result                                        |
| ------------------------------------------------------------- | ------------------------------------------------------------------------------------------- | --------------------------------------------- |
| Profit share, allotted, profit (BR-SET-02)                    | Investment ₹15,000 · + Profit ₹3,000 · − Your share (30%) ₹900                              | **To pay ₹17,100**                            |
| Fixed, allotted, profit (BR-SET-05)                           | Investment ₹15,000 · + Profit ₹3,000 · − Your share (fixed) ₹500 · − Brokerage ₹40          | **To pay ₹17,460**                            |
| Fixed, allotted, loss (BR-SET-06, GS-03)                      | Investment ₹15,000 · − Loss ₹3,000 · − Your share (fixed) ₹500 · Brokerage "None on a loss" | **To pay ₹11,500**                            |
| Profit share, allotted, loss (BR-SET-03)                      | Investment ₹15,000 · − Loss ₹3,000 · Your share "None, no profit"                           | **To pay ₹12,000** · note "The loss is ours." |
| Fixed, not allotted (BR-SET-04)                               | Single line "Not allotted" (grey chip) · Your share (fixed) ₹500                            | **We pay you ₹500**                           |
| Profit share, not allotted (BR-SET-01)                        | Single line "Not allotted"                                                                  | **Nothing to pay**                            |
| Fixed, loss larger than investment value (BR-SET-06 negative) | as loss case                                                                                | **We pay you ₹{abs}**                         |

- Profit share rows omit Brokerage (it never applies).
- "Profit" is labelled "Loss" when G < 0, and the sign flips to "−".
- Estimate: every amount except Investment is prefixed "about"; the card header carries the blue "Estimate" chip and caption "Based on today's expected profit, updated 2:15 pm".
- Each card header: `AccountName`, deal label ("Fixed ₹500" / "Profit share 30%"), allotment chip (green "Allotted · 14 shares" / grey "Not allotted").
- Settled accounts collapse to one line with a green "Settled" chip.

GS-01 group total: 3 × ₹17,460 + 2 × ₹17,100 = **₹86,580** (shown in the IPO group header, not recomputed in the client).

## Interactions

1. **Pay via UPI** (tap 1 from Payments, tap 2 from Home's Do-now): opens a UPI app with payee, amount and reference prefilled (`upi://pay?pa=…&am=…&tn=…`). Returning to the app shows a prompt card: "Did the payment go through?" **Yes, I've paid** · **Not yet**.
2. **I've paid** opens a sheet (BR-PAY-01):
   - Amount (prefilled with the balance; editable for partial payments, numeric keypad, Indian grouping as you type)
   - UTR / reference (optional, 12 digits hint)
   - Screenshot (optional; picker)
   - Button **Send to Divya**. Result: chip turns blue "Waiting for us to confirm"; toast "Thanks. We'll confirm when it reaches our bank."
3. **Yes, I received it** (BR-PAY-02): one tap, confirm sheet "Confirm you received ₹1,500?" **Yes, received** · **Not yet**. Result: green "Settled".
4. **I haven't received it**: opens Message us with the payment reference prefilled.
5. Tap a BreakdownCard header to expand/collapse. Default: expanded when ≤ 2 accounts, else collapsed with the result row visible.
6. History: list of claims, confirmations, payouts and corrections (BR-SET-10 corrections show as "Corrected: listing price updated, new amount ₹…" with old amount struck through and labelled "was").

## States

| State                       | What shows                                                                                                                                                                                                                                                                          |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Loading                     | Skeleton balance card (big number block) + 2 breakdown cards.                                                                                                                                                                                                                       |
| Empty (no settlements ever) | `StateBlock`: "Nothing to pay or receive yet. After an IPO lists, your amounts will show here."                                                                                                                                                                                     |
| Error                       | `StateBlock` error: "We couldn't load your payments. Check your internet and try again." **Try again**                                                                                                                                                                              |
| Error (claim fails)         | Sheet stays with inputs kept: "That didn't go through. Nothing was sent. Please try again."                                                                                                                                                                                         |
| Offline                     | `OfflineBanner`; cached amounts with "Last updated 2:15 pm"; Pay via UPI and Copy still work (they don't need our server) but show the note "Amounts may have changed since {time}". **I've paid** and **Yes, I received it** are disabled: "Connect to the internet to send this." |

## Copy

| Element                                          | Copy                                                                                            |
| ------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| Directions                                       | "You pay" · "We pay you" · "You paid" · "We paid you" · "All settled" · "Nothing to pay"        |
| Due                                              | "Due Tue, 18 Nov, 11:59 pm" · "Due today, 11:59 pm" · "Overdue since Tue, 18 Nov"               |
| Buttons                                          | "Pay via UPI" · "I've paid" · "Send to Divya" · "Yes, I received it" · "I haven't received it"  |
| Breakdown labels                                 | "Investment" · "+ Profit" / "− Loss" · "− Your share" · "− Brokerage" · "To pay" / "We pay you" |
| Estimate caption                                 | "Based on today's expected profit, updated {time}. Final after listing."                        |
| Final notice (after listing, top of screen once) | "Jio Financial listed at ₹{price}. Your amounts are now final."                                 |

## Accessibility

- Balance card reads: "You pay rupees 86,580. Due Tuesday 18 November, 11:59 pm, in 2 days."
- Breakdown is a table semantically (web: `<table>` with row headers; RN: each row one element "Minus your share, rupees 500"). Signs are spoken as words ("plus", "minus") through the `Amount` helper.
- Copy buttons: "Copy UPI ID", "Copy amount".
- At 200% text, label and amount stack in each row (label above, amount right-aligned below).

## Android vs iPhone PWA

|                  | Android                                                            | iPhone PWA                                                                                                                                                                                                                          |
| ---------------- | ------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Pay via UPI      | `upi://` intent opens the app chooser (GPay, PhonePe, Paytm, BHIM) | `upi://` support is inconsistent: show the button, plus the always-visible UPI ID and Amount Copy fields as the reliable path; after 2 s without leaving the page show "Didn't open? Copy the UPI ID and amount into your UPI app." |
| Return detection | `AppState` change triggers the "Did the payment go through?" card  | `visibilitychange` triggers the same card                                                                                                                                                                                           |
| Screenshot       | Image picker (`expo-image-picker`)                                 | `<input type="file" accept="image/*">`                                                                                                                                                                                              |

## Open questions (owner)

1. Partial payments: the default "Partial payments reduce the balance" implies the client can enter a smaller amount in "I've paid". Confirm the amount field is editable by the client.
2. When we owe the client, is there a promised payout date to show? Default copy is "We'll pay you soon" when none exists.
