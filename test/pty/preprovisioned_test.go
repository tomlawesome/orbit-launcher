package pty

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
)

// #261 item 2: after the installer rolls a fresh install back, the folder
// holds only the pre-provisioned .env-orbit and .orbit-secrets, plus the
// installer's orbit-install-backups directory. That is not a deployment:
// the menu starts on Install, which the engine accepts, not on Update,
// which it refuses.
func TestApp_RealPTY_PreProvisionedFolderPreselectsInstall(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	for _, sub := range []string{".orbit-secrets", filepath.Join("orbit-install-backups", "20261020T101010Z", "originals")} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	console, cmd := startUnderPTYInDir(t, launcher.BuildBinary(t), dir)
	skipArrival(t, console)

	if err := console.expectString("▸ Install"); err != nil {
		t.Fatalf("caret was not preselected on Install for a pre-provisioned folder: %v", err)
	}
	console.send("\x03")
	waitForExit(t, cmd)
}
