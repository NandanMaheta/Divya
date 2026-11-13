# CLI contract: `divya admin create` (FR-080, D-15, US-9)

```text
divya admin create --email <email> --name <name> [--mobile <mobile>] [--org-name <name>] [--org-id <uuid>]
```

| Aspect | Contract |
|---|---|
| Config | Only `DATABASE_URL` (and optional `APP_ENV`). JWT and PII keys are not needed. |
| Password input | If stdin is a TTY: the prompt `Password:` and then `Repeat password:`, with no echo (`golang.org/x/term`). Otherwise: exactly one line read from stdin (trailing newline removed). The password is never accepted as a flag or an environment variable. |
| Org | No org exists: create one named `--org-name` (default `Divya`). Exactly one org exists: use it (`--org-name` is ignored). More than one: `--org-id` is required, otherwise `bootstrap.org_ambiguous`. |
| Concurrency | `pg_advisory_xact_lock` on a fixed key. Two concurrent runs on an empty database: exactly one succeeds and the other gets `bootstrap.admin_exists`. |
| Result | An `active` admin with the email verified and TOTP not enrolled. Audit `auth.admin.bootstrapped`, actor `system`, in the same transaction. |
| stdout on success | `admin created: id=<uuid> email=<normalised email>` |
| stderr on refusal | `error: <code>: <English message naming the failed rule>` |
| Exit status | 0 on success; 1 on any refusal or error; 2 on usage errors (bad flags) |
| Never | The password in stdout, stderr, logs, audit or the process arguments. No `--verbose`. |
| Where it runs | Locally against the local database, or by the owner from their own machine against staging or production (`DATABASE_URL` from Supabase). Agents never run it against staging or production. |
