package ui

import (
	"os"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// TestMain gives every test in the package a machine with plenty of
// memory, so the install table (#260) reads the same on any host. Tests
// that need another machine set deploy.ReadHostMemory themselves.
func TestMain(m *testing.M) {
	deploy.ReadHostMemory = func() (uint64, bool) { return 16 << 30, true }
	os.Exit(m.Run())
}
