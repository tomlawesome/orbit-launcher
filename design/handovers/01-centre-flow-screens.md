> **Resolved 2026-08-12** — implemented across #75/#77. The "version corner" became the single centred foot (a later owner decision); the centring grammar shipped.

# Handover 01 — Centre all flow screens

**Model: Haiku 4.5 · no dependencies · read design/handovers/README.md first**

## Problem

`SplashModel.View()` centres its output (`lipgloss.PlaceHorizontal(m.width,
lipgloss.Center, …)` per row plus a vertical offset). Every other screen —
`InstallModel`, `UpdateModel`, `RemoveModel`, `RepairModel` — renders through a
`frame(body)` helper that is just `lipgloss.NewStyle().Padding(1, 2).Render(body)`,
so their content hugs the top-left. The original design
(`design/mockups.html`, `.screen { align-items: center; justify-content: center; }`)
and the v2 direction doc (`design/mockups-v2.html`, section 04) both specify that
**every** screen is centred, horizontally and vertically.

## Scope

- In `internal/ui/`, change the frame/render path of `InstallModel`,
  `UpdateModel`, `RemoveModel`, and `RepairModel` so the content block is centred
  in the terminal both axes. Use `lipgloss.Place(m.width, m.height,
  lipgloss.Center, lipgloss.Center, body)` or the splash's row-based approach —
  match whichever fits each model's existing structure with the smallest diff.
- The content block itself stays left-aligned internally (labels, fields, lists) —
  you are centring the block, not the text inside it.
- Each model already receives `tea.WindowSizeMsg`; store width/height if a model
  doesn't already.
- Degenerate sizes: if the terminal is smaller than the content, fall back to the
  current top-left behaviour rather than clipping the top of the block (vertical
  centring with negative space must never cut off the first line).

## Out of scope

Any new visual elements (corner arc, starfield on flow screens, status line),
any change to screen content or copy, anything in `internal/deploy/`.

## Acceptance criteria

1. At 120×40 and at 80×24, every flow screen's content block is centred both
   axes (equal padding within one cell, given odd remainders).
2. At a terminal smaller than the content block, the first line of content is
   still visible at the top (no negative-offset clipping).
3. All existing unit tests pass unmodified **except** tests that literally
   assert top-left padding — update those to assert centring, and say so in
   the PR body.
4. `test/visual/` snapshots updated; the suite passes.

## Verification

`go test -race ./...` and the visual suite; also run the real binary in a
120×40 and an 80×24 terminal (`ORBIT_LAUNCHER_NO_ANIMATION=1` makes screens
stable for eyeballing) and attach before/after screenshots to the PR.
