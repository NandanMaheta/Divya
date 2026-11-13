---

name: business-rules
description: Divya's business rules (IPOs, clients and family accounts, deals and fixed-price quotes, tasks and reminders, settlement formulas, due dates, payments) and the golden scenarios. Load whenever work touches money, deals, settlement, ledger, tasks, approvals or reminders.
---

# Divya business rules

The authoritative list is `docs/product/business-rules.md` — read it in full before specifying, testing or reviewing anything it covers. Key formulas (I = issue price × shares, G = (NSE listing price − issue price) × shares):

| Deal                                  | Not allotted    | Allotted, G > 0           | Allotted, G ≤ 0              |
| ------------------------------------- | --------------- | ------------------------- | ---------------------------- |
| Profit share p% (default)             | 0               | client pays I + G − p·G   | client pays I + G            |
| Fixed ₹F (B = brokerage, default ₹40) | operator pays F | client pays I + G − F − B | client pays I + G − F (no B) |

- Due: listing day + 2 weekdays (skip Sat/Sun), 23:59 IST.
- Deal changeable by the client until that account is marked APPLIED; default is profit share; fixed quotes are per client and time-bound, frozen at submit, need admin approval.
- 1 lot per account; family accounts are name + PAN, ≤10 per main member; every add/edit/delete needs admin approval.
- Golden scenarios: GS-01 → ₹86,580 owed; GS-02 → operator owes ₹1,500; GS-03 → ₹11,500; GS-04 → Fri→Tue, Thu→Mon.

Never infer a rule that is not written down; ask the owner.
