# Handover 05 — Size tiers (full / compact / minimal)

**Model: Sonnet 5 · depends on handover 03 being merged · read
design/handovers/README.md first**

## Problem

The orbital system is designed for a roomy terminal. Small windows must degrade
gracefully instead of clipping the sphere (design/mockups-v2.html section 06,
suggestion 4).

## Scope

Three tiers, chosen from the current `tea.WindowSizeMsg` and re-evaluated on
every resize:

- **full** (≥ ~100×30): sphere, four planets, both star layers — as shipped by
  handover 03.
- **compact** (≥ ~60×18): smaller sphere radius, two planets (ice + ember),
  near star layer only.
- **minimal** (below compact): no sphere, no planets; centred wordmark, tagline,
  menu, and the status/version row on a sparse static field — essentially
  today's layout.

Exact cut-offs are yours to tune against real rendering; the numbers above are
starting points, not requirements. Whatever you pick, encode them as named
constants with a comment explaining the choice.

Also apply the version corner (and status line, if handover 02 is merged) to
flow screens at all tiers, so the bottom row is consistent app-wide.

## Out of scope

New visual elements; changing flow-screen content; animation timing changes.

## Acceptance criteria

1. Tier selection unit-tested (width/height → tier).
2. Resizing live across a tier boundary re-renders cleanly (no artifacts,
   no crash) — teatest with scripted `WindowSizeMsg`s.
3. At exactly 80×24 (the classic default) the result is compact tier and
   looks deliberate: nothing clipped, menu fully visible.
4. At 40×12 minimal tier still shows the full menu.
5. Visual suite gains compact and minimal snapshots.

## Verification

`go test -race ./...`, visual suite, plus real-terminal spot checks at 80×24
and a deliberately tiny window; attach screenshots at each tier to the PR.
