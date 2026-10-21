package ui

// Pins #271 (Remove screens inside the screen edge), which carries FR2-4
// and FR3-3 of #267. The design note (#271, note 40910) says:
//
//   - a. at 50, 80 and 120 columns no row of Remove's confirm, done,
//     failed, not-found or unreadable screen is wider than the screen;
//   - b, c, d. each hand-broken prose line on the done, confirm and
//     not-found screens wraps at spaces on its own at 50 columns and
//     stays one row at 80 and 120;
//   - e. the command box has one width, at most width-4;
//   - f. a word wider than the box inside is cut across rows as `'\` /
//     `'` (quoted) or a glued backslash (bare);
//   - g. the rows read back as exactly the one-line command;
//   - h. no empty fragment, and no cut inside an escaped apostrophe;
//   - i. `&&` keeps its own row and an option keeps its value;
//   - j. Copy command still writes the raw one-line command;
//   - k. FR2-4: an escape-heavy folder grows the box, not the screen;
//   - l. the done screen keeps its menu on an 80x26 terminal;
//   - m. the identity row is one row, the host cut with an ellipsis and the
//     install date whole;
//   - n. FR3-3: an escape-heavy APP_URL cannot widen the identity row;
//   - o. identityLine takes the limit, 0 meaning no cut.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

var narrowWidths = []int{50, 80, 120}

// narrowBareDir is "/srv/" and 110 letters and slashes, no spaces: a
// path the shell needs no quotes for.
var narrowBareDir = "/srv/" + strings.Repeat("abcdefghi/", 11)[:109] + "z"

// narrowItsDir is "/opt/it's a long" padded to 110 characters: it holds
// a space and an apostrophe, so it is quoted and its apostrophe is written as a closing quote, a
// backslash-quote and an opening quote.
var narrowItsDir = "/opt/it's a long" + strings.Repeat("x", 110-len("/opt/it's a long"))

// narrowManyQuotesDir has an escaped apostrophe every few characters, so
// a cut often has an escaped apostrophe close by (never touching one).
var narrowManyQuotesDir = "/opt/it's/ben's/tom's/amy's/joe's/lee's a long" + strings.Repeat("yz", 30)

func narrowDeployment(dir string) *deploy.Deployment {
	return &deploy.Deployment{TargetDir: dir, AppURL: "https://mail.example.com"}
}

// narrowDoneModel drives Remove to its stand-down screen for folder, at
// width x height, with the clipboard going to buf.
func narrowDoneModel(t *testing.T, folder string, width, height int, buf *bytes.Buffer) RemoveModel {
	t.Helper()
	m := NewRemoveModel(narrowDeployment(folder))
	m.standDown = func(context.Context, string) error { return nil }
	m.clipboard = buf
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("confirming ran nothing")
	}
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateDone {
		t.Fatalf("state = %v, want removeStateDone", m.state)
	}
	return m
}

// narrowRemoveScreens is every Remove screen the long directory touches
// at width (height 30), by name.
func narrowRemoveScreens(t *testing.T, width int) map[string]string {
	t.Helper()
	out := map[string]string{}

	out["confirm"] = screen(c9Size(t, NewRemoveModel(narrowDeployment(c9LongDir)), width))
	out["done"] = screen(narrowDoneModel(t, c9LongDir, width, 30, &bytes.Buffer{}))

	f := NewRemoveModel(narrowDeployment(c9LongDir))
	f.standDown = func(context.Context, string) error { return errors.New(c9LongErr) }
	f.clipboard = &bytes.Buffer{}
	f = c9Size(t, f, width)
	f, cmd := updateAs(t, f, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("confirming ran nothing")
	}
	f, _ = updateAs(t, f, cmd())
	if f.state != removeStateFailed {
		t.Fatalf("state = %v, want removeStateFailed", f.state)
	}
	out["failed"] = screen(f)

	nf := c9Size(t, NewRemoveModel(nil), width)
	nf.targetDir = c9LongDir
	out["not found"] = screen(nf)

	ur := c9Size(t, NewRemoveModel(nil), width)
	ur.targetDir = c9LongDir
	ur.detectErr = &fs.PathError{Op: "open", Path: c9LongDir + "/" + deploy.EnvFile, Err: syscall.EACCES}
	out["unreadable"] = screen(ur)
	return out
}

// narrowPhraseRows finds the consecutive screen rows that spell phrase
// word for word (a row is never cut mid-word, and holds nothing else).
func narrowPhraseRows(s, phrase string) ([]string, bool) {
	rows := folderRowsOf(s)
	words := strings.Fields(phrase)
	for i := range rows {
		idx, j := 0, i
		for idx < len(words) && j < len(rows) {
			f := strings.Fields(rows[j])
			if len(f) == 0 || len(f) > len(words)-idx || !slices.Equal(f, words[idx:idx+len(f)]) {
				break
			}
			idx += len(f)
			j++
		}
		if idx == len(words) {
			return rows[i:j], true
		}
	}
	return nil, false
}

