#!/usr/bin/env bats

# Tests scripts/readme-screenshot.sh, which retakes docs/images/launcher-menu.png
# (#373). Only the refusals are run here; a real capture needs ttyd, Chromium
# and a Go build, so every tool the script would reach for is a fake on PATH
# that records its call. A refusal must happen before any of them is called.
#
# Expects:
#   - scripts/readme-screenshot.sh <version>, run with bash, working on the git
#     repository that holds the script (its own parent directory), so these
#     tests copy scripts/ into a throwaway repo.
#   - scripts/release-ldflags.sh next to it (the one shared place for the
#     release link flags; see test/supplychain/readme_screenshot_test.go).
#   - Order of checks: version argument, shape, dirty tree, ttyd 1.7.7.
#   - Each refusal exits non-zero with exactly one line on stderr.
#   - Version shape is strictly vX.Y.Z (digits only): v1.2, 1.2.3, v1.2.3-rc1,
#     v1.2.3.4 and v1.2.x are refused (decided here; the spec names only vX.Y.Z).
#   - ttyd's version is read from `ttyd --version` ("ttyd version 1.7.7" or
#     "ttyd version 1.7.7-<sha>" is accepted; anything else is refused).

setup() {
  src="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  work="$(mktemp -d)"
  repo="$work/repo"
  mkdir -p "$repo/scripts" "$work/bin"
  cp "$src/readme-screenshot.sh" "$repo/scripts/" 2>/dev/null || true
  cp "$src/release-ldflags.sh" "$repo/scripts/" 2>/dev/null || true
  echo seed > "$repo/scripts/seed.txt"
  git -C "$repo" init -q
  git -C "$repo" add -A
  git -C "$repo" -c user.name=t -c user.email=t@example.invalid commit -q -m init
  fake_tool ttyd 'ttyd version 1.7.7'
  for t in go npm npx node; do fake_tool "$t" ''; done
  script="$repo/scripts/readme-screenshot.sh"
}

teardown() {
  rm -rf "$work"
}

# fake_tool NAME VERSION_LINE: a stub that logs every call; with --version it
# prints VERSION_LINE.
fake_tool() {
  cat > "$work/bin/$1" <<STUB
#!/usr/bin/env bash
echo "$1 \$*" >> "$work/calls.log"
if [ "\$1" = "--version" ]; then echo "$2"; fi
STUB
  chmod +x "$work/bin/$1"
}

shoot() {
  run --separate-stderr env PATH="$work/bin:$PATH" bash "$script" "$@"
}

# refused: non-zero, one stderr line, and no tool (other than a ttyd --version
# probe) was called.
refused() {
  [ "$status" -ne 0 ]
  [ -n "$stderr" ]
  [ "$(printf '%s\n' "$stderr" | wc -l)" -eq 1 ]
  if [ -e "$work/calls.log" ]; then
    run grep -v '^ttyd --version' "$work/calls.log"
    [ -z "$output" ]
  fi
  [ ! -e "$repo/docs/images/launcher-menu.png" ]
}

@test "the script exists" {
  [ -f "$src/readme-screenshot.sh" ]
}

@test "no version argument is refused" {
  shoot
  refused
  [[ "$stderr" == *version* ]]
}

@test "malformed versions are refused" {
  for v in v1.2 1.2.3 v1.2.3-rc1 v1.2.3.4 v1.2.x vx.y.z ""; do
    shoot "$v"
    refused
  done
}

@test "a dirty working tree is refused" {
  echo stray > "$repo/stray.txt"
  shoot v1.2.3
  refused
  [[ "$stderr" == *dirty* || "$stderr" == *uncommitted* || "$stderr" == *clean* ]]
}

@test "a modified tracked file is refused too" {
  echo change >> "$repo/scripts/seed.txt"
  shoot v1.2.3
  refused
}

@test "a ttyd that is not 1.7.7 is refused, naming 1.7.7" {
  fake_tool ttyd 'ttyd version 1.7.6'
  shoot v1.2.3
  refused
  [[ "$stderr" == *1.7.7* ]]
}

@test "a ttyd with no recognisable version is refused" {
  fake_tool ttyd 'something else'
  shoot v1.2.3
  refused
  [[ "$stderr" == *1.7.7* ]]
}
