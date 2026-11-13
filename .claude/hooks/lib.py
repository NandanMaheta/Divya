"""Shared helpers for Divya Claude Code hooks (stdlib only)."""

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


def active_states() -> list[tuple[Path, dict]]:
    out = []
    for p in sorted((ROOT / "specs").glob("*/state.json")):
        try:
            out.append((p, json.loads(p.read_text())))
        except (OSError, json.JSONDecodeError):
            continue
    return out
