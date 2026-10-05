package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rewriting .env-orbit (reconfiguration, repair) must not move the
// install date: it comes from when Docker created the database volume.
func TestInstalledAt_ComesFromTheDatabaseVolumeNotTheEnvFile(t *testing.T) {
	fakeDockerPrinting(t, "2026-01-10T09:00:00+00:00\n", 0)

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env-orbit")
	if err := os.WriteFile(envPath, []byte("APP_URL=https://mail.example.com\n"), 0o600); err != nil {
		t.Fatalf("write .env-orbit: %v", err)
	}
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat .env-orbit: %v", err)
	}
	d, err := Detect(dir)
	if err != nil || d == nil {
		t.Fatalf("Detect = %+v, %v", d, err)
	}

	got := InstalledAt(t.Context(), d)
	want := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("InstalledAt = %v, want the volume's creation time %v", got, want)
	}
	if got.Format("2006-01-02") == info.ModTime().Format("2006-01-02") {
		t.Errorf("InstalledAt = %v is the .env-orbit write date, not the volume's", got)
	}
}

// The date is read from this deployment's own database volume, and the
// lookup only ever inspects: it never removes anything.
func TestInstalledAt_InspectsThisDeploymentsDatabaseVolume(t *testing.T) {
	binDir := t.TempDir()
	callLog := filepath.Join(binDir, "calls.log")
	writeFakeDocker(t, binDir, callLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	InstalledAt(t.Context(), &Deployment{TargetDir: "/opt/orbit", Project: "my-orbit"})

	logged, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	calls := string(logged)
	if !strings.Contains(calls, "volume inspect") {
		t.Errorf("expected a volume inspect, got %q", calls)
	}
	if want := "my-orbit_" + orbitDatabaseVolumePattern; !strings.Contains(calls, want) {
		t.Errorf("expected the volume %q in the docker call, got %q", want, calls)
	}
	for _, arg := range strings.Fields(calls) {
		// Whole arguments, not substrings: "--format" contains "rm".
		if arg == "rm" || arg == "prune" || arg == "-f" {
			t.Errorf("the install-date lookup must only inspect, got %q in %q", arg, calls)
		}
	}
}

// When Docker cannot say, the date is unknown — never a guess.
func TestInstalledAt_UnknownWhenDockerCannotSay(t *testing.T) {
	d := &Deployment{TargetDir: "/opt/orbit", Project: "orbit"}

	t.Run("no such volume", func(t *testing.T) {
		fakeDockerPrinting(t, "Error response from daemon: get orbit_orbit-db-data: no such volume\n", 1)
		if got := InstalledAt(t.Context(), d); !got.IsZero() {
			t.Errorf("InstalledAt = %v, want unknown", got)
		}
	})

	t.Run("not a date", func(t *testing.T) {
		fakeDockerPrinting(t, "not a date\n", 0)
		if got := InstalledAt(t.Context(), d); !got.IsZero() {
			t.Errorf("InstalledAt = %v, want unknown", got)
		}
	})

	t.Run("empty output", func(t *testing.T) {
		fakeDockerPrinting(t, "", 0)
		if got := InstalledAt(t.Context(), d); !got.IsZero() {
			t.Errorf("InstalledAt = %v, want unknown", got)
		}
	})

	t.Run("no docker on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got := InstalledAt(t.Context(), d); !got.IsZero() {
			t.Errorf("InstalledAt = %v, want unknown", got)
		}
	})

	t.Run("nil deployment", func(t *testing.T) {
		fakeDockerPrinting(t, "2026-01-10T09:00:00+00:00\n", 0)
		if got := InstalledAt(t.Context(), nil); !got.IsZero() {
			t.Errorf("InstalledAt = %v, want unknown", got)
		}
	})
}
