package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDockerPrinting puts a docker on PATH that ignores its arguments and
// prints stdout verbatim, so the parser is exercised against the exact
// shape `docker volume ls --format` really emits.
func fakeDockerPrinting(t *testing.T, stdout string, exitCode int) {
	t.Helper()
	binDir := t.TempDir()
	script := fmt.Sprintf("#!/usr/bin/env bash\nprintf '%%b' %q\nexit %d\n", stdout, exitCode)
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestUnownedDatabaseVolumes_ReadsNameAndProjectVerbatim(t *testing.T) {
	fakeDockerPrinting(t, "orbit_orbit-db-data\torbit\nold-tree_orbit-db-data\told-tree\n", 0)

	got := UnownedDatabaseVolumes(t.Context(), t.TempDir())
	if len(got) != 2 {
		t.Fatalf("expected 2 volumes, got %d: %+v", len(got), got)
	}
	if got[0].Name != "orbit_orbit-db-data" || got[0].Project != "orbit" {
		t.Errorf("first volume = %+v", got[0])
	}
	if got[1].Name != "old-tree_orbit-db-data" || got[1].Project != "old-tree" {
		t.Errorf("second volume = %+v", got[1])
	}
}

// An unlabelled volume still blocks the install, so it must still be
// reported — with an empty project rather than an invented one.
func TestUnownedDatabaseVolumes_UnlabelledVolumeHasNoProject(t *testing.T) {
	fakeDockerPrinting(t, "orbit-db-data\t\n", 0)

	got := UnownedDatabaseVolumes(t.Context(), t.TempDir())
	if len(got) != 1 {
		t.Fatalf("expected 1 volume, got %+v", got)
	}
	if got[0].Name != "orbit-db-data" || got[0].Project != "" {
		t.Errorf("volume = %+v, want the name with an empty project", got[0])
	}
}

// A recognised deployment owns its own volume: that is not a surprise
// and not a blocker, so the pre-flight has nothing to say.
func TestUnownedDatabaseVolumes_SilentWhenTheTargetHasADeployment(t *testing.T) {
	fakeDockerPrinting(t, "orbit_orbit-db-data\torbit\n", 0)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte("APP_URL=https://orbit.example.com\n"), 0o600); err != nil {
		t.Fatalf("write .env-orbit: %v", err)
	}

	if got := UnownedDatabaseVolumes(t.Context(), dir); got != nil {
		t.Errorf("expected silence for a recognised deployment, got %+v", got)
	}
}

// The whole check is advisory: it explains a refusal the engine will make
// again on its own. A detection problem must never be able to stand
// between someone and an install, so every failure is silence.
func TestUnownedDatabaseVolumes_FailureIsSilence(t *testing.T) {
	t.Run("docker exits non-zero", func(t *testing.T) {
		fakeDockerPrinting(t, "Cannot connect to the Docker daemon\n", 1)
		if got := UnownedDatabaseVolumes(t.Context(), t.TempDir()); got != nil {
			t.Errorf("expected silence, got %+v", got)
		}
	})

	t.Run("no docker on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got := UnownedDatabaseVolumes(t.Context(), t.TempDir()); got != nil {
			t.Errorf("expected silence, got %+v", got)
		}
	})

	t.Run("no volumes", func(t *testing.T) {
		fakeDockerPrinting(t, "", 0)
		if got := UnownedDatabaseVolumes(t.Context(), t.TempDir()); got != nil {
			t.Errorf("expected silence, got %+v", got)
		}
	})
}

// This mirrors the engine's own machine-wide check; matching on a
// different string would mean explaining a refusal that never happens, or
// staying quiet about one that does. It also pins the call to listing —
// the pre-flight reads, it never removes.
func TestUnownedDatabaseVolumes_ListsOnTheEnginesOwnPattern(t *testing.T) {
	binDir := t.TempDir()
	callLog := filepath.Join(binDir, "calls.log")
	writeFakeDocker(t, binDir, callLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	UnownedDatabaseVolumes(t.Context(), t.TempDir())

	logged, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	calls := string(logged)

	if want := "name=" + orbitDatabaseVolumePattern; !strings.Contains(calls, want) {
		t.Errorf("expected the filter %q in the docker call, got %q", want, calls)
	}
	if !strings.Contains(calls, "volume ls") {
		t.Errorf("expected a volume listing, got %q", calls)
	}
	for _, arg := range strings.Fields(calls) {
		// Whole arguments, not substrings: "--format" contains "rm".
		if arg == "rm" || arg == "prune" || arg == "-f" {
			t.Errorf("the pre-flight must only ever list volumes, got %q in %q", arg, calls)
		}
	}
}
