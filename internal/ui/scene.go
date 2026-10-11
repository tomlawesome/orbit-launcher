package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// This file is the shared scene renderer: the rotating sky composited
// behind a centred content block, plus the single-row footer grammar.
// The splash, the mission console and the success screen are all the
// same scene with different content — extracting it keeps the three
// pixel-identical in sky behaviour by construction.

// newSky builds the background sky for a width × height screen. Every
// screen seeds it the same, 1, so every sky is pixel-identical by
// construction.
func newSky(width, height int) starfield.Model {
	return starfield.New(width, height, 1)
}

// topOffsetFor is the optical centre: the first row of a block of lines
// set in bodyHeight rows, 0.42 of the spare rows down (a touch above
// the true middle), never negative.
func topOffsetFor(bodyHeight, lines int) int {
	return max(int(0.42*float64(bodyHeight-lines)), 0)
}

// writeHeader writes the stacked flow-screen header: the rendered mark,
// a blank line, the title (one or more lines) in style.Title, a blank
// line.
func writeHeader(b *strings.Builder, mark string, title ...string) {
	fmt.Fprintln(b, mark)
	fmt.Fprintln(b)
	for _, line := range title {
		fmt.Fprintln(b, style.Title.Render(line))
	}
	fmt.Fprintln(b)
}

// writeInlineHeader writes the one-line header of an outcome screen:
// the rendered glyph and the title in style.Title on one line, then a
// blank line.
func writeInlineHeader(b *strings.Builder, glyph, title string) {
	fmt.Fprintln(b, glyph+" "+style.Title.Render(title))
	fmt.Fprintln(b)
}

// workingLine is the one way a screen says something is running (#229):
// the spinner glyph at frame in WarmText, a space, and a lowercase
// phrase with no ellipsis; hint, when not "", is a second line in
// Tagline. The caller advances frame on the shared tick (nextFrame), so
// the glyph moves the way the console's does and never reads as a hang.
func workingLine(frame int, text, hint string) string {
	line := style.WarmText.Render(string(style.SpinnerFrames[frame%len(style.SpinnerFrames)])) + " " + style.MutedText.Render(text)
	if hint != "" {
		line += "\n" + style.Tagline.Render(hint)
	}
	return line
}

// nextFrame is the spinner frame after frame, round the ring.
func nextFrame(frame int) int { return (frame + 1) % len(style.SpinnerFrames) }

// viewNoDeployment is the one "nothing here" screen Update, Remove and
// Repair share (#229): block header, gold mark, the title naming the
// directory that was looked at (wrapped, since nobody chose its length),
// the flow's own body lines, then the dead end's one row (#228).
func viewNoDeployment(star starfield.Model, width, height int, dir string, body ...string) string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark),
		wrapWords("No Orbit deployment found in "+dir, min(width-4, proseWidth))...)
	for _, line := range body {
		fmt.Fprintln(&b, style.MutedText.Render(line))
	}
	fmt.Fprintln(&b)
	writeStackedMenu(&b, deadEndRows, 0)
	return skyBlock(star, width, height, b.String())
}

// drawBox draws the one rounded box every screen uses (the console's
// event log, the notice's typing field, the removal command), width
// cells wide with one row per entry of rows, its frame in border:
// "│ " and " │" either side of each row, the row padded to the inside
// width with spaces. Rows are measured in cells and must already fit;
// a box too narrow for any inside (under four cells) is frame only.
func drawBox(border lipgloss.Style, width int, rows []string) []string {
	if width < 2 {
		return nil
	}
	inner := width - 4
	side := border.Render("│")
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, border.Render("╭"+strings.Repeat("─", width-2)+"╮"))
	for _, row := range rows {
		if inner <= 0 {
			lines = append(lines, side+strings.Repeat(" ", width-2)+side)
			continue
		}
		gap := max(inner-lipgloss.Width(row), 0)
		lines = append(lines, side+" "+row+strings.Repeat(" ", gap+1)+side)
	}
	return append(lines, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
}

// skyCell is one background cell: a glyph plus which style class draws
// it. Class 0 is empty, 1 is a star; higher classes are planet kinds.
type skyCell struct {
	glyph rune
	class int
}

var planetClassStyles = map[int]lipgloss.Style{
	1: style.StarField,
	2: style.PlanetLeadText,
	3: style.PlanetPartnerText,
	4: style.PlanetPaleText,
	5: style.PlanetEmberText,
	6: style.PlanetTrailText,
}

