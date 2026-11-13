#!/usr/bin/env bash
# Deliberate-violation check for the golangci-lint boundary rules of feature 002 (T002/T003).
#
# It copies apps/api to a scratch directory (the working tree is never modified), drops small
# offending Go packages into the copy, runs golangci-lint once with the repository's own
# apps/api/.golangci.yml, and asserts that each offender is reported by the expected linter:
#
#   depguard  platform importing health / scheduler / outbox     (platform-is-a-leaf)
#   depguard  outbox importing scheduler                         (outbox-does-not-know-scheduler)
#   depguard  go-oidc imported outside platform/tickauth         (oidc-only-in-tickauth)
#   forbidigo time.Now / time.Since / time.Until / time.Local in business code
#
# It also asserts the negative controls: go-oidc inside platform/tickauth, and the clock,
# httpx, cmd/ and _test.go exclusions for time.Now, are NOT reported.
#
# Cases that need a package which does not exist yet (scheduler, outbox) or a dependency that
# is not buildable yet (go-oidc without its go.sum entries) are SKIPPED, not failed.
#
# Usage: bash scripts/check-lint-boundaries.sh
# Env:   GOLANGCI_LINT  path of the golangci-lint binary (default: golangci-lint on PATH).
#                       It must be built with a Go version >= the one in apps/api/go.mod;
#                       CI pins the version in .github/workflows/pr.yml.
# Exit:  0 all asserted cases pass | 1 at least one case fails | 2 the linter could not run
#        cleanly on the unmodified copy (environment problem, nothing was asserted).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API="$ROOT/apps/api"
LINT="${GOLANGCI_LINT:-golangci-lint}"
MOD="github.com/NandanMaheta/divya/apps/api"
OIDC_PKG="github.com/coreos/go-oidc/v3/oidc"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cp -R "$API" "$WORK/api"
cd "$WORK/api" || exit 2

LINT_FLAGS=(run --max-issues-per-linter=0 --max-same-issues=0 ./...)

# Baseline: the unmodified copy must lint clean, otherwise nothing below means anything.
baseline="$("$LINT" "${LINT_FLAGS[@]}" 2>&1)"
rc=$?
if [ "$rc" -ne 0 ]; then
	echo "ERROR: golangci-lint does not run cleanly on the unmodified apps/api (exit $rc); nothing asserted." >&2
	echo "$baseline" | tail -20 >&2
	exit 2
fi

pass=0
fail=0
skip=0

# Each case is recorded as: id|kind|linter|file ; kind is "violation" (must be reported by
# linter) or "control" (must NOT be reported by linter).
CASES=()

has_pkg() { # has_pkg <dir relative to apps/api>: true if the directory holds a non-test .go file
	compgen -G "$API/$1/*.go" >/dev/null
}

# add_case <id> <kind> <linter> <relative file>   (file content is read from stdin)
add_case() {
	local id="$1" kind="$2" linter="$3" file="$4"
	mkdir -p "$(dirname "$file")"
	cat >"$file"
	CASES+=("$id|$kind|$linter|$file")
}

skip_case() { # skip_case <id> <reason>
	printf 'SKIP  %-34s %s\n' "$1" "$2"
	skip=$((skip + 1))
}

# --- depguard: platform is a leaf ------------------------------------------------------------
for target in health scheduler outbox; do
	id="platform-imports-$target"
	if has_pkg "internal/$target"; then
		add_case "$id" violation depguard "internal/platform/zzviol_$target/v.go" <<EOF
package zzviol

import _ "$MOD/internal/$target"
EOF
	else
		skip_case "$id" "internal/$target does not exist yet"
	fi
done

# --- depguard: outbox must not know scheduler ------------------------------------------------
if has_pkg internal/outbox && has_pkg internal/scheduler; then
	add_case outbox-imports-scheduler violation depguard "internal/outbox/zzviol/v.go" <<EOF
package zzviol

import _ "$MOD/internal/scheduler"
EOF
else
	skip_case outbox-imports-scheduler "internal/outbox or internal/scheduler does not exist yet"
fi