// narrowCheckProse: at 50 columns phrase spans several rows of at most 46
// cells; at 80 and 120 it is one row.
func narrowCheckProse(t *testing.T, what string, screens map[int]string, phrase string, mustWrapAt50 bool) {
	t.Helper()
	for _, width := range narrowWidths {
		s := screens[width]
		rows, ok := narrowPhraseRows(s, phrase)
		if !ok {
			t.Errorf("%s at %d: %q does not read in order across whole rows:\n%s", what, width, phrase, s)
			continue
		}
		switch {
		case width == 50:
			for _, r := range rows {
				if w := lipgloss.Width(r); w > 46 {
					t.Errorf("%s at 50: row %q is %d cells, want at most 46", what, r, w)
				}
			}
			if mustWrapAt50 && len(rows) < 2 {
				t.Errorf("%s at 50: %q is still one row %q", what, phrase, rows)
			}
		default:
			if len(rows) != 1 {
				t.Errorf("%s at %d: %q is %d rows %q, want one", what, width, phrase, len(rows), rows)
			}
		}
	}
}

// narrowBoxInside is the box's text rows with the frame and the right
// padding taken off but the left indent kept: "  --env-file ..." keeps
// its two cells, a fragment that starts flush left stays flush left.
func narrowBoxInside(t *testing.T, s string) []string {
	t.Helper()
	rows := boxRowsOf(t, s)
	var out []string
	for _, r := range rows[1 : len(rows)-1] {
		if !strings.HasPrefix(r, "│ ") || !strings.HasSuffix(r, " │") {
			t.Errorf("box row %q is not │, a cell, the text, a cell, │:\n%s", r, s)
			continue
		}
		out = append(out, strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(r, "│ "), " │"), " "))
	}
	return out
}

// narrowReadBack reads rows back the way criterion g says: a row ending
// in " \" joins the next with a space; one ending in `'\` joins it
// directly and the doubled quote at the join goes; one ending in a bare `\` joins
// it directly. Leading indent is whitespace.
func narrowReadBack(rows []string) string {
	var b strings.Builder
	glueQuote := false
	for i, r := range rows {
		r = strings.TrimLeft(r, " ")
		if glueQuote {
			cur := strings.TrimSuffix(b.String(), "'")
			b.Reset()
			b.WriteString(cur)
			r = strings.TrimPrefix(r, "'")
			glueQuote = false
		}
		switch {
		case i == len(rows)-1:
			b.WriteString(r)
		case strings.HasSuffix(r, " \\"):
			b.WriteString(strings.TrimSuffix(r, " \\") + " ")
		case strings.HasSuffix(r, "'\\"):
			b.WriteString(strings.TrimSuffix(r, "\\"))
			glueQuote = true
		case strings.HasSuffix(r, "\\"):
			b.WriteString(strings.TrimSuffix(r, "\\"))
		default:
			b.WriteString(r + " <no continuation>")
		}
	}
	return b.String()
}

// narrowCheckFragments is criterion h, and the inside limit, on rows (the
// box inside, or what wrapShellCommand returned).
func narrowCheckFragments(t *testing.T, what string, rows []string, limit int) {
	t.Helper()
	for i, r := range rows {
		if limit > 0 && lipgloss.Width(r) > limit {
			t.Errorf("%s: row %d is %d cells, limit %d: %q", what, i, lipgloss.Width(r), limit, r)
		}
		if strings.HasSuffix(r, "''\\") {
			t.Errorf("%s: row %d ends in an empty fragment: %q", what, i, r)
		}
		if strings.HasPrefix(r, "''") {
			t.Errorf("%s: row %d starts with an empty fragment: %q", what, i, r)
		}
		if n := strings.Count(strings.ReplaceAll(r, `'\''`, ""), "'"); n%2 != 0 {
			t.Errorf("%s: row %d has an odd number of quotes (a cut inside '\\''?): %q", what, i, r)
		}
	}
}

