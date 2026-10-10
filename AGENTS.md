# orbit-launcher agent instructions

A full-screen terminal application (TUI) for installing, updating and repairing
Orbit: animated starfield, static Orbit mark, a few clear choices. Layout:
`docs/implementation-plan.md`. Hosts, CI and releases: `.gitlab-ci.yml`
and `docs/releasing.md`. Commit style: `CONTRIBUTING.md`.

- Not a shell-script wrapper (it supersedes `orbit#260`'s bash command centre):
  changes pushing it back that way are wrong. Module
  `github.com/tomlawesome/orbit-launcher`, AGPL-3.0 (`LICENSING.md`).
- One project with Orbit, its base image and the site: act across them without
  asking (`ai/orbit` ADR-0036, `docs/adr/0036-the-orbit-family-is-one-project.md`).
- `gh-pages` is the branch for the published design iterations.

## Traps

- A visual-regression failure is real: look at the diff, never re-run hoping
  for green.
- After any `go.mod` change, run `go run ./tools/licencenotices` and commit
  `internal/notices/THIRD_PARTY_NOTICES.txt`, or `deps` fails.
- A Renovate bump of a licence-reviewed module fails `deps` until its files
  are re-read. A licence decision there is the owner's.
- Code a module carries from elsewhere, named only in its comments, gets a
  hand-kept notice in `internal/notices/extra/`.
- A merge request that edits `.gitlab-ci.yml` needs the `live` job started by
  hand (#192).
- GitHub workflow failures are advisory; they never block a GitLab merge. Change
  a check on both hosts together.
- Only the owner presses `release:version`, which cuts the release tag
  (`docs/releasing.md`).
- A green `go test` without running the TUI is not verified. Say plainly what
  could not be exercised here.
