package ui

// Pins the fix for audit findings SEC-S3-1 and SEC-SP2-1 (#248, design
// in note 40766): a folder name reaches the screen escaped and hard-cut.
// A CR in the name used to let the text after it replace the line, an LF
// added rows, and a long name ran off the edge. The specification says:
//
//   - every rune that is not graphic is shown as its Go escape (\r, \n,
//     \t, \x1b, \x7f, \u202e, and \xff for an invalid byte); printable
//     text, spaces and non-ASCII letters stay as they are;
//   - a long name is cut at the prose measure so the whole of it reads
//     across rows, and at most folderRows (4) rows are kept, the last
//     one ending in "…" if anything was dropped;
//   - this holds where a path is named: the profile screen, the
//     no-deployment and unreadable-deployment titles, and the Remove
//     stand-down screen (acceptance a-l).
//
// The helpers visibleText, wrapHard and folderRows are what the fix adds
// (wrap.go), so these tests fail to build until it lands.

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

var folderWidths = []int{50, 80}

const folderHeight = 26

// folderScreen is one screen that names a folder: how to draw it for a
// folder at a width, the words in front of the folder, and what follows
// it on the same sentence.
type folderScreen struct {
	name string
	lead string // the words just before the folder
	tail string // the text after the folder, with its space ("" if none)
	// glyph is a symbol the first row of the sentence starts with.
	glyph string
	// wholeScreen is true when the screen holds no other copy of the
	// folder, so the width and control-character checks may cover every
	// row; otherwise they cover the sentence's rows.
	wholeScreen bool
	draw        func(t *testing.T, folder string, width int) string
}

func folderInstallScreen() folderScreen {
	return folderScreen{
		name: "profile", lead: "Orbit installs into", wholeScreen: true,
		draw: func(t *testing.T, folder string, width int) string {
			m := NewInstallModel(folder, "v9.9.9")
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: folderHeight})
			return screen(m)
		},
	}
}

func folderOtherScreens() []folderScreen {
	return []folderScreen{
		{
			name: "no deployment", lead: "No Orbit deployment found in", wholeScreen: true,
			draw: func(t *testing.T, folder string, width int) string {
				body := []string{"Remove acts only on a deployment the launcher has found."}
				return ansi.Strip(viewNoDeployment(newSky(width, folderHeight), width, folderHeight, folder, body...))
			},
		},
		{
			name: "unreadable deployment", lead: "Couldn't read " + deploy.EnvFile + " in",
			glyph: style.SymbolFailure, wholeScreen: true,
			draw: func(t *testing.T, folder string, width int) string {
				err := errors.New("open: permission denied")
				return ansi.Strip(viewUnreadableDeployment(newSky(width, folderHeight), width, folderHeight, folder, err))
			},
		},
		{
			// The command box on this screen names the folder again,
			// and a command word wider than the screen is placed whole,
			// so only the sentence's rows are held to the screen.
			name: "remove stand-down", lead: "volumes are still on disk at", tail: " —",
			draw: func(t *testing.T, folder string, width int) string {
				m := NewRemoveModel(&deploy.Deployment{TargetDir: folder, AppURL: "https://mail.example.com"})
				m.standDown = func(context.Context, string) error { return nil }
				m.clipboard = io.Discard
				m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
				m, cmd := updateAs(t, m, key(tea.KeyEnter))
				if cmd == nil {
					t.Fatal("confirming ran nothing")
				}
				m, _ = updateAs(t, m, cmd())
				if m.state != removeStateDone {
					t.Fatalf("state = %v, want removeStateDone", m.state)
				}
				return screen(m)
			},
		},
	}
}

func folderAllScreens() []folderScreen {
	return append([]folderScreen{folderInstallScreen()}, folderOtherScreens()...)
}

// folderRowsOf is the screen's rows with the sky and the caret trimmed
// off the ends (a star is never part of a sentence).
func folderRowsOf(s string) []string {
	rows := strings.Split(s, "\n")
	for i, r := range rows {
		rows[i] = strings.Trim(r, " ▸.·✦●⟡")
	}
	return rows
}

// sentence is the rows of the folder sentence: the row that starts with
// the lead, and the consecutive non-blank rows after it, at most limit
// (0 = no limit).
func (f folderScreen) sentence(s string, limit int) []string {
	var out []string
	for _, r := range folderRowsOf(s) {
		r = strings.TrimSpace(strings.TrimPrefix(r, f.glyph))
		if len(out) == 0 {
			if strings.HasPrefix(r, f.lead) {
				out = append(out, r)
			}
			continue
		}
		if r == "" || (limit > 0 && len(out) >= limit) {
			break
		}
		out = append(out, r)
	}
	return out
}

