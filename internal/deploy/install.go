package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// BuildInstallCommand stages script (install.sh's content) to a temp
// file and returns a ready-to-run command against targetDir, plus a
// cleanup func that removes the staged file — call it once the command
// has finished.
//
// Deliberately not run here, and deliberately leaves Stdin/Stdout/Stderr
// unset: install.sh must see a real controlling terminal, because its
// own scripts/configure.sh is the single source of truth for what
// configuration it needs and how to collect it (guided prompts for
// missing fields, a hidden-input prompt for the OIDC client secret).
// This handoff is the fallback for engines that don't speak the
// machine prompt protocol (see configure.go for the in-console path,
// which keeps the same source of truth — configure.sh runs the
// collection there too). Either way, orbit-launcher never invents a
// field name or validation rule; the handoff runs install.sh exactly
// as if a person had run `curl -fsSL .../install.sh | bash` themselves.
func BuildInstallCommand(script []byte, targetDir string) (cmd *exec.Cmd, cleanup func() error, err error) {
	scriptFile, err := os.CreateTemp("", "orbit-launcher-install-*.sh")
	if err != nil {
		return nil, nil, fmt.Errorf("stage install.sh: %w", err)
	}
	cleanup = func() error { return os.Remove(scriptFile.Name()) }

	if _, err := scriptFile.Write(script); err != nil {
		scriptFile.Close()
		cleanup()
		return nil, nil, fmt.Errorf("stage install.sh: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("stage install.sh: %w", err)
	}

	cmd = exec.Command("bash", scriptFile.Name())
	cmd.Dir = targetDir
	return cmd, cleanup, nil
}

// BuildEngineCommand stages script like BuildInstallCommand but builds
// the mission console's non-interactive engine run instead of a
// terminal handoff: `--plain --<action>`, detached from the controlling
// terminal (Setsid), so the engine's documented non-interactive
// contract engages — it can never prompt, and with incomplete
// configuration it refuses before Compose with a
// reason=configuration-failure event (orbit docs/engine-events.md).
// That refusal is the console's cue for the interactive handoff, which
// still uses BuildInstallCommand unchanged.
//
// A legacy install.sh (orbit main today) parses no arguments at all and
// simply ignores these flags; detached and piped it either completes a
// real run printing prose (which the console displays raw, judging the
// outcome by exit code alone) or hits its own identical
// no-controlling-terminal refusal. Both engines' refusals roll the
// target back via install.sh's own file transaction, verified against
// orbit develop, so the follow-up interactive handoff always starts
// from a clean target.
func BuildEngineCommand(script []byte, targetDir, action string) (cmd *exec.Cmd, cleanup func() error, err error) {
	switch action {
	case "install", "update", "repair":
	default:
		return nil, nil, fmt.Errorf("unknown engine action %q", action)
	}

	cmd, cleanup, err = BuildInstallCommand(script, targetDir)
	if err != nil {
		return nil, nil, err
	}
	cmd.Args = append(cmd.Args, "--plain", "--"+action)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, cleanup, nil
}
