# Handover 04 — Sphere health states

**Model: Haiku 4.5 · depends on handover 03 being merged · read
design/handovers/README.md first**
**Design source: design/mockups-v3-flows.html section 04 (the trio).**

## Problem

The splash sphere from handover 03 should carry deployment state at a glance:

| State | When | Look | Status word under wordmark |
|-------|------|------|---------------------------|
| dormant | no deployment detected | muted limb (faint gray), no glow, darker interior | `not installed` (faint) |
| lit | deployment detected, all health probes pass | bright limb, green cast | `running` (green #4ade80) |
| errored | deployment detected, ≥1 health probe failing | deep amber limb #fb923c, amber cast | `running · unhealthy` (amber) |

Threshold rule: **any** failing probe → errored. Exactly three states; no
gradients or partial shades.

## Scope

- Wire the existing deployment detection (already run by the splash to
  preselect Install/Update) plus a health probe result into a three-valued
  state, and colour the sphere limb/interior/status-word accordingly.
- Health probing: reuse whatever `internal/deploy` already exposes for
  service health (the repair/remove paths query compose state). If the
  cheapest honest signal available is only "deployment exists" vs "responds
  on its health endpoint", implement dormant/lit/errored from that and note
  the simplification in the PR body. Do **not** build new probing machinery.
- Errored state preselects the `Repair` menu item (same mechanism that
  preselects Install/Update today).
- The status word replaces the tagline line only when a deployment exists;
  with none, tagline stays as-is and the sphere is dormant.

## Out of scope

New health checks, polling/refresh while the splash is open (state is computed
once at startup), flow screens, the ignition animation.

## Acceptance criteria

1. Three states unit-tested at the model level (fake detection/health inputs →
   expected palette + preselection).
2. Errored preselects Repair; lit preselects Update; dormant preselects Install.
3. `NO_ANIMATION` static frame reflects the state (so PTY tests can assert it).
4. Visual suite gains one snapshot per state (seeded fixtures).

## Verification

`go test -race ./...`, visual suite, plus run the real binary against: no
deployment, a healthy seeded deployment, and a seeded deployment with one
stopped container. Attach all three screenshots to the PR.
