package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// trustedDeployment is a deployment directory holding scripts/repair.sh,
// with modes set exactly (not through the umask).
func trustedDeployment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	chmod(t, dir, 0o755)
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	scriptTree(t, dir, 0o755, testutil.File{Path: "scripts/repair.sh", Body: "#!/usr/bin/env bash\n", Mode: 0o644})
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
			name: "symlinked dir with a trailing slash",
			prepare: func(t *testing.T) string {
				link := filepath.Join(t.TempDir(), "deployment")
				if err := os.Symlink(trustedDeployment(t), link); err != nil {
					t.Fatal(err)
				}
				return link + "/"
			},
			reason: "symbolic link",
		},
		{
			name: "symlinked dir with a trailing /.",
			prepare: func(t *testing.T) string {
				link := filepath.Join(t.TempDir(), "deployment")
				if err := os.Symlink(trustedDeployment(t), link); err != nil {
					t.Fatal(err)
				}
				return link + "/."
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
			if !strings.HasPrefix(untrusted.Path, filepath.Clean(dir)) {
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

// Owner decision 19a: a missing .env-orbit no longer stops the script, but
// the script must still come from a trusted path. An untrusted scripts/
// directory or script is refused, not reported as "no deployment".
func TestRepairCommand_ScriptWithoutEnvFileStillNeedsATrustedPath(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"world-writable scripts directory": func(t *testing.T, dir string) {
			chmod(t, filepath.Join(dir, "scripts"), 0o777)
		},
		"symlinked script": func(t *testing.T, dir string) {
			elsewhere := filepath.Join(t.TempDir(), "repair.sh")
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
		},
	}
	for name, untrust := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			chmod(t, dir, 0o755)
			scriptTree(t, dir, 0o755, testutil.File{Path: "scripts/repair.sh", Body: "#!/usr/bin/env bash\n", Mode: 0o644})
			untrust(t, dir)
			cmd, err := RepairCommand(dir, RepairPlan)
			var untrusted *UntrustedPathError
			if !errors.As(err, &untrusted) {
				t.Fatalf("err = %v, want an *UntrustedPathError", err)
			}
			if errors.Is(err, ErrNoDeployment) {
				t.Error("an untrusted script was reported as no deployment")
			}
			if cmd != nil {
				t.Error("an untrusted script returned a command")
			}
		})
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

// The owner branch without root: a system directory root owns is not
// the launcher user's deployment.
func TestRequireTrustedPath_RootOwnedPathRefusedForAnotherUser(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: root may use any consistently owned path")
	}
	err := RequireTrustedPath("/usr", "bin/env")
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) || !strings.Contains(err.Error(), "not by the user running the launcher") {
		t.Fatalf("err = %v, want the owner refusal", err)
	}
}

