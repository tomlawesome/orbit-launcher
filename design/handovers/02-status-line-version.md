> **Resolved 2026-08-12** — implemented in #74 (identity block, status vocabulary, amber degraded, state-aware preselection); the version presentation was superseded by #77's centred foot.

# Handover 02 — Status line, status vocabulary, and version corner

**Model: Haiku 4.5 · no dependencies · read design/handovers/README.md first**

## Problem

The splash knows things it doesn't say: whether a deployment exists, and (from
the health data the repair/remove paths already query) whether it's healthy.
The owner has fixed the vocabulary and wants it surfaced quietly — without any
change to the splash's existing look (starfield, ⟡ mark, wordmark, menu, and
keybind footer hint all stay exactly as shipped).

## The vocabulary (fixed, do not invent alternatives)

| State | When | Mark ⟡ colour | Status line text |
|-------|------|---------------|------------------|
| dormant | no deployment detected | current accent (unchanged) | *(none — silence is the normal state)* |
| alive | deployment detected, health passes | success green #4ade80 | `mail.example.com · alive` |
| degraded | deployment detected, ≥1 health probe failing | deep amber #fb923c | `mail.example.com · degraded` |

- Amber means *up but wrong* — never red (red = stopped/failed elsewhere).
- Any failing probe → degraded. Exactly three states, no gradients.
- degraded preselects the Repair menu item; alive preselects Update; dormant
  preselects Install (the preselection mechanism already exists).

## Scope

- Bottom-right corner, all screens: the launcher version (`v0.1.0` style),
  faint, matching the footer hint's weight. Never wraps; drop the status line
  first on narrow terminals, version second.
- Bottom-left corner, splash only: the status line per the table, faint, only
  when a deployment exists. A waiting self-update may appear instead
  (`update available · v0.2.0`) when there is no deployment status to show.
- The ⟡ mark takes the state colour. Nothing else on the splash changes.
- Health probing: reuse whatever `internal/deploy` exposes; if the honest
  available signal is coarser than per-service probes, implement the
  three states from what exists and note the simplification in the PR body.
  Do **not** build new probing machinery.

## Out of scope

Removing the keybind footer (it stays), any sphere/ring/planet visuals (all
scrapped), polling while the splash is open (state computed once at startup).

## Acceptance criteria

1. Three states unit-tested at model level (fake inputs → colour + text +
   preselection).
2. `ORBIT_LAUNCHER_NO_ANIMATION=1` static frame reflects the state.
3. Narrow-terminal drop order unit-tested.
4. Visual suite gains one snapshot per state (seeded fixtures); suite green.

## Verification

`go test -race ./...`, visual suite, plus run the real binary against no
deployment / healthy seeded deployment / seeded deployment with a stopped
container; attach all three screenshots to the PR.
