package ui

// Pins #277 A6: AppModel.WithServicesReader(nil) stops the splash's count,
// as now, and also leaves Repair with no services reader: no services
// block, no `docker compose ps`.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// fakeDocker puts a `docker` first on PATH that records that it was run
// and fails, and returns the marker file it writes.
func fakeDocker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "docker-was-run")
	script := "#!/bin/sh\necho \"$@\" >> '" + marker + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func TestServicesReaderNil_RepairHasNoServicesBlockAndRunsNoDocker(t *testing.T) {
	marker := fakeDocker(t)
	dir := t.TempDir()
	writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
	m := NewAppModelNoAnimation().WithServicesReader(nil)
	m.targetDir = dir
	m = m.WithDeploymentStatus(func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding })
	m.flowSeams = engineRunSeams{prepareRepair: fakeRepairStream(`echo 'diagnosis result=healthy checked=1 skipped=0'; exit 0`)}
	queue := newSink()
	m = m.WithSender(queue.Send)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})

	m = moveCaretTo(t, m, "Repair")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	m = appDrainStream(t, m, queue)
	s := screen(m)
	if !strings.Contains(s, "Diagnosis clear") {
		t.Fatalf("repair did not finish:\n%s", s)
	}
	if strings.Contains(s, "services") || strings.Contains(s, "orbit-db") || strings.Contains(s, "could not check") {
		t.Errorf("Repair shows a services block although the app has no services reader:\n%s", s)
	}
	if _, err := os.Stat(marker); err == nil {
		b, _ := os.ReadFile(marker)
		t.Errorf("docker was run although the app has no services reader: %s", b)
	}
}

// The splash's count stops with a nil reader, whichever order the app's
// options are given in.
func TestServicesReaderNil_TheSplashDoesNotCount(t *testing.T) {
	status := func(context.Context, string) deploy.HealthReason { return deploy.HealthNotResponding }
	orders := map[string]func(AppModel) AppModel{
		"status then reader": func(m AppModel) AppModel { return m.WithDeploymentStatus(status).WithServicesReader(nil) },
		"reader then status": func(m AppModel) AppModel { return m.WithServicesReader(nil).WithDeploymentStatus(status) },
	}
	for name, build := range orders {
		t.Run(name, func(t *testing.T) {
			marker := fakeDocker(t)
			dir := t.TempDir()
			writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
			m := NewAppModelNoAnimation()
			m.targetDir = dir
			m = build(m)
			cmd := m.splash.probeHealthCmd()
			if cmd == nil {
				t.Fatal("the splash has no probe")
			}
			got, ok := cmd().(healthResultMsg)
			if !ok {
				t.Fatal("the probe did not produce a healthResultMsg")
			}
			if got.counted {
				t.Errorf("the splash counted services with no reader: %+v", got)
			}
			if got.reason != deploy.HealthNotResponding {
				t.Errorf("reason = %q, want not responding", got.reason)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Error("docker was run by the splash although the app has no services reader")
			}
		})
	}
}
