---

paths:

- "**/*_test.go"
- "**/*.test.ts"
- "**/*.test.tsx"
- "apps/worker/tests/**"
- "**/.maestro/**"
- "**/e2e/**"

---

# Test rules

- Tests are written before the implementation and must fail first for the right reason.
- Name tests after behaviour and include business-rule IDs (`BR_SET_05`) where they apply; golden scenarios are `GS_01`…
- Prefer table-driven tests; property-based tests (`pgregory.net/rapid`) for money and scheduling invariants.
- No sleeps or real clocks: inject time. No live network: use fixtures.
- Never weaken or delete an existing assertion to make code pass; raise it with the reviewer instead.
