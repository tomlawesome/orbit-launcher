package ui

// v0.4.3 group B, W3 (ratified design, issue #277): a folder whose name
// holds a control character stops Install, Update, Repair's diagnosis and
// the credential rotation with one sentence, shown under the existing
// headings ("The launcher couldn't start the installer." / "Diagnosis
// stopped"):
//
//	the folder's name holds a control character (\x1b), so nothing runs in it:
//	rename the folder to a plain name, then run the launcher from there
//
// Install's and Update's stopped screens offer Menu and Exit only (a rerun
// from the same folder fails the same way), and the folder is not read: no
// "reading the folder" row, no listing. The safe and dangerous repairs are
// refused at the deploy level (internal/deploy/groupb_name_refusal_test.go);
// the screens cannot reach them past the diagnosis.
//
// Written from the specification alone. These tests go through the real
// start (no prepare seam), with the launcher's local script override so
// nothing is fetched.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// gbStDeployment is the deployment an Update of dir acts on.
func gbStDeployment(dir string) *deploy.Deployment {
	return &deploy.Deployment{TargetDir: dir, AppURL: "https://mail.example.com", Image: "ghcr.io/tomlawesome/orbit@sha256:abc"}
}

const (
	gbStNameLine1 = "the folder's name holds a control character (`ESC`), so nothing runs in it:"
	gbStNameLine2 = "rename the folder to a plain name, then run the launcher from there"
)

func gbStLine1(escape string) string {
	return strings.Replace(gbStNameLine1, "`ESC`", escape, 1)
}

// gbStNamedFolder is a folder path whose last name holds ESC (not made
// on disk: the refusal must not need the folder).
func gbStNamedFolder(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortTempDir(t), "ab\x1bcd")
}

// gbStCountingList is a folder reader that counts its calls and finds
// nothing, so a screen that reads the folder shows up as a count.
func gbStCountingList(calls *atomic.Int32) func(string) ([]os.DirEntry, error) {
	return func(string) ([]os.DirEntry, error) {
		calls.Add(1)
		return nil, nil
	}
}

func gbStSentenceOnScreen(t *testing.T, s string) {
	t.Helper()
	words := flat(s)
	i := strings.Index(words, gbStLine1(`\x1b`))
	j := strings.Index(words, gbStNameLine2)
	if i < 0 || j < 0 {
		t.Errorf("the refusal sentence is not on the screen (first line %v, second line %v):\n%s", i >= 0, j >= 0, s)
		return
	}
	if i > j {
		t.Errorf("the sentence's lines are out of order:\n%s", s)
	}
	if strings.Contains(s, "\x1b") {
		t.Errorf("the screen holds a raw ESC:\n%q", s)
	}
}

// Install in a control-named folder stops with the sentence under the
// launcher's own heading, offers Menu and Exit only, and reads nothing.
func TestGroupB_W3_InstallInAControlNamedFolderStopsWithTheSentence(t *testing.T) {
	installScriptAt(t, "#!/usr/bin/env bash\nexit 0\n")
	dir := gbStNamedFolder(t)
	var listed atomic.Int32
	m := NewInstallModel(dir, "v9.9.9")
	m.seams = engineRunSeams{detect: fakeDetect("https://mail.example.com"), listFolder: gbStCountingList(&listed)}
	m.send = newSink().Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd)
	s := screen(m)

	if m.run.state != runFailed {
		t.Fatalf("the run did not stop (state %v):\n%s", m.run.state, s)
	}
	for _, want := range []string{"Install stopped", stopCouldNotStart} {
		if !strings.Contains(flat(s), want) {
			t.Errorf("%q is missing:\n%s", want, s)
		}
	}
	gbStSentenceOnScreen(t, s)
	if got := screenLabels(s); !slices.Equal(got, rowsLeave) {
		t.Errorf("rows = %q, want %q (a rerun from this folder fails the same way):\n%s", got, rowsLeave, s)
	}
	for _, gone := range []string{"Install again", "Open the guided installer", "reading the folder", "in the folder", "choose another folder"} {
		if strings.Contains(flat(s), gone) {
			t.Errorf("the screen shows %q:\n%s", gone, s)
		}
	}
	if n := listed.Load(); n != 0 {
		t.Errorf("the folder was read %d time(s); nothing is read from it", n)
	}
}

