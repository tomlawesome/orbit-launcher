package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A menu with no rows (a model built without its constructor, before a
// screen names its rows) answers nothing and never indexes past its end.
func TestMenu_WithNoRowsAnswersNothing(t *testing.T) {
	var m menu
	for _, k := range []rune{tea.KeyUp, tea.KeyDown, tea.KeyEnter} {
		var chosen string
		m, chosen = m.update(key(k))
		if chosen != "" || m.sel != 0 {
			t.Fatalf("key %v on an empty menu: chosen %q, sel %d", k, chosen, m.sel)
		}
	}
	if got := (menu{items: []string{"a"}, sel: 3}).selected(); got != "" {
		t.Fatalf("selected past the end = %q, want empty", got)
	}
}

// over keeps the selection and changes only the rows, so Enter answers
// with the row the screen drew.
func TestMenu_OverKeepsTheSelection(t *testing.T) {
	m, _ := newMenu("a", "b").update(key(tea.KeyDown))
	_, chosen := m.over([]string{"x", "y"}).update(key(tea.KeyEnter))
	if chosen != "y" {
		t.Fatalf("Enter after over = %q, want the second of the new rows", chosen)
	}
	var b strings.Builder
	m.over([]string{"x", "y"}).write(&b)
	if s := stripANSI(b.String()); !strings.Contains(s, "▸ y") {
		t.Fatalf("write did not draw the caret on the selected row:\n%s", s)
	}
}
