#!/usr/bin/env bash
# Retakes docs/images/launcher-menu.png, the picture in the README (#373).
#
# Usage: scripts/readme-screenshot.sh vX.Y.Z
#
# Builds the launcher with the release flags (scripts/release-ldflags.sh), runs
# it under ttyd in headless Chromium via the Playwright spec
# test/visual/tests/readme-screenshot.spec.ts, and writes a 1940x1120 PNG. Run
# it in the last batch before the release audit, with the version about to be
# released, never in the release record (docs/releasing.md). The Revision it
# embeds is this checkout's, not the shipped build's; the menu shows only the
# version. It needs
# ttyd 1.7.7 (the version .gitlab-ci.yml pins), Go, and Node with Chromium
# installed for Playwright.
set -euo pipefail

fail() {
  echo "readme-screenshot.sh: $*" >&2
  exit 1
}

version="${1:-}"
[[ -n "$version" ]] || fail "a version argument is required, e.g. v0.4.9"
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "version '$version' is not shaped vX.Y.Z"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

[[ -z "$(git -C "$root" status --porcelain)" ]] || fail "the working tree is dirty; commit or stash first so the revision is true"

ttyd_line="$(ttyd --version 2>&1 | head -n 1 || true)"
[[ "$ttyd_line" =~ ^ttyd\ version\ 1\.7\.7(-.*)?$ ]] || fail "ttyd 1.7.7 is required (got '${ttyd_line:-nothing}')"

revision="$(git -C "$root" rev-parse --short HEAD)"
mapfile -t build_args < <("$root/scripts/release-ldflags.sh" "$version" "$revision")

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

(cd "$root" && CGO_ENABLED=0 go build "${build_args[@]}" -o "$work/orbit-launcher" ./cmd/orbit-launcher) ||
  fail "go build failed"

out="$root/docs/images/launcher-menu.png"
cd "$root/test/visual"
npm ci --silent
ORBIT_README_SCREENSHOT=1 \
  ORBIT_README_BINARY="$work/orbit-launcher" \
  ORBIT_README_OUT="$out" \
  npx playwright test tests/readme-screenshot.spec.ts
echo "wrote $out for $version ($revision)"
