#!/usr/bin/env bash
# Runs the test suite with coverage and writes one profile, coverage.out,
# that counts both the unit tests and the compiled launcher test/pty runs
# under a real pty (#169).
#
# test/pty spawns the real binary, so its coverage never reached a plain
# `go test -coverprofile`: cmd/orbit-launcher read 23.5% when the pty
# suite exercises nearly all of it. With ORBIT_LAUNCHER_BINARY_COVERDIR
# set, test/pty builds that binary with -cover (see buildBinary), each
# run writes its counters there, and `go tool covdata` merges them with
# the unit tests' own.
#
# Atomic mode on both sides: -race needs it, and covdata cannot merge
# counters recorded in two modes.
#
# Usage: scripts/coverage.sh [extra go test flags, e.g. -race]
# Then:  go run ./tools/coveragefloor coverage.out
set -euo pipefail

cd "$(dirname "$0")/.."
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
mkdir -p "$dir/unit" "$dir/binary"

ORBIT_LAUNCHER_BINARY_COVERDIR="$dir/binary" go test "$@" -count=1 -cover -covermode=atomic \
  -coverpkg=github.com/tomlawesome/orbit-launcher/... ./... \
  -args -test.gocoverdir="$dir/unit"

go tool covdata textfmt -i="$dir/unit,$dir/binary" -o coverage.out
go tool covdata percent -i="$dir/unit,$dir/binary"
