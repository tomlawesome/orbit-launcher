# orbit-launcher agent instructions

A full-screen terminal application (TUI) for installing, updating and repairing
Orbit: animated starfield, static Orbit mark, a few clear choices. Layout:
`docs/implementation-plan.md`. Hosts, CI and releases: `.gitlab-ci.yml`
and `docs/releasing.md`. Commit style: `CONTRIBUTING.md`.

- Not a shell-script wrapper (it supersedes `orbit#260`'s bash command centre):
  changes pushing it back that way are wrong. Module
  `github.com/tomlawesome/orbit-launcher`, AGPL-3.0 (`LICENSING.md`).
- The Orbit family (`ai/orbit`, `ai/orbit-launcher`, `ai/orbit-site`, `ai/orbit-base-image`) is one project (Orbit ADR-0036): each repo's files, layout and scripts are the whole project's own, never designed around as an outside system. The launcher owns install, update, repair and removal; removal work belongs in the launcher, not a separate tool in Orbit.
- When work here needs a change in another of the four, do not make it from here: file an issue there with the design fully worked out, labelled `cross-repo`, in that repo's nearest open milestone, and link it from this repo's issue, parked as blocked. That repo's session builds and closes it; on resuming, check blocked issues, and bump any pin once the other repo has released. A `cross-repo` issue is ordinary work in its milestone. ADR-0036 still covers reading sibling repos and one-line pin or doc fixes (owner, 2026-10-22: avoids two sessions editing one repo at once).
- Mockups are shown to the owner only from the local design container (`orbit-launcher-design-host`, nginx, ports from 8307), with direct links in chat; never uploaded to issues or published on Pages (owner, 2026-10-21). Starting it needs no go-ahead: a standing auto-mode rule in the Claude settings allows it.

## Traps

- A visual-regression failure is real: look at the diff, never re-run hoping
  for green.
- After any `go.mod` change, run `go run ./tools/licencenotices` and commit
  `internal/notices/THIRD_PARTY_NOTICES.txt`, or `deps` fails.
- A Renovate bump of a licence-reviewed module fails `deps` until its files
  are re-read. A licence decision there is the owner's.
- Code a module carries from elsewhere, named only in its comments, gets a
  hand-kept notice in `internal/notices/extra/`.
- `deps` has no GitHub counterpart on purpose: it replaced GitHub's dependency
  review when merges moved to GitLab (#140).
- Tool pins live in `.gitlab-ci.yml`'s variables. The GitHub workflows copy
  them by hand, and `test/supplychain` fails when a copy differs (#216).
- Every pinned download on both hosts goes through
  `scripts/ci/fetch-verified.sh <url> <sha256> <dest>`, never a hand-typed
  checksum line (#216).
- GitHub workflow failures are advisory; they never block a GitLab merge. Change
  a check on both hosts together.
- Only the owner presses `release:patch` or `release:minor` (on `main`
  pipelines), which cut the release tag (`docs/releasing.md`).
- A green `go test` without running the TUI is not verified. Say plainly what
  could not be exercised here.

## Live job

- Runs on the MR label `run-live-matrix` or `RUN_LIVE=true` on a hand-started
  pipeline. An MR that moves the `UBUNTU_IMAGE`, `GO_VERSION` or `GO_SHA256`
  pin, or edits `.gitlab-ci.yml` or `scripts/ci/fetch-verified.sh`, needs it
  started by hand (#192, #216).

- The update leg (#198) skips unless the pipeline is started with
  `ORBIT_FIRST_RECORD_IMAGE_JOB` and `ORBIT_FIRST_SIGN_EVIDENCE_JOB`: the
  `record_image` and `sign_evidence` job IDs of an earlier signed ai/orbit
  preview pipeline. The job token can't list pipelines to find them itself.
- Every GitLab live run is a preview run (`ORBIT_BRANCH` and `ORBIT_CHANNEL`
  default to preview). It fails only if the job falls back to the GitHub mirror,
  which has no release manifest (#194): the job token got 401/403 reading
  ai/orbit (as on Renovate's MRs), or `ORBIT_FILES_SOURCE=github` is set.
  `orbit_branch` is an input of the GitHub workflow only, which is stable-only (#196).
