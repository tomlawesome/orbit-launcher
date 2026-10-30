#!/usr/bin/env bats

# The live jobs' skip gate (.live_gate in .gitlab-ci.yml, #164, #313). It
# decides from LIVE_PINS_CHANGED (the verdict classify publishes), RUN_LIVE and
# the run-live-matrix label whether a live job stops before its install. The
# gate text is extracted from the CI file itself and run under sh, so what is
# tested is what the jobs run. A merge request into preview or main runs the
# installs whatever changed, so only a merge request into dev may skip.

setup() {
  ci="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)/.gitlab-ci.yml"
  work="$(mktemp -d)"
  # The block's indented lines, up to the next line at column 0.
  awk '/^\.live_gate: &live_gate \|/ {on=1; next} on && /^[^ ]/ {exit} on {print}' "$ci" > "$work/body"
  # Without this, every case below would pass against a missing gate.
  [ -s "$work/body" ]
  { cat "$work/body"; echo 'echo GATE_PASSED'; } > "$work/gate.sh"
}

teardown() {
  rm -rf "$work"
}

# gate VAR=value ... runs the gate with only those variables set.
gate() {
  run env -i PATH="$PATH" "$@" sh "$work/gate.sh"
}

@test "skips an MR into dev whose pins did not move" {
  gate LIVE_PINS_CHANGED=false CI_MERGE_REQUEST_TARGET_BRANCH_NAME=dev CI_MERGE_REQUEST_LABELS=
  [ "$status" -eq 0 ]
  [[ "$output" == *"skipped"* ]]
  [[ "$output" != *GATE_PASSED* ]]
}

@test "runs an MR into preview whose pins did not move" {
  gate LIVE_PINS_CHANGED=false CI_MERGE_REQUEST_TARGET_BRANCH_NAME=preview CI_MERGE_REQUEST_LABELS=
  [ "$status" -eq 0 ]
  [[ "$output" == *GATE_PASSED* ]]
}

@test "runs an MR into main whose pins did not move" {
  gate LIVE_PINS_CHANGED=false CI_MERGE_REQUEST_TARGET_BRANCH_NAME=main CI_MERGE_REQUEST_LABELS=
  [ "$status" -eq 0 ]
  [[ "$output" == *GATE_PASSED* ]]
}

@test "runs when no verdict is set" {
  gate CI_MERGE_REQUEST_TARGET_BRANCH_NAME=dev CI_MERGE_REQUEST_LABELS=
  [ "$status" -eq 0 ]
  [[ "$output" == *GATE_PASSED* ]]
}

@test "runs when a pin moved" {
  gate LIVE_PINS_CHANGED=true CI_MERGE_REQUEST_TARGET_BRANCH_NAME=dev CI_MERGE_REQUEST_LABELS=
  [ "$status" -eq 0 ]
  [[ "$output" == *GATE_PASSED* ]]
}

@test "RUN_LIVE and the label still run" {
  gate RUN_LIVE=true LIVE_PINS_CHANGED=false CI_MERGE_REQUEST_TARGET_BRANCH_NAME=dev CI_MERGE_REQUEST_LABELS=
  [ "$status" -eq 0 ]
  [[ "$output" == *GATE_PASSED* ]]

  gate LIVE_PINS_CHANGED=false CI_MERGE_REQUEST_TARGET_BRANCH_NAME=dev CI_MERGE_REQUEST_LABELS=a,run-live-matrix,b
  [ "$status" -eq 0 ]
  [[ "$output" == *GATE_PASSED* ]]
}

# classify gives a hand-started pipeline (no merge request) an empty base, so
# the verdict is normally unset there; this case is defensive, for a verdict of
# false with no target branch at all.
@test "a hand-started pipeline with no MR runs" {
  gate LIVE_PINS_CHANGED=false CI_MERGE_REQUEST_LABELS=
  [ "$status" -eq 0 ]
  [[ "$output" == *GATE_PASSED* ]]
}
