package deploy

import (
	"os/exec"
	"strings"
	"testing"
)

func movedEntries(cmd *exec.Cmd) []string {
	var got []string
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, FolderMovedEnv+"=") {
			got = append(got, e)
		}
	}
	return got
}

func TestWithFolderMoved_OnlyTheGivenValueSurvives(t *testing.T) {
	newCmd := func() *exec.Cmd {
		c := exec.Command("true")
		c.Env = []string{"KEEP=1", FolderMovedEnv + "=recreate"}
		return c
	}
	c := newCmd()
	WithFolderMoved(c, "")
	if got := movedEntries(c); len(got) != 0 {
		t.Errorf("empty value left %q", got)
	}
	if !strings.Contains(strings.Join(c.Env, "\n"), "KEEP=1") {
		t.Errorf("empty value dropped KEEP: %q", c.Env)
	}
	c = newCmd()
	WithFolderMoved(c, "recreate")
	if got := movedEntries(c); len(got) != 1 || got[0] != FolderMovedEnv+"=recreate" {
		t.Errorf("got %q, want exactly one =recreate", got)
	}
}
