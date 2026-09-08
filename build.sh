#!/usr/bin/env bash
# build.sh — local, CGO-free binary with a freshly embedded frontend.
# CPU tuning is explicit through Go's environment, not a partial host-flag probe.
# Use release.sh for the supported distributable archives.
set -euo pipefail

usage() {
  printf 'Usage: ./build.sh [--run] [--skip-web]\n  --run       build, then replace this process with the native binary\n  --skip-web  explicitly reuse the existing frontend (which may be stale)\n'
}
RUN=0
SKIP_WEB=0
for arg in "$@"; do
  case "$arg" in
    --run) RUN=1 ;;
    --skip-web) SKIP_WEB=1 ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'unknown option: %s\n' "$arg" >&2; usage >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")"
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
require_tool() { command -v "$1" >/dev/null 2>&1 || fail "missing required tool: $1 (see README.md for setup)"; }
require_tool go
[[ "$SKIP_WEB" -eq 1 ]] || require_tool bun
# Build never downloads or silently switches the selected compiler.
export GOTOOLCHAIN=local

# Keep the target executable suffix, including for an explicit cross-build.
BIN="sayumi$(go env GOEXE)"
if [[ "$RUN" -eq 1 ]]; then
  target="$(go env GOOS)/$(go env GOARCH)"
  host="$(go env GOHOSTOS)/$(go env GOHOSTARCH)"
  [[ "$target" == "$host" ]] || fail "--run requires a native target ($target selected; host is $host)"
fi

# Version metadata is best-effort, so source archives still build without Git.
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [[ "$SKIP_WEB" -eq 0 ]]; then
  echo "▸ building frontend (bun)…"
  # The package script owns Vite invocation and the shared Bun revision guard.
  # npm cannot replace the Bun runtime used by the iframe bundler.
  (cd frontend && bun run build)
else
  echo "▸ skipping frontend build (explicitly reusing potentially stale dist)"
fi

# go:embed needs real generated assets, not an empty file or a directory.
[[ -f cmd/sayumi/dist/index.html && -s cmd/sayumi/dist/index.html ]] || \
  fail "cmd/sayumi/dist/index.html missing or empty — run without --skip-web first"

# Honor Go's configured CPU baseline (GOAMD64 defaults to v1). AVX2/BMI2/FMA
# alone do not prove v3 support: Go also requires, for example, LZCNT and
# OSXSAVE. Host probing is especially wrong for an explicit cross-build.
# https://go.dev/wiki/MinimumRequirements#amd64
cpu=""
if [[ "$(go env GOARCH)" == amd64 ]]; then cpu=", GOAMD64=$(go env GOAMD64)"; fi
LDFLAGS="-s -w -X main.version=${VERSION} -X main.buildDate=${DATE}"
echo "▸ building $BIN (version=$VERSION$cpu)…"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$BIN" ./cmd/sayumi

SIZE="$(du -h "$BIN" | cut -f1)"
echo "✓ built ./$BIN ($SIZE)"
if [[ "$RUN" -eq 1 ]]; then
  echo "▸ running ./$BIN…"
  exec "./$BIN"
fi
