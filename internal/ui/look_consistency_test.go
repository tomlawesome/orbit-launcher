package ui

// Pins #229 (working lines, outcome marks and one no-deployment screen;
// #200 C5-C7) from the issue and the owner's decision of 2026-10-11
// (10a), not from the code:
//
//   - C5. A working line is the spinner glyph in WarmText, advancing one
//     frame on each UI tick, a space, and a lowercase phrase with no
//     ellipsis. Remove and Repair draw a static glyph or none, and
//     three of the phrases end in an ellipsis. (The terminal handoff
//     line is exempt: the child process owns the terminal.)
//   - C6. Every non-failure outcome title wears the mark in its state
//     colour, never a check; failures wear the cross. The configuration
//     title carries the same amber mark on both screens that show it.
//   - C7. "Nothing here" is one screen: block header, gold mark, the
//     title "No Orbit deployment found in <dir>", one body line that is
//     the flow's own, and the dead end's one row (#228: Menu). Repair
//     draws it when the engine exits 5 instead of a diagnosis.
//
// Screens are read the way a person reads them: lines with the sky
// taken off their ends, grouped into blocks at the blank lines. The
// colours are the style package's own, so no RGB value is repeated here.

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// --- Reading a screen -------------------------------------------------------

// skyCells are the starfield's glyphs, the caret and the padding: what
// can sit at the ends of a text line without being part of it.
const skyCells = " ▸.·✦●"

func lookTrim(line string) string { return strings.Trim(line, skyCells) }

// lookBlocks groups the lines of a stripped screen that have content
// into runs separated by blank (or sky-only) lines.
func lookBlocks(s string) [][]string {
	var blocks [][]string
	var cur []string
	for _, line := range strings.Split(s, "\n") {
		if t := lookTrim(line); t != "" {
			cur = append(cur, t)
			continue
		}
		if cur != nil {
			blocks = append(blocks, cur)
			cur = nil
		}
	}
	if cur != nil {
		blocks = append(blocks, cur)
	}
	return blocks
}

// firstContentLine is the index of the first line of a stripped screen
// that is not sky, or -1.
func firstContentLine(s string) int {
	for i, line := range strings.Split(s, "\n") {
		if lookTrim(line) != "" {
			return i
		}
	}
	return -1
}

// lineAt is line i of a screen, "" when it has none.
func lineAt(s string, i int) string {
	lines := strings.Split(s, "\n")
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}

// --- C7: one no-deployment screen -------------------------------------------

const noDeploymentSentence = "Install is the way to get into Orbit first"

// noDeployment is a no-deployment screen taken apart.
type noDeployment struct {
	shape      string // "block": the mark on its own line; "inline": with the title
	title      string
	body       string // every block between the header and the rows, as running text
	bodyBlocks int
	rows       []string
}

func parseNoDeployment(plain string) noDeployment {
	blocks := lookBlocks(plain)
	var d noDeployment
	d.rows = screenLabels(plain)
	if len(blocks) == 0 {
		return d
	}
	rest := blocks[1:]
	switch {
	case slices.Equal(blocks[0], []string{style.SymbolMark}):
		d.shape = "block"
		if len(rest) > 0 {
			d.title = oneLine(strings.Join(rest[0], " "))
			rest = rest[1:]
		}
	case strings.HasPrefix(blocks[0][0], style.SymbolMark+" "):
		d.shape = "inline"
		d.title = oneLine(strings.TrimPrefix(strings.Join(blocks[0], " "), style.SymbolMark+" "))
	}
	if len(rest) > 0 {
		rest = rest[:len(rest)-1] // the last block holds the rows
	}
	d.bodyBlocks = len(rest)
	var lines []string
	for _, b := range rest {
		lines = append(lines, b...)
	}
	d.body = oneLine(strings.Join(lines, " "))
	return d
}

// lookView is one flow's screen as a person sees it, and the model.
type lookView struct {
	name  string
	model tea.Model
}

func (v lookView) plain() string { return screen(v.model) }
func (v lookView) raw() string   { return v.model.View().Content }

