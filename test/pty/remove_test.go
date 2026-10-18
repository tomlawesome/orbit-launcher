package pty

import (
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

func TestApp_RealPTY_RemoveWithNoDeploymentStopsAtTheStart(t *testing.T) {
	// The launcher starts in test/pty, which holds no .env-orbit: there is
	// no deployment, so Remove stops at its first screen with Menu only
	// (#205, owner 5a), and Esc is one step back, to the menu (owner 4a).
	binPath := launcher.BuildBinary(t)
	console, cmd := startUnderPTY(t, binPath)
	skipArrival(t, console)

	if err := console.expectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}

	for i := 0; i < 3; i++ { // Install, Update, Repair, Remove
		console.send("\x1b[B")
	}
	if err := console.expectString("▸ Remove"); err != nil {
		t.Fatalf("caret did not reach Remove: %v", err)
	}

	console.send("\r") // Enter
	if err := console.expectString("No Orbit deployment found in"); err != nil {
		t.Fatalf("did not reach the no-deployment Remove screen: %v", err)
	}

	// The dead end's one row is Menu (#228), and a stray key does nothing.
	if err := console.expectString("▸ Menu"); err != nil {
		t.Fatalf("the no-deployment Remove screen did not offer Menu: %v", err)
	}
	console.send("x")
	console.send("\x1b") // Escape is one step back: the menu
	if err := console.expectWithin(10*time.Second, vtscreen.ContainsAll("Install", "Update", "Repair")); err != nil {
		t.Fatalf("Escape did not return to the menu: %v", err)
	}

	console.send("\x03") // Ctrl-C quits from anywhere
	waitForExit(t, cmd)
}
