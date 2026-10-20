#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

# Tests scripts/ci/release-tag-check.sh, the guard release:patch and
# release:minor run before a release button may create a tag (#178, #264).
# Every refusal here is a tag that must never exist: the wrong commit, a
# duplicate, or a version that sorts below one already cut.

setup() {
  script="$(cd "$(dirname "$BATS_TEST_FILENAME")/../ci" && pwd)/release-tag-check.sh"
  tip=1111111111111111111111111111111111111111
  old=2222222222222222222222222222222222222222
  tags="$(printf '%s\t%s\n' \
    aaaa refs/tags/v0.1.0 aaab 'refs/tags/v0.1.0^{}' \
    bbbb refs/tags/v0.2.0 bbbc 'refs/tags/v0.2.0^{}')"
}

@test "the next version at the tip of main is accepted" {
  run --separate-stderr bash "$script" 0.3.0 "$tags" main "$tip" "$tip"
  [ "$status" -eq 0 ]
  [ "$output" = "RELEASE_TAG=v0.3.0" ]
}

@test "the first tag ever is accepted with no remote tags" {
  run --separate-stderr bash "$script" 0.1.0 "" main "$tip" "$tip"
  [ "$status" -eq 0 ]
  [ "$output" = "RELEASE_TAG=v0.1.0" ]
}

@test "a commit main has moved past is refused, naming main" {
  run --separate-stderr bash "$script" 0.3.0 "$tags" main "$tip" "$old"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"not the current tip of main"* ]]
  [[ "$stderr" == *"newest main pipeline"* ]]
}

@test "the branch named is the branch the message gives" {
  run --separate-stderr bash "$script" 0.3.0 "$tags" dev "$tip" "$old"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"not the current tip of dev"* ]]
}

@test "an unknown branch tip is refused" {
  run --separate-stderr bash "$script" 0.3.0 "$tags" main "" "$tip"
  [ "$status" -eq 1 ]
}

@test "a tag that already exists is refused" {
  run --separate-stderr bash "$script" 0.2.0 "$tags" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"already exists"* ]]
}

@test "a version below the newest tag is refused" {
  run --separate-stderr bash "$script" 0.1.5 "$tags" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"does not sort above"* ]]
}

@test "a version that is not X.Y.Z is refused" {
  for bad in "" 0.3 0.3.0.1 03.0.0 v0.3.0 "0.3.0 "; do
    run --separate-stderr bash "$script" "$bad" "$tags" main "$tip" "$tip"
    [ "$status" -eq 1 ]
  done
}

@test "the wrong number of arguments is refused" {
  run --separate-stderr bash "$script" 0.3.0 "$tags" main "$tip"
  [ "$status" -eq 1 ]
}
