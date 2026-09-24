# Releasing

Since #171 (`ai/orbit#1107`, ADR-0031) this repo no longer builds or
publishes runnable launcher binaries. Orbit builds, signs and ships the
launcher alongside itself, pinning a launcher tag and commit, so the two
stay version-matched.

## What stays here

- **Source, tags and CI.** Feature branches merge into `dev` via merge
  request, running the full CI suite: `gofmt`, `go vet`, `go build`,
  `shellcheck` on the bootstrap script, `bats` against it, `staticcheck`,
  and `go test -race ./...` (unit tests, `teatest` in-memory TUI tests,
  and real-PTY `go-expect` tests against the compiled binary). CodeQL and
  the dependency/licence policy also run.
- **Semantic version tags.** [`tools/calculateversion`](../tools/calculateversion)
  reads the highest existing `vMAJOR.MINOR.PATCH` git tag and increments
  minor (or, with `--hotfix`, patch) for the next release; before any tag
  exists, the baseline is `0.1.0`. A tag here marks a source revision
  Orbit can pin to build from — it doesn't publish a binary itself.

## What moved to Orbit

Orbit's own pipeline pins a launcher tag and commit, builds it, signs a
manifest covering the launcher archives, the image digest and the install
scripts, and attaches everything to Orbit's releases. Orbit's
`get-orbit.sh` checks that signature before running the launcher.

`scripts/get-orbit-launcher.sh` in this repo is developer-only now (behind
`ORBIT_LAUNCHER_DEVELOPER=1`); everyone else is pointed at Orbit's
installer. See the [README](../README.md#quickstart).
