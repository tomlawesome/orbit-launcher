#!/usr/bin/env bash
# Downloads the orbit-launcher binary for this machine and execs into it.
# This script's only job is getting orbit-launcher running — it never
# touches Orbit's own application files. Those are fetched by
# orbit-launcher itself, only once a person commits at a flow's Final
# Review screen.
#
# Usage: curl -fsSL <raw-url>/scripts/get-orbit-launcher.sh | bash
set -Eeuo pipefail

repository="${ORBIT_LAUNCHER_REPOSITORY:-tomlawesome/orbit-launcher}"
version="${ORBIT_LAUNCHER_VERSION:-latest}"

fail() {
  echo "get-orbit-launcher: $1" >&2
  exit 1
}

[[ "$(uname -s)" == "Linux" ]] || fail "orbit-launcher runs on Linux only (this manages the server it's installed on, not a remote desktop tool)."

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "unsupported architecture: $(uname -m) (supported: amd64, arm64)" ;;
esac

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required"

if [[ "$version" == "latest" ]]; then
  release_path="releases/latest"
else
  release_path="releases/tags/${version}"
fi

release_json="$(curl -fsSL "https://api.github.com/repos/${repository}/${release_path}")" \
  || fail "could not resolve release '${version}' for ${repository}"

tag="$(printf '%s' "$release_json" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
[[ -n "$tag" ]] || fail "could not determine release tag"

# The binary must still exist on disk when we exec into it below, so it
# lives in a stable cache directory, not the scratch directory — `exec`
# replaces this shell's process image without running EXIT traps, so
# anything we want cleaned up has to be removed explicitly, before exec,
# not left to a trap that will never fire.
cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/orbit-launcher"
mkdir -p "$cache_dir"
scratch_dir="$(mktemp -d)"
cleanup_scratch() { rm -rf "$scratch_dir"; }
trap cleanup_scratch EXIT

asset="orbit-launcher_linux_${arch}.tar.gz"
base_url="https://github.com/${repository}/releases/download/${tag}"

curl -fsSL -o "${scratch_dir}/${asset}" "${base_url}/${asset}" \
  || fail "could not download ${asset} for release ${tag}"
curl -fsSL -o "${scratch_dir}/checksums.txt" "${base_url}/checksums.txt" \
  || fail "could not download checksums.txt for release ${tag}"

expected="$(grep " ${asset}\$" "${scratch_dir}/checksums.txt" | awk '{print $1}' || true)"
[[ -n "$expected" ]] || fail "no checksum entry found for ${asset}; refusing to run an unverified binary"

actual="$(sha256sum "${scratch_dir}/${asset}" | awk '{print $1}')"
[[ "$expected" == "$actual" ]] || fail "checksum mismatch for ${asset}: expected ${expected}, got ${actual}; refusing to run an unverified binary"

tar -xzf "${scratch_dir}/${asset}" -C "$cache_dir" orbit-launcher
chmod +x "${cache_dir}/orbit-launcher"

cleanup_scratch
trap - EXIT
exec "${cache_dir}/orbit-launcher" "$@"
