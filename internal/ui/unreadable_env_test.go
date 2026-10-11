package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// #238: a .env-orbit the launcher cannot read is shown as unreadable,
// not as "No Orbit deployment found". Owner-approved wording, 2026-10-11
// (answer 17a): flow title "Couldn't read .env-orbit in <dir>", the OS's
// words beneath it, and "can't read .env-orbit" on the splash where
// "dormant" would otherwise sit.

// unreadableEnvDir returns a short temp directory holding a .env-orbit
// with mode 0000, so deploy.Detect can see the file but not open it.
// Root ignores file modes, so the test is skipped there.
func unreadableEnvDir(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0000 file; the unreadable case cannot be built")
	}
	dir := shortTempDir(t)
	path := filepath.Join(dir, deploy.EnvFile)
	if err := os.WriteFile(path, []byte("APP_URL=https://mail.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertUnreadableScreen(t *testing.T, s, dir string) {
	t.Helper()
	flat := oneLine(s)
	if want := "Couldn't read .env-orbit in " + dir; !strings.Contains(flat, want) {
		t.Errorf("screen should say %q:\n%s", want, s)
	}
	if !strings.Contains(flat, "permission denied") {
		t.Errorf("screen should carry the OS's words (permission denied):\n%s", s)
	}
	if strings.Contains(flat, "No Orbit deployment found") {
		t.Errorf("an unreadable .env-orbit is not a missing deployment:\n%s", s)
	}
	if !strings.Contains(s, "▸ Menu") || strings.Count(s, "▸") != 1 {
		t.Errorf("the screen is a dead end and should offer Menu and nothing else:\n%s", s)
	}
}

func TestAppModel_RemoveOnAnUnreadableEnvSaysItCouldNotReadIt(t *testing.T) {
	dir := unreadableEnvDir(t)
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = dir
	m = openRemove(t, m)

	if m.state != appStateRemove {
		t.Fatalf("state = %v, want the remove flow", m.state)
	}
	if m.remove.detectErr == nil {
		t.Error("RemoveModel.detectErr is nil; the app dropped deploy.Detect's error")
	}
	assertUnreadableScreen(t, screen(m), dir)
}

func TestAppModel_UpdateOnAnUnreadableEnvSaysItCouldNotReadIt(t *testing.T) {
	dir := unreadableEnvDir(t)
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = dir
	m = openUpdate(t, m, nil)

	if m.state != appStateUpdate {
		t.Fatalf("state = %v, want the update flow", m.state)
	}
	if m.update.detectErr == nil {
		t.Error("UpdateModel.detectErr is nil; the app dropped deploy.Detect's error")
	}
	assertUnreadableScreen(t, screen(m), dir)
}

// The wording holds at every width the c9 goldens use.
func TestAppModel_UnreadableEnvScreensHoldAtEveryWidth(t *testing.T) {
	dir := unreadableEnvDir(t)
	for _, w := range c9Widths {
		for name, open := range map[string]func(*testing.T, AppModel) AppModel{
			"Remove": openRemove,
			"Update": func(t *testing.T, m AppModel) AppModel { return openUpdate(t, m, nil) },
		} {
			m := NewAppModelNoAnimation().WithoutVolumeCheck()
			m.targetDir = dir
			m = open(t, m)
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: 30})
			s := oneLine(screen(m))
			if !strings.Contains(s, "Couldn't read .env-orbit in") || !strings.Contains(s, "permission denied") ||
				strings.Contains(s, "No Orbit deployment found") {
				t.Errorf("%s at width %d:\n%s", name, w, screen(m))
			}
		}
	}
}

// The dead end stays a dead end: Enter on Menu goes back to the menu.
func TestAppModel_UnreadableEnvScreenMenuRowGoesBack(t *testing.T) {
	dir := unreadableEnvDir(t)
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = dir
	m = openRemove(t, m)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Fatalf("state after Menu = %v, want the splash", m.state)
	}
}

func TestAppModel_SplashOnAnUnreadableEnvSaysItCannotReadIt(t *testing.T) {
	dir := unreadableEnvDir(t)
	probes := 0
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = dir
	m = m.WithDeploymentStatus(func(context.Context, string) bool { probes++; return true })
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})

	if m.splash.state != stateUnreadable {
		t.Errorf("splash state = %v, want stateUnreadable", m.splash.state)
	}
	s := screen(m)
	if strings.Contains(s, "dormant") {
		t.Errorf("the splash calls a deployment it cannot read dormant:\n%s", s)
	}
	if !strings.Contains(s, "can't read .env-orbit") {
		t.Errorf("the splash should say \"can't read .env-orbit\":\n%s", s)
	}
	if m.splash.fqdn != "" {
		t.Errorf("fqdn = %q, want none for a file that could not be read", m.splash.fqdn)
	}
	appMessages(t, m.Init())
	if probes != 0 {
		t.Errorf("the health probe ran %d times; there is no address to probe", probes)
	}
}

// The first Enter must land on the screen that explains, not on an
// Install the engine would refuse.
func TestAppModel_SplashOnAnUnreadableEnvPreselectsUpdateAndEnterExplains(t *testing.T) {
	dir := unreadableEnvDir(t)
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = dir
	m = m.WithDeploymentStatus(nil)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})

	if m.splash.menu.sel != menuUpdate {
		t.Fatalf("selected = %d, want Update (%d) preselected", m.splash.menu.sel, menuUpdate)
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateUpdate {
		t.Fatalf("Enter went to state %v, want the update flow", m.state)
	}
	assertUnreadableScreen(t, screen(m), dir)
}
