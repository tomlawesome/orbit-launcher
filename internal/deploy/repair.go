package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// ErrNoDeployment means there is no Orbit deployment in the directory
// at all (neither .env-orbit nor scripts/repair.sh): the caller's cue
// for the shared no-deployment screen rather than "diagnosis
// unavailable" (#237).
var ErrNoDeployment = errors.New("no Orbit deployment here (no " + EnvFile + ")")

// ErrRepairUnavailable means the deployment has no scripts/repair.sh —
// it predates the repair diagnosis: diagnosis honestly isn't available
// rather than being guessed at.
var ErrRepairUnavailable = errors.New("this deployment has no repair diagnosis (no scripts/repair.sh)")

// repairScript is repair.sh's name in the deployment's scripts
// directory, where install.sh places it.
const repairScript = "repair.sh"

// RepairCommand builds one repair run against the deployment's own
// scripts/repair.sh. An absent script is ErrNoDeployment when there is
// no .env-orbit either, else ErrRepairUnavailable; one on a path
// RequireTrustedPath refuses is an UntrustedPathError. There is no
// fallback to any other copy.
//
// A trusted scripts/repair.sh runs even without .env-orbit: the
// script's own Step 0 is deliberately looser than Detect (any of
// .env-orbit, docker-compose.yml, .orbit-secrets or installer staging
// counts), so a deployment that lost its env file is diagnosed and
// restored rather than refused, and its exit 5 reaches the same
// no-deployment screen (ui maps engine.ExitNotInstalled).
func RepairCommand(targetDir string, mode RepairMode) (*exec.Cmd, error) {
	// The folder's name before anything else: nothing is read from or
	// run in a folder whose name holds a control character (#277 W3).
	if err := checkFolder(targetDir); err != nil {
		return nil, err
	}
	// The owner rule before the settings are read: another account's
	// folder is refused as such, not as a file it cannot open (#277).
	if err := requireLauncherOwnsIfDir(targetDir); err != nil {
		return nil, err
	}
	d, err := Detect(targetDir)
	if err != nil {
		return nil, fmt.Errorf("read the deployment: %w", err)
	}
	if err := requireScript(targetDir, repairScript, ErrRepairUnavailable); err != nil {
		// No script and no env file is no deployment at all (#237);
		// no script beside an env file is a deployment too old for
		// repair. Any other lookup or trust failure is returned as is.
		if d == nil && errors.Is(err, ErrRepairUnavailable) {
			return nil, ErrNoDeployment
		}
		return nil, err
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

// BuildRepairCommand builds one repair run, detached like every engine
// script run (detachedCommand), so neither repair.sh nor anything it
// starts can reach /dev/tty. Not every child is detached: Docker calls
// stay attached, and the terminal handoff (BuildInstallCommand) must
// reach the terminal. The dangerous mode gets the machine prompt
// transport (orbit#297 grammar, repair's own env var), which is the only
// non-TTY way its confirmation prompts can exist at all.
func BuildRepairCommand(targetDir string, mode RepairMode) *exec.Cmd {
	args := append([]string{filepath.Join(scriptsDir, repairScript)}, strings.Fields(string(mode))...)
	cmd := detachedCommand(context.Background(), targetDir, "bash", args...)
	if mode == RepairExecuteDangerous {
		cmd.Env = append(os.Environ(), "ORBIT_REPAIR_PROMPTS=machine")
	}
	return cmd
}
