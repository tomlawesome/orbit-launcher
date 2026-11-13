package ui

// #285: the done screen offers the file-by-file command and says what
// stays. Owner answers 19a (the person runs it; the launcher deletes
// nothing), 20a (backups are never in the command and the folder stays
// while they are there), 21a (home or root: same command, the folder
// itself never removed) and note 41988 (a folder without .orbit-files
// gets today's screen unchanged). Uses deploy.Own, deploy.OwnFiles and
// the two-argument deploy.RemovalCommand (see deploy/own_files_test.go).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

var ownUITodays = []string{
	"docker-compose.yml", "docker-compose.mail.yml", "docker-compose.kek-rotation.yml",
	".env-orbit.example", "config/tika-config.json", "scripts/backup.sh", "scripts/configure.sh",
	"scripts/end-maintenance.sh", "scripts/export-recovery-bundle.sh", "scripts/import-recovery-bundle.sh",
	"scripts/installer-ui.sh", "scripts/repair.sh", "scripts/restore.sh", ".env-orbit", ".orbit-secrets/", ".orbit-files",
}

const ownUIHeader = "# Files Orbit installed in this folder. orbit-launcher's Remove reads this list; do not edit.\n"

// ownUIFolder makes dir with today's Orbit (when manifest is true) and
// the extra entries (a trailing slash is a folder holding one file).
func ownUIFolder(t *testing.T, dir string, manifest bool, extra ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := append([]string{}, extra...)
	if manifest {
		files = append(files, ownUITodays...)
	}
	for _, f := range files {
		if manifest && f == ".orbit-files" {
			continue // written below, last, so the list is not overwritten
		}
		p := filepath.Join(dir, f)
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			p = filepath.Join(p, "inside")
		} else if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if manifest {
		if err := os.WriteFile(filepath.Join(dir, ".orbit-files"), []byte(ownUIHeader+strings.Join(ownUITodays, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// ownDone runs Remove for dir through the stand-down to the done screen
// at 80x26.
func ownDone(t *testing.T, dir string, clip *bytes.Buffer, standDown func(context.Context, string) error) RemoveModel {
	t.Helper()
	if standDown == nil {
		standDown = standDownOK
	}
	m := lowsRemoveModel(&deploy.Deployment{TargetDir: dir}, standDown)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	if clip != nil {
		m.clipboard = clip
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("confirming started nothing")
	}
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateDone {
		t.Fatalf("state = %v, want removeStateDone", m.state)
	}
	return m
}

func ownCommand(t *testing.T, dir string) string {
	t.Helper()
	own, err := deploy.OwnFiles(dir)
	if err != nil {
		t.Fatalf("OwnFiles: %v", err)
	}
	cmd, err := deploy.RemovalCommand(dir, own)
	if err != nil {
		t.Fatalf("RemovalCommand: %v", err)
	}
	return cmd
}

func ownCopied(t *testing.T, m RemoveModel, clip *bytes.Buffer) string {
	t.Helper()
	_, cmd := updateAs(t, m, key(tea.KeyEnter)) // Copy command is the default row
	if cmd == nil {
		t.Fatal("Copy command ran nothing")
	}
	cmd()
	return decodeOSC52(t, clip.String())
}

func ownFlat(m RemoveModel) string { return proseOnly(screen(m)) }

func assertRowsFit(t *testing.T, s string) {
	t.Helper()
	for _, row := range strings.Split(s, "\n") {
		if w := lipgloss.Width(row); w > 80 {
			t.Errorf("row is %d cells on an 80-column screen: %q", w, row)
		}
	}
	if n := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); n > 26 {
		t.Errorf("screen is %d rows, want at most 26:\n%s", n, s)
	}
}

func TestRemoveOwn_NothingElseInTheFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orbit")
	ownUIFolder(t, dir, true)
	var clip bytes.Buffer
	m := ownDone(t, dir, &clip, nil)
	flat := ownFlat(m)
	for _, want := range []string{
		"Orbit has been stood down",
		"nothing has been deleted.",
		"this deletes all mail, documents and configuration — it cannot be undone",
		"The folder is removed too: nothing else is in it.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen(m))
		}
	}
	if strings.Contains(flat, "backups included") || strings.Contains(flat, "everything else in the folder") {
		t.Errorf("the old whole-folder warning is still shown:\n%s", screen(m))
	}
	if strings.Contains(flat, "Left in place") {
		t.Errorf("a leftovers line with nothing left:\n%s", screen(m))
	}
	assertRowsFit(t, screen(m))
	if got := screenLabels(screen(m)); !reflect.DeepEqual(got, []string{removeCopy, "Menu", "Exit"}) {
		t.Errorf("menu rows = %q", got)
	}
	// Copy carries the whole command, the one the box is drawn from.
	copied := ownCopied(t, m, &clip)
	if want := ownCommand(t, dir); copied != want {
		t.Errorf("copied\n%s\nwant\n%s", copied, want)
	}
	if !strings.Contains(copied, "sudo rm -rf -- docker-compose.yml") || strings.Contains(copied, "rm -rf "+dir) {
		t.Errorf("the command is not file by file:\n%s", copied)
	}
}

func TestRemoveOwn_LeftInPlaceNamesBackupsAndTheFolderStays(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orbit")
	ownUIFolder(t, dir, true, "backups/a.tar", "notes.txt")
	var clip bytes.Buffer
	m := ownDone(t, dir, &clip, nil)
	flat := ownFlat(m)
	if want := "Left in place for you: backups, notes.txt — the folder stays while they are in it."; !strings.Contains(flat, want) {
		t.Errorf("screen lacks %q:\n%s", want, screen(m))
	}
	if strings.Contains(flat, "The folder is removed too") {
		t.Errorf("the screen says the folder goes too:\n%s", screen(m))
	}
	copied := ownCopied(t, m, &clip)
	if strings.Contains(copied, "backups") || strings.Contains(copied, "notes.txt") {
		t.Errorf("the command names what is left in place:\n%s", copied)
	}
}

func TestRemoveOwn_ManyLeftoversNameThreeThenCount_AndTheMenuStaysOnScreen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", 60), "orbit")
	ownUIFolder(t, dir, true, "a1", "a2", "a3", "a4", "a5", "a6")
	var clip bytes.Buffer
	m := ownDone(t, dir, &clip, nil)
	s := screen(m)
	if want := "Left in place for you: a1, a2, a3 and 3 more — the folder stays while they are in it."; !strings.Contains(ownFlat(m), want) {
		t.Errorf("screen lacks %q:\n%s", want, s)
	}
	if got := screenLabels(s); !reflect.DeepEqual(got, []string{removeCopy, "Menu", "Exit"}) {
		t.Errorf("menu rows = %q, want all three on screen:\n%s", got, s)
	}
	assertRowsFit(t, s)
	// The box may show "…"; Copy command still carries the whole command.
	if copied := ownCopied(t, m, &clip); copied != ownCommand(t, dir) || !strings.Contains(copied, "docker compose") {
		t.Errorf("copied command is not the whole command:\n%s", copied)
	}
}

func TestRemoveOwn_ARefusedListSaysSo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orbit")
	ownUIFolder(t, dir, false)
	if err := os.WriteFile(filepath.Join(dir, ".orbit-files"), []byte(ownUIHeader+"/etc/passwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var clip bytes.Buffer
	m := ownDone(t, dir, &clip, nil)
	if want := "Orbit's own file list here could not be read"; !strings.Contains(ownFlat(m), want) {
		t.Errorf("screen lacks %q:\n%s", want, screen(m))
	}
	if copied := ownCopied(t, m, &clip); strings.Contains(copied, "/etc/passwd") {
		t.Errorf("a refused list reached the command:\n%s", copied)
	}
}

func TestRemoveOwn_HomeFolderKeepsItselfAndOffersTheSameCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ownUIFolder(t, home, true, "Documents/")
	var clip bytes.Buffer
	m := ownDone(t, home, &clip, nil)
	flat := ownFlat(m)
	if !strings.Contains(flat, "Your home folder itself is never removed.") {
		t.Errorf("screen lacks the home-folder line:\n%s", screen(m))
	}
	for _, gone := range []string{"The folder is removed too", "The folder itself is not removed: it is your home folder"} {
		if strings.Contains(flat, gone) {
			t.Errorf("screen still says %q:\n%s", gone, screen(m))
		}
	}
	copied := ownCopied(t, m, &clip)
	if !strings.Contains(copied, "sudo rm -rf -- docker-compose.yml") || strings.Contains(copied, "rmdir -- "+home) || strings.Contains(copied, "Documents") {
		t.Errorf("home-folder command is wrong:\n%s", copied)
	}
	// Remove's first screen no longer shortens its promise for home.
	c := lowsRemoveModel(&deploy.Deployment{TargetDir: home}, standDownOK)
	if cs := proseOnly(screen(c)); !strings.Contains(cs, "shows exactly how to remove them") {
		t.Errorf("confirm screen for a home folder with a manifest lost its full prose:\n%s", screen(c))
	}
}

// Note 41988: no .orbit-files, today's screen, whatever else is there.
func TestRemoveOwn_NoManifestIsTodaysScreen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orbit")
	ownUIFolder(t, dir, false, "docker-compose.yml", ".env-orbit", "backups/a.tar", "notes.txt")
	var clip bytes.Buffer
	m := ownDone(t, dir, &clip, nil)
	flat := ownFlat(m)
	const warn = "this deletes all mail, documents and configuration, and everything else in the folder, backups included — it cannot be undone"
	if !strings.Contains(flat, warn) {
		t.Errorf("today's warning is gone:\n%s", screen(m))
	}
	for _, not := range []string{"Left in place", "The folder is removed too", "could not be read", "itself is never removed"} {
		if strings.Contains(flat, not) {
			t.Errorf("screen shows %q for a folder without .orbit-files:\n%s", not, screen(m))
		}
	}
	want, err := deploy.RemovalCommand(dir, deploy.Own{})
	if err != nil {
		t.Fatal(err)
	}
	if copied := ownCopied(t, m, &clip); copied != want || !strings.HasSuffix(copied, "&& sudo rm -rf "+dir) {
		t.Errorf("copied\n%s\nwant today's\n%s", copied, want)
	}
}

