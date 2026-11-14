"""PreToolUse(Edit|Write|MultiEdit): block edits to secrets, state files, applied migrations,
deploy workflows and protected (test-first) tests."""

import os
import subprocess
from pathlib import Path

from lib import active_states, block, read_input, repo_root

data = read_input()
raw = data.get("tool_input", {}).get("file_path", "")
if not raw:
    raise SystemExit(0)
path = Path(raw).resolve()
root = repo_root(path)
try:
    rel = path.relative_to(root).as_posix()
except ValueError:
    raise SystemExit(0)

name = path.name
if name.startswith(".env") and name != ".env.example":
    block("Editing .env files is not allowed. Secrets live in GitHub Environments / Secret Manager.")
if name.endswith(".tfstate") or ".terraform/" in rel:
    block("Infrastructure state is managed by CI only.")
if rel.startswith(".github/workflows/deploy") and os.environ.get("SWASTIK_ALLOW_DEPLOY_EDIT") != "1":
    block("Deploy workflows change only with owner approval (set SWASTIK_ALLOW_DEPLOY_EDIT=1 for that session).")

if rel.startswith("apps/api/migrations/") and rel.endswith(".sql"):
    for base in ("origin/stage", "origin/main"):
        tracked = subprocess.run(["git", "cat-file", "-e", f"{base}:{rel}"], cwd=root, capture_output=True)
        if tracked.returncode == 0:
            block(f"{rel} is already on {base} (applied). Add a new migration instead of editing it.")

agent = data.get("agent_type") or os.environ.get("CLAUDE_AGENT_TYPE", "")
if agent != "test-engineer":
    for _, state in active_states(root):
        for task in state.get("tasks", {}).values():
            if rel in task.get("protected_tests", []):
                block(f"{rel} is a protected test written test-first. Make the code satisfy it; "
                      "if the test itself is wrong, report it to the reviewer/owner instead.")
