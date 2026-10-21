// Package deploy orchestrates Docker/Compose: profile and compose-file
// selection, health probes, and stand-down. Owns every side effect on the
// host so internal/ui only ever owns rendering and input.
package deploy

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// errNotRegularFile is why Detect refuses an EnvFile that is a pipe, a
// device or a directory.
var errNotRegularFile = errors.New("not a regular file")

// The deployment's own files, named once here (#208): everything that
// reads, writes, stands down or describes a deployment spells them
// through these, the UI's wording included.
const (
	// EnvFile is the deployment's configuration, in its target
	// directory: a Compose env file Compose never loads on its own.
	EnvFile = ".env-orbit"
	// SecretsDir is the directory beside EnvFile holding one file per
	// secret.
	SecretsDir = ".orbit-secrets"
)

// orbitImageFile names the Orbit image configure.sh pulls and runs its
// configuration engine in, at the root beside EnvFile. Choosing what
// runs makes it as sensitive as the scripts themselves.
const orbitImageFile = ".orbit-image"

// scriptsDir is where a deployment, and a configuration tree shaped like
// one, keep orbit's scripts.
const scriptsDir = "scripts"

// Deployment describes a recognised existing Orbit install, read from its
// .env-orbit file — see orbit's .env-orbit.example for the authoritative
// format this parses a practical subset of.
type Deployment struct {
	TargetDir string
	AppURL    string
	Profiles  []string
	Image     string

	// Project is the Compose project name, which prefixes the names of
	// the deployment's volumes. The installer persists the name it used
	// as COMPOSE_PROJECT_NAME in .env-orbit, so this is read, not
	// guessed; without that key Compose falls back to the compose
	// file's own top-level name, defaultComposeProject.
	Project string

	// Version is orbit's own applied version (ORBIT_CONFIG_APPLIED_VERSION,
	// e.g. "v1.2.0"), recorded by the installer's configuration
	// migration — shown in the splash's version foot.
	Version string
}

// defaultComposeProject is the top-level `name:` in orbit's
// docker-compose.yml: the project Compose uses when .env-orbit sets no
// COMPOSE_PROJECT_NAME, the same precedence orbit's installer applies.
const defaultComposeProject = "orbit"

// Detect looks for a recognised Orbit deployment in targetDir. It returns
// (nil, nil) — not an error — when there simply isn't one there; an error
// return means something went wrong trying to read a file that exists.
func Detect(targetDir string) (*Deployment, error) {
	envPath := filepath.Join(targetDir, EnvFile)
	// Opened without blocking and then checked: a named pipe someone left
	// as .env-orbit would otherwise hang the open, and the launcher with
	// it. Anything but a regular file is refused as unreadable.
	f, err := os.OpenFile(envPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, &fs.PathError{Op: "read", Path: envPath, Err: errNotRegularFile}
	}

	d := &Deployment{TargetDir: targetDir, Project: defaultComposeProject}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = envValue(value)
		switch strings.TrimSpace(key) {
		case "APP_URL":
			d.AppURL = value
		case "ORBIT_IMAGE":
			d.Image = value
		case "COMPOSE_PROFILES":
			d.Profiles = splitProfiles(value)
		case "COMPOSE_PROJECT_NAME":
			if value != "" {
				d.Project = value
			}
		case "ORBIT_CONFIG_APPLIED_VERSION":
			d.Version = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return d, nil
}

// envValue is the one rule for an EnvFile value, Compose's own for an
// env file: the surrounding space is trimmed, then one matching pair of
// quotes comes off. Space inside the quotes, any other quote and any
// "=" are part of the value and stay.
func envValue(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// splitProfiles splits an already cleaned COMPOSE_PROFILES value into
// its profile names.
func splitProfiles(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var profiles []string
	for _, p := range strings.Split(value, ",") {
		if p = strings.TrimSpace(p); p != "" {
			profiles = append(profiles, p)
		}
	}
	return profiles
}

// requireScript is the one lookup for scripts/<name> in dir, before
// anything runs it: absent (or scripts/ not a directory) is sentinel, the caller's "not available";
// any other failure to look is an error naming the script; a script
// that is there must pass the trust check (trusted.go) first.
func requireScript(dir, name string, sentinel error) error {
	if _, err := os.Lstat(filepath.Join(dir, scriptsDir, name)); err != nil {
		if errors.Is(err, fs.ErrNotExist) || scriptsNotADirectory(dir, err) {
			return sentinel
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	// It exists; run it only from a path nobody else could have put it
	// in (#191).
	return requireTrustedScripts(dir, name)
}

// scriptsNotADirectory reports whether a lookup failed with ENOTDIR
// because scripts/ inside dir is not a directory: that is as absent as
// no scripts/ at all. dir itself not being a directory stays an error.
func scriptsNotADirectory(dir string, err error) bool {
	if !errors.Is(err, syscall.ENOTDIR) {
		return false
	}
	info, statErr := os.Stat(dir)
	return statErr == nil && info.IsDir()
}
