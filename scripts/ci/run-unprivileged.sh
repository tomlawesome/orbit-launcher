#!/usr/bin/env bash
# Runs one command as an unprivileged user over a copy of the tracked tree
# that user owns, because the launcher refuses to run as root (#291) and CI's
# containers run as root: GitLab's `fast`, `visual` and `live` jobs, and
# GitHub's Playwright `container:` (#292).
#
# The same pattern as scripts/ci/nonroot-tests.sh (#195): a copy of the
# tracked files the user owns (so the launcher's ownership checks see the
# layout a developer has, and git is never asked about a checkout someone else
# owns), setpriv to drop root, and Go with GOPROXY=off against the module
# cache the caller fills first, so the unprivileged run downloads nothing Go.
# The caller's build cache is handed to the user too: its dependency and
# standard-library entries do not depend on where the tree lives, so they stay
# warm. Run by anyone but root, it runs the command as that caller instead.
#
# Usage: scripts/ci/run-unprivileged.sh [--user NAME] [--chdir DIR]
#          [--keep PATH]... -- COMMAND [ARG...]
#   --user NAME  run as this existing account, with its own groups and home
#                (the live job's docker-group user); default uid 65534
#                (nobody) with no groups and a throwaway home
#   --chdir DIR  run in this folder of the copy (default: its root)
#   --keep PATH  copy PATH, relative to the repository root, back from the
#                copy afterwards, whether the command passed or not (reports
#                a job uploads)
# The command's exit status is the script's.
set -euo pipefail

user=""
chdir="."
keep=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --user) user="$2"; shift 2 ;;
    --chdir) chdir="$2"; shift 2 ;;
    --keep) keep+=("$2"); shift 2 ;;
    --) shift; break ;;
    *) echo "run-unprivileged: unknown option $1" >&2; exit 2 ;;
  esac
done
if [ "$#" -eq 0 ]; then
  echo "run-unprivileged: no command after --" >&2
  exit 2
fi

cd "$(dirname "$0")/../.."
repo="$PWD"

work="$(mktemp -d)"
trap 'chmod -R u+w "$work" 2> /dev/null; rm -rf "$work"' EXIT
mkdir -p "$work/src" "$work/home"
git ls-files -z | tar --null -T - -cf - | tar -xf - -C "$work/src"

# Every module the build and its tests need, fetched as the caller, so the
# GOPROXY=off run below finds them. Nothing is fetched when the cache is full.
go mod download
modcache="$(go env GOMODCACHE)"
gocache="$(go env GOCACHE)"
mkdir -p "$gocache"

run_as=()
home="$work/home"
if [ "$(id -u)" -eq 0 ]; then
  command -v setpriv > /dev/null || { echo "run-unprivileged: setpriv (util-linux) is missing from the image" >&2; exit 1; }
  if [ -n "$user" ]; then
    uid="$(id -u "$user")"
    gid="$(id -g "$user")"
    home="$(getent passwd "$user" | cut -d: -f6)"
    run_as=(setpriv --reuid="$uid" --regid="$gid" --init-groups --)
  else
    uid=65534
    gid=65534
    run_as=(setpriv --reuid="$uid" --regid="$gid" --clear-groups --)
  fi
  chown -R "$uid:$gid" "$work" "$gocache"
  chmod 0755 "$work"
fi
if [ "$("${run_as[@]}" id -u)" -eq 0 ]; then
  echo "run-unprivileged: the command would still run as root" >&2
  exit 1
fi
if ! "${run_as[@]}" test -r "$modcache/cache/download" -a -x "$modcache/cache/download"; then
  echo "run-unprivileged: uid $("${run_as[@]}" id -u) cannot read the module cache at $modcache" >&2
  exit 1
fi

echo "run-unprivileged: running $1 as uid $("${run_as[@]}" id -u) in $chdir"
status=0
(cd "$work/src/$chdir" && "${run_as[@]}" env HOME="$home" GOCACHE="$gocache" \
  GOMODCACHE="$modcache" GOPROXY=off GOFLAGS=-mod=readonly "$@") || status=$?

for path in "${keep[@]}"; do
  if [ -e "$work/src/$path" ]; then
    rm -rf "${repo:?}/$path"
    mkdir -p "$(dirname "$repo/$path")"
    cp -R "$work/src/$path" "$repo/$path"
  fi
done
exit "$status"
