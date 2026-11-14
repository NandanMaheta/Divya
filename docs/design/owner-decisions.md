# Owner design decisions (2026-11-13)

Answered by the owner:

| Topic                              | Decision                                                                                                                            |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| Brand colour                       | **Jamun purple `#5B2A86`** (dark mode `#C4A3E8`); gold is decorative only (logo, festival days).                                    |
| Undo "Mark applied"                | **No.** Marking applied locks the deal for that account; only the admin can reopen it. The client confirms before marking.          |
| Fixed price changed while choosing | **Re-confirm**: on Submit the app shows the new price and the client confirms it before it goes for approval.                       |
| Partial payments                   | **Yes**: "I've paid" defaults to the full amount due and is editable; the admin confirms what arrived; the balance carries forward. |

Conductor defaults (owner may change):

| Topic                                              | Default                                                                                                                 |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Mark applied before the reminder window opens      | Allowed whenever the IPO is open for bidding.                                                                           |
| Switch an approved fixed deal back to profit share | Allowed without approval until the account is marked applied (profit share is the default deal); the admin is notified. |
| Payout date when the operator owes the client      | Shown as "We'll pay by <date>" using the same rule: listing day + 2 weekdays.                                           |
| Excuse reason visible to the client                | No. The client sees "Not needed this time".                                                                             |
| Admin marks an account applied for the client      | Yes, audited and labelled "Marked by admin".                                                                            |
| "Do now" order on Home                             | Overdue payment → apply task closing today → other open apply tasks → deal awaiting the client → payment due.           |
