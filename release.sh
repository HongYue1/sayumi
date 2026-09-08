#!/usr/bin/env bash
#
# release.sh — cross-compile portable, distributable binaries for all targets.
#
# Unlike a developer's optional CPU tuning, releases always use the baseline
# instruction set so they run on any CPU supported by Go for that architecture.
# SOURCE_DATE_EPOCH pins timestamps; reproducible bytes also require identical
# source, frontend dependencies, and Go/Bun/resource tool versions.
#
#   ./release.sh                 # build every target into ./dist-release/
#   ./release.sh linux/amd64     # build only the given target(s)
#
set -euo pipefail
cd "$(dirname "$0")"
export LC_ALL=C TZ=UTC GOTOOLCHAIN=local
# Release inputs are explicit, not a caller's development workspace/flags.
export GOWORK=off GOFLAGS= GOEXPERIMENT=
umask 022

# Validate the entire request before building or replacing any output. These
# values become path components; accepting arbitrary os/arch text permits path
# traversal as well as accidental, unsupported release configurations.
TARGETS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)
[[ $# -gt 0 ]] && TARGETS=("$@")
seen=" "
windows=0
for tgt in "${TARGETS[@]}"; do
  case "$tgt" in
    linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) ;;
    windows/amd64|windows/arm64) windows=1 ;;
    *) echo "error: unsupported target: $tgt" >&2; exit 2 ;;
  esac
  if [[ "$seen" == *" $tgt "* ]]; then echo "error: duplicate target: $tgt" >&2; exit 2; fi
  seen+="$tgt "
done

# package.json's build command itself uses Bun; npm is not a Bun-free fallback.
for tool in bun go; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: required tool not found: $tool" >&2; exit 1; }
done
if [[ "$windows" == 1 ]]; then
  for tool in go-winres; do
    command -v "$tool" >/dev/null 2>&1 || { echo "error: required tool not found: $tool" >&2; exit 1; }
  done
fi
FONTS_SRC="fonts-bundle"
[[ -d "$FONTS_SRC" && ! -L "$FONTS_SRC" ]] || { echo "error: fonts-bundle must be a real directory" >&2; exit 1; }
# Reject links before copying or writing Fonts/README.txt into a staged tree.
font_links="$(find "$FONTS_SRC" -type l -print)"
[[ -z "$font_links" ]] || { echo "error: fonts-bundle must not contain symlinks" >&2; exit 1; }
[[ ! -L cmd/sayumi/dist ]] || { echo "error: frontend dist must not be a symlink" >&2; exit 1; }

OUT="dist-release"
[[ ! -L "$OUT" ]] || { echo "error: dist-release must not be a symlink" >&2; exit 1; }
# A tagged workflow supplies its exact tag; local builds use git describe.
VERSION="${RELEASE_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
[[ "$VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z.+_-]*$ ]] || { echo "error: invalid release version: $VERSION" >&2; exit 1; }

# One source-derived epoch controls the binary buildDate and archive mtimes.
if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then SOURCE_DATE_EPOCH="$(git log -1 --pretty=%ct 2>/dev/null || true)"; fi
if [[ ! "$SOURCE_DATE_EPOCH" =~ ^(0|[1-9][0-9]{0,11})$ ]] || (( SOURCE_DATE_EPOCH > 253402300799 )); then
  echo "error: SOURCE_DATE_EPOCH must be a canonical integer from 0 through 253402300799" >&2; exit 1
fi
if [[ "$windows" == 1 ]] && (( SOURCE_DATE_EPOCH < 315532800 || SOURCE_DATE_EPOCH > 4294967295 )); then
  echo "error: Windows ZIP timestamps require an epoch from 315532800 through 4294967295" >&2; exit 1
fi
export SOURCE_DATE_EPOCH
if DATE="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"; then :
elif DATE="$(date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"; then :
else echo "error: cannot convert SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH" >&2; exit 1; fi
LDFLAGS="-s -w -X main.version=${VERSION} -X main.buildDate=${DATE}"

mkdir -p "$OUT"
# A failed cleanup must abort, not mix old artifacts into a new release. Remove
# the old checksum manifest first; only a complete build may install a new one.
rm -f "$OUT/SHA256SUMS" "$OUT/SHA256SUMS.tmp"
rm -rf "$OUT"/sayumi-* "$OUT/_stage"
mkdir -p "$OUT/_stage"
cleanup() {
  local status=$?
  trap - EXIT
  if ! rm -rf "$OUT/_stage"; then
    echo "error: could not remove release staging" >&2
    [[ "$status" != 0 ]] || status=1
  fi
  if [[ "$status" != 0 ]]; then rm -f "$OUT/SHA256SUMS" || true; fi
  exit "$status"
}
trap cleanup EXIT

# Compile the stdlib-only archiver for the HOST, once, with the selected Go.
HOST_OS="$(go env GOHOSTOS)"; HOST_ARCH="$(go env GOHOSTARCH)"
PACKER="$OUT/_stage/release-archive"
[[ "$HOST_OS" != windows ]] || PACKER+=.exe
env CGO_ENABLED=0 GOOS="$HOST_OS" GOARCH="$HOST_ARCH" GOAMD64=v1 GOARM64=v8.0 \
  go build -mod=readonly -trimpath -buildvcs=false -o "$PACKER" ./tools/release-archive

echo "▸ building frontend (bun)…"
rm -f cmd/sayumi/dist/index.html
( cd frontend && bun run build )
[[ -f cmd/sayumi/dist/index.html && -s cmd/sayumi/dist/index.html && ! -L cmd/sayumi/dist/index.html ]] || { echo "error: frontend build did not produce cmd/sayumi/dist/index.html" >&2; exit 1; }

# The Go linker does not honor overlays for .syso inputs. Stage the small main
# package instead: fresh resources for BOTH Windows architectures, without
# rewriting the committed .syso or leaving an untracked one in cmd/sayumi.
# Keep staging inside this module so imports of sayumi/internal remain legal.
WINDOWS_SRC="$OUT/_stage/windows-src"
if [[ "$windows" == 1 ]]; then
  mkdir -p "$WINDOWS_SRC"
  for source in cmd/sayumi/*.go; do
    if [[ "$source" != *_test.go ]]; then cp "$source" "$WINDOWS_SRC/"; fi
  done
  cp -R cmd/sayumi/dist "$WINDOWS_SRC/dist"
  echo "▸ go-winres make (icon/metadata for windows amd64+arm64)…"
  go-winres make --in cmd/sayumi/winres/winres.json --out "$WINDOWS_SRC/rsrc" \
    --arch amd64,arm64 --file-version="$VERSION" --product-version="$VERSION"
fi

echo "▸ version $VERSION (SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH -> $DATE)"
archives=()
for tgt in "${TARGETS[@]}"; do
  os="${tgt%/*}"; arch="${tgt#*/}"
  ext=""; [[ "$os" == "windows" ]] && ext=".exe"

  echo "  → $tgt"

  # Stage a clean payload: binary named plainly + Fonts/ + README.
  stage="$OUT/_stage/sayumi-${os}-${arch}"
  mkdir -p "$stage"
  # Version and buildDate come from -ldflags; do not leak checkout-specific VCS
  # state into otherwise identical builds. CPU settings override ambient tuning.
  arch_env=(); case "$arch" in amd64) arch_env+=("GOAMD64=v1") ;; arm64) arch_env+=("GOARM64=v8.0") ;; esac
  build_package="./cmd/sayumi"
  [[ "$os" != windows ]] || build_package="./$WINDOWS_SRC"
  env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "${arch_env[@]}" \
    go build -mod=readonly -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$stage/sayumi${ext}" "$build_package"

  cp -R "$FONTS_SRC" "$stage/Fonts"
  # Ship the drop-in font layout/convention guide inside ./Fonts itself.
  cat > "$stage/Fonts/README.txt" <<'FONTSREADME'
