# Handover 03 — Splash orbital system

**Model: Sonnet 5 · no dependencies · read design/handovers/README.md first**
**Design source of truth: design/mockups-v2.html (open it in a browser — it is
animated; the CSS keyframes contain the exact orbital coordinates).**

## Problem

The current splash draws a drifting starfield behind the wordmark/menu. The
approved v2 design replaces it with "the orbital system":

1. **Rigid rotating starfield.** Stars never move relative to each other. The
   whole field rotates about the screen centre — one revolution ≈ 6 minutes —
   in two layers (near layer 360s/rev, far layer 520s/rev) for parallax.
   Twinkle (slow per-star brightness cycles, 5–12s) is unchanged.
2. **An opaque central sphere** (not an outline): circle of terminal cells,
   bright limb (border), interior filled with a darker shade that fully
   occludes stars behind it. A subtle terminator (darker cells toward
   bottom-right of the interior) makes it read as lit. Wordmark, tagline, and
   menu render on top of it, centred (the text layer is already centred today).
3. **Four orbiting planets** on tilted ellipses around the sphere, periods
   40s / 64s / 96s / 150s. Each planet is 1 cell (small) to 2×1 cells (large),
   coloured: ice #60a5fa, rose #e879f9, pale #cbd5e1, ember #fb7185 (filled).
   A planet on the **upper half of its ellipse is behind the sphere** — not
   drawn where the sphere's cells are — and on the lower half it draws **over**
   the sphere's cells. Text always wins over everything.

Draw order per frame: stars → far-half planets → sphere → near-half planets → text.

## Implementation notes

- Ellipse maths: the v2 HTML's `@keyframes orbit-*` blocks are the actual
  coordinate tables (24 steps/orbit, px in a 660×430 frame — scale to cells,
  and remember terminal cells are ~2:1 tall, so divide y by 2 relative to x).
- Rotation of the starfield: store stars in polar coordinates around screen
  centre; add a slowly-increasing angle per layer each tick. Round to cells at
  render time only, so stars don't jitter.
- The existing tick/animation scaffolding in `internal/ui/splash.go` (tick
  cmds, `ORBIT_LAUNCHER_NO_ANIMATION`) is the right place; keep the same tick
  rate and derive angles from elapsed ticks, not wall clock.
- `ORBIT_LAUNCHER_NO_ANIMATION=1`: render one static frame of the full system
  (sphere, planets at their t=0 positions, stars unrotated). This is what the
  PTY/teatest tests will see, so keep t=0 deterministic.
- Menu behaviour, key handling, and preselection logic are untouched.

## Out of scope

Sphere health states (handover 04), size tiers (05), arrival animation (08),
flow screens.

## Acceptance criteria

1. Stars provably rigid: any two stars keep a constant angular separation over
   time (unit-test the model, not the render).
2. A planet at the top of its orbit is not visible where the sphere is; the
   same planet at the bottom of its orbit draws over the sphere. Unit-test via
   the cell buffer.
3. Text cells are never overwritten by any planet or sphere cell.
4. `NO_ANIMATION` renders a deterministic static frame; existing splash
   go-expect/teatest tests updated to the new static frame and green.
5. Visual suite updated with the new splash snapshot.
6. No flicker at 80×24 (compact rendering may be rough — tiers come later —
   but it must not crash or corrupt cells at any size ≥ 40×12).

## Verification

`go test -race ./...`, visual suite, plus run the real binary for ~3 minutes
and confirm rotation is perceptible but calm, and one full planet occlusion
cycle looks right. Attach a terminal screen recording (asciinema or ttyd
capture) to the PR.
