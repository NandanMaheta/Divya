"""Shared helpers for Swastik Claude Code hooks (stdlib only)."""

import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(os.environ.get("CLAUDE_PROJECT_DIR", Path(__file__).resolve().parents[2]))


def read_input() -> dict:
    try:
        return json.load(sys.stdin)
    except json.JSONDecodeError:
        return {}


def block(message: str) -> None:
    """Exit code 2: the action is blocked and the message is shown to Claude."""
    print(message, file=sys.stderr)
    sys.exit(2)


def run(cmd: list[str], cwd: Path) -> tuple[int, str]:
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=240)
    return proc.returncode, (proc.stdout + proc.stderr)[-4000:]


def repo_root(start: str | Path | None) -> Path:
    """Root of the git worktree containing `start` (the agent's cwd or the edited file);
    falls back to the project dir. Feature work happens in worktrees like ../swastik-002."""
    if start:
        p = Path(start)
        probe = p if p.is_dir() else p.parent
        while not probe.exists() and probe != probe.parent:
            probe = probe.parent
        proc = subprocess.run(["git", "-C", str(probe), "rev-parse", "--show-toplevel"],
                              capture_output=True, text=True)
        if proc.returncode == 0 and proc.stdout.strip():
            return Path(proc.stdout.strip())
    return ROOT


def active_states(root: Path | None = None) -> list[tuple[Path, dict]]:
    out = []
    for p in sorted(((root or ROOT) / "specs").glob("*/state.json")):
        try:
            out.append((p, json.loads(p.read_text())))
        except (OSError, json.JSONDecodeError):
            continue
    return out
