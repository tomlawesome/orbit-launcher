#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

# Tests scripts/ci/classify-changed-paths.sh against real commits in a
# throwaway repository, so the git invocation is exercised too and not only
# the path matching.
#
# The cases that matter most are the fail-safe ones. This gate decides whether
# the pipeline's expensive jobs run at all, so every answer it cannot be sure
# of must mean more testing, not less: DOCS_ONLY=false (#153) and
# LIVE_PINS_CHANGED=true (#164).

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
  cat > .gitlab-ci.yml <<'YML'
variables:
  GO_IMAGE: golang:1.26.8-bookworm@sha256:aaa
  GO_VERSION: "1.26.8"
  GO_SHA256: aaa
  UBUNTU_IMAGE: ubuntu:24.04@sha256:aaa
  STATICCHECK_VERSION: v0.7.0
YML
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

docs_only() { printf '%s' "${lines[0]}"; }
live_pins() { printf '%s' "${lines[1]}"; }

# --- documentation-only, or not (#153) ---------------------------------------

@test "a README-only change is documentation" {
  echo "more" >> README.md
  classify
  [ "$status" -eq 0 ]
  [ "$(docs_only)" = "DOCS_ONLY=true" ]
}

@test "a change under docs/ is documentation" {
  echo "plan" > docs/implementation-plan.md
  classify
  [ "$(docs_only)" = "DOCS_ONLY=true" ]
}

@test "a handoff is documentation" {
  echo "handoff" > .agents/2026-09-10-note.md
  classify
  [ "$(docs_only)" = "DOCS_ONLY=true" ]
}

@test "several documentation files together are still documentation" {
  echo "more" >> README.md
  echo "plan" > docs/plan.md
  echo "sec" > SECURITY.md
  classify
  [ "$(docs_only)" = "DOCS_ONLY=true" ]
}

@test "a Go source change runs the full gate" {
  echo "// change" >> internal/ui/app.go
  classify
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
}

@test "one Go file among documentation still runs the full gate" {
  echo "more" >> README.md
  echo "plan" > docs/plan.md
  echo "// change" >> internal/ui/app.go
  classify
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
}

@test "a change to the pipeline definition runs the full gate" {
  echo "# change" >> .gitlab-ci.yml
  classify
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
}

@test "a change to the classifier itself runs the full gate" {
  echo "# change" > scripts/ci/classify-changed-paths.sh
  classify
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
}

@test "a dependency change runs the full gate" {
  echo "module example.invalid" > go.mod
  classify
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
}

@test "a design source change runs the full gate, because the visual suite reads it" {
  mkdir -p design
  echo "<html></html>" > design/mockups.html
  classify
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
}

@test "a path with a space in it is still classified, not silently skipped" {
  echo "// change" > "internal/ui/two words.go"
  classify
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
}

# --- pins the live install runs on (#164) ------------------------------------

@test "a moved ubuntu image runs the live install" {
  sed -i 's|ubuntu:24.04@sha256:aaa|ubuntu:26.04@sha256:bbb|' .gitlab-ci.yml
  classify
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=true" ]
}

@test "a moved Go tarball version runs the live install" {
  sed -i 's|GO_VERSION: "1.26.8"|GO_VERSION: "1.27.1"|' .gitlab-ci.yml
  classify
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=true" ]
}

@test "a moved Go tarball checksum runs the live install" {
  sed -i 's|GO_SHA256: aaa|GO_SHA256: bbb|' .gitlab-ci.yml
  classify
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=true" ]
}

@test "an ordinary pipeline edit does not run the live install" {
  echo "# a comment" >> .gitlab-ci.yml
  classify
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=false" ]
}

@test "a pin the live install does not use does not run it" {
  sed -i 's|STATICCHECK_VERSION: v0.7.0|STATICCHECK_VERSION: v0.8.0|' .gitlab-ci.yml
  classify
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=false" ]
}

@test "a Go source change alone does not run the live install" {
  echo "// change" >> internal/ui/app.go
  classify
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=false" ]
}

@test "a documentation-only change does not run the live install" {
  echo "more" >> README.md
  classify
  [ "$(docs_only)" = "DOCS_ONLY=true" ]
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=false" ]
}

# --- the fail-safe answers, in both directions -------------------------------

@test "a missing base commit runs everything" {
  echo "more" >> README.md
  git add -A
  git commit --quiet -m "change"
  run --separate-stderr "$script" "" "$(git rev-parse HEAD)"
  [ "$status" -eq 0 ]
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=true" ]
}

@test "an unreadable comparison runs everything" {
  run --separate-stderr "$script" "0000000000000000000000000000000000000000" "HEAD"
  [ "$status" -eq 0 ]
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=true" ]
}

@test "an empty diff runs everything" {
  run --separate-stderr "$script" "$base" "$base"
  [ "$status" -eq 0 ]
  [ "$(docs_only)" = "DOCS_ONLY=false" ]
  [ "$(live_pins)" = "LIVE_PINS_CHANGED=true" ]
}
