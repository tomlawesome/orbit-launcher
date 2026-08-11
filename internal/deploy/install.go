package deploy

import (
	"fmt"
	"os"
	"os/exec"
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
// missing fields, a hidden-input prompt for the OIDC client secret —
// the secret path has no non-interactive form at all, by design, per
// configure.sh's set_oidc_secret). orbit-launcher must not — and after
// this change, does not — know a single field name orbit's config
// requires; it only ever hands its real terminal to install.sh via
// tea.ExecProcess (see internal/ui) and waits for it to finish, exactly
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
