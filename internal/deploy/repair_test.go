package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// deploymentWithRepair is a deployment directory holding the
// repair.sh install.sh placed there from the image.
func deploymentWithRepair(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	scriptTree(t, dir, 0o755, testutil.File{Path: "scripts/repair.sh", Body: script, Mode: 0o700})
	return dir
}

func TestRepairCommand_RunsTheDeploymentsOwnScript(t *testing.T) {
	requests := testutil.CountingScriptSource(t)
	const script = "#!/usr/bin/env bash\necho installed-from-the-image\n"
	dir := deploymentWithRepair(t, script)

	cmd, err := RepairCommand(dir, RepairPlan)
	if err != nil {
		t.Fatalf("RepairCommand: %v", err)
	}
	want := []string{"bash", "scripts/repair.sh", "--plan"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %v, want %v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", cmd.Args, want)
		}
	}
	if cmd.Dir != dir {
		t.Errorf("dir = %q, want the deployment %q", cmd.Dir, dir)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("preparing a repair made %d HTTP requests; it must fetch nothing", n)
	}
	// The launcher never writes repair.sh: the deployment's copy is
	// exactly what install.sh put there.
	if got, _ := os.ReadFile(filepath.Join(dir, "scripts", "repair.sh")); string(got) != script {
		t.Errorf("repair.sh was changed: %q", got)
	}
}

func TestRepairCommand_AbsentScriptIsUnavailable(t *testing.T) {
	requests := testutil.CountingScriptSource(t)
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile)) // a deployment, but one without repair.sh

	cmd, err := RepairCommand(dir, RepairCheck)
	if !errors.Is(err, ErrRepairUnavailable) {
		t.Fatalf("err = %v, want ErrRepairUnavailable", err)
	}
	if cmd != nil {
		t.Error("an unavailable repair returned a command")
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("an absent repair.sh made %d HTTP requests; there is no fallback fetch", n)
	}
	if _, err := os.Lstat(filepath.Join(dir, "scripts")); !os.IsNotExist(err) {
		t.Error("nothing may be written into the deployment when repair.sh is absent")
	}
}

// A deployment directory with an env file but no scripts/ directory at
// all is a deployment from before repair.sh, not a missing deployment.
func TestRepairCommand_MissingScriptsDirectoryIsUnavailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nowhere")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	_, err := RepairCommand(dir, RepairCheck)
	if !errors.Is(err, ErrRepairUnavailable) {
		t.Fatalf("err = %v, want ErrRepairUnavailable", err)
	}
}

// #237: a directory with no Orbit deployment in it is not "this
// deployment is too old for repair": every repair start says so.
func TestRepairCommand_EmptyDirectoryIsNoDeployment(t *testing.T) {
	requests := testutil.CountingScriptSource(t)
	dir := t.TempDir()

	for _, mode := range []RepairMode{RepairCheck, RepairPlan, RepairExecuteSafe, RepairExecuteDangerous} {
		cmd, err := RepairCommand(dir, mode)
		if !errors.Is(err, ErrNoDeployment) {
			t.Errorf("mode %v: err = %v, want ErrNoDeployment", mode, err)
		}
		if errors.Is(err, ErrRepairUnavailable) {
			t.Errorf("mode %v: an empty directory was reported as repair being unavailable", mode)
		}
		if cmd != nil {
			t.Errorf("mode %v: a directory with no deployment returned a command", mode)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("made %d HTTP requests; it must fetch nothing", n)
	}
	if _, err := os.Lstat(filepath.Join(dir, "scripts")); !os.IsNotExist(err) {
		t.Error("nothing may be written into a directory with no deployment")
	}
}

// #237: a target directory that does not exist is "no deployment", the
// same as an empty one.
func TestRepairCommand_MissingTargetDirectoryIsNoDeployment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	cmd, err := RepairCommand(dir, RepairCheck)
	if !errors.Is(err, ErrNoDeployment) {
		t.Fatalf("err = %v, want ErrNoDeployment", err)
	}
	if cmd != nil {
		t.Error("a missing directory returned a command")
	}
}

