package pty

import (
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

func TestApp_RealPTY_NavigatingIntoInstallShowsTheProfileScreen(t *testing.T) {
	binPath := launcher.BuildBinary(t)
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
	// Stops here, deliberately: the notice's countdown and typed phrase
	// come next, and accepting them would start a real install, which
	// would fetch from the network — not something to trigger from this
	// test tier. Confirming the notice renders and Escape navigates back
	// is enough.
	if err := console.expectString("A note before you install"); err != nil {
		t.Fatalf("did not reach the development notice: %v", err)
	}

	console.send("\x1b") // Escape back to profile
	if err := console.expectString("Choose a deployment profile"); err != nil {
		t.Fatalf("did not return to the profile screen: %v", err)
	}
	// The profile screen is the flow's first: Escape is one step back,
	// to the menu (#210, owner 4a), and Ctrl-C quits from anywhere.
	console.send("\x1b")
	if err := console.expectWithin(10*time.Second, vtscreen.ContainsAll("Install", "Update", "Repair")); err != nil {
		t.Fatalf("Escape on the profile screen did not return to the menu: %v", err)
	}
	console.send("\x03")

	waitForExit(t, cmd)
}
