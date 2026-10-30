#!/usr/bin/env bats

# scripts/coverage.sh and scripts/ci/nonroot-tests.sh pass the quarantine
# pattern to Go as `-skip` and `-test.skip` (#310). quarantine-skip.bats tests
# the pattern, but scripts/quarantine.txt is empty, so neither script ever
# ran with a name in it: a typo in the flag, or the flag dropped, would pass
# every check. Each case copies the scripts into a throwaway tree with its own
# quarantine.txt, puts a stub `go` first on PATH, and reads what the stub was
# given. The stub also writes the "compiled test binary" nonroot-tests.sh
# runs, which logs its own arguments.

setup() {
  src="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  # Without these, every case below would pass against a missing script.
  [ -f "$src/scripts/coverage.sh" ]
  [ -f "$src/scripts/ci/nonroot-tests.sh" ]
  [ -f "$src/scripts/ci/quarantine-skip.sh" ]

  work="$(mktemp -d)"
  # Readable by the unprivileged user nonroot-tests.sh drops to when run as root.
  chmod 0755 "$work"
  log="$work/go.log"
  : > "$log"
  chmod 0666 "$log"

  tree="$work/tree"
  mkdir -p "$tree/scripts/ci" "$work/bin" "$work/modcache/cache/download"
  chmod -R a+rX "$work/modcache"
  cp "$src/scripts/coverage.sh" "$tree/scripts/"
  cp "$src/scripts/ci/nonroot-tests.sh" "$src/scripts/ci/quarantine-skip.sh" "$tree/scripts/ci/"

  # A package nonroot-tests.sh will pick: it names the root-skipping tests.
  mkdir -p "$tree/pkg/a"
  printf 'package a\n// os.Geteuid() == 0\n' > "$tree/pkg/a/a_test.go"
  git -C "$tree" init -q
  git -C "$tree" add -A

  export GO_STUB_LOG="$log" GO_STUB_MODCACHE="$work/modcache" GO_STUB_TESTBIN="$work/testbin"
  cat > "$work/bin/go" <<'STUB'
#!/usr/bin/env bash
{ echo "--"; printf '%s\n' "$@"; } >> "$GO_STUB_LOG"
case "$1" in
  env) echo "$GO_STUB_MODCACHE" ;;
  list) case "$*" in *" -f "*) ;; *) echo example.com/x ;; esac ;;
  test)
    # `go test -c -o FILE ./pkg`: put the stub test binary at FILE.
    out=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "-o" ] && out="$a"
      prev="$a"
    done
    if [ -n "$out" ]; then
      cp "$GO_STUB_TESTBIN" "$out"
      chmod 0755 "$out"
    fi
    ;;
esac
exit 0
STUB
  cat > "$work/testbin" <<'STUB'
#!/usr/bin/env bash
{ echo "-- test binary"; printf '%s\n' "$@"; } >> "$GO_STUB_LOG"
STUB
  chmod 0644 "$work/testbin"
  chmod 0755 "$work/bin/go"
  export PATH="$work/bin:$PATH"
}

teardown() {
  rm -rf "$work"
}

# arg_after FLAG prints the log line after the first line that is FLAG.
arg_after() {
  local prev="" line
  while IFS= read -r line; do
    if [ "$prev" = "$1" ]; then
      printf '%s' "$line"
      return 0
    fi
    prev="$line"
  done < "$log"
  return 1
}

@test "coverage.sh gives go test -skip with the quarantined names" {
  printf '# why\nTestFlakyOne\n\n  TestOther/sub  \n' > "$tree/scripts/quarantine.txt"
  run bash "$tree/scripts/coverage.sh"
  [ "$status" -eq 0 ]
  [ "$(arg_after -skip)" = 'TestFlakyOne|TestOther/sub' ]
  # It is a flag of the go test call, which also got the coverage flags.
  grep -qx -- '-cover' "$log"
}

@test "coverage.sh passes no -skip when nothing is quarantined" {
  printf '# nothing is quarantined\n' > "$tree/scripts/quarantine.txt"
  run bash "$tree/scripts/coverage.sh"
  [ "$status" -eq 0 ]
  grep -qx -- '-cover' "$log"
  ! grep -qx -- '-skip' "$log"
}

@test "nonroot-tests.sh runs each test binary with -test.skip and the quarantined names" {
  printf '# why\nTestFlakyOne\n\n  TestOther/sub  \n' > "$tree/scripts/quarantine.txt"
  run bash "$tree/scripts/ci/nonroot-tests.sh"
  [ "$status" -eq 0 ]
  # The flag reaches the compiled test binary, not the `go test -c` build.
  grep -qx -- '-- test binary' "$log"
  [ "$(arg_after -test.skip)" = 'TestFlakyOne|TestOther/sub' ]
  [ -z "$(sed '/^-- test binary$/,$d' "$log" | grep -x -- '-test.skip' || true)" ]
}

@test "nonroot-tests.sh passes no -test.skip when nothing is quarantined" {
  printf '# nothing is quarantined\n' > "$tree/scripts/quarantine.txt"
  run bash "$tree/scripts/ci/nonroot-tests.sh"
  [ "$status" -eq 0 ]
  grep -qx -- '-- test binary' "$log"
  grep -qx -- '-test.count=1' "$log"
  ! grep -qx -- '-test.skip' "$log"
}