// repairNoDeployment is Repair after the engine exited 5, with whatever
// evidence it printed first.
func repairNoDeployment(t *testing.T, dir string, lines ...string) RepairModel {
	t.Helper()
	m, _ := updateAs(t, NewRepairModel(dir, "v0.6.0"), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = repairLines(t, m, lines...)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 5})
	return m
}

// notOrbitEvidence is what the engine prints before exit 5.
var notOrbitEvidence = []string{
	"finding class=not-orbit-directory target=directory severity=fail",
	"diagnosis result=failed checked=1 skipped=12",
}

// noDeploymentViews are the three flows looking at the same empty
// directory: Update and Remove through the app, which hands them the
// directory, and Repair on exit 5.
func noDeploymentViews(t *testing.T, dir string) []lookView {
	t.Helper()
	app := NewAppModelNoAnimation().WithoutVolumeCheck()
	app.targetDir = dir // no .env-orbit here
	return []lookView{
		{"Update", openUpdate(t, app, nil)},
		{"Remove", openRemove(t, app)},
		{"Repair", repairNoDeployment(t, dir, notOrbitEvidence...)},
	}
}

// C7: each flow draws it the one way: mark alone on its line (block
// header), gold, the title naming the directory, one body, one row.
func TestLookNoDeployment_EachFlowDrawsTheOneScreen(t *testing.T) {
	dir := shortTempDir(t)
	gold := style.AccentText.Render(style.SymbolMark)
	for _, v := range noDeploymentViews(t, dir) {
		t.Run(v.name, func(t *testing.T) {
			d := parseNoDeployment(v.plain())
			if d.shape != "block" {
				t.Errorf("header shape = %q, want the block header (mark on its own line)", d.shape)
			}
			if !strings.Contains(v.raw(), gold) {
				t.Errorf("the mark is not gold (style.AccentText)")
			}
			if want := "No Orbit deployment found in " + dir; d.title != want {
				t.Errorf("title = %q, want %q", d.title, want)
			}
			if d.bodyBlocks != 1 || d.body == "" {
				t.Errorf("want one body block under the title, got %d (%q)", d.bodyBlocks, d.body)
			}
			if !slices.Equal(d.rows, []string{"Menu"}) {
				t.Errorf("rows = %q, want the dead end's one row, Menu", d.rows)
			}
			if strings.Contains(v.plain(), style.SymbolFailure) {
				t.Errorf("a missing deployment is not a failure; the screen shows %s:\n%s", style.SymbolFailure, v.plain())
			}
		})
	}
}

// C7: the three views share the title and the header, and differ only
// in the body line; Update keeps its pointer to Install.
func TestLookNoDeployment_ThreeViewsDifferOnlyInTheBody(t *testing.T) {
	views := noDeploymentViews(t, shortTempDir(t))
	parsed := make([]noDeployment, len(views))
	for i, v := range views {
		parsed[i] = parseNoDeployment(v.plain())
	}
	for i := 1; i < len(views); i++ {
		a, b := parsed[0], parsed[i]
		if a.shape != b.shape || a.title != b.title || !slices.Equal(a.rows, b.rows) {
			t.Errorf("%s and %s differ beyond the body:\n %s: shape %q title %q rows %q\n %s: shape %q title %q rows %q",
				views[0].name, views[i].name,
				views[0].name, a.shape, a.title, a.rows,
				views[i].name, b.shape, b.title, b.rows)
		}
	}
	for i := range views {
		for j := i + 1; j < len(views); j++ {
			if parsed[i].body == parsed[j].body {
				t.Errorf("%s and %s share a body (%q); each flow says its own", views[i].name, views[j].name, parsed[i].body)
			}
		}
	}
	if !strings.Contains(parsed[0].body, noDeploymentSentence) {
		t.Errorf("Update's body lost %q: %q", noDeploymentSentence, parsed[0].body)
	}
}

