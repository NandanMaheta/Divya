#!/usr/bin/env bash
# Prepares the local environment for agents (run by the SessionStart hook; safe to run any time).
# Starts Docker if it is installed but not running (root only), and reports tool versions.
set -uo pipefail
if command -v docker >/dev/null 2>&1 && ! docker info >/dev/null 2>&1; then
  if [ "$(id -u)" = 0 ] && command -v dockerd >/dev/null 2>&1; then
    (dockerd >/tmp/dockerd.log 2>&1 &)
    for _ in $(seq 1 30); do docker info >/dev/null 2>&1 && break; sleep 1; done
  fi
fi
docker info >/dev/null 2>&1 && echo "docker: running" || echo "docker: NOT running (integration tests will skip or fail)"
command -v go >/dev/null && echo "go: $(go env GOVERSION) (module toolchain from go.mod)"
echo "lint: use 'make lint-go' (pinned golangci-lint v2.15.0)"
