# Implementation handovers — all resolved

**Closed 2026-08-12.** Every surviving brief below was implemented — none were
delegated in the end; the work landed as frontier-model PRs: the v5 splash
(#74), the mission console + success screen (#75), and the v6 starchart pass
(#77), which also superseded parts of briefs 01/02/06 (normal-size wordmark,
centred menu grammar, version-only centred foot — see
design/mockups-v6-starchart.html for the owner-locked decisions). These files
stay as the record of what was planned and why; nothing here is a live task.

Bounded task briefs for the surviving design decisions. **Scope was cut on
2026-08-11**: the v2/v3 orbital-system visuals (ring/sphere, orbiting planets,
corona, ignition/arrival animation) are **scrapped for the TUI** — faithful
cell rendering (design/mockups-terminal.html) showed they don't survive the
medium, and the shipped v1 splash is the final TUI aesthetic. v2/v3 mockups
are retained solely as reference for a future Orbit web UI rebrand.

Four decisions survive, confirmed by the repository owner:

## Model assignment

| # | Task | Model | Why this tier | Status |
|---|------|-------|---------------|--------|
| 1 | Centre all flow screens + version corner | Haiku 4.5 | Mechanical lipgloss change, existing idiom to copy from splash.go | **done** — #75/#77 (corner became the centred foot) |
| 2 | Status line + status vocabulary (dormant/alive/degraded, amber mark) | Haiku 4.5 | Small additive UI; detection/health data already exists | **done** — #74 |
| 3 | ~~Splash orbital system~~ | — | **Withdrawn** — embellishment scrapped; shipped splash is final | withdrawn |
| 4 | ~~Sphere health states~~ | — | **Superseded** — folded into brief 02 (mark colour + status line, no sphere) | superseded |
| 5 | ~~Size tiers~~ | — | **Withdrawn** — existed to scale the sphere; shipped splash already degrades acceptably | withdrawn |
| 6 | Mission console (install.sh inside a launcher-owned PTY viewport) | **frontier (Fable/Opus) — not delegated** | Reverses the issue #51 terminal-handoff architecture; PTY lifecycle, SIGWINCH, raw-mode edge cases | **done** — #75, via the engine event stream (#73), preserving #51: no PTY viewport needed |
| 7 | Success screen: copy + layout ("Orbit achieved", URL hero, stacked actions) | Sonnet 5 | Plain text screen, but its entry couples to the mission console | **done** — #75, restyled by #77 |

## Rules that apply to every task

- **The cell-truth bar**: any visual change must read at 80×26/10fps with
  glyph-ramp brightness — no idea that needs blur or 60fps. When in doubt,
  prototype it in design/mockups-terminal.html's renderer first.
- Branch from `develop`; ordinary issue branches target `develop`.
- Do **not** touch `.github/workflows/`, `docs/adr/`, `docs/implementation-plan.md`,
  or anything else listed in `.github/planning-governance.json`.
- PR body must follow `.github/pull_request_template.md`, including exactly one
  `Observability-Impact` declaration.
- Before opening a PR: `gofmt -l .` (must be empty), `go build ./... && go vet ./...`,
  `staticcheck ./...`, `go test -race ./...` all green locally.
- The visual-regression suite in `test/visual/` must be updated in the same PR
  as any change it covers.
- Never weaken or delete an existing test to make a change pass; if a test
  asserts the old design, update it to assert the new design explicitly.
