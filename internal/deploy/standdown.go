package deploy

import (
	"context"
	"fmt"
	"os/exec"
)

// StandDown stops a deployment's containers and network — the safe,
// reversible half of Remove. It deliberately never passes -v: data
// volumes are left untouched, matching the "your files and data volumes
// are still on disk" claim in design/mockups.html section 11.
func StandDown(ctx context.Context, targetDir string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "--project-directory", targetDir, "down")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose down: %w: %s", err, out)
	}
	return nil
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
