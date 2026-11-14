---

name: ship-module
description: Runs /ship-feature (pipeline v2) for every ready feature of a Swastik module or the next N ready features, batching owner approvals and pipelining passes across features. Use when asked to build a whole module such as "M7" or "the next features".
---

# Ship a module (pipeline v2)

1. List the module's features from `docs/product/modules.md` whose dependencies allow at least specification.
2. Run step 1 of `ship-feature` (specify) for all of them, then ask the owner to approve the batch at once.
3. Run steps 2–5 per feature. **Pipeline across features:** while one feature's code pass runs, start the next feature's test pass in its own worktree. Features on different tracks (core / ipo / money / ui / data / ops) run concurrently.
4. After the module's last feature: full suite, E2E smoke, golden scenarios, one combined UAT checklist; update `docs/engineering/handover.md`.
