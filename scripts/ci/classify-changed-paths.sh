#!/usr/bin/env bash
#
# Decides whether a diff is documentation-only, for the `classify` job in
# .gitlab-ci.yml (#153).
#
# Prints exactly one line, `DOCS_ONLY=true` or `DOCS_ONLY=false`, and always
# exits 0: the caller writes the line to a dotenv artifact, and a non-zero exit
# here would fail the pipeline rather than gate it.
#
# Fail-safe by construction. `false` is the answer to every question this
# script cannot answer with certainty: no base commit, a diff that will not
# read, an empty file list, or a single path that is not on the safe list
# below. Skipping is opt-in against that list and is never a fallback, because
# a gate that fails toward skipping is worse than no gate -- it looks green
# (#153, criterion 3).
#
# Usage: classify-changed-paths.sh <base-sha> <head-sha>

set -uo pipefail

# The only paths a documentation-only change may touch. Everything else --
# Go sources, the bootstrap script, go.mod, the pipeline's own definition,
# this script, the design sources the visual suite screenshots -- means the
# full gate. Add to this list only after asking what reads the path in CI.
is_safe_path() {
  case "$1" in
    *.md) return 0 ;;
    docs/*) return 0 ;;
    .agents/*) return 0 ;;
    LICENSE) return 0 ;;
  esac
  return 1
}

base="${1:-}"
head="${2:-}"

verdict() {
  printf 'DOCS_ONLY=%s\n' "$1"
  exit 0
}

if [ -z "$base" ] || [ -z "$head" ]; then
  echo "classify: no base or head commit, so the full gate runs" >&2
  verdict false
fi

if ! changed="$(git diff --name-only "$base" "$head" 2>/dev/null)"; then
  echo "classify: could not diff $base..$head, so the full gate runs" >&2
  verdict false
fi

if [ -z "$changed" ]; then
  echo "classify: the diff is empty, so the full gate runs" >&2
  verdict false
fi

while IFS= read -r path; do
  [ -n "$path" ] || continue
  if ! is_safe_path "$path"; then
    echo "classify: $path is not documentation, so the full gate runs" >&2
    verdict false
  fi
done <<EOF
$changed
EOF

echo "classify: every changed path is documentation" >&2
verdict true
