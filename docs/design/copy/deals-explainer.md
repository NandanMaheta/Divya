# Copy: "How deals work"

Status: final copy for owner approval (Stage 1). Shown as a bottom sheet from the Deals screen (`../screens/client-deal-pick.md`) and from IPO details. Rules: BR-DEAL-01..04, BR-SET-01..06, BR-SET-09. Examples match GS-01 and GS-03.

Placeholders in `{braces}` are filled per client and per IPO from the API: `{p}` = this client's profit-share %, `{F}` = this client's current fixed price, `{B}` = brokerage for this IPO. The **Example** section always uses the fixed numbers below (₹15,000 / ₹3,000 / 30% / ₹500 / ₹40) so the arithmetic stays the same for everyone and can be checked by hand. When the client's own numbers differ, the sheet adds one line under the example: "Your numbers for this IPO: profit share {p}%, fixed ₹{F}, brokerage ₹{B}."

---

## Sheet copy

### How deals work

For each account, you choose one of two deals. You can change your choice until you mark that account **Applied**.

### Profit share (this is your default)

If the account gets shares, you keep **{p}%** of the listing profit.

- **Not allotted:** nothing to pay, nothing to receive.
- **Listed at a profit:** you keep {p}% of the profit.
- **Listed at a loss:** the loss is ours. You only pay back what the shares were worth at listing.

### Fixed deal

You get a fixed **₹{F}** for the account, **whether or not it gets shares**.

- **Not allotted:** we pay you ₹{F}.
- **Listed at a profit:** you keep ₹{F}. A ₹{B} brokerage is taken off.
- **Listed at a loss:** you still keep ₹{F}, and the loss is ours. No brokerage.

The fixed price can change during the day. The price on screen when you tap **Submit** is the one you get. We approve each fixed deal; if we can't, the account goes back to profit share and we tell you.

### Example

One account. Investment **₹15,000**. The shares list **₹3,000** higher.

**Profit share 30%**
Your share is 30% of ₹3,000 = ₹900.
You pay ₹15,000 + ₹3,000 − ₹900 = **₹17,100**

**Fixed ₹500, brokerage ₹40**
You pay ₹15,000 + ₹3,000 − ₹500 − ₹40 = **₹17,460**

**If the shares list ₹3,000 lower (fixed ₹500)**
You pay ₹15,000 − ₹3,000 − ₹500 = **₹11,500**
No brokerage on a loss.

**If the account is not allotted (fixed ₹500)**
You pay nothing. **We pay you ₹500.**

### When is payment due?

Two weekdays after the listing day, by 11:59 pm. Saturday and Sunday are skipped, so a Friday listing is due on Tuesday. Before listing, amounts are estimates based on today's expected profit; they become final when we enter the listing price, and we'll notify you.

[ **Got it** ]

---

## Short forms (used elsewhere)

| Where                                    | Copy                                                                          |
| ---------------------------------------- | ----------------------------------------------------------------------------- |
| Profit share tile                        | "Profit share" · "You keep {p}% of profit"                                    |
| Fixed tile                               | "Fixed ₹{F}" · "Yours, allotted or not"                                       |
| Link                                     | "How deals work"                                                              |
| Rejected fixed pick (notification + row) | "We couldn't accept the fixed deal for {account}. It's on profit share {p}%." |
| Approved fixed pick (notification)       | "Fixed deal approved: ₹{F} for {account}, {ipo}."                             |
| Price change (gentle notification)       | "Fixed deal for {ipo} is now ₹{F}. Valid until {time}."                       |
| Locked                                   | "Locked because you marked it applied."                                       |

## Arithmetic check (for reviewers)

| Case                      | Rule                         | Calculation               | Result          |
| ------------------------- | ---------------------------- | ------------------------- | --------------- |
| Profit share 30%, profit  | BR-SET-02: I + G − p·G       | 15,000 + 3,000 − 900      | ₹17,100         |
| Fixed ₹500, B ₹40, profit | BR-SET-05: I + G − F − B     | 15,000 + 3,000 − 500 − 40 | ₹17,460         |
| Fixed ₹500, loss ₹3,000   | BR-SET-06: I + G − F         | 15,000 − 3,000 − 500      | ₹11,500 (GS-03) |
| Fixed ₹500, not allotted  | BR-SET-04                    | operator pays F           | We pay you ₹500 |
| GS-01 cross-check         | 3 × fixed + 2 × profit share | 3 × 17,460 + 2 × 17,100   | ₹86,580         |

## Writing notes and localisation

- Words used everywhere, never swapped for synonyms: **profit share**, **fixed deal**, **allotted / not allotted**, **listing**, **you pay**, **we pay you**, **your share**, **brokerage**, **Applied**.
- "We" is the operator (Swastik). "You" is the main member; the copy is per account, so "the account" not "your account" when it may be a family member's.
- Keep the arithmetic lines as digits with the ₹ sign and Indian grouping in every language; do not spell numbers out. Hindi and Gujarati use the same Latin digits (matching `formatRupees`).
- The minus sign is U+2212 with spaces either side.
- Translators: "listing" means the first day the shares trade on NSE; "allotted" means the account received shares. Gujarati and Hindi text runs about 30–40% longer; the sheet scrolls, so no limits, but keep each bullet one sentence.
- Avoid "return" (ambiguous with returns on investment); use "pay back" or "pay".
