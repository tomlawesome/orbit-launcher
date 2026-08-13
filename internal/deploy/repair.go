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

// RepairMode selects which repair invocation runs.
type RepairMode string

const (
	// RepairCheck is the read-only diagnosis alone (orbit#261
	// slices 1+2).
	RepairCheck RepairMode = "--check"
	// RepairPlan is diagnosis plus the classified proposed plan
	// (slice 3) — still zero mutation. An older repair.sh rejects it
	// as a usage error (exit 2), which is the caller's cue to fall
	// back to RepairCheck.
	RepairPlan RepairMode = "--plan"
	// RepairExecuteSafe runs the safe batch (slice 4 stage 1):
	// fix-permissions, restore-transaction, restart-services — every
	// action reversible, per-file backups, full re-diagnosis after.
	// Piped and detached this takes the script's documented unattended
	// path: --safe-only is itself the automation opt-in, and the
	// person's explicit menu choice is the consent that path expects
	// the caller to have obtained.
	RepairExecuteSafe RepairMode = "--execute --safe-only"
	// RepairExecuteDangerous is the guarded database-credential
	// rotation (slice 4 stage 2). Never unattended by the script's own
	// contract: it must run with ORBIT_REPAIR_PROMPTS=machine (see
	// BuildRepairCommand) and be driven over stdin through the typed
	// action word and checkpoint passphrase prompts, or it refuses
	// with exit 6.
	RepairExecuteDangerous RepairMode = "--execute --dangerous"
)

// BuildRepairCommand builds one repair run. Detached (Setsid) for
// uniformity with every other engine invocation — nothing the launcher
// spawns may ever reach /dev/tty. The dangerous mode gets the machine
// prompt transport (orbit#297 grammar, repair's own env var), which is
// the only non-TTY way its confirmation prompts can exist at all.
func BuildRepairCommand(targetDir string, mode RepairMode) *exec.Cmd {
	args := append([]string{"scripts/repair.sh"}, strings.Fields(string(mode))...)
	cmd := exec.Command("bash", args...)
	cmd.Dir = targetDir
	if mode == RepairExecuteDangerous {
		cmd.Env = append(os.Environ(), "ORBIT_REPAIR_PROMPTS=machine")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
