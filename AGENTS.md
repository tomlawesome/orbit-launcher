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

## Branching and review

Observed flow: feature branch → `dev` → `preview` → `main`, with
`gh-pages` for the published site.

- Protected branches here are `dev`, `preview`, and `main`.
- Approval to merge into `dev` is not approval to promote.

## Commit style

Observed convention is **Conventional Commits** with an issue reference:
`feat: name the database that blocks a fresh install (#105)`,
`test: gate the live suite's volume pre-flight too (#105)`. Match it.

## CI

Seven workflows, several of which are unusually expensive to break:

- `ci.yml`, `codeql.yml`, `dependency-review.yml` — the standard gates
- `live-install-test.yml` — a real install, end to end
- `visual-regression.yml` — the TUI is a visual product; screenshots are
  part of the contract
- `release-preview.yml`, `promote.yml` — the release lane

`.gitlab-ci.yml` reproduces the gate for the move to the owner's GitLab
(#140, GitLab-first migration). Until that issue's step 3 flips the mirror,
GitHub is still the source and the GitLab pipeline is a second copy: keep
the two in step when changing a check.

Because a TUI's output *is* its interface, treat a visual-regression
failure as a real failure and look at the diff. Do not re-run it hoping
for green.

## Run it before you ship it

This is a full-screen terminal application: its correctness includes how
it *looks* and how it behaves under a real terminal. A change that passes
`go test` and was never run is not verified. Where something genuinely
cannot be exercised here, say so plainly in the PR rather than letting
"tested" imply more than was observed.
