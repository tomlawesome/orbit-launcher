package deploy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// noScriptSource points the install.sh override at a server that counts
// every request, so a test can prove nothing was fetched.
func noScriptSource(t *testing.T) *atomic.Int32 {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("#!/usr/bin/env bash\necho fetched\n"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", srv.URL+"/scripts/install.sh")
	return &requests
}

// deploymentWithRepair is a deployment directory holding the
// repair.sh install.sh placed there from the image.
func deploymentWithRepair(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "repair.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRepairCommand_RunsTheDeploymentsOwnScript(t *testing.T) {
	requests := noScriptSource(t)
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
	requests := noScriptSource(t)
	dir := t.TempDir()

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

func TestRepairCommand_MissingDeploymentIsUnavailable(t *testing.T) {
	_, err := RepairCommand(filepath.Join(t.TempDir(), "nowhere"), RepairCheck)
	if !errors.Is(err, ErrRepairUnavailable) {
		t.Fatalf("err = %v, want ErrRepairUnavailable", err)
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
