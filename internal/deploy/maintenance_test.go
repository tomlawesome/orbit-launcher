package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// API these tests expect (internal/deploy/maintenance.go), #242:
//
//	var ErrMaintenanceUnavailable error
//	    the deployment has no scripts/start-maintenance.sh (start) or
//	    scripts/end-maintenance.sh (end): an Orbit too old to ship them.
//	func MaintenanceCommand(targetDir string, start bool, notice, until string) (*exec.Cmd, error)
//	    argv is exactly
//	      bash scripts/start-maintenance.sh [--notice <notice>] [--until <until>]   (start)
//	      bash scripts/end-maintenance.sh                                            (end; notice and until ignored)
//	    each flag present only when its value is non-empty; every value one
//	    argv element, never a shell. Dir is targetDir. Detached like every
//	    engine script run (Setsid). Errors as RepairCommand's: folder name,
//	    owner, ErrNoDeployment (no .env-orbit and no script), the
//	    ErrMaintenanceUnavailable sentinel, *UntrustedPathError.
//	func HasMaintenanceScripts(targetDir string) bool
//	    true when both scripts exist in targetDir/scripts (Lstat only; trust is
//	    judged at MaintenanceCommand). The UI uses it to choose the
//	    "Maintenance needs a newer Orbit" screen.
//	Both scripts join orbitScripts in trusted.go, so each one is trusted
//	before any sibling script runs.

const (
	startScript = "scripts/start-maintenance.sh"
	endScript   = "scripts/end-maintenance.sh"
)

// deploymentWithMaintenance is an installed deployment holding both
// maintenance scripts, with modes set exactly.
func deploymentWithMaintenance(t *testing.T, startBody, endBody string) string {
	t.Helper()
	dir := t.TempDir()
	chmod(t, dir, 0o755)
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	scriptTree(t, dir, 0o755,
		testutil.File{Path: startScript, Body: startBody, Mode: 0o700},
		testutil.File{Path: endScript, Body: endBody, Mode: 0o700},
	)
	return dir
}

