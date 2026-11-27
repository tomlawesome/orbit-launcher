#!/usr/bin/env bash
# Prints the `go build` arguments for a release build of the launcher, one per
# line, for `mapfile -t args < <(scripts/release-ldflags.sh vX.Y.Z <sha>)`.
#
# This repo has no release build of its own: Orbit builds the launcher it
# ships. The flags are copied from Orbit's scripts/ci/build-launcher.sh (the
# `ldflags=` line and its `go build -trimpath` call) so a picture or build made
# here shows the same version string as the shipped one. When Orbit's change,
# change these in the same merge request. This is the one place they live (#373).
#
# Usage: release-ldflags.sh <version> <revision>
set -euo pipefail

if [[ $# -ne 2 || -z "$1" || -z "$2" ]]; then
  echo "usage: release-ldflags.sh <version> <revision>" >&2
  exit 2
fi
version="$1"
revision="$2"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
module="$(sed -n 's/^module[[:space:]]\{1,\}//p' "$root/go.mod" | head -n 1)"
if [[ -z "$module" ]]; then
  echo "release-ldflags.sh: no module line in go.mod" >&2
  exit 1
fi

echo "-trimpath"
echo "-ldflags=-s -w -buildid= -X ${module}/internal/release.Version=${version} -X ${module}/internal/release.Revision=${revision}"
