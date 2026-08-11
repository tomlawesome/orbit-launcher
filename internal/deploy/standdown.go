package deploy

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

// StandDown stops a deployment's containers and network — the safe,
// reversible half of Remove. It deliberately never passes -v: data
// volumes are left untouched, matching the "your files and data volumes
// are still on disk" claim in design/mockups.html section 11.
//
// --env-file is required, not optional: every variable the compose file
// interpolates (ORBIT_IMAGE, COMPOSE_PROJECT_NAME, ...) lives in
// .env-orbit, a non-standard filename Compose never auto-loads on its
// own — install.sh's own compose() helper always passes it explicitly
// for exactly this reason. Without it, "docker compose down" fails
// outright trying to interpolate ${ORBIT_IMAGE}, discovered via a real
// live deployment (issue #54) that unit tests mocking StandDown could
// never have caught.
func StandDown(ctx context.Context, targetDir string) error {
	cmd := standDownCommand(ctx, targetDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose down: %w: %s", err, out)
	}
	return nil
}

// standDownCommand builds the exact command StandDown runs — separated
// out so its arguments (not just its behaviour once actually executed
// against a real deployment) are directly, cheaply testable.
func standDownCommand(ctx context.Context, targetDir string) *exec.Cmd {
	envFile := filepath.Join(targetDir, ".env-orbit")
	return exec.CommandContext(ctx, "docker", "compose",
		"--project-directory", targetDir, "--env-file", envFile, "down")
}

// RemovalCommand returns the exact, copy-pasteable shell command that
// fully and irreversibly removes an Orbit deployment — including its data
// volumes and every file in targetDir. This package never executes it:
// see removal_property_test.go, which asserts that as a real, checked
// property, not just a comment someone could quietly invalidate later.
func RemovalCommand(targetDir string) string {
	return fmt.Sprintf(
		"docker compose --project-directory %s down -v && sudo rm -rf %s",
		targetDir, targetDir,
	)
}
