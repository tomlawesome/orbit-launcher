package deploy

// Pins #232 C12 at the level a test can see without naming the helper:
// the argument lists the three Docker queries must hand to docker, and
// that the command StandDown builds is attached (no Setsid) with
// WaitDelay == pipeWaitDelay. These describe today's behaviour and must
// still pass once the queries go through dockerCommand.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// fakeDockerArgs puts a docker on PATH that records each call's arguments,
// one per line with a "--" line closing each call, and returns the log.
func fakeDockerArgs(t *testing.T) (callLog string) {
	t.Helper()
	callLog = filepath.Join(t.TempDir(), "args.log")
	testutil.FakeExecutable(t, "docker", "#!/usr/bin/env bash\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '"+callLog+"'; done\nprintf -- '--\\n' >> '"+callLog+"'\n")
	return callLog
}

func recordedArgs(t *testing.T, callLog string) []string {
	t.Helper()
	b, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) == 0 || lines[len(lines)-1] != "--" {
		t.Fatalf("malformed call log %q", b)
	}
	return lines[:len(lines)-1]
}

func TestDockerQuery_StandDownBuildsAnAttachedBoundedComposeDown(t *testing.T) {
	dir := "/opt/orbit"
	cmd := standDownCommand(context.Background(), dir)

	want := []string{"docker", "compose", "--project-directory", dir, "--env-file", filepath.Join(dir, EnvFile), "down"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
	if cmd.WaitDelay != pipeWaitDelay {
		t.Errorf("WaitDelay = %v, want pipeWaitDelay %v", cmd.WaitDelay, pipeWaitDelay)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid {
		t.Error("a Docker query must stay attached to the launcher's session (no Setsid)")
	}
}

func TestDockerQuery_UnownedVolumesListsByNameWithTheProjectLabel(t *testing.T) {
	callLog := fakeDockerArgs(t)

	UnownedDatabaseVolumes(t.Context(), t.TempDir())

	got := recordedArgs(t, callLog)
	want := []string{
		"volume", "ls",
		"--filter", "name=" + orbitDatabaseVolumePattern,
		"--format", "{{.Name}}\t{{json (.Label \"com.docker.compose.project\")}}",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("docker args = %q, want %q", got, want)
	}
}

func TestDockerQuery_InstalledAtInspectsTheProjectsDatabaseVolume(t *testing.T) {
	callLog := fakeDockerArgs(t)

	InstalledAt(t.Context(), &Deployment{TargetDir: "/opt/orbit", Project: "my-orbit"})

	got := recordedArgs(t, callLog)
	want := []string{"volume", "inspect", "--format", "{{.CreatedAt}}", "my-orbit_" + orbitDatabaseVolumePattern}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("docker args = %q, want %q", got, want)
	}
}
