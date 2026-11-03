package deploy

// Pins #304 SEC-S1-1: AdoptConfig trust-checks the destination but never
// the collected tree it reads from (under $TMPDIR, where anyone can
// plant one). Adopting must refuse a collected tree that is a symlink,
// is world-writable, or is not owned by the user running the launcher,
// by the rule requireTrustedConfigDirs applies (the tree, and its
// .orbit-secrets when present), and leave the destination untouched. Each
// refusal is paired with the tree it must still accept.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireRefusedSource(t *testing.T, err error, path, reason, targetDir string) {
	t.Helper()
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) || untrusted.Path != path || !strings.Contains(untrusted.Reason, reason) {
		t.Fatalf("AdoptConfig err = %v, want a refusal naming %s (%q)", err, path, reason)
	}
	assertLiveConfigUnchanged(t, targetDir)
}

func requireAdopted(t *testing.T, err error, targetDir string) {
	t.Helper()
	if err != nil {
		t.Fatalf("AdoptConfig refused a trusted collected tree: %v", err)
	}
	if env, _ := os.ReadFile(filepath.Join(targetDir, EnvFile)); !strings.Contains(string(env), "OIDC_CLIENT_ID=new") {
		t.Fatalf("the collected answers were not adopted: %q", env)
	}
}

func TestAdoptConfig_CollectedTreeSymlink(t *testing.T) {
	t.Run("a symlink to a tree is refused", func(t *testing.T) {
		targetDir := liveTarget(t)
		link := filepath.Join(t.TempDir(), "collected")
		if err := os.Symlink(newSession(t), link); err != nil {
			t.Fatal(err)
		}
		requireRefusedSource(t, AdoptConfig(link, targetDir), link, "symbolic link", targetDir)
	})
	t.Run("the real tree is adopted", func(t *testing.T) {
		targetDir := liveTarget(t)
		requireAdopted(t, AdoptConfig(newSession(t), targetDir), targetDir)
	})
}

func TestAdoptConfig_CollectedTreeWorldWritable(t *testing.T) {
	t.Run("a world-writable tree is refused", func(t *testing.T) {
		targetDir := liveTarget(t)
		tree := newSession(t)
		chmod(t, tree, 0o777)
		requireRefusedSource(t, AdoptConfig(tree, targetDir), tree, "writable by everyone", targetDir)
	})
	t.Run("a world-writable .orbit-secrets is refused", func(t *testing.T) {
		targetDir := liveTarget(t)
		tree := newSession(t)
		secrets := filepath.Join(tree, SecretsDir)
		chmod(t, secrets, 0o777)
		requireRefusedSource(t, AdoptConfig(tree, targetDir), secrets, "writable by everyone", targetDir)
	})
	t.Run("a group-writable tree is adopted", func(t *testing.T) {
		// The rule accepts group-writable (RequireTrustedPath's doc).
		targetDir := liveTarget(t)
		tree := newSession(t)
		chmod(t, tree, 0o770)
		requireAdopted(t, AdoptConfig(tree, targetDir), targetDir)
	})
}

// The owner case needs a second account's files, which only root can
// make (as TestRequireTrustedPath_Owners); CI runs the suite as root.
func TestAdoptConfig_CollectedTreeOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to give the collected tree to another user; runs where the suite runs as root")
	}
	const nobody = 65534
	t.Run("another user's tree is refused", func(t *testing.T) {
		targetDir := liveTarget(t)
		asOperatorOf(t, targetDir, operatorUID)
		tree := newSession(t)
		asOperatorOf(t, tree, nobody)
		asLauncherUser(t, operatorUID)
		requireRefusedSource(t, AdoptConfig(tree, targetDir), tree, "not by the user running the launcher", targetDir)
	})
	t.Run("the launcher user's own tree is adopted", func(t *testing.T) {
		targetDir := liveTarget(t)
		asOperatorOf(t, targetDir, operatorUID)
		tree := newSession(t)
		asOperatorOf(t, tree, operatorUID)
		requireAdopted(t, AdoptConfig(tree, targetDir), targetDir)
	})
}
