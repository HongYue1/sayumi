#!/usr/bin/env bash
#
# release.sh — cross-compile portable, distributable binaries for all targets.
#
# Unlike build.sh (which tunes for THIS machine with GOAMD64=v3), release builds
# use the baseline instruction set so they run on any CPU of that arch.
#
#   ./release.sh                 # build every target into ./dist-release/
#   ./release.sh linux/amd64     # build only the given target(s)
#
set -euo pipefail
cd "$(dirname "$0")"

OUT="dist-release"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.buildDate=${DATE}"

if command -v bun >/dev/null 2>&1; then JS=bun; else JS=npm; fi

# All supported targets. Override by passing one or more "os/arch" args.
TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
  "windows/arm64"
)
[[ $# -gt 0 ]] && TARGETS=("$@")

echo "▸ building frontend ($JS)…"
( cd frontend && "$JS" run build )

# Windows resources (icon + version metadata). The repo ships prebuilt .syso for
# 386/amd64; regenerate for all arches if go-winres is available so arm64 also
# gets the icon. Either way the .syso are picked up automatically by `go build`.
if command -v go-winres >/dev/null 2>&1; then
  echo "▸ go-winres make (icon/metadata for all windows arches)…"
  ( cd cmd/sayumi && go-winres make --in winres/winres.json ) || \
    echo "  (go-winres failed; falling back to committed .syso)"
else
  echo "▸ go-winres not found — using committed .syso (windows/amd64 icon only)"
fi

mkdir -p "$OUT"
rm -rf "$OUT"/sayumi-* "$OUT"/_stage 2>/dev/null || true

# The drop-in reading fonts (the 7 not embedded in the binary) ship beside the
# executable as ./Fonts/, ready to use. fonts-bundle/ is the repo source.
FONTS_SRC="fonts-bundle"

echo "▸ version $VERSION"
for tgt in "${TARGETS[@]}"; do
  os="${tgt%/*}"; arch="${tgt#*/}"
  ext=""; [[ "$os" == "windows" ]] && ext=".exe"

  echo "  → $tgt"

  # Stage a clean payload: binary named plainly + Fonts/ + README.
  stage="$OUT/_stage/sayumi-${os}-${arch}"
  mkdir -p "$stage"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$stage/sayumi${ext}" ./cmd/sayumi

  if [[ -d "$FONTS_SRC" ]]; then
    cp -R "$FONTS_SRC" "$stage/Fonts"
  fi
  cat > "$stage/README.txt" <<EOF
Sayumi — portable EPUB reader ($VERSION)

Run the 'sayumi' binary; it opens your browser automatically.
Your books live in a ./Library folder created next to the binary.

Fonts:
  Two reading fonts (EB Garamond, Atkinson Hyperlegible) are built in.
  The ./Fonts folder beside this file adds more (Literata, Lora, Crimson Pro,
  Spectral, Ovo, Bookerly, Bitter). Drop your own font family into
  ./Fonts/<Name>/ (a folder with .woff2/.ttf/.otf files) to add it, then use
  "Rescan" in the reader's font settings.
EOF

  # Package from the staging dir so the archive unpacks to a single folder.
  ( cd "$OUT/_stage"
    if [[ "$os" == "windows" ]]; then
      zip -qr "../sayumi-${os}-${arch}.zip" "sayumi-${os}-${arch}"
    else
      tar -czf "../sayumi-${os}-${arch}.tar.gz" "sayumi-${os}-${arch}"
    fi
  )
done
rm -rf "$OUT/_stage"

echo "▸ checksums"
( cd "$OUT" && sha256sum sayumi-* > SHA256SUMS )

echo "✓ release artifacts in ./$OUT/"
ls -1 "$OUT"
