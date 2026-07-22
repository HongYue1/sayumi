#!/usr/bin/env bash
# fix.sh — mutating auto-fix pass; run check.sh afterward.
set -uo pipefail
cd "$(dirname "$0")"
bold=$'\033[1m'; green=$'\033[32m'; red=$'\033[31m'; dim=$'\033[2m'; reset=$'\033[0m'; overall=0
ok(){ echo "   ${green}✓${reset} $*"; }; fail(){ echo "   ${red}✗${reset} $*"; overall=1; }; skip(){ echo "   ${dim}– skipped: $*${reset}"; }; step(){ echo; echo "${bold}$*${reset}"; }; have(){ command -v "$1" &>/dev/null; }
step "1. Modernizations (go fix)"; if go fix ./...; then ok "go fix"; else fail "go fix failed"; fi
step "2. golangci-lint --fix"; if have golangci-lint; then if golangci-lint run ./... --fix --timeout=5m; then ok "lint --fix"; else fail "lint --fix failed or left non-fixable issues"; fi; else skip "golangci-lint"; fi
step "3. Go imports & formatting"
if have goimports; then if goimports -w -local sayumi cmd internal; then ok "goimports -w"; else fail "goimports failed"; fi; else skip "goimports (imports not auto-managed)"; fi
if have gofumpt; then if gofumpt -w cmd internal; then ok "gofumpt -w"; else fail "gofumpt failed"; fi
elif ! have goimports; then if gofmt -w cmd internal; then ok "gofmt -w (fallback)"; else fail "gofmt failed"; fi; else skip "gofumpt"; fi
step "4. Frontend formatting"
if have bun; then if (cd frontend && bun run format); then ok "prettier"; else fail "frontend formatting failed"; fi
elif have npm; then if (cd frontend && npm run format); then ok "prettier"; else fail "frontend formatting failed"; fi
else fail "frontend formatting requires bun or npm"; fi
step "5. go mod tidy"; if go mod tidy; then ok "tidy"; else fail "go mod tidy failed"; fi
echo; if [[ $overall -eq 0 ]]; then echo "${green}${bold}✓ fixes applied — now run ./check.sh${reset}"; else echo "${red}${bold}✗ some fixes failed — review diagnostics${reset}"; fi; exit $overall
