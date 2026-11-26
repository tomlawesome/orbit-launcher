package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Maintenance mode from the launcher (#242): the deployment's own
// scripts/start-maintenance.sh and scripts/end-maintenance.sh, run the
// way Repair runs repair.sh. Nothing is fetched or written; a deployment
// without the scripts is an Orbit too old to ship them, said honestly.

// ErrMaintenanceUnavailable means the deployment lacks the maintenance
// script the run needs: an Orbit from before it shipped them.
var ErrMaintenanceUnavailable = errors.New("this deployment has no maintenance commands (no scripts/start-maintenance.sh or end-maintenance.sh)")

const (
	startMaintenanceScript = "start-maintenance.sh"
	endMaintenanceScript   = "end-maintenance.sh"
)

// MaintenanceCommand builds one maintenance run. start selects
// start-maintenance.sh (with --notice and --until when non-empty; each
// value is one exec argument, never a shell) or end-maintenance.sh
// (notice and until ignored). Errors as RepairCommand's.
func MaintenanceCommand(targetDir string, start bool, notice, until string) (*exec.Cmd, error) {
	if err := checkFolder(targetDir); err != nil {
		return nil, err
	}
	if err := requireLauncherOwnsIfDir(targetDir); err != nil {
		return nil, err
	}
	d, err := Detect(targetDir)
	if err != nil {
		return nil, fmt.Errorf("read the deployment: %w", err)
	}
	script := endMaintenanceScript
	if start {
		script = startMaintenanceScript
	}
	if err := requireScript(targetDir, script, ErrMaintenanceUnavailable); err != nil {
		if d == nil && errors.Is(err, ErrMaintenanceUnavailable) {
			return nil, ErrNoDeployment
		}
		return nil, err
	}
	args := []string{filepath.Join(scriptsDir, script)}
	if start {
		if notice != "" {
			args = append(args, "--notice", notice)
		}
		if until != "" {
			args = append(args, "--until", until)
		}
	}
	return detachedCommand(context.Background(), targetDir, "bash", args...), nil
}

// MaintenanceScripts reports which maintenance scripts exist in
// targetDir/scripts: start-maintenance.sh and end-maintenance.sh (Lstat
// only; trust is judged by MaintenanceCommand).
func MaintenanceScripts(targetDir string) (start, end bool) {
	has := func(name string) bool {
		_, err := os.Lstat(filepath.Join(targetDir, scriptsDir, name)) //nolint:gosec // G703: existence check only
		return err == nil
	}
	return has(startMaintenanceScript), has(endMaintenanceScript)
}

// HasMaintenanceScripts reports whether both maintenance scripts exist.
func HasMaintenanceScripts(targetDir string) bool {
	start, end := MaintenanceScripts(targetDir)
	return start && end
}
