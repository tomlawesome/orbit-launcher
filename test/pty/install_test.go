package pty

import "testing"

func TestApp_RealPTY_NavigatingIntoInstallShowsTheProfileScreen(t *testing.T) {
	binPath := buildBinary(t)
	console, cmd := startUnderPTY(t, binPath)

	if _, err := console.ExpectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}

	if _, err := console.Send("\r"); err != nil { // Enter — Install is selected by default
		t.Fatalf("send Enter: %v", err)
	}
	if _, err := console.ExpectString("Choose a deployment profile"); err != nil {
		t.Fatalf("did not reach the Install profile screen: %v", err)
	}

	if _, err := console.Send("\r"); err != nil { // Enter — Standard is selected by default
		t.Fatalf("send Enter: %v", err)
	}
	// Stops here, deliberately: the next screen's Enter hands the real
	// terminal to install.sh (see internal/ui/handoff.go), which would
	// fetch from the network and try to run a real install — not
	// something to trigger from this test tier. Confirming the handoff
	// screen renders and Escape navigates back is enough; the handoff
	// mechanism itself (tea.ExecProcess) is proven in internal/ui's unit
	// tests via an injected fake.
	if _, err := console.ExpectString("Ready to install"); err != nil {
		t.Fatalf("did not reach the Install confirm screen: %v", err)
	}

	if _, err := console.Send("\x1b"); err != nil { // Escape back to profile
		t.Fatalf("send Escape: %v", err)
	}
	if _, err := console.ExpectString("Choose a deployment profile"); err != nil {
		t.Fatalf("did not return to the profile screen: %v", err)
	}
	if _, err := console.Send("\x1b"); err != nil { // Escape quits
		t.Fatalf("send Escape: %v", err)
	}

	waitForExit(t, cmd)
}