// Owner decision 19a (2026-10-17): when scripts/repair.sh is present and
// trusted, the script decides whether this is an Orbit directory. The
// missing env file is exactly what repair diagnoses, so it must not stop
// the script from running. This reverses the #237 rule that a
// scripts/repair.sh lying around does not make a deployment; the old test
// of that name asserted ErrNoDeployment and is replaced by this one.
func TestRepairCommand_ScriptWithoutEnvFileRunsTheScript(t *testing.T) {
	const script = "#!/usr/bin/env bash\n"
	cases := []struct {
		name    string
		secrets bool
	}{
		{name: "script only"},
		{name: "script and an empty secrets directory", secrets: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			scriptTree(t, dir, 0o755, testutil.File{Path: "scripts/repair.sh", Body: script, Mode: 0o700})
			if tc.secrets {
				if err := os.Mkdir(filepath.Join(dir, ".orbit-secrets"), 0o700); err != nil {
					t.Fatal(err)
				}
				chmod(t, filepath.Join(dir, ".orbit-secrets"), 0o700)
			}
			requests := testutil.CountingScriptSource(t)

			for _, mode := range []RepairMode{RepairCheck, RepairPlan, RepairExecuteSafe, RepairExecuteDangerous} {
				cmd, err := RepairCommand(dir, mode)
				if err != nil {
					t.Errorf("mode %v: err = %v, want <nil>", mode, err)
					continue
				}
				if cmd == nil {
					t.Errorf("mode %v: no command returned", mode)
					continue
				}
				want := append([]string{"bash", "scripts/repair.sh"}, strings.Fields(string(mode))...)
				if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
					t.Errorf("mode %v: args = %v, want %v", mode, cmd.Args, want)
				}
				if cmd.Dir != dir {
					t.Errorf("mode %v: dir = %q, want %q", mode, cmd.Dir, dir)
				}
			}
			if n := requests.Load(); n != 0 {
				t.Errorf("made %d HTTP requests; it must fetch nothing", n)
			}
			if _, err := os.Lstat(filepath.Join(dir, EnvFile)); !os.IsNotExist(err) {
				t.Errorf("%s was created or is otherwise present (err = %v); nothing may be fabricated", EnvFile, err)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "scripts", "repair.sh")); string(got) != script {
				t.Errorf("repair.sh was changed: %q", got)
			}
		})
	}
}

// An env file that cannot be read is neither "no deployment" nor
// "unavailable": it is an error saying the deployment could not be read.
func TestRepairCommand_UnreadableEnvFileIsNeitherNoDeploymentNorUnavailable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	chmod(t, filepath.Join(dir, EnvFile), 0o000)
	t.Cleanup(func() { chmod(t, filepath.Join(dir, EnvFile), 0o600) })
	_, err := RepairCommand(dir, RepairPlan)
	if err == nil || errors.Is(err, ErrNoDeployment) || errors.Is(err, ErrRepairUnavailable) {
		t.Fatalf("err = %v, want a read error that is neither ErrNoDeployment nor ErrRepairUnavailable", err)
	}
	if !strings.Contains(err.Error(), "read the deployment") {
		t.Errorf("err = %q, want it to say it could not read the deployment", err)
	}
}

func TestBuildRepairCommand_Shape(t *testing.T) {
	cmd := BuildRepairCommand("/tmp/target", RepairExecuteSafe)
	want := []string{"bash", "scripts/repair.sh", "--execute", "--safe-only"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %v, want %v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", cmd.Args, want)
		}
	}
	if cmd.Dir != "/tmp/target" {
		t.Errorf("dir = %q", cmd.Dir)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("Setsid must be set")
	}
	if cmd.Env != nil {
		t.Error("safe mode must not opt into machine prompts — unattended is its documented path")
	}

	dangerous := BuildRepairCommand("/tmp/target", RepairExecuteDangerous)
	machine := false
	for _, e := range dangerous.Env {
		if e == "ORBIT_REPAIR_PROMPTS=machine" {
			machine = true
		}
	}
	if !machine {
		t.Error("dangerous mode requires the machine prompt transport or the script refuses (exit 6)")
	}
}
