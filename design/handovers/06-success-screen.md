# Handover 06 — Success screen: copy and layout

**Model: Sonnet 5 · depends on the mission console (frontier work) being merged
· read design/handovers/README.md first**

## Problem

When an install (or repair) completes inside the mission console, the launcher
should end on a quiet, plain success screen — no sphere, no corona, no
ignition animation (all scrapped). The shipped splash aesthetic (starfield +
text) is the template.

## Layout (fixed by the owner)

Centred stack on the standard starfield:

    ⟡                       ← mark in alive-green (#4ade80)
    O R B I T
    alive                   ← green, replaces the tagline slot

    https://mail.example.com   ← hero line, accent colour, OSC 52 copyable

    ▸ Get into Orbit        ← stacked menu, same caret grammar as splash
      Terminal              ← quits the launcher cleanly into a shell
      Menu                  ← back to the splash

Footer row: `Orbit achieved in 3m 42s` bottom-left (real elapsed time from the
mission console's clock), version bottom-right — both in the same faint style.
After a repair, the footer elapsed-time line is omitted.

## Out of scope

Any new animation beyond the existing starfield twinkle; sphere/corona visuals;
changes to the mission console itself.

## Acceptance criteria

1. "Get into Orbit" opens the deployment URL (reuse the existing
   open-dashboard mechanism if present; otherwise print-and-copy).
2. "Terminal" exits with status 0, restoring the terminal cleanly.
3. Elapsed time shown is the real duration, formatted `Nm NNs` (`NNs` under a
   minute).
4. Screen centred at 120×40 and 80×24; never clips at small sizes.
5. Unit tests for menu actions + elapsed-time formatting; visual snapshot
   added; suite green.
