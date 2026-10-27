package deploy

// The copy helpers behind AdoptConfig and the target import, on the
// branches the flows never reach: a source that is missing, a source
// that cannot be looked at, and a destination that cannot be made.
// Added for #295 so the coverage ratchet keeps its margin on CI.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyConfigFile_MissingSourceIsNothingToCopy(t *testing.T) {
	dir := t.TempDir()
	if err := copyConfigFile(filepath.Join(dir, "absent"), filepath.Join(dir, "out")); err != nil {
		t.Fatalf("copyConfigFile(missing source) = %v, want nil", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
		t.Fatalf("a missing source wrote a destination: %v", err)
	}
}

func TestCopyConfigFile_SourceUnderAFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A path under a regular file cannot be looked at (ENOTDIR): that is
	// an error, not "nothing to copy".
	if err := copyConfigFile(filepath.Join(file, EnvFile), filepath.Join(dir, "out")); err == nil {
		t.Fatal("copyConfigFile(source under a file) = nil, want an error")
	}
}

func TestRequireReplaceable_PathUnderAFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireReplaceable(filepath.Join(file, EnvFile)); err == nil {
		t.Fatal("requireReplaceable(path under a file) = nil, want an error")
	}
}

func TestCopySecretsDir_SourceUnderAFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	trust := func(string) error { return nil }
	if err := copySecretsDir(filepath.Join(file, SecretsDir), filepath.Join(dir, "out"), trust); err == nil {
		t.Fatal("copySecretsDir(source under a file) = nil, want an error")
	}
}

func TestCopySecretsDir_DestinationUnderAFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, SecretsDir)
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	trust := func(string) error { return nil }
	if err := copySecretsDir(src, filepath.Join(file, SecretsDir), trust); err == nil {
		t.Fatal("copySecretsDir(destination under a file) = nil, want an error")
	}
}
