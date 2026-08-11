# v2/v3 implementation handovers

Bounded task briefs for delegating the approved design work (design/mockups-v2.html,
design/mockups-v3-flows.html) to the cheapest model that can do each job safely.
**All of these are contingent on the repository owner approving the mockups** — do
not start a task until its brief is explicitly assigned.

## Model assignment

| # | Task | Model | Why this tier | Depends on |
|---|------|-------|---------------|-----------|
| 1 | Centre all flow screens | Haiku 4.5 | Mechanical lipgloss change, existing idiom to copy from splash.go, failure is visually obvious | — |
| 2 | Status line + version footer | Haiku 4.5 | Small additive UI, data sources already exist | — |
| 3 | Splash orbital system (rotating starfield, sphere, planets, occlusion) | Sonnet 5 | Real compositor math (cell-space ellipses, z-order, aspect correction) but fully specified by the design doc + screenshots | — |
| 4 | Sphere states (dormant / lit / errored) | Haiku 4.5 | Palette + one conditional once the sphere exists | 3 |
| 5 | Size tiers (full / compact / minimal) | Sonnet 5 | Layout logic across many terminal sizes; needs judgement about degradation, plus visual-test updates | 3 |
| 6 | Mission console (install.sh inside a launcher-owned PTY viewport) | **frontier (Fable/Opus) — not delegated** | Reverses the issue #51 terminal-handoff architecture; PTY lifecycle, SIGWINCH, raw-mode, partial-read edge cases; highest blast radius | — |
| 7 | Ignition hero screen | Sonnet 5 | Medium UI work, but its entry transition couples to 6 | 6 |
| 8 | Arrival / travel transitions | Sonnet 5 | Choreography on the compositor built in 3 | 3 |

## Rules that apply to every task

- Branch from `develop`; ordinary issue branches target `develop`.
- Do **not** touch `.github/workflows/`, `docs/adr/`, `docs/implementation-plan.md`,
  or anything else listed in `.github/planning-governance.json` — those paths
  require a Planning-Model attestation and are out of scope for these briefs.
- PR body must follow `.github/pull_request_template.md`, including exactly one
  `Observability-Impact` declaration (these are all UI-only:
  `Observability-Impact: none — <reason>` with a real reason).
- Before opening a PR: `gofmt -l .` (must be empty), `go build ./... && go vet ./...`,
  `staticcheck ./...`, `go test -race ./...` all green locally.
- The visual-regression suite in `test/visual/` must be updated in the same PR
  as any change it covers — a PR that breaks it is not done.
- Never weaken or delete an existing test to make a change pass; if a test now
  asserts the old design, update it to assert the new design explicitly.
