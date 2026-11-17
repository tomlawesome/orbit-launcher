package ui

// #354: when the splash knows a newer launcher, Update's dead-end screens
// carry the launcher's installer command in the confirm screen's box, with
// Copy command before the last row. Written from the design note alone.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/release"
)

const launcherLine354 = "to update the launcher, copy and run"

// scenario354 builds one dead-end screen at width x 26, with or without
// the splash's newer-launcher notice. buf receives the clipboard for
// scenarios driven directly (direct); called flags an engine start.
type scenario354 struct {
	name   string
	rows   []string // rows with the notice
	after  string   // text the block follows
	direct bool
	build  func(t *testing.T, width int, notice bool, buf *bytes.Buffer, called *bool) tea.Model
}

func appFlow354(t *testing.T, m AppModel, width int, notice bool, label string) tea.Model {
	t.Helper()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 26})
	if notice {
		m = withNotice349(t, m)
	}
	return openFlow(t, m, label)
}

func direct354(t *testing.T, m UpdateModel, width int, notice bool, buf *bytes.Buffer) UpdateModel {
	t.Helper()
	if notice {
		m = m.withLauncherUpdate("v9.9.9")
	}
	m.clipboard = buf
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 26})
	return m
}

func release354(t *testing.T, installed string, target release.Target, width int, notice bool, buf *bytes.Buffer, called *bool) tea.Model {
	t.Helper()
	m, _ := newTestUpdateModel(versionedDeployment(installed), refusingSeams(called))
	m.lookupTarget = func(context.Context) (release.Target, error) { return target, nil }
	if notice {
		m = m.withLauncherUpdate("v9.9.9")
	}
	m.clipboard = buf
	u, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 26})
	u, _ = u.Update(installedAtMsg{at: time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)})
	u, _ = u.Update(targetMsg{target: target, err: nil})
	return u
}

func scenarios354() []scenario354 {
	return []scenario354{
		{name: "not found", rows: []string{"Copy command", "Menu"}, after: "Install is the way to get into Orbit first.",
			build: func(t *testing.T, w int, n bool, _ *bytes.Buffer, _ *bool) tea.Model {
				return appFlow354(t, started(t, folderApp(t, shortTempDir(t), w, 26, deploy.HealthResponding)), w, n, "Update")
			}},
		{name: "not found, direct", direct: true, rows: []string{"Copy command", "Menu"}, after: "Install is the way to get into Orbit first.",
			build: func(t *testing.T, w int, n bool, buf *bytes.Buffer, _ *bool) tea.Model {
				m, _ := newTestUpdateModel(nil, engineRunSeams{})
				return direct354(t, m, w, n, buf)
			}},
		{name: "unreadable record", rows: []string{"Copy command", "Menu"}, after: "Couldn't read .env-orbit in",
			build: func(t *testing.T, w int, n bool, _ *bytes.Buffer, _ *bool) tea.Model {
				dir := shortTempDir(t)
				if err := os.Mkdir(filepath.Join(dir, deploy.EnvFile), 0o700); err != nil {
					t.Fatal(err)
				}
				return appFlow354(t, started(t, folderApp(t, dir, w, 26, deploy.HealthResponding)), w, n, "Update")
			}},
		{name: "unreadable record, direct", direct: true, rows: []string{"Copy command", "Menu"}, after: "Couldn't read .env-orbit in",
			build: func(t *testing.T, w int, n bool, buf *bytes.Buffer, _ *bool) tea.Model {
				m, _ := newTestUpdateModel(nil, engineRunSeams{})
				m.detectErr = errors.New("permission denied")
				return direct354(t, m, w, n, buf)
			}},
		{name: "other folder", rows: []string{"Copy command", "Menu"}, after: "nothing has been",
			build: func(t *testing.T, w int, n bool, _ *bytes.Buffer, _ *bool) tea.Model {
				m, _, _, _ := otherFolderApp(t, existingFolder(t, filepath.Join(shortTempDir(t), "elsewhere")))
				return appFlow354(t, m, w, n, "Update")
			}},
		{name: "other folder, direct", direct: true, rows: []string{"Copy command", "Menu"}, after: "nothing has been",
			build: func(t *testing.T, w int, n bool, buf *bytes.Buffer, called *bool) tea.Model {
				m, _ := newTestUpdateModel(testDeployment(), refusingSeams(called))
				m = m.refuseOtherFolder(folderMismatch(existingFolder(t, filepath.Join(shortTempDir(t), "elsewhere"))))
				return direct354(t, m, w, n, buf)
			}},
		{name: "gone folder, carry on", direct: true, rows: []string{"Carry on from this folder", "Copy command", "Cancel"}, after: "nothing has been",
			build: func(t *testing.T, w int, n bool, buf *bytes.Buffer, called *bool) tea.Model {
				m, _ := newTestUpdateModel(testDeployment(), refusingSeams(called))
				m = m.refuseOtherFolder(goneMismatch(false))
				m.refusal = newFolderRefusal(goneMismatch(false)) // the carry-on variant (Remove's rows)
				return direct354(t, m, w, n, buf)
			}},
		{name: "run from here", rows: []string{"Run from this folder", "Move it back", "Copy command", "Cancel"}, after: "nothing has been",
			build: func(t *testing.T, w int, n bool, _ *bytes.Buffer, _ *bool) tea.Model {
				m, _, _ := goneApp(t, false)
				return appFlow354(t, m, w, n, "Update")
			}},
		{name: "run from here, direct", direct: true, rows: []string{"Run from this folder", "Move it back", "Copy command", "Cancel"}, after: "nothing has been",
			build: func(t *testing.T, w int, n bool, buf *bytes.Buffer, called *bool) tea.Model {
				m, _ := newTestUpdateModel(testDeployment(), refusingSeams(called))
				m = m.refuseOtherFolder(goneMismatch(false))
				return direct354(t, m, w, n, buf)
			}},
		{name: "release current", direct: true, rows: []string{"Copy command", "Menu"}, after: "nothing has been",
			build: func(t *testing.T, w int, n bool, buf *bytes.Buffer, called *bool) tea.Model {
				return release354(t, "v0.3.6", currentTarget, w, n, buf, called)
			}},
		{name: "release newer", direct: true, rows: []string{"Copy command", "Menu"}, after: "nothing has been",
			build: func(t *testing.T, w int, n bool, buf *bytes.Buffer, called *bool) tea.Model {
				return release354(t, "v0.3.7", release.Target{Version: "v0.3.6", Pin: true}, w, n, buf, called)
			}},
	}
}

