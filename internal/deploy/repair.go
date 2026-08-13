package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Repair diagnosis — orbit scripts/repair.sh --check (orbit#261, first
// slice). repair.sh is deliberately standalone and source-less so a
// current copy can diagnose any generation of deployment, but it
// anchors to the tree it lives in (it cds to its own parent-of-scripts
// and delegates configuration readiness to that tree's own
// configure.sh). It isn't a deployment asset, so the launcher fetches
// it fresh — same channel as install.sh — and stages it into the
// target's scripts/ directory before each diagnosis. The diagnosis
// itself is read-only by construction (the script's own contract,
// contract-tested orbit-side); the staged file is the one write, and
// it is overwritten on every run so it can never go stale.

// ErrRepairUnavailable means the configured orbit line doesn't publish
// repair.sh yet (orbit main today): diagnosis honestly isn't available
// rather than being guessed at.
var ErrRepairUnavailable = errors.New("this orbit line doesn't publish the repair diagnosis yet")

// FetchRepairScript downloads the current repair.sh from the same
// source as install.sh (including the CI override).
func FetchRepairScript(ctx context.Context) ([]byte, error) {
	scriptsBase, _ := scriptSourceURLs()
	body, err := fetchFile(ctx, scriptsBase+"/repair.sh")
	if err != nil {
		var status statusError
		if errors.As(err, &status) {
			return nil, ErrRepairUnavailable
		}
		return nil, fmt.Errorf("fetch repair.sh: %w", err)
	}
	if !strings.HasPrefix(string(body), "#!") {
		return nil, ErrRepairUnavailable
	}
	return body, nil
}

// StageRepairScript writes repair.sh into the target's scripts/
// directory, where it diagnoses that installation.
func StageRepairScript(targetDir string, script []byte) error {
	if _, err := os.Stat(targetDir); err != nil {
		return fmt.Errorf("target directory: %w", err)
	}
	scriptsDir := filepath.Join(targetDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return fmt.Errorf("stage repair.sh: %w", err)
	}
	dst := filepath.Join(scriptsDir, "repair.sh")
	if err := os.WriteFile(dst, script, 0o700); err != nil {
		return fmt.Errorf("stage repair.sh: %w", err)
	}
	return os.Chmod(dst, 0o700)
}

// RepairMode selects which read-only repair invocation runs.
type RepairMode string

const (
	// RepairCheck is the diagnosis alone (orbit#261 slice 1+2).
	RepairCheck RepairMode = "--check"
	// RepairPlan is diagnosis plus the classified proposed plan
	// (slice 3) — still zero mutation; execution is a later slice. An
	// older repair.sh rejects it as a usage error (exit 2), which is
	// the caller's cue to fall back to RepairCheck.
	RepairPlan RepairMode = "--plan"
)

// BuildRepairCommand builds one read-only repair run. Detached
// (Setsid) for uniformity with every other engine invocation — nothing
// the launcher spawns may ever reach /dev/tty.
func BuildRepairCommand(targetDir string, mode RepairMode) *exec.Cmd {
	cmd := exec.Command("bash", "scripts/repair.sh", string(mode))
	cmd.Dir = targetDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
