# Design decisions — the owner-locked visual law

> **Historical record, noted 2026-10-05.** Kept as written. A later mockup
> set, `mockups-v7-notice.html` (the development notice), is the v7 in the
> lineage below, and its screen has shipped. The line that said the next
> mockup set is v7 has been removed. Sections up to 2026-10-05 are the
> historical record, kept as written; dated entries after it (such as
> 2026-10-21) are current decisions.


This is the durable record of every visual decision the repository owner has
made about the orbit-launcher TUI, with the reasoning that produced it. It
exists so no future contributor — human or model — relitigates a settled
question or reintroduces a rejected idea. Where a decision reversed an
earlier one, the reversal is recorded; the archive keeps both.

Companion artifacts: the mockup lineage (v1 `mockups.html` → v6
`mockups-v6-starchart.html`, all published at
https://tomlawesome.github.io/orbit-launcher/; then v7
`mockups-v7-notice.html`, the development notice, not published: see
Process), the terminal-truth acceptance renderer (`mockups-terminal.html`),
and the closed handover briefs (`handovers/`). Decisions were made 2026-08-11/12 against the v2–v6
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
  maintenance `#facc15` yellow, degraded `#fb923c` deep amber ("up but
  wrong" — never red, red means stopped/failed). A status word is never a
  guess: unknown health renders the FQDN with no word at all.
- Status vocabulary: **dormant / alive / maintenance / degraded / can't tell**. Fixed.
  `maintenance` is Orbit's own health answer saying it is closed to users
  (owner, 2026-10-28, #302 answer 11): up and healthy, closed on purpose,
  so it is neither alive nor degraded. Beneath it, one faint line:
  `closed to users`. No status row or drawer; those stay degraded-only.
  `can't tell` is either half of the check missing with no fault seen
  (Docker could not be read, or no health address could be worked out;
  owner, 2026-11-08, #341 answer 2: "we should say that we can't tell"):
  faint word under the FQDN, gold mark, one faint line per missing half
  (`containers couldn't be checked`, `health address couldn't be worked out`),
  no status row or drawer, caret unmoved. An HTTP failure over unreadable
  Docker is still degraded: a seen fault outranks an unknown.
- The word is decided from the containers themselves (#341): Docker's
  health of the profile's expected containers plus Orbit's own answer at
  its published port on this machine (`ORBIT_BIND_ADDRESS`/`ORBIT_PORT`),
  worst component wins. The public address is never checked or mentioned
  (owner, 2026-11-08, #341 answer 3b): external access is the user's to
  resolve.

## Layout grammar

- **Identity block sits tight** under the wordmark: FQDN directly beneath,
  status word directly beneath that. No floating gaps.
- **The identity block ends with the folder row** (#316): the absolute path
  the launcher was started in, faint, one row, in every state, never with a
  `~`. Wider than the screen less two cells it is cut from the start (`…`),
  so the last components, which tell two deployment folders apart, always
  read. When the app container contradicts the record, up to three rows
  follow: the fact in amber (`started from another folder` or `started from
  a different build`), the detail in muted text, the next step in faint
  text. A row is a fact, never a guess: nothing shows while the check runs
  or when it could not run. The next step is plain advice, never a key.
- **The other-folder refusal** (#316): Update, Repair and Remove opened
  while the app container was started from another folder are a dead end in
  the no-deployment grammar: the amber mark, a title naming that folder, a
  muted line for the flow and the shared advice, a faint line saying nothing
  was changed, and the one `Menu` row (a gone folder with the same build
  offers more; see below). Install (#318) opened while the
  project's app container was started from another folder is the same dead
  end, its title reading "This machine's Orbit was started from". It has no
  "Continue anyway", because the act it prevents is Compose's takeover of
  that folder's Orbit, not a refusal of the installer's.
- **A gone folder** (#334; owner, 2026-11-03, 13a, 14a, 15a): when nothing
  exists at the recorded folder (strict: a folder that exists is elsewhere,
  whatever it holds; a relative label or a Stat that fails otherwise is
  elsewhere too), the refusal says "That folder no longer exists." In
  Update, Repair and Remove, with this folder's record naming the running
  build, it offers `Carry on from this folder` and `Cancel` (Cancel goes
  where Esc goes, the menu). Carrying on is consent, not a write: the flow
  goes on exactly as from the container's own folder (Update's confirm,
  Repair's diagnosis, Remove's confirm and its own running check), and the
  row writes, relabels and asks Docker nothing itself; Compose restamps
  the label on the next Update. #1576 (owner answers 43a and 44c) changes
  Update and Repair: the body says the containers can't start and, on
  `Run from this folder`, are made again here with the data kept; the rows
  are `Run from this folder`, `Move it back` and `Cancel`. In Update the
  engine run is then asked, in its environment only
  (`ORBIT_INSTALL_FOLDER_MOVED=recreate`, kept for a retry), to make the
  containers again; in Repair repair.sh finds the moved folder itself.
  `Move it back` leaves the launcher, prints one stderr line naming the old
  path (escaped as on screen) and no command, and exits 0; Orbit never
  moves the folder. Remove keeps the screen and row above.
  While carried on, an answer whose folder is still
  gone with the same build is no mismatch; one whose folder is back, or
  whose build differs, refuses as above. With
  another build the screen is a dead end that says so. Install never
  offers the row: with this folder's record it points at Update, Repair or
  Remove; without one it prints `docker compose --project-name <name>
  down`. The splash's rows say the folder no longer exists, and the caret
  follows the normal rules instead of resting on Exit.
- **The other-project advisory** (#321): Install opened while an Orbit app
  container exists under another Compose project is the stale-volume
  screen's shape: the amber mark, the title "There is already an Orbit on
  this machine", a row naming where (the folder it was started from, or the
  Compose project when the label has none or the folder is this one), the
  reason, plain advice, the faint promise, and `Continue anyway` and
  `Cancel`. It is an advisory, not a dead end, because the act it predicts
  is the installer's own refusal (Compose's container-name conflict, rolled
  back by ai/orbit#1456), not a takeover. Order of the three Install
  pre-flights: the same-project clash, this, the stale volume. A check that
  cannot run, or finds two such containers, stays silent.
- **Remove's stood-down tail** (#285; owner, 2026-10-22, 19a–21a, note
  41988): with the deployment's `.orbit-files` read, the box offers Orbit's
  files by name and `rmdir` for `config`, `scripts` and the folder; the
  warning reads "this deletes all mail, documents and configuration — it
  cannot be undone", then one muted line: "The folder is removed too:
  nothing else is in it.", or "Left in place for you: a, b, c and N more —
  the folder stays while they are in it." (backups and
  orbit-install-backups are never in the command), or for the home or
  root folder "Your home folder itself is never removed." / "The root
  folder itself is never removed." A folder without the list keeps the
  whole-folder screen; a list refused whole adds "Orbit's own file list
  here could not be read." The confirm screen's full prose shows for a
  home or root folder that has the list.
- **Menu items centre individually** on the screen axis; the caret rides two
  cells left of the selected label (`menuRow`). Not a common left edge —
  that was tried and reversed by owner correction.
- **A yes/no option is a row in its list** (owner, 2026-11-11, #340): it sits
  under the choices, one blank row apart, with name, a short reason and its
  state (`on`/`off`); Enter on it flips it in place. Its consequence, while
  off, is one faint line under the table. Mock-up:
  `design/340-scanner/round-1/` (direction A).
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
- **Optional parts show by plain name** (owner, 2026-11-11, #340): `clamav`
  is `scanner`, `tika` is `text extraction`, `ollama` is `local model`.
  Every other component still shows as the engine names it. A step the
  engine skips by choice (`state=skipped action=skip`) reads
  `✓ <name> off by choice` in faint ink, not as pending.

### The install notice (v7)

Proposals 2, 3 and 4 of `design/mockups-v7-notice.html` were accepted as
shipped (owner, 2026-10-21, #195).

- **Bottom region (2):** the timer row, prompt, text box, feedback row and
  Back are pinned at the bottom; only the prose scrolls.
- **Every install (3):** the notice shows on every install, not once per
  machine.
- **Keys (4):** PgDn, PgUp, Home, End and the mouse wheel scroll the prose.

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
  of an existing set (owner instruction).
- Design iterations publish to the `gh-pages` branch (GitHub Pages), which
  carries only design artifacts; approved sets also land in `design/` here
  so code comments can reference them in-tree. *Superseded 2026-10-21
  (owner): mockups are never published on Pages; they are served only from
  the local design container — see AGENTS.md.*
- Owner review happens against cell-truth renders only.
