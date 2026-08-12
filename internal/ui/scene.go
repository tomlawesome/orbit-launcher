package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// This file is the shared scene renderer: the rotating sky composited
// behind a centred content block, plus the single-row footer grammar.
// The splash, the mission console and the success screen are all the
// same scene with different content — extracting it keeps the three
// pixel-identical in sky behaviour by construction.

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
