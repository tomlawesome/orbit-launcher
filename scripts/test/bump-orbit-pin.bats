#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

# Tests scripts/bump-orbit-pin.sh (#193), which moves orbit/pin.json, the
# ai/orbit branch and commit the live job installs from. The GitLab lookup is
# stubbed through BUMP_ORBIT_PIN_RESOLVE, and the script runs from a copy in a
# temporary tree, so it never touches the network or the real pin.

# The file the script writes, exactly: no jq in the fast job's image.
pin() {
  printf '{\n  "branch": "%s",\n  "commit": "%s"\n}\n' "$1" "$2"
}

setup() {
  work="$(mktemp -d)"
  mkdir -p "$work/scripts" "$work/orbit" "$work/bin"
  cp "$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)/bump-orbit-pin.sh" "$work/scripts/"
  script="$work/scripts/bump-orbit-pin.sh"
  old=1111111111111111111111111111111111111111
  new=2222222222222222222222222222222222222222
  pin preview "$old" > "$work/orbit/pin.json"
  cp "$work/orbit/pin.json" "$work/pin.before"
  # A resolver that answers like GitLab: the commit for a known ref, a 404
  # on stderr and a non-zero exit for anything else.
  cat > "$work/bin/resolve" <<STUB
#!/usr/bin/env bash
echo "\$1" >> "$work/resolve.log"
case "\$1" in
  preview|v0.3.1) echo "$new" ;;
  odd) echo "not-a-commit" ;;
  *) echo "glab: 404 Commit Not Found (HTTP 404)" >&2; exit 1 ;;
esac
STUB
  chmod +x "$work/bin/resolve"
  export BUMP_ORBIT_PIN_RESOLVE="$work/bin/resolve"
}

teardown() {
  rm -rf "$work"
}

@test "rewrites orbit/pin.json with the ref as given and its commit" {
  run --separate-stderr bash "$script" preview
  [ "$status" -eq 0 ]
  [ "$(cat "$work/orbit/pin.json")" = "$(pin preview "$new")" ]
  [ "$(cat "$work/resolve.log")" = "preview" ]
}

@test "a tag goes in as given" {
  run --separate-stderr bash "$script" v0.3.1
  [ "$status" -eq 0 ]
  [ "$(cat "$work/orbit/pin.json")" = "$(pin v0.3.1 "$new")" ]
}

@test "an answer that is not a 40-hex commit is refused and the pin kept" {
  run --separate-stderr bash "$script" odd
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"not a 40-hex sha"* ]]
  cmp "$work/orbit/pin.json" "$work/pin.before"
}

@test "a ref ai/orbit does not have is refused and the pin kept" {
  run --separate-stderr bash "$script" no-such-branch
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"could not resolve no-such-branch"* ]]
  [[ "$stderr" == *"404"* ]]
  cmp "$work/orbit/pin.json" "$work/pin.before"
}

@test "no ref, a full ref or a ref that is not a name is refused before any lookup" {
  for ref in "" refs/heads/preview "-x" "a b"; do
    run --separate-stderr bash "$script" "$ref"
    [ "$status" -eq 1 ]
  done
  [ ! -e "$work/resolve.log" ]
  cmp "$work/orbit/pin.json" "$work/pin.before"
}
