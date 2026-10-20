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
  The **release:patch** and **release:minor** buttons below take the patch
  and minor steps.

## Cutting a tag

Tags are made by a CI button, never by hand.

1. Promote what the release needs (`dev` into `preview` into `main`, each
   passing `live`) and wait for the `main` pipeline to go green.
2. Open the newest `main` pipeline on GitLab (the buttons only appear on
   `main` pipelines) and press **release:patch** (a fix on the current
   minor, e.g. 0.4.1 to 0.4.2) or **release:minor** (e.g. 0.4.1 to
   0.5.0). One press is all it takes. The button picks the next version
   number. It stops if that tag already exists, if a higher tag already
   exists, if this commit already has a tag, or if newer commits have
   landed on `main` since. If it stops, use the newest `main` pipeline
   instead.
3. **release:gitlab:patch** or **release:gitlab:minor** runs by itself and
   creates the annotated tag `v<version>` and its GitLab release at that
   commit, as whoever pressed the button. GitLab's push mirror carries the
   tag to GitHub. Tagging jobs run one at a time, so a second press
   on the same commit stops: "already tagged" after the other button, or
   "already exists on the remote" after the same button again. It stops at
   `release:gitlab:*` if both presses were close together, otherwise at
   the button. The next tag goes on the next commit.
4. In the Orbit repository, on its `dev` branch, move Orbit's pin to the
   new tag with `scripts/bump-launcher-pin.sh v<version>`.

### v0.4.1, the one tag made by hand

v0.4.1 needed a patch tag, which the old single button (minor only, on
`dev`) could not make, and the v0.4.0 tag stays where it is because Orbit
pins it. So, once only (#163), the owner made it by hand on GitLab's
**New tag** page: an annotated tag `v0.4.1` at `main`'s promotion commit
(`90ad87b`), after `dev` → `preview` → `main` had all passed `live`. The
push mirror carried it to GitHub. Patch and minor buttons on `main`
pipelines replaced that button (#264); tags are no longer made by hand.

## What moved to Orbit

Orbit's own pipeline pins a launcher tag and commit, builds it, signs a
manifest covering the launcher archives, the image digest and the install
scripts, and attaches everything to Orbit's releases. Orbit's
`get-orbit.sh` checks that signature before running the launcher.

`scripts/get-orbit-launcher.sh` in this repo downloads nothing any more.
It points anyone using its old URL at Orbit's installer. See the
[README](../README.md).

Background: Orbit issue ai/orbit#1107 and Orbit ADR-0031 made this change
here (#171). The tag button came with #178; the patch and minor buttons on `main` with #264.
