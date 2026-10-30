package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// UntrustedPathError is a refusal to run or read something from a path
// that RequireTrustedPath does not trust. It names the path and the
// reason, for the failure screen.
type UntrustedPathError struct {
	Path   string
	Reason string
}

// Error puts the reason before the path: the failure screens cut a line
// at the terminal width, and a long path must not push the reason off.
func (e *UntrustedPathError) Error() string {
	return "refusing: " + e.Reason + " — " + e.Path
}

// RequireTrustedPath checks dir/rel before the launcher runs or reads it
// (#191) — the trusted-path check install.sh applies to its own files
// (is_real_non_symlink_directory, is_regular_non_symlink_file):
//
//   - dir is a real directory, not a symlink, and so is every directory
//     between it and rel;
//   - rel is a regular file, not a symlink;
//   - none of them is world-writable;
//   - all of them have dir's owner, and that owner is the user running
//     the launcher (which never runs as root, #291).
//
// Group-writable is accepted: install.sh writes with the operator's
// umask, and Debian and Ubuntu default to 0002 with user-private groups,
// so an ordinary install's files are 0664.
//
// A path that doesn't exist is reported as the underlying not-exist
// error, so callers can tell "absent" from "untrusted".
func RequireTrustedPath(dir, rel string) error {
	dir = filepath.Clean(dir)
	owner, err := trustedRoot(dir)
	if err != nil {
		return err
	}

	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	path := dir
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if i < len(parts)-1 {
			if _, err := checkTrustedDir(path, info, owner); err != nil {
				return err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return &UntrustedPathError{Path: path, Reason: "it is a symbolic link, not a regular file"}
		}
		if !info.Mode().IsRegular() {
			return &UntrustedPathError{Path: path, Reason: "it is not a regular file"}
		}
		if err := checkModeAndOwner(path, info, owner); err != nil {
			return err
		}
	}
	return nil
}

// launcherEUID is the user the launcher runs as, for the owner rule. A
// variable so tests can check that rule whoever runs the suite.
var launcherEUID = os.Geteuid

// trustedRoot checks dir itself — a real directory, not a symlink, not
// world-writable, owned by the launcher's user — and returns its owner. dir must already be Cleaned: Lstat
// follows a symlink named as "link/" or "link/.".
func trustedRoot(dir string) (int, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return 0, err
	}
	owner, err := checkTrustedDir(dir, info, -1)
	if err != nil {
		return 0, err
	}
	if err := launcherOwns(dir, owner); err != nil {
		return 0, err
	}
	return owner, nil
}

// launcherOwns is the owner rule alone: dir, owned by owner, belongs to
// the user running the launcher. There is no root exception: the
// launcher refuses to run as root (#291).
func launcherOwns(dir string, owner int) error {
	if euid := launcherEUID(); owner != euid {
		return &UntrustedPathError{Path: dir, Reason: fmt.Sprintf("it is owned by uid %d, not by the user running the launcher (uid %d)", owner, euid)}
	}
	return nil
}

// requireLauncherOwnsIfDir applies the owner rule to dir before anything
// in it is read, so another account's folder is refused as such rather
// than failing on a file the launcher cannot open (#277). A dir that is
// missing, a symlink or not a directory is left to the full check
// (trustedRoot) that follows wherever something is run from it.
func requireLauncherOwnsIfDir(dir string) error {
	dir = filepath.Clean(dir)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	return launcherOwns(dir, ownerOf(info))
}

// requireTrustedConfigDirs checks a deployment directory before its
// configuration is read, written or tidied: the directory itself, and
// .orbit-secrets when it exists (removeStaleTemps globs inside it). A
// directory that doesn't exist yet has nothing in it to trust.
func requireTrustedConfigDirs(dir string) error {
	dir = filepath.Clean(dir)
	owner, err := trustedRoot(dir)
	if err != nil {
		return err
	}
	secrets := filepath.Join(dir, SecretsDir)
	info, err := os.Lstat(secrets)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = checkTrustedDir(secrets, info, owner)
	return err
}

// requireTrustedCollectedTree is requireTrustedConfigDirs for a
// collected configuration tree before its answers are adopted (#304):
// the tree itself, and its .orbit-secrets when that is a directory. An
// .orbit-secrets that is a symlink or a file is left to planAdoption,
// which refuses it before anything is written, as a plain error.
func requireTrustedCollectedTree(tree string) error {
	tree = filepath.Clean(tree)
	owner, err := trustedRoot(tree)
	if err != nil {
		return err
	}
	secrets := filepath.Join(tree, SecretsDir)
	info, err := os.Lstat(secrets)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	_, err = checkTrustedDir(secrets, info, owner)
	return err
}

// orbitScripts are the scripts in a deployment's scripts/ directory that
// source one another: trusting one of them means trusting all that are
// there. configuration.sh and engine-check.sh stay on the list for
// deployments from before Orbit 5e9b1233, whose configure.sh still runs
// them; newer deployments lack them and are unaffected.
var orbitScripts = []string{"configure.sh", "configuration.sh", "installer-ui.sh", "engine-check.sh", "repair.sh"}

// requireTrustedScripts is RequireTrustedPath for scripts/<name>, plus
// each sibling it may source that is present, plus orbitImageFile when
// present: configure.sh (which repair.sh's check also runs) reads it.
func requireTrustedScripts(dir, name string) error {
	if err := RequireTrustedPath(dir, filepath.Join("scripts", name)); err != nil {
		return err
	}
	for _, sibling := range orbitScripts {
		if sibling == name {
			continue
		}
		if err := requireTrustedIfPresent(dir, filepath.Join("scripts", sibling)); err != nil {
			return err
		}
	}
	return requireTrustedIfPresent(dir, orbitImageFile)
}

// checkTrustedDir checks one directory and returns its owner. want is
// the owner it must have, or -1 for any.
func checkTrustedDir(path string, info os.FileInfo, want int) (int, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, &UntrustedPathError{Path: path, Reason: "it is a symbolic link, not a directory"}
	}
	if !info.IsDir() {
		return 0, &UntrustedPathError{Path: path, Reason: "it is not a directory"}
	}
	if err := checkModeAndOwner(path, info, want); err != nil {
		return 0, err
	}
	return ownerOf(info), nil
}

func checkModeAndOwner(path string, info os.FileInfo, want int) error {
	if info.Mode().Perm()&0o002 != 0 {
		return &UntrustedPathError{Path: path, Reason: "it is writable by everyone"}
	}
	if owner := ownerOf(info); want >= 0 && owner != want {
		return &UntrustedPathError{Path: path, Reason: fmt.Sprintf("it is owned by uid %d, not by the deployment's owner (uid %d)", owner, want)}
	}
	return nil
}

func ownerOf(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// requireTrustedIfPresent is RequireTrustedPath for a file that may
// legitimately be absent (a fresh install has no configuration yet).
func requireTrustedIfPresent(dir, rel string) error {
	if _, err := os.Lstat(filepath.Join(dir, rel)); os.IsNotExist(err) {
		return nil
	}
	return RequireTrustedPath(dir, rel)
}
