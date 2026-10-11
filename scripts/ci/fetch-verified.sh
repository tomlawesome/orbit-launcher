#!/usr/bin/env bash
# fetch-verified.sh <url> <sha256> <dest>
#
# The one place CI downloads a pinned artifact (#216, #199 SU-12). Both hosts
# call it for every pinned download instead of typing their own
# `sha256sum -c`; the second opinion on a download is the other host running
# this same script, not a second copy of the check.
#
# Download, verify, then move into place: the file is fetched to a temporary
# name beside <dest>, its digest compared with <sha256>, and only a match is
# renamed onto <dest> (a rename within one directory is atomic, so <dest> is
# never a partial file). Any refusal -- a malformed call, a failed download,
# a digest that differs -- exits non-zero, leaves an existing <dest> exactly
# as it was and leaves nothing behind. Tested by
# scripts/test/fetch-verified.bats.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: fetch-verified.sh <url> <sha256> <dest>" >&2
  exit 2
fi
url="$1"
want="$2"
dest="$3"

if ! [[ "$want" =~ ^[0-9a-f]{64}$ ]]; then
  echo "fetch-verified.sh: '${want}' is not a SHA-256 digest (64 lowercase hex characters); nothing fetched" >&2
  exit 2
fi

dir="$(dirname -- "$dest")"
tmp="$(mktemp "${dir}/.fetch-verified.XXXXXX")"
trap 'rm -f -- "$tmp"' EXIT

# --fail: an HTTP error is a failed download, whatever page came with it.
# --proto/--proto-redir: fetch https only and follow redirects to https
# only, so a file://, http:// or other URL -- typed, or reached through a
# redirect -- is refused before anything is read.
if ! curl --fail --silent --show-error --location \
  --proto '=https' --proto-redir '=https' --output "$tmp" "$url"; then
  echo "fetch-verified.sh: could not download ${url}; ${dest} left as it was" >&2
  exit 1
fi

got="$(sha256sum -- "$tmp" | cut -d' ' -f1)"
if [ "$got" != "$want" ]; then
  echo "fetch-verified.sh: ${url} has SHA-256 ${got}, not the pinned ${want}; refused, ${dest} left as it was" >&2
  exit 1
fi

# mktemp creates the file 0600; give it the mode a plain download would get.
chmod 0644 "$tmp"
mv -f -- "$tmp" "$dest"
trap - EXIT
echo "fetch-verified.sh: ${dest} verified (${want})"
