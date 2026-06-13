#!/usr/bin/env bash
#
# check.sh — read-only quality gates for the whole project (CI-safe).
#
# Unlike fix.sh, this NEVER modifies files: it reports and fails on issues so it
# can run in CI. Tools that aren't installed are skipped with a note (so it
# still runs on a fresh machine), except the always-available core gates.
#
#   ./check.sh        # full check (incl. frontend production build)
#   ./check.sh --fast # skip the production frontend/go builds
#
set -uo pipefail
cd "$(dirname "$0")"

FAST=0
[[ "${1:-}" == "--fast" ]] && FAST=1

bold=$'\033[1m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'; dim=$'\033[2m'; reset=$'\033[0m'
overall=0
ok()   { echo "   ${green}✓${reset} $*"; }
warn() { echo "   ${yellow}⚠${reset} $*"; }
fail() { echo "   ${red}✗${reset} $*"; overall=1; }
skip() { echo "   ${dim}– skipped: $*${reset}"; }
step() { echo; echo "${bold}$*${reset}"; }
have() { command -v "$1" &>/dev/null; }

if have bun; then JS=bun; else JS=npm; fi

# ── 0. modules tidy (read-only) ───────────────────────────────────────────────
step "0. go.mod / go.sum tidy"
tmp="$(mktemp -d)"
cp go.mod go.sum "$tmp"/
if go mod tidy >/dev/null 2>&1 && diff -q go.mod "$tmp/go.mod" >/dev/null && diff -q go.sum "$tmp/go.sum" >/dev/null; then
  ok "tidy"
else
  fail "go.mod/go.sum are not tidy — run ./fix.sh (or 'go mod tidy')"
fi
cp "$tmp"/go.mod "$tmp"/go.sum . 2>/dev/null || true   # restore originals
rm -rf "$tmp"

# ── 1. formatting (read-only) ─────────────────────────────────────────────────
step "1. Formatting"
fmt_tool="gofmt"; have gofumpt && fmt_tool="gofumpt"
unformatted="$($fmt_tool -l cmd internal 2>/dev/null)"
[[ -z "$unformatted" ]] && ok "$fmt_tool: all files formatted" || fail "$fmt_tool: needs formatting:
$(echo "$unformatted" | sed 's/^/       /')"
if have goimports; then
  unorganized="$(goimports -l cmd internal 2>/dev/null)"
  [[ -z "$unorganized" ]] && ok "goimports: imports organized" || fail "goimports: needs organizing:
$(echo "$unorganized" | sed 's/^/       /')"
else
  skip "goimports (go install golang.org/x/tools/cmd/goimports@latest)"
fi

# ── 2. go vet ─────────────────────────────────────────────────────────────────
step "2. go vet"
vet_out="$(go vet ./... 2>&1)"
[[ -z "$vet_out" ]] && ok "no issues" || fail "issues:
$(echo "$vet_out" | sed 's/^/       /')"

# ── 3. golangci-lint (read-only) ──────────────────────────────────────────────
step "3. golangci-lint"
if have golangci-lint; then
  lint_out="$(golangci-lint run ./... --timeout=5m 2>&1)"; [[ $? -eq 0 ]] \
    && ok "no issues" || fail "issues:
$(echo "$lint_out" | sed 's/^/       /')"
else
  skip "golangci-lint (https://golangci-lint.run/usage/install)"
fi

# ── 4. govulncheck ────────────────────────────────────────────────────────────
step "4. govulncheck"
if have govulncheck; then
  vuln_out="$(govulncheck ./... 2>&1)"
  echo "$vuln_out" | grep -q "No vulnerabilities found" \
    && ok "no known vulnerabilities" || fail "vulnerabilities:
$(echo "$vuln_out" | sed 's/^/       /')"
else
  skip "govulncheck (go install golang.org/x/vuln/cmd/govulncheck@latest)"
fi

# ── 5. go test ────────────────────────────────────────────────────────────────
step "5. go test"
# The race detector requires cgo (CGO_ENABLED=1 + a C compiler). Sayumi
# otherwise builds CGO-free (modernc sqlite), so fall back to a plain run when
# cgo is unavailable rather than failing on a CGO-disabled CI/fresh machine.
race_flag=""
if [[ "$(go env CGO_ENABLED)" == "1" ]] && { have cc || have gcc || have clang; }; then
  race_flag="-race"
else
  warn "race detector skipped (cgo unavailable); running plain go test"
fi
test_out="$(go test $race_flag ./... 2>&1)"; test_exit=$?
echo "$test_out" | sed 's/^/   /'
[[ $test_exit -eq 0 ]] && ok "tests passed" || fail "tests failed"

# ── 6. frontend ───────────────────────────────────────────────────────────────
step "6. svelte-check"
( cd frontend && "$JS" run check >/dev/null 2>&1 ) && ok "no type/a11y issues" || fail "svelte-check failed (run: cd frontend && $JS run check)"

# ── 7. builds ─────────────────────────────────────────────────────────────────
if [[ "$FAST" -eq 0 ]]; then
  step "7. Builds"
  ( cd frontend && "$JS" run build >/dev/null 2>&1 ) && ok "frontend build" || fail "frontend build failed"
  CGO_ENABLED=0 go build -o /dev/null ./cmd/sayumi 2>/dev/null && ok "go build" || fail "go build failed"
fi

echo
[[ $overall -eq 0 ]] && echo "${green}${bold}✅ all checks passed${reset}" || echo "${red}${bold}❌ some checks failed${reset}"
exit $overall
