package ui

// A folder Orbit's installer leaves after rolling back a failed fresh
// install holds .env-orbit and .orbit-secrets but no docker-compose.yml:
// nothing is installed there. The splash must not probe the address in
// .env-orbit and call it "degraded" or "alive", nor move the caret off
// Install for Repair (v0.4.3-c audit, G1; #261's relaunch flow).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// rolledBackFolder is the folder a rolled-back fresh install leaves.
func rolledBackFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	if err := os.Mkdir(filepath.Join(dir, ".orbit-secrets"), 0o700); err != nil {
		t.Fatalf("make .orbit-secrets: %v", err)
	}
	return dir
}

// launchAt builds the launcher on dir with a probe answering reason,
// sizes it and runs its launch commands. It returns the app and the
// count of probe calls.
func launchAt(t *testing.T, dir string, reason deploy.HealthReason) (AppModel, *int) {
	t.Helper()
	probes := 0
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = dir
	m = m.WithDeploymentStatus(func(context.Context, string) deploy.HealthReason {
		probes++
		return reason
	})
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m = drive(t, m, m.Init())
	return m, &probes
}

func TestRolledBackInstall_NoProbeAndCaretStaysOnInstall(t *testing.T) {
	m, probes := launchAt(t, rolledBackFolder(t), deploy.HealthNotResponding)
	s := screen(m)
	if *probes != 0 {
		t.Errorf("the probe ran %d times on a folder with nothing installed, want 0", *probes)
	}
	if got := caretRowOf(s); got != "Install" {
		t.Errorf("caret on %q, want Install:\n%s", got, s)
	}
	if !strings.Contains(s, "mail.example.com") {
		t.Errorf("the screen does not name mail.example.com:\n%s", s)
	}
	for _, word := range []string{"degraded", "alive"} {
		if strings.Contains(s, word) {
			t.Errorf("the screen says %q about a folder with nothing installed:\n%s", word, s)
		}
	}
	if m.splash.state == stateDegraded {
		t.Errorf("splash state is degraded for a folder with nothing installed")
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateInstall {
		t.Errorf("Enter led to state %v, want Install:\n%s", m.state, screen(m))
	}
}

func TestRolledBackInstall_StrangerAnsweringIsNotAlive(t *testing.T) {
	m, probes := launchAt(t, rolledBackFolder(t), deploy.HealthResponding)
	s := screen(m)
	if *probes != 0 {
		t.Errorf("the probe ran %d times on a folder with nothing installed, want 0", *probes)
	}
	if strings.Contains(s, "alive") {
		t.Errorf("the screen calls a stranger at the address an alive Orbit:\n%s", s)
	}
	if got := caretRowOf(s); got != "Install" {
		t.Errorf("caret on %q, want Install:\n%s", got, s)
	}
}

func TestRolledBackInstall_ReturnToMenuDoesNotProbe(t *testing.T) {
	m, probes := launchAt(t, rolledBackFolder(t), deploy.HealthNotResponding)
	m, cmd := m.refreshSplash()
	m = drive(t, m, cmd)
	s := screen(m)
	if *probes != 0 {
		t.Errorf("launch and return to menu ran the probe %d times, want 0", *probes)
	}
	if got := caretRowOf(s); got != "Install" {
		t.Errorf("caret on %q after return to menu, want Install:\n%s", got, s)
	}
	if strings.Contains(s, "degraded") {
		t.Errorf("the rebuilt menu says degraded about a folder with nothing installed:\n%s", s)
	}
}

// Guard: with a compose file the folder is an install, so the probe still
// runs and a silent address still reads as degraded with Repair offered.
func TestRolledBackInstall_GuardInstalledFolderStillProbed(t *testing.T) {
	dir := rolledBackFolder(t)
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))
	m, probes := launchAt(t, dir, deploy.HealthNotResponding)
	s := screen(m)
	if *probes != 1 {
		t.Errorf("the probe ran %d times on an installed folder, want 1", *probes)
	}
	if !strings.Contains(s, "degraded") {
		t.Errorf("the screen does not say degraded for a silent install:\n%s", s)
	}
	if got := caretRowOf(s); got != "Repair" {
		t.Errorf("caret on %q, want Repair:\n%s", got, s)
	}
}
