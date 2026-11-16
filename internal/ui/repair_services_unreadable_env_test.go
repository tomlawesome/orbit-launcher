package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #338: expectedServices must not guess the profiles when .env-orbit
// exists but cannot be read; the block says it could not check.

// unreadableEnvModel is servicesModelUnread with the folder's .env-orbit
// replaced by a directory (unreadable, even as root); the reader itself
// answers healthily, so only the record can cause "could not check".
func unreadableEnvModel(t *testing.T) RepairModel {
	t.Helper()
	m := servicesModelUnread(t, "full", 80, 30, up("orbit-db", "orbit-app", "orbit-clamav", "orbit-tika", "orbit-ollama"), nil)
	env := filepath.Join(m.targetDir, ".env-orbit")
	if err := os.Remove(env); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(env, 0o700); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRepairServices_UnreadableEnvIsCouldNotCheck(t *testing.T) {
	m := servicesRead(t, unreadableEnvModel(t))
	if !strings.Contains(plain(m), "services · could not check") {
		t.Errorf("no `services · could not check` row for an unreadable .env-orbit:\n%s", screen(m))
	}
	if rows := svcRows(t, m); len(rows) != 0 {
		t.Errorf("per-service rows shown from a guessed list: %v", rows)
	}
}

func TestRepairServices_NoEnvRecordKeepsTheBaseThree(t *testing.T) {
	m := unreadableEnvModel(t)
	if err := os.Remove(filepath.Join(m.targetDir, ".env-orbit")); err != nil {
		t.Fatal(err)
	}
	m = servicesRead(t, m)
	assertRows(t, m, []svcRow{
		{"✓", "orbit-db", "running"}, {"✓", "orbit-app", "running"}, {"✓", "orbit-clamav", "running"},
	})
}
