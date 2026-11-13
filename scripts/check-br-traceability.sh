#!/usr/bin/env bash
# Fails if a business rule belonging to a feature marked "done" in docs/product/modules.md
# has no test whose name contains it (BR-SET-05 -> BR_SET_05). Prints full coverage either way.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - <<'PY'
import re, subprocess, sys
from pathlib import Path

rules = sorted(set(re.findall(r"\b(BR-[A-Z]+-\d{2}|GS-\d{2})\b", Path("docs/product/business-rules.md").read_text())))
test_files = subprocess.run(
    ["git", "ls-files", "*_test.go", "*.test.ts", "*.test.tsx", "apps/worker/tests/*.py", "*.yaml"],
    capture_output=True, text=True, check=True).stdout.split()
corpus = "\n".join(Path(f).read_text(errors="ignore") for f in test_files if Path(f).is_file())

def expand(spec: str) -> set[str]:
    out = set()
    for m in re.finditer(r"(BR-[A-Z]+|GS)-(\d{2})(?:…-?(\d{2}))?((?:,\s*-\d{2})*)", spec):
        prefix, start, end, extra = m.groups()
        nums = [int(start)] + ([*range(int(start) + 1, int(end) + 1)] if end else [])
        nums += [int(x) for x in re.findall(r"-(\d{2})", extra or "")]
        out |= {f"{prefix}-{n:02d}" for n in nums}
    return out

required = set()
for row in Path("docs/product/modules.md").read_text().splitlines():
    cols = [c.strip() for c in row.split("|")]
    if len(cols) > 7 and cols[7].startswith("done"):
        required |= expand(cols[4])

covered = {r for r in rules if r.replace("-", "_") in corpus or r in corpus}
print(f"Business-rule test coverage: {len(covered)}/{len(rules)}")
missing = sorted(required - covered)
if missing:
    print("Missing tests for rules of done features:", ", ".join(missing))
    sys.exit(1)
print("OK: every rule of a done feature has a test.")
PY
