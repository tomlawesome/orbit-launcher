package ui

// Pins #239: a raw engine line is made safe to draw. A carriage return
// or a cursor/erase escape code inside the line must not reach the
// screen, where the terminal would obey it and overwrite the box's left
// border. A line made only of escape codes is not an entry.

import (
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

// t239Cells lays one screen row out as a terminal would after the
// styling is removed: a carriage return puts the cursor back at column
// 0, and what follows overwrites what was there.
func t239Cells(row string) []rune {
	var cells []rune
	col := 0
	for _, r := range ansi.Strip(row) {
		if r == '\r' {
			col = 0
			continue
		}
		for len(cells) <= col {
			cells = append(cells, ' ')
		}
		cells[col] = r
		col++
	}
	return cells
}

func t239Index(cells []rune, glyph rune, last bool) int {
	at := -1
	for i, r := range cells {
		if r == glyph {
			at = i
			if !last {
				return at
			}
		}
	}
	return at
}

// The engine's database wait redraws one line with carriage returns,
// and some tools clear the line and move the cursor first.
func t239DirtyConsole() ConsoleModel {
	c := testConsole()
	c = c.observeRaw("waiting (1)\rwaiting (2)\rwaiting (3)")
	c = c.observeRaw("\x1b[2K\x1b[1Gpulling")
	return c
}

func TestConsoleRaw_ViewHasNoCarriageReturnOrCursorEscape(t *testing.T) {
	view := t239DirtyConsole().view(80, 26)
	if strings.Contains(view, "\r") {
		t.Errorf("the view still holds a carriage return: %q", view)
	}
	for _, seq := range []string{"\x1b[2K", "\x1b[1G"} {
		if strings.Contains(view, seq) {
			t.Errorf("the view still holds the engine's escape %q: %q", seq, view)
		}
	}
	if left := ansi.Strip(view); strings.Contains(left, "\x1b") {
		t.Errorf("an escape byte is left in the view after styling is removed: %q", left)
	}
	// Dropping the "\r" or the escape byte alone leaves printable debris:
	// the earlier redraws, or the escape's tail ("[2K", "[1G").
	plain := ansi.Strip(view)
	for _, junk := range []string{"waiting (1)", "waiting (2)", "[2K", "[1G"} {
		if strings.Contains(plain, junk) {
			t.Errorf("the view still holds %q, debris of a redraw or an escape:\n%s", junk, plain)
		}
	}
}

func TestConsoleRaw_ViewKeepsTheWordsOfTheLine(t *testing.T) {
	view := ansi.Strip(t239DirtyConsole().view(80, 26))
	for _, want := range []string{"waiting (3)", "pulling"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view lost %q:\n%s", want, view)
		}
	}
}

// Every row of the box has its border where the top row has its
// corners, so the frame is not torn when the terminal draws it.
func TestConsoleRaw_EveryBoxRowKeepsItsBorderColumns(t *testing.T) {
	view := t239DirtyConsole().view(80, 26)
	var rows []string
	for _, row := range strings.Split(view, "\n") {
		// Keep any carriage return: the terminal obeys it.
		rows = append(rows, strings.TrimRight(row, "\n"))
	}
	top, bottom := -1, -1
	for i, row := range rows {
		plain := ansi.Strip(row)
		if top < 0 && strings.Contains(plain, "╭") {
			top = i
		}
		if strings.Contains(plain, "╰") {
			bottom = i
		}
	}
	if top < 0 || bottom <= top+1 {
		t.Fatalf("no box found in the view (top %d, bottom %d):\n%s", top, bottom, ansi.Strip(view))
	}
	topCells := t239Cells(rows[top])
	left, right := t239Index(topCells, '╭', false), t239Index(topCells, '╮', true)
	if left < 0 || right <= left {
		t.Fatalf("the top row has no corners: %q", string(topCells))
	}
	checked := 0
	for i := top + 1; i < bottom; i++ {
		cells := t239Cells(rows[i])
		gotLeft, gotRight := t239Index(cells, '│', false), t239Index(cells, '│', true)
		if gotLeft != left || gotRight != right {
			t.Errorf("box row %d has │ in columns %d and %d, the top row has its corners in %d and %d: %q",
				i-top, gotLeft, gotRight, left, right, string(cells))
		}
		checked++
	}
	if checked < 2 {
		t.Errorf("only %d box rows checked, want the box to hold both lines", checked)
	}
}

// An all-escape line has no words: it is not an entry and draws no row.
func TestConsoleRaw_ALineOfOnlyEscapeCodesIsNotAnEntry(t *testing.T) {
	for _, raw := range []string{"\x1b[2K", "\x1b[2K\x1b[1G", "\x1b[0m"} {
		base := testConsole().observeRaw("first line")
		before := len(base.entries)
		viewBefore := base.view(80, 26)
		got := base.observeRaw(raw)
		if len(got.entries) != before {
			t.Errorf("%q: entries = %d, want %d: an all-escape line is not an entry", raw, len(got.entries), before)
		}
		if view := got.view(80, 26); view != viewBefore {
			t.Errorf("%q: the view changed:\nbefore:\n%s\nafter:\n%s", raw, ansi.Strip(viewBefore), ansi.Strip(view))
		}
	}
}
