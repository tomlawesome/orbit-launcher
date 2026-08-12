package pty

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApp_RealPTY_UpdateWithNoDeploymentShowsNotFound(t *testing.T) {
	binPath := buildBinary(t)
	console, cmd := startUnderPTYInDir(t, binPath, t.TempDir())
	skipArrival(t, console)

	if _, err := console.ExpectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}
	if _, err := console.Send("\x1b[B"); err != nil { // Down to Update
		t.Fatalf("send Down: %v", err)
	}
	if _, err := console.ExpectString("▸ Update"); err != nil {
		t.Fatalf("caret did not reach Update: %v", err)
	}
	if _, err := console.Send("\r"); err != nil { // Enter
		t.Fatalf("send Enter: %v", err)
	}
	if _, err := console.ExpectString("No Orbit deployment found here"); err != nil {
		t.Fatalf("did not reach the Update not-found screen: %v", err)
	}

	if _, err := console.Send("\r"); err != nil { // any key quits
		t.Fatalf("send Enter: %v", err)
	}

	waitForExit(t, cmd)
}

func TestApp_RealPTY_UpdateWithAnExistingDeploymentShowsTheConfirmScreen(t *testing.T) {
	dir := t.TempDir()
	envContent := "APP_URL=https://mail.example.com\nORBIT_IMAGE=ghcr.io/tomlawesome/orbit@sha256:" +
		"0000000000000000000000000000000000000000000000000000000000000000\n"
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(envContent), 0o600); err != nil {
		t.Fatalf("write fixture .env-orbit: %v", err)
	}

	binPath := buildBinary(t)
	console, cmd := startUnderPTYInDir(t, binPath, dir)
	skipArrival(t, console)

	if _, err := console.ExpectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}
	// A detected deployment preselects Update — no navigation needed, and
	// the identity block shows the deployment's FQDN with no status word
	// (the health probe is env-gated off in these tests).
	if _, err := console.ExpectString("▸ Update"); err != nil {
		t.Fatalf("caret was not preselected on Update: %v", err)
	}
	if _, err := console.Send("\r"); err != nil { // Enter
		t.Fatalf("send Enter: %v", err)
	}
	if _, err := console.ExpectString("Pull the latest Orbit and update this deployment"); err != nil {
		t.Fatalf("did not reach the Update confirm screen: %v", err)
	}
	// The confirm screen's identity line carries the bare FQDN — the
	// scheme is launcher noise at a glance, same as the splash.
	if _, err := console.ExpectString("mail.example.com"); err != nil {
		t.Fatalf("did not see the detected deployment's host: %v", err)
	}

	if _, err := console.Send("\x1b"); err != nil { // Escape cancels, never touches Docker
		t.Fatalf("send Escape: %v", err)
	}

	waitForExit(t, cmd)
}
