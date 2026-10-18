#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

# Tests scripts/ci/fetch-verified.sh <url> <sha256> <dest> (#216, SU-12): the
# one place CI downloads a pinned artifact and checks its digest. Both hosts
# call it instead of typing their own `sha256sum -c`, so what matters here is
# the refusal: a wrong digest, a failed download or a malformed call must
# leave nothing at <dest> that a later step could run or unpack.
#
# The script fetches and follows https only, so the cases that need a
# download to arrive run against a curl stub on PATH, which copies a local
# file to --output (or leaves an error page and fails) and records the
# arguments it was given. No test touches the network: the refusal of other
# schemes uses the real curl, which refuses them before connecting.

setup() {
  script="$(cd "$(dirname "$BATS_TEST_FILENAME")/../ci" && pwd)/fetch-verified.sh"
  # Without this, every refusal test below would pass against a missing script.
  [ -f "$script" ]
  work="$(mktemp -d)"
  mkdir -p "$work/src" "$work/out" "$work/bin"
  printf 'the artifact body\n' > "$work/src/artifact"
  url="https://example.invalid/artifact"
  good="$(sha256sum "$work/src/artifact" | cut -d' ' -f1)"
  bad="$(printf 'some other bytes' | sha256sum | cut -d' ' -f1)"
  page_sum="$(printf '%s' '<html>404 Not Found</html>' | sha256sum | cut -d' ' -f1)"
  dest="$work/out/artifact"
  stub_curl
}

teardown() {
  rm -rf "$work"
}

# A curl that "downloads" $work/src/artifact, or with STUB_CURL_EXIT set
# non-zero (or STUB_ERROR_PAGE set) writes an HTTP error page instead, and
# exits with STUB_CURL_EXIT. Its arguments go to $work/curl-args, one a line.
stub_curl() {
  cat > "$work/bin/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$@" > "$STUB_WORK/curl-args"
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output|-o) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ "${STUB_CURL_EXIT:-0}" -ne 0 ] || [ -n "${STUB_ERROR_PAGE:-}" ]; then
  printf '%s' '<html>404 Not Found</html>' > "$out"
else
  cp "$STUB_WORK/src/artifact" "$out"
fi
exit "${STUB_CURL_EXIT:-0}"
STUB
  chmod +x "$work/bin/curl"
}

# Runs the script with the stub curl first on PATH.
fetch() {
  run --separate-stderr env PATH="$work/bin:$PATH" STUB_WORK="$work" "$@" bash "$script" "${FETCH_ARGS[@]}"
}

# Nothing may be left in the destination directory: not the file, and not a
# partial or temporary copy beside it.
assert_nothing_written() {
  [ ! -e "$dest" ]
  [ -z "$(ls -A "$work/out")" ]
}

@test "a download whose digest matches is written to the destination" {
  FETCH_ARGS=("$url" "$good" "$dest")
  fetch
  [ "$status" -eq 0 ]
  [ -f "$dest" ]
  cmp "$dest" "$work/src/artifact"
}

@test "curl is told to fetch and follow https only" {
  FETCH_ARGS=("$url" "$good" "$dest")
  fetch
  [ "$status" -eq 0 ]
  grep -qx -- '--proto' "$work/curl-args"
  grep -qx -- '--proto-redir' "$work/curl-args"
  [ "$(grep -A1 -x -- '--proto' "$work/curl-args" | tail -1)" = "=https" ]
  [ "$(grep -A1 -x -- '--proto-redir' "$work/curl-args" | tail -1)" = "=https" ]
}

@test "an http:// or file:// URL is refused and writes nothing" {
  # The real curl, not the stub. The file:// URL names the real artifact
  # with its real digest, so only the scheme can stop it; the http:// one is
  # refused before any connection is tried.
  for other in "file://$work/src/artifact" "http://127.0.0.1:9/artifact"; do
    run --separate-stderr bash "$script" "$other" "$good" "$dest"
    [ "$status" -ne 0 ]
    assert_nothing_written
  done
}

@test "the stubbed error page also succeeds when it matches, so the failure cases below are not vacuous" {
  FETCH_ARGS=("$url" "$page_sum" "$dest")
  fetch STUB_ERROR_PAGE=1 STUB_CURL_EXIT=0
  [ "$status" -eq 0 ]
  [ -f "$dest" ]
}

@test "a wrong digest is refused and writes nothing" {
  FETCH_ARGS=("$url" "$bad" "$dest")
  fetch
  [ "$status" -ne 0 ]
  [ -n "$stderr" ]
  assert_nothing_written
}

@test "a wrong digest leaves an existing destination exactly as it was" {
  printf 'earlier contents' > "$dest"
  FETCH_ARGS=("$url" "$bad" "$dest")
  fetch
  [ "$status" -ne 0 ]
  [ "$(cat "$dest")" = "earlier contents" ]
  [ "$(ls -A "$work/out")" = "artifact" ]
}

@test "a download that cannot be fetched is refused and writes nothing" {
  # 6: curl could not resolve the host.
  FETCH_ARGS=("https://example.invalid/missing" "$good" "$dest")
  fetch STUB_CURL_EXIT=6
  [ "$status" -ne 0 ]
  assert_nothing_written
}

@test "a failing curl is refused even when the error page it left matches the digest" {
  # The digest of the error page itself: only the exit status can stop this.
  FETCH_ARGS=("$url" "$page_sum" "$dest")
  fetch STUB_CURL_EXIT=22
  [ "$status" -ne 0 ]
  assert_nothing_written
}

@test "a digest that is not 64 hex characters is refused and writes nothing" {
  for malformed in "" abc "${good:0:63}" "${good}0" "${good:0:63}z" "sha256:$good"; do
    FETCH_ARGS=("$url" "$malformed" "$dest")
    fetch
    [ "$status" -ne 0 ]
    assert_nothing_written
  done
}

@test "the wrong number of arguments is refused and writes nothing" {
  for n in 0 1 2 4; do
    FETCH_ARGS=("$url" "$good" "$dest" extra)
    FETCH_ARGS=("${FETCH_ARGS[@]:0:$n}")
    fetch
    [ "$status" -ne 0 ]
  done
  assert_nothing_written
}