// narrowCheckAndAnd is criterion i on rows. A row starts with "&&" and
// nothing else holds it; when the path is too long for "-rf <path>" to
// share that row (longPath), the row is exactly "  && sudo rm \\".
func narrowCheckAndAnd(t *testing.T, what string, rows []string, longPath bool) {
	t.Helper()
	if len(rows) == 1 {
		return // the whole command fits on one row: nothing to wrap
	}
	if longPath && !slices.Contains(rows, "  && sudo rm \\") {
		t.Errorf("%s: no row is %q (&& must lead its own row): %q", what, "  && sudo rm \\", rows)
	}
	for i, r := range rows {
		if strings.Contains(r, "&&") && !strings.HasPrefix(r, "  && sudo rm") {
			t.Errorf("%s: row %d holds && but does not start with it: %q", what, i, r)
		}
		trimmed := strings.TrimSuffix(strings.TrimSuffix(r, "\\"), " ")
		if strings.HasSuffix(trimmed, "--env-file") || strings.HasSuffix(trimmed, "--project-directory") {
			t.Errorf("%s: row %d ends on an option without its value: %q", what, i, r)
		}
	}
	if !slices.ContainsFunc(rows, func(r string) bool { return strings.HasPrefix(r, "  && sudo rm") }) {
		t.Errorf("%s: no row starts with && sudo rm: %q", what, rows)
	}
}

// narrowShellPrints is what sh prints, one word a line, for printf over
// script's words. The "&&" word is quoted so the shell prints it, not
// runs it; nothing else in script is executed.
func narrowShellPrints(script string) ([]string, error) {
	script = strings.ReplaceAll(script, "&&", "'&&'")
	out, err := exec.Command("sh", "-c", "printf '%s\\n' "+script).Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"), nil
}

// a. Nothing on a Remove screen is wider than the screen.
func TestNarrow_A_NoRowIsWiderThanTheScreen(t *testing.T) {
	for _, width := range narrowWidths {
		for name, s := range narrowRemoveScreens(t, width) {
			for _, r := range strings.Split(s, "\n") {
				if w := lipgloss.Width(r); w > width {
					t.Errorf("%s at %d: a row is %d cells: %q", name, width, w, r)
				}
			}
		}
	}
}

// narrowScreensByWidth draws one Remove screen at each width.
func narrowScreensByWidth(t *testing.T, name string) map[int]string {
	t.Helper()
	out := map[int]string{}
	for _, w := range narrowWidths {
		out[w] = narrowRemoveScreens(t, w)[name]
	}
	return out
}

// b. The done screen's prose wraps on its own at 50 columns.
func TestNarrow_B_DoneProseWrapsAt50AndStaysOneRowWider(t *testing.T) {
	screens := narrowScreensByWidth(t, "done")
	for _, phrase := range []string{
		"Containers and networks are stopped. Your files and data",
		"to fully remove Orbit from this machine, copy and run",
		"this deletes all mail, documents, and configuration —",
	} {
		narrowCheckProse(t, "done", screens, phrase, true)
	}
}

// c. The confirm screen's prose likewise.
func TestNarrow_C_ConfirmProseWrapsAt50AndStaysOneRowWider(t *testing.T) {
	screens := narrowScreensByWidth(t, "confirm")
	for _, phrase := range []string{
		"Your mail, documents, and configuration are not deleted",
		"by this step — they stay on disk, and the next screen",
		"shows exactly how to remove them if you choose to.",
	} {
		narrowCheckProse(t, "confirm", screens, phrase, true)
	}
}

// d. The not-found screen's body lines wrap on their own; Update's
// not-found screen shares the helper (its lines already fit).
func TestNarrow_D_NotFoundProseWrapsAt50AndStaysOneRowWider(t *testing.T) {
	remove := narrowScreensByWidth(t, "not found")
	narrowCheckProse(t, "Remove not found", remove, "Remove acts only on a deployment the launcher has found.", true)
	narrowCheckProse(t, "Remove not found", remove, "Start it in the directory that holds .env-orbit.", true)

	update := map[int]string{}
	for _, w := range narrowWidths {
		m := c9Size(t, NewUpdateModel(nil, c9LongDir, "v1"), w)
		if m.state != updateStateNotFound {
			t.Fatalf("update state = %v, want updateStateNotFound", m.state)
		}
		update[w] = screen(m)
	}
	narrowCheckProse(t, "Update not found", update, "There's nothing to update in this directory —", false)
	narrowCheckProse(t, "Update not found", update, "Install is the way to get into Orbit first.", false)
}

// narrowBoxDirs are the folders the box tests run over.
var narrowBoxDirs = map[string]string{
	"quoted c9 path": c9LongDir,
	"bare long path": narrowBareDir,
	"/opt/orbit":     "/opt/orbit",
}

