package pty

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

func TestApp_RealPTY_UpdateWithNoDeploymentShowsNotFound(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	console, cmd := startUnderPTYInDir(t, binPath, t.TempDir())
	skipArrival(t, console)

	if err := console.expectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}
	console.send("\x1b[B") // Down to Update
	if err := console.expectString("▸ Update"); err != nil {
		t.Fatalf("caret did not reach Update: %v", err)
	}
	console.send("\r") // Enter
	if err := console.expectString("No Orbit deployment found in"); err != nil {
		t.Fatalf("did not reach the Update not-found screen: %v", err)
	}

	// The dead end is a one-row menu: Menu goes back to the main menu, and
	// no other key quits (#228).
	if err := console.expectString("▸ Menu"); err != nil {
		t.Fatalf("the not-found screen did not offer Menu: %v", err)
	}
	console.send("x")
	console.send("5")
	console.send("\x1b[B") // Down
	if err := console.expectString("▸ Menu"); err != nil {
		t.Fatalf("a stray key changed the not-found screen: %v", err)
	}
	console.send("\r") // Enter on Menu
	if err := console.expectWithin(10*time.Second, vtscreen.ContainsAll("Install", "Update", "Repair")); err != nil {
		t.Fatalf("Menu did not return to the main menu (a stray key may have quit): %v", err)
	}
	console.send("\x03") // Ctrl-C quits from anywhere

	waitForExit(t, cmd)
}

func TestApp_RealPTY_UpdateWithAnExistingDeploymentShowsTheConfirmScreen(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))

	binPath := launcher.BuildBinary(t)
	console, cmd := startUnderPTYInDir(t, binPath, dir)
	skipArrival(t, console)

	if err := console.expectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}
	// A detected deployment preselects Update — no navigation needed, and
	// the identity block shows the deployment's FQDN with no status word
	// (the health probe is env-gated off in these tests).
	if err := console.expectString("▸ Update"); err != nil {
		t.Fatalf("caret was not preselected on Update: %v", err)
	}
	console.send("\r") // Enter
	if err := console.expectString("Pull the latest Orbit and update this deployment"); err != nil {
		t.Fatalf("did not reach the Update confirm screen: %v", err)
	}
	// The confirm screen's identity line carries the bare FQDN — the
	// scheme is launcher noise at a glance, same as the splash.
	if err := console.expectString("mail.example.com"); err != nil {
		t.Fatalf("did not see the detected deployment's host: %v", err)
	}

	// Escape is one step back, to the menu (#210, owner 4a), and never
	// touches Docker; Ctrl-C quits from anywhere.
	console.send("\x1b")
	if err := console.expectWithin(10*time.Second, vtscreen.ContainsAll("Install", "Update", "Repair")); err != nil {
		t.Fatalf("Escape on the confirm screen did not return to the menu: %v", err)
	}
	console.send("\x03")

	waitForExit(t, cmd)
}
