#!/usr/bin/env bash
# Runs the test suite with coverage and writes one profile, coverage.out,
# that counts both the unit tests and the compiled launcher test/pty runs
# under a real pty (#169).
#
# test/pty spawns the real binary, so its coverage never reached a plain
# `go test -coverprofile`: cmd/orbit-launcher read 23.5% when the pty
# suite exercises nearly all of it. With ORBIT_LAUNCHER_BINARY_COVERDIR
# set, test/pty builds that binary with -cover (see BuildBinary in
# test/internal/launcher), each run writes its counters there, and `go
# tool covdata` merges them with the unit tests' own.
#
# Atomic mode on both sides: -race needs it, and covdata cannot merge
# counters recorded in two modes.
#
# The launcher refuses to run as root (#291). Run as root (GitLab's `fast`
# job), the packages whose tests start the real binary -- the ones that
# import the test/internal/launcher harness -- run as an unprivileged user
# through scripts/ci/run-unprivileged.sh, and the rest run here as before, so
# the tests that need root still get it (#292). Their counters land in the
# same two directories and merge as one profile.
#
# Tests listed in scripts/quarantine.txt are skipped here (#310): they leave
# the merge gate once a flake has three sightings (docs/flakes.md).
#
# Usage: scripts/coverage.sh [extra go test flags, e.g. -race]
# Then:  go run ./tools/coveragefloor coverage.out
set -euo pipefail

cd "$(dirname "$0")/.."
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
mkdir -p "$dir/unit" "$dir/binary"

module=github.com/tomlawesome/orbit-launcher
cover=(-count=1 -cover -covermode=atomic "-coverpkg=$module/...")
pkgs=(./...)
# An empty pattern would skip every test, so the flag is added only for a name.
skip=()
quarantined="$(bash scripts/ci/quarantine-skip.sh)"
if [ -n "$quarantined" ]; then
  skip=(-skip "$quarantined")
fi
spawners=()
if [ "$(id -u)" -eq 0 ]; then
  mapfile -t spawners < <(go list -f '{{.ImportPath}}{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}' ./... \
    | awk -v h="$module/test/internal/launcher" '{ for (i = 2; i <= NF; i++) if ($i == h) { print $1; break } }')
  mapfile -t pkgs < <(go list ./... | grep -vxF -f <(printf '%s\n' "${spawners[@]}"))
  # The unprivileged run writes its counters here too.
  chmod 0755 "$dir"
  chmod 0777 "$dir/unit" "$dir/binary"
fi

# The two runs go side by side: one after the other pushed `fast` past its
# 15-minute cap (pipeline 2954). The unprivileged run gets its own build
# cache, because run-unprivileged.sh hands its cache to the other user while
# the root run is still writing to the shared one.
unprivileged=""
if [ "${#spawners[@]}" -gt 0 ]; then
  GOCACHE="$dir/gocache" bash scripts/ci/run-unprivileged.sh -- env ORBIT_LAUNCHER_BINARY_COVERDIR="$dir/binary" \
    go test "$@" "${skip[@]}" "${cover[@]}" "${spawners[@]}" -args -test.gocoverdir="$dir/unit" &
  unprivileged=$!
fi
status=0
ORBIT_LAUNCHER_BINARY_COVERDIR="$dir/binary" go test "$@" "${skip[@]}" "${cover[@]}" "${pkgs[@]}" \
  -args -test.gocoverdir="$dir/unit" || status=$?
if [ -n "$unprivileged" ]; then
  wait "$unprivileged" || status=$?
fi
[ "$status" -eq 0 ] || exit "$status"

go tool covdata textfmt -i="$dir/unit,$dir/binary" -o coverage.out
go tool covdata percent -i="$dir/unit,$dir/binary"
