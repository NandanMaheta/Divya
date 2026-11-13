---

paths:

- "apps/api/migrations/**"
- "apps/api/queries/**"

---

# SQL rules

- PostgreSQL 18: `uuidv7()` primary keys, `timestamptz`, `bigint` paise, CHECK constraints for enums/states, FKs everywhere, `org_id` on every business table.
- Append-only tables (`audit_log`, `ledger_entries`, history tables) get the `forbid_mutation()` trigger.
- PAN and similar identifiers: store `*_enc bytea` + `*_hash bytea` (blind index); never plaintext.
- Migrations must be backward compatible with the previous app version (expand → migrate → contract).
- After editing: `sqlc generate && sqlc vet` and the integration tests.
