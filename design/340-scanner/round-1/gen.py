#!/usr/bin/env python3
"""Mock-ups for #340 question: where the scanner on/off switch lives.

Built from the real Install screen snapshot (install_profile_80.golden) so
everything but the new row is exactly what the launcher draws today.
"""
import html, re, sys, textwrap
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
GOLDEN = ROOT / "internal/ui/testdata/c9/install_profile_80.golden"
OUT = Path(__file__).with_name("index.html")

INK, MUTED, FAINT, GOLD = "#e7e9ee", "#7c8699", "#4a5468", "#d8b45a"
LEFT, TABLE_W = 6, 67  # table's first column and width (install.go installTableW)
SGR = re.compile(r"\x1b\[([0-9;]*)m")


def parse(text):
    rows = []
    for line in text.split("\n")[:29]:
        cells, fg, bold, i = [], None, False, 0
        while i < len(line):
            m = SGR.match(line, i)
            if m:
                p = m.group(1)
                if p in ("", "0"):
                    fg, bold = None, False
                else:
                    parts = p.split(";")
                    bold = parts[0] == "1"
                    if "38" in parts:
                        k = parts.index("38")
                        r, g, b = parts[k + 2:k + 5]
                        fg = "#%02x%02x%02x" % (int(r), int(g), int(b))
                i = m.end()
                continue
            cells.append([line[i], fg, bold])
            i += 1
        rows.append(cells + [[" ", None, False]] * (80 - len(cells)))
    return rows


def blank():
    return [[" ", None, False] for _ in range(80)]


def put(row, col, text, fg, bold=False):
    for j, ch in enumerate(text):
        row[col + j] = [ch, fg, bold]


def table_row(name, adds, status, fg, caret=False):
    r = blank()
    if caret:
        put(r, LEFT, "▸", GOLD)
    put(r, LEFT + 2, name, fg)
    put(r, LEFT + 2 + 11, adds, fg)
    put(r, LEFT + 2 + 11 + 44, status, fg)
    return r


def recolor(row, fg):
    for c in row:
        if c[0] != " " and c[1] in (INK, MUTED):
            c[1] = fg


def reason_rows(text):
    out = []
    for line in textwrap.wrap(text, TABLE_W - 2):
        r = blank()
        put(r, LEFT + 2, line, FAINT)
        out.append(r)
    return out


CONSEQUENCE = ("Off: uploaded and mailed documents are not virus-scanned. To turn "
               "it on later, run scripts/configure.sh --set-scanner on, then Update.")
ADDS = "recommended: scans documents for viruses"


def scene(base, extra, caret_on_extra=False):
    rows = [list(map(list, r)) for r in base]
    std, docs = rows[15], rows[16]
    if caret_on_extra:
        for c in std:
            if c[0] == "▸":
                c[0], c[1] = " ", None
        recolor(std, MUTED)
    rows = rows[:19] + extra + rows[19:]
    # Keep the screen height: drop sky-only rows from the bottom.
    return rows[:29]


def screen_a(off, caret):
    status = "off" if off else "on"
    fg = INK if caret else MUTED
    extra = [blank(), table_row("Scanner", ADDS, status, fg, caret)]
    if off:
        extra += [blank()] + reason_rows(CONSEQUENCE)
    base = parse(GOLDEN.read_text())
    rows = [list(map(list, r)) for r in base]
    if caret:
        for c in rows[15]:
            if c[0] == "▸":
                c[0], c[1] = " ", None
        recolor(rows[15], MUTED)
    rows = rows[:19] + extra + rows[19:]
    return rows[:29]


def screen_b(off, caret):
    status = "off" if off else "on"
    label = f"Scanner: {status}"
    r = blank()
    col = (80 - len(label)) // 2
    if caret:
        put(r, col - 2, "▸", GOLD)
    put(r, col, label, INK if caret else MUTED)
    extra = [blank(), r]
    if off:
        extra += [blank()] + reason_rows(CONSEQUENCE)
    rows = [list(map(list, x)) for x in parse(GOLDEN.read_text())]
    if caret:
        for c in rows[15]:
            if c[0] == "▸":
                c[0], c[1] = " ", None
        recolor(rows[15], MUTED)
    rows = rows[:19] + extra + rows[19:]
    return rows[:29]


def to_html(rows):
    out = []
    for r in rows:
        line, cur = [], None
        for ch, fg, bold in r:
            key = (fg, bold)
            if key != cur:
                if cur is not None and cur != (None, False):
                    line.append("</span>")
                if key != (None, False):
                    st = f"color:{fg or INK}" + (";font-weight:700" if bold else "")
                    line.append(f'<span style="{st}">')
                cur = key
            line.append(html.escape(ch) if ord(ch) < 128 else f'<span style="display:inline-block;width:1ch;text-align:center">{html.escape(ch)}</span>')
        if cur not in (None, (None, False)):
            line.append("</span>")
        out.append("".join(line))
    return "\n".join(out)


SCENES = [
    ("A", "Fifth row in the table (recommended)", [
        ("Opens with the scanner on", screen_a(False, False)),
        ("Down four times, Enter: scanner off, the line underneath explains", screen_a(True, True)),
    ]),
    ("B", "Separate line under the table (the alternative)", [
        ("Opens with the scanner on", screen_b(False, False)),
        ("Caret on the line, Enter: scanner off", screen_b(True, True)),
    ]),
]

css = """
:root{color-scheme:dark}
body{background:#060b1c;color:#e7e9ee;margin:0;padding:32px 16px;
 font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
h1{font-size:15px;letter-spacing:.2em;text-transform:uppercase;color:#d8b45a;text-align:center;font-weight:600}
.sub{font-size:12px;color:#7c8699;max-width:78ch;margin:0 auto 32px;text-align:center;line-height:1.7}
section{max-width:1400px;margin:0 auto 40px}
h2{font-size:12px;letter-spacing:.14em;text-transform:uppercase;color:#7c8699;margin:0 0 12px}
.grid{display:flex;flex-wrap:wrap;gap:20px}
figure{margin:0;overflow-x:auto;max-width:100%}
.term{background:#05070d;border:1px solid #14203e;border-radius:8px;padding:10px 8px;width:fit-content}
pre{margin:0;font-size:12px;line-height:15px;font-family:"DejaVu Sans Mono",ui-monospace,Menlo,Consolas,monospace}
figcaption{font-size:11px;color:#7c8699;margin-top:6px;max-width:72ch;line-height:1.6}
"""

parts = [f"<!doctype html><html lang=en><head><meta charset=utf-8>"
         f"<meta name=viewport content='width=device-width,initial-scale=1'>"
         f"<title>Scanner switch placement</title><style>{css}</style></head><body>",
         "<h1>Install · scanner on/off</h1>",
         "<p class=sub>#340 round 1 · 80-column terminal · everything except the new row is "
         "the launcher's current Install screen. Up/Down moves, Enter on the scanner flips it "
         "and stays on this screen; Enter on a profile installs.</p>"]
for letter, name, shots in SCENES:
    parts.append(f"<section aria-label='{html.escape(name)}'><h2>{letter} · {html.escape(name)}</h2><div class=grid>")
    for cap, rows in shots:
        parts.append(f"<figure><div class=term><pre aria-label='{html.escape(cap)}'>{to_html(rows)}</pre></div>"
                     f"<figcaption>{html.escape(cap)}</figcaption></figure>")
    parts.append("</div></section>")
parts.append("</body></html>")
OUT.write_text("\n".join(parts))
print(OUT)
