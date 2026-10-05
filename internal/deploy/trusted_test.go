package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// trustedDeployment is a deployment directory holding scripts/repair.sh,
// with modes set exactly (not through the umask).
func trustedDeployment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.Mkdir(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scripts, "repair.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o755, scripts: 0o755, script: 0o644} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// #191: nothing runs from a deployment unless the path is a real
// directory and a regular file, neither world-writable, consistently
// owned by the user running the launcher (or by anyone when it runs as
// root).
func TestRequireTrustedPath(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T) (dir string)
		reason  string // "" means accepted
	}{
		{
			name:    "happy path",
			prepare: trustedDeployment,
		},
		{
			name: "group-writable file accepted",
			prepare: func(t *testing.T) string {
				dir := trustedDeployment(t)
				// What install.sh writes under the Debian/Ubuntu umask 0002.
				chmod(t, filepath.Join(dir, "scripts"), 0o775)
				chmod(t, filepath.Join(dir, "scripts", "repair.sh"), 0o664)
				chmod(t, dir, 0o775)
				return dir
			},
		},
		{
			name: "symlinked dir",
			prepare: func(t *testing.T) string {
				link := filepath.Join(t.TempDir(), "deployment")
				if err := os.Symlink(trustedDeployment(t), link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			reason: "symbolic link",
		},
		{
			name: "symlinked script",
			prepare: func(t *testing.T) string {
				dir := trustedDeployment(t)
				elsewhere := filepath.Join(t.TempDir(), "other.sh")
				if err := os.WriteFile(elsewhere, []byte("#!/bin/bash\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				script := filepath.Join(dir, "scripts", "repair.sh")
				if err := os.Remove(script); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(elsewhere, script); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			reason: "symbolic link",
		},
		{
			name: "symlinked directory on the way to the script",
			prepare: func(t *testing.T) string {
				dir := trustedDeployment(t)
				scripts := filepath.Join(dir, "scripts")
				moved := filepath.Join(t.TempDir(), "scripts")
				if err := os.Rename(scripts, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, scripts); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			reason: "symbolic link",
		},
		{
			name: "world-writable script",
			prepare: func(t *testing.T) string {
				dir := trustedDeployment(t)
				chmod(t, filepath.Join(dir, "scripts", "repair.sh"), 0o666)
				return dir
			},
			reason: "writable by everyone",
		},
		{
			name: "world-writable dir",
			prepare: func(t *testing.T) string {
				dir := trustedDeployment(t)
				chmod(t, dir, 0o777)
				return dir
			},
			reason: "writable by everyone",
		},
		{
			name: "world-writable directory on the way to the script",
			prepare: func(t *testing.T) string {
				dir := trustedDeployment(t)
				chmod(t, filepath.Join(dir, "scripts"), 0o777)
				return dir
			},
			reason: "writable by everyone",
		},
		{
			name: "script is not a regular file",
			prepare: func(t *testing.T) string {
				dir := trustedDeployment(t)
				script := filepath.Join(dir, "scripts", "repair.sh")
				if err := os.Remove(script); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(script, 0o600); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			reason: "not a regular file",
		},
		{
			name: "dir is not a directory",
			prepare: func(t *testing.T) string {
				file := filepath.Join(t.TempDir(), "deployment")
				if err := os.WriteFile(file, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				return file
			},
			reason: "not a directory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.prepare(t)
			err := RequireTrustedPath(dir, "scripts/repair.sh")
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("refused a trusted path: %v", err)
				}
				return
			}
			var untrusted *UntrustedPathError
			if !errors.As(err, &untrusted) {
				t.Fatalf("err = %v, want an UntrustedPathError", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("err = %q, want the reason %q", err, tc.reason)
			}
			if !strings.HasPrefix(untrusted.Path, dir) {
				t.Errorf("error names %q, want a path under %q", untrusted.Path, dir)
			}
			if !strings.Contains(err.Error(), untrusted.Path) {
				t.Errorf("err = %q does not name the path", err)
			}
		})
	}
}

// The owner checks need a second uid, which only root can create files
// for.
func TestRequireTrustedPath_Owners(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown a file to another user; the owner checks run where the suite runs as root")
	}
	const nobody = 65534
	t.Run("file owner differs from dir owner", func(t *testing.T) {
		dir := trustedDeployment(t)
		if err := os.Lchown(filepath.Join(dir, "scripts", "repair.sh"), nobody, nobody); err != nil {
			t.Fatal(err)
		}
		err := RequireTrustedPath(dir, "scripts/repair.sh")
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) || !strings.Contains(err.Error(), "owned by uid") {
			t.Fatalf("err = %v, want an owner mismatch refusal", err)
		}
	})
	t.Run("root may run a deployment another user owns consistently", func(t *testing.T) {
		dir := trustedDeployment(t)
		for _, p := range []string{dir, filepath.Join(dir, "scripts"), filepath.Join(dir, "scripts", "repair.sh")} {
			if err := os.Lchown(p, nobody, nobody); err != nil {
				t.Fatal(err)
			}
		}
		if err := RequireTrustedPath(dir, "scripts/repair.sh"); err != nil {
			t.Fatalf("root refused a consistently owned deployment: %v", err)
		}
	})
}

func TestRequireTrustedPath_MissingFileIsNotExist(t *testing.T) {
	err := RequireTrustedPath(t.TempDir(), "scripts/repair.sh")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}

// Repair refuses an untrusted repair.sh instead of running it, and says
// so rather than calling the diagnosis unavailable.
func TestRepairCommand_RefusesAnUntrustedScript(t *testing.T) {
	dir := trustedDeployment(t)
	chmod(t, filepath.Join(dir, "scripts", "repair.sh"), 0o666)
	cmd, err := RepairCommand(dir, RepairPlan)
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) || cmd != nil {
		t.Fatalf("cmd = %v, err = %v; want a refusal", cmd, err)
	}
	if errors.Is(err, ErrRepairUnavailable) {
		t.Fatal("an untrusted script was reported as an absent one")
	}
}

func TestOpenConfigTree_RefusesAnUntrustedConfigure(t *testing.T) {
	tree := handedOverTree(t)
	chmod(t, filepath.Join(tree, "scripts", "configure.sh"), 0o666)
	_, err := OpenConfigTree(tree)
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestImportTargetConfig_RefusesUntrustedConfiguration(t *testing.T) {
	t.Run("world-writable .env-orbit", func(t *testing.T) {
		target := t.TempDir()
		env := filepath.Join(target, ".env-orbit")
		if err := os.WriteFile(env, []byte("APP_URL=x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		chmod(t, env, 0o666)
		tree := t.TempDir()
		err := ImportTargetConfig(tree, target)
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) {
			t.Fatalf("err = %v, want a refusal", err)
		}
		if _, err := os.Lstat(filepath.Join(tree, ".env-orbit")); !os.IsNotExist(err) {
			t.Error("an untrusted .env-orbit was imported anyway")
		}
	})
	t.Run("world-writable secrets directory", func(t *testing.T) {
		target := t.TempDir()
		secrets := filepath.Join(target, ".orbit-secrets")
		if err := os.Mkdir(secrets, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(secrets, "oidc-client-secret"), []byte("s\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		chmod(t, secrets, 0o777)
		err := ImportTargetConfig(t.TempDir(), target)
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})
	t.Run("world-writable target", func(t *testing.T) {
		target := t.TempDir()
		if err := os.WriteFile(filepath.Join(target, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		chmod(t, target, 0o777)
		err := ImportTargetConfig(t.TempDir(), target)
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})
}
