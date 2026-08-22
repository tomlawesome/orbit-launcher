# orbit-launcher agent instructions

Applies to Claude Code and any other AI tooling working in this
repository, alongside the global agent instructions in
`~/.claude/AGENTS.md`.

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

This project's whole purpose is acting **on** Orbit, which makes it the
most likely place to slip across the global stay-inside-your-project
boundary. Reading Orbit as reference is fine and expected; if work here
appears to need an Orbit change, say so and stop — cross-project writes
follow the global scope rule.

## Branching and review

Observed flow: feature branch → `develop` → `preview` → `main`, with
`gh-pages` for the published site. Recent feature branches use a
`claude/<topic>` prefix.

- Protected branches here are `develop`, `preview`, and `main`.
- **A promotion toward `main` needs a fresh, explicit instruction each
  time.** Approval to merge into `develop` is not approval to promote.

## Commit style

Observed convention is **Conventional Commits** with an issue reference:
`feat: name the database that blocks a fresh install (#105)`,
`test: gate the live suite's volume pre-flight too (#105)`. Match it.

> **Discrepancy to resolve (to confirm).** 23 of the last 60 commits here
> carry AI attribution trailers, so this repo's history does not match
> the global no-AI-attribution rule the owner set on 2026-08-22. History
> is not to be rewritten, so those stay; the rule applies from now on.
> The `claude/<topic>` branch prefix sits in the same grey area and has
> not been ruled on.

## CI

Eight workflows, several of which are unusually expensive to break:

- `ci.yml`, `codeql.yml`, `dependency-review.yml` — the standard gates
- `live-install-test.yml` — a real install, end to end
- `installer-compat-watch.yml` — guards the bootstrap script's
  compatibility surface
- `visual-regression.yml` — the TUI is a visual product; screenshots are
  part of the contract
- `release-preview.yml`, `promote.yml` — the release lane

Because a TUI's output *is* its interface, treat a visual-regression
failure as a real failure and look at the diff. Do not re-run it hoping
for green.

## Environment

The live install test exercises containers, so the global environment
constraints (rootless Docker, container-to-host networking) apply here
in practice.

## Run it before you ship it

This is a full-screen terminal application: its correctness includes how
it *looks* and how it behaves under a real terminal. A change that passes
`go test` and was never run is not verified. Where something genuinely
cannot be exercised here, say so plainly in the PR rather than letting
"tested" imply more than was observed.
