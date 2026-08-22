# Releasing

orbit-launcher ships through three lanes — `dev`, `preview`,
`main` — mirroring [orbit](https://github.com/tomlawesome/orbit)'s own
branch-protection model, adapted from container-image promotion to
binary releases. Nothing is rebuilt between lanes: the exact bytes
tested on preview are the exact bytes promoted to stable.

## The lanes

### `dev` — integration

Feature branches (`codex/issue-N-description`) merge here via PR. Every
PR runs the full CI suite: `gofmt`, `go vet`, `go build`, `shellcheck`
on the bootstrap script, `bats` against it, `staticcheck`, planning
governance, and `go test -race ./...` (unit tests, `teatest`
in-memory TUI tests, and real-PTY `go-expect` tests against the actual
compiled binary). CodeQL and the dependency/licence policy check also
run on every PR.

### `preview` — release candidate

A "release train" PR from `dev` merges into `preview`. This runs
the same CI suite again, then
[`release-preview.yml`](../.github/workflows/release-preview.yml)
builds real Linux amd64/arm64 archives with `goreleaser`, attests their
build provenance, and publishes them as the GitHub Release tagged
`preview-latest` — a mutable pre-release that always points at the
newest commit on `preview`. Its version string embeds the exact commit
SHA it was built from (e.g. `0.1.0-preview.<sha>`), so a preview build
is always traceable back to source.

`preview-latest` is what [the quickstart](../README.md#quickstart)
points at before v1.0.0 ships, and what CI itself downloads to sanity
check the bootstrap script end to end.

### `main` — stable

A PR from `preview` into `main` carries a lane-specific gate: the
`Verify tested preview for stable merge` job (only runs when the PR
targets `main`) downloads the current `preview-latest` release assets
and checksums them, refusing to pass unless what's being merged is
what was actually built and shipped as a preview — code can't skip
the preview lane and land on `main` untested.

Once merged, promotion to a stable, immutable release is a separate,
deliberate step — see below.

## Promoting a stable release

Promotion never rebuilds anything; it takes the exact archives already
published as `preview-latest`, verifies their checksums and build
provenance attestation, confirms the commit they were built from is
now an ancestor of `main`, and republishes those same bytes under a
real semantic-version tag (`vX.Y.Z`, read from the binary's own
`--version` output).

This runs as [`promote.yml`](../.github/workflows/promote.yml), a
manual `workflow_dispatch` gated on the `production` GitHub
Environment. That environment requires a reviewer to approve the run —
this is the one step in the whole pipeline that is deliberately never
automatic, matching orbit's own model of a human accepting a specific,
already-tested build before it becomes the thing people install by
default.

## Version numbers

[`tools/calculateversion`](../tools/calculateversion) reads the
highest existing stable `vMAJOR.MINOR.PATCH` git tag and increments
minor (or, with `--hotfix`, patch) for the next release train; before
any stable tag exists, the baseline is `0.1.0`. `release-preview.yml`
appends a `-preview.<sha>` suffix to that for pre-release builds. A
stable release tag is exactly `v<version>` with no suffix, and is only
ever created by `promote.yml`.