func build354(t *testing.T, sc scenario354, width int, notice bool) (tea.Model, *bytes.Buffer, *bool) {
	t.Helper()
	buf, called := &bytes.Buffer{}, new(bool)
	return sc.build(t, width, notice, buf, called), buf, called
}

// assertBlock354 checks the faint line, the box and the rows around it.
func assertBlock354(t *testing.T, s, after string, rows []string) {
	t.Helper()
	lines := strings.Split(s, "\n")
	idx := func(sub string) int {
		for i, l := range lines {
			if strings.Contains(l, sub) {
				return i
			}
		}
		return -1
	}
	line, afterAt, top := idx(launcherLine354), idx(after), idx("╭")
	if line < 0 || top < 0 {
		t.Fatalf("no %q line or no box:\n%s", launcherLine354, s)
	}
	if afterAt < 0 || afterAt >= line {
		t.Errorf("the block (row %d) does not follow %q (row %d):\n%s", line, after, afterAt, s)
	}
	if line > 0 && screenRow(lines[line-1]) != "" {
		t.Errorf("no blank row above the faint line:\n%s", s)
	}
	if top < line {
		t.Errorf("the box is above its faint line:\n%s", s)
	}
	bottom := -1
	for i := top; i < len(lines); i++ {
		if strings.Contains(lines[i], "╰") {
			bottom = i
			break
		}
	}
	if bottom < 0 || bottom+1 >= len(lines) || screenRow(lines[bottom+1]) != "" {
		t.Errorf("no blank row under the box:\n%s", s)
	}
	if got := narrowReadBack(narrowBoxInside(t, s)); got != launcherCmd349 {
		t.Errorf("the box reads back as %q, want %q:\n%s", got, launcherCmd349, s)
	}
	if got := screenLabels(s); !sameLabels(got, rows) {
		t.Errorf("rows = %q, want %q:\n%s", got, rows, s)
	}
	// Search from the faint line down: a body line above the block may
	// begin with the same words as a row ("Run from this folder: ...").
	for i := line; i < len(lines) && i <= bottom; i++ {
		if strings.Contains(lines[i], rows[0]) {
			t.Errorf("the rows start above the box:\n%s", s)
			break
		}
	}
}

func assertFits354(t *testing.T, s string, width, height int) {
	t.Helper()
	for _, r := range rowsOf(s) {
		if w := lipgloss.Width(r); w > width {
			t.Errorf("a row is %d cells, want at most %d: %q", w, width, r)
		}
	}
	if height > 0 && screenRows(s) > height {
		t.Errorf("the screen is %d rows, want at most %d:\n%s", screenRows(s), height, s)
	}
}

func TestUpdateLauncher354_DeadEndsShowTheBlockAndRows(t *testing.T) {
	for _, sc := range scenarios354() {
		t.Run(sc.name, func(t *testing.T) {
			m, _, _ := build354(t, sc, 80, true)
			s := screen(m)
			assertBlock354(t, s, sc.after, sc.rows)
			assertFits354(t, s, 80, 26)
		})
	}
}

