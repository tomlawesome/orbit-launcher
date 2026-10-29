#!/usr/bin/env bats

# scripts/ci/quarantine-skip.sh turns scripts/quarantine.txt into the pattern
# `go test -skip` takes (#310). The file is also the flake check's list of
# quarantined tests, so what matters here is that the pattern is exactly the
# listed names: nothing for an empty list (an empty -skip pattern would skip
# every test), comments and blanks ignored, regex characters matched literally.

setup() {
  script="$(cd "$(dirname "$BATS_TEST_FILENAME")/../ci" && pwd)/quarantine-skip.sh"
  # Without this, every case below would pass against a missing script.
  [ -f "$script" ]
  work="$(mktemp -d)"
}

teardown() {
  rm -rf "$work"
}

@test "a file of only comments and blank lines prints nothing" {
  printf '# nothing is quarantined\n\n   \n  # indented comment\n' > "$work/list"
  run bash "$script" "$work/list"
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

@test "names are joined with | and surrounding space is trimmed" {
  printf '# why\nTestA\n\n  TestB_Sub/case  \n' > "$work/list"
  run bash "$script" "$work/list"
  [ "$status" -eq 0 ]
  [ "$output" = 'TestA|TestB_Sub/case' ]
}

@test "regex characters in a name match literally" {
  printf 'TestX(a.b)+[c]\n' > "$work/list"
  run bash "$script" "$work/list"
  [ "$status" -eq 0 ]
  [ "$output" = 'TestX\(a\.b\)\+\[c\]' ]
  printf 'TestX(a.b)+[c]\n' | grep -qE "$output"
  ! printf 'TestXQaXb\n' | grep -qE "$output"
}

@test "a missing file is an error, not an empty list" {
  run bash "$script" "$work/absent"
  [ "$status" -eq 2 ]
  [[ "$output" == *"cannot read"* ]]
}

@test "the committed list is readable and prints a pattern with no empty alternative" {
  run bash "$script"
  [ "$status" -eq 0 ]
  [[ "$output" != '|'* && "$output" != *'|' && "$output" != *'||'* ]]
}
