package ui

// v0.4.3 group B, L3 (ratified design, issue #277 row 54): the stopped
// screen reads the folder in a command, not in Update, and keeps a slot for
// the answer so nothing moves when it lands.
//
//   - While the read is out, one muted row "reading the folder" sits where
//     the listing will, the next-step lines directly below it.
//   - The menu's rows are on the same lines in that first frame as in the
//     frame after a one-row listing arrives.
//   - Names: the row becomes the listing, "in the folder: ..." (W5's
//     wording: it ends "- the installer needs it empty" and the next-step
//     line is "fix what the installer names above, then install again",
//     never "move them out ...").
//   - Empty folder: the row goes; the next-step line is unchanged.
//   - Error: "the folder could not be read", row kept, next step unchanged.
//   - The seam (engineRunSeams.listFolder) is called only from a command.
//   - An answer after Install again or Menu is dropped.
//   - Update reads and shows nothing of the folder.
//
// The 4 second limit is in groupb_stopped_folder_limit_test.go, which
// assumes a field the specification does not name.
//
// Written from the specification alone.

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	gbStReadingRow  = "reading the folder"
	gbStCouldNotRow = "the folder could not be read"
	gbStListingTail = "the installer needs it empty"
)

// gbStEntry is a fake directory entry.
type gbStEntry struct {
	name string
	dir  bool
}

func (e gbStEntry) Name() string               { return e.name }
func (e gbStEntry) IsDir() bool                { return e.dir }
func (e gbStEntry) Type() fs.FileMode          { return 0 }
func (e gbStEntry) Info() (fs.FileInfo, error) { return nil, errors.ErrUnsupported }

// gbStFolderSeam is a folder reader the test controls: it counts its
// calls and answers from answer (os.ReadDir when nil).
type gbStFolderSeam struct {
	calls  atomic.Int32
	answer func(dir string) ([]os.DirEntry, error)
}

func (f *gbStFolderSeam) read(dir string) ([]os.DirEntry, error) {
	f.calls.Add(1)
	if f.answer == nil {
		return os.ReadDir(dir)
	}
	return f.answer(dir)
}

func gbStAnswers(entries ...os.DirEntry) *gbStFolderSeam {
	return &gbStFolderSeam{answer: func(string) ([]os.DirEntry, error) { return entries, nil }}
}

// gbStDrive is drive for a run whose folder read the test wants to see
// out. With hold, the message the read's command produces is not fed to
// the model: it is returned, so the test can look at the screen while the
// read is out and feed the answer when it likes. Whatever else is pending
// is driven as drive does. Any call of the folder seam inside Update (as
// opposed to a command) is an error.
func gbStDrive[M tea.Model](t *testing.T, m M, cmd tea.Cmd, q *sink, seam *gbStFolderSeam, hold bool) (M, tea.Msg) {
	t.Helper()
	var held tea.Msg
	cmds := []tea.Cmd{cmd}
	for i := 0; ; i++ {
		if i > 1000 {
			t.Fatal("command chain did not settle")
		}
		var msg tea.Msg
		switch {
		case len(cmds) > 0:
			next := cmds[0]
			cmds = cmds[1:]
			if next == nil {
				continue
			}
			before := seam.calls.Load()
			msg = next()
			if hold && held == nil && seam.calls.Load() > before {
				held = msg
				continue
			}
		case q != nil:
			select {
			case msg = <-q.msgs:
			case <-time.After(2 * time.Second):
				return m, held
			}
		default:
			return m, held
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmds = append(cmds, batch...)
			continue
		}
		if _, ok := msg.(tickMsg); ok || msg == nil {
			continue
		}
		before := seam.calls.Load()
		var more tea.Cmd
		m, more = updateAs(t, m, msg)
		if seam.calls.Load() != before {
			t.Errorf("the folder seam was called inside Update (handling %T); it may only be called from a command", msg)
		}
		if _, ended := msg.(engineStreamEndedMsg); ended && more == nil && len(cmds) == 0 {
			return m, held
		}
		cmds = append(cmds, more)
	}
}