// 19a: the launcher shows; it deletes nothing, before or after Copy.
func TestRemoveOwn_TheLauncherDeletesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orbit")
	ownUIFolder(t, dir, true, "backups/a.tar", "notes.txt")
	list := func() []string {
		var out []string
		_ = filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error { out = append(out, p); return nil })
		return out
	}
	before := list()
	var clip bytes.Buffer
	m := ownDone(t, dir, &clip, nil)
	ownCopied(t, m, &clip)
	if after := list(); !reflect.DeepEqual(before, after) {
		t.Errorf("the folder changed:\nbefore %q\nafter  %q", before, after)
	}
}

// The command is built when the done screen is entered, after the
// stand-down, and stored: what the stand-down leaves behind is counted.
func TestRemoveOwn_BuiltAfterTheStandDown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orbit")
	ownUIFolder(t, dir, true)
	m := ownDone(t, dir, nil, func(_ context.Context, d string) error {
		return os.WriteFile(filepath.Join(d, "left-by-standdown.txt"), []byte("x"), 0o644)
	})
	if !strings.Contains(ownFlat(m), "Left in place for you: left-by-standdown.txt") {
		t.Errorf("an entry present when the done screen was entered is not named:\n%s", screen(m))
	}
}

// Other Remove screens are unchanged: a failed stand-down offers only the
// data-volume command, with none of the new lines.
func TestRemoveOwn_FailedStandDownScreenIsUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orbit")
	ownUIFolder(t, dir, true, "notes.txt")
	m := lowsRemoveModel(&deploy.Deployment{TargetDir: dir}, func(context.Context, string) error { return os.ErrInvalid })
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateFailed {
		t.Fatalf("state = %v, want removeStateFailed", m.state)
	}
	flat := ownFlat(m)
	for _, not := range []string{"Left in place", "The folder is removed too", "rm -rf", "rmdir"} {
		if strings.Contains(flat, not) {
			t.Errorf("failed screen shows %q:\n%s", not, screen(m))
		}
	}
}
