---

name: ship-module
description: Runs /ship-feature for every feature of a Swastik module (or the next N ready features) in dependency order, batching owner approvals. Use when asked to build a whole module such as "M7" or "the next features".
---

# Ship a module

1. List the module's features from `docs/product/modules.md` whose dependencies are `done`.
2. To minimise owner interruptions, first run steps 1–2 of `ship-feature` (research + spec) for **all** ready features, then ask the owner to approve the specs together (gate A, batched).
3. Then run steps 4–10 for each approved feature. Features on different tracks (core / ipo / money / ui / data / ops) may run concurrently in separate worktrees; features on the same track run sequentially.
4. After the module's last feature: run an integration pass — the full test suite, E2E smoke, golden scenarios — and post a module summary with a combined UAT checklist.
