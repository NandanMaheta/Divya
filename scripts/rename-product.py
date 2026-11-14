#!/usr/bin/env python3
"""Rename the product across the repository (text + file paths).

Usage: python3 scripts/rename-product.py <old> <new>   e.g.  swastik swastik
Idempotent and safe to re-run on feature branches after merging stage.
Lockfiles are regenerated afterwards (pnpm install, uv lock), not edited.
"""

import subprocess
import sys
from pathlib import Path

old, new = (s.lower() for s in sys.argv[1:3])
pairs = [
    (old.upper(), new.upper()),
    (old.capitalize(), new.capitalize()),
    (old, new),
]
skip_suffix = {".png", ".jpg", ".jpeg", ".ico", ".webp", ".gif", ".woff", ".woff2", ".ttf"}
skip_names = {"pnpm-lock.yaml", "uv.lock", "go.sum"}

root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True,
                           check=True).stdout.strip())
files = subprocess.run(["git", "ls-files", "-z"], cwd=root, capture_output=True, check=True).stdout
changed = 0
for rel in filter(None, files.decode().split("\0")):
    p = root / rel
    if not p.is_file() or p.suffix in skip_suffix or p.name in skip_names:
        continue
    try:
        text = p.read_text()
    except UnicodeDecodeError:
        continue
    new_text = text
    for a, b in pairs:
        new_text = new_text.replace(a, b)
    if new_text != text:
        p.write_text(new_text)
        changed += 1

moved = 0
for rel in sorted(filter(None, files.decode().split("\0")), key=len, reverse=True):
    parts = Path(rel).parts
    if any(old in part.lower() for part in parts):
        new_rel = Path(*[part.replace(old, new).replace(old.capitalize(), new.capitalize()) for part in parts])
        if (root / rel).exists() and rel != str(new_rel):
            (root / new_rel).parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(["git", "mv", "-k", rel, str(new_rel)], cwd=root, check=True)
            moved += 1
# remove now-empty old directories
for d in sorted(root.rglob(f"*{old}*"), key=lambda x: len(str(x)), reverse=True):
    if d.is_dir() and not any(d.iterdir()):
        d.rmdir()
print(f"rename {old} -> {new}: {changed} files edited, {moved} paths moved")