// C7: Repair draws it on exit 5 whatever the engine printed first, and
// not as a diagnosis: no diagnosis title, no finding, no tally.
func TestLookNoDeployment_RepairDrawsItOnExitFive(t *testing.T) {
	dir := shortTempDir(t)
	for _, c := range []struct {
		name  string
		lines []string
	}{
		{"after a finding and a verdict", notOrbitEvidence},
		{"with nothing printed", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := screen(repairNoDeployment(t, dir, c.lines...))
			d := parseNoDeployment(s)
			if want := "No Orbit deployment found in " + dir; d.title != want {
				t.Errorf("title = %q, want %q", d.title, want)
			}
			for _, banned := range []string{"No Orbit installation here", "Diagnosis couldn't run", "checked", "skipped", "no recognizable"} {
				if strings.Contains(s, banned) {
					t.Errorf("the screen is still a diagnosis result (shows %q):\n%s", banned, s)
				}
			}
		})
	}
}

// C7 with #228: Repair's no-deployment screen is a dead end like the
// others: Enter and Esc go to the menu, nothing else does anything.
func TestLookNoDeployment_RepairIsADeadEndWithOneBackAct(t *testing.T) {
	dir := shortTempDir(t)
	for _, k := range []tea.KeyPressMsg{key(tea.KeyEnter), key(tea.KeyEsc)} {
		t.Run(k.String(), func(t *testing.T) {
			m, cmd := updateAs(t, repairNoDeployment(t, dir, notOrbitEvidence...), k)
			if isQuit(cmd) || !m.Done || !m.WantsMenu {
				t.Errorf("Done=%v WantsMenu=%v quit=%v, want a way back to the menu", m.Done, m.WantsMenu, isQuit(cmd))
			}
		})
	}
	for _, k := range []tea.KeyPressMsg{runeKey('x'), runeKey('q'), runeKey('5'), key(tea.KeySpace), key(tea.KeyLeft)} {
		t.Run("other "+k.String(), func(t *testing.T) {
			before := repairNoDeployment(t, dir, notOrbitEvidence...)
			m, cmd := updateAs(t, before, k)
			if cmd != nil || m.Done || screen(m) != screen(before) {
				t.Errorf("%q did something on the dead end (cmd %v, Done %v)", k.String(), cmd != nil, m.Done)
			}
		})
	}
}

// --- C6: one outcome mark ---------------------------------------------------

// outcomeTitle is a screen whose first line is an outcome title: the
// glyph it wears and the coloured glyph a person sees.
type outcomeTitle struct {
	name     string
	build    func(t *testing.T) tea.Model
	glyph    string
	coloured string
}

func repairExecutionOutcome(result string) func(t *testing.T) tea.Model {
	return func(t *testing.T) tea.Model {
		t.Helper()
		m := sizedRepair(t)
		m.state = repairExecuting
		m = repairLines(t, m, "execution result="+result+" done=0 failed=0")
		m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
		return m
	}
}

func repairDiagnosisClear(t *testing.T) tea.Model {
	t.Helper()
	m := repairLines(t, sizedRepair(t), "diagnosis result=healthy checked=15 skipped=0")
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
	return m
}

func outcomeTitles() []outcomeTitle {
	ok := style.SuccessText.Render(style.SymbolMark)
	amber := style.DegradedText.Render(style.SymbolMark)
	cross := style.ErrorText.Render(style.SymbolFailure)
	return []outcomeTitle{
		// Remove's done title was the one outcome wearing a check.
		{"Remove removed", removedScreen, style.SymbolMark, ok},
		{"Install success", installSuccessScreen, style.SymbolMark, ok},
		{"Repair applied", repairExecutionOutcome("complete"), style.SymbolMark, ok},
		{"Repair nothing to repair", repairExecutionOutcome("empty"), style.SymbolMark, ok},
		{"Repair nothing was changed", repairExecutionOutcome("declined"), style.SymbolMark, amber},
		{"Repair nothing safe to run", repairExecutionOutcome("unactionable"), style.SymbolMark, amber},
		{"Repair run ended", repairExecutionOutcome("interrupted"), style.SymbolMark, amber},
		{"Repair diagnosis clear", repairDiagnosisClear, style.SymbolMark, ok},
		// Failures keep the cross.
		{"Remove failed", removeFailedScreen, style.SymbolFailure, cross},
		{"Repair some repairs failed", repairExecutionOutcome("failed"), style.SymbolFailure, cross},
	}
}

