#!/usr/bin/env bash
# fix.sh — mutating auto-fix pass; run check.sh afterward.
set -uo pipefail
usage() {
  printf 'Usage: ./fix.sh [--go-format]\n  --go-format runs only goimports and gofumpt (make fmt).\n'
}
[[ $# -le 1 ]] || { usage >&2; exit 2; }
case "${1:-}" in
  --help|-h) usage; exit 0 ;;
  ""|--go-format) ;;
  *) usage >&2; exit 2 ;;
esac
cd "$(dirname "$0")" || exit 1
bold=$'\033[1m'; green=$'\033[32m'; red=$'\033[31m'; reset=$'\033[0m'; overall=0
if [[ ! -t 1 || -n "${NO_COLOR:-}" || "${TERM:-}" == dumb ]]; then
  bold=; green=; red=; reset=
fi
ok(){ echo "   ${green}✓${reset} $*"; }
fail(){ echo "   ${red}✗${reset} $*"; overall=1; }
step(){ echo; echo "${bold}$*${reset}"; }
have(){ command -v "$1" &>/dev/null; }
format_go() {
  # Imports first: goimports applies gofmt, so gofumpt must have the last word.
  goimports -w -local sayumi cmd internal && gofumpt -w cmd internal
}

# Check the entire requested tool set before the first mutation. A fallback to
# gofmt/npm or a skipped linter cannot satisfy the checks we tell users to run.
required=(goimports gofumpt)
if [[ "${1:-}" != --go-format ]]; then
  required+=(go bun golangci-lint)
fi
for tool in "${required[@]}"; do
  have "$tool" || fail "missing required tool: $tool (see README.md for setup)"
done
[[ $overall -eq 0 ]] || exit "$overall"
if [[ "${1:-}" == --go-format ]]; then
  format_go
  exit $?
fi

# Reuse the canonical Bun policy; never install or switch toolchains here.
export GOTOOLCHAIN=local
if out="$(bash ./provision.sh check-bun 2>&1)"; then ok "$out"
else fail "$out"; exit 1; fi

step "1. Frontend build (//go:embed dist)"
# Go fix and lint load packages. A fresh checkout has only dist/.gitkeep,
# which cannot satisfy go:embed; stop before changing Go sources if it fails.
if (cd frontend && bun run build); then
  if [[ -f cmd/sayumi/dist/index.html ]]; then ok "frontend build"
  else fail "frontend build produced no cmd/sayumi/dist/index.html"; exit 1; fi
else fail "frontend build failed"; exit 1; fi

step "2. Modernizations (go fix)"
if go fix ./...; then ok "go fix"; else fail "go fix failed"; fi
step "3. golangci-lint --fix"
if golangci-lint run ./... --fix --timeout=5m; then ok "lint --fix"
else fail "lint --fix failed or left non-fixable issues"; fi
step "4. Go imports & formatting"
if format_go; then ok "goimports + gofumpt"; else fail "Go formatting failed"; fi
step "5. Frontend lint --fix & formatting"
# Lint fixes change token positions, so oxfmt must re-wrap their result last.
if (cd frontend && bun run lint:fix); then ok "oxlint --fix"
else fail "oxlint --fix failed or left non-fixable issues"; fi
if (cd frontend && bun run format); then ok "oxfmt"
else fail "frontend formatting failed"; fi
step "6. go mod tidy"
if go mod tidy; then ok "tidy"; else fail "go mod tidy failed"; fi

echo
if [[ $overall -eq 0 ]]; then echo "${green}${bold}✓ fixes applied — now run ./check.sh${reset}"
else echo "${red}${bold}✗ some fixes failed — review diagnostics${reset}"; fi
exit "$overall"
