package deploy

import (
	"context"
	"strings"
	"testing"
)

// dockerCommand is the one builder for Docker queries (#232 C12): the
// caller's arguments verbatim after "docker", bound to the caller's
// context, pipes bounded by pipeWaitDelay, and attached to the launcher's
// session (that is what separates it from detachedCommand).
func TestDockerCommand_BuildsAnAttachedBoundedDockerCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := dockerCommand(ctx, "volume", "inspect", "--format", "{{.CreatedAt}}", "x_db")

	want := []string{"docker", "volume", "inspect", "--format", "{{.CreatedAt}}", "x_db"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
	if cmd.WaitDelay != pipeWaitDelay {
		t.Errorf("WaitDelay = %v, want pipeWaitDelay %v", cmd.WaitDelay, pipeWaitDelay)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid {
		t.Error("a Docker query must stay attached to the launcher's session (no Setsid)")
	}
	if cmd.Dir != "" {
		t.Errorf("Dir = %q, want the launcher's own directory", cmd.Dir)
	}
}

func TestDockerCommand_WithNoArgumentsIsBareDocker(t *testing.T) {
	cmd := dockerCommand(context.Background())
	if len(cmd.Args) != 1 || cmd.Args[0] != "docker" {
		t.Errorf("args = %q, want just docker", cmd.Args)
	}
}

func TestDockerCommand_IsBoundToTheCallersContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled context stops the command before it starts: Start
	// reports the context's error rather than launching docker.
	if err := dockerCommand(ctx, "version").Start(); err == nil {
		t.Error("Start on a cancelled context succeeded; the command is not bound to ctx")
	}
}
