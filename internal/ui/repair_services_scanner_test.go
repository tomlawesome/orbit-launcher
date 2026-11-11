package ui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// #345: with the virus scanner off (DOCUMENT_SCAN_MODE=disabled in
// .env-orbit) the launcher no longer expects an orbit-clamav container.

func scannerEnvDir(t *testing.T, profiles string, scan *string) string {
	t.Helper()
	dir := t.TempDir()
	env := "APP_URL=http://localhost\nCOMPOSE_PROFILES=" + profiles + "\n"
	if scan != nil {
		env += "DOCUMENT_SCAN_MODE=" + *scan + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func strp(s string) *string { return &s }

func TestExpectedServices_ScannerDisabledLeavesOutClamav(t *testing.T) {
	for _, c := range []struct {
		profiles string
		want     []string
	}{
		{"", []string{"orbit-db", "orbit-app"}},
		{"full", []string{"orbit-db", "orbit-app", "orbit-tika", "orbit-ollama"}},
	} {
		t.Run("profiles="+c.profiles, func(t *testing.T) {
			got, err := expectedServices(scannerEnvDir(t, c.profiles, strp("disabled")))
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("expectedServices = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestExpectedServices_ScannerOnKeepsClamav(t *testing.T) {
	for name, scan := range map[string]*string{
		"unset":    nil,
		"empty":    strp(""),
		"enabled":  strp("enabled"),
		"required": strp("required"),
	} {
		t.Run(name, func(t *testing.T) {
			want := []string{"orbit-db", "orbit-app", "orbit-clamav"}
			got, err := expectedServices(scannerEnvDir(t, "", scan))
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("expectedServices = %v, %v; want %v", got, err, want)
			}
		})
	}
}

// The splash: scanner off, health ready, db and app running, no
// orbit-clamav container: alive, no faults.
func TestSplashProbe_ScannerOffIsAliveNotDegraded(t *testing.T) {
	m, dir := probeModel(t, "")
	if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile),
		[]byte("APP_URL=http://localhost\nDOCUMENT_SCAN_MODE=disabled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) { return allRunning(2), nil }
	msg, ok := m.probeHealthCmd()().(healthResultMsg)
	if !ok {
		t.Fatal("not a healthResultMsg")
	}
	if len(msg.faults) != 0 || !msg.counted || msg.running != 2 || msg.expected != 2 {
		t.Errorf("message = %+v, want 2 of 2 counted and no faults", msg)
	}
	if got := degradedSplash(t, msg).state; got != stateAlive {
		t.Errorf("state = %v, want alive", got)
	}
}
