package pty

import "testing"

func TestApp_RealPTY_NavigatingIntoInstallShowsTheProfileScreen(t *testing.T) {
	binPath := buildBinary(t)
	console, cmd := startUnderPTY(t, binPath)
	skipArrival(t, console)

	if err := console.expectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}

	console.send("\r") // Enter — Install is selected by default
	if err := console.expectString("Choose a deployment profile"); err != nil {
		t.Fatalf("did not reach the Install profile screen: %v", err)
	}

	console.send("\r") // Enter — Standard is selected by default
	// Stops here, deliberately: the next screen's Enter hands the real
	// terminal to install.sh (see internal/ui/handoff.go), which would
	// fetch from the network and try to run a real install — not
	// something to trigger from this test tier. Confirming the handoff
	// screen renders and Escape navigates back is enough; the handoff
	// mechanism itself (tea.ExecProcess) is proven in internal/ui's unit
	// tests via an injected fake.
	if err := console.expectString("Ready to install"); err != nil {
		t.Fatalf("did not reach the Install confirm screen: %v", err)
	}

	console.send("\x1b") // Escape back to profile
	if err := console.expectString("Choose a deployment profile"); err != nil {
		t.Fatalf("did not return to the profile screen: %v", err)
	}
	console.send("\x1b") // Escape quits

	waitForExit(t, cmd)
}
