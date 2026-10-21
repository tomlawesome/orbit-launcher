#!/usr/bin/env bash
# Runs the Go packages whose tests skip as root again, as an unprivileged
# user, so their read/open error branches are checked in CI (#195 T2-Q1).
#
# GitLab's `fast` job runs the suite as root in the Go image, and root reads a
# mode-0000 file, so every test that guards itself with `os.Geteuid() == 0`
# skips there. This script finds those packages, copies the tracked tree to a
# directory the unprivileged user owns (so ownership checks see the same
# layout a developer has), builds the test binaries as the caller with the
# caller's warm caches, and runs them as `nobody` through setpriv. Some
# suites build the launcher binary themselves (test/internal/launcher), so the
# unprivileged run gets a build cache of its own and reads the caller's module
# cache. Nothing is downloaded: every go command here runs with GOPROXY=off
# against the module cache the job already filled.
#
# Run as anyone else, it runs the same binaries as that user: still not root,
# which is the point. GitHub's runner is not root, so its own suite already
# covers these and it does not call this script.
#
# Usage: scripts/ci/nonroot-tests.sh
set -euo pipefail

cd "$(dirname "$0")/../.."

mapfile -t pkgs < <(git grep -l 'os.Geteuid() == 0' -- '*_test.go' | xargs -r -n1 dirname | sort -u)
if [ "${#pkgs[@]}" -eq 0 ]; then
  echo "nonroot-tests: no test skips as root any more; drop this step" >&2
  exit 1
fi

modcache="$(go env GOMODCACHE)"
work="$(mktemp -d)"
trap 'chmod -R u+w "$work" 2> /dev/null; rm -rf "$work"' EXIT
mkdir -p "$work/src" "$work/bin" "$work/home" "$work/gocache"
git ls-files -z | tar --null -T - -cf - | tar -xf - -C "$work/src"

run_as=()
if [ "$(id -u)" -eq 0 ]; then
  command -v setpriv > /dev/null || { echo "nonroot-tests: setpriv (util-linux) is missing from the image" >&2; exit 1; }
  chown -R 65534:65534 "$work"
  chmod 0755 "$work"
  run_as=(setpriv --reuid=65534 --regid=65534 --clear-groups --)
fi
if [ "$("${run_as[@]}" id -u)" -eq 0 ]; then
  echo "nonroot-tests: the tests would still run as root" >&2
  exit 1
fi
if ! "${run_as[@]}" test -r "$modcache/cache/download" -a -x "$modcache/cache/download"; then
  echo "nonroot-tests: uid $("${run_as[@]}" id -u) cannot read the module cache at $modcache" >&2
  exit 1
fi

for pkg in "${pkgs[@]}"; do
  name="${pkg//\//_}"
  echo "nonroot-tests: building ./$pkg"
  (cd "$work/src" && GOPROXY=off GOFLAGS=-mod=readonly go test -c -o "$work/bin/$name.test" "./$pkg")
done
chmod -R a+rX "$work/bin"

status=0
for pkg in "${pkgs[@]}"; do
  name="${pkg//\//_}"
  echo "nonroot-tests: running ./$pkg as uid $("${run_as[@]}" id -u)"
  (cd "$work/src/$pkg" && "${run_as[@]}" env HOME="$work/home" GOCACHE="$work/gocache" \
    GOMODCACHE="$modcache" GOPROXY=off GOFLAGS=-mod=readonly \
    "$work/bin/$name.test" -test.count=1 -test.timeout=10m) || status=1
done
exit "$status"
