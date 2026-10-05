# Releasing

This repo no longer builds or publishes launcher programs. Orbit builds,
signs and ships the launcher with each Orbit release, pinned to a tag from
here so the two versions match.

## What stays here

- **Source, tags and CI.** Changes reach `dev` by merge request on GitLab.
  The checks that must pass before a merge are:
  - `classify`, which spots changes that touch only documentation and lets
    the heavy checks skip themselves;
  - `fast`: `gofmt`, `go vet`, `go build`, `shellcheck` and `bats` on the
    bootstrap script, `staticcheck`, and `go test -race ./...` (unit tests,
    in-memory TUI tests, and real-terminal tests against the built program,
    read through Charm's `x/vt` terminal emulator);
  - `deps`, the dependency and licence policy;
  - `gitleaks`, the secret scan;
  - `visual`, screenshot checks, run when the screens, the visual tests or `.gitlab-ci.yml` change;
  - `live`, a real install. It runs when asked for (a merge-request label
    or a hand-started pipeline) and when a change moves the pins it
    installs with.

  CodeQL runs only on the GitHub mirror.
- **Version tags.** [`tools/calculateversion`](../tools/calculateversion)
  reads the highest existing `vMAJOR.MINOR.PATCH` git tag and adds one to
  the minor number (or, with `--hotfix`, the patch number). Before any tag
  exists, it starts at `0.1.0`. A tag here marks a source revision Orbit
  can pin and build from. It does not publish a program itself.
  The **release:version** button below always takes the minor step: CI has
  no way to cut a patch (hotfix) tag yet.

## Cutting a tag

Tags are made by a CI button, never by hand.

1. Merge what the release needs into `dev` and wait for that `dev`
   pipeline to go green.
2. Open that pipeline on GitLab (the button only appears on `dev`
   pipelines) and press **release:version**. It picks the next version
   number. It stops if that tag already exists, if a higher tag already
   exists, or if newer commits have landed on `dev` since. If it stops,
   use the newest `dev` pipeline instead.
3. **release:gitlab** runs by itself and creates the annotated tag
   `v<version>` and its GitLab release at that commit, as whoever pressed
   the button. GitLab's push mirror carries the tag to GitHub.
4. In the Orbit repository, on its `dev` branch, move Orbit's pin to the
   new tag with `scripts/bump-launcher-pin.sh v<version>`.

## What moved to Orbit

Orbit's own pipeline pins a launcher tag and commit, builds it, signs a
manifest covering the launcher archives, the image digest and the install
scripts, and attaches everything to Orbit's releases. Orbit's
`get-orbit.sh` checks that signature before running the launcher.

`scripts/get-orbit-launcher.sh` in this repo downloads nothing any more.
It points anyone using its old URL at Orbit's installer. See the
[README](../README.md).

Background: Orbit issue ai/orbit#1107 and Orbit ADR-0031 made this change
here (#171). The tag button came with #178.
