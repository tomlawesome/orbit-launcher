#!/usr/bin/env bats

bats_require_minimum_version 1.5.0

# Tests scripts/ci/fetch-verified.sh <url> <sha256> <dest> (#216, SU-12): the
# one place CI downloads a pinned artifact and checks its digest. Both hosts
# call it instead of typing their own `sha256sum -c`, so what matters here is
# the refusal: a wrong digest, a failed download or a malformed call must
# leave nothing at <dest> that a later step could run or unpack.
#
# URLs are file:// so no test touches the network. The curl stub is for the
# cases a file:// URL cannot make: curl itself failing, as it does on an HTTP
# 404 with --fail, after writing an error page.

setup() {
  script="$(cd "$(dirname "$BATS_TEST_FILENAME")/../ci" && pwd)/fetch-verified.sh"
  # Without this, every refusal test below would pass against a missing script.
  [ -f "$script" ]
  work="$(mktemp -d)"
  mkdir -p "$work/src" "$work/out" "$work/bin"
  printf 'the artifact body\n' > "$work/src/artifact"
  url="file://$work/src/artifact"
  good="$(sha256sum "$work/src/artifact" | cut -d' ' -f1)"
  bad="$(printf 'some other bytes' | sha256sum | cut -d' ' -f1)"
  dest="$work/out/artifact"
}

teardown() {
  rm -rf "$work"
}

# A curl that "downloads" an HTTP error page and exits with STUB_CURL_EXIT.
stub_curl() {
  cat > "$work/bin/curl" <<'STUB'
#!/usr/bin/env bash
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output|-o) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
body='<html>404 Not Found</html>'
if [ -n "$out" ]; then printf '%s' "$body" > "$out"; else printf '%s' "$body"; fi
exit "${STUB_CURL_EXIT:-0}"
STUB
  chmod +x "$work/bin/curl"
}

# Nothing may be left in the destination directory: not the file, and not a
# partial or temporary copy beside it.
assert_nothing_written() {
  [ ! -e "$dest" ]
  [ -z "$(ls -A "$work/out")" ]
}

@test "a download whose digest matches is written to the destination" {
  run --separate-stderr bash "$script" "$url" "$good" "$dest"
  [ "$status" -eq 0 ]
  [ -f "$dest" ]
  cmp "$dest" "$work/src/artifact"
}

@test "the stubbed curl path also succeeds, so the failure cases below are not vacuous" {
  stub_curl
  body_sum="$(printf '%s' '<html>404 Not Found</html>' | sha256sum | cut -d' ' -f1)"
  run --separate-stderr env PATH="$work/bin:$PATH" STUB_CURL_EXIT=0 bash "$script" "$url" "$body_sum" "$dest"
  [ "$status" -eq 0 ]
  [ -f "$dest" ]
}

@test "a wrong digest is refused and writes nothing" {
  run --separate-stderr bash "$script" "$url" "$bad" "$dest"
  [ "$status" -ne 0 ]
  [ -n "$stderr" ]
  assert_nothing_written
}

@test "a wrong digest leaves an existing destination exactly as it was" {
  printf 'earlier contents' > "$dest"
  run --separate-stderr bash "$script" "$url" "$bad" "$dest"
  [ "$status" -ne 0 ]
  [ "$(cat "$dest")" = "earlier contents" ]
  [ "$(ls -A "$work/out")" = "artifact" ]
}

@test "a download that cannot be fetched is refused and writes nothing" {
  run --separate-stderr bash "$script" "file://$work/src/missing" "$good" "$dest"
  [ "$status" -ne 0 ]
  assert_nothing_written
}

@test "a failing curl is refused even when the error page it left matches the digest" {
  stub_curl
  # The digest of the error page itself: only the exit status can stop this.
  body_sum="$(printf '%s' '<html>404 Not Found</html>' | sha256sum | cut -d' ' -f1)"
  run --separate-stderr env PATH="$work/bin:$PATH" STUB_CURL_EXIT=22 bash "$script" "$url" "$body_sum" "$dest"
  [ "$status" -ne 0 ]
  assert_nothing_written
}

@test "a digest that is not 64 hex characters is refused and writes nothing" {
  for malformed in "" abc "${good:0:63}" "${good}0" "${good:0:63}z" "sha256:$good"; do
    run --separate-stderr bash "$script" "$url" "$malformed" "$dest"
    [ "$status" -ne 0 ]
    assert_nothing_written
  done
}

@test "the wrong number of arguments is refused and writes nothing" {
  run --separate-stderr bash "$script"
  [ "$status" -ne 0 ]
  run --separate-stderr bash "$script" "$url"
  [ "$status" -ne 0 ]
  run --separate-stderr bash "$script" "$url" "$good"
  [ "$status" -ne 0 ]
  run --separate-stderr bash "$script" "$url" "$good" "$dest" extra
  [ "$status" -ne 0 ]
  assert_nothing_written
}
