#!/usr/bin/env bash
# Cross-job handoff: reconstruct one Go directive change, never execute or apply
# files supplied by the build job. Both jobs check out the same github.sha.
set -euo pipefail
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
[[ $# -eq 1 && ( "$1" == verify || "$1" == prepare ) ]] || {
  echo 'Usage: go-toolchain-patch.sh {verify|prepare}' >&2
  exit 2
}
: "${NEWGO:?missing validated Go version}"
OLDGO="$(git show HEAD:go.mod | awk '$1 == "go" { sub(/\r$/, "", $2); print $2 }')"
# Bound numeric fields before Bash arithmetic, including outputs crossing jobs.
# No prereleases, minor changes, leading zeroes, overflow or tidy-only updates.
version='^1\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$'
[[ "$OLDGO" =~ $version && "$NEWGO" =~ $version ]] || fail 'invalid Go patch version'
[[ "${NEWGO%.*}" == "${OLDGO%.*}" ]] || fail 'Go minor changes require manual review'
(( 10#${NEWGO##*.} > 10#${OLDGO##*.} )) || fail 'Go patch must strictly increase'

render_module() {
  git show HEAD:go.mod | awk -v version="$NEWGO" '
    $1 == "go" { sub(/1\.[0-9]+\.[0-9]+/, version) }
    { print }
  '
}
# This blob is the equality contract between validated and published module
# content, not an artifact to unpack, a patch to apply, or executable job output.
EXPECTED="$(render_module | git hash-object --stdin)"
if [[ "$1" == verify ]]; then
  [[ -f go.mod && ! -L go.mod ]] || fail 'go.mod must remain a regular file'
  CHANGED="$(git diff --name-only HEAD --)"
  UNTRACKED="$(git ls-files --others --exclude-standard)"
  [[ "$CHANGED" == go.mod && -z "$UNTRACKED" ]] || fail 'only go.mod may change; other changes require manual review'
  [[ "$(git hash-object go.mod)" == "$EXPECTED" ]] || fail 'only the Go directive may change; dependency or toolchain edits require manual review'
  : "${GITHUB_OUTPUT:?missing workflow output file}"
  printf 'version=%s\nmod-blob=%s\n' "$NEWGO" "$EXPECTED" >> "$GITHUB_OUTPUT"
else
  : "${MOD_BLOB:?missing validated module blob}"
  [[ "$MOD_BLOB" =~ ^[a-f0-9]{40}$ && "$MOD_BLOB" == "$EXPECTED" ]] || fail 'validated module does not match this source/version'
  STATUS="$(git status --porcelain --untracked-files=all)"
  [[ -z "$STATUS" ]] || fail 'publisher requires a clean checkout'
  # The source is Git's immutable blob, not the destination file being written.
  render_module > go.mod
fi
