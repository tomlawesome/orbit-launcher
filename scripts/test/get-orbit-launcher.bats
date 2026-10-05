#!/usr/bin/env bats
# scripts/get-orbit-launcher.sh only redirects to Orbit's installer now
# (#171): it must never download or run anything, whatever the caller sets.

setup() {
  script="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)/get-orbit-launcher.sh"
  work="$(mktemp -d)"
  # A curl that records any call, so a download attempt cannot pass unseen.
  mkdir -p "$work/bin"
  cat > "$work/bin/curl" <<STUB
#!/usr/bin/env bash
echo "curl called: \$*" >> "$work/curl.log"
STUB
  chmod +x "$work/bin/curl"
}

teardown() {
  rm -rf "$work"
}

@test "points at Orbit's installer, exits non-zero and downloads nothing" {
  run env PATH="$work/bin:/usr/bin:/bin" bash "$script"
  [ "$status" -ne 0 ]
  [[ "$output" == *"get-orbit.sh"* ]]
  [ ! -e "$work/curl.log" ]
}

@test "the retired ORBIT_LAUNCHER_DEVELOPER=1 no longer downloads anything" {
  run env ORBIT_LAUNCHER_DEVELOPER=1 PATH="$work/bin:/usr/bin:/bin" bash "$script"
  [ "$status" -ne 0 ]
  [[ "$output" == *"get-orbit.sh"* ]]
  [ ! -e "$work/curl.log" ]
}
