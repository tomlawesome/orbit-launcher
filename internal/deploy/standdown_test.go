package deploy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestStandDownCommand_PassesEnvFile is the load-bearing test for issue
// #54: every variable the compose file interpolates (ORBIT_IMAGE,
// COMPOSE_PROJECT_NAME, ...) lives in .env-orbit, which Compose never
// auto-loads on its own since it isn't the standard ".env" filename.
// Without --env-file, "docker compose down" fails outright trying to
// interpolate ${ORBIT_IMAGE} — confirmed against a real live
// deployment, not caught by any prior test because nothing ever
// inspected the actual command StandDown builds.
func TestStandDownCommand_PassesEnvFile(t *testing.T) {
	dir := "/opt/orbit"
	cmd := standDownCommand(context.Background(), dir)

	want := filepath.Join(dir, ".env-orbit")
	found := false
	for i, arg := range cmd.Args {
		if arg == "--env-file" {
			found = true
			if i+1 >= len(cmd.Args) || cmd.Args[i+1] != want {
				t.Errorf("--env-file value = %v, want %q", cmd.Args[i+1:], want)
			}
		}
	}
	if !found {
		t.Errorf("expected --env-file in command args, got %v", cmd.Args)
	}
}

func TestStandDownCommand_UsesProjectDirectory(t *testing.T) {
	dir := "/opt/orbit"
	cmd := standDownCommand(context.Background(), dir)

	found := false
	for i, arg := range cmd.Args {
		if arg == "--project-directory" {
			found = true
			if i+1 >= len(cmd.Args) || cmd.Args[i+1] != dir {
				t.Errorf("--project-directory value = %v, want %q", cmd.Args[i+1:], dir)
			}
		}
	}
	if !found {
		t.Errorf("expected --project-directory in command args, got %v", cmd.Args)
	}
}

func TestStandDownCommand_EndsWithDown(t *testing.T) {
	cmd := standDownCommand(context.Background(), "/opt/orbit")
	if len(cmd.Args) == 0 || cmd.Args[len(cmd.Args)-1] != "down" {
		t.Errorf("expected the command to end with \"down\", got %v", cmd.Args)
	}
}

// When docker compose down fails, the error carries docker's own output,
// so the person sees why their containers are still running.
func TestStandDown_FailureCarriesDockersOutput(t *testing.T) {
	fakeDockerPrinting(t, "no such service: orbit\n", 1)

	err := StandDown(t.Context(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error when docker compose down fails")
	}
	if !strings.Contains(err.Error(), "no such service: orbit") {
		t.Errorf("error should carry docker's output, got: %v", err)
	}
}
