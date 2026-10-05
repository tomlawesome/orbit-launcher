package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Adoption's refusal and failure paths (#188). Each test starts from a
// working configuration in the target and checks it on disk afterwards:
// a refused or failed adoption must leave it exactly as it was — never
// new settings beside old secrets, never an empty or half-written file,
// never a staging temp left behind.

// liveTarget is a target directory holding a working configuration.
func liveTarget(t *testing.T) string {
	t.Helper()
	targetDir := t.TempDir()
	writeTestFile(t, filepath.Join(targetDir, ".env-orbit"), "APP_URL=https://kept.example\nOIDC_CLIENT_ID=old\n", 0o600)
	writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret"), "old-secret\n", 0o600)
	return targetDir
}

// assertLiveConfigUnchanged fails unless the target still holds exactly
// what liveTarget wrote, with no staging temp beside it.
func assertLiveConfigUnchanged(t *testing.T, targetDir string) {
	t.Helper()
	if env, err := os.ReadFile(filepath.Join(targetDir, ".env-orbit")); err != nil || string(env) != "APP_URL=https://kept.example\nOIDC_CLIENT_ID=old\n" {
		t.Errorf(".env-orbit was changed by a failed adoption: %q err=%v", env, err)
	}
	if secret, err := os.ReadFile(filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret")); err != nil || string(secret) != "old-secret\n" {
		t.Errorf("the secret was changed by a failed adoption: %q err=%v", secret, err)
	}
	assertNoTemps(t, targetDir)
}

// newSession is a configuration session's tree with new settings and a
// new secret.
func newSession(t *testing.T) string {
	t.Helper()
	treeDir := t.TempDir()
	writeTestFile(t, filepath.Join(treeDir, ".env-orbit"), "APP_URL=https://kept.example\nOIDC_CLIENT_ID=new\n", 0o600)
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "oidc-client-secret"), "new-secret\n", 0o600)
	return treeDir
}

// limitFileSize caps the size of any file this process writes, for the
// rest of the test, so a write past it fails as it would on a full disk.
// Root is bound by the limit too, so unlike a permission trick this runs
// in CI.
func limitFileSize(t *testing.T, limit uint64) {
	t.Helper()
	var prev syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &prev); err != nil {
		t.Fatal(err)
	}
	capped := prev
	capped.Cur = limit
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &capped); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &prev); err != nil {
			t.Errorf("restore the file size limit: %v", err)
		}
	})
}

// A .env-orbit in the session tree that is a symlink is refused, so a
// link pointing out of the tree is never followed and its target's
// contents never become the live configuration.
func TestAdoptConfig_RefusesASymlinkedEnvFileInTheSession(t *testing.T) {
	targetDir := liveTarget(t)
	treeDir := newSession(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeTestFile(t, outside, "APP_URL=https://outside.example\n", 0o600)
	envSrc := filepath.Join(treeDir, ".env-orbit")
	if err := os.Remove(envSrc); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, envSrc); err != nil {
		t.Fatal(err)
	}

	err := AdoptConfig(treeDir, targetDir)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected a not-a-regular-file refusal, got %v", err)
	}
	assertLiveConfigUnchanged(t, targetDir)
}

// A session whose .orbit-secrets is not a directory is refused before
// anything is written: the new settings are not adopted without their
// secrets.
func TestAdoptConfig_RefusesASessionSecretsPathThatIsNotADirectory(t *testing.T) {
	targetDir := liveTarget(t)
	treeDir := t.TempDir()
	writeTestFile(t, filepath.Join(treeDir, ".env-orbit"), "APP_URL=https://kept.example\nOIDC_CLIENT_ID=new\n", 0o600)
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets"), "not a directory\n", 0o600)

	err := AdoptConfig(treeDir, targetDir)
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("expected a not-a-directory refusal, got %v", err)
	}
	assertLiveConfigUnchanged(t, targetDir)
}

