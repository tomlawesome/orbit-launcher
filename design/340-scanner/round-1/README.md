# #340 round 1: where the scanner on/off switch lives (2026-11-11)

Problem: Install needs a virus-scanner on/off choice (design note on #340).
Built from the real Install screen snapshot
(`internal/ui/testdata/c9/install_profile_80.golden`) by `gen.py`; only the
new row and the consequence line are drawn by hand. 80 columns.

Directions:

- **A, fifth row in the table:** `Scanner · recommended: scans documents for
  viruses · on|off`, one blank row under the profiles. Enter on it flips it.
- **B, separate line under the table:** a centred `Scanner: on|off` line.

Scenes, both directions: opens with the scanner on; caret on the switch,
Enter, scanner off with the consequence line under the table.

Capture: `shot.cjs` writes `shots/index.png`; looked at. Fixed: wide glyphs
(caret, stars) drew wider than one cell and shifted rows; each is now one cell.

## Owner verdicts

2026-11-11, on "Fifth row in the table?": "Yes looks great."

- A: accepted. The build ports it (design note #340, section 1).
- B: dropped.

Same day, on the progress-list word: the owner chose friendly names for all
three optional parts (`scanner`, `text extraction`, `local model`), so a
skipped scanner reads `✓ scanner off by choice`.