Sayumi - drop-in reading fonts (./Fonts/)
=========================================

Every subfolder of ./Fonts/ is one font family that appears in the reader's
font picker under Settings -> reading font -> "Your fonts". Two families
(Literata and Atkinson Hyperlegible Next) are built into the binary; everything
in here is an extra drop-in you can add to, remove, or replace freely.

Adding a family
---------------
Create a folder and drop in up to four font files, named by role. Any common
font format works - .woff2, .ttf or .otf:

    ./Fonts/<FamilyDir>/
        Regular.<ext>       (required)    <ext> = woff2, ttf or otf
        Bold.<ext>          (optional)
        Italic.<ext>        (optional)
        BoldItalic.<ext>    (optional)
        family.json         (optional metadata)

Then click "Rescan ./Fonts" in font settings - no restart needed.

Rules
-----
  * The folder name is the family id: letters and digits only, no spaces
    (e.g. "CrimsonPro", "SourceSerif4"). Use family.json "label" for the
    pretty name shown in the UI.
  * .woff2 is strongly preferred (smallest). .woff, .ttf and .otf also load.
  * Only the Regular file is required. Any missing role falls back to the nearest
    available file (the browser may synthesize the rest), so a single Regular
    file still works.

Variable fonts
--------------
If your font is a variable font with a weight axis, ship just:

    ./Fonts/<FamilyDir>/
        Regular.woff2       (the upright variable file)
        Italic.woff2        (optional, the italic variable file)
        family.json         with  "variable": true

One 100-900 face then provides regular AND real bold straight from the weight
axis (and Italic.woff2 covers italic + bold-italic). Do NOT also add
Bold.woff2 / BoldItalic.woff2 for a variable family.

family.json
-----------
All fields are optional:

    {
        "label":    "Crimson Pro",
        "category": "serif",
        "variable": false
    }

  * label    - display name in the picker (defaults to the folder name).
  * category - "serif", "sans-serif", "monospace" or "display" (grouping hint).
  * variable - true if Regular.woff2 is a weight-axis variable font (see above).
FONTSREADME
  cat > "$stage/README.txt" <<EOF
Sayumi — portable EPUB reader ($VERSION)

Run the 'sayumi' binary; it opens your browser automatically.
Your books live in a ./Library folder created next to the binary.

Fonts:
  Two reading fonts (Literata, Atkinson Hyperlegible Next) are built into the
  binary; Literata is the default. The ./Fonts folder beside this file adds
  more drop-in reading fonts. To add your own, drop a family folder into
  ./Fonts/ - see ./Fonts/README.txt for the exact layout - then click
  "Rescan ./Fonts" in the reader's font settings.
EOF

  # Normalize archive metadata directly; the staging filesystem's modes and
  # times are not portable, especially for ELF/Mach-O executables on NTFS.
  archive="sayumi-${os}-${arch}.tar.gz"
  [[ "$os" != windows ]] || archive="sayumi-${os}-${arch}.zip"
  "$PACKER" "$stage" "$OUT/$archive" "$SOURCE_DATE_EPOCH"
  archives+=("$archive")
done

echo "▸ checksums"
"$PACKER" checksums "$OUT" "${archives[@]}"
rm -rf "$OUT/_stage"

echo "✓ release artifacts in ./$OUT/"
ls -1 "$OUT"
