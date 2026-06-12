#!/usr/bin/env bash
#
# fix.sh — auto-fix pass (MUTATES files). The opposite of check.sh, which is
# read-only. Run this locally to clean up, then run ./check.sh to verify.
#
set -uo pipefail
cd "$(dirname "$0")"

bold=$'\033[1m'; green=$'\033[32m'; dim=$'\033[2m'; reset=$'\033[0m'
ok()   { echo "   ${green}✓${reset} $*"; }
skip() { echo "   ${dim}– skipped: $*${reset}"; }
step() { echo; echo "${bold}$*${reset}"; }
have() { command -v "$1" &>/dev/null; }

step "1. Imports & formatting"
if have goimports; then goimports -w cmd internal && ok "goimports -w"; else
  gofmt -w cmd internal && ok "gofmt -w"; skip "goimports"; fi
if have gofumpt; then gofumpt -w cmd internal && ok "gofumpt -w"; else skip "gofumpt"; fi

step "2. Modernizations (go fix)"
go fix ./... >/dev/null 2>&1 && ok "go fix"

step "3. golangci-lint --fix"
if have golangci-lint; then golangci-lint run ./... --fix --timeout=5m || true; ok "lint --fix"; else
  skip "golangci-lint"; fi

step "4. go mod tidy"
go mod tidy && ok "tidy"

echo
echo "${green}${bold}✓ fixes applied — now run ./check.sh${reset}"