// gbStInstallStopped runs an Install in dir through to the stopped
// screen at 80x26, the engine from prepare, the folder from seam. With
// hold the read is left out and its answer returned unfed.
func gbStInstallStopped(t *testing.T, dir string, prepare prepareEngineFunc, seam *gbStFolderSeam, hold bool) (InstallModel, *sink, tea.Msg) {
	t.Helper()
	m := NewInstallModel(dir, "v9.9.9")
	m.seams = engineRunSeams{prepareEngine: prepare, detect: fakeDetect("https://mail.example.com"), listFolder: seam.read}
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := startInstallRun(t, m)
	m, held := gbStDrive(t, m, cmd, s, seam, hold)
	if m.run.state != runFailed {
		t.Fatalf("the run did not stop (state %v):\n%s", m.run.state, screen(m))
	}
	return m, s, held
}

func gbStRefusedEngine() prepareEngineFunc {
	return fakeEngine(nil, hostRefused, exit1(notEmptyStderr...))
}

// gbStRowAt is the index of the screen line whose text, sky and caret
// trimmed, is exactly text; -1 if none.
func gbStRowAt(s, text string) int {
	for i, line := range strings.Split(s, "\n") {
		if trimSky(line) == text {
			return i
		}
	}
	return -1
}

// gbStRowStarting is the index of the first line that starts with prefix.
func gbStRowStarting(s, prefix string) int {
	for i, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(trimSky(line), prefix) {
			return i
		}
	}
	return -1
}

// gbStMenuRows are the line indexes of the stopped screen's rows.
func gbStMenuRows(s string) []int {
	var at []int
	for i, line := range strings.Split(s, "\n") {
		if rowLabels[screenRow(line)] {
			at = append(at, i)
		}
	}
	return at
}

func gbStLine(s string, i int) string {
	lines := strings.Split(s, "\n")
	if i < 0 || i >= len(lines) {
		return ""
	}
	return trimSky(lines[i])
}

// While the read is out the screen holds "reading the folder" where the
// listing will go, with the next-step lines directly below it.
func TestGroupB_L3_WhileTheReadIsOutARowHoldsTheSlot(t *testing.T) {
	dir := folderWith(t, "notes.txt")
	seam := gbStAnswers(gbStEntry{"notes.txt", false})
	m, _, held := gbStInstallStopped(t, dir, gbStRefusedEngine(), seam, true)
	if held == nil {
		t.Fatalf("the folder was not read by a command (seam calls: %d):\n%s", seam.calls.Load(), screen(m))
	}
	s := screen(m)
	at := gbStRowAt(s, gbStReadingRow)
	if at < 0 {
		t.Fatalf("no %q row while the read is out:\n%s", gbStReadingRow, s)
	}
	if got := gbStLine(s, at+1); got != stepFixNamedAbove {
		t.Errorf("the row under %q is %q, want the next step %q:\n%s", gbStReadingRow, got, stepFixNamedAbove, s)
	}
	if got := gbStLine(s, at+2); got != stepAnotherFolder {
		t.Errorf("two rows under %q is %q, want %q:\n%s", gbStReadingRow, got, stepAnotherFolder, s)
	}
	for _, gone := range []string{"in the folder", stepMoveThemOut, gbStCouldNotRow} {
		if strings.Contains(flat(s), gone) {
			t.Errorf("the first frame shows %q:\n%s", gone, s)
		}
	}
	if got := screenLabels(s); !slices.Equal(got, rowsAgain) {
		t.Errorf("rows = %q, want %q while the read is out:\n%s", got, rowsAgain, s)
	}
	if got := caretLabel(s); got != "Install again" {
		t.Errorf("the caret is on %q, want Install again:\n%s", got, s)
	}
}

