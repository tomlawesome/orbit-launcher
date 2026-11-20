# Releasing

This repo no longer builds or publishes launcher programs. Orbit builds,
signs and ships the launcher with each Orbit release, pinned to a tag from
here so the two versions match.

## What stays here

- **Source, tags and CI.** Changes reach `dev` by merge request on GitLab.
  Each pipeline runs only what an earlier one could not (#306): test once,
  then promote what passed.

  | Pipeline | Runs |
  |---|---|
  | merge request into `dev` | the checks below |
  | push that lands it | nothing |
  | merge request `dev` into `preview` | `release_readiness` first, then the live installs, automatic and blocking |
  | merge request `preview` into `main` | only `release_readiness` (#359) |
  | merge request into `preview` or `main` from any other branch (a hotfix) | the checks and the live installs; a person starts the installs if it also edits the CI files |
  | push to `main` | only the release buttons |
  | push to a branch with no merge request | nothing |

  Renovate's schedule and pipelines started by hand (`RUN_LIVE=true`) still
  run as before. The checks on a merge request into `dev` are:
  - `classify`, which spots changes that touch only documentation and lets
    the heavy checks skip themselves;
  - `fast`: `gofmt`, `go vet`, `go build`, `shellcheck` and `bats` on the
    bootstrap script, `staticcheck`, and `go test -race ./...` (unit tests,
    in-memory TUI tests, and real-terminal tests against the built program,
    read through Charm's `x/vt` terminal emulator);
  - `deps`, the dependency and licence policy;
  - `gitleaks`, the secret scan;
  - `private-details`, the shared check that no real names or addresses are added;
  - `flake-check`, which fails when a flake in `docs/flakes.md` reaches three
    sightings without an issue and a line in `scripts/quarantine.txt`;
  - `visual`, screenshot checks, run when the screens, the visual tests,
    `.gitlab-ci.yml`, `scripts/ci/fetch-verified.sh` or
    `scripts/ci/run-unprivileged.sh` change;
  - `live`, a real install. Here it runs only when asked for (a
    merge-request label or a hand-started pipeline) and waits for a person
    when a change moves the pins it installs with. The promotion into
    `preview`, and any merge request into `preview` or `main` from another
    branch, runs it without being asked.

  CodeQL runs only on the GitHub mirror.
- **Version tags.** [`tools/calculateversion`](../tools/calculateversion)
  reads the highest existing `vMAJOR.MINOR.PATCH` git tag and adds one to
  the minor number (or, with `--hotfix`, the patch number). Before any tag
  exists, it starts at `0.1.0`. A tag here marks a source revision Orbit
  can pin and build from. It does not publish a program itself.
  The **release:patch** and **release:minor** buttons below take the patch
  and minor steps.

## What you do, in short

`go run ./tools/releasereadiness` reads the numbered lines of this section
(up to the next heading) as the steps a release record lists, so change them
only together with the record's template.

Before step 1, on `dev`, the release agent: on a `release/vX.Y.Z` branch
into `dev` (only `CHANGELOG.md` and `docs/releases/`), renames
`## [Unreleased]` in `CHANGELOG.md` to `## [X.Y.Z] - YYYY-MM-DD` and commits
the signed release record (below). Once that has merged, it fetches
(`git fetch --tags gitlab dev preview main`), checks out `dev` and runs
`go run ./tools/releasereadiness -target preview -remote gitlab`. It opens
the promotion only when the output ends "Ready".

1. The release agent opens `dev` -> `preview`; the owner approves; plain
   **Merge**. `release_readiness` runs first, then the live installs.
   Straight after the merge the release agent opens the `preview` -> `dev`
   back-merge.
2. The owner tests the release by hand on `preview`, or decides it needs no
   manual test, and says so.
3. The release agent checks out `preview` and runs
   `go run ./tools/releasereadiness -target main -remote gitlab`; when it
   ends "Ready", it opens `preview` -> `main`. If GitLab starts no pipeline
   (an agent-opened merge request, #333), the owner presses **Run
   pipeline**: without it `release_readiness` never runs. The merge
   request's reviewer checks the `CHANGELOG.md` section against the issues
   merged since the last release; the owner approves; plain **Merge**.
   Straight after, the release agent opens the `main` -> `preview`
   back-merge.
4. The owner presses the button `release_readiness` named
   (**release:patch** or **release:minor**) on the new `main` pipeline.
5. **release:gitlab:patch** or **release:gitlab:minor** creates the tag and
   its GitLab release, whose body is the version's `CHANGELOG.md` section.
   The release agent checks that the release page shows it.
6. In Orbit, an agent working there runs
   `scripts/bump-launcher-pin.sh vX.Y.Z` on Orbit's `dev`.

## Release readiness and the signed release record

`release_readiness` is the first job of both promotions, and nothing else
in the pipeline starts before it passes. It runs
`go run ./tools/releasereadiness -target <preview|main>` on the source
branch, the same command the release agent runs before opening the merge
request. Every check runs and prints `ok` or `FAIL`; the last line is
"Ready: every check passed." or "Not ready: N of M checks failed (...)".
The exit code is 0 when ready, 1 when not, 2 on a usage error. A check that
cannot read what it needs fails with "could not check: ...".

The version is the newest `## [X.Y.Z] - YYYY-MM-DD` heading in
`CHANGELOG.md` on the source branch. The tag checks read the remote's tags
from `refs/remote-tags/<remote>/`, which the tool fetches first; in CI the
job fetches them before it drops its credentials and runs the tool with
`-no-fetch`. If they could not be fetched, those checks fail with "could
not check".

Into `preview` (from `dev`):

- `version`: it is the next patch or the next minor after the highest tag.
- `tag-free`: the tag `vX.Y.Z` does not exist on the remote.
- `changelog`: the heading has exactly that shape, its section has at least
  one entry under Added, Changed, Deprecated, Removed, Fixed or Security,
  and no Unreleased section holds entries.
- `contains-target`: `dev` contains `preview` (otherwise merge the open
  `preview` -> `dev` back-merge first).
- `audit`: every non-merge commit in `preview..dev` is covered by an
  `Audited-through:` line in `.agents/reviews/*.md`, or touches only
  `docs/releases/`, `CHANGELOG.md` and `.agents/reviews/`; and each audit
  issue whose `Audited-through:` commit is in that range is closed. Reading
  issues needs a token that can read this project's issues, in the
  `RELEASE_READINESS_TOKEN` CI/CD variable or, locally, the environment
  with `CI_API_V4_URL` and `CI_PROJECT_ID`. Make it a project access token
  with the Reporter role, only the `read_api` scope and an expiry date, and
  save it masked and protected, so only pipelines on protected branches
  (the promotions among them) receive it. Without it that half
  fails with "check by hand that audit issue #N is closed".
- `orbit-pin`: `orbit/pin.json` names the tip of its Orbit branch, read from
  Orbit's public GitHub mirror (`ORBIT_GITHUB_REPO`, default
  `tomlawesome/orbit`) with no token. `live-prepare` stays the exact check:
  for the minutes Orbit's build of that tip is still running, this passes
  and `live-prepare` refuses.
- `release-record`: below.

Into `main` (from `preview`): `tag-free`, `changelog`, `audit` (over
`main..preview`) and `release-record` as above, and:

- `main-tree-equals-preview`: `main` is contained in `preview` and the
  merge leaves exactly `preview`'s files (otherwise merge the `main` ->
  `preview` back-merge first).
- `button`: names the release button, **release:patch** or
  **release:minor**, that gives the `CHANGELOG.md` version, and fails when
  neither does (tags moved since the preview promotion, for example by a
  hotfix).

### The signed release record

`docs/releases/vX.Y.Z.md`, copied from
[`docs/releases/TEMPLATE.md`](releases/TEMPLATE.md), reaches `dev` on the
`release/vX.Y.Z` branch before the promotion into `preview`, and is not
changed for `main` (`main` must get `preview`'s exact tree). It holds:

```
version: vX.Y.Z
guide-blob: <git rev-parse HEAD:docs/releasing.md>
guide-commit: <git log -1 --format=%H -- docs/releasing.md>
step 1: done: <link or job id> | not-needed: <owner decision> | after-merge
... one line per numbered step of "What you do, in short", in order
Signed-off-by: <your name, as git config user.name>
```

The check, on the source branch's tip:

- the file exists, and its `version:` is the `CHANGELOG.md` version;
- it has exactly one `Signed-off-by:` line, a name with no email, equal to
  the author of the last commit that touched the file and to a
  `Signed-off-by` trailer of that commit: commit it with
  `git commit --trailer "Signed-off-by: <your name>"` (not `git commit -s`,
  which adds an email);
- `guide-blob:` is this guide's current blob id; if the guide changed,
  re-read it and regenerate the record (a rebase does not change the blob);
- one `step N:` line per step above, none extra, none repeated, in order.
  `done:` and `not-needed:` need real evidence: an empty value or a
  placeholder (`<...>`, todo, tbd, tbc, `...`, `-`, n/a, none, x) counts as
  left out. `after-merge` needs none.

### What the job cannot check

| What | Who |
|---|---|
| approving both promotion merge requests | the owner |
| the manual test on `preview` (step 2) | the owner |
| **Run pipeline** on an agent-opened `preview` -> `main` merge request | the owner |
| the `CHANGELOG.md` section against the issues merged since the last release | the merge request's reviewer |
| pressing the release button | the owner |
| opening the back-merges | the release agent |
| the GitLab release body showing the `CHANGELOG.md` section | the release agent |
| Orbit's pin bump | an agent working in Orbit |

## Cutting a tag

Tags are made by a CI button, never by hand.

1. Promote what the release needs (see "What you do, in short"): merge
   `dev` into `preview` (its merge request runs `release_readiness`, then
   `live` by itself), then `preview` into `main`, which runs only
   `release_readiness`. Merging into `main` starts the `main` pipeline, which
   offers only the release buttons.
   Merge each promotion with the plain **Merge** button, never **Rebase**:
   a rebase copies `dev`'s commits onto `preview` as new ones. If the
   `preview` into `main` merge request was opened by the agent's account,
   GitLab starts no pipeline for it (that account may not run pipelines on
   `preview`, #333): press **Run pipeline** on its Pipelines tab.
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
   commit, as whoever pressed the button. The release body is the
   version's `CHANGELOG.md` section; the job stops before tagging if
   `CHANGELOG.md` has no `## [<version>] - ` section or that section is
   blank. It does not check the date or that the section lists entries:
   `release_readiness` checks those on a promotion. GitLab's push mirror carries the
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