// e. The box has one width, at most width-4, and its inside limit is
// width-8: its text is what wrapShellCommand gives for that limit.
func TestNarrow_E_BoxIsOneWidthInsideTheScreenEdge(t *testing.T) {
	for name, dir := range narrowBoxDirs {
		for _, width := range narrowWidths {
			s := screen(narrowDoneModel(t, dir, width, 40, &bytes.Buffer{}))
			rows := boxRowsOf(t, s)
			top := lipgloss.Width(rows[0])
			if top > width-4 {
				t.Errorf("%s at %d: box is %d cells, want at most %d", name, width, top, width-4)
			}
			for _, r := range rows {
				if w := lipgloss.Width(r); w != top {
					t.Errorf("%s at %d: box row is %d cells, top edge is %d: %q", name, width, w, top, r)
				}
			}
			for _, r := range strings.Split(s, "\n") {
				if w := lipgloss.Width(r); w > width {
					t.Errorf("%s at %d: a row is %d cells: %q", name, width, w, r)
				}
			}
			want := wrapShellCommand(removalWords(t, dir), width-8)
			if got := narrowBoxInside(t, s); !slices.Equal(got, want) {
				t.Errorf("%s at %d: box text %q, want wrapShellCommand at %d: %q", name, width, got, width-8, want)
			}
		}
	}
}

// f. A word wider than the inside is cut as `'\` / `'` (quoted) or as a
// backslash glued to the text (bare); the next row starts flush left.
func TestNarrow_F_LongWordIsCutWithAContinuationMarker(t *testing.T) {
	for _, width := range []int{50, 80} {
		inside := narrowBoxInside(t, screen(narrowDoneModel(t, c9LongDir, width, 30, &bytes.Buffer{})))
		found := false
		for i := 0; i+1 < len(inside); i++ {
			if strings.HasSuffix(inside[i], "'\\") && strings.HasPrefix(inside[i+1], "'") {
				found = true
			}
		}
		if !found {
			t.Errorf("quoted path at %d: no row ends in '\\ with the next starting with ': %q", width, inside)
		}

		inside = narrowBoxInside(t, screen(narrowDoneModel(t, narrowBareDir, width, 30, &bytes.Buffer{})))
		found = false
		for i := 0; i+1 < len(inside); i++ {
			r := strings.TrimSuffix(inside[i], "\\")
			if r == inside[i] || r == "" || strings.HasSuffix(r, " ") {
				continue
			}
			last, _ := lastRune(r)
			next, _ := firstRune(inside[i+1])
			if (unicode.IsLetter(last) || last == '/') && (unicode.IsLetter(next) || next == '/') {
				found = true
			}
		}
		if !found {
			t.Errorf("bare path at %d: no row ends in a backslash glued to text with text flush left below: %q", width, inside)
		}
	}
}

func lastRune(s string) (rune, bool) {
	r := []rune(s)
	if len(r) == 0 {
		return 0, false
	}
	return r[len(r)-1], true
}

func firstRune(s string) (rune, bool) {
	r := []rune(s)
	if len(r) == 0 {
		return 0, false
	}
	return r[0], true
}

// narrowWrapDirs add the paths with apostrophes.
var narrowWrapDirs = map[string]string{
	"quoted c9 path":   c9LongDir,
	"bare long path":   narrowBareDir,
	"/opt/orbit":       "/opt/orbit",
	"apostrophe path":  narrowItsDir,
	"many apostrophes": narrowManyQuotesDir,
	"short with a gap": "/home/sam/my orbit",
	"short apostrophe": "/opt/it's",
	"mid-length path":  "/srv/containers/mail/orbit-production",
}

// g. The rows read back as the one-line command, and a shell reads them
// as the same words, at inside limits 42, 72 and 112 and in the box.
func TestNarrow_G_RowsReadBackAsTheOneLineCommand(t *testing.T) {
	for name, dir := range narrowWrapDirs {
		words := removalWords(t, dir)
		want := strings.Join(words, " ")
		wantWords, err := narrowShellPrints(want)
		if err != nil {
			t.Fatalf("%s: sh over the one-line command: %v", name, err)
		}
		for _, limit := range []int{42, 72, 112} {
			rows := wrapShellCommand(words, limit)
			if got := narrowReadBack(rows); got != want {
				t.Errorf("%s at limit %d: rows read back as\n  %q\nwant\n  %q\nrows: %q", name, limit, got, want, rows)
			}
			got, err := narrowShellPrints(strings.Join(rows, "\n"))
			if err != nil || !slices.Equal(got, wantWords) {
				t.Errorf("%s at limit %d: sh reads the rows as %q (err %v), want %q", name, limit, got, err, wantWords)
			}
		}
	}
	for name, dir := range narrowBoxDirs {
		want := strings.Join(removalWords(t, dir), " ")
		for _, width := range narrowWidths {
			inside := narrowBoxInside(t, screen(narrowDoneModel(t, dir, width, 40, &bytes.Buffer{})))
			if got := narrowReadBack(inside); got != want {
				t.Errorf("%s box at %d reads back as\n  %q\nwant\n  %q", name, width, got, want)
			}
		}
	}
}

