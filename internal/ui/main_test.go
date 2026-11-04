package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// TestMain gives every test in the package a machine with plenty of
// memory, so the install table (#260) reads the same on any host. Tests
// that need another machine set deploy.ReadHostMemory themselves.
func TestMain(m *testing.M) {
	deploy.ReadHostMemory = func() (uint64, bool) { return 16 << 30, true }
	base := "/tmp"
	if fi, err := os.Stat(base); err != nil || !fi.IsDir() {
		base = ""
	}
	elsewhere, err := os.MkdirTemp(base, "oe")
	if err != nil {
		panic(err)
	}
	otherProduction = filepath.Join(elsewhere, "production")
	if err := os.Mkdir(otherProduction, 0o700); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(elsewhere)
	// Runs built outside an InstallModel keep their own collected trees.
	releaseCollectedTrees()
	os.Exit(code)
}
