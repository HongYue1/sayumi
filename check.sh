#!/usr/bin/env bash
# check.sh — read-only quality gates for the whole project (CI-safe).
set -uo pipefail
cd "$(dirname "$0")"
FAST=0; [[ "${1:-}" == "--fast" ]] && FAST=1
bold=$'\033[1m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'; dim=$'\033[2m'; reset=$'\033[0m'
overall=0
ok(){ echo "   ${green}✓${reset} $*"; }; warn(){ echo "   ${yellow}⚠${reset} $*"; }
fail(){ echo "   ${red}✗${reset} $*"; overall=1; }; skip(){ echo "   ${dim}– skipped: $*${reset}"; }
step(){ echo; echo "${bold}$*${reset}"; }; have(){ command -v "$1" &>/dev/null; }; indent(){ sed 's/^/       /'; }
if have bun; then JS=bun; elif have npm; then JS=npm; else JS=""; fi

step "1. Frontend build (//go:embed dist)"
frontend_built=0
if [[ -e cmd/sayumi/dist/index.html ]]; then skip "frontend build (reusing existing cmd/sayumi/dist)"
elif [[ -z "$JS" ]]; then fail "frontend build requires bun or npm"
elif out="$(cd frontend && "$JS" run build 2>&1)"; then ok "frontend build"; frontend_built=1
else fail "frontend build failed:
$(printf '%s\n' "$out"|indent)"; fi

step "2. go.mod / go.sum tidy"
tidy_diff=""
if tidy_diff="$(go mod tidy -diff)"; then
  [[ -z "$tidy_diff" ]] && ok "tidy" || fail "go.mod/go.sum are not tidy — run ./fix.sh:
$(printf '%s\n' "$tidy_diff"|indent)"
else fail "go mod tidy -diff failed; see diagnostics above"; fi

step "3. Go formatting"
if have gofumpt; then
  if out="$(gofumpt -l cmd internal 2>&1)"; then [[ -z "$out" ]] && ok "gofumpt: all files formatted" || fail "gofumpt: needs formatting:
$(printf '%s\n' "$out"|indent)"; else fail "gofumpt failed:
$(printf '%s\n' "$out"|indent)"; fi
elif ! have goimports; then
  if out="$(gofmt -l cmd internal 2>&1)"; then [[ -z "$out" ]] && ok "gofmt: all files formatted" || fail "gofmt: needs formatting:
$(printf '%s\n' "$out"|indent)"; else fail "gofmt failed:
$(printf '%s\n' "$out"|indent)"; fi
fi
if have goimports; then
  if out="$(goimports -l -local sayumi cmd internal 2>&1)"; then [[ -z "$out" ]] && ok "goimports: imports organized" || fail "goimports: needs organizing:
$(printf '%s\n' "$out"|indent)"; else fail "goimports failed:
$(printf '%s\n' "$out"|indent)"; fi
else skip "goimports (go install golang.org/x/tools/cmd/goimports@v0.46.0)"; fi

step "4. Frontend formatting"
if [[ -z "$JS" ]]; then fail "prettier requires bun or npm"
elif out="$(cd frontend && "$JS" run format:check 2>&1)"; then ok "prettier: all files formatted"
else fail "prettier failed (run: cd frontend && $JS run format):
$(printf '%s\n' "$out"|indent)"; fi

step "5. go vet"
if out="$(go vet ./... 2>&1)"; then ok "no issues"; else fail "issues:
$(printf '%s\n' "$out"|indent)"; fi

step "6. golangci-lint"
if have golangci-lint; then
  if out="$(golangci-lint run ./... --timeout=5m 2>&1)"; then ok "no issues"; else fail "issues:
$(printf '%s\n' "$out"|indent)"; fi
else skip "golangci-lint (go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2)"; fi

step "7. govulncheck"
if have govulncheck; then
  if out="$(govulncheck ./... 2>&1)"; then ok "no known vulnerabilities"; else fail "vulnerabilities or scan failure:
$(printf '%s\n' "$out"|indent)"; fi
else skip "govulncheck (go install golang.org/x/vuln/cmd/govulncheck@v1.3.0)"; fi

step "8. go test"
race_flag=""; if [[ "$(go env CGO_ENABLED)" == "1" ]] && { have cc || have gcc || have clang; }; then race_flag="-race"; else warn "race detector skipped (cgo unavailable); running plain go test"; fi
out="$(go test $race_flag ./... 2>&1)"; status=$?; printf '%s\n' "$out"|sed 's/^/   /'; [[ $status -eq 0 ]] && ok "tests passed" || fail "tests failed"

step "9. frontend tests"
if [[ -z "$JS" ]]; then fail "frontend checks require bun or npm"
else
  if out="$(cd frontend && "$JS" run check 2>&1)"; then ok "svelte-check: no type/a11y issues"; else fail "svelte-check failed:
$(printf '%s\n' "$out"|indent)"; fi
  if out="$(cd frontend && "$JS" run test 2>&1)"; then ok "vitest: tests passed"; else fail "vitest failed:
$(printf '%s\n' "$out"|indent)"; fi
fi

if [[ "$FAST" -eq 0 ]]; then
  step "10. Builds"
  if [[ "$frontend_built" -eq 1 ]]; then ok "frontend build (already built in step 1)"
  elif [[ -z "$JS" ]]; then fail "frontend build requires bun or npm"
  elif out="$(cd frontend && "$JS" run build 2>&1)"; then ok "frontend build"; else fail "frontend build failed:
$(printf '%s\n' "$out"|indent)"; fi
  if out="$(CGO_ENABLED=0 go build -o /dev/null ./cmd/sayumi 2>&1)"; then ok "go build"; else fail "go build failed:
$(printf '%s\n' "$out"|indent)"; fi
fi
echo; [[ $overall -eq 0 ]] && echo "${green}${bold}✅ all checks passed${reset}" || echo "${red}${bold}❌ some checks failed${reset}"; exit $overall
