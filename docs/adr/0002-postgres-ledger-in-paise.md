# ADR 0002: Double-entry ledger in PostgreSQL, integer paise

- Status: Accepted (2026-11-13)

## Context

Every rupee owed between the operator and clients must be explainable, correctable and reportable by financial year.

## Decision

Money is `bigint` paise. Settlements post balanced transactions to an append-only `ledger_entries` table (DB constraint: each transaction sums to zero; trigger forbids UPDATE/DELETE). Balances are derived. Corrections are reversal + replacement. Each application stores a JSON snapshot of its deal terms. TigerBeetle and external ledgers are out of scope at this scale.

## Consequences

Reports are SQL over the ledger and always agree with balances. Formula logic lives in pure Go functions with property tests and golden scenarios.
