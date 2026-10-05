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

	if err := console.expectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}
	console.send("\x1b[B") // Down to Update
	if err := console.expectString("▸ Update"); err != nil {
		t.Fatalf("caret did not reach Update: %v", err)
	}
	console.send("\r") // Enter
	if err := console.expectString("No Orbit deployment found here"); err != nil {
		t.Fatalf("did not reach the Update not-found screen: %v", err)
	}

	console.send("\r") // any key quits

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

	console.send("\x1b") // Escape cancels, never touches Docker

	waitForExit(t, cmd)
}
