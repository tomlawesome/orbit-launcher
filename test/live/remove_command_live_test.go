//go:build live

package live

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// removeCommandFor is the command Remove offers for dir as it stands now,
// built the way the done screen builds it, with "sudo " dropped when the
// runner has no passwordless sudo (the files are then the runner's own).
func removeCommandFor(t *testing.T, dir string) string {
	t.Helper()
	own, err := deploy.OwnFiles(dir)
	if err != nil {
		t.Fatalf("OwnFiles(%s): %v", dir, err)
	}
	if own.ManifestIgnored != nil {
		t.Fatalf("the installed Orbit's .orbit-files was refused: %v", own.ManifestIgnored)
	}
	if len(own.Names) == 0 {
		t.Fatalf("the installed Orbit has no usable .orbit-files (ai/orbit#1496), so Remove would offer the whole-folder command")
	}
	cmd, err := deploy.RemovalCommand(dir, own)
	if err != nil {
		t.Fatalf("RemovalCommand: %v", err)
	}
	if exec.Command("sudo", "-n", "true").Run() != nil {
		cmd = strings.ReplaceAll(cmd, "sudo ", "")
	}
	return cmd
}

// runRemoveCommand runs the offered command for real. The environment pins
// the Compose project to this run's, as dockerComposeDown does, because the
// command carries down -v.
func runRemoveCommand(t *testing.T, dir, cmd string) (string, error) {
	t.Helper()
	c := exec.Command("bash", "-c", cmd)
	c.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+liveProjectName(dir))
	out, err := c.CombinedOutput()
	return string(out), err
}

func entriesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// assertRemoveCommandOnRealDeployment runs after Remove stood the stack
// down (#285). Case two runs on a copy of the deployment with backups/a.tar
// and notes.txt planted; case one then runs on the deployment itself.
func assertRemoveCommandOnRealDeployment(t *testing.T, dir string) {
	t.Helper()
	copyDir := dir + "-with-backups"
	if out, err := exec.Command("cp", "-a", dir, copyDir).CombinedOutput(); err != nil {
		t.Fatalf("copying the deployment: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = os.RemoveAll(copyDir) })

	t.Run("leftovers survive and rmdir names the folder", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(copyDir, "backups"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"backups/a.tar", "notes.txt"} {
			if err := os.WriteFile(filepath.Join(copyDir, f), []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := removeCommandFor(t, copyDir)
		out, err := runRemoveCommand(t, copyDir, cmd)
		if err == nil {
			t.Errorf("the command succeeded, but rmdir must refuse a folder that still holds files:\n%s", out)
		}
		if !strings.Contains(out, "rmdir") || !strings.Contains(out, copyDir) {
			t.Errorf("output does not show rmdir naming %s:\n%s", copyDir, out)
		}
		want := []string{"backups", "backups/a.tar", "notes.txt"}
		if got := entriesUnder(t, copyDir); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("after the command the folder holds %q, want exactly %q\ncommand: %s\noutput:\n%s", got, want, cmd, out)
		}
	})

	t.Run("folder is gone", func(t *testing.T) {
		cmd := removeCommandFor(t, dir)
		out, err := runRemoveCommand(t, dir, cmd)
		if err != nil {
			t.Errorf("the command failed: %v\n%s\ncommand: %s", err, out, cmd)
		}
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("the folder is still there (stat: %v); left: %q\n%s", statErr, entriesUnder(t, dir), out)
		}
	})
}