# --- depguard: go-oidc only in platform/tickauth ---------------------------------------------
# Importing a module whose dependencies are not in go.sum makes the whole lint run fail with a
# typecheck error, so the case only runs when the package builds.
if (cd "$API" && go list -deps "$OIDC_PKG" >/dev/null 2>&1); then
	add_case oidc-outside-tickauth violation depguard "internal/zzviol_oidc/v.go" <<EOF
package zzviol

import _ "$OIDC_PKG"
EOF
	add_case oidc-inside-tickauth control depguard "internal/platform/tickauth/zzctl/v.go" <<EOF
package zzctl

import _ "$OIDC_PKG"
EOF
else
	skip_case oidc-outside-tickauth "go-oidc not buildable yet (go.sum entries land with T018)"
	skip_case oidc-inside-tickauth "go-oidc not buildable yet (go.sum entries land with T018)"
fi

# --- forbidigo: machine clock and host time zone ---------------------------------------------
add_case time-now violation forbidigo "internal/zzviol_now/v.go" <<'EOF'
package zzviol

import "time"

func Now() time.Time { return time.Now() }
EOF
add_case time-since violation forbidigo "internal/zzviol_since/v.go" <<'EOF'
package zzviol

import "time"

func Since(t time.Time) time.Duration { return time.Since(t) }
EOF
add_case time-until violation forbidigo "internal/zzviol_until/v.go" <<'EOF'
package zzviol

import "time"

func Until(t time.Time) time.Duration { return time.Until(t) }
EOF
add_case time-local violation forbidigo "internal/zzviol_local/v.go" <<'EOF'
package zzviol

import "time"

func Local() *time.Location { return time.Local }
EOF
add_case time-now-in-platform violation forbidigo "internal/platform/zzviol_now/v.go" <<'EOF'
package zzviol

import "time"

func Now() time.Time { return time.Now() }
EOF

# --- forbidigo exclusions (negative controls) ------------------------------------------------
add_case time-now-in-clock control forbidigo "internal/platform/clock/zzctl/v.go" <<'EOF'
package zzctl

import "time"

func Now() time.Time { return time.Now() }
EOF
add_case time-now-in-httpx control forbidigo "internal/platform/httpx/zzctl/v.go" <<'EOF'
package zzctl

import "time"

func Now() time.Time { return time.Now() }
EOF
add_case time-now-in-cmd control forbidigo "cmd/zzctl/v.go" <<'EOF'
package zzctl

import "time"

func Now() time.Time { return time.Now() }
EOF
add_case time-now-in-test-file control forbidigo "internal/zzctl_test/v_test.go" <<'EOF'
package zzctl

import (
	"testing"
	"time"
)

func TestNow(t *testing.T) { _ = time.Now() }
EOF

# --- one lint run over all offenders ---------------------------------------------------------
out="$("$LINT" "${LINT_FLAGS[@]}" 2>&1)"
if grep -qF "(typecheck)" <<<"$out"; then
	echo "ERROR: the dropped offender files do not type-check, so linters did not run:" >&2
	grep -F "(typecheck)" <<<"$out" | head -5 >&2
	exit 2
fi

for c in "${CASES[@]}"; do
	IFS='|' read -r id kind linter file <<<"$c"
	if grep -F "$file" <<<"$out" | grep -qF "($linter)"; then
		reported=yes
	else
		reported=no
	fi
	if [ "$kind" = violation ] && [ "$reported" = yes ]; then
		printf 'PASS  %-34s reported by %s\n' "$id" "$linter"
		pass=$((pass + 1))
	elif [ "$kind" = control ] && [ "$reported" = no ]; then
		printf 'PASS  %-34s not reported by %s (allowed)\n' "$id" "$linter"
		pass=$((pass + 1))
	elif [ "$kind" = violation ]; then
		printf 'FAIL  %-34s expected a %s finding in %s, got none\n' "$id" "$linter" "$file"
		fail=$((fail + 1))
	else
		printf 'FAIL  %-34s %s must not be reported by %s\n' "$id" "$file" "$linter"
		fail=$((fail + 1))
	fi
done

echo "boundary check: $pass passed, $fail failed, $skip skipped"
[ "$fail" -eq 0 ]
