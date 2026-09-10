#!/usr/bin/env bats

bats_require_minimum_version 1.5.0
# Tests scripts/ci/classify-changed-paths.sh against real commits in a
# throwaway repository, so the git invocation is exercised too and not only
# the path matching.
#
# The cases that matter most are the fail-safe ones: this gate decides whether
# the pipeline's expensive jobs run at all, so every answer it cannot be sure
# of must come back false (#153, criterion 3).

setup() {
  script="$(cd "$(dirname "$BATS_TEST_FILENAME")/../ci" && pwd)/classify-changed-paths.sh"

  repo="$(mktemp -d)"
  cd "$repo" || exit 1
  git init --quiet
  git config user.email ci@example.invalid
  git config user.name "CI"
  mkdir -p docs internal/ui scripts/ci .agents
  echo "start" > README.md
  echo "start" > internal/ui/app.go
  git add -A
  git commit --quiet -m "base"
  base="$(git rev-parse HEAD)"
}

teardown() {
  cd / || exit 1
  rm -rf "$repo"
}

# Commits the current worktree and classifies base..HEAD.
classify() {
  git add -A
  git commit --quiet -m "change"
  run --separate-stderr "$script" "$base" "$(git rev-parse HEAD)"
}

@test "a README-only change is documentation" {
  echo "more" >> README.md
  classify
  [ "$status" -eq 0 ]
  [ "$output" = "DOCS_ONLY=true" ]
}

@test "a change under docs/ is documentation" {
  echo "plan" > docs/implementation-plan.md
  classify
  [ "$output" = "DOCS_ONLY=true" ]
}

@test "a handoff is documentation" {
  echo "handoff" > .agents/2026-09-10-note.md
  classify
  [ "$output" = "DOCS_ONLY=true" ]
}

@test "several documentation files together are still documentation" {
  echo "more" >> README.md
  echo "plan" > docs/plan.md
  echo "sec" > SECURITY.md
  classify
  [ "$output" = "DOCS_ONLY=true" ]
}

@test "a Go source change runs the full gate" {
  echo "// change" >> internal/ui/app.go
  classify
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "one Go file among documentation still runs the full gate" {
  echo "more" >> README.md
  echo "plan" > docs/plan.md
  echo "// change" >> internal/ui/app.go
  classify
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "a change to the pipeline definition runs the full gate" {
  echo "# change" > .gitlab-ci.yml
  classify
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "a change to the classifier itself runs the full gate" {
  echo "# change" > scripts/ci/classify-changed-paths.sh
  classify
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "a dependency change runs the full gate" {
  echo "module example.invalid" > go.mod
  classify
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "a design source change runs the full gate, because the visual suite reads it" {
  mkdir -p design
  echo "<html></html>" > design/mockups.html
  classify
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "a missing base commit runs the full gate" {
  echo "more" >> README.md
  git add -A
  git commit --quiet -m "change"
  run --separate-stderr "$script" "" "$(git rev-parse HEAD)"
  [ "$status" -eq 0 ]
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "an unreadable comparison runs the full gate" {
  run --separate-stderr "$script" "0000000000000000000000000000000000000000" "HEAD"
  [ "$status" -eq 0 ]
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "an empty diff runs the full gate" {
  run --separate-stderr "$script" "$base" "$base"
  [ "$status" -eq 0 ]
  [ "$output" = "DOCS_ONLY=false" ]
}

@test "a path with a space in it is still classified, not silently skipped" {
  echo "// change" > "internal/ui/two words.go"
  classify
  [ "$output" = "DOCS_ONLY=false" ]
}