// The answer replaces the row and the menu does not move: its rows are on
// the same lines before and after a one-row listing, and the caret too.
func TestGroupB_L3_TheAnswerFillsTheSlotAndTheMenuDoesNotMove(t *testing.T) {
	dir := folderWith(t, "a.txt")
	seam := gbStAnswers(gbStEntry{"a.txt", false})
	m, _, held := gbStInstallStopped(t, dir, gbStRefusedEngine(), seam, true)
	if held == nil {
		t.Fatalf("the folder was not read by a command:\n%s", screen(m))
	}
	first := screen(m)
	firstRows := gbStMenuRows(first)
	slot := gbStRowAt(first, gbStReadingRow)
	if slot < 0 {
		t.Fatalf("no %q row in the first frame:\n%s", gbStReadingRow, first)
	}

	m, cmd := updateAs(t, m, held)
	m = drive(t, m, cmd)
	after := screen(m)

	want := "in the folder: a.txt — " + gbStListingTail
	if !strings.Contains(flat(after), want) {
		t.Errorf("want %q once the answer lands:\n%s", want, after)
	}
	if strings.Contains(after, gbStReadingRow) {
		t.Errorf("%q is still on the screen after the answer:\n%s", gbStReadingRow, after)
	}
	if at := gbStRowStarting(after, "in the folder:"); at != slot {
		t.Errorf("the listing is on line %d, the slot was line %d:\n%s", at, slot, after)
	}
	if got := gbStLine(after, slot+1); got != stepFixNamedAbove {
		t.Errorf("the next step is %q, want %q (W5's wording, under the listing):\n%s", got, stepFixNamedAbove, after)
	}
	if strings.Contains(flat(after), stepMoveThemOut) || strings.Contains(flat(after), "move them out") {
		t.Errorf("the screen says \"move them out\":\n%s", after)
	}
	if got := gbStMenuRows(after); !slices.Equal(got, firstRows) {
		t.Errorf("the menu rows moved: lines %v while reading, %v after the answer", firstRows, got)
	}
	if got := caretLabel(after); got != "Install again" {
		t.Errorf("the caret is on %q after the answer, want Install again", got)
	}
}

// An empty folder drops the row; the next step stays the general one.
func TestGroupB_L3_AnEmptyAnswerDropsTheRow(t *testing.T) {
	dir := folderWith(t)
	seam := gbStAnswers()
	m, _, held := gbStInstallStopped(t, dir, gbStRefusedEngine(), seam, true)
	if held == nil {
		t.Fatalf("the folder was not read by a command:\n%s", screen(m))
	}
	if !strings.Contains(screen(m), gbStReadingRow) {
		t.Fatalf("no %q row in the first frame:\n%s", gbStReadingRow, screen(m))
	}
	m, cmd := updateAs(t, m, held)
	m = drive(t, m, cmd)
	s := screen(m)
	for _, gone := range []string{gbStReadingRow, "in the folder", gbStListingTail, gbStCouldNotRow, "move them out"} {
		if strings.Contains(flat(s), gone) {
			t.Errorf("an empty folder's screen shows %q:\n%s", gone, s)
		}
	}
	for _, want := range []string{stepFixNamedAbove, stepAnotherFolder} {
		if !strings.Contains(flat(s), want) {
			t.Errorf("%q is missing:\n%s", want, s)
		}
	}
	if got := screenLabels(s); !slices.Equal(got, rowsAgain) {
		t.Errorf("rows = %q, want %q", got, rowsAgain)
	}
}

// A read that fails leaves "the folder could not be read" in the slot; the
// menu does not move and the next step is unchanged.
func TestGroupB_L3_AnErrorAnswerKeepsTheRowAsCouldNotBeRead(t *testing.T) {
	dir := folderWith(t, "a.txt")
	seam := &gbStFolderSeam{answer: func(string) ([]os.DirEntry, error) { return nil, errors.New("permission denied") }}
	m, _, held := gbStInstallStopped(t, dir, gbStRefusedEngine(), seam, true)
	if held == nil {
		t.Fatalf("the folder was not read by a command:\n%s", screen(m))
	}
	first := screen(m)
	slot := gbStRowAt(first, gbStReadingRow)
	if slot < 0 {
		t.Fatalf("no %q row in the first frame:\n%s", gbStReadingRow, first)
	}
	firstRows := gbStMenuRows(first)

	m, cmd := updateAs(t, m, held)
	m = drive(t, m, cmd)
	s := screen(m)
	if at := gbStRowAt(s, gbStCouldNotRow); at != slot {
		t.Errorf("%q is on line %d, want the slot's line %d:\n%s", gbStCouldNotRow, at, slot, s)
	}
	for _, gone := range []string{gbStReadingRow, "in the folder", "permission denied", "move them out"} {
		if strings.Contains(flat(s), gone) {
			t.Errorf("the screen shows %q:\n%s", gone, s)
		}
	}
	if got := gbStLine(s, slot+1); got != stepFixNamedAbove {
		t.Errorf("the next step is %q, want %q:\n%s", got, stepFixNamedAbove, s)
	}
	if got := gbStMenuRows(s); !slices.Equal(got, firstRows) {
		t.Errorf("the menu rows moved: lines %v while reading, %v after the error", firstRows, got)
	}
}

