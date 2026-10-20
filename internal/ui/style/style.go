// Package style is the single source of truth for orbit-launcher's
// palette and typography, mirrored in design/mockups.html. Colour hex
// values here must stay traceable 1:1 to that file's style-guide swatches.
package style

import "charm.land/lipgloss/v2"

// Separator is the one middle-dot separator between facts on a line:
// one space each side, written here once (#230).
const Separator = " · "

// Palette — the starchart identity, aligned with orbit's own web
// direction (orbit issue #307): gold is the accent, approach-blue its
// companion. See design/mockups-v6-starchart.html for the swatches
// these values are traceable to; design/mockups.html holds the
// original (pre-starchart) set.
var (
	Background = lipgloss.Color("#05070d")
	Panel      = lipgloss.Color("#0b0f1a")
	Border     = lipgloss.Color("#1c2434")
	BorderSoft = lipgloss.Color("#151b28")
	Text       = lipgloss.Color("#e7e9ee")
	TextMuted  = lipgloss.Color("#7c8699")
	TextFaint  = lipgloss.Color("#4a5468")
	Accent     = lipgloss.Color("#d8b45a")
	AccentDim  = lipgloss.Color("#5a4c28")
	Warm       = lipgloss.Color("#f0b429")
	Success    = lipgloss.Color("#4ade80")
	Error      = lipgloss.Color("#f87171")

	// Degraded is deep amber: the deployment is up, but wrong — never
	// red, which means stopped/failed (design/mockups-v5.html).
	Degraded = lipgloss.Color("#fb923c")

	// Planet palette for the splash's free planetary systems: the
	// binary pair leads gold with an approach-blue partner (starchart);
	// the pale planet and its ember moon are unchanged from v5. The
	// lead planet is the accent itself, not a copy of its hex.
	PlanetLead    = Accent
	PlanetPartner = lipgloss.Color("#8fb8ff")
	PlanetPale    = lipgloss.Color("#cbd5e1")
	PlanetEmber   = lipgloss.Color("#fb7185")
)

// Symbol glossary — see design/mockups.html section 01.
const (
	SymbolSelected = "▸"
	SymbolSuccess  = "✓"
	SymbolFailure  = "✗"
	SymbolQueued   = "·"
	SymbolMark     = "⟡"

	// SymbolWarn marks a warning-severity repair finding.
	SymbolWarn = "!"
	// SymbolCursor is the text-input cursor drawn after typed text.
	SymbolCursor = "▏"
	// SymbolMasked is one bullet of the fixed mask a secret prompt shows
	// once its answer holds anything (#252).
	SymbolMasked = "●"
)

// SpinnerFrames are the in-progress glyph's animation frames, one per
// UI tick; frame 0 is also the static "working" glyph.
var SpinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Title is the bold full-ink style of every screen title, and of the
// wordmark.
var Title = lipgloss.NewStyle().Bold(true).Foreground(Text)

// Ink is plain full-ink text: Title's colour without the bold.
var Ink = lipgloss.NewStyle().Foreground(Text)

// Tracked letter-spaces word with single spaces: the wordmark's tracking.
func Tracked(word string) string {
	tracked := ""
	for i, r := range word {
		if i > 0 {
			tracked += " "
		}
		tracked += string(r)
	}
	return tracked
}

// Wordmark renders "ORBIT": bold, wide-tracked, matching the mockup's
// splash-screen wordmark. Letter-spacing isn't a real terminal-cell
// concept, so tracking is approximated with literal spaces between
// characters, same technique the mockup's CSS letter-spacing approximates
// for a monospace grid.
func Wordmark(word string) string {
	return Title.Render(Tracked(word))
}

// MarkStyle renders the ⟡ orbit mark in the accent colour.
var MarkStyle = lipgloss.NewStyle().Foreground(Accent)

// Tagline renders small, faint, uppercase-tracked hint text (e.g. "personal
// server launcher", the footer keybinding hint).
var Tagline = lipgloss.NewStyle().Foreground(TextFaint)

// MenuSelected and MenuUnselected style a single main-menu row.
var (
	MenuSelected   = lipgloss.NewStyle().Foreground(Text)
	MenuUnselected = lipgloss.NewStyle().Foreground(TextMuted)
	MenuCaret      = lipgloss.NewStyle().Foreground(Accent)
)

// StarField styles the animated background dots — faint, never competing
// with foreground text.
var StarField = lipgloss.NewStyle().Foreground(TextFaint)

// SuccessText, ErrorText and WarmText style status glyphs and messages —
// success/error/in-progress, matching the mockup's symbol glossary.
var (
	SuccessText  = lipgloss.NewStyle().Foreground(Success)
	ErrorText    = lipgloss.NewStyle().Foreground(Error)
	WarmText     = lipgloss.NewStyle().Foreground(Warm)
	AccentText   = lipgloss.NewStyle().Foreground(Accent)
	DegradedText = lipgloss.NewStyle().Foreground(Degraded)
	MutedText    = lipgloss.NewStyle().Foreground(TextMuted)
)

// Planet styles, one per starfield planet kind, plus the faint trail
// the restored-orbit drift leaves behind (design/mockups-v6-starchart.html
// section 03).
var (
	PlanetLeadText    = lipgloss.NewStyle().Foreground(PlanetLead)
	PlanetPartnerText = lipgloss.NewStyle().Foreground(PlanetPartner)
	PlanetPaleText    = lipgloss.NewStyle().Foreground(PlanetPale)
	PlanetEmberText   = lipgloss.NewStyle().Foreground(PlanetEmber)
	PlanetTrailText   = lipgloss.NewStyle().Foreground(AccentDim)
)

// InkRamp is the fade ladder from near-background to full ink — colour
// steps are the terminal-honest way to fade (glyphs never move; only
// brightness changes). Index by alpha. The splash's arrival uses it.
var InkRamp = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#232837")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#3c4557")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7488")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1c1")),
	Title,
}

// SweepRamp lifts a wordmark cell toward gold-white as the arrival's
// sweep highlight passes it.
var SweepRamp = []lipgloss.Style{
	Title,
	lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f2e7c8")),
	lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffeec4")),
}