// C6: every non-failure outcome title wears the mark in its state
// colour and never a check; every failure wears the cross.
func TestLookOutcomeMark_EveryTitleWearsTheMarkOrTheCrossNeverACheck(t *testing.T) {
	for _, c := range outcomeTitles() {
		t.Run(c.name, func(t *testing.T) {
			m := c.build(t)
			plain, raw := screen(m), m.View().Content
			i := firstContentLine(plain)
			if i < 0 {
				t.Fatalf("the screen is empty:\n%s", plain)
			}
			line, rawLine := lineAt(plain, i), lineAt(raw, i)
			for _, g := range []string{style.SymbolMark, style.SymbolFailure, style.SymbolSuccess} {
				if has := strings.Contains(line, g); has != (g == c.glyph) {
					t.Errorf("first line %q: shows %q = %v, want %v", strings.TrimSpace(line), g, has, g == c.glyph)
				}
			}
			if !strings.Contains(rawLine, c.coloured) {
				t.Errorf("the glyph on the first line is not in its state colour: %q", rawLine)
			}
		})
	}
}

// C6, the named case: Remove's done view opens on the mark in the
// success colour, not a check.
func TestLookOutcomeMark_RemoveDoneOpensOnTheSuccessMark(t *testing.T) {
	m := removedScreen(t)
	plain := screen(m)
	line := lineAt(plain, firstContentLine(plain))
	if !strings.Contains(line, style.SymbolMark) || strings.Contains(line, style.SymbolSuccess) {
		t.Errorf("first line = %q, want the mark %s and no %s", strings.TrimSpace(line), style.SymbolMark, style.SymbolSuccess)
	}
	if !strings.Contains(line, "stood down") {
		t.Errorf("first line = %q, want the title", strings.TrimSpace(line))
	}
	if !strings.Contains(m.View().Content, style.SuccessText.Render(style.SymbolMark)) {
		t.Error("the mark is not in the success colour")
	}
}

// C6: one colour. The engine's configuration stop no longer has a screen
// of its own (#251): the guided session is the first thing a person sees,
// and its field screens keep the amber mark, because they are still inside
// the stop. The removed screen's own words are never shown.
func TestLookOutcomeMark_ConfigurationSessionKeepsTheAmberMark(t *testing.T) {
	const oldScreen = "The engine stopped before touching anything"
	amber := style.DegradedText.Render(style.SymbolMark)

	t.Run("straight in from the stop", func(t *testing.T) {
		m, s := newTestInstallModel(engineRunSeams{
			prepareEngine: fakeEngine(nil, configRefusalStream()...),
			prepareConfig: planned(true, false),
		})
		m, cmd := startInstallRun(t, m)
		m = driveWatching(t, m, cmd, []string{oldScreen}, s)
		if m.run.state != runConfigSignInMode {
			t.Fatalf("run state = %v, want the guided session's first screen", m.run.state)
		}
	})
	t.Run("the collection session", func(t *testing.T) {
		r := collectingRun(t, engineRunSeams{})
		if !strings.Contains(r.view(80, 26), amber) {
			t.Error("the mark is not amber (style.DegradedText); the stop before it was")
		}
	})
}

// --- C5: one working line ---------------------------------------------------

// workingScreen is a flow with something running: open puts it there and
// hands back what a person sees (plain, then with colour) and a way to
// let one UI tick through.
type workingScreen struct {
	name   string
	phrase string // how the line begins, after the spinner and its space
	open   func(t *testing.T) (plain, raw func() string, tick func())
}

func modelWorking[M tea.Model](t *testing.T, m M) (func() string, func() string, func()) {
	return func() string { return screen(m) },
		func() string { return m.View().Content },
		func() { m, _ = updateAs(t, m, tickMsg{}) }
}

