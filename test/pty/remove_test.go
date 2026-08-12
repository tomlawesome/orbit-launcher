package pty

import (
	"testing"
)

func TestApp_RealPTY_NavigatingToRemoveShowsTheConfirmScreen(t *testing.T) {
	binPath := buildBinary(t)
	console, cmd := startUnderPTY(t, binPath)
	skipArrival(t, console)

	if _, err := console.ExpectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}

	for i := 0; i < 3; i++ { // Install, Update, Repair, Remove
		if _, err := console.Send("\x1b[B"); err != nil {
			t.Fatalf("send Down: %v", err)
		}
	}
	if _, err := console.ExpectString("▸ Remove"); err != nil {
		t.Fatalf("caret did not reach Remove: %v", err)
	}

	if _, err := console.Send("\r"); err != nil { // Enter
		t.Fatalf("send Enter: %v", err)
	}
	if _, err := console.ExpectString("This stops Orbit and removes its containers"); err != nil {
		t.Fatalf("did not reach the Remove confirm screen: %v", err)
	}

	if _, err := console.Send("\x1b"); err != nil { // Escape cancels
		t.Fatalf("send Escape: %v", err)
	}

	waitForExit(t, cmd)
}