// h. No empty fragment, and no cut inside an escaped apostrophe, at every
// inside limit from the 50-column box (42) up to 120 (112).
func TestNarrow_H_NoEmptyFragmentAndNoCutInsideAnEscapedApostrophe(t *testing.T) {
	for _, name := range []string{"apostrophe path", "many apostrophes", "quoted c9 path"} {
		dir := narrowWrapDirs[name]
		words := removalWords(t, dir)
		want := strings.Join(words, " ")
		for limit := 42; limit <= 120; limit++ {
			rows := wrapShellCommand(words, limit)
			narrowCheckFragments(t, name+" at limit "+itoa(limit), rows, limit)
			if got := narrowReadBack(rows); got != want {
				t.Errorf("%s at limit %d: rows read back as %q, want %q", name, limit, got, want)
			}
		}
	}
	for _, width := range []int{50, 80} {
		inside := narrowBoxInside(t, screen(narrowDoneModel(t, narrowItsDir, width, 30, &bytes.Buffer{})))
		narrowCheckFragments(t, "apostrophe box at "+itoa(width), inside, width-8)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// narrowLongPath: "-rf <path>" cannot share the && row at any limit here.
func narrowLongPath(dir string) bool {
	return dir == c9LongDir || dir == narrowBareDir || dir == narrowItsDir
}

// i. && still leads its own row, and an option keeps its value.
func TestNarrow_I_AndAndStaysOnItsOwnRowAndOptionsKeepTheirValue(t *testing.T) {
	for name, dir := range narrowWrapDirs {
		words := removalWords(t, dir)
		for _, limit := range []int{42, 72, 112} {
			narrowCheckAndAnd(t, name+" at limit "+itoa(limit), wrapShellCommand(words, limit), narrowLongPath(dir))
		}
	}
	for name, dir := range narrowBoxDirs {
		for _, width := range narrowWidths {
			inside := narrowBoxInside(t, screen(narrowDoneModel(t, dir, width, 40, &bytes.Buffer{})))
			narrowCheckAndAnd(t, name+" box at "+itoa(width), inside, narrowLongPath(dir))
		}
	}
}

// j. Copy command writes the raw one-line command: no backslashes added.
func TestNarrow_J_CopyCommandCarriesTheRawOneLineCommand(t *testing.T) {
	var buf bytes.Buffer
	m := narrowDoneModel(t, c9LongDir, 80, 30, &buf)
	_, cmd := updateAs(t, m, key(tea.KeyEnter)) // Copy command is the default row
	if cmd == nil {
		t.Fatal("Copy command ran nothing")
	}
	cmd()

	const prefix, suffix = "\x1b]52;c;", "\x07"
	got := buf.String()
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("clipboard write = %q, want an OSC 52 sequence", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
	if err != nil {
		t.Fatalf("payload is not standard base64: %v", err)
	}
	if want := strings.Join(removalWords(t, c9LongDir), " "); string(decoded) != want {
		t.Errorf("copied %q, want the raw one-line command %q", decoded, want)
	}
}

// k. FR2-4: an escape-heavy folder grows the box, not the screen.
func TestNarrow_K_EscapeHeavyFolderGrowsTheBoxNotTheScreen(t *testing.T) {
	folder := "/srv/" + strings.Repeat("\x1b", 40)
	var shown []string
	for _, w := range removalWords(t, folder) {
		shown = append(shown, visibleText(w))
	}
	want := strings.Join(shown, " ")
	for _, width := range []int{50, 80} {
		// Height is not promised at 50 columns: the screen is tall enough
		// for the whole box, which is never capped in rows.
		s := screen(narrowDoneModel(t, folder, width, 60, &bytes.Buffer{}))
		for _, r := range strings.Split(s, "\n") {
			if w := lipgloss.Width(r); w > width {
				t.Errorf("at %d: a row is %d cells: %q", width, w, r)
			}
		}
		rows := boxRowsOf(t, s)
		top := lipgloss.Width(rows[0])
		if top > width-4 {
			t.Errorf("at %d: box is %d cells, want at most %d", width, top, width-4)
		}
		for _, r := range rows {
			if w := lipgloss.Width(r); w != top {
				t.Errorf("at %d: box row is %d cells, top edge is %d: %q", width, w, top, r)
			}
		}
		inside := narrowBoxInside(t, s)
		if !strings.Contains(strings.Join(inside, "\n"), `\x1b`) {
			t.Errorf("at %d: the box lacks the text \\x1b: %q", width, inside)
		}
		if got := narrowReadBack(inside); got != want {
			t.Errorf("at %d: box reads back as\n  %q\nwant\n  %q", width, got, want)
		}
	}
}

// l. The done screen keeps its menu on an 80x26 terminal.
func TestNarrow_L_DoneScreenKeepsItsMenuAt80x26(t *testing.T) {
	s := screen(narrowDoneModel(t, c9LongDir, 80, 26, &bytes.Buffer{}))
	lines := strings.Split(s, "\n")
	for _, label := range []string{"Copy command", "Menu", "Exit"} {
		at := -1
		for i, l := range lines {
			if screenRow(l) == label {
				at = i
			}
		}
		switch {
		case at < 0:
			t.Errorf("no %q row on the screen:\n%s", label, s)
		case at >= 26:
			t.Errorf("%q is on row %d of a 26-row terminal:\n%s", label, at+1, s)
		}
	}
	if len(lines) > 26 {
		t.Errorf("the screen is %d rows, want at most 26:\n%s", len(lines), s)
	}
}

// r. At 50 columns the done screen keeps its menu on a terminal 24, 26 or
// 30 rows tall, whatever the folder: the command box gives up rows first.
func TestNarrow_R_DoneScreenKeepsItsMenuAt50Columns(t *testing.T) {
	dirs := map[string]string{
		"quoted c9 path": c9LongDir,
		"bare long path": narrowBareDir,
		"apostrophe":     narrowItsDir,
	}
	for name, dir := range dirs {
		for _, h := range []int{24, 26, 30} {
			s := screen(narrowDoneModel(t, dir, 50, h, &bytes.Buffer{}))
			lines := strings.Split(s, "\n")
			for _, label := range []string{"Copy command", "Menu", "Exit"} {
				at := -1
				for i, l := range lines {
					if screenRow(l) == label {
						at = i
					}
				}
				switch {
				case at < 0:
					t.Errorf("%s at 50x%d: no %q row on the screen:\n%s", name, h, label, s)
				case at >= h:
					t.Errorf("%s at 50x%d: %q is on row %d:\n%s", name, h, label, at+1, s)
				}
			}
			if len(lines) > h {
				t.Errorf("%s at 50x%d: the screen is %d rows, want at most %d:\n%s", name, h, len(lines), h, s)
			}
			for _, phrase := range []string{"it cannot be undone", "this deletes all mail, documents, and"} {
				if !strings.Contains(s, phrase) {
					t.Errorf("%s at 50x%d: the screen lacks %q:\n%s", name, h, phrase, s)
				}
			}
		}
	}
}

// s. A box cut for room ends in an ellipsis row, and Copy command still
// carries the whole command: the cut is display only.
func TestNarrow_S_ACutBoxEndsInAnEllipsisRowAndCopyStillHasTheWholeCommand(t *testing.T) {
	var buf bytes.Buffer
	m := narrowDoneModel(t, c9LongDir, 50, 24, &buf)
	inside := narrowBoxInside(t, screen(m))
	if len(inside) < 2 {
		t.Fatalf("the box has %d rows, want at least 2: %q", len(inside), inside)
	}
	if last := strings.TrimSpace(inside[len(inside)-1]); last != "…" {
		t.Errorf("the box's last row is %q, want %q: %q", last, "…", inside)
	}
	for i, r := range inside {
		if w := lipgloss.Width(r); w > 42 {
			t.Errorf("box row %d is %d cells, want at most 42: %q", i, w, r)
		}
	}

	_, cmd := updateAs(t, m, key(tea.KeyEnter)) // Copy command is the default row
	if cmd == nil {
		t.Fatal("Copy command ran nothing")
	}
	cmd()
	const prefix, suffix = "\x1b]52;c;", "\x07"
	got := buf.String()
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("clipboard write = %q, want an OSC 52 sequence", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
	if err != nil {
		t.Fatalf("payload is not standard base64: %v", err)
	}
	if want := strings.Join(removalWords(t, c9LongDir), " "); string(decoded) != want {
		t.Errorf("copied %q, want the whole one-line command %q", decoded, want)
	}
}

// t. Where the whole box fits, it is not cut: the cap only bites when the
// menu would otherwise go.
func TestNarrow_T_TheBoxIsNotCutWhereItFits(t *testing.T) {
	cases := []struct {
		name          string
		dir           string
		width, height int
	}{
		{"c9 path at 80x26", c9LongDir, 80, 26},
		{"c9 path at 120x30", c9LongDir, 120, 30},
		{"bare path at 50x40", narrowBareDir, 50, 40},
	}
	for _, c := range cases {
		inside := narrowBoxInside(t, screen(narrowDoneModel(t, c.dir, c.width, c.height, &bytes.Buffer{})))
		for i, r := range inside {
			if strings.TrimSpace(r) == "…" {
				t.Errorf("%s: box row %d is an ellipsis: %q", c.name, i, inside)
			}
		}
		want := strings.Join(removalWords(t, c.dir), " ")
		if got := narrowReadBack(inside); got != want {
			t.Errorf("%s: box reads back as\n  %q\nwant\n  %q", c.name, got, want)
		}
	}
}

// q. At 80x24 the done screen keeps its menu for a bare path of 46
// characters and a quoted path of 44: every unit still fits one row at the
// 72-cell inside. (Longer paths are promised at 80x26 only, DECISIONS.md.)
func TestNarrow_Q_DoneScreenKeepsItsMenuAt80x24ForRealisticPaths(t *testing.T) {
	dirs := map[string]string{
		"bare 46":   "/srv/" + strings.Repeat("a", 41),
		"quoted 44": "/srv/mail stack/" + strings.Repeat("x", 28),
	}
	if len(dirs["bare 46"]) != 46 || len(dirs["quoted 44"]) != 44 {
		t.Fatalf("test paths are %d and %d characters", len(dirs["bare 46"]), len(dirs["quoted 44"]))
	}
	for name, dir := range dirs {
		s := screen(narrowDoneModel(t, dir, 80, 24, &bytes.Buffer{}))
		lines := strings.Split(s, "\n")
		for _, label := range []string{"Copy command", "Menu", "Exit"} {
			at := -1
			for i, l := range lines {
				if screenRow(l) == label {
					at = i
				}
			}
			switch {
			case at < 0:
				t.Errorf("%s: no %q row on the screen:\n%s", name, label, s)
			case at >= 24:
				t.Errorf("%s: %q is on row %d of a 24-row terminal:\n%s", name, label, at+1, s)
			}
		}
		if len(lines) > 24 {
			t.Errorf("%s: the screen is %d rows, want at most 24:\n%s", name, len(lines), s)
		}
	}
}

// narrowIdentityMax is the longest the identity row may be.
func narrowIdentityMax(width int) int { return min(width-4, 56) }

// narrowConfirmScreens draws both confirm screens for d at width, height
// 30, once the install date (zero = not yet known) has arrived.
func narrowConfirmScreens(t *testing.T, d *deploy.Deployment, installedAt time.Time, width int) map[string]string {
	t.Helper()
	msgs := []tea.Msg{tea.WindowSizeMsg{Width: width, Height: 30}}
	if !installedAt.IsZero() {
		msgs = append(msgs, installedAtMsg{at: installedAt})
	}
	rm := NewRemoveModel(d)
	rm.clipboard = &bytes.Buffer{}
	rm, _ = updateAs(t, rm, msgs...)
	if rm.state != removeStateConfirm {
		t.Fatalf("remove state = %v, want the confirm screen", rm.state)
	}
	um, _ := newTestUpdateModel(d, engineRunSeams{})
	um, _ = updateAs(t, um, msgs...)
	if um.state != updateStateConfirm {
		t.Fatalf("update state = %v, want the confirm screen", um.state)
	}
	return map[string]string{"Remove": screen(rm), "Update": screen(um)}
}

// narrowRowsWith are the screen's rows (sky trimmed) holding needle.
func narrowRowsWith(s, needle string) []string {
	var out []string
	for _, r := range folderRowsOf(s) {
		if strings.Contains(r, needle) {
			out = append(out, r)
		}
	}
	return out
}

// m. The identity row is one row: whole when it fits, otherwise the host
// is cut to an ellipsis and the install date stays.
func TestNarrow_M_IdentityRowIsOneRowWithTheHostCutAndTheDateKept(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	const suffix = " · installed 2026-03-04"
	host := strings.Repeat("abcdefghij", 8)
	short := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}
	long := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://" + host}
	for _, width := range narrowWidths {
		limit := narrowIdentityMax(width)
		for name, s := range narrowConfirmScreens(t, short, at, width) {
			rows := narrowRowsWith(s, "mail.example.com")
			if len(rows) != 1 || !strings.Contains(rows[0], "mail.example.com"+suffix) {
				t.Errorf("%s at %d, short host: want one row holding the host and date, got %q", name, width, rows)
			}
		}
		for name, s := range narrowConfirmScreens(t, long, at, width) {
			rows := narrowRowsWith(s, "abcdefghij")
			if len(rows) != 1 {
				t.Errorf("%s at %d, long host with date: %d identity rows %q, want 1:\n%s", name, width, len(rows), rows, s)
				continue
			}
			if w := lipgloss.Width(rows[0]); w > limit {
				t.Errorf("%s at %d, long host with date: row is %d cells, want at most %d: %q", name, width, w, limit, rows[0])
			}
			if !strings.HasSuffix(rows[0], "…"+suffix) {
				t.Errorf("%s at %d, long host with date: row %q should end in an ellipsis and the date", name, width, rows[0])
			}
		}
		for name, s := range narrowConfirmScreens(t, long, time.Time{}, width) {
			rows := narrowRowsWith(s, "abcdefghij")
			if len(rows) != 1 {
				t.Errorf("%s at %d, long host, no date: %d identity rows %q, want 1:\n%s", name, width, len(rows), rows, s)
				continue
			}
			if w := lipgloss.Width(rows[0]); w > limit {
				t.Errorf("%s at %d, long host, no date: row is %d cells, want at most %d: %q", name, width, w, limit, rows[0])
			}
			if !strings.HasSuffix(rows[0], "…") {
				t.Errorf("%s at %d, long host, no date: row %q should end in an ellipsis", name, width, rows[0])
			}
		}
	}
}

// n. FR3-3: an escape-heavy APP_URL cannot widen the identity row.
func TestNarrow_N_EscapeHeavyAppURLCannotWidenTheIdentityRow(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://" + strings.Repeat("\x1b", 60) + ".example"}
	for _, width := range []int{50, 80} {
		limit := narrowIdentityMax(width)
		for name, s := range narrowConfirmScreens(t, d, time.Time{}, width) {
			if r := identityStray(s); r != 0 {
				t.Errorf("%s at %d: %U reached the screen:\n%q", name, width, r, s)
			}
			rows := narrowRowsWith(s, `\x1b`)
			if len(rows) != 1 {
				t.Errorf("%s at %d: %d identity rows %q, want 1:\n%s", name, width, len(rows), rows, s)
			} else if w := lipgloss.Width(rows[0]); w > limit {
				t.Errorf("%s at %d: identity row is %d cells, want at most %d: %q", name, width, w, limit, rows[0])
			}
			below := map[string]string{"Remove": "Your mail, documents", "Update": "The update runs inside"}[name]
			if !strings.Contains(s, below) {
				t.Errorf("%s at %d: the %q row is gone:\n%s", name, width, below, s)
			}
		}
	}
}

// o. identityLine takes the limit: a short identity whole, a long one cut
// to the limit with the date whole, 0 meaning no cut.
func TestIdentityLine_LimitCutsTheHostAndKeepsTheDate(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	const suffix = " · installed 2026-03-04"
	host := strings.Repeat("abcdefghij", 8)
	short := &deploy.Deployment{AppURL: "https://mail.example.com"}
	long := &deploy.Deployment{AppURL: "https://" + host}

	if got, want := identityLine(short, at, 56), "mail.example.com"+suffix; got != want {
		t.Errorf("short identity with room = %q, want %q", got, want)
	}
	if got := identityLine(short, time.Time{}, 56); got != "mail.example.com" {
		t.Errorf("short identity, no date = %q, want the bare host", got)
	}
	if got := identityLine(nil, at, 56); got != "no deployment details found" {
		t.Errorf("identityLine(nil) = %q", got)
	}

	whole := host + suffix
	if got := identityLine(long, at, lipgloss.Width(whole)); got != whole {
		t.Errorf("an identity that exactly fits = %q, want it whole %q", got, whole)
	}
	for _, limit := range []int{lipgloss.Width(whole) - 1, 56, 40} {
		got := identityLine(long, at, limit)
		if w := lipgloss.Width(got); w != limit {
			t.Errorf("limit %d: got %d cells %q, want exactly the limit", limit, w, got)
		}
		if !strings.HasSuffix(got, "…"+suffix) || !strings.HasPrefix(got, "abcdefghij") {
			t.Errorf("limit %d: got %q, want the host cut to an ellipsis and the date whole", limit, got)
		}
	}
	got := identityLine(long, time.Time{}, 30)
	if w := lipgloss.Width(got); w != 30 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "abcdefghij") {
		t.Errorf("no date, limit 30: got %d cells %q, want the host cut to an ellipsis", w, got)
	}

	if got, want := identityLine(long, at, 0), whole; got != want {
		t.Errorf("limit 0 = %q, want no cut: %q", got, want)
	}
	if got := identityLine(long, time.Time{}, 0); got != host {
		t.Errorf("limit 0, no date = %q, want the whole host", got)
	}

	// The cut is on grapheme clusters: a letter and its accent stay together.
	accented := &deploy.Deployment{AppURL: "https://" + strings.Repeat("e\u0301", 40)}
	got = identityLine(accented, time.Time{}, 21)
	if w := lipgloss.Width(got); w != 21 || !strings.HasSuffix(got, "…") || strings.Count(got, "e\u0301") != 20 || strings.Count(got, "e") != 20 {
		t.Errorf("accented host, limit 21: got %d cells %q, want 20 whole letters and an ellipsis", w, got)
	}
}