// leadRows counts the rows that start with the lead.
func (f folderScreen) leadRows(s string) int {
	n := 0
	for _, r := range folderRowsOf(s) {
		if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(r, f.glyph)), f.lead) {
			n++
		}
	}
	return n
}

// noSpaces drops every space, so a break between rows (where a space
// goes) or inside a word does not matter to a text comparison.
func noSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// checkReadsInOrder: text appears, in order, across the consecutive rows
// of the sentence, ignoring spaces at row breaks.
func checkReadsInOrder(t *testing.T, f folderScreen, s, text string, width int) {
	t.Helper()
	rows := f.sentence(s, folderRows+1)
	if len(rows) == 0 {
		t.Errorf("%s at %d: no row starts with %q:\n%s", f.name, width, f.lead, s)
		return
	}
	if got := noSpaces(strings.Join(rows, "")); !strings.Contains(got, noSpaces(text)) {
		t.Errorf("%s at %d: %q does not read in order across the rows %q:\n%s", f.name, width, text, rows, s)
	}
}

func checkRowsFit(t *testing.T, f folderScreen, s string, width int) {
	t.Helper()
	rows := strings.Split(s, "\n")
	if !f.wholeScreen {
		rows = f.sentence(s, folderRows+1)
	}
	for _, r := range rows {
		if w := lipgloss.Width(r); w > width {
			t.Errorf("%s at %d: row %q is %d cells wide", f.name, width, r, w)
		}
	}
}

func checkNoControl(t *testing.T, f folderScreen, s string, width int) {
	t.Helper()
	text := s
	if !f.wholeScreen {
		text = strings.Join(f.sentence(s, folderRows+1), "\n")
	}
	for _, r := range text {
		if r < 0x20 && r != '\n' {
			t.Errorf("%s at %d: control rune %U on the screen:\n%s", f.name, width, r, s)
			return
		}
	}
}

// checkForged is the b/c rule: the folder holds a line break followed by
// a forged copy of the sentence's opening; the screen must show it as one
// sentence with the break written out, never as a second line.
func checkForged(t *testing.T, f folderScreen, escape, shown string) {
	t.Helper()
	forgery := f.lead + " /tmp/evil"
	folder := "/opt/orbit" + escape + forgery
	for _, w := range folderWidths {
		s := f.draw(t, folder, w)
		if n := f.leadRows(s); n != 1 {
			t.Errorf("%s at %d: %d rows start with %q, want exactly 1:\n%s", f.name, w, n, f.lead, s)
		}
		for _, r := range folderRowsOf(s) {
			if r == forgery {
				t.Errorf("%s at %d: a row is the forged %q:\n%s", f.name, w, forgery, s)
			}
		}
		checkReadsInOrder(t, f, s, f.lead+" /opt/orbit"+shown+forgery, w)
	}
}

func checkEscapeShown(t *testing.T, f folderScreen, folder, shown string) {
	t.Helper()
	for _, w := range folderWidths {
		s := f.draw(t, folder, w)
		checkReadsInOrder(t, f, s, f.lead+" "+shown, w)
		checkNoControl(t, f, s, w)
	}
}

func checkLongPath(t *testing.T, f folderScreen) {
	t.Helper()
	folder := "/srv/" + strings.Repeat("a", 120)
	for _, w := range folderWidths {
		s := f.draw(t, folder, w)
		checkRowsFit(t, f, s, w)
		checkReadsInOrder(t, f, s, f.lead+" "+folder, w)
		if rows := f.sentence(s, folderRows+1); strings.Contains(strings.Join(rows, "\n"), "…") {
			t.Errorf("%s at %d: a path that fits in %d rows was cut:\n%s", f.name, w, folderRows, s)
		}
	}
}

// a. A plain folder shows unchanged.
func TestFolderEscape_a_PlainFolderShowsUnchanged(t *testing.T) {
	f := folderInstallScreen()
	for _, folder := range []string{"/opt/orbit", "/home/sam/my orbit/ö"} {
		for _, w := range folderWidths {
			s := f.draw(t, folder, w)
			if !strings.Contains(s, "Orbit installs into "+folder) {
				t.Errorf("at %d the screen does not say %q:\n%s", w, "Orbit installs into "+folder, s)
			}
			if !strings.Contains(s, "▸ Standard") {
				t.Errorf("at %d the menu is missing:\n%s", w, s)
			}
		}
	}
}

