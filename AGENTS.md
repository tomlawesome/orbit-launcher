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
- Merges: ask the owner first; once they say merge, run the merge call
  yourself (`glab api -X PUT …/merge_requests/<iid>/merge`, reading `sha`
  from the MR in the same command). Handoffs from 2026-10-23 that say to hand
  the merge over were wrong (owner, 2026-10-24).
- Merges are semi-linear (since 2026-10-29): an MR merges only if its source
  holds the target's tip, and GitLab's automatic rebase must never touch
  `dev` or `preview`. After each release, merge `main` into a `chore/` branch
  off `dev` and land it before the next promotion.
- Only the owner presses `release:patch` or `release:minor` (on `main`
  pipelines), which cut the release tag (`docs/releasing.md`).
- A green `go test` without running the TUI is not verified. Say plainly what
  could not be exercised here.

## Live job

- Stages (#306): live runs by itself, blocking, on every MR into `preview`,
  and on an MR into `main` from any branch but `preview`, and nowhere else by
  default. On an MR into either from another branch (a hotfix) a person
  starts it if the MR also edits the CI files (#313). Pushes to `dev`, `preview`, `main` and to a branch with no MR run
  nothing (`main` only offers the release buttons); `preview` into `main`
  runs a no-op. An MR into `dev` runs the checks, and live
  only on the MR label `run-live-matrix`, or `RUN_LIVE=true` on a
  hand-started pipeline. An MR into `dev` that moves the `UBUNTU_IMAGE`,
  `FEDORA_IMAGE`, `GO_VERSION` or `GO_SHA256` pin, or edits `.gitlab-ci.yml`,
  `scripts/ci/fetch-verified.sh`, `scripts/ci/run-unprivileged.sh` or
  `orbit/pin.json`, needs it started by hand (#192, #216, #292, #193): first
  `live-prepare`, which holds the Orbit read token and the legs need, then the
  legs (#304). A hotfix MR into `preview`/`main` that edits those files waits
  the same way.

- Two legs, `live` (Ubuntu) and `live-fedora` (#183), from one `.live_leg`
  template; both must pass.
- The update leg (#198) runs by default from the previous build ai/orbit's
  latest `record_image` keeps in `.orbit-supply-chain/previous/` (#263), and
  fails if there is none. `ORBIT_FIRST_RECORD_IMAGE_JOB` and
  `ORBIT_FIRST_SIGN_EVIDENCE_JOB` on a hand-started pipeline override it: the
  job IDs of an earlier signed ai/orbit preview pipeline. Never give them a
  default. Its first install runs that release's own launcher, which
  `live-prepare` fetches (from `previous/`, then from `build_launcher`) and
  checks against the signed manifest (#307, #313).
- Orbit is pinned in `orbit/pin.json` (branch and commit, #193); move it with
  `scripts/bump-orbit-pin.sh <ref>`. `ORBIT_BRANCH`, empty by default, set on
  a hand-started pipeline overrides the pin: files from that branch's tip.
- A contract change goes: Orbit side promoted to Orbit `preview` (or a
  `hotfix/*` branch; only those produce the signed release manifest live
  reads) → bump `orbit/pin.json` here (live green with Orbit's install files
  fixed at the pin; its image still follows that branch's latest build) →
  launcher release → Orbit bumps `launcher/pin.json`.
- Every GitLab live run is a preview run (the pin's branch and `ORBIT_CHANNEL`
  are preview). It fails only if the job falls back to the GitHub mirror,
  which has no release manifest (#194): `live-prepare`, which reads Orbit's
  files for both legs, got 401/403 because `ORBIT_READ_TOKEN` is missing or
  expired (#303), or `ORBIT_FILES_SOURCE=github` is set.
  `orbit_branch` is an input of the GitHub workflow only, which is stable-only (#196).
- `test/live/screens.go` is a compatibility contract with the launcher the
  earlier Orbit release ships: a renamed screen is added beside the old
  wording, removed only when that release's launcher draws the new one (#307).
