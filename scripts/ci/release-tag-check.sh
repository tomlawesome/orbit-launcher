#!/usr/bin/env bash
set -euo pipefail

# Checks release:version needs before it is safe to cut a tag (#178).
# Ported from gauntlet's scripts/release-version-check.sh; here the version
# comes from tools/calculateversion rather than a tracked VERSION file.
#
#   1. VERSION is a strict three-part semantic version, no leading zeros.
#   2. Its tag does not exist yet, and it sorts above every v* tag already
#      cut -- calculateversion reads local tags, so this is checked again
#      against the remote, which is the one that counts.
#   3. The commit being tagged is the current tip of dev, so an old
#      pipeline's button can never tag a commit dev has moved past.
#
# Pure string logic, no network calls, so scripts/test/release-tag-check.bats
# can feed it canned input offline.
#
# Usage:
#   release-tag-check.sh VERSION REMOTE_TAGS DEV_SHA COMMIT_SHA
#
#   VERSION      tools/calculateversion's output, e.g. "0.3.0"
#   REMOTE_TAGS  output of `git ls-remote --tags origin` (may be empty)
#   DEV_SHA      sha of origin/dev, from `git ls-remote origin refs/heads/dev`
#   COMMIT_SHA   the commit being tagged, $CI_COMMIT_SHA
#
# On success prints "RELEASE_TAG=v<VERSION>" and exits 0.

if [ "$#" -ne 4 ]; then
  echo "usage: release-tag-check.sh VERSION REMOTE_TAGS DEV_SHA COMMIT_SHA" >&2
  exit 1
fi

version="$1"
remote_tags="$2"
dev_sha="$3"
commit_sha="$4"

num='(0|[1-9][0-9]*)'
if ! [[ "$version" =~ ^${num}\.${num}\.${num}$ ]]; then
  echo "the next version came out as '$version', not a semantic version (X.Y.Z)" >&2
  exit 1
fi
tag="v$version"

existing_versions="$(printf '%s\n' "$remote_tags" \
  | grep -oE 'refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  | sed -E 's#refs/tags/v##' || true)"

if printf '%s\n' "$existing_versions" | grep -qx "$version"; then
  echo "$tag already exists on the remote -- nothing to cut." >&2
  exit 1
fi

if [ -n "$existing_versions" ]; then
  highest="$(printf '%s\n%s\n' "$existing_versions" "$version" | sort -V | tail -1)"
  if [ "$highest" != "$version" ]; then
    echo "$tag does not sort above the newest tag already cut ($highest)." >&2
    exit 1
  fi
fi

if [ -z "$dev_sha" ] || [ "$commit_sha" != "$dev_sha" ]; then
  echo "commit $commit_sha is not the current tip of dev (${dev_sha:-unknown}) -- press the button on the newest dev pipeline." >&2
  exit 1
fi

echo "RELEASE_TAG=$tag"
