#!/usr/bin/env bash
#
# Classifies a diff for the `classify` job in .gitlab-ci.yml. Prints exactly
# two lines, in this order:
#
#   DOCS_ONLY=true|false           every changed path is documentation (#153)
#   LIVE_PINS_CHANGED=true|false   a pin the live install runs on moved (#164)
#
# Always exits 0: the caller writes both lines to a dotenv artifact, and a
# non-zero exit here would fail the pipeline rather than gate it.
#
# Fail-safe in both directions, which point opposite ways. Every question this
# script cannot answer with certainty means *more* testing, not less: no base
# commit, an unreadable diff, an empty file list or an unrecognised path give
# DOCS_ONLY=false, and the same faults give LIVE_PINS_CHANGED=true. A gate that
# fails toward skipping is worse than no gate, because it looks green.
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

# The variables the live install actually runs on: the image it installs
# inside, and the Go tarball it fetches. A change to any of these cannot be
# proven by any other job, because nothing else installs anything (#164).
LIVE_PIN_KEYS='UBUNTU_IMAGE|GO_VERSION|GO_SHA256'

docs_only=false
live_pins=true

report() {
  printf 'DOCS_ONLY=%s\n' "$docs_only"
  printf 'LIVE_PINS_CHANGED=%s\n' "$live_pins"
  exit 0
}

base="${1:-}"
head="${2:-}"

if [ -z "$base" ] || [ -z "$head" ]; then
  echo "classify: no base or head commit, so the full gate runs" >&2
  report
fi

if ! changed="$(git diff --name-only "$base" "$head" 2>/dev/null)"; then
  echo "classify: could not diff $base..$head, so the full gate runs" >&2
  report
fi

if [ -z "$changed" ]; then
  echo "classify: the diff is empty, so the full gate runs" >&2
  report
fi

# Documentation-only, path by path.
docs_only=true
while IFS= read -r path; do
  [ -n "$path" ] || continue
  if ! is_safe_path "$path"; then
    echo "classify: $path is not documentation, so the full gate runs" >&2
    docs_only=false
    break
  fi
done <<EOF
$changed
EOF

if [ "$docs_only" = true ]; then
  echo "classify: every changed path is documentation" >&2
fi

# Did a pin the live install runs on move? Line-level, not file-level: the
# pipeline definition is edited far more often than these three lines are, and
# the point is to charge an ordinary CI edit nothing.
live_pins=false
if ! pin_diff="$(git diff -U0 "$base" "$head" -- .gitlab-ci.yml 2>/dev/null)"; then
  echo "classify: could not read the pipeline diff, so the live install runs" >&2
  live_pins=true
elif printf '%s\n' "$pin_diff" \
  | grep -qE "^[-+][[:space:]]*($LIVE_PIN_KEYS):"; then
  echo "classify: a pin the live install runs on moved, so it runs" >&2
  live_pins=true
else
  echo "classify: no pin the live install runs on moved" >&2
fi

report
