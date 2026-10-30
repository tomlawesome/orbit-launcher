package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// collectedSession makes a session tree holding .env-orbit and one secret.
func collectedSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile), []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, deploy.SecretsDir)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "token"), []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func collectedTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return tmp
}

func listCollected(t *testing.T, tmp string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(tmp, "orbit-launcher-collected-*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func requireCollectedMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode %o, want %o", path, got, want)
	}
}

func TestCollected_KeepCopiesTheSessionTreeIntoAPrivateTemp(t *testing.T) {
	tmp := collectedTemp(t)
	c := &collectedConfig{}
	t.Cleanup(c.release)
	if err := c.keep(collectedSession(t), deploy.AdoptConfig); err != nil {
		t.Fatalf("keep: %v", err)
	}
	dir := c.seed()
	if dir == "" || filepath.Dir(dir) != tmp || !strings.HasPrefix(filepath.Base(dir), "orbit-launcher-collected-") {
		t.Fatalf("seed = %q, want an orbit-launcher-collected-* directory in %s", dir, tmp)
	}
	requireCollectedMode(t, dir, 0o700)
	requireCollectedMode(t, filepath.Join(dir, deploy.EnvFile), 0o600)
	requireCollectedMode(t, filepath.Join(dir, deploy.SecretsDir), 0o700)
	requireCollectedMode(t, filepath.Join(dir, deploy.SecretsDir, "token"), 0o600)
}

func TestCollected_KeepAgainReplacesTheTree(t *testing.T) {
	collectedTemp(t)
	c := &collectedConfig{}
	t.Cleanup(c.release)
	if err := c.keep(collectedSession(t), deploy.AdoptConfig); err != nil {
		t.Fatal(err)
	}
	first := c.seed()
	if err := c.keep(collectedSession(t), deploy.AdoptConfig); err != nil {
		t.Fatal(err)
	}
	second := c.seed()
	if second == "" || second == first {
		t.Fatalf("seed after second keep = %q, first was %q", second, first)
	}
	if _, err := os.Lstat(first); !os.IsNotExist(err) {
		t.Errorf("the replaced tree was not removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(second, deploy.EnvFile)); err != nil {
		t.Errorf("the new tree lacks .env-orbit: %v", err)
	}
}

func TestCollected_FailedKeepLeavesTheEarlierTreeAndNoTempBehind(t *testing.T) {
	tmp := collectedTemp(t)
	c := &collectedConfig{}
	t.Cleanup(c.release)
	if err := c.keep(collectedSession(t), deploy.AdoptConfig); err != nil {
		t.Fatal(err)
	}
	first := c.seed()
	boom := errors.New("adopt failed")
	err := c.keep(collectedSession(t), func(string, string) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("keep error = %v, want %v", err, boom)
	}
	if got := c.seed(); got != first {
		t.Errorf("seed = %q after a failed keep, want %q", got, first)
	}
	if _, err := os.Lstat(filepath.Join(first, deploy.EnvFile)); err != nil {
		t.Errorf("the earlier tree was damaged: %v", err)
	}
	if left := listCollected(t, tmp); len(left) != 1 || left[0] != first {
		t.Errorf("temp holds %v, want only %s", left, first)
	}
}

func TestCollected_KeepFailsWhenTheTempDirIsMissing(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	c := &collectedConfig{}
	called := false
	err := c.keep(collectedSession(t), func(string, string) error { called = true; return nil })
	if err == nil {
		t.Fatal("keep succeeded with TMPDIR pointing at a missing directory")
	}
	if called {
		t.Error("adopt ran although no tree could be made")
	}
	if got := c.seed(); got != "" {
		t.Errorf("seed = %q, want empty", got)
	}
}

func TestCollected_ReleaseRemovesTheTreeAndIsSafeWhenEmptyOrNil(t *testing.T) {
	collectedTemp(t)
	c := &collectedConfig{}
	if err := c.keep(collectedSession(t), deploy.AdoptConfig); err != nil {
		t.Fatal(err)
	}
	dir := c.seed()
	c.release()
	if got := c.seed(); got != "" {
		t.Errorf("seed after release = %q, want empty", got)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("release left the tree: %v", err)
	}
	c.release() // already released
	(&collectedConfig{}).release()
	var nilc *collectedConfig
	nilc.release()
	if got := nilc.seed(); got != "" {
		t.Errorf("nil seed = %q, want empty", got)
	}
}

func TestCollected_ReleaseCollectedTreesRemovesEveryHeldTree(t *testing.T) {
	tmp := collectedTemp(t)
	a, b := &collectedConfig{}, &collectedConfig{}
	t.Cleanup(a.release)
	t.Cleanup(b.release)
	for _, c := range []*collectedConfig{a, b} {
		if err := c.keep(collectedSession(t), deploy.AdoptConfig); err != nil {
			t.Fatal(err)
		}
	}
	da, db := a.seed(), b.seed()
	if len(listCollected(t, tmp)) != 2 {
		t.Fatalf("expected two trees, got %v", listCollected(t, tmp))
	}
	releaseCollectedTrees()
	for _, d := range []string{da, db} {
		if _, err := os.Lstat(d); !os.IsNotExist(err) {
			t.Errorf("%s survived releaseCollectedTrees: %v", d, err)
		}
	}
	if a.seed() != "" || b.seed() != "" {
		t.Errorf("seeds not cleared: %q %q", a.seed(), b.seed())
	}
}