// skyGrid rasterises the current sky (stars then planets, planets on
// top) into a cell grid.
func skyGrid(star starfield.Model, ready bool, width, height int) [][]skyCell {
	grid := make([][]skyCell, height)
	for y := range grid {
		grid[y] = make([]skyCell, width)
		for x := range grid[y] {
			grid[y][x] = skyCell{glyph: ' '}
		}
	}
	if !ready {
		return grid
	}
	for _, c := range star.StarCells() {
		if c.Y >= 0 && c.Y < height {
			grid[c.Y][c.X] = skyCell{glyph: c.Glyph, class: 1}
		}
	}
	for _, p := range star.Planets() {
		if p.Y >= 0 && p.Y < height {
			grid[p.Y][p.X] = skyCell{glyph: p.Glyph, class: 2 + int(p.Kind)}
		}
	}
	return grid
}

// renderSkyCells renders a run of sky cells, batching consecutive cells
// of the same class into a single Render() call.
func renderSkyCells(cells []skyCell) string {
	if len(cells) == 0 {
		return ""
	}
	var b strings.Builder
	runStart := 0
	flush := func(end int) {
		if end == runStart {
			return
		}
		chunk := make([]rune, 0, end-runStart)
		for _, c := range cells[runStart:end] {
			chunk = append(chunk, c.glyph)
		}
		if s, ok := planetClassStyles[cells[runStart].class]; ok {
			b.WriteString(s.Render(string(chunk)))
		} else {
			b.WriteString(string(chunk))
		}
		runStart = end
	}
	for i := 1; i < len(cells); i++ {
		if cells[i].class != cells[runStart].class {
			flush(i)
		}
	}
	flush(len(cells))
	return b.String()
}

// compositeScene renders bodyHeight rows: the sky everywhere, with each
// content line horizontally centred starting at row topOffset. A content
// row keeps the sky visible in its margins (that's where the planetary
// systems live), and every segment is a whole Render() call, so no ANSI
// escape sequence is ever split mid-sequence.
func compositeScene(star starfield.Model, ready bool, width, bodyHeight int, contentLines []string, topOffset int) []string {
	if topOffset < 0 {
		topOffset = 0
	}
	// A terminal can report zero rows mid-resize; every scene screen passes height-1.
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	sky := skyGrid(star, ready, width, bodyHeight)
	rows := make([]string, bodyHeight)
	for y := 0; y < bodyHeight; y++ {
		if i := y - topOffset; i >= 0 && i < len(contentLines) {
			line := contentLines[i]
			lineWidth := lipgloss.Width(line)
			start := (width - lineWidth) / 2
			if start < 0 {
				start = 0
			}
			rows[y] = renderSkyCells(sky[y][:start]) + line + renderSkyCells(sky[y][min(start+lineWidth, width):])
		} else {
			rows[y] = renderSkyCells(sky[y])
		}
	}
	return rows
}

// skyBlock is centreBlock's grammar — the same optical centre, the
// same per-line centring — rendered over the live sky instead of a bare
// terminal. The flow screens (install, remove, update, repair, in-console
// configuration) were built with centreBlock and so read as plain text on
// black while the splash, console and success screen carried the sky;
// the identity broke exactly where a person spends the most time. Every
// screen is the same scene with different content.
func skyBlock(star starfield.Model, width, height int, content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if width <= 0 || height <= 0 {
		return centreBlock(width, height, content)
	}
	return strings.Join(compositeScene(star, true, width, height, lines, topOffsetFor(height-1, len(lines))), "\n")
}

// footerRow builds the screen's last row: a single centred faint line —
// the whole footer grammar. There are no hints and no corner text
// anywhere, by decision (design/mockups-v6-starchart.html): the splash
// carries version numbers here, the success screen its achieved line,
// nothing else exists.
func footerRow(width int, text string) string {
	if text == "" {
		return ""
	}
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, style.Tagline.Render(text))
}

// footVersion is the launcher's version foot text, "orbit-launcher <v>",
// shared by the splash and the console. An empty version gives "", which
// footerRow renders as no foot at all: the UI prints the version it is
// given and never invents one (release.DisplayVersion shapes it).
func footVersion(v string) string {
	if v == "" {
		return ""
	}
	return "orbit-launcher " + v
}

// menuRow renders one stacked-menu row for the centred-menu grammar:
// each label centres on the screen axis, and the selected row's caret
// rides two cells left of its label — achieved by padding the selected
// row's right edge so per-line centring lands the label itself at
// centre (design/mockups-v6-starchart.html).
func menuRow(label string, selected bool) string {
	if selected {
		return style.MenuCaret.Render(style.SymbolSelected) + " " + style.MenuSelected.Render(label) + "  "
	}
	return style.MenuUnselected.Render(label)
}
