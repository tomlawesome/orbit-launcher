package pty

import (
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

func TestApp_RealPTY_RemoveWithNoDeploymentStopsAtTheStart(t *testing.T) {
	// The launcher starts in test/pty, which holds no .env-orbit: there is
	// no deployment, so Remove stops at its first screen with Back only
	// (#205, owner 5a), and Esc is one step back, to the menu (owner 4a).
	binPath := buildBinary(t)
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

	console.send("\x1b") // Escape is one step back: the menu
	if err := console.expectWithin(10*time.Second, vtscreen.ContainsAll("Install", "Update", "Repair")); err != nil {
		t.Fatalf("Escape did not return to the menu: %v", err)
	}

	console.send("\x03") // Ctrl-C quits from anywhere
	waitForExit(t, cmd)
}
