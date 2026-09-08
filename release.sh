#!/usr/bin/env bash
#
# release.sh — cross-compile portable, distributable binaries for all targets.
#
# Unlike build.sh (which tunes for THIS machine), release builds use the baseline
# instruction set so they run on any CPU supported by Go for that architecture.
# SOURCE_DATE_EPOCH pins timestamps; reproducible bytes also require identical
# source, frontend dependencies, and Go/Bun/resource/archive tool versions.
#
#   ./release.sh                 # build every target into ./dist-release/
#   ./release.sh linux/amd64     # build only the given target(s)
#
set -euo pipefail
cd "$(dirname "$0")"
export LC_ALL=C TZ=UTC
umask 022

# Validate the entire request before building or replacing any output. These
# values become path components; accepting arbitrary os/arch text permits path
# traversal as well as accidental, unsupported release configurations.
TARGETS=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)
[[ $# -gt 0 ]] && TARGETS=("$@")
seen=" "
windows=0
unix=0
for tgt in "${TARGETS[@]}"; do
  case "$tgt" in
    linux/amd64|linux/arm64|darwin/amd64|darwin/arm64) unix=1 ;;
    windows/amd64|windows/arm64) windows=1 ;;
    *) echo "error: unsupported target: $tgt" >&2; exit 2 ;;
  esac
  if [[ "$seen" == *" $tgt "* ]]; then echo "error: duplicate target: $tgt" >&2; exit 2; fi
  seen+="$tgt "
done

# package.json's build command itself uses Bun; npm is not a Bun-free fallback.
for tool in bun go sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: required tool not found: $tool" >&2; exit 1; }
done
TAR=tar
if [[ "$unix" == 1 ]]; then
  # BSD tar lacks the deterministic metadata flags used below. macOS users can
  # install GNU tar as gtar without changing their system tar.
  if ! tar --version 2>/dev/null | grep -q 'GNU tar'; then TAR=gtar; fi
  "$TAR" --version 2>/dev/null | grep -q 'GNU tar' || { echo "error: GNU tar (tar or gtar) is required" >&2; exit 1; }
fi
if [[ "$windows" == 1 ]]; then
  for tool in zip go-winres; do
    command -v "$tool" >/dev/null 2>&1 || { echo "error: required tool not found: $tool" >&2; exit 1; }
  done
fi
FONTS_SRC="fonts-bundle"
[[ -d "$FONTS_SRC" ]] || { echo "error: fonts-bundle is required for release payloads" >&2; exit 1; }

OUT="dist-release"
[[ ! -L "$OUT" ]] || { echo "error: dist-release must not be a symlink" >&2; exit 1; }
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"

# One source-derived epoch controls the binary buildDate and archive mtimes.
if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then SOURCE_DATE_EPOCH="$(git log -1 --pretty=%ct 2>/dev/null || true)"; fi
if [[ ! "$SOURCE_DATE_EPOCH" =~ ^[0-9]+$ ]]; then echo "error: SOURCE_DATE_EPOCH must be a non-negative integer (or available from git)" >&2; exit 1; fi
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

echo "▸ building frontend (bun)…"
( cd frontend && bun run build )
[[ -s cmd/sayumi/dist/index.html ]] || { echo "error: frontend build did not produce cmd/sayumi/dist/index.html" >&2; exit 1; }

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
    --arch amd64,arm64 --file-version=git-tag --product-version=git-tag
fi

echo "▸ version $VERSION (SOURCE_DATE_EPOCH=$SOURCE_DATE_EPOCH -> $DATE)"
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
    go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$stage/sayumi${ext}" "$build_package"

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

  # Normalize modes and mtimes rather than inheriting a caller's umask or font
  # checkout permissions. The executable must remain executable after unpacking.
  find "$stage" -type d -exec chmod 755 {} +
  find "$stage" -type f -exec chmod 644 {} +
  chmod 755 "$stage/sayumi${ext}"
  if find "$stage" -print0 | xargs -0 touch -d "@${SOURCE_DATE_EPOCH}" 2>/dev/null; then :
  else
    touch_stamp="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y%m%d%H%M.%S 2>/dev/null || date -u -r "${SOURCE_DATE_EPOCH}" +%Y%m%d%H%M.%S 2>/dev/null)" || { echo "error: cannot derive staging timestamp" >&2; exit 1; }
    find "$stage" -exec touch -t "$touch_stamp" {} + || { echo "error: failed to normalize staging timestamps" >&2; exit 1; }
  fi

  # Stable entry order, fixed mtimes, zeroed owner/group, and timestamp-free gzip.
  ( cd "$OUT/_stage"
    if [[ "$os" == "windows" ]]; then
      find "sayumi-${os}-${arch}" -print | sort | \
        zip -X -q "../sayumi-${os}-${arch}.zip" -@
    else
      # MSYS/NTFS can report ELF/Mach-O files as 0644 even after chmod.
      # GNU tar applies --mode globally, so append the executable separately
      # with its explicit mode instead of making fonts and docs executable.
      tar_meta=(--sort=name --mtime="@${SOURCE_DATE_EPOCH}" --owner=0 --group=0 --numeric-owner)
      "$TAR" "${tar_meta[@]}" --mode='u=rwX,go=rX' \
          --exclude="sayumi-${os}-${arch}/sayumi" \
          -cf "sayumi-${os}-${arch}.tar" "sayumi-${os}-${arch}"
      "$TAR" "${tar_meta[@]}" --mode=0755 \
          -rf "sayumi-${os}-${arch}.tar" "sayumi-${os}-${arch}/sayumi"
      gzip -n -9 < "sayumi-${os}-${arch}.tar" > "../sayumi-${os}-${arch}.tar.gz"
      rm -f "sayumi-${os}-${arch}.tar"
    fi
  )
done
rm -rf "$OUT/_stage"

echo "▸ checksums"
( cd "$OUT" && sha256sum --binary sayumi-* > SHA256SUMS.tmp && mv SHA256SUMS.tmp SHA256SUMS )

echo "✓ release artifacts in ./$OUT/"
ls -1 "$OUT"
