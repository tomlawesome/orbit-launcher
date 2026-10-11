package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// menu is a stacked menu: its rows, which row is selected, and the keys
// that move and choose (#199 UI-12). Up and Down wrap over however many
// rows there are, so a two-row confirm screen is simply a two-item menu.
// Enter answers with the selected row's label, never its position: a
// row added, removed or hidden on one screen cannot send Enter to the
// wrong act.
//
// Esc is not the menu's. What it means belongs to each screen (#210:
// one step back, which at a flow's first screen is the main menu).
//
// A screen whose rows never change keeps a menu built with its rows. A
// model whose screens show different rows under one cursor (the engine
// run, Repair) keeps the selection here and names each screen's rows
// with over, so the rows a key acts on are the rows the screen drew.
type menu struct {
	items []string
	sel   int
}

func newMenu(items ...string) menu { return menu{items: items} }

// over is the same selection on the given rows.
func (m menu) over(items []string) menu {
	m.items = items
	return m
}

// selected is the label of the selected row; "" for a menu with no rows.
func (m menu) selected() string {
	if m.sel < 0 || m.sel >= len(m.items) {
		return ""
	}
	return m.items[m.sel]
}

// update moves the selection on Up or Down, wrapping at both ends, and
// returns the selected label on Enter. Every other key, and Enter on a
// menu with no rows, returns "".
func (m menu) update(msg tea.KeyPressMsg) (menu, string) {
	n := len(m.items)
	if n == 0 {
		return m, ""
	}
	switch msg.Code {
	case tea.KeyUp:
		m.sel = (m.sel - 1 + n) % n
	case tea.KeyDown:
		m.sel = (m.sel + 1) % n
	case tea.KeyEnter:
		return m, m.selected()
	}
	return m, ""
}

// write renders the menu in the centred stacked grammar.
func (m menu) write(b *strings.Builder) { writeStackedMenu(b, m.items, m.sel) }
