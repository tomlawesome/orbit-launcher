#!/usr/bin/env bash
#
# Bumps orbit/pin.json (#193): the ai/orbit branch and commit the `live` job
# installs from (.gitlab-ci.yml). It reads install.sh, Orbit's verifier
# scripts and cosign.pub at the commit, and the release manifest from the
# latest build of the branch, so the two must name the same release or the
# job stops (#236). The pin is the partner's half of Orbit's own
# launcher/pin.json, bumped by its scripts/bump-launcher-pin.sh.
#
# Usage:
#   scripts/bump-orbit-pin.sh <ref>
#
# Resolves <ref>, a branch or tag on ai/orbit, to its commit through the
# GitLab API (`glab api`, so the token stays in glab's own configuration and
# never reaches a command line) and writes orbit/pin.json with `branch` set to
# <ref> as given and `commit` to that commit. Refuses a ref GitLab does not
# have, and an answer that is not a 40-hex commit, so branch and commit are
# never typed independently.
#
# Inputs (environment):
#   ORBIT_GITLAB_HOST        The GitLab host ai/orbit lives on. Defaults to
#                            gitlab.tomlawson.io, this repository's `gitlab`
#                            remote.
#   ORBIT_PROJECT            The project to resolve against. Defaults to
#                            ai/orbit, as in .gitlab-ci.yml.
#   BUMP_ORBIT_PIN_RESOLVE   A command run as `<command> <ref>` that prints
#                            the commit, in place of the GitLab API call; the
#                            tests (scripts/test/bump-orbit-pin.bats) stub it.
#
# Output: overwrites orbit/pin.json and prints the resolved ref and commit.
set -Eeuo pipefail

repo_root="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${repo_root}"

fail() { printf 'bump-orbit-pin: %s\n' "$1" >&2; exit 1; }

ref="${1:-}"
[[ -n "$ref" ]] || fail 'a branch or tag is required as the first argument, e.g. preview'
# The short name only: the live job reads the manifest by ref from GitLab's
# artifacts endpoint, which rejects refs/heads/... (#273).
[[ "$ref" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ && "$ref" != refs/* ]] ||
  fail "not a plain branch or tag name: ${ref}"

host="${ORBIT_GITLAB_HOST:-gitlab.tomlawson.io}"
project="${ORBIT_PROJECT:-ai/orbit}"

resolve_with_gitlab() {
  command -v glab > /dev/null || fail 'glab is not installed'
  command -v jq > /dev/null || fail 'jq is not installed'
  glab api --hostname "$host" \
    "projects/${project//\//%2F}/repository/commits/${1//\//%2F}" | jq -r .id
}

resolve=resolve_with_gitlab
[[ -z "${BUMP_ORBIT_PIN_RESOLVE:-}" ]] || resolve="$BUMP_ORBIT_PIN_RESOLVE"
errors="$(mktemp)"
trap 'rm -f "$errors"' EXIT
commit="$("$resolve" "$ref" 2> "$errors")" ||
  fail "could not resolve ${ref} on ${project} at ${host} (no such branch or tag, or GitLab could not be read): $(cat "$errors")"
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || fail "resolved commit is not a 40-hex sha: ${commit}"

pin_path="${repo_root}/orbit/pin.json"
mkdir -p "$(dirname "$pin_path")"
cat > "${pin_path}.tmp" <<JSON
{
  "branch": "${ref}",
  "commit": "${commit}"
}
JSON
mv "${pin_path}.tmp" "$pin_path"

printf 'bump-orbit-pin: orbit/pin.json now pins %s at %s\n' "$ref" "$commit"