// symlinkSibling replaces dir/scripts/name with a symlink to a script
// elsewhere.
func symlinkSibling(t *testing.T, dir, name string) {
	t.Helper()
	elsewhere := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(elsewhere, []byte("#!/bin/bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "scripts", name)); err != nil {
		t.Fatal(err)
	}
}

// repair.sh and configure.sh source their siblings in scripts/, so a
// trusted script next to an untrusted sibling is still refused.
func TestRepairCommand_RefusesAnUntrustedSibling(t *testing.T) {
	for _, sibling := range []string{"configure.sh", "installer-ui.sh"} {
		t.Run(sibling, func(t *testing.T) {
			dir := trustedDeployment(t)
			symlinkSibling(t, dir, sibling)
			_, err := RepairCommand(dir, RepairPlan)
			var untrusted *UntrustedPathError
			if !errors.As(err, &untrusted) || filepath.Base(untrusted.Path) != sibling {
				t.Fatalf("err = %v, want a refusal naming %s", err, sibling)
			}
		})
	}
}

func TestOpenConfigTree_RefusesAnUntrustedSibling(t *testing.T) {
	for _, sibling := range []string{"installer-ui.sh", "repair.sh"} {
		t.Run(sibling, func(t *testing.T) {
			tree := handedOverTree(t)
			if err := os.Remove(filepath.Join(tree, "scripts", sibling)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			symlinkSibling(t, tree, sibling)
			_, err := OpenConfigTree(tree)
			var untrusted *UntrustedPathError
			if !errors.As(err, &untrusted) || filepath.Base(untrusted.Path) != sibling {
				t.Fatalf("err = %v, want a refusal naming %s", err, sibling)
			}
		})
	}
}

// #204: Orbit deleted scripts/configuration.sh, so it is no longer one
// of the scripts whose trust is checked. A stray one left in a tree —
// even a symlink — is not a reason to refuse it.
func TestTrustCheck_StrayConfigurationScriptIsNotOrbits(t *testing.T) {
	t.Run("repair", func(t *testing.T) {
		dir := trustedDeployment(t)
		symlinkSibling(t, dir, "configuration.sh")
		cmd, err := RepairCommand(dir, RepairPlan)
		if err != nil {
			t.Fatalf("RepairCommand refused a stray configuration.sh: %v", err)
		}
		if cmd == nil {
			t.Fatal("RepairCommand returned no command")
		}
	})
	t.Run("configure tree", func(t *testing.T) {
		tree := handedOverTree(t)
		symlinkSibling(t, tree, "configuration.sh")
		endSession, err := OpenConfigTree(tree)
		if err != nil {
			t.Fatalf("OpenConfigTree refused a stray configuration.sh: %v", err)
		}
		if endSession == nil {
			t.Fatal("OpenConfigTree returned no endSession")
		}
		endSession()
	})
}

// orbitImageTree is a handed-over configure tree with an .orbit-image
// pin at its root, written with mode exactly.
func orbitImageTree(t *testing.T, mode os.FileMode) (tree, image string) {
	t.Helper()
	tree = handedOverTree(t)
	image = filepath.Join(tree, ".orbit-image")
	writeTestFile(t, image, "ghcr.io/tomlawesome/orbit@sha256:abc\n", mode)
	return tree, image
}

// #204: configure.sh reads the first line of .orbit-image to pick the
// image it pulls and runs, so a present .orbit-image passes the same
// checks as the tree's scripts; an absent one is fine (older trees).
func TestOpenConfigTree_ChecksOrbitImage(t *testing.T) {
	accepted := map[string]func(t *testing.T) string{
		"absent": func(t *testing.T) string { return handedOverTree(t) },
		"owner-only": func(t *testing.T) string {
			tree, _ := orbitImageTree(t, 0o600)
			return tree
		},
		"world-readable": func(t *testing.T) string {
			tree, _ := orbitImageTree(t, 0o644)
			return tree
		},
		"group-writable, as the scripts may be": func(t *testing.T) string {
			tree, _ := orbitImageTree(t, 0o664)
			return tree
		},
	}
	for name, prepare := range accepted {
		t.Run("accepted: "+name, func(t *testing.T) {
			endSession, err := OpenConfigTree(prepare(t))
			if err != nil {
				t.Fatalf("OpenConfigTree: %v", err)
			}
			if endSession == nil {
				t.Fatal("OpenConfigTree returned no endSession")
			}
			endSession()
		})
	}

	refused := []struct {
		name    string
		prepare func(t *testing.T) (tree, image string)
		reason  string
	}{
		{
			name: "symbolic link",
			prepare: func(t *testing.T) (string, string) {
				tree := handedOverTree(t)
				elsewhere := filepath.Join(t.TempDir(), "orbit-image")
				writeTestFile(t, elsewhere, "ghcr.io/attacker/orbit:latest\n", 0o644)
				image := filepath.Join(tree, ".orbit-image")
				if err := os.Symlink(elsewhere, image); err != nil {
					t.Fatal(err)
				}
				return tree, image
			},
			reason: "symbolic link",
		},
		{
			name: "world-writable",
			prepare: func(t *testing.T) (string, string) {
				return orbitImageTree(t, 0o666)
			},
			reason: "writable by everyone",
		},
		{
			name: "not a regular file",
			prepare: func(t *testing.T) (string, string) {
				tree := handedOverTree(t)
				image := filepath.Join(tree, ".orbit-image")
				if err := syscall.Mkfifo(image, 0o600); err != nil {
					t.Fatal(err)
				}
				return tree, image
			},
			reason: "not a regular file",
		},
	}
	for _, tc := range refused {
		t.Run("refused: "+tc.name, func(t *testing.T) {
			tree, image := tc.prepare(t)
			endSession, err := OpenConfigTree(tree)
			var untrusted *UntrustedPathError
			if !errors.As(err, &untrusted) {
				if endSession != nil {
					endSession()
				}
				t.Fatalf("err = %v, want an *UntrustedPathError naming %s", err, image)
			}
			if endSession != nil {
				t.Error("a refused tree returned a session")
			}
			if untrusted.Path != image {
				t.Errorf("refusal names %q, want %q", untrusted.Path, image)
			}
			if !strings.Contains(err.Error(), image) {
				t.Errorf("err = %q does not name %s", err, image)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("err = %q, want the reason %q", err, tc.reason)
			}
		})
	}
}

// #204: repair.sh reads .orbit-image the same way configure.sh does, so
// Repair's command checks a present .orbit-image at the deployment root
// like the scripts; an absent one is fine (older deployments).
func TestRepairCommand_ChecksOrbitImage(t *testing.T) {
	deploymentWithImage := func(t *testing.T, mode os.FileMode) string {
		dir := trustedDeployment(t)
		writeTestFile(t, filepath.Join(dir, ".orbit-image"), "ghcr.io/tomlawesome/orbit@sha256:abc\n", mode)
		return dir
	}

	accepted := map[string]func(t *testing.T) string{
		"absent": func(t *testing.T) string { return trustedDeployment(t) },
		"owner-only": func(t *testing.T) string {
			return deploymentWithImage(t, 0o600)
		},
		"world-readable": func(t *testing.T) string {
			return deploymentWithImage(t, 0o644)
		},
		"group-writable, as the scripts may be": func(t *testing.T) string {
			return deploymentWithImage(t, 0o664)
		},
	}
	for name, prepare := range accepted {
		t.Run("accepted: "+name, func(t *testing.T) {
			cmd, err := RepairCommand(prepare(t), RepairPlan)
			if err != nil {
				t.Fatalf("RepairCommand: %v", err)
			}
			if cmd == nil {
				t.Fatal("RepairCommand returned no command")
			}
		})
	}

	refused := []struct {
		name    string
		prepare func(t *testing.T) (dir, image string)
	}{
		{
			name: "symbolic link",
			prepare: func(t *testing.T) (string, string) {
				dir := trustedDeployment(t)
				elsewhere := filepath.Join(t.TempDir(), "orbit-image")
				writeTestFile(t, elsewhere, "ghcr.io/attacker/orbit:latest\n", 0o644)
				image := filepath.Join(dir, ".orbit-image")
				if err := os.Symlink(elsewhere, image); err != nil {
					t.Fatal(err)
				}
				return dir, image
			},
		},
		{
			name: "world-writable",
			prepare: func(t *testing.T) (string, string) {
				dir := deploymentWithImage(t, 0o666)
				return dir, filepath.Join(dir, ".orbit-image")
			},
		},
		{
			name: "not a regular file",
			prepare: func(t *testing.T) (string, string) {
				dir := trustedDeployment(t)
				image := filepath.Join(dir, ".orbit-image")
				if err := syscall.Mkfifo(image, 0o600); err != nil {
					t.Fatal(err)
				}
				return dir, image
			},
		},
	}
	for _, tc := range refused {
		t.Run("refused: "+tc.name, func(t *testing.T) {
			dir, image := tc.prepare(t)
			cmd, err := RepairCommand(dir, RepairPlan)
			var untrusted *UntrustedPathError
			if !errors.As(err, &untrusted) {
				t.Fatalf("err = %v, want an *UntrustedPathError naming %s", err, image)
			}
			if cmd != nil {
				t.Error("a refused deployment returned a command")
			}
			if untrusted.Path != image {
				t.Errorf("refusal names %q, want %q", untrusted.Path, image)
			}
		})
	}
}

// The owner check on .orbit-image needs a second uid, which only root
// can create files for.
func TestOpenConfigTree_RefusesAnOrbitImageAnotherUserOwns(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown a file to another user; the owner checks run where the suite runs as root")
	}
	const nobody = 65534
	tree, image := orbitImageTree(t, 0o644)
	if err := os.Lchown(image, nobody, nobody); err != nil {
		t.Fatal(err)
	}
	_, err := OpenConfigTree(tree)
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) || untrusted.Path != image || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("err = %v, want an owner mismatch refusal naming %s", err, image)
	}
}

