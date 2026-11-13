---

name: admin-page-pattern
description: How to build a Divya admin panel page (TanStack file route, query hooks, data table with bulk actions, forms, tests, Playwright flow). Use for any page under apps/admin.
---

# Admin page pattern

```
apps/admin/src/routes/<section>/index.tsx     // file route; search params validated with zod (filters live in the URL)
apps/admin/src/features/<section>/
  queries.ts             // query options + mutations over @divya/api-client
  <section>-table.tsx    // TanStack Table: sorting, filtering, row selection, bulk-action bar, keyboard nav
  <section>-form.tsx     // forms with zod schemas shared from @divya/domain where possible
  *.test.tsx
apps/admin/e2e/<section>.spec.ts               // Playwright journey
```

Every page: loading skeleton, empty state with the next action, error state with retry, optimistic updates for quick toggles, confirmation dialogs for money-affecting actions, audit link on records.
