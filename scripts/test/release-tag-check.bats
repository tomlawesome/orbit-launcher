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
  run --separate-stderr sh "$script" 0.3.0 "$tags" main "$tip" "$tip"
  [ "$status" -eq 0 ]
  [ "$output" = "RELEASE_TAG=v0.3.0" ]
}

@test "the first tag ever is accepted with no remote tags" {
  run --separate-stderr sh "$script" 0.1.0 "" main "$tip" "$tip"
  [ "$status" -eq 0 ]
  [ "$output" = "RELEASE_TAG=v0.1.0" ]
}

@test "a commit main has moved past is refused, naming main" {
  run --separate-stderr sh "$script" 0.3.0 "$tags" main "$tip" "$old"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"not the current tip of main"* ]]
  [[ "$stderr" == *"newest main pipeline"* ]]
}

@test "the branch named is the branch the message gives" {
  run --separate-stderr sh "$script" 0.3.0 "$tags" dev "$tip" "$old"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"not the current tip of dev"* ]]
}

@test "an unknown branch tip is refused" {
  run --separate-stderr sh "$script" 0.3.0 "$tags" main "" "$tip"
  [ "$status" -eq 1 ]
}

@test "a tag that already exists is refused" {
  run --separate-stderr sh "$script" 0.2.0 "$tags" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"already exists"* ]]
}

@test "a version below the newest tag is refused" {
  run --separate-stderr sh "$script" 0.1.5 "$tags" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"does not sort above"* ]]
}

@test "a version that is not X.Y.Z is refused" {
  for bad in "" 0.3 0.3.0.1 03.0.0 v0.3.0 "0.3.0 "; do
    run --separate-stderr sh "$script" "$bad" "$tags" main "$tip" "$tip"
    [ "$status" -eq 1 ]
  done
}

@test "the wrong number of arguments is refused" {
  run --separate-stderr sh "$script" 0.3.0 "$tags" main "$tip"
  [ "$status" -eq 1 ]
}

@test "a commit already carrying an annotated tag is refused, naming the tag" {
  run --separate-stderr sh "$script" 0.3.0 "$tags" main bbbc bbbc
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"already tagged v0.2.0"* ]]
}

@test "a commit carrying a lightweight tag is refused" {
  lightweight="$(printf '%s\n%s\t%s\n' "$tags" "$tip" refs/tags/v0.2.1)"
  run --separate-stderr sh "$script" 0.3.0 "$lightweight" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"already tagged v0.2.1"* ]]
}

@test "a tag object's own sha is not the commit" {
  run --separate-stderr sh "$script" 0.3.0 "$tags" main bbbb bbbb
  [ "$status" -eq 0 ]
  [ "$output" = "RELEASE_TAG=v0.3.0" ]
}

@test "a non-version tag on the commit is ignored" {
  other="$(printf '%s\n%s\t%s\n' "$tags" "$tip" refs/tags/live-ok)"
  run --separate-stderr sh "$script" 0.3.0 "$other" main "$tip" "$tip"
  [ "$status" -eq 0 ]
  [ "$output" = "RELEASE_TAG=v0.3.0" ]
}

# Two-digit components: a version compared as text puts 0.10.0 below 0.9.0,
# so these cases only pass when the guard compares the numbers (T2-Q2, #195).
@test "0.10.0 is accepted above a newest tag of 0.9.0" {
  multi="$(printf '%s\t%s\n' aaaa refs/tags/v0.2.0 cccc refs/tags/v0.9.0)"
  run --separate-stderr sh "$script" 0.10.0 "$multi" main "$tip" "$tip"
  [ "$status" -eq 0 ]
  [ "$output" = "RELEASE_TAG=v0.10.0" ]
}

@test "0.9.5 is refused below a newest tag of 0.10.0, naming it" {
  multi="$(printf '%s\t%s\n' aaaa refs/tags/v0.9.0 cccc refs/tags/v0.10.0)"
  run --separate-stderr sh "$script" 0.9.5 "$multi" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"does not sort above"* ]]
  [[ "$stderr" == *"0.10.0"* ]]
}

@test "a two-digit tag that already exists is refused" {
  multi="$(printf '%s\t%s\n' aaaa refs/tags/v0.9.0 cccc refs/tags/v0.10.0)"
  run --separate-stderr sh "$script" 0.10.0 "$multi" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"already exists"* ]]
}

@test "a commit carrying v0.9.0 and v0.10.0 is reported as tagged v0.10.0" {
  both="$(printf '%s\t%s\n%s\t%s\n' "$tip" refs/tags/v0.9.0 "$tip" refs/tags/v0.10.0)"
  run --separate-stderr sh "$script" 0.11.0 "$both" main "$tip" "$tip"
  [ "$status" -eq 1 ]
  [[ "$stderr" == *"already tagged v0.10.0"* ]]
}

@test "a leading zero in any part is refused, in multi-digit versions too" {
  for bad in 0.01.0 0.10.00 1.0.010 00.10.0 0.010.0; do
    run --separate-stderr sh "$script" "$bad" "$tags" main "$tip" "$tip"
    [ "$status" -eq 1 ]
    [[ "$stderr" == *"not a semantic version"* ]]
  done
}

@test "a multi-digit version with no leading zero is accepted" {
  for good in 10.0.0 0.10.0 1.0.10 12.34.56; do
    run --separate-stderr sh "$script" "$good" "$tags" main "$tip" "$tip"
    [ "$status" -eq 0 ]
    [ "$output" = "RELEASE_TAG=v$good" ]
  done
}
