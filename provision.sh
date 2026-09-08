#!/usr/bin/env bash
# provision.sh — explicit dependency/tool setup shared by contributors and CI.
set -euo pipefail

usage() {
  printf 'Usage: bash ./provision.sh {frontend|quality-tools|release-tools|check-bun}\n'
}
[[ $# -eq 1 ]] || { usage >&2; exit 2; }
case "$1" in
  --help|-h) usage; exit 0 ;;
  frontend|quality-tools|release-tools|check-bun) ;;
  *) usage >&2; exit 2 ;;
esac
cd "$(dirname "$0")"

fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
require_tool() { command -v "$1" >/dev/null 2>&1 || fail "missing required tool: $1 (see README.md for setup)"; }

check_bun() {
  require_tool bun
  # setup-bun reads this same field. Do not install/switch a local toolchain;
  # --version alone can hide a canary's prerelease tag.
  local pin revision
  if ! pin="$(bun -p 'require("./frontend/package.json").packageManager' 2>&1)"; then
    fail "cannot read Bun pin from frontend/package.json: $pin"
  fi
  if [[ ! "$pin" =~ ^bun@(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    fail "frontend/package.json packageManager must pin an exact stable Bun release (got '$pin')"
  fi
  if ! revision="$(bun --revision 2>&1)"; then
    fail "cannot read Bun revision: $revision"
  fi
  if [[ "${revision%%+*}" != "${pin#bun@}" ]]; then
    fail "expected $pin from frontend/package.json; found '$revision' (select the pinned Bun explicitly; see README.md)"
  fi
  printf '%s (%s)\n' "$pin" "$revision"
}

case "$1" in
  check-bun) check_bun ;;
  frontend)
    # Bun's --frozen-lockfile alone accepts an absent lockfile. Never resolve
    # fresh versions in setup/CI; intentional updates belong in a reviewed diff.
    [[ -s frontend/bun.lock ]] || fail "frontend/bun.lock is missing or empty; restore the committed lockfile before provisioning"
    check_bun
    (cd frontend && bun install --frozen-lockfile --ignore-scripts)
    ;;
  quality-tools|release-tools)
    require_tool go
    # Version-suffixed installs keep each tool's module graph independent of
    # the application and of other tools (notably golangci-lint). A shared
    # go.mod 'tool' graph can silently substitute their transitive dependencies.
    # Keep pins here only; callers must not copy them or use @latest.
    export GOTOOLCHAIN=local
    if [[ "$1" == quality-tools ]]; then
      go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
      go install golang.org/x/vuln/cmd/govulncheck@v1.7.0
      go install mvdan.cc/gofumpt@v0.11.0
      go install golang.org/x/tools/cmd/goimports@v0.49.0
    else
      go install github.com/tc-hib/go-winres@v0.3.3
    fi
    ;;
esac
