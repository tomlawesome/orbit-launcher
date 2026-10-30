#!/usr/bin/env bash
# quarantine-skip.sh [FILE]
#
# Prints the regular expression that matches every quarantined Go test, for
# `go test -skip` (or `-run` in the quarantine run, or `-test.skip` on a
# compiled test binary). FILE defaults to scripts/quarantine.txt: one test name
# per line, blank lines and # comments ignored. Prints an empty line when
# nothing is quarantined, so callers pass the flag only if the output is not
# empty.
#
# The same file is the flake-check job's --quarantined list, so the tests that
# are skipped and the tests the flake check believes are skipped come from one
# place (#310). Names are escaped so characters such as `(` or `.` match
# literally. Tested by scripts/test/quarantine-skip.bats.
set -euo pipefail

file="${1:-$(dirname "$0")/../quarantine.txt}"
if [ ! -r "$file" ]; then
  echo "quarantine-skip: cannot read $file" >&2
  exit 2
fi

{ grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$file" || true; } \
  | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/[][\\.*+?(){}|^$]/\\&/g' \
  | paste -sd'|' -
