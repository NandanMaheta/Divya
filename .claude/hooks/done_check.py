"""Stop/SubagentStop: if a task is in progress, run its done command; exit 2 (keep working) while red.
Gives up after 3 consecutive failures so the conductor can escalate to the owner."""

import json
import os

from lib import ROOT, active_states, read_input, run

data = read_input()
if os.environ.get("DIVYA_DONE_CHECK", "on") == "off":
    raise SystemExit(0)

for state_path, state in active_states():
    for task_id, task in state.get("tasks", {}).items():
        if task.get("status") != "in_progress" or not task.get("done_command"):
            continue
        code, out = run(["bash", "-lc", task["done_command"]], ROOT)
        if code == 0:
            task["status"] = "done"
            task["failures"] = 0
            state_path.write_text(json.dumps(state, indent=2) + "\n")
            continue
        task["failures"] = task.get("failures", 0) + 1
        state_path.write_text(json.dumps(state, indent=2) + "\n")
        if task["failures"] >= 3:
            print(f"{task_id} still failing after 3 attempts; escalate to the owner.")
            raise SystemExit(0)
        print(f"Task {task_id} is not done. `{task['done_command']}` failed:\n{out}", file=__import__("sys").stderr)
        raise SystemExit(2)
