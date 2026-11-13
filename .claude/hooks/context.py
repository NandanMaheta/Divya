"""SessionStart: print the current branch and in-flight features as context."""

import subprocess

import os

from lib import active_states, repo_root

root = repo_root(os.getcwd())
branch = subprocess.run(["git", "branch", "--show-current"], cwd=root, capture_output=True, text=True).stdout.strip()
lines = [f"Divya repo · branch: {branch or '(detached)'}"]
for path, state in active_states(root):
    if state.get("phase") != "done":
        todo = [t for t, v in state.get("tasks", {}).items() if v.get("status") != "done"]
        lines.append(f"In flight: {path.parent.name} · phase {state.get('phase')} · open tasks {len(todo)}")
lines.append("Business rules: docs/product/business-rules.md · Build order: docs/product/modules.md")
print("\n".join(lines))