func decodeClip354(t *testing.T, raw string) string {
	t.Helper()
	const prefix, suffix = "\x1b]52;c;", "\x07"
	if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, suffix) {
		t.Fatalf("clipboard write = %q, want an OSC 52 sequence", raw)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(raw, prefix), suffix))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	return string(b)
}

func TestUpdateLauncher354_CopyCommandWritesTheCommandAndChangesNothingElse(t *testing.T) {
	for _, sc := range scenarios354() {
		if !sc.direct {
			continue
		}
		t.Run(sc.name, func(t *testing.T) {
			m, buf, called := build354(t, sc, 80, true)
			before := screen(m)
			m = moveCaretTo(t, m, "Copy command")
			beforeState := m
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			if cmd == nil {
				t.Fatal("Copy command ran nothing")
			}
			cmd()
			if got := decodeClip354(t, buf.String()); got != launcherCmd349 {
				t.Errorf("copied %q, want %q", got, launcherCmd349)
			}
			s := screen(m)
			if !strings.Contains(s, "Copy command · copied") {
				t.Errorf("the row does not read %q:\n%s", "Copy command · copied", s)
			}
			if strings.ReplaceAll(s, " · copied", "") != strings.ReplaceAll(before, " · copied", "") &&
				len(screenLabels(s)) != len(sc.rows) {
				t.Errorf("the screen changed beyond the copied label:\n%s", s)
			}
			if um, ok := m.(UpdateModel); ok {
				if um.state != beforeState.(UpdateModel).state {
					t.Errorf("state changed from %v to %v", beforeState.(UpdateModel).state, um.state)
				}
			}
			if *called {
				t.Error("the engine started")
			}
		})
	}
}

func TestUpdateLauncher354_EscMenuAndCancelStillGoBack(t *testing.T) {
	for _, sc := range scenarios354() {
		if !sc.direct {
			continue
		}
		last := sc.rows[len(sc.rows)-1]
		for _, how := range []string{"esc", last} {
			t.Run(sc.name+" "+how, func(t *testing.T) {
				m, _, called := build354(t, sc, 80, true)
				k := key(tea.KeyEsc)
				if how != "esc" {
					m = moveCaretTo(t, m, last)
					k = key(tea.KeyEnter)
				}
				m, cmd := updateAs(t, m, k)
				if cmd != nil {
					cmd()
				}
				if o := m.(UpdateModel).Outcome(); !leftForMenu(o) {
					t.Errorf("outcome = %+v, want back to the menu", o)
				}
				if *called {
					t.Error("the engine started")
				}
			})
		}
	}
}

func TestUpdateLauncher354_NoNoticeNoBlock(t *testing.T) {
	for _, sc := range scenarios354() {
		t.Run(sc.name, func(t *testing.T) {
			m, _, _ := build354(t, sc, 80, false)
			s := screen(m)
			for _, bad := range []string{"to update the launcher", "curl", "╭", "Copy command"} {
				if strings.Contains(s, bad) {
					t.Errorf("without a known version the screen shows %q:\n%s", bad, s)
				}
			}
		})
	}
}

// Repair and Remove refusals never show the block, notice or not.
func TestUpdateLauncher354_RepairAndRemoveRefusalsShowNone(t *testing.T) {
	for _, label := range []string{"Repair", "Remove"} {
		t.Run(label+" other folder", func(t *testing.T) {
			m, _, _, _ := otherFolderApp(t, existingFolder(t, filepath.Join(shortTempDir(t), "elsewhere")))
			s := screen(appFlow354(t, m, 80, true, label))
			for _, bad := range []string{"to update the launcher", "curl", "╭", "Copy command"} {
				if strings.Contains(s, bad) {
					t.Errorf("shows %q:\n%s", bad, s)
				}
			}
		})
		t.Run(label+" gone folder", func(t *testing.T) {
			m, _, _ := goneApp(t, false)
			s := screen(appFlow354(t, m, 80, true, label))
			for _, bad := range []string{"to update the launcher", "curl", "╭", "Copy command"} {
				if strings.Contains(s, bad) {
					t.Errorf("shows %q:\n%s", bad, s)
				}
			}
		})
	}
}

func TestUpdateLauncher354_At50ColumnsEveryRowFitsAndTheBoxReadsBack(t *testing.T) {
	for _, sc := range scenarios354() {
		if sc.name != "not found" && sc.name != "other folder" {
			continue
		}
		t.Run(sc.name, func(t *testing.T) {
			m, _, _ := build354(t, sc, 50, true)
			s := screen(m)
			assertFits354(t, s, 50, 0)
			if got := narrowReadBack(narrowBoxInside(t, s)); got != launcherCmd349 {
				t.Errorf("the box reads back as %q, want %q:\n%s", got, launcherCmd349, s)
			}
			if got := screenLabels(s); !sameLabels(got, sc.rows) {
				t.Errorf("rows = %q, want %q:\n%s", got, sc.rows, s)
			}
		})
	}
}
