---

name: money-safety
description: Checklist for any code that computes, stores, posts or displays money in Divya (settlement, ledger, payments, reports). Use before writing or reviewing such code.
---

# Money safety checklist

- [ ] Amounts are `int64` paise in Go, `bigint` in SQL, `Paise` in TS. No float anywhere on the path (including JSON: send paise integers).
- [ ] Percentages are basis points (`int64`, 3000 = 30%). Multiply first, divide last, with the rounding rule from `internal/settlement` (half-up to the rupee unless the setting says paise).
- [ ] Rounding happens in exactly one function; tests cover .5 boundaries and negative values.
- [ ] Every ledger transaction is balanced (sum = 0) and inserted in one DB transaction with its audit row and outbox events.
- [ ] No UPDATE/DELETE on ledger or audit rows; corrections are reversal + replacement linked by `reverses_txn_id`.
- [ ] Settlement reads the application's deal **snapshot**, never current defaults.
- [ ] Property tests: balance invariants, reversal symmetry, idempotent recomputation (running settlement twice changes nothing).
- [ ] Golden scenarios GS-01…GS-04 pass.
- [ ] Display only through `formatRupees` (`@divya/domain`).
