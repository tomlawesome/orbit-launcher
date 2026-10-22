#!/bin/sh
set -eu

# Checks the release buttons (release:patch, release:minor) need before it is
# safe to cut a tag (#178, #264).
# Ported from gauntlet's scripts/release-version-check.sh; here the version
# comes from tools/calculateversion rather than a tracked VERSION file.
#
#   1. VERSION is a strict three-part semantic version, no leading zeros.
#   2. Its tag does not exist yet, and it sorts above every v* tag already
#      cut -- calculateversion reads local tags, so this is checked again
#      against the remote, which is the one that counts.
#   3. The commit being tagged carries no v* tag yet: one tag per commit, so
#      a second press of either button on the same pipeline cuts nothing.
#   4. The commit being tagged is the current tip of BRANCH (main in CI), so
#      an old pipeline's button can never tag a commit the branch has moved
#      past.
#
# Pure string logic, no network calls, so scripts/test/release-tag-check.bats
# can feed it canned input offline. POSIX sh, not bash: the tagging job runs
# it again in the release-cli image, which has no bash.
#
# Usage:
#   release-tag-check.sh VERSION REMOTE_TAGS BRANCH TIP_SHA COMMIT_SHA
#
#   VERSION      tools/calculateversion's output, e.g. "0.3.0"
#   REMOTE_TAGS  output of `git ls-remote --tags origin` (may be empty)
#   BRANCH       the branch the tag is cut from, "main"; named in messages
#   TIP_SHA      sha of origin/BRANCH, from `git ls-remote origin refs/heads/BRANCH`
#   COMMIT_SHA   the commit being tagged, $CI_COMMIT_SHA
#
# On success prints "RELEASE_TAG=v<VERSION>" and exits 0.

if [ "$#" -ne 5 ]; then
  echo "usage: release-tag-check.sh VERSION REMOTE_TAGS BRANCH TIP_SHA COMMIT_SHA" >&2
  exit 1
fi

version="$1"
remote_tags="$2"
branch="$3"
tip_sha="$4"
commit_sha="$5"

num='(0|[1-9][0-9]*)'
nl='
'
case "$version" in *"$nl"*) version_ok=false ;; *) version_ok=true ;; esac
if [ "$version_ok" = false ] || ! printf '%s' "$version" | grep -qE "^${num}\.${num}\.${num}$"; then
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

# An annotated tag shows twice in ls-remote: the plain line holds the tag
# object's sha, the peeled `^{}` line the commit it points at. A lightweight
# tag has only the plain line, holding the commit. So a tag's commit is its
# peeled sha when it has one, its plain sha otherwise.
already_tagged="$(printf '%s\n' "$remote_tags" | awk -v commit="$commit_sha" '
  {
    name = $2
    if (substr(name, 1, 10) != "refs/tags/") next
    name = substr(name, 11)
    peeled = 0
    if (length(name) > 3 && substr(name, length(name) - 2) == "^{}") {
      name = substr(name, 1, length(name) - 3)
      peeled = 1
    }
    if (name !~ /^v[0-9]+\.[0-9]+\.[0-9]+$/) next
    if (peeled) peeled_sha[name] = $1; else plain_sha[name] = $1
  }
  END {
    for (name in plain_sha) if (!(name in peeled_sha) && plain_sha[name] == commit) print name
    for (name in peeled_sha) if (peeled_sha[name] == commit) print name
  }' | sort -V | tail -n 1)"

if [ -n "$already_tagged" ]; then
  echo "commit $commit_sha is already tagged $already_tagged -- one tag per commit; nothing to cut. To cut another version, land a new commit on $branch first." >&2
  exit 1
fi

if [ -z "$tip_sha" ] || [ "$commit_sha" != "$tip_sha" ]; then
  echo "commit $commit_sha is not the current tip of $branch (${tip_sha:-unknown}) -- press the button on the newest $branch pipeline." >&2
  exit 1
fi

echo "RELEASE_TAG=$tag"
