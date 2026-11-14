# Client: Choose your deal (per IPO)

Status: design proposal (Stage 1). Tokens and components: `../design-direction.md`. Explainer copy: `../copy/deals-explainer.md`. Rules: BR-DEAL-01 (default profit share at the client's p), BR-DEAL-02 (fixed offer is a quote with a validity window, may differ per client), BR-DEAL-03 (price frozen at Submit, goes to approval; reject reverts to profit share), BR-DEAL-04 (client can change only until Applied), BR-DEAL-05 (snapshot), BR-SET-05 (B). Blueprint 2.3.

## Purpose

For one IPO, let the main member choose **Profit share** or **Fixed ₹F** for each family account, understand what each means in rupees, and submit. Reached from the IPO detail "Deals" section, from the "Deal:" line on a task, or from a "Fixed deal available" notification.

## Wireframe (360 dp, light mode)

```
┌──────────────────────────────────────┐
│ ‹ Jio Financial                      │  back chevron + IPO name (iPhone needs the label)
│ Choose your deal                     │  title 24/600
│ ⓘ How deals work                  ›  │  row, opens explainer sheet
├──────────────────────────────────────┤
│ ┌──────────────────────────────────┐ │  Fixed offer card (only if an offer is live for this client)
│ │ Fixed deal: ₹500 per account     │ │  heading; ₹500 in amount weight
│ │ Price valid until 4:00 pm        │ │  bodyLg
│ │ Ends in 1 h 20 min               │ │  Countdown amber bold
│ └──────────────────────────────────┘ │
│                                      │
│ Rajesh · PAN ••••4F                  │  AccountName, bodyLg 600
│ ┌────────────────┬─────────────────┐ │  two large option tiles (radio group), 72 dp tall
│ │ ● Profit share │ ○ Fixed ₹500    │ │  selected = brand.50 fill + 2 px brand.600 border + check
│ │   You keep 30% │   Allotted or   │ │  body, ink.600
│ │   of profit    │   not           │ │
│ └────────────────┴─────────────────┘ │
│ With today's expected profit: about  │  caption, ink.600
│ ₹900 for you · Estimate              │  blue Estimate chip
│ ──────────────────────────────────── │
│ You · PAN ••••2K          🔒 Locked  │  grey lock icon + "Locked"
│ Profit share 30%                     │
│ Locked because you marked it applied.│  body ink.600
│ ──────────────────────────────────── │
│ Meena · PAN ••••7Q   ⏳ Waiting       │  blue chip
│ Fixed ₹500 · price locked 11:05 am   │
│ Waiting for approval                 │
├──────────────────────────────────────┤
│ ┌──────────────────────────────────┐ │  StickyActionBar
│ │   Submit 1 change                │ │  primary; disabled until something changed
│ └──────────────────────────────────┘ │
└──────────────────────────────────────┘
```

On ≥ 400 dp the option tiles stay side by side; below 360 dp or at large text they stack vertically (full-width tiles).

## Components

Fixed offer card, option tile (radio), `AccountName`, `StatusChip`, `Countdown`, `Amount`, estimate line, `StickyActionBar`, `ConfirmSheet`, explainer bottom sheet, `StateBlock`, `OfflineBanner`.

## Account row states

| State                           | Shows                                                                                                                  | Editable                                                            |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| Default (never picked)          | Profit share selected, caption "This is your default."                                                                 | yes                                                                 |
| Picked profit share             | Profit share selected                                                                                                  | yes                                                                 |
| Fixed, waiting for approval     | Blue "Waiting" chip, "Fixed ₹{F} · price locked {time}", "Waiting for approval"                                        | Can switch back to profit share until Applied (submitting a change) |
| Fixed, approved                 | Green "Approved" chip, "Fixed ₹{F}"                                                                                    | yes, until Applied                                                  |
| Fixed, rejected                 | Notice on the row in `toneSoft.grey`: "We couldn't accept the fixed deal for this account. It's on profit share {p}%." | yes, may pick fixed again if an offer is live                       |
| Locked (account marked Applied) | Grey lock + "Locked because you marked it applied." Current deal shown. Link "Need a change? Message us"               | no                                                                  |
| No fixed offer for this client  | Fixed tile absent; one line "Profit share {p}% · No fixed deal for this IPO right now."                                | no choice to make                                                   |

Only the deal choice is editable here; the per-client p is set by the operator.

## Interactions

1. Tap an option tile: selects it (1 tap). The sticky button updates to "Submit {n} change(s)".
2. **Submit** opens a `ConfirmSheet` summarising each change:
   - "Rajesh: Fixed ₹500 (price valid until 4:00 pm)"
   - Note: "Fixed deals need our approval. Until then the account shows Waiting."
   - Buttons **Submit** · **Go back**.
3. On submit, the server freezes the quote valid at that instant (BR-DEAL-03). Result:
   - Success: rows switch to "Waiting" (fixed) or plain profit share. Toast "Sent. We'll let you know when it's approved."
   - **Price changed** since the screen loaded (server returns a different frozen price or no valid quote): the sheet shows "The fixed price changed to ₹450 at 2:00 pm." with **Submit at ₹450** · **Keep profit share**. See open question 1.
   - Account became Applied meanwhile: row turns Locked, message "Rajesh was marked applied, so this deal is locked."
4. Countdown reaching zero: offer card shows "This price has ended." and Fixed tiles disable (unless a new quote arrives on refresh).
5. **How deals work** opens the explainer sheet with the client's own p, F and B filled in (copy in `../copy/deals-explainer.md`).
6. Leaving with unsubmitted changes: sheet "Leave without submitting? Your changes won't be saved." **Leave** · **Stay**.

## Estimate line

Under each editable row, show what the selected option is worth for one account at today's expected profit (BR-IPO-04, BR-SET-09), as returned by the API (no client-side math):

- Profit share: "With today's expected profit: about ₹900 for you · Estimate"
- Fixed: "₹500 for you, allotted or not" (no estimate; fixed is certain)
- When the expected profit is zero or negative: "Expected profit is ₹0 today, so profit share would give you nothing extra."

## States

| State          | What shows                                                                                                                                                                |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Loading        | Header renders; skeleton offer card and 3 account rows.                                                                                                                   |
| Empty          | Not reachable for APPLY IPOs (every account has a deal). For DO NOT APPLY IPOs the Deals entry is hidden.                                                                 |
| Error (load)   | `StateBlock` error: "We couldn't load the deals for {ipo}. Check your internet and try again."                                                                            |
| Error (submit) | Sheet stays, inline message: "That didn't go through. Your choice wasn't sent. Please try again."                                                                         |
| Offline        | `OfflineBanner`; current choices visible; tiles disabled; button reads "Connect to the internet to submit". The countdown keeps running from the cached valid-until time. |

## Copy

| Element           | Copy                                                                                      |
| ----------------- | ----------------------------------------------------------------------------------------- |
| Title             | "Choose your deal"                                                                        |
| Profit share tile | "Profit share" · "You keep {p}% of the listing profit" (short: "You keep {p}% of profit") |
| Fixed tile        | "Fixed ₹{F}" · "Yours, allotted or not"                                                   |
| Offer card        | "Fixed deal: ₹{F} per account" · "Price valid until {time}"                               |
| Default caption   | "This is your default."                                                                   |
| Locked            | "Locked because you marked it applied." · link "Need a change? Message us"                |
| Waiting           | "Waiting for approval"                                                                    |
| Submit            | "Submit 1 change" / "Submit {n} changes"                                                  |
| Toast             | "Sent. We'll let you know when it's approved." (profit-share-only changes: "Saved.")      |

## Accessibility

- Each account's tiles are a radio group named by the account ("Deal for Rajesh"). Tiles: role radio, state checked/disabled. Selected state shows check icon + border, not colour alone.
- Countdown label: "Fixed price valid until 4:00 pm, in 1 hour 20 minutes".
- Locked rows: the lock is in text, not only icon.
- Explainer sheet: headings are real headings; examples are a list, amounts with full rupee labels.

## Android vs iPhone PWA

|                                     | Android                                      | iPhone PWA                                                                      |
| ----------------------------------- | -------------------------------------------- | ------------------------------------------------------------------------------- |
| Back with unsaved changes           | System back triggers the leave sheet         | Back chevron triggers it; swipe-back gesture is not available in standalone PWA |
| Notification "Fixed deal available" | Deep links here                              | Deep links here if installed; else Inbox                                        |
| Price-change notification           | Gentle (at most once per 2 h, blueprint 2.3) | Same, via Web Push                                                              |

## Open questions (owner)

1. When the quote changed between viewing and Submit, BR-DEAL-03 freezes the price "valid at that instant". This design asks the client to re-confirm the new price instead of silently submitting it. Confirm that is acceptable (it means the frozen timestamp is the re-confirm tap).
2. Can a client switch an account from an approved fixed deal back to profit share before applying? BR-DEAL-04 suggests yes; confirm no approval is needed for that direction.
