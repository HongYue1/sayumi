#!/usr/bin/env bash
# check.sh — source-read-only quality gates; refreshes the generated frontend.
set -uo pipefail
cd "$(dirname "$0")" || exit 1
FAST=0
for arg in "$@"; do
  case "$arg" in
    --fast) FAST=1 ;;
    --help|-h) printf 'Usage: ./check.sh [--fast]\n  --fast skips the final Go build, not frontend compilation or quality gates.\n'; exit 0 ;;
    *) printf 'unknown option: %s\n' "$arg" >&2; exit 2 ;;
  esac
done
bold=$'\033[1m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'; dim=$'\033[2m'; reset=$'\033[0m'
if [[ ! -t 1 || -n "${NO_COLOR:-}" || "${TERM:-}" == dumb ]]; then
  bold=; green=; yellow=; red=; dim=; reset=
fi
overall=0
ok(){ echo "   ${green}✓${reset} $*"; }; warn(){ echo "   ${yellow}⚠${reset} $*"; }
fail(){ echo "   ${red}✗${reset} $*"; overall=1; }; skip(){ echo "   ${dim}– skipped: $*${reset}"; }
step(){ echo; echo "${bold}$*${reset}"; }; have(){ command -v "$1" &>/dev/null; }; indent(){ sed 's/^/       /'; }

# Enforce prerequisites here, not only in CI or a task-runner wrapper: direct
# ./check.sh calls must never turn a missing formatter/linter/scanner into green.
# The Vite frame plugin uses Bun.build, so npm alone is not a fallback runtime.
step "Required tools"
for tool in go bun gofumpt goimports golangci-lint govulncheck; do
  have "$tool" || fail "missing required tool: $tool (see README.md for setup)"
done
[[ $overall -eq 0 ]] || exit "$overall"

step "1. Frontend build (//go:embed dist)"
# An existing index.html proves existence, not freshness. Build before any Go
# analysis/tests embed it, including --fast runs, and stop on prerequisite failure.
if out="$(cd frontend && bun run build 2>&1)"; then
  if [[ -f cmd/sayumi/dist/index.html ]]; then ok "frontend build"
  else fail "frontend build produced no cmd/sayumi/dist/index.html"; exit 1; fi
else fail "frontend build failed:
$(printf '%s\n' "$out"|indent)"; exit 1; fi

step "2. go.mod / go.sum tidy"
tidy_diff=""
if tidy_diff="$(go mod tidy -diff)"; then
  [[ -z "$tidy_diff" ]] && ok "tidy" || fail "go.mod/go.sum are not tidy — run ./fix.sh:
$(printf '%s\n' "$tidy_diff"|indent)"
else fail "go mod tidy -diff failed; see diagnostics above"; fi

step "3. Go formatting"
if out="$(gofumpt -l cmd internal 2>&1)"; then [[ -z "$out" ]] && ok "gofumpt: all files formatted" || fail "gofumpt: needs formatting:
$(printf '%s\n' "$out"|indent)"; else fail "gofumpt failed:
$(printf '%s\n' "$out"|indent)"; fi
if out="$(goimports -l -local sayumi cmd internal 2>&1)"; then [[ -z "$out" ]] && ok "goimports: imports organized" || fail "goimports: needs organizing:
$(printf '%s\n' "$out"|indent)"; else fail "goimports failed:
$(printf '%s\n' "$out"|indent)"; fi

step "4. Frontend formatting"
if out="$(cd frontend && bun run format:check 2>&1)"; then ok "oxfmt: all files formatted"
else fail "oxfmt failed (run: cd frontend && bun run format):
$(printf '%s\n' "$out"|indent)"; fi

step "5. go vet"
if out="$(go vet ./... 2>&1)"; then ok "no issues"; else fail "issues:
$(printf '%s\n' "$out"|indent)"; fi

step "6. golangci-lint"
if out="$(golangci-lint run ./... --timeout=5m 2>&1)"; then ok "no issues"; else fail "issues:
$(printf '%s\n' "$out"|indent)"; fi

step "7. govulncheck"
if out="$(govulncheck ./... 2>&1)"; then ok "no known vulnerabilities"; else fail "vulnerabilities or scan failure:
$(printf '%s\n' "$out"|indent)"; fi

step "8. go test (-race, -shuffle=on)"
# Go resolves CC, which may be a custom compiler command. Do not infer race
# support from cc/gcc/clang on PATH or silently retry a failed race run without it.
race_flag=""
if cgo="$(go env CGO_ENABLED 2>&1)"; then
  case "$cgo" in
    1) race_flag="-race" ;;
    0) warn "race detector skipped (CGO_ENABLED=0); running plain go test" ;;
    *) fail "invalid CGO_ENABLED reported by go env: $cgo" ;;
  esac
else fail "go env CGO_ENABLED failed:
$(printf '%s\n' "$cgo"|indent)"; cgo=""; fi
if [[ "$cgo" == 0 || "$cgo" == 1 ]]; then
  out="$(go test $race_flag -shuffle=on ./... 2>&1)"; status=$?; printf '%s\n' "$out"|sed 's/^/   /'; [[ $status -eq 0 ]] && ok "tests passed" || fail "tests failed"
fi

step "9. Frontend lint, types & tests"
if out="$(cd frontend && bun run lint 2>&1)"; then ok "oxlint: no issues"; else fail "oxlint failed:
$(printf '%s\n' "$out"|indent)"; fi
if out="$(cd frontend && bun run check 2>&1)"; then ok "tsc: no type errors"; else fail "tsc failed:
$(printf '%s\n' "$out"|indent)"; fi
if out="$(cd frontend && bun run test 2>&1)"; then ok "vitest: tests passed"; else fail "vitest failed:
$(printf '%s\n' "$out"|indent)"; fi

step "10. Builds"
ok "frontend build (already built in step 1)"
if [[ "$FAST" -eq 0 ]]; then
  if out="$(CGO_ENABLED=0 go build -o /dev/null ./cmd/sayumi 2>&1)"; then ok "go build"; else fail "go build failed:
$(printf '%s\n' "$out"|indent)"; fi
else skip "production Go build (--fast)"; fi
echo
if [[ $overall -ne 0 ]]; then echo "${red}${bold}❌ some checks failed${reset}"
elif [[ "$FAST" -eq 1 ]]; then echo "${green}${bold}✅ fast checks passed (production Go build not run)${reset}"
else echo "${green}${bold}✅ all checks passed${reset}"; fi
exit "$overall"
