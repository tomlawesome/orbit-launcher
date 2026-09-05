# orbit-launcher agent instructions

Created 2026-08-22 because the repo had no agent file at all. It records
what was observable from the repository itself; anything marked **(to
confirm)** is an inference the owner should correct or ratify.

## What this project is for

A dedicated terminal application (TUI) for installing, updating, and
repairing **Orbit** — full-screen, animated starfield, static Orbit mark,
and a small set of clear choices.

It supersedes the bash "command centre" approach from `orbit#260`. That
work dressed up `install.sh` with more terminal control codes; this is a
proper application instead. Keep that distinction: **changes that push
this back toward being a shell-script wrapper are going the wrong way.**

Go module `github.com/tomlawesome/orbit-launcher`. Licensed AGPL-3.0 (see
`LICENSING.md`).

Layout:

- `cmd/orbit-launcher` — entry point
- `internal/ui` — the TUI
- `internal/engine` — install/update/repair logic
- `internal/deploy`, `internal/release` — deployment and release handling
- `scripts/get-orbit-launcher.sh` — the bootstrap script users curl
- `tools/calculateversion` — version derivation
- `docs/implementation-plan.md`, `docs/releasing.md` — the roadmap and
  the release contract

## This repo installs Orbit; it does not edit Orbit

This project's whole purpose is acting **on** Orbit, so reading the Orbit
repo as reference is expected; modifying it is not.

## Hosts: GitLab first, GitHub is the mirror

Since 2026-09-04 (#140) the project lives at
`gitlab.tomlawson.io/ai/orbit-launcher` (project id 50): issues, merge
requests, protected branches and the CI gate are there. Remote `gitlab`.

GitHub `tomlawesome/orbit-launcher` (remote `origin`) is a push mirror
GitLab updates after every merge, plus the public download source: GitHub
Releases, `promote.yml`, `release-preview.yml` and CodeQL stay there.
Nothing is filed, pushed or merged on GitHub by hand.

- Mirror identity: a GitLab-generated SSH key held as a write-access deploy
  key on GitHub; "Deploy keys" is the only bypass on GitHub's rulesets.
- Issues 1–148 have the same number on both hosts; from 149 on, GitLab only.
- Runner: `orbit-launcher-build` (privileged, for the live test); everything
  else runs on the shared group runner.
- Agent credential: `glab` with `GLAB_CONFIG_DIR=~/.config/glab-claude` (or
  the Codex one) and `GITLAB_TOKEN` unset; the repo-local git credential
  helper for the `gitlab` remote is `!glab auth git-credential`.
- Start a pipeline by hand with `gl-pipeline-run ai/orbit-launcher <ref>
  RUN_LIVE=true` to include the live test.

## Branching and review

Flow: feature branch → `dev` → `preview` → `main`, all by merge request on
GitLab, with `gh-pages` for the published site.

- Protected branches are `dev`, `preview`, and `main` on both hosts.
- Approval to merge into `dev` is not approval to promote.

## Commit style

Observed convention is **Conventional Commits** with an issue reference:
`feat: name the database that blocks a fresh install (#105)`,
`test: gate the live suite's volume pre-flight too (#105)`. Match it.

## CI

Six workflows, several of which are unusually expensive to break:

- `ci.yml`, `codeql.yml` — the standard gates (dependency review moved to
  the GitLab `deps` job when merges left GitHub; see `.gitlab-ci.yml`)
- `live-install-test.yml` — a real install, end to end
- `visual-regression.yml` — the TUI is a visual product; screenshots are
  part of the contract
- `release-preview.yml`, `promote.yml` — the release lane

`.gitlab-ci.yml` is the gate merges wait on: `fast`, `deps`, `gitleaks`, `visual`
(when web sources change) and `live` (MR label `run-live-matrix` or
`RUN_LIVE=true`). The GitHub workflows still run on the mirrored push as a
second opinion; a failure there is advisory and never blocks a GitLab merge.
Keep the two in step when changing a check.

Because a TUI's output *is* its interface, treat a visual-regression
failure as a real failure and look at the diff. Do not re-run it hoping
for green.

## Run it before you ship it

This is a full-screen terminal application: its correctness includes how
it *looks* and how it behaves under a real terminal. A change that passes
`go test` and was never run is not verified. Where something genuinely
cannot be exercised here, say so plainly in the PR rather than letting
"tested" imply more than was observed.
