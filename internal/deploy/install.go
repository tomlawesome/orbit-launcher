package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
)

// BuildInstallCommand stages script (install.sh's content) to a temp
// file and returns a ready-to-run command against targetDir, plus a
// cleanup func that removes what the run staged — call it once the
// run, and any configuration session using its configuration tree, is
// over.
//
// Each run also gets a fresh, private (0700) and empty directory,
// passed to install.sh as ORBIT_LAUNCHER_CONFIG_TREE: on its
// configuration refusal install.sh copies the configuration tree it has
// already verified against the image into it (ai/orbit#1225), and that
// copy is the only configure.sh the launcher ever runs (ConfigTreeDir,
// OpenConfigTree). The cleanup removes it with whatever install.sh
// left there.
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
	if err := checkFolder(targetDir); err != nil {
		return nil, nil, err
	}
	scriptPath, env, cleanup, err := stageInstall(script, "")
	if err != nil {
		return nil, nil, err
	}
	cmd = exec.Command("bash", scriptPath)
	cmd.Dir = targetDir
	cmd.Env = env
	return cmd, cleanup, nil
}

// ErrFolderNameControl is what every engine start returns, wrapped in
// the refusal sentence, for a folder whose name holds a control
// character (checkFolder). A rerun from the same folder fails the same
// way, so a screen offers no "again" for it.
var ErrFolderNameControl = errors.New("the folder's name holds a control character")

// folderNameError is the refusal for one control character: the
// sentence names it as its Go escape, never raw, and says what to do
// (#277 W3). Orbit reads its Compose project name from .env-orbit and
// names its volumes exactly, so renaming the folder keeps the
// deployment.
type folderNameError struct{ r rune }

func (e folderNameError) Error() string {
	return "the folder's name holds a control character (" + strings.Trim(strconv.QuoteRune(e.r), "'") + "), so nothing runs in it: " +
		"rename the folder to a plain name, then run the launcher from there"
}

func (e folderNameError) Is(target error) bool { return target == ErrFolderNameControl }

// checkFolder refuses a folder whose name holds a control character
// (#272): such a name can disturb the engine's --plain output and what
// the screens show, so nothing runs in it. It is the first check of
// every start that runs a script in the folder: Install, Update, and
// RepairCommand (the diagnosis, the safe and dangerous repairs, and
// credential rotation), before anything is read from the folder.
// Format runes such as U+202E are not refused; the screens show them
// escaped. Remove runs nothing there and is not covered.
func checkFolder(dir string) error {
	for _, r := range dir {
		if unicode.IsControl(r) {
			return folderNameError{r}
		}
	}
	return nil
}

// CheckFolderName is checkFolder for a screen that reads the folder
// itself before an engine start (Repair's ownership check, #277 W3):
// the same refusal, so the name is checked once, by one rule.
func CheckFolderName(dir string) error { return checkFolder(dir) }

// stageInstall writes script to a temp file and makes the run's private
// configuration tree directory, seeded from collected when it is set
// (see BuildEngineCommandWithConfig). It returns the script's path, the
// run's environment, and the cleanup that removes both. A seed that
// cannot be copied fails the staging like any other step: nothing is
// left behind and nothing runs.
func stageInstall(script []byte, collected string) (scriptPath string, env []string, cleanup func() error, err error) {
	scriptFile, err := os.CreateTemp("", "orbit-launcher-install-*.sh")
	if err != nil {
		return "", nil, nil, fmt.Errorf("stage install.sh: %w", err)
	}
	removeScript := func() error { return os.Remove(scriptFile.Name()) }

	if _, err := scriptFile.Write(script); err != nil {
		scriptFile.Close()
		removeScript()
		return "", nil, nil, fmt.Errorf("stage install.sh: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		removeScript()
		return "", nil, nil, fmt.Errorf("stage install.sh: %w", err)
	}

	// MkdirTemp creates the directory 0700, owned by this user, and
	// empty — what install.sh insists on before it writes there.
	configTree, err := os.MkdirTemp("", "orbit-launcher-config-*")
	if err != nil {
		removeScript()
		return "", nil, nil, fmt.Errorf("stage configuration tree: %w", err)
	}
	if collected != "" {
		// The guided configuration is handed to the installer here, in
		// its own tree, never written into the deployment folder: the
		// tree is two-way, and on a fresh install the engine writes it
		// through its install transaction, so both rollbacks leave the
		// folder as the person had it (orbit docs/engine-events.md,
		// Non-interactive contract, #1519).
		if err := AdoptConfig(collected, configTree); err != nil {
			removeScript()
			os.RemoveAll(configTree)
			return "", nil, nil, fmt.Errorf("stage configuration tree: %w", err)
		}
	}
	cleanup = func() error {
		return errors.Join(removeScript(), os.RemoveAll(configTree))
	}

	// Appended to the launcher's own environment, never replacing it:
	// install.sh reads ORBIT_CHANNEL, COMPOSE_PROJECT_NAME and the rest
	// from what the person (or CI) set.
	env = append(os.Environ(), ConfigTreeEnv+"="+configTree)
	return scriptFile.Name(), env, cleanup, nil
}

// ConfigTreeDir is the configuration tree directory cmd hands install.sh,
// or "" when it hands over none.
func ConfigTreeDir(cmd *exec.Cmd) string {
	prefix := ConfigTreeEnv + "="
	for i := len(cmd.Env) - 1; i >= 0; i-- {
		if dir, ok := strings.CutPrefix(cmd.Env[i], prefix); ok {
			return dir
		}
	}
	return ""
}

// Action is the engine flag an unattended run is started with, as
// RepairMode, ConfigStep and AuthMode are for their scripts.
type Action string

// The engine actions BuildEngineCommand accepts.
const (
	ActionInstall Action = "install"
	ActionUpdate  Action = "update"
	ActionRepair  Action = "repair"
)

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
func BuildEngineCommand(script []byte, targetDir string, action Action) (cmd *exec.Cmd, cleanup func() error, err error) {
	return BuildEngineCommandWithConfig(script, targetDir, action, "")
}

// BuildEngineCommandWithConfig is BuildEngineCommand whose fresh
// configuration tree is first seeded with the guided configuration the
// launcher collected earlier in its session: collected's .env-orbit
// and, when it has one, .orbit-secrets/, owner-only (AdoptConfig).
// Empty collected seeds nothing, as BuildEngineCommand does.
func BuildEngineCommandWithConfig(script []byte, targetDir string, action Action, collected string) (cmd *exec.Cmd, cleanup func() error, err error) {
	switch action {
	case ActionInstall, ActionUpdate, ActionRepair:
	default:
		return nil, nil, fmt.Errorf("unknown engine action %q", action)
	}
	if err := checkFolder(targetDir); err != nil {
		return nil, nil, err
	}

	scriptPath, env, cleanup, err := stageInstall(script, collected)
	if err != nil {
		return nil, nil, err
	}
	cmd = detachedCommand(context.Background(), targetDir, "bash", scriptPath, "--plain", "--"+string(action))
	cmd.Env = env
	return cmd, cleanup, nil
}
