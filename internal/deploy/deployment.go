// Package deploy orchestrates Docker/Compose: profile and compose-file
// selection, health probes, and stand-down. Owns every side effect on the
// host so internal/ui only ever owns rendering and input.
package deploy

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Deployment describes a recognised existing Orbit install, read from its
// .env-orbit file — see orbit's .env-orbit.example for the authoritative
// format this parses a practical subset of.
type Deployment struct {
	TargetDir   string
	AppURL      string
	Profiles    []string
	Image       string
	InstalledAt time.Time
}

// Detect looks for a recognised Orbit deployment in targetDir. It returns
// (nil, nil) — not an error — when there simply isn't one there; an error
// return means something went wrong trying to read a file that exists.
func Detect(targetDir string) (*Deployment, error) {
	envPath := filepath.Join(targetDir, ".env-orbit")
	info, err := os.Stat(envPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	f, err := os.Open(envPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	d := &Deployment{TargetDir: targetDir, InstalledAt: info.ModTime()}
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
		switch strings.TrimSpace(key) {
		case "APP_URL":
			d.AppURL = value
		case "ORBIT_IMAGE":
			d.Image = value
		case "COMPOSE_PROFILES":
			d.Profiles = splitProfiles(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return d, nil
}

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
