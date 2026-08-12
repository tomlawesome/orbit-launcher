# Design decisions — the owner-locked visual law

This is the durable record of every visual decision the repository owner has
made about the orbit-launcher TUI, with the reasoning that produced it. It
exists so no future contributor — human or model — relitigates a settled
question or reintroduces a rejected idea. Where a decision reversed an
earlier one, the reversal is recorded; the archive keeps both.

Companion artifacts: the mockup lineage (v1 `mockups.html` → v6
`mockups-v6-starchart.html`, all published at
https://tomlawesome.github.io/orbit-launcher/), the terminal-truth
acceptance renderer (`mockups-terminal.html`), and the closed handover
briefs (`handovers/`). Decisions were made 2026-08-11/12 against the v2–v6
iterations; the delivering PRs are #74 (v5 splash), #75 (mission console +
success), #77 (v6 starchart).

## The bar every visual change must pass

**Cell truth**: any TUI visual must read at 80×26 cells, 10 fps, using
glyph-ramp brightness and whole-cell steps only — no idea that needs blur,
sub-cell motion, or 60 fps. Prototype in the cell renderer
(`mockups-terminal.html` / `mockups-v6-starchart.html`) before proposing.
Browser-smooth mockups presented as TUI direction were explicitly called out
by the owner as misleading; never do it again. The only honest animations a
cell grid has are: things appearing, colour changing, and whole-cell
movement — everything shipped uses exactly those three.

## The wordmark

- The wordmark is the **letter-spaced, normal-size `O R B I T`**, bold ink,
  one row (`style.Wordmark`). Everywhere, always.
- The v5 half-block pixel "big text" wordmark shipped briefly and was
  **retired by owner decision** ("I hate the large font… the normal size is
  simple and more elegant"). Do not reintroduce it.
- The wordmark is **never painted in a state colour**. A green success
  wordmark was rejected outright ("hate hate hate — terminal can't do this
  well, so let's not try"). States are carried by the ⟡ mark and the small
  status word only.

## Colour: the starchart palette

Aligned with orbit's own web identity (orbit issue #307, "starchart" theme):

- **Gold `#d8b45a` is the accent**: the ⟡ mark when dormant, the ▸ caret,
  the success screen's hero URL, the binary pair's lead planet. On the
  success screen the URL is deliberately the *only* gold object.
- **Approach-blue `#8fb8ff`** is gold's companion (the pair's partner).
- **State colours outrank brand** on the mark: alive `#4ade80` green,
  degraded `#fb923c` deep amber ("up but wrong" — never red, red means
  stopped/failed). A status word is never a guess: unknown health renders
  the FQDN with no word at all.
- Status vocabulary: **dormant / alive / degraded**. Fixed.

## Layout grammar

- **Identity block sits tight** under the wordmark: FQDN directly beneath,
  status word directly beneath that. No floating gaps.
- **Menu items centre individually** on the screen axis; the caret rides two
  cells left of the selected label (`menuRow`). Not a common left edge —
  that was tried and reversed by owner correction.
- **No keybind hints, ever.** No navigation instructions anywhere; the
  screen teaches itself.
- **The foot is one centred faint line per screen**: splash carries
  `orbit-launcher vX · orbit vY` (orbit's version from
  `ORBIT_CONFIG_APPLIED_VERSION`, shown only when a deployment is
  detected); success carries `Orbit achieved in Nm NNs` (the mission
  console's real clock — never an estimate); the console carries the
  launcher version. Nothing else lives in a foot, and nothing lives in
  corners.

## The two approved motion beats

Both one-shot, both skipped by `ORBIT_LAUNCHER_NO_ANIMATION`:

- **Arrival** (once per process; any key skips and is swallowed): "Get",
  "Into" fade at the true screen centre; the third word is the wordmark
  itself, which takes a ~2s gold-white sweep, slides up row-by-row to its
  resting position, then the menu fades in one item at a time, top to
  bottom, and the foot arrives last. Returning to Menu never replays it.
- **Restored orbit** (success screen entry): the binary pair's gold lead
  eases once to a wider, calmer orbit over ~1.2s with a brief fading trail
  (`starfield.Model.Drift`) — a completed thing moves to a calmer orbit.

## The mission console

- The engine runs *inside* the TUI: framed event log, thin plain stage bar
  whose fill is the engine's own phase progression — **never a
  percentage** — a stage word, and an elapsed clock.
- The **calendar-compass tick bar was rejected** ("conceptually cool,
  visually awkward in terminal"). The plain thin line stays.
- Success/failure key off events plus exit codes, never scraped prose.

## Rejected, with reasons (do not resurrect for the TUI)

- Ring/sphere around the menu, orbiting planets with occlusion, sun
  corona/streamers, ignition animations (v2/v3): died at cell resolution;
  archived solely as reference for orbit's web UI.
- Compass-tick stage bar (v6 proposal): see above.
- Green success wordmark, big-text wordmark: see above.
- Hover-dependent ideas from orbit's web polish set (constellations,
  bidirectional highlight, chart callout) and ripple "pings": no hover in a
  terminal; rings don't survive cells.

## Process

- **Every design iteration gets a new version number** — no "rev 2/rev 3"
  of an existing set (owner instruction). The next mockup set is v7.
- Design iterations publish to the `gh-pages` branch (GitHub Pages), which
  carries only design artifacts; approved sets also land in `design/` here
  so code comments can reference them in-tree.
- Owner review happens against cell-truth renders only.