// Only regular files in the session's secrets are carried over: a
// symlink there is skipped, so whatever it points at never lands in the
// target's secrets.
func TestAdoptConfig_SkipsASymlinkAmongTheSessionSecrets(t *testing.T) {
	targetDir := liveTarget(t)
	treeDir := newSession(t)
	outside := filepath.Join(t.TempDir(), "outside")
	writeTestFile(t, outside, "not-a-session-secret\n", 0o600)
	if err := os.Symlink(outside, filepath.Join(treeDir, ".orbit-secrets", "linked")); err != nil {
		t.Fatal(err)
	}

	if err := AdoptConfig(treeDir, targetDir); err != nil {
		t.Fatalf("AdoptConfig: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(targetDir, ".orbit-secrets", "linked")); !os.IsNotExist(err) {
		t.Errorf("the symlinked secret was carried into the target (err=%v)", err)
	}
	if secret, _ := os.ReadFile(filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret")); string(secret) != "new-secret\n" {
		t.Errorf("the session's real secret was not adopted: %q", secret)
	}
	if env, _ := os.ReadFile(filepath.Join(targetDir, ".env-orbit")); !strings.Contains(string(env), "OIDC_CLIENT_ID=new") {
		t.Errorf("the session's settings were not adopted: %q", env)
	}
}

// A live secret that is not a regular file is refused, never replaced,
// and the refusal comes before anything is written: the new settings are
// not adopted beside the old secret, and the file the link points at is
// not overwritten through it.
func TestAdoptConfig_RefusesToReplaceALiveSecretThatIsNotAFile(t *testing.T) {
	targetDir := liveTarget(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	writeTestFile(t, elsewhere, "someone else's file\n", 0o600)
	livePath := filepath.Join(targetDir, ".orbit-secrets", "zz-linked")
	if err := os.Symlink(elsewhere, livePath); err != nil {
		t.Fatal(err)
	}
	treeDir := newSession(t)
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "zz-linked"), "new-linked\n", 0o600)

	err := AdoptConfig(treeDir, targetDir)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected a not-a-regular-file refusal, got %v", err)
	}
	assertLiveConfigUnchanged(t, targetDir)
	if info, err := os.Lstat(livePath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the refused symlink was replaced (err=%v)", err)
	}
	if body, _ := os.ReadFile(elsewhere); string(body) != "someone else's file\n" {
		t.Errorf("the file behind the symlink was overwritten: %q", body)
	}
}

// A secret that cannot be staged stops the adoption before any live file
// is switched: every temp staged so far is removed, and a secrets
// directory the adoption created for itself is taken back. The name is
// a legal file name, but its temp name is over the 255-byte limit, so
// creating the temp fails — even as root.
func TestAdoptConfig_StagingFailureTakesBackEverythingItWrote(t *testing.T) {
	longName := "zz-" + strings.Repeat("s", 247) // sorts after the real secret

	t.Run("existing secrets directory", func(t *testing.T) {
		targetDir := liveTarget(t)
		treeDir := newSession(t)
		writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", longName), "x\n", 0o600)

		err := AdoptConfig(treeDir, targetDir)
		if err == nil || !strings.Contains(err.Error(), "existing configuration is unchanged") {
			t.Fatalf("expected an unchanged-configuration error, got %v", err)
		}
		assertLiveConfigUnchanged(t, targetDir)
	})

	t.Run("fresh secrets directory", func(t *testing.T) {
		targetDir := t.TempDir()
		writeTestFile(t, filepath.Join(targetDir, ".env-orbit"), "APP_URL=https://kept.example\n", 0o600)
		treeDir := newSession(t)
		writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", longName), "x\n", 0o600)

		err := AdoptConfig(treeDir, targetDir)
		if err == nil || !strings.Contains(err.Error(), "existing configuration is unchanged") {
			t.Fatalf("expected an unchanged-configuration error, got %v", err)
		}
		if env, _ := os.ReadFile(filepath.Join(targetDir, ".env-orbit")); string(env) != "APP_URL=https://kept.example\n" {
			t.Errorf(".env-orbit was changed by a failed adoption: %q", env)
		}
		if _, err := os.Lstat(filepath.Join(targetDir, ".orbit-secrets")); !os.IsNotExist(err) {
			entries, _ := os.ReadDir(filepath.Join(targetDir, ".orbit-secrets"))
			t.Errorf("the secrets directory the failed adoption created was left behind, holding %d entries (err=%v)", len(entries), err)
		}
		assertNoTemps(t, targetDir)
	})
}

// Running out of space part-way through writing a staged secret leaves
// the live configuration whole and removes the half-written temp.
func TestAdoptConfig_DiskFullWhileStagingLeavesTheLiveConfigWhole(t *testing.T) {
	targetDir := liveTarget(t)
	treeDir := newSession(t)
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "oidc-client-secret"), strings.Repeat("n", 4096), 0o600)

	limitFileSize(t, 1024)
	err := AdoptConfig(treeDir, targetDir)
	if err == nil || !strings.Contains(err.Error(), "existing configuration is unchanged") {
		t.Fatalf("expected an unchanged-configuration error, got %v", err)
	}
	assertLiveConfigUnchanged(t, targetDir)
}

