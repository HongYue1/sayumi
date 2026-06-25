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

# ── 1. frontend build (required before the Go gates) ─────────────────────────
# cmd/sayumi/main.go embeds the built SPA via //go:embed dist, so go vet, the
# linters, go test and go build all fail on a clean checkout unless
# cmd/sayumi/dist is populated first. Build it up front so `make check` is
# robust on any fresh clone. An existing build is reused (keeping --fast fast
# and not duplicating CI's prebuild); the authoritative clean build still runs
# in step 10 for full checks.
step "1. Frontend build (//go:embed dist)"
frontend_built=0
if [[ -e cmd/sayumi/dist/index.html ]]; then
  skip "frontend build (reusing existing cmd/sayumi/dist)"
elif ( cd frontend && "$JS" run build >/dev/null 2>&1 ); then
  ok "frontend build"; frontend_built=1
else
  fail "frontend build failed (cd frontend && $JS run build)"
fi

# ── 2. modules tidy (read-only) ──────────────────────────────────────────────
step "2. go.mod / go.sum tidy"
# `go mod tidy -diff` (Go 1.23+) reports whether tidy WOULD change go.mod/go.sum
# without writing to them — keeps this gate strictly read-only and interrupt-safe
# (no mutate-then-restore that could leave the files dirty if killed mid-run).
if tidy_diff="$(go mod tidy -diff 2>&1)" && [[ -z "$tidy_diff" ]]; then
  ok "tidy"
else
  fail "go.mod/go.sum are not tidy — run ./fix.sh (or 'go mod tidy'):
$(echo "$tidy_diff" | sed 's/^/       /')"
fi

# ── 3. formatting (read-only) ────────────────────────────────────────────────
step "3. Go formatting"
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

# ── 4. frontend formatting (read-only) ───────────────────────────────────────
step "4. Frontend formatting"
( cd frontend && "$JS" run format:check >/dev/null 2>&1 ) && ok "prettier: all files formatted" || fail "prettier failed (run: cd frontend && $JS run format)"

# ── 5. go vet ────────────────────────────────────────────────────────────────
step "5. go vet"
vet_out="$(go vet ./... 2>&1)"
[[ -z "$vet_out" ]] && ok "no issues" || fail "issues:
$(echo "$vet_out" | sed 's/^/       /')"

# ── 6. golangci-lint (read-only) ─────────────────────────────────────────────
step "6. golangci-lint"
if have golangci-lint; then
  lint_out="$(golangci-lint run ./... --timeout=5m 2>&1)"; [[ $? -eq 0 ]] \
    && ok "no issues" || fail "issues:
$(echo "$lint_out" | sed 's/^/       /')"
else
  skip "golangci-lint (https://golangci-lint.run/usage/install)"
fi

# ── 7. govulncheck ───────────────────────────────────────────────────────────
step "7. govulncheck"
if have govulncheck; then
  vuln_out="$(govulncheck ./... 2>&1)"
  echo "$vuln_out" | grep -q "No vulnerabilities found" \
    && ok "no known vulnerabilities" || fail "vulnerabilities:
$(echo "$vuln_out" | sed 's/^/       /')"
else
  skip "govulncheck (go install golang.org/x/vuln/cmd/govulncheck@latest)"
fi

# ── 8. go test ───────────────────────────────────────────────────────────────
step "8. go test"
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

# ── 9. frontend tests ────────────────────────────────────────────────────────
step "9. frontend tests"
( cd frontend && "$JS" run check >/dev/null 2>&1 ) && ok "svelte-check: no type/a11y issues" || fail "svelte-check failed (run: cd frontend && $JS run check)"
( cd frontend && "$JS" run test >/dev/null 2>&1 ) && ok "vitest: tests passed" || fail "vitest failed (run: cd frontend && $JS run test)"

# ── 10. builds ───────────────────────────────────────────────────────────────
if [[ "$FAST" -eq 0 ]]; then
  step "10. Builds"
  if [[ "$frontend_built" -eq 1 ]]; then
    ok "frontend build (already built in step 1)"
  elif ( cd frontend && "$JS" run build >/dev/null 2>&1 ); then
    ok "frontend build"
  else
    fail "frontend build failed"
  fi
  CGO_ENABLED=0 go build -o /dev/null ./cmd/sayumi 2>/dev/null && ok "go build" || fail "go build failed"
fi

echo
[[ $overall -eq 0 ]] && echo "${green}${bold}✅ all checks passed${reset}" || echo "${red}${bold}❌ some checks failed${reset}"
exit $overall
