package ui

import (
	"context"
	"io"
	"os/exec"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

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

// prepareInstallCmd wraps prepare as a tea.Cmd; cancelling ctx (Ctrl-C)
// stops the fetch.
func prepareInstallCmd(ctx context.Context, prepare func(context.Context, string) (*exec.Cmd, func() error, error), targetDir string) tea.Cmd {
	return func() tea.Msg {
		cmd, cleanup, err := prepare(ctx, targetDir)
		return installPreparedMsg{cmd: cmd, cleanup: cleanup, err: err}
	}
}

// defaultRunHandoff hands the real terminal to cmd via tea.Exec, which
// suspends this program's rendering, runs cmd with the real
// stdin/stdout/stderr, and resumes once it exits.
func defaultRunHandoff(cmd *exec.Cmd) tea.Cmd {
	return tea.Exec(&handoffCommand{Cmd: cmd}, func(err error) tea.Msg {
		return installFinishedMsg{err: err}
	})
}

// terminalHandedOver is set while install.sh holds the real terminal.
var terminalHandedOver atomic.Bool

// TerminalHandedOver reports whether install.sh holds the real terminal
// in the handoff. A SIGINT then is the person's Ctrl-C to install.sh,
// which shares the launcher's process group, not one to the launcher.
func TerminalHandedOver() bool { return terminalHandedOver.Load() }

// handoffCommand is tea.ExecProcess's command wrapper, marking the time
// install.sh holds the terminal (TerminalHandedOver).
type handoffCommand struct{ *exec.Cmd }

func (c *handoffCommand) Run() error {
	terminalHandedOver.Store(true)
	defer terminalHandedOver.Store(false)
	return c.Cmd.Run()
}

// The setters leave a stream the command already has, as ExecProcess's do.

func (c *handoffCommand) SetStdin(r io.Reader) {
	if c.Stdin == nil {
		c.Stdin = r
	}
}

func (c *handoffCommand) SetStdout(w io.Writer) {
	if c.Stdout == nil {
		c.Stdout = w
	}
}

func (c *handoffCommand) SetStderr(w io.Writer) {
	if c.Stderr == nil {
		c.Stderr = w
	}
}
