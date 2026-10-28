package pty

// #277 (inline review of the Esc-during-import fix): quitting while the
// deployment's configuration is being imported into the run's
// configuration tree leaves nothing of the launcher's in TMPDIR once the
// process has exited -- no configuration folder, partial or empty. The
// moment that matters is the process's exit: a removal still running
// in the launcher dies with it.
//
// The deployment holds many secrets, so the import takes a while; the
// quit lands once the copy has begun, at a different point each round.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

// importSecrets is how many secrets the deployment holds: enough that
// importing them takes about a second.
const importSecrets = 10000

// alwaysRefusesConfiguration is an install.sh that refuses for
// configuration on every run, handing its configure tree over as
// install.sh does, so an Update goes straight into guided
// configuration, whose preparation imports the deployment's
// configuration.
var alwaysRefusesConfiguration = handsOverTree(`#!/usr/bin/env bash
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
`+configRefusal+`  exit 1
`, fakeMachineConfigure)

// deploymentWithSecrets is an installed deployment whose .orbit-secrets
// holds n secrets.
func deploymentWithSecrets(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))
	secrets := filepath.Join(dir, ".orbit-secrets")
	if err := os.Mkdir(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(secrets, fmt.Sprintf("secret-%05d", i)), []byte("secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// importedSecrets counts the secrets copied so far into the
// configuration tree in tmp (-1 when there is no tree or no secrets
// folder in it yet).
func importedSecrets(tmp string) int {
	trees, _ := filepath.Glob(filepath.Join(tmp, "orbit-launcher-config-*"))
	for _, tree := range trees {
		if entries, err := os.ReadDir(filepath.Join(tree, ".orbit-secrets")); err == nil {
			return len(entries)
		}
	}
	return -1
}

// updateIntoImport starts the launcher on dir with TMPDIR at tmp, starts
// an Update, and returns once at least copied secrets have been imported
// into the configuration tree.
func updateIntoImport(t *testing.T, binPath, dir, tmp, scriptURL string, copied int) (*vtConsole, *exec.Cmd) {
	t.Helper()
	console := newConsole(t, 80, 26, 15*time.Second)
	cmd := exec.Command(binPath)
	cmd.Dir = dir
	cmd.Env = append(launcher.HermeticEnv(launcher.Animation),
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+scriptURL,
		"TMPDIR="+tmp)
	console.start(t, cmd)

	skipArrival(t, console)
	if err := console.expectString("▸ Update"); err != nil {
		t.Fatalf("Update was not preselected for the deployment: %v", err)
	}
	console.send("\r")
	if err := console.expectString("Pull the latest Orbit and update this deployment"); err != nil {
		t.Fatalf("did not reach the Update confirm screen: %v", err)
	}
	console.send("\r")

	// A busy shared runner copied as few as 120 secrets a second, so the
	// wait allows for that; a launcher that stops copying still fails here.
	// Each count lists a folder of thousands of files, so it is taken every
	// 50 ms, not every 2 ms, to leave the copy the CPU it is waiting on (#305).
	deadline := time.Now().Add(2 * time.Minute)
	for importedSecrets(tmp) < copied {
		if time.Now().After(deadline) {
			t.Fatalf("the import never reached %d secrets (now %d):\n%s", copied, importedSecrets(tmp), vtscreen.Screen(console.term))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := importedSecrets(tmp); n >= importSecrets {
		t.Fatalf("setup: the import had finished (%d secrets) before the quit could land", n)
	}
	return console, cmd
}

// awaitExit waits for the launcher to end, within the stop grace.
func awaitExit(t *testing.T, cmd *exec.Cmd, after string) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(stopGraceCeiling):
		_ = cmd.Process.Kill()
		t.Fatalf("orbit-launcher had not exited %v after %s", stopGraceCeiling, after)
	}
}

// leftAtExit fails the round if the launcher left anything in tmp,
// looked at the moment the process has exited.
func leftAtExit(t *testing.T, tmp, how string, round, copied int) bool {
	t.Helper()
	left := launcherTemps(t, tmp)
	if len(left) == 0 {
		return false
	}
	var what []string
	for _, name := range left {
		inside, _ := os.ReadDir(filepath.Join(tmp, name))
		what = append(what, fmt.Sprintf("%s (%d entries)", name, len(inside)))
	}
	t.Errorf("round %d, %s after %d secrets imported: the launcher exited leaving %s in TMPDIR",
		round, how, copied, strings.Join(what, ", "))
	return true
}

const quitRounds = 7

// 1. Ctrl-C, or SIGTERM from outside, while the import runs.
func TestConsole_RealPTY_QuittingDuringTheConfigurationImportLeavesNothingInTMPDIR(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	scriptURL := launcher.ServeScript(t, alwaysRefusesConfiguration)
	dir := deploymentWithSecrets(t, importSecrets)
	for _, how := range []string{"Ctrl-C", "SIGTERM"} {
		t.Run(how, func(t *testing.T) {
			for round := 0; round < quitRounds; round++ {
				tmp := t.TempDir()
				copied := 1 + round*1300
				console, cmd := updateIntoImport(t, binPath, dir, tmp, scriptURL, copied)
				if how == "Ctrl-C" {
					console.send("\x03")
				} else if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatalf("signal the launcher: %v", err)
				}
				quit := time.Now()
				awaitExit(t, cmd, how)
				t.Logf("round %d: exited %v after %s", round, time.Since(quit), how)
				if leftAtExit(t, tmp, how, round, copied) {
					return
				}
			}
		})
	}
}

// 2. Esc during the import goes to the menu; quitting from the menu
// before the copy has finished.
func TestConsole_RealPTY_EscDuringTheImportThenQuittingLeavesNothingInTMPDIR(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	scriptURL := launcher.ServeScript(t, alwaysRefusesConfiguration)
	dir := deploymentWithSecrets(t, importSecrets)
	for round := 0; round < quitRounds; round++ {
		tmp := t.TempDir()
		copied := 1 + round*1300
		console, cmd := updateIntoImport(t, binPath, dir, tmp, scriptURL, copied)
		console.send("\x1b")
		if err := console.expectWithin(10*time.Second, vtscreen.ContainsAll("Install", "Update", "Repair")); err != nil {
			t.Fatalf("Esc during the import did not go to the main menu: %v", err)
		}
		t.Logf("round %d: at the menu with %d secrets imported", round, importedSecrets(tmp))
		console.send("\x03") // quit from the menu
		awaitExit(t, cmd, "Ctrl-C at the menu")
		if leftAtExit(t, tmp, "Esc then Ctrl-C", round, copied) {
			return
		}
	}
}