func workingScreens() []workingScreen {
	return []workingScreen{
		{"Remove standing down", "standing down Orbit", func(t *testing.T) (func() string, func() string, func()) {
			m := newTestRemoveModel(func(context.Context, string) error { return nil })
			m, _ = updateAs(t, m, key(tea.KeyEnter))
			if m.state != removeStateStandingDown {
				t.Fatalf("Remove is not standing down (state %v)", m.state)
			}
			return modelWorking(t, m)
		}},
		{"Repair reading", "reading the deployment", func(t *testing.T) (func() string, func() string, func()) {
			return modelWorking(t, sizedRepair(t))
		}},
		{"Repair running the safe repairs", "running the safe repairs", func(t *testing.T) (func() string, func() string, func()) {
			m := sizedRepair(t)
			m.state = repairExecuting
			return modelWorking(t, m)
		}},
		{"Repair rotation before the first prompt", "talking to the engine", func(t *testing.T) (func() string, func() string, func()) {
			m := sizedRepair(t)
			m.state = repairRotating
			return modelWorking(t, m)
		}},
		{"Repair rotation stopping", "stopping", func(t *testing.T) (func() string, func() string, func()) {
			m := sizedRepair(t)
			m.state = repairRotating
			m, _ = updateAs(t, m, repairRotateReadyMsg{stream: sleepingStream(t), stdin: &recordingStdin{}})
			m = repairLines(t, m, "prompt field=action-word kind=typed-word required=true attempt=1")
			m, _ = updateAs(t, m, key(tea.KeyEsc))
			return modelWorking(t, m)
		}},
		{"Install waiting on the configuration session", "talking to the engine", func(t *testing.T) (func() string, func() string, func()) {
			m, _ := newTestInstallModel(engineRunSeams{})
			m.run = collectingRun(t, engineRunSeams{})
			m.state = installStateRunning
			return modelWorking(t, m)
		}},
	}
}

// frameOn is the index in style.SpinnerFrames of the one spinner glyph on
// a screen; -1 when there is none or more than one.
func frameOn(s string) int {
	found := -1
	for i, f := range style.SpinnerFrames {
		if n := strings.Count(s, string(f)); n > 1 || (n == 1 && found >= 0) {
			return -1
		} else if n == 1 {
			found = i
		}
	}
	return found
}

// C5: the line is the spinner glyph in WarmText, a space, then the
// phrase.
func TestLookWorking_ASpinnerThenASpaceThenThePhrase(t *testing.T) {
	for _, w := range workingScreens() {
		t.Run(w.name, func(t *testing.T) {
			plain, raw, _ := w.open(t)
			n := frameOn(plain())
			if n < 0 {
				t.Fatalf("want exactly one spinner glyph on screen:\n%s", plain())
			}
			frame := string(style.SpinnerFrames[n])
			var line string
			for _, l := range strings.Split(plain(), "\n") {
				if strings.Contains(l, w.phrase) {
					line = lookTrim(l)
				}
			}
			if !strings.HasPrefix(line, frame+" "+w.phrase) {
				t.Errorf("the line = %q, want it to begin %q", line, frame+" "+w.phrase)
			}
			if !strings.Contains(raw(), style.WarmText.Render(frame)) {
				t.Errorf("the spinner glyph is not in WarmText")
			}
		})
	}
}

// C5: the glyph advances one frame on every tick, round the ring and
// back; a glyph that never moves reads as a hang.
func TestLookWorking_TheSpinnerAdvancesOneFrameOnEachTick(t *testing.T) {
	for _, w := range workingScreens() {
		t.Run(w.name, func(t *testing.T) {
			plain, _, tick := w.open(t)
			n := frameOn(plain())
			if n < 0 {
				t.Fatalf("want exactly one spinner glyph on screen:\n%s", plain())
			}
			frames := len(style.SpinnerFrames)
			for i := 1; i <= frames+1; i++ {
				tick()
				if got, want := frameOn(plain()), (n+i)%frames; got != want {
					t.Fatalf("after %d ticks the frame is %d, want %d", i, got, want)
				}
			}
		})
	}
}

// C5: no working view carries an ellipsis, before or after a tick.
func TestLookWorking_NoWorkingViewHasAnEllipsis(t *testing.T) {
	for _, w := range workingScreens() {
		t.Run(w.name, func(t *testing.T) {
			plain, _, tick := w.open(t)
			if s := plain(); strings.Contains(s, "…") {
				t.Errorf("the working view has an ellipsis:\n%s", s)
			}
			tick()
			if s := plain(); strings.Contains(s, "…") {
				t.Errorf("the working view has an ellipsis after a tick:\n%s", s)
			}
		})
	}
}
