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
	if _, err := console.ExpectString("Core configuration"); err != nil {
		t.Fatalf("did not reach the Install configuration screen: %v", err)
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
