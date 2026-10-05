package pty

import (
	"testing"
)

func TestApp_RealPTY_NavigatingToRemoveShowsTheConfirmScreen(t *testing.T) {
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
	if err := console.expectString("This stops Orbit and removes its containers"); err != nil {
		t.Fatalf("did not reach the Remove confirm screen: %v", err)
	}

	console.send("\x1b") // Escape cancels

	waitForExit(t, cmd)
}