// b. CR forging: one row starts with the lead, and the \r is visible.
func TestFolderEscape_b_CarriageReturnCannotForgeTheLine(t *testing.T) {
	checkForged(t, folderInstallScreen(), "\r", `\r`)
}

// c. The same with LF, and with 500 LFs.
func TestFolderEscape_c_LineFeedCannotAddARow(t *testing.T) {
	checkForged(t, folderInstallScreen(), "\n", `\n`)
}

func TestFolderEscape_c_ManyLineFeedsAreCutAtFolderRows(t *testing.T) {
	f := folderInstallScreen()
	for _, w := range folderWidths {
		s := f.draw(t, "/opt/"+strings.Repeat("\n", 500), w)
		rows := f.sentence(s, 0)
		if len(rows) == 0 || len(rows) > folderRows {
			t.Fatalf("at %d the sentence is %d rows, want 1 to %d:\n%s", w, len(rows), folderRows, s)
		}
		if last := rows[len(rows)-1]; !strings.HasSuffix(last, "…") {
			t.Errorf("at %d the last row %q does not end in …:\n%s", w, last, s)
		}
		if !strings.Contains(s, "▸ Standard") {
			t.Errorf("at %d the menu was pushed off:\n%s", w, s)
		}
	}
}

// d. ESC is shown as \x1b text.
func TestFolderEscape_d_EscapeIsShownAsText(t *testing.T) {
	checkEscapeShown(t, folderInstallScreen(), "/opt/\x1b[31mred\x1b[0m", `/opt/\x1b[31mred\x1b[0m`)
}

// e. A tab is shown as \t text.
func TestFolderEscape_e_TabIsShownAsText(t *testing.T) {
	checkEscapeShown(t, folderInstallScreen(), "/opt/or\tbit", `/opt/or\tbit`)
}

// f. U+202E is shown as \u202e text, with no format characters on screen.
func TestFolderEscape_f_BidiOverrideIsShownAsText(t *testing.T) {
	f := folderInstallScreen()
	checkEscapeShown(t, f, "/opt/\u202eorbit", `/opt/\u202eorbit`)
	for _, w := range folderWidths {
		for _, r := range f.draw(t, "/opt/\u202eorbit", w) {
			if unicode.Is(unicode.Cf, r) {
				t.Errorf("at %d a format character %U is on the screen", w, r)
			}
		}
	}
}

// g. An invalid byte is shown as \xff, and the screen is valid UTF-8.
func TestFolderEscape_g_InvalidByteIsShownAsText(t *testing.T) {
	f := folderInstallScreen()
	checkEscapeShown(t, f, "/opt/\xff", `/opt/\xff`)
	for _, w := range folderWidths {
		if s := f.draw(t, "/opt/\xff", w); !utf8.ValidString(s) {
			t.Errorf("at %d the screen is not valid UTF-8: %q", w, s)
		}
	}
}

// h. A 120-character path: rows within width, the whole path in order,
// no …, and the menu and tagline still there.
func TestFolderEscape_h_LongPathReadsAcrossRows(t *testing.T) {
	f := folderInstallScreen()
	checkLongPath(t, f)
	for _, w := range folderWidths {
		s := f.draw(t, "/srv/"+strings.Repeat("a", 120), w)
		for _, want := range []string{"▸ Standard", "AI", "Full", "only Standard is available so far"} {
			if !strings.Contains(s, want) {
				t.Errorf("at %d the screen lacks %q:\n%s", w, want, s)
			}
		}
	}
}

// i. A 2000-character path: at most folderRows rows, the last ending in
// …, the menu and tagline shown.
func TestFolderEscape_i_HugePathIsCutAtFolderRows(t *testing.T) {
	f := folderInstallScreen()
	for _, w := range folderWidths {
		s := f.draw(t, "/srv/"+strings.Repeat("a", 2000), w)
		for _, r := range strings.Split(s, "\n") {
			if lw := lipgloss.Width(r); lw > w {
				t.Errorf("at %d row %q is %d cells wide", w, r, lw)
			}
		}
		rows := f.sentence(s, 0)
		if len(rows) == 0 || len(rows) > folderRows {
			t.Fatalf("at %d the sentence is %d rows, want 1 to %d:\n%s", w, len(rows), folderRows, s)
		}
		if last := rows[len(rows)-1]; !strings.HasSuffix(last, "…") {
			t.Errorf("at %d the last row %q does not end in …:\n%s", w, last, s)
		}
		for _, want := range []string{"▸ Standard", "only Standard is available so far"} {
			if !strings.Contains(s, want) {
				t.Errorf("at %d the screen lacks %q:\n%s", w, want, s)
			}
		}
	}
}

