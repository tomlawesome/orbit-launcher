package deploy

// #351 item 2: the file-by-file Remove command is safe to paste again.
// Its compose step runs only while .env-orbit is still there, so a second
// paste (after .env-orbit is gone) goes on to the remaining rm/rmdir steps.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const guardedDown = "[ ! -e .env-orbit ] || docker compose --env-file .env-orbit down -v"

func TestRemovalCommand351_ComposeStepIsGuardedByEnvOrbitInsideTheSubshell(t *testing.T) {
	dir := standardTree(t)
	got := mustRemovalCommand2(t, dir, mustOwn(t, dir))
	q := shellQuote(dir)
	if want := "(cd " + q + " && " + guardedDown + " && sudo rm -rf -- "; !strings.HasPrefix(got, want) {
		t.Errorf("command does not start with the guarded compose step\n got: %s\nwant prefix: %s", got, want)
	}
	// The rest of the chain is unchanged.
	if !strings.Contains(got, " && sudo rmdir -- config scripts) && sudo rmdir -- "+q) {
		t.Errorf("the rmdir tail changed:\n%s", got)
	}
	if strings.Count(got, "docker compose") != 1 {
		t.Errorf("want exactly one compose step:\n%s", got)
	}
}

// pathWith writes executable scripts into a fresh bin dir and returns the
// PATH that puts it first.
func pathWith(t *testing.T, scripts map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

func runRemoval351(t *testing.T, cmd, path string) error {
	t.Helper()
	c := exec.Command("bash", "-c", cmd)
	c.Env = append(os.Environ(), "PATH="+path)
	if out, err := c.CombinedOutput(); err != nil {
		t.Logf("command output:\n%s", out)
		return err
	}
	return nil
}

func TestRemovalCommand351_SecondPasteWithoutEnvOrbitStillRemovesTheRest(t *testing.T) {
	log := filepath.Join(t.TempDir(), "docker.log")
	path := pathWith(t, map[string]string{
		"docker": `printf '%s\n' "$*" >> ` + log + "\n",
		"sudo":   `exec "$@"` + "\n",
	})
	dir := standardTree(t)
	cmd := mustRemovalCommand2(t, dir, mustOwn(t, dir))

	// With .env-orbit present, the compose step runs as it always did.
	probe := standardTree(t)
	if err := runRemoval351(t, mustRemovalCommand2(t, probe, mustOwn(t, probe)), path); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "compose --env-file .env-orbit down -v" {
		t.Errorf("docker saw %q on the first run, want one 'compose --env-file .env-orbit down -v'", b)
	}
	if _, err := os.Stat(probe); !os.IsNotExist(err) {
		t.Errorf("first run left %s behind", probe)
	}

	// The paste after .env-orbit is gone: no compose step, the rest still goes.
	if err := os.Remove(filepath.Join(dir, ".env-orbit")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := runRemoval351(t, cmd, path); err != nil {
		t.Errorf("second run failed: %v", err)
	}
	if b, err := os.ReadFile(log); err == nil {
		t.Errorf("docker was run without .env-orbit: %q", b)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("second run left %s behind", dir)
	}
}
