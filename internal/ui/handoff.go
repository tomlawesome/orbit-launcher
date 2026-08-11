package ui

import (
	"context"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// installPreparedMsg carries the result of fetching install.sh and
// staging it as a runnable command — a plain network+disk step that
// needs no terminal access, so it runs as an ordinary tea.Cmd.
type installPreparedMsg struct {
	cmd     *exec.Cmd
	cleanup func() error
	err     error
}

// installFinishedMsg carries the result of actually running install.sh
// with a real controlling terminal handed to it.
type installFinishedMsg struct{ err error }

// defaultPrepareInstall fetches the current install.sh and stages it as
// a runnable command. Both Install and Update use exactly this — see
// internal/deploy.BuildInstallCommand for why orbit-launcher never
// collects or writes configuration itself.
func defaultPrepareInstall(ctx context.Context, targetDir string) (*exec.Cmd, func() error, error) {
	script, err := deploy.FetchInstallScript(ctx)
	if err != nil {
		return nil, nil, err
	}
	return deploy.BuildInstallCommand(script, targetDir)
}

// prepareInstallCmd wraps prepare as a tea.Cmd.
func prepareInstallCmd(prepare func(context.Context, string) (*exec.Cmd, func() error, error), targetDir string) tea.Cmd {
	return func() tea.Msg {
		cmd, cleanup, err := prepare(context.Background(), targetDir)
		return installPreparedMsg{cmd: cmd, cleanup: cleanup, err: err}
	}
}

// defaultRunHandoff hands the real terminal to cmd via tea.ExecProcess,
// which suspends this program's rendering, runs cmd with the real
// stdin/stdout/stderr, and resumes once it exits.
func defaultRunHandoff(cmd *exec.Cmd) tea.Cmd {
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return installFinishedMsg{err: err}
	})
}
