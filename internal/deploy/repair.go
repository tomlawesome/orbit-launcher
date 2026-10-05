package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Repair diagnosis — orbit scripts/repair.sh --check (orbit#261, first
// slice). The launcher runs the deployment's own scripts/repair.sh: the
// copy install.sh placed there from the digest-pinned image, so it came
// through the same verified channel as the rest of the deployment
// (#190). Nothing is fetched and nothing is written — a deployment
// without the script simply has no diagnosis, said honestly. repair.sh
// anchors to the tree it lives in (it cds to its own parent-of-scripts
// and delegates configuration readiness to that tree's own
// configure.sh), and its diagnosis is read-only by construction (the
// script's own contract, contract-tested orbit-side).

// ErrRepairUnavailable means the deployment has no scripts/repair.sh —
// it predates the repair diagnosis, or there is no deployment here:
// diagnosis honestly isn't available rather than being guessed at.
var ErrRepairUnavailable = errors.New("this deployment has no repair diagnosis (no scripts/repair.sh)")

// repairScript is where install.sh places repair.sh in a deployment.
const repairScript = "scripts/repair.sh"

// RepairCommand builds one repair run against the deployment's own
// scripts/repair.sh. An absent script is ErrRepairUnavailable; there is
// no fallback to any other copy.
func RepairCommand(targetDir string, mode RepairMode) (*exec.Cmd, error) {
	if _, err := os.Lstat(filepath.Join(targetDir, repairScript)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrRepairUnavailable
		}
		return nil, fmt.Errorf("repair.sh: %w", err)
	}
	return BuildRepairCommand(targetDir, mode), nil
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
	args := append([]string{repairScript}, strings.Fields(string(mode))...)
	cmd := exec.Command("bash", args...)
	cmd.Dir = targetDir
	if mode == RepairExecuteDangerous {
		cmd.Env = append(os.Environ(), "ORBIT_REPAIR_PROMPTS=machine")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