// Importing into the tree uses the same whole-file replacement: a write
// that runs out of space leaves the tree's existing .env-orbit whole,
// not truncated, and no temp beside it.
func TestImportTargetConfig_DiskFullLeavesTheTreeFileWhole(t *testing.T) {
	targetDir := t.TempDir()
	writeTestFile(t, filepath.Join(targetDir, ".env-orbit"), "APP_URL=https://kept.example\n"+strings.Repeat("#\n", 2048), 0o600)
	treeDir := t.TempDir()
	treeEnv := filepath.Join(treeDir, ".env-orbit")
	writeTestFile(t, treeEnv, "APP_URL=https://tree.example\n", 0o600)

	limitFileSize(t, 1024)
	if err := ImportTargetConfig(treeDir, targetDir); err == nil {
		t.Fatal("expected an error when the import cannot be written")
	}
	if body, err := os.ReadFile(treeEnv); err != nil || string(body) != "APP_URL=https://tree.example\n" {
		t.Errorf("the tree's .env-orbit was not left whole: %q err=%v", body, err)
	}
	assertNoTemps(t, treeDir)
}

// When the finished temp cannot be renamed into place (here a directory
// stands where the file goes), the temp is removed rather than left as
// clutter that a later import would mistake for configuration.
func TestImportTargetConfig_FailedSwitchRemovesItsTemp(t *testing.T) {
	targetDir := t.TempDir()
	writeTestFile(t, filepath.Join(targetDir, ".env-orbit"), "APP_URL=https://kept.example\n", 0o600)
	treeDir := t.TempDir()
	writeTestFile(t, filepath.Join(treeDir, ".env-orbit", "keep"), "k\n", 0o600)

	if err := ImportTargetConfig(treeDir, targetDir); err == nil {
		t.Fatal("expected an error when .env-orbit cannot be replaced")
	}
	if body, _ := os.ReadFile(filepath.Join(treeDir, ".env-orbit", "keep")); string(body) != "k\n" {
		t.Errorf("the directory in .env-orbit's place was disturbed: %q", body)
	}
	assertNoTemps(t, treeDir)
}

// The import tests below are root-proof counterparts of permission tests
// that skip themselves in CI: each forces its failure through the file
// system's shape, not its permissions.

// A target path that is a file, not a directory, is an error — not a
// fresh install with nothing to import.
func TestImportTargetConfig_TargetThatIsAFileIsAnErrorNotAFreshInstall(t *testing.T) {
	target := filepath.Join(t.TempDir(), "orbit")
	writeTestFile(t, target, "not a directory\n", 0o600)
	treeDir := t.TempDir()

	if err := ImportTargetConfig(treeDir, target); err == nil {
		t.Fatal("expected an error for a target that is a file")
	}
	if _, err := os.Lstat(filepath.Join(treeDir, ".env-orbit")); !os.IsNotExist(err) {
		t.Errorf("a failed import still seeded the tree (err=%v)", err)
	}
}

// If the target's secrets cannot be carried into the tree, the import
// fails rather than letting the session run without them.
func TestImportTargetConfig_SecretsThatCannotBeCarriedFailTheImport(t *testing.T) {
	t.Run("the tree's secrets path is taken by a file", func(t *testing.T) {
		targetDir := liveTarget(t)
		treeDir := t.TempDir()
		writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets"), "in the way\n", 0o600)

		if err := ImportTargetConfig(treeDir, targetDir); err == nil {
			t.Fatal("expected an error when the secrets cannot be carried into the tree")
		}
		assertLiveConfigUnchanged(t, targetDir)
	})

	t.Run("a secret cannot be written", func(t *testing.T) {
		targetDir := liveTarget(t)
		// A legal name whose staging temp is over the 255-byte limit.
		writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets", "zz-"+strings.Repeat("s", 247)), "x\n", 0o600)
		treeDir := t.TempDir()

		if err := ImportTargetConfig(treeDir, targetDir); err == nil {
			t.Fatal("expected an error when a secret cannot be carried into the tree")
		}
		assertNoTemps(t, treeDir)
	})
}