func TestFolderEscape_FolderRowsIsFour(t *testing.T) {
	if folderRows != 4 {
		t.Errorf("folderRows = %d, want 4", folderRows)
	}
}

// j. visibleText.
func TestVisibleText_PrintableTextIsUnchanged(t *testing.T) {
	for _, in := range []string{"", "/opt/orbit", "/home/sam/my orbit/ö", "日本語 パス"} {
		if got := visibleText(in); got != in {
			t.Errorf("visibleText(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestVisibleText_NonGraphicRunesAreShownAsTheirGoEscape(t *testing.T) {
	if got, want := visibleText("a\r\n\x1b\u202e\xffb"), `a\r\n\x1b\u202e\xffb`; got != want {
		t.Errorf("visibleText = %q, want %q", got, want)
	}
	for in, want := range map[string]string{
		"a\tb":   `a\tb`,
		"a\x7fb": `a\x7fb`,
		"a\xffb": `a\xffb`,
	} {
		if got := visibleText(in); got != want {
			t.Errorf("visibleText(%q) = %q, want %q", in, got, want)
		}
	}
}

// k. wrapHard.
func TestWrapHard_CutsALongWordAcrossRowsWithoutLosingAnything(t *testing.T) {
	in := "/srv/" + strings.Repeat("a", 100)
	rows := wrapHard(in, 46, 4)
	if len(rows) == 0 || len(rows) > 4 {
		t.Fatalf("wrapHard gave %d rows, want 1 to 4: %q", len(rows), rows)
	}
	for _, r := range rows {
		if w := lipgloss.Width(r); w > 46 {
			t.Errorf("row %q is %d cells, want at most 46", r, w)
		}
	}
	if got := strings.Join(rows, ""); got != in {
		t.Errorf("rows joined = %q, want %q", got, in)
	}
}

func TestWrapHard_MaxRowsKeepsOneRowEndingInAnEllipsis(t *testing.T) {
	rows := wrapHard("/srv/"+strings.Repeat("a", 100), 46, 1)
	if len(rows) != 1 {
		t.Fatalf("wrapHard with maxRows 1 gave %d rows: %q", len(rows), rows)
	}
	if w := lipgloss.Width(rows[0]); w != 46 {
		t.Errorf("the row is %d cells, want 46: %q", w, rows[0])
	}
	if !strings.HasSuffix(rows[0], "…") {
		t.Errorf("the row %q does not end in …", rows[0])
	}
}

func TestWrapHard_ShowsEscapesAndKeepsAnExactFitWhole(t *testing.T) {
	if got := wrapHard("/opt/a\rb", 46, 4); len(got) != 1 || got[0] != `/opt/a\rb` {
		t.Errorf("wrapHard escaped = %q, want [%q]", got, `/opt/a\rb`)
	}
	in := "/srv/" + strings.Repeat("a", 100) // 105 cells: three rows of 46 at most
	rows := wrapHard(in, 46, 3)
	if strings.Join(rows, "") != in {
		t.Errorf("a path that fits in maxRows was cut: %q", rows)
	}
}

// l. b, d and h on the other screens that name a folder.
func TestFolderEscape_l_CarriageReturnCannotForgeTheOtherScreens(t *testing.T) {
	for _, f := range folderOtherScreens() {
		t.Run(f.name, func(t *testing.T) { checkForged(t, f, "\r", `\r`) })
	}
}

func TestFolderEscape_l_EscapeIsShownAsTextOnTheOtherScreens(t *testing.T) {
	for _, f := range folderOtherScreens() {
		t.Run(f.name, func(t *testing.T) {
			checkEscapeShown(t, f, "/opt/\x1b[31mred\x1b[0m", `/opt/\x1b[31mred\x1b[0m`)
		})
	}
}

func TestFolderEscape_l_LongPathReadsAcrossRowsOnTheOtherScreens(t *testing.T) {
	for _, f := range folderOtherScreens() {
		t.Run(f.name, func(t *testing.T) { checkLongPath(t, f) })
	}
}
