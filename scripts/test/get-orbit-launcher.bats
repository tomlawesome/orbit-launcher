#!/usr/bin/env bats
# Tests scripts/get-orbit-launcher.sh against a fake GitHub API and a fake
# release asset, without any real network access.

setup() {
  script_dir="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  script="$script_dir/get-orbit-launcher.sh"

  work="$(mktemp -d)"
  fake_bin="$work/bin"
  fixtures="$work/fixtures"
  mkdir -p "$fake_bin" "$fixtures"

  # A real, valid tarball containing a fake "orbit-launcher" that just
  # echoes its arguments, so we can prove exec really happened.
  mkdir -p "$fixtures/payload"
  cat > "$fixtures/payload/orbit-launcher" <<'BINARY'
#!/usr/bin/env bash
echo "fake-orbit-launcher-ran: $*"
BINARY
  chmod +x "$fixtures/payload/orbit-launcher"
  tar -czf "$fixtures/orbit-launcher_linux_amd64.tar.gz" -C "$fixtures/payload" orbit-launcher

  good_checksum="$(sha256sum "$fixtures/orbit-launcher_linux_amd64.tar.gz" | awk '{print $1}')"
  printf '%s  orbit-launcher_linux_amd64.tar.gz\n' "$good_checksum" > "$fixtures/checksums-good.txt"
  printf '%s  orbit-launcher_linux_amd64.tar.gz\n' "0000000000000000000000000000000000000000000000000000000000000000" > "$fixtures/checksums-bad.txt"
  printf 'no-matching-entry  some-other-file.tar.gz\n' > "$fixtures/checksums-missing.txt"

  cat > "$fixtures/release.json" <<'JSON'
{"tag_name": "v0.1.0"}
JSON
}

teardown() {
  rm -rf "$work"
}

# Writes a fake curl that serves fixed fixture content for known URLs,
# so the script never makes a real network call under test.
stub_curl() {
  local checksums_fixture="$1"
  cat > "$fake_bin/curl" <<STUB
#!/usr/bin/env bash
set -euo pipefail
out=""
url=""
args=("\$@")
for ((i = 0; i < \${#args[@]}; i++)); do
  if [[ "\${args[i]}" == "-o" ]]; then
    out="\${args[i+1]}"
  fi
done
url="\${args[-1]}"
case "\$url" in
  *api.github.com*releases/latest) cat "$fixtures/release.json" ;;
  *releases/download/*/orbit-launcher_linux_amd64.tar.gz) cp "$fixtures/orbit-launcher_linux_amd64.tar.gz" "\$out" ;;
  *releases/download/*/checksums.txt) cp "$checksums_fixture" "\$out" ;;
  *) echo "stub curl: unexpected url \$url" >&2; exit 1 ;;
esac
STUB
  chmod +x "$fake_bin/curl"
}

@test "rejects a non-Linux platform before any network call" {
  cat > "$fake_bin/uname" <<'STUB'
#!/usr/bin/env bash
[[ "$1" == "-s" ]] && echo Darwin || echo x86_64
STUB
  chmod +x "$fake_bin/uname"

  run env PATH="$fake_bin:$PATH" bash "$script"
  [ "$status" -ne 0 ]
  [[ "$output" == *"Linux only"* ]]
}

@test "rejects an unsupported architecture before any network call" {
  cat > "$fake_bin/uname" <<'STUB'
#!/usr/bin/env bash
[[ "$1" == "-s" ]] && echo Linux || echo riscv64
STUB
  chmod +x "$fake_bin/uname"

  run env PATH="$fake_bin:$PATH" bash "$script"
  [ "$status" -ne 0 ]
  [[ "$output" == *"unsupported architecture"* ]]
}

@test "downloads, verifies and execs into the real binary on a checksum match" {
  stub_curl "$fixtures/checksums-good.txt"
  run env PATH="$fake_bin:/usr/bin:/bin" HOME="$work" bash "$script" --version
  [ "$status" -eq 0 ]
  [[ "$output" == *"fake-orbit-launcher-ran: --version"* ]]
}

@test "refuses to run the binary on a checksum mismatch" {
  stub_curl "$fixtures/checksums-bad.txt"
  run env PATH="$fake_bin:/usr/bin:/bin" HOME="$work" bash "$script"
  [ "$status" -ne 0 ]
  [[ "$output" == *"checksum mismatch"* ]]
  [[ "$output" != *"fake-orbit-launcher-ran"* ]]
}

@test "refuses to run the binary when no checksum entry is found" {
  stub_curl "$fixtures/checksums-missing.txt"
  run env PATH="$fake_bin:/usr/bin:/bin" HOME="$work" bash "$script"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no checksum entry found"* ]]
  [[ "$output" != *"fake-orbit-launcher-ran"* ]]
}

@test "cleans up its scratch directory after a successful run" {
  stub_curl "$fixtures/checksums-good.txt"
  before="$(find /tmp -maxdepth 1 -name 'tmp.*' 2>/dev/null | wc -l)"
  run env PATH="$fake_bin:/usr/bin:/bin" HOME="$work" bash "$script"
  after="$(find /tmp -maxdepth 1 -name 'tmp.*' 2>/dev/null | wc -l)"
  [ "$status" -eq 0 ]
  [ "$before" -eq "$after" ]
}