// #204: the end of a configure session removes what the session
// collected (.env-orbit, .orbit-secrets) and nothing else: the
// .orbit-image pin and the scripts stay for a second attempt.
func TestOpenConfigTree_EndSessionKeepsOrbitImage(t *testing.T) {
	tree, image := orbitImageTree(t, 0o600)
	endSession, err := OpenConfigTree(tree)
	if err != nil {
		t.Fatalf("OpenConfigTree: %v", err)
	}
	writeTestFile(t, filepath.Join(tree, ".env-orbit"), "APP_URL=x\n", 0o600)
	writeTestFile(t, filepath.Join(tree, ".orbit-secrets", "oidc-client-secret"), "s\n", 0o600)
	endSession()
	for _, gone := range []string{".env-orbit", ".orbit-secrets"} {
		if _, err := os.Lstat(filepath.Join(tree, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the end of the session", gone)
		}
	}
	got, err := os.ReadFile(image)
	if err != nil {
		t.Fatalf(".orbit-image was removed with the session: %v", err)
	}
	if string(got) != "ghcr.io/tomlawesome/orbit@sha256:abc\n" {
		t.Errorf(".orbit-image changed at the end of the session: %q", got)
	}
	for _, kept := range []string{"scripts/configure.sh", "scripts/installer-ui.sh", ".env-orbit.example"} {
		if _, err := os.Lstat(filepath.Join(tree, kept)); err != nil {
			t.Errorf("%s was removed with the session: %v", kept, err)
		}
	}
}

// Absent siblings are fine: not every Orbit line ships all of them.
func TestRepairCommand_AbsentSiblingsAreFine(t *testing.T) {
	if _, err := RepairCommand(trustedDeployment(t), RepairPlan); err != nil {
		t.Fatalf("RepairCommand: %v", err)
	}
}

// removeStaleTemps globs inside .orbit-secrets; through a symlink it
// would delete files somewhere else entirely.
func TestImportTargetConfig_DeletesNothingThroughASymlinkedSecretsDir(t *testing.T) {
	for name, run := range map[string]func(tree, target string) error{
		"import": func(tree, target string) error { return ImportTargetConfig(tree, target) },
		"adopt":  func(tree, target string) error { return AdoptConfig(tree, target) },
	} {
		t.Run(name, func(t *testing.T) {
			elsewhere := t.TempDir()
			bystander := filepath.Join(elsewhere, "keep.tmp-123")
			if err := os.WriteFile(bystander, []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if err := os.Symlink(elsewhere, filepath.Join(target, ".orbit-secrets")); err != nil {
				t.Fatal(err)
			}
			tree := t.TempDir()
			if err := os.WriteFile(filepath.Join(tree, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := run(tree, target)
			var untrusted *UntrustedPathError
			if !errors.As(err, &untrusted) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if _, err := os.Stat(bystander); err != nil {
				t.Fatalf("a file outside the target was deleted: %v", err)
			}
		})
	}
}

func TestAdoptConfig_RefusesASymlinkedTargetWithNoConfiguration(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := AdoptConfig(tree, link)
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Errorf("configuration was written through the symlink: %v", entries)
	}
}

func TestImportTargetConfig_RefusesAWorldWritableTargetWithNoConfiguration(t *testing.T) {
	target := t.TempDir()
	chmod(t, target, 0o777)
	err := ImportTargetConfig(t.TempDir(), target)
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

// The failure screens cut a line at the terminal width, so the reason
// comes before the (long) path.
func TestUntrustedPathError_ReasonBeforePath(t *testing.T) {
	err := &UntrustedPathError{Path: "/a/very/long/deployment/scripts/repair.sh", Reason: "it is writable by everyone"}
	if got, want := err.Error(), "refusing: it is writable by everyone — /a/very/long/deployment/scripts/repair.sh"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// asLauncherUser makes RequireTrustedPath see the launcher running as
// uid, so the owner rules are tested whoever runs the suite (CI runs
// as root, a developer usually doesn't).
func asLauncherUser(t *testing.T, uid int) {
	t.Helper()
	saved := launcherEUID
	launcherEUID = func() int { return uid }
	t.Cleanup(func() { launcherEUID = saved })
}

func TestRequireTrustedPath_OwnerRules(t *testing.T) {
	t.Run("a deployment another user owns is refused", func(t *testing.T) {
		dir := trustedDeployment(t)
		owner := os.Geteuid()
		asLauncherUser(t, owner+1)
		err := RequireTrustedPath(dir, "scripts/repair.sh")
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) || untrusted.Path != dir || !strings.Contains(err.Error(), "not by the user running the launcher") {
			t.Fatalf("err = %v, want the owner refusal naming %s", err, dir)
		}
	})
	t.Run("root may run a deployment another user owns", func(t *testing.T) {
		dir := trustedDeployment(t)
		asLauncherUser(t, 0)
		if err := RequireTrustedPath(dir, "scripts/repair.sh"); err != nil {
			t.Fatalf("root refused a consistently owned deployment: %v", err)
		}
	})
	t.Run("the same user's deployment is accepted", func(t *testing.T) {
		dir := trustedDeployment(t)
		asLauncherUser(t, os.Geteuid())
		if err := RequireTrustedPath(dir, "scripts/repair.sh"); err != nil {
			t.Fatalf("refused the launcher user's own deployment: %v", err)
		}
	})
}

// Repair, the configure tree and the configuration import all apply
// the owner rule, not only RequireTrustedPath on its own.
func TestOwnerRuleGuardsEveryEntryPoint(t *testing.T) {
	asLauncherUser(t, os.Geteuid()+1)
	var untrusted *UntrustedPathError
	if _, err := RepairCommand(trustedDeployment(t), RepairPlan); !errors.As(err, &untrusted) {
		t.Errorf("RepairCommand: err = %v, want the owner refusal", err)
	}
	if _, err := OpenConfigTree(handedOverTree(t)); !errors.As(err, &untrusted) {
		t.Errorf("OpenConfigTree: err = %v, want the owner refusal", err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	if err := ImportTargetConfig(tree, target); !errors.As(err, &untrusted) {
		t.Errorf("ImportTargetConfig: err = %v, want the owner refusal", err)
	}
	if _, err := os.Lstat(filepath.Join(tree, ".env-orbit")); !os.IsNotExist(err) {
		t.Error("another user's configuration was imported")
	}
	if err := os.WriteFile(filepath.Join(tree, ".env-orbit"), []byte("APP_URL=y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AdoptConfig(tree, target); !errors.As(err, &untrusted) {
		t.Errorf("AdoptConfig: err = %v, want the owner refusal", err)
	}
	if got, _ := os.ReadFile(filepath.Join(target, ".env-orbit")); string(got) != "APP_URL=x\n" {
		t.Errorf("another user's configuration was replaced: %q", got)
	}
	fakeDockerLogging(t) // a regressed guard must never reach the real docker
	if err := StandDown(t.Context(), trustedDeployment(t)); !errors.As(err, &untrusted) {
		t.Errorf("StandDown: err = %v, want the owner refusal", err)
	}
}

// Stand-down applies the #191 trusted-path rule to the deployment
// directory and its .env-orbit before it runs anything: a refusal means
// docker was never started.
func TestStandDown_RefusesAnUntrustedDeployment(t *testing.T) {
	// dockerNeverRan fails unless the fake docker's call log is absent or empty.
	dockerNeverRan := func(t *testing.T, callLog string) {
		t.Helper()
		got, err := os.ReadFile(callLog)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("docker ran despite the refusal: %q", got)
		}
	}
	refused := func(t *testing.T, err error, path string) *UntrustedPathError {
		t.Helper()
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) {
			t.Fatalf("err = %v, want a *UntrustedPathError", err)
		}
		if untrusted.Path != path {
			t.Errorf("refusal names %q, want %q", untrusted.Path, path)
		}
		return untrusted
	}

	t.Run("world-writable deployment directory", func(t *testing.T) {
		dir := trustedDeployment(t)
		chmod(t, dir, 0o777)
		callLog := fakeDockerLogging(t)
		err := StandDown(t.Context(), dir)
		refused(t, err, dir)
		dockerNeverRan(t, callLog)
	})
	t.Run("world-writable .env-orbit", func(t *testing.T) {
		dir := trustedDeployment(t)
		env := filepath.Join(dir, EnvFile)
		chmod(t, env, 0o666)
		callLog := fakeDockerLogging(t)
		err := StandDown(t.Context(), dir)
		refused(t, err, env)
		dockerNeverRan(t, callLog)
	})
	t.Run(".env-orbit is a symbolic link", func(t *testing.T) {
		dir := trustedDeployment(t)
		env := filepath.Join(dir, EnvFile)
		real := filepath.Join(t.TempDir(), "elsewhere-env")
		testutil.WriteEnvOrbit(t, real)
		if err := os.Remove(env); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, env); err != nil {
			t.Fatal(err)
		}
		callLog := fakeDockerLogging(t)
		err := StandDown(t.Context(), dir)
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) {
			t.Fatalf("err = %v, want a *UntrustedPathError", err)
		}
		dockerNeverRan(t, callLog)
	})
	t.Run("deployment directory owned by another user", func(t *testing.T) {
		dir := trustedDeployment(t)
		asLauncherUser(t, os.Geteuid()+1)
		callLog := fakeDockerLogging(t)
		err := StandDown(t.Context(), dir)
		refused(t, err, dir)
		if !strings.Contains(err.Error(), "not by the user running the launcher") {
			t.Errorf("err = %v, want the owner refusal", err)
		}
		dockerNeverRan(t, callLog)
	})
	t.Run("root may stand down a deployment another user owns", func(t *testing.T) {
		dir := trustedDeployment(t)
		asLauncherUser(t, 0)
		callLog := fakeDockerLogging(t)
		if err := StandDown(t.Context(), dir); err != nil {
			t.Fatalf("root was refused a consistently owned deployment: %v", err)
		}
		assertOneComposeDown(t, callLog, dir)
	})
}

// The refusal must not fire on a good deployment.
func TestStandDown_RunsATrustedDeployment(t *testing.T) {
	dir := trustedDeployment(t)
	callLog := fakeDockerLogging(t)
	if err := StandDown(t.Context(), dir); err != nil {
		t.Fatalf("a trusted deployment was refused: %v", err)
	}
	assertOneComposeDown(t, callLog, dir)
}

// assertOneComposeDown checks the fake docker was called exactly once,
// with the stand-down arguments for dir.
func assertOneComposeDown(t *testing.T, callLog, dir string) {
	t.Helper()
	got, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("docker was never called: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	want := "compose --project-directory " + dir + " --env-file " + dir + "/.env-orbit down"
	if len(lines) != 1 || !strings.Contains(lines[0], want) {
		t.Errorf("docker calls = %q, want one line containing %q", lines, want)
	}
}

func TestRequireTrustedPath_MissingDirIsNotExist(t *testing.T) {
	err := RequireTrustedPath(filepath.Join(t.TempDir(), "gone"), "scripts/repair.sh")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}

// A target that is a file is an error, not "this deployment has no
// repair diagnosis" or "no deployment here": the person pointed the
// launcher at the wrong path. Detect reads it first, so the error names
// the deployment read (#237).
func TestRepairCommand_TargetThatIsAFileIsAnError(t *testing.T) {
	target := filepath.Join(t.TempDir(), "orbit")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := RepairCommand(target, RepairPlan)
	if err == nil || errors.Is(err, ErrRepairUnavailable) || errors.Is(err, ErrNoDeployment) || !strings.Contains(err.Error(), "read the deployment") {
		t.Fatalf("err = %v, want an error naming the deployment read", err)
	}
}

// Likewise a configure tree path that is a file is broken, not merely
// an install.sh that handed nothing over.
func TestOpenConfigTree_TreeThatIsAFileIsAnError(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "tree")
	if err := os.WriteFile(tree, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenConfigTree(tree)
	if err == nil || errors.Is(err, ErrNoConfigTree) || !strings.Contains(err.Error(), "configure.sh") {
		t.Fatalf("err = %v, want an error naming configure.sh", err)
	}
}

// A target that doesn't exist yet (a fresh install into a new
// directory) has nothing to import and nothing to distrust.
func TestImportTargetConfig_MissingTargetImportsNothing(t *testing.T) {
	tree := t.TempDir()
	if err := ImportTargetConfig(tree, filepath.Join(t.TempDir(), "new")); err != nil {
		t.Fatalf("ImportTargetConfig: %v", err)
	}
	if entries, _ := os.ReadDir(tree); len(entries) != 0 {
		t.Errorf("imported %v from a target that does not exist", entries)
	}
}

// Each secret is checked on its own: a trusted .orbit-secrets can still
// hold one file anybody could have written.
func TestImportTargetConfig_RefusesAWorldWritableSecret(t *testing.T) {
	target := t.TempDir()
	secrets := filepath.Join(target, ".orbit-secrets")
	if err := os.Mkdir(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(secrets, "oidc-client-secret")
	if err := os.WriteFile(secret, []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chmod(t, secret, 0o666)
	tree := t.TempDir()
	err := ImportTargetConfig(tree, target)
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) || untrusted.Path != secret {
		t.Fatalf("err = %v, want a refusal naming %s", err, secret)
	}
	if _, err := os.Lstat(filepath.Join(tree, ".orbit-secrets", "oidc-client-secret")); !os.IsNotExist(err) {
		t.Error("the untrusted secret was imported")
	}
}