func TestMaintenanceCommand_StartArgvIsExact(t *testing.T) {
	requests := testutil.CountingScriptSource(t)
	dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\n")

	cases := []struct {
		name          string
		notice, until string
		want          []string
	}{
		{"both", "Back soon", "2025-10-29T22:30:00Z",
			[]string{"bash", startScript, "--notice", "Back soon", "--until", "2025-10-29T22:30:00Z"}},
		{"notice only", "Back soon", "",
			[]string{"bash", startScript, "--notice", "Back soon"}},
		{"until only", "", "2025-10-29T22:30:00Z",
			[]string{"bash", startScript, "--until", "2025-10-29T22:30:00Z"}},
		{"neither", "", "",
			[]string{"bash", startScript}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, err := MaintenanceCommand(dir, true, c.notice, c.until)
			if err != nil {
				t.Fatalf("MaintenanceCommand: %v", err)
			}
			if !slices.Equal(cmd.Args, c.want) {
				t.Fatalf("args = %q, want %q", cmd.Args, c.want)
			}
			if cmd.Dir != dir {
				t.Errorf("dir = %q, want the deployment %q", cmd.Dir, dir)
			}
			if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
				t.Error("the run is not detached (Setsid): it could reach /dev/tty")
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("preparing a maintenance run made %d HTTP requests; it must fetch nothing", n)
	}
}

func TestMaintenanceCommand_EndArgvIsExactAndIgnoresNoticeAndUntil(t *testing.T) {
	dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\n")
	for _, c := range [][2]string{{"", ""}, {"ignored", "2025-10-29T22:30:00Z"}} {
		cmd, err := MaintenanceCommand(dir, false, c[0], c[1])
		if err != nil {
			t.Fatalf("MaintenanceCommand: %v", err)
		}
		if want := []string{"bash", endScript}; !slices.Equal(cmd.Args, want) {
			t.Fatalf("args = %q, want %q", cmd.Args, want)
		}
	}
}

// The notice is typed by a person and is never parsed by a shell: whatever
// it holds reaches the script as the one argument after --notice.
func TestMaintenanceCommand_NoticeIsOneArgumentNeverAShell(t *testing.T) {
	dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\n")
	for _, notice := range []string{
		"it's $(touch pwned); `id` && echo \"x\" | cat > y",
		"--until 2030-01-01T00:00:00Z",
		"-rf",
		"two\nlines",
		"  padded  ",
		"Ünïcode — back soon",
	} {
		cmd, err := MaintenanceCommand(dir, true, notice, "")
		if err != nil {
			t.Fatalf("notice %q: %v", notice, err)
		}
		if want := []string{"bash", startScript, "--notice", notice}; !slices.Equal(cmd.Args, want) {
			t.Errorf("notice %q: args = %q, want %q", notice, cmd.Args, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("a shell expanded the notice")
	}
}

func TestMaintenanceCommand_RunsTheDeploymentsOwnScriptAndNothingElse(t *testing.T) {
	const startBody = "#!/usr/bin/env bash\nIFS='|'; echo \"got:$*|\"\necho 'maintenance action=start result=started until=none'\n"
	dir := deploymentWithMaintenance(t, startBody, "#!/usr/bin/env bash\nexit 9\n")
	cmd, err := MaintenanceCommand(dir, true, "Back soon", "2025-10-29T22:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "got:--notice|Back soon|--until|2025-10-29T22:30:00Z|") ||
		!strings.Contains(got, "maintenance action=start result=started until=none") {
		t.Errorf("stdout = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, startScript)); string(b) != startBody {
		t.Error("the launcher changed the deployment's script")
	}
}

func TestMaintenanceCommand_AbsentScriptIsUnavailable(t *testing.T) {
	requests := testutil.CountingScriptSource(t)
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile)) // a deployment from before the scripts
	for _, start := range []bool{true, false} {
		cmd, err := MaintenanceCommand(dir, start, "", "")
		if !errors.Is(err, ErrMaintenanceUnavailable) {
			t.Errorf("start=%v: err = %v, want ErrMaintenanceUnavailable", start, err)
		}
		if cmd != nil {
			t.Errorf("start=%v: an unavailable run returned a command", start)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("made %d HTTP requests; there is no fallback fetch", n)
	}
	if _, err := os.Lstat(filepath.Join(dir, "scripts")); !os.IsNotExist(err) {
		t.Error("nothing may be written into the deployment")
	}
}

// Only the script the run needs is looked for: an Orbit that ships the
// end script but not the start script can still be reopened.
func TestMaintenanceCommand_OnlyTheNeededScriptIsRequired(t *testing.T) {
	dir := t.TempDir()
	chmod(t, dir, 0o755)
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	scriptTree(t, dir, 0o755, testutil.File{Path: endScript, Body: "#!/usr/bin/env bash\n", Mode: 0o700})
	if _, err := MaintenanceCommand(dir, false, "", ""); err != nil {
		t.Errorf("end with only end-maintenance.sh: %v", err)
	}
	if _, err := MaintenanceCommand(dir, true, "", ""); !errors.Is(err, ErrMaintenanceUnavailable) {
		t.Errorf("start without start-maintenance.sh: err = %v, want ErrMaintenanceUnavailable", err)
	}
}

func TestMaintenanceCommand_NoDeploymentAtAll(t *testing.T) {
	for _, start := range []bool{true, false} {
		for name, dir := range map[string]string{
			"empty":   t.TempDir(),
			"missing": filepath.Join(t.TempDir(), "nope"),
		} {
			cmd, err := MaintenanceCommand(dir, start, "", "")
			if !errors.Is(err, ErrNoDeployment) || errors.Is(err, ErrMaintenanceUnavailable) {
				t.Errorf("%s start=%v: err = %v, want ErrNoDeployment alone", name, start, err)
			}
			if cmd != nil {
				t.Errorf("%s start=%v: returned a command", name, start)
			}
		}
	}
}

func TestHasMaintenanceScripts(t *testing.T) {
	dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\n")
	if !HasMaintenanceScripts(dir) {
		t.Error("both scripts present: want true")
	}
	if err := os.Remove(filepath.Join(dir, startScript)); err != nil {
		t.Fatal(err)
	}
	if HasMaintenanceScripts(dir) {
		t.Error("start script gone: want false")
	}
	bare := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(bare, EnvFile))
	if HasMaintenanceScripts(bare) || HasMaintenanceScripts(filepath.Join(bare, "nope")) {
		t.Error("no scripts directory: want false")
	}
}

// #191: nothing runs from a path someone else could have written.
func TestMaintenanceCommand_RefusesAnUntrustedScriptAndNeverRunsIt(t *testing.T) {
	for _, start := range []bool{true, false} {
		script := startScript
		if !start {
			script = endScript
		}
		for name, mode := range map[string]os.FileMode{"world-writable": 0o666} {
			t.Run(strings.TrimPrefix(script, "scripts/")+" "+name, func(t *testing.T) {
				dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\ntouch ran\n", "#!/usr/bin/env bash\ntouch ran\n")
				chmod(t, filepath.Join(dir, script), mode)
				cmd, err := MaintenanceCommand(dir, start, "", "")
				var untrusted *UntrustedPathError
				if !errors.As(err, &untrusted) || untrusted.Path != filepath.Join(dir, script) {
					t.Fatalf("err = %v, want a refusal naming %s", err, script)
				}
				if cmd != nil {
					t.Error("an untrusted script returned a command")
				}
				if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
					t.Error("the untrusted script ran")
				}
			})
		}
	}
}

func TestMaintenanceCommand_RefusesASymlinkedScript(t *testing.T) {
	dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\n")
	elsewhere := filepath.Join(t.TempDir(), "x.sh")
	if err := os.WriteFile(elsewhere, []byte("#!/usr/bin/env bash\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, startScript)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatal(err)
	}
	_, err := MaintenanceCommand(dir, true, "", "")
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) {
		t.Fatalf("err = %v, want an UntrustedPathError", err)
	}
}

// Both scripts are in orbitScripts: they source one another's helpers, so
// an untrusted end script stops the start script, and either one stops
// Repair (and the reverse), exactly as configure.sh and repair.sh do.
func TestMaintenanceScripts_AreSiblingsOfTheTrustedSet(t *testing.T) {
	for _, name := range []string{"start-maintenance.sh", "end-maintenance.sh"} {
		if !slices.Contains(orbitScripts, name) {
			t.Errorf("orbitScripts lacks %s", name)
		}
	}

	dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\n")
	chmod(t, filepath.Join(dir, endScript), 0o666)
	_, err := MaintenanceCommand(dir, true, "", "")
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) || filepath.Base(untrusted.Path) != "end-maintenance.sh" {
		t.Fatalf("start with an untrusted end script: err = %v, want a refusal naming end-maintenance.sh", err)
	}

	// An untrusted start-maintenance.sh stops Repair's own script too.
	repairDir := trustedDeployment(t)
	extra := filepath.Join(repairDir, startScript)
	if err := os.WriteFile(extra, []byte("#!/usr/bin/env bash\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	chmod(t, extra, 0o666)
	_, err = RepairCommand(repairDir, RepairPlan)
	if !errors.As(err, &untrusted) || filepath.Base(untrusted.Path) != "start-maintenance.sh" {
		t.Fatalf("repair with an untrusted start-maintenance.sh: err = %v, want a refusal naming it", err)
	}
}

// A deployment with the scripts but a trusted, ordinary repair.sh keeps
// working: adding the new siblings changes nothing for a clean tree.
func TestMaintenanceScripts_ACleanTreeStillRepairs(t *testing.T) {
	dir := deploymentWithMaintenance(t, "#!/usr/bin/env bash\n", "#!/usr/bin/env bash\n")
	if err := os.WriteFile(filepath.Join(dir, "scripts", "repair.sh"), []byte("#!/usr/bin/env bash\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairCommand(dir, RepairCheck); err != nil {
		t.Fatalf("RepairCommand on a clean tree with maintenance scripts: %v", err)
	}
}
