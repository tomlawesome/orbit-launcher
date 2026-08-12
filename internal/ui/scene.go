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
	2: style.PlanetIceText,
	3: style.PlanetRoseText,
	4: style.PlanetPaleText,
	5: style.PlanetEmberText,
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

// footerRow builds the screen's last row: an optional centred segment,
// an optional left segment and an optional right segment, all in the
// faint footer style. The centre keeps its place; left and right yield
// (disappear) rather than collide on narrow terminals.
func footerRow(width int, left, centre, right string) string {
	leftW, centreW, rightW := lipgloss.Width(left), lipgloss.Width(centre), lipgloss.Width(right)

	if centre != "" {
		if width < centreW {
			return lipgloss.PlaceHorizontal(width, lipgloss.Center, style.Tagline.Render(centre))
		}
		pad := (width - centreW) / 2
		row := strings.Repeat(" ", pad) + style.Tagline.Render(centre)
		rightGap := width - pad - centreW
		if right != "" && rightGap >= rightW+2 {
			row += strings.Repeat(" ", rightGap-rightW-1) + style.Tagline.Render(right) + " "
		}
		if left != "" && pad >= leftW+2 {
			row = " " + style.Tagline.Render(left) + strings.Repeat(" ", pad-leftW-1) + strings.TrimPrefix(row, strings.Repeat(" ", pad))
		}
		return row
	}

	var b strings.Builder
	used := 0
	if left != "" && width >= leftW+1 {
		b.WriteString(" " + style.Tagline.Render(left))
		used = leftW + 1
	}
	if right != "" && width >= used+rightW+2 {
		b.WriteString(strings.Repeat(" ", width-used-rightW-1) + style.Tagline.Render(right) + " ")
	}
	return b.String()
}
