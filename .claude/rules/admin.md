---

paths:

- "apps/admin/**"

---

# Admin web rules

- Vite + React 19 + TanStack Router (file routes in `src/routes`) + TanStack Query + Tailwind v4 with `@swastik/tokens/theme.css`.
- Data only through `@swastik/api-client` hooks; no raw fetch. Money formatting only via `@swastik/domain`.
- Every page: loading, empty, error states; keyboard-accessible tables with bulk actions; URL holds filters.
- Each page gets a Vitest + Testing Library test and a Playwright flow in `e2e/`.
- `routeTree.gen.ts` is generated — commit it, never hand-edit it.
