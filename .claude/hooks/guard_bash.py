"""PreToolUse(Bash): block destructive or production-touching commands."""

import re

from lib import block, read_input

cmd = read_input().get("tool_input", {}).get("command", "")
rules = [
    (r"git\s+push\s+.*(--force|-f\b)(?!-with-lease)", "Force-push is not allowed."),
    (r"git\s+push\s+\S+\s+(main|stage)\b|git\s+push\s+\S+\s+\S*:(refs/heads/)?(main|stage)\b", "Never push to main or stage directly; open a PR into stage."),
    (r"\brm\s+-rf\s+(/|~|\$HOME|\.\.)", "Refusing a broad rm -rf."),
    (r"gcloud\s+.*(deploy|delete|sql|iam|secrets)", "Cloud changes happen only in CI (deploy.yml)."),
    (r"supabase\s+(db\s+push|db\s+reset\s+--linked|link)", "Remote database changes happen only in CI."),
    (r"tofu\s+(apply|destroy)|terraform\s+(apply|destroy)", "Infrastructure applies happen only in CI."),
    (r"wrangler\s+(deploy|pages\s+deploy|secret)", "Cloudflare deploys happen only in CI."),
    (r"\b(cat|less|head|tail|grep)\s+[^|;&]*\.env(\s|$)|\bprintenv\b|(^|[;&|]\s*)env\s*$", "Reading secrets/environment dumps is not allowed."),
    (r"curl\s+.*\|\s*(sh|bash)", "Piping remote scripts into a shell is not allowed."),
]
for pattern, message in rules:
    if re.search(pattern, cmd):
        block(message + f"\nCommand: {cmd}")
