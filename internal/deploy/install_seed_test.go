package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedListing(t *testing.T, tmp string) []string {
	t.Helper()
	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	return names
}

func TestBuildEngineCommandWithConfig_SeedsTheRunsTreeFromCollected(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	src := collectedTree(t)
	cmd, cleanup, err := BuildEngineCommandWithConfig([]byte("#!/usr/bin/env bash\n"), t.TempDir(), ActionInstall, src)
	if err != nil {
		t.Fatalf("BuildEngineCommandWithConfig: %v", err)
	}
	tree := ConfigTreeDir(cmd)
	if tree == "" {
		cleanup()
		t.Fatal("no config tree on the command")
	}
	requireMode(t, filepath.Join(tree, EnvFile), false, 0o600)
	requireMode(t, filepath.Join(tree, SecretsDir), true, 0o700)
	requireMode(t, filepath.Join(tree, SecretsDir, "oidc-client-secret"), false, 0o600)
	script := cmd.Args[len(cmd.Args)-1]
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	for _, p := range []string{tree, script} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("cleanup left %s: %v", p, err)
		}
	}
	if got := seedListing(t, tmp); len(got) != 0 {
		t.Errorf("temp holds %v after cleanup", got)
	}
}

func TestBuildEngineCommandWithConfig_UnstageableSeedLeavesNothingBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	empty := t.TempDir() // no .env-orbit
	cmd, cleanup, err := BuildEngineCommandWithConfig([]byte("#!/usr/bin/env bash\n"), t.TempDir(), ActionInstall, empty)
	if err == nil || !strings.Contains(err.Error(), "stage configuration tree") {
		t.Fatalf("error = %v, want one mentioning \"stage configuration tree\"", err)
	}
	if cmd != nil || cleanup != nil {
		t.Errorf("got cmd=%v cleanup=%v, want neither", cmd, cleanup != nil)
	}
	if got := seedListing(t, tmp); len(got) != 0 {
		t.Errorf("a failed seed left %v behind", got)
	}
}

func TestBuildEngineCommandWithConfig_NoCollectedGivesAnEmptyTree(t *testing.T) {
	cmd, cleanup, err := BuildEngineCommandWithConfig([]byte("#!/usr/bin/env bash\n"), t.TempDir(), ActionInstall, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	tree := ConfigTreeDir(cmd)
	if tree == "" {
		t.Fatal("no config tree on the command")
	}
	if entries, _ := os.ReadDir(tree); len(entries) != 0 {
		t.Errorf("config tree is not empty: %v", entries)
	}
}
