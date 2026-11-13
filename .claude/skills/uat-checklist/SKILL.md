---

name: uat-checklist
description: Generates a short, phone-friendly manual test script (UAT) for the owner from a feature's spec acceptance criteria and preview links. Use when a PR or module is ready for owner testing.
---

# UAT checklist

Write `specs/<feature>/uat.md` and paste it into the PR:

1. **Setup** (≤3 lines): preview admin URL, APK QR / PWA link, test accounts (never real client data).
2. **Checks**: one numbered line per acceptance criterion, phrased as an action + expected result the owner can verify in under a minute, e.g. "Publish a ₹500 fixed quote for Ramesh valid till 4 pm → Ramesh's app shows 'Fixed ₹500 · valid until 4:00 pm' within 1 minute."
3. **Money check** when relevant: the expected amounts table computed from the business rules.
4. Target ≤15 checks and ≤10 minutes. Checkboxes so the owner can tick them on the PR.
