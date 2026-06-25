#!/usr/bin/env bash
#
# build.sh — local optimized build for THIS machine.
#
# Produces a single self-contained ./sayumi binary with the frontend embedded.
# Tuned for the local CPU (GOAMD64=v3) — NOT portable to older amd64 chips; use
# release.sh for distributable, run-anywhere binaries.
#
# Usage:
#   ./build.sh            # build optimized ./sayumi
#   ./build.sh --run      # build, then run it
#   ./build.sh --skip-web # reuse the existing embedded frontend (skip vite)
#
set -euo pipefail

cd "$(dirname "$0")"

BIN="sayumi"
RUN=0
SKIP_WEB=0
for arg in "$@"; do
  case "$arg" in
    --run) RUN=1 ;;
    --skip-web) SKIP_WEB=1 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

# Pick a JS package runner (bun preferred, npm fallback).
if command -v bun >/dev/null 2>&1; then
  JS=bun
elif command -v npm >/dev/null 2>&1; then
  JS=npm
else
  echo "error: need bun or npm to build the frontend" >&2
  exit 1
fi

# Version metadata stamped into the binary (best-effort; works without git).
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# ---- frontend ----
if [[ "$SKIP_WEB" -eq 0 ]]; then
  echo "▸ building frontend ($JS)…"
  ( cd frontend && "$JS" run build )
else
  echo "▸ skipping frontend build (reusing embedded dist)"
fi

# Guard: the //go:embed dist directive needs a non-empty dist.
if [[ ! -e cmd/sayumi/dist/index.html ]]; then
  echo "error: cmd/sayumi/dist/index.html missing — run without --skip-web first" >&2
  exit 1
fi

# ---- decide GOAMD64 level ----
# v3 (AVX2/BMI2/FMA) gives the Go runtime faster memmove/memclr/math on capable
# CPUs. Detect support and fall back to the safe baseline otherwise. CPU flags
# live in /proc/cpuinfo on Linux and in sysctl on macOS (AVX2/BMI2 under
# leaf7_features, FMA under features); read whichever exists. Any other host
# (or a missing flag) keeps the baseline default so the binary stays runnable.
GOAMD64_LEVEL=""
if [[ "$(go env GOARCH)" == "amd64" ]]; then
  cpuflags=""
  if [[ -r /proc/cpuinfo ]]; then
    cpuflags="$(grep -m1 -i '^flags' /proc/cpuinfo 2>/dev/null || true)"
  elif command -v sysctl >/dev/null 2>&1; then
    cpuflags="$(sysctl -n machdep.cpu.features machdep.cpu.leaf7_features 2>/dev/null || true)"
  fi
  if grep -qiw avx2 <<<"$cpuflags" \
     && grep -qiw bmi2 <<<"$cpuflags" \
     && grep -qiw fma <<<"$cpuflags"; then
    GOAMD64_LEVEL="v3"
  fi
fi

# ---- go build ----
# -s -w     strip symbol table + DWARF (smaller binary)
# -trimpath remove local filesystem paths (reproducible, no path leakage)
LDFLAGS="-s -w -X main.version=${VERSION} -X main.buildDate=${DATE}"

echo "▸ building $BIN  (version=$VERSION${GOAMD64_LEVEL:+, GOAMD64=$GOAMD64_LEVEL})…"
export CGO_ENABLED=0
[[ -n "$GOAMD64_LEVEL" ]] && export GOAMD64="$GOAMD64_LEVEL"
go build -trimpath -ldflags "$LDFLAGS" -o "$BIN" ./cmd/sayumi

SIZE="$(du -h "$BIN" | cut -f1)"
echo "✓ built ./$BIN ($SIZE)"

if [[ "$RUN" -eq 1 ]]; then
  echo "▸ running ./$BIN…"
  exec "./$BIN"
fi
