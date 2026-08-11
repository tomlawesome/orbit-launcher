# Handover 02 — Status line + version footer

**Model: Haiku 4.5 · no dependencies · read design/handovers/README.md first**

## Problem

The splash currently shows a keybind hint footer (`↑↓ navigate · ↵ select · esc
quit`). The approved v2 design (design/mockups-v2.html, sections 02 and
suggestion 3) replaces it: keybind hints are dropped entirely; the bottom-right
corner shows the version (`v0.1.0` style); the bottom-left shows **one quiet
status line, only when there is something true to say**.

## Scope

- Remove the keybind hint footer from the splash.
- Bottom-right: the launcher's own version string, faint style (reuse the same
  faint/dim lipgloss style the footer used). The version is already embedded in
  the binary (`--version` works) — render the same value, without the commit hash.
- Bottom-left, same row, same faint style, exactly one of (first match wins):
  1. a detected deployment's address (e.g. `mail.example.com · installed 2026-06-14`) —
     the splash already runs deployment detection to preselect Install vs Update;
  2. a waiting self-update (e.g. `update available · v0.2.0`) — the update check
     already runs at startup behind `ORBIT_LAUNCHER_NO_UPDATE_CHECK`;
  3. nothing (empty left side is the normal case).
- The row must never wrap: if width is too small for both sides, drop the left
  side first, then the version.

## Out of scope

Changing what the update check or deployment detection do; adding new detections;
flow screens (they get their version corner in the tier task, not here).

## Acceptance criteria

1. No keybind hints anywhere on the splash.
2. Version bottom-right at 120×40 and 80×24.
3. With a seeded fake deployment (`.env-orbit` fixture — see how
   `test/visual` seeds one), the address line appears bottom-left.
4. With no deployment and no update, bottom-left is empty.
5. Narrow-terminal fallback order verified by a unit test.
6. Visual snapshots updated; whole suite green.

## Verification

`go test -race ./...`; run the binary against an empty target dir and against a
seeded fixture dir; attach both screenshots to the PR.
