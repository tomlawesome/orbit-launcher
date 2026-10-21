package ui

// Specified by issue #249 (show that the pre-install notice scrolls): the
// notice draws a scroll track in the right gutter of its prose block, and
// one down arrow below the prose while more is still below. The viewport
// gives two rows to a blank and the arrow, so the prose gets viewport-2.

import (
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

const (
	scrollTrackRune = '│'
	scrollThumbRune = '┃'
	scrollArrowRune = '↓'
)

var noticeScrollSizes = []struct{ w, h int }{{80, 26}, {50, 26}}

// noticeBlock is the notice block's rows with ANSI stripped, as runes.
func noticeBlock(m InstallModel) [][]rune {
	var rows [][]rune
	for _, l := range strings.Split(ansi.Strip(m.notice.view(m.now(), m.noticeDur())), "\n") {
		rows = append(rows, []rune(l))
	}
	return rows
}

func runeAt(row []rune, col int) rune {
	if col < 0 || col >= len(row) {
		return ' '
	}
	return row[col]
}

type noticeScrollGeometry struct {
	first, proseRows, arrowRow, trackCol, arrowCol, total int
}

func noticeScrollGeom(m InstallModel) noticeScrollGeometry {
	vp := m.notice.viewport()
	measure := noticeMeasureFor(m.notice.width)
	return noticeScrollGeometry{
		first:     noticeHeaderRows,
		proseRows: vp - 2,
		arrowRow:  noticeHeaderRows + vp - 1,
		trackCol:  noticeGutter + measure,
		arrowCol:  noticeGutter + measure/2,
		total:     len(m.notice.doc),
	}
}

// thumbRows returns the prose-row indexes (0-based within the prose) that
// show the thumb, failing on any prose row with neither track nor thumb.
func thumbRows(t *testing.T, rows [][]rune, g noticeScrollGeometry) []int {
	t.Helper()
	var thumb []int
	for i := 0; i < g.proseRows; i++ {
		if g.first+i >= len(rows) {
			t.Fatalf("block has only %d rows", len(rows))
		}
		switch r := runeAt(rows[g.first+i], g.trackCol); r {
		case scrollThumbRune:
			thumb = append(thumb, i)
		case scrollTrackRune:
		default:
			t.Errorf("prose row %d: track column %d holds %q, want │ or ┃: %q", i, g.trackCol, r, string(rows[g.first+i]))
		}
	}
	return thumb
}

func countArrows(s string) int { return strings.Count(s, string(scrollArrowRune)) }

func TestNoticeScroll_TrackAndArrowAtTheTop(t *testing.T) {
	t.Parallel()
	for _, sz := range noticeScrollSizes {
		m, _, _ := openNotice(t, sz.w, sz.h, engineRunSeams{})
		g := noticeScrollGeom(m)
		rows := noticeBlock(m)
		if m.notice.layout().endOnScreen {
			t.Fatalf("%dx%d: the notice must have more below at the top", sz.w, sz.h)
		}
		thumb := thumbRows(t, rows, g)
		wantLen := max(1, int(math.Round(float64(g.proseRows*g.proseRows)/float64(g.total))))
		if len(thumb) < 1 || thumb[0] != 0 {
			t.Errorf("%dx%d: thumb rows %v, want one or more starting on the first prose row", sz.w, sz.h, thumb)
		} else if len(thumb) != wantLen {
			t.Errorf("%dx%d: thumb is %d rows, want round(%d*%d/%d) = %d", sz.w, sz.h, len(thumb), g.proseRows, g.proseRows, g.total, wantLen)
		}

		if got := countArrows(strings.Join(func() []string {
			var s []string
			for _, r := range rows {
				s = append(s, string(r))
			}
			return s
		}(), "\n")); got != 1 {
			t.Errorf("%dx%d: %d down arrows in the notice, want 1", sz.w, sz.h, got)
		}
		if got := countArrows(screen(m)); got != 1 {
			t.Errorf("%dx%d: %d down arrows on the screen, want 1", sz.w, sz.h, got)
		}
		blankRow := strings.TrimSpace(string(rows[g.arrowRow-1]))
		if blankRow != "" {
			t.Errorf("%dx%d: row above the arrow should be blank, got %q", sz.w, sz.h, blankRow)
		}
		if c := runeAt(rows[g.arrowRow], g.arrowCol); c != scrollArrowRune {
			t.Errorf("%dx%d: arrow row %d col %d holds %q, want ↓: %q", sz.w, sz.h, g.arrowRow, g.arrowCol, c, string(rows[g.arrowRow]))
		}
		if s := strings.TrimSpace(string(rows[g.arrowRow])); s != string(scrollArrowRune) {
			t.Errorf("%dx%d: arrow row holds %q, want only ↓", sz.w, sz.h, s)
		}
	}
}

func TestNoticeScroll_ThumbMovesDownWithDown(t *testing.T) {
	t.Parallel()
	for _, sz := range noticeScrollSizes {
		m, _, _ := openNotice(t, sz.w, sz.h, engineRunSeams{})
		g := noticeScrollGeom(m)
		before := thumbRows(t, noticeBlock(m), g)
		for i := 0; i < 5; i++ {
			m, _ = updateAs(t, m, key(tea.KeyDown))
		}
		rows := noticeBlock(m)
		after := thumbRows(t, rows, g)
		if len(before) == 0 || len(after) == 0 || after[0] <= before[0] {
			t.Errorf("%dx%d: thumb top %v before, %v after five Downs, want it lower", sz.w, sz.h, before, after)
		}
		if m.notice.endSeen {
			t.Fatalf("%dx%d: five Downs should not reach the end", sz.w, sz.h)
		}
		if got := countArrows(screen(m)); got != 1 {
			t.Errorf("%dx%d: %d down arrows after Down, want 1", sz.w, sz.h, got)
		}
	}
}

func TestNoticeScroll_AtTheEndTheArrowGoesAndThumbReachesTheBottom(t *testing.T) {
	t.Parallel()
	for _, sz := range noticeScrollSizes {
		m, _, _ := openNotice(t, sz.w, sz.h, engineRunSeams{})
		m, _ = updateAs(t, m, key(tea.KeyEnd))
		if !m.notice.endSeen {
			t.Fatalf("%dx%d: End should show the whole notice", sz.w, sz.h)
		}
		g := noticeScrollGeom(m)
		rows := noticeBlock(m)
		if got := countArrows(screen(m)); got != 0 {
			t.Errorf("%dx%d: %d down arrows at the end, want none", sz.w, sz.h, got)
		}
		if s := strings.TrimSpace(string(rows[g.arrowRow])); s != "" {
			t.Errorf("%dx%d: arrow row should be blank at the end, got %q", sz.w, sz.h, s)
		}
		thumb := thumbRows(t, rows, g)
		if len(thumb) == 0 || thumb[len(thumb)-1] != g.proseRows-1 {
			t.Errorf("%dx%d: thumb rows %v, want the last to be the last prose row (%d)", sz.w, sz.h, thumb, g.proseRows-1)
		}
	}
}

func TestNoticeScroll_ProseGivesTwoRowsToTheBlankAndArrow(t *testing.T) {
	t.Parallel()
	for _, sz := range noticeScrollSizes {
		m, _, _ := openNotice(t, sz.w, sz.h, engineRunSeams{})
		g := noticeScrollGeom(m)
		rows := noticeBlock(m)
		pinned := noticeHeaderRows + m.notice.viewport()
		if len(rows) != pinned+noticePinnedRows {
			t.Errorf("%dx%d: block is %d rows, want %d (header+viewport+pinned)", sz.w, sz.h, len(rows), pinned+noticePinnedRows)
		}
		// The last prose row is the one above the blank above the arrow.
		last := string(rows[g.first+g.proseRows-1])
		if strings.TrimSpace(strings.TrimRight(last, "│┃ ")) == "" {
			t.Errorf("%dx%d: last prose row %d is empty, want text: %q", sz.w, sz.h, g.proseRows-1, last)
		}
		// The text rows between header and pinned hold at most proseRows lines.
		textRows := 0
		for i := g.first; i < pinned; i++ {
			if strings.TrimSpace(strings.TrimRight(string(rows[i]), "│┃↓ ")) != "" {
				textRows++
			}
		}
		if textRows > g.proseRows {
			t.Errorf("%dx%d: %d text rows in the viewport, want at most %d", sz.w, sz.h, textRows, g.proseRows)
		}
		// Pinned region unchanged: the countdown row follows the viewport.
		if !strings.Contains(string(rows[pinned]), "0:20") {
			t.Errorf("%dx%d: row %d should hold the countdown: %q", sz.w, sz.h, pinned, string(rows[pinned]))
		}
	}
}

func TestNoticeScroll_NoWordsAboutScrolling(t *testing.T) {
	t.Parallel()
	for _, sz := range noticeScrollSizes {
		m, _, _ := openNotice(t, sz.w, sz.h, engineRunSeams{})
		for step := 0; step < 3; step++ {
			rows := noticeBlock(m)
			g := noticeScrollGeom(m)
			for i := g.first + g.proseRows; i <= g.arrowRow; i++ {
				s := strings.ToLower(string(rows[i]))
				for _, w := range []string{"more", "press", "scroll", "wheel"} {
					if strings.Contains(s, w) {
						t.Errorf("%dx%d: row %d contains %q: %q", sz.w, sz.h, i, w, s)
					}
				}
			}
			for _, r := range rows {
				s := strings.ToLower(string(r))
				for _, w := range []string{"press", "scroll", "wheel"} {
					if strings.Contains(s, w) {
						t.Errorf("%dx%d: a row contains %q: %q", sz.w, sz.h, w, s)
					}
				}
			}
			m, _ = updateAs(t, m, key(tea.KeyDown))
			m, _ = updateAs(t, m, key(tea.KeyEnd))
			if step == 0 {
				m, _ = updateAs(t, m, key(tea.KeyHome))
			}
		}
	}
}