// Update in a control-named folder: the same sentence under "Update
// stopped", Menu and Exit only.
func TestGroupB_W3_UpdateInAControlNamedFolderStopsWithTheSentence(t *testing.T) {
	installScriptAt(t, "#!/usr/bin/env bash\nexit 0\n")
	dir := gbStNamedFolder(t)
	var listed atomic.Int32
	d := gbStDeployment(dir)
	m := NewUpdateModel(d, dir, "v9.9.9")
	m.seams = engineRunSeams{detect: fakeDetect("https://mail.example.com"), listFolder: gbStCountingList(&listed)}
	m.send = newSink().Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Update Orbit is preselected
	m = drive(t, m, cmd)
	s := screen(m)

	if m.run.state != runFailed {
		t.Fatalf("the update did not stop (state %v):\n%s", m.run.state, s)
	}
	for _, want := range []string{"Update stopped", stopCouldNotStart} {
		if !strings.Contains(flat(s), want) {
			t.Errorf("%q is missing:\n%s", want, s)
		}
	}
	gbStSentenceOnScreen(t, s)
	if got := screenLabels(s); !slices.Equal(got, rowsLeave) {
		t.Errorf("rows = %q, want %q:\n%s", got, rowsLeave, s)
	}
	if strings.Contains(flat(s), "Update again") {
		t.Errorf("the screen offers Update again:\n%s", s)
	}
	if n := listed.Load(); n != 0 {
		t.Errorf("the folder was read %d time(s); nothing is read from it", n)
	}
}

// Repair's diagnosis in a control-named folder stops with the sentence
// under "Diagnosis stopped", with Menu and Exit on screen.
func TestGroupB_W3_RepairDiagnosisInAControlNamedFolderStopsWithTheSentence(t *testing.T) {
	dir := gbStNamedFolder(t)
	m := NewRepairModel(dir, "v9.9.9")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	m, _ = updateAs(t, m, m.Init()())
	s := screen(m)

	if m.state != repairError {
		t.Fatalf("state = %v, want the stopped screen:\n%s", m.state, s)
	}
	if !strings.Contains(s, "Diagnosis stopped") {
		t.Errorf("not titled Diagnosis stopped:\n%s", s)
	}
	gbStSentenceOnScreen(t, s)
	for _, want := range []string{"Menu", "Exit"} {
		if !hasRow(s, want) {
			t.Errorf("no %q row:\n%s", want, s)
		}
	}
}

// The credential rotation's start is refused the same way, with the
// sentence as the error the screen shows under its own heading.
func TestGroupB_W3_CredentialRotationInAControlNamedFolderIsRefused(t *testing.T) {
	dir := gbStNamedFolder(t)
	m := NewRepairModel(dir, "v9.9.9")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	ready, ok := m.startRotate()().(repairRotateReadyMsg)
	if !ok {
		t.Fatal("startRotate did not answer with a repairRotateReadyMsg")
	}
	if ready.err == nil {
		t.Fatal("the rotation started in a control-named folder")
	}
	if ready.stream != nil || ready.stdin != nil {
		t.Error("a stream or stdin came back alongside the refusal")
	}
	if msg := ready.err.Error(); !strings.Contains(msg, gbStLine1(`\x1b`)) || !strings.Contains(msg, gbStNameLine2) {
		t.Errorf("error %q is not the refusal sentence", msg)
	}
	m.state = repairRotating
	m, _ = updateAs(t, m, ready)
	s := screen(m)
	if !strings.Contains(s, "Credential rotation stopped") {
		t.Errorf("not titled Credential rotation stopped:\n%s", s)
	}
	gbStSentenceOnScreen(t, s)
}

// Other control characters are shown as their own escape.
func TestGroupB_W3_TheSentenceShowsTheCharactersOwnEscape(t *testing.T) {
	for name, tc := range map[string]struct{ name, escape string }{
		"LF":  {"ab\ncd", `\n`},
		"DEL": {"ab\x7fcd", `\x7f`},
		"TAB": {"ab\tcd", `\t`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(shortTempDir(t), tc.name)
			m := NewRepairModel(dir, "v9.9.9")
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
			m, _ = updateAs(t, m, m.Init()())
			s := screen(m)
			if want := gbStLine1(tc.escape); !strings.Contains(flat(s), want) {
				t.Errorf("want %q on the screen:\n%s", want, s)
			}
		})
	}
}

// U+202E alone is not refused: the folder gets through the name check, so
// the sentence never shows.
func TestGroupB_W3_ARightToLeftOverrideAloneIsNotRefusedOnScreen(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "evil\u202etxt")
	m := NewRepairModel(dir, "v9.9.9")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	m, _ = updateAs(t, m, m.Init()())
	if s := screen(m); strings.Contains(s, "holds a control character") {
		t.Errorf("a folder with U+202E alone was refused for a control character:\n%s", s)
	}
}
