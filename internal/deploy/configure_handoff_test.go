package deploy

// #295: written from the issue alone, without the code. AdoptConfig is
// retargeted: it no longer writes into the deployment folder but puts the
// collected configuration (.env-orbit, 0600, and .orbit-secrets/, 0700 of
// 0600 files) into the retry's fresh configuration tree, the private
// directory stageInstall hands install.sh in ORBIT_LAUNCHER_CONFIG_TREE.
// Its MkdirAll of the destination is dropped: nothing is created there.
// The signature is taken to be unchanged, AdoptConfig(collected, dest).

import (
	"os"
	"path/filepath"
	"testing"
)

// collectedTree is what a guided configuration session leaves: settings
// and a secret, written with looser modes than the tree may hold, so the
// copy's own modes are what is checked.
func collectedTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, EnvFile), "APP_URL=https://orbit.example.test\n", 0o644)
	writeTestFile(t, filepath.Join(dir, SecretsDir, "oidc-client-secret"), "s3cret\n", 0o644)
	if err := os.Chmod(filepath.Join(dir, SecretsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func requireMode(t *testing.T, path string, dir bool, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	if info.IsDir() != dir || (!dir && !info.Mode().IsRegular()) {
		t.Errorf("%s: type %v, want dir=%t (regular file otherwise)", path, info.Mode().Type(), dir)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode %o, want %o", path, got, want)
	}
}

// Seeding the run's fresh configuration tree: the collected files land
// there whole, owner-only, as Orbit's non-interactive contract requires
// of a handed-in configuration.
// Against the old code this fails if the copy keeps the source's looser
// modes; the old code may already normalise them (see the report).
func TestAdoptConfigHandoff_SeedsTheFreshConfigTreeOwnerOnly(t *testing.T) {
	src := collectedTree(t)
	tree := t.TempDir()
	if err := os.Chmod(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := AdoptConfig(src, tree); err != nil {
		t.Fatalf("AdoptConfig into the run's configuration tree: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(tree, EnvFile)); err != nil || string(body) != "APP_URL=https://orbit.example.test\n" {
		t.Errorf("the tree's .env-orbit = %q, err=%v", body, err)
	}
	if body, err := os.ReadFile(filepath.Join(tree, SecretsDir, "oidc-client-secret")); err != nil || string(body) != "s3cret\n" {
		t.Errorf("the tree's secret = %q, err=%v", body, err)
	}
	requireMode(t, filepath.Join(tree, EnvFile), false, 0o600)
	requireMode(t, filepath.Join(tree, SecretsDir), true, 0o700)
	requireMode(t, filepath.Join(tree, SecretsDir, "oidc-client-secret"), false, 0o600)
	assertNoTemps(t, tree)
}

// The destination is never created: the old AdoptConfig ran
// os.MkdirAll(targetDir) and so made a deployment folder that did not
// exist (TestAdoptConfig_CreatesFreshTarget pins that old behaviour).
// Fails against the old code: brand-new exists afterwards.
func TestAdoptConfigHandoff_NeverCreatesTheDestination(t *testing.T) {
	src := collectedTree(t)
	dest := filepath.Join(t.TempDir(), "brand-new")
	_ = AdoptConfig(src, dest) // an error is fine; a created folder is not
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("AdoptConfig created the destination %s (err=%v); nothing may be created there", dest, err)
	}
}

// The collected tree is a session's answers, re-seeded into every later
// run's tree ("Install again" asks nothing), so seeding copies and never
// moves or changes it.
// Against the old code this passes if the old copy already left the
// source alone; it pins the property the re-seed depends on.
func TestAdoptConfigHandoff_LeavesTheCollectedTreeForTheNextRun(t *testing.T) {
	src := collectedTree(t)
	for i := 0; i < 2; i++ {
		tree := t.TempDir()
		if err := AdoptConfig(src, tree); err != nil {
			t.Fatalf("seed %d: %v", i+1, err)
		}
		if _, err := os.Stat(filepath.Join(tree, EnvFile)); err != nil {
			t.Fatalf("seed %d: no .env-orbit in the tree: %v", i+1, err)
		}
	}
	if body, err := os.ReadFile(filepath.Join(src, EnvFile)); err != nil || string(body) != "APP_URL=https://orbit.example.test\n" {
		t.Errorf("the collected .env-orbit was changed by seeding: %q err=%v", body, err)
	}
	if body, err := os.ReadFile(filepath.Join(src, SecretsDir, "oidc-client-secret")); err != nil || string(body) != "s3cret\n" {
		t.Errorf("the collected secret was changed by seeding: %q err=%v", body, err)
	}
}

// A collected tree with no secrets seeds only .env-orbit: no empty
// .orbit-secrets appears in the tree (the contract says "when it has
// one").
func TestAdoptConfigHandoff_NoSecretsSeedsNoSecretsDirectory(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, filepath.Join(src, EnvFile), "APP_URL=https://orbit.example.test\n", 0o600)
	tree := t.TempDir()
	if err := AdoptConfig(src, tree); err != nil {
		t.Fatalf("AdoptConfig: %v", err)
	}
	requireMode(t, filepath.Join(tree, EnvFile), false, 0o600)
	if _, err := os.Lstat(filepath.Join(tree, SecretsDir)); !os.IsNotExist(err) {
		t.Errorf("a .orbit-secrets was created in the tree from a session with none (err=%v)", err)
	}
}