// The folder seam is never called from Update: the model handling the
// messages that stop the run only returns a command, and the command
// calls the seam. (gbStDrive also fails on any call inside Update.)
func TestGroupB_L3_TheSeamIsCalledFromACommandNeverFromUpdate(t *testing.T) {
	dir := folderWith(t, "a.txt")
	seam := gbStAnswers(gbStEntry{"a.txt", false})
	m := NewInstallModel(dir, "v9.9.9")
	m.seams = engineRunSeams{prepareEngine: gbStRefusedEngine(), detect: fakeDetect("https://mail.example.com"), listFolder: seam.read}
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := startInstallRun(t, m)
	if n := seam.calls.Load(); n != 0 {
		t.Fatalf("the folder was read %d time(s) before the run stopped", n)
	}
	m, held := gbStDrive(t, m, cmd, s, seam, true)
	if held == nil {
		t.Fatalf("no command read the folder (seam calls %d):\n%s", seam.calls.Load(), screen(m))
	}
	if n := seam.calls.Load(); n != 1 {
		t.Errorf("the folder was read %d times, want once", n)
	}
}

// An answer that lands after Install again is dropped: the restarted run
// shows nothing of the old folder read.
func TestGroupB_L3_AnAnswerAfterInstallAgainIsDropped(t *testing.T) {
	dir := folderWith(t, "a.txt")
	seam := gbStAnswers(gbStEntry{"a.txt", false})
	calls := 0
	m, s, held := gbStInstallStopped(t, dir, rerunEngine(t, &calls, gbStRefusedEngine()), seam, true)
	if held == nil {
		t.Fatalf("the folder was not read by a command:\n%s", screen(m))
	}
	m = installAgainRestarts(t, m, s, &calls, "Install again", gbStReadingRow)

	m, cmd := updateAs(t, m, held)
	m = drive(t, m, cmd)
	after := screen(m)
	for _, gone := range []string{"in the folder", gbStReadingRow, gbStCouldNotRow, "Install stopped"} {
		if strings.Contains(after, gone) {
			t.Errorf("the late answer put %q on the restarted run's screen:\n%s", gone, after)
		}
	}
	if m.run.state != runStreaming {
		t.Errorf("run state = %v, want the restarted run still streaming", m.run.state)
	}
}

// An answer that lands after Menu is dropped: the flow has left, and the
// late answer changes neither its screen nor its outcome.
func TestGroupB_L3_AnAnswerAfterMenuIsDropped(t *testing.T) {
	dir := folderWith(t, "a.txt")
	seam := gbStAnswers(gbStEntry{"a.txt", false})
	m, _, held := gbStInstallStopped(t, dir, gbStRefusedEngine(), seam, true)
	if held == nil {
		t.Fatalf("the folder was not read by a command:\n%s", screen(m))
	}
	m = moveCaretTo(t, m, "Menu")
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if o := m.Outcome(); !leftForMenu(o) {
		t.Fatalf("outcome after Menu = %+v, want back to the main menu", o)
	}
	before := screen(m)

	m, _ = updateAs(t, m, held)
	if o := m.Outcome(); !leftForMenu(o) {
		t.Errorf("the late answer changed the outcome to %+v", o)
	}
	if after := screen(m); after != before {
		t.Errorf("the late answer changed the screen:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// Update never reads the folder for a listing, and shows no slot for one.
func TestGroupB_L3_UpdateReadsNothingAndHoldsNoSlot(t *testing.T) {
	dir := folderWith(t, "notes.txt")
	seam := gbStAnswers(gbStEntry{"notes.txt", false})
	m := NewUpdateModel(gbStDeployment(dir), dir, "v9.9.9")
	m.seams = engineRunSeams{prepareEngine: fakeEngine(nil, hostDone, imageRetry, exit1("Orbit installer: Could not pull the image.")), detect: fakeDetect("https://mail.example.com"), listFolder: seam.read}
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Update Orbit
	m, _ = gbStDrive(t, m, cmd, s, seam, false)
	if m.run.state != runFailed {
		t.Fatalf("the update did not stop (state %v):\n%s", m.run.state, screen(m))
	}
	for _, gone := range []string{gbStReadingRow, "in the folder", gbStCouldNotRow, "notes.txt"} {
		if strings.Contains(screen(m), gone) {
			t.Errorf("Update's stopped screen shows %q:\n%s", gone, screen(m))
		}
	}
	if n := seam.calls.Load(); n != 0 {
		t.Errorf("Update read the folder %d time(s)", n)
	}
}
