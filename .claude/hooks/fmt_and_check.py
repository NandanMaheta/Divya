"""PostToolUse(Edit|Write|MultiEdit): format the edited file and run fast checks; exit 2 on problems."""

import shutil
from pathlib import Path

from lib import ROOT, block, read_input, repo_root, run

data = read_input()
path = Path(data.get("tool_input", {}).get("file_path", ""))
if not path.is_file():
    raise SystemExit(0)

root = repo_root(path)
problems: list[str] = []
suffix = path.suffix

if suffix == ".go" and shutil.which("gofmt"):
    run(["gofmt", "-w", str(path)], root)
    module = next((p for p in path.parents if (p / "go.mod").exists()), None)
    if module:
        code, out = run(["go", "vet", f"./{path.parent.relative_to(module)}"], module)
        if code:
            problems.append(f"go vet:\n{out}")
elif suffix in {".ts", ".tsx", ".js", ".jsx", ".json", ".css"}:
    biome = next((b for b in (root / "node_modules/.bin/biome", ROOT / "node_modules/.bin/biome") if b.exists()), None)
    if biome:
        run([str(biome), "check", "--write", str(path)], root)
        code, out = run([str(biome), "check", str(path)], root)
        if code:
            problems.append(f"biome:\n{out}")
elif suffix == ".py" and shutil.which("uv"):
    worker = root / "apps" / "worker"
    if worker in path.parents:
        run(["uv", "run", "ruff", "format", str(path)], worker)
        code, out = run(["uv", "run", "ruff", "check", "--fix", str(path)], worker)
        if code:
            problems.append(f"ruff:\n{out}")

if problems:
    block(f"Fix these issues in {path.name} before continuing:\n\n" + "\n".join(problems))
