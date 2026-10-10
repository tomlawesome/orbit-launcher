package deploy

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// dockerQueryTimeout bounds each read-only Docker query this package
// makes (UnownedDatabaseVolumes, InstalledAt). It is generous for
// `docker volume ls` or `inspect` and still short enough that an
// unreachable daemon costs a moment, not a wait: both answers are
// advisory, so running out of time simply leaves them unsaid (#207:
// the limit lives beside the call, not in the UI).
const dockerQueryTimeout = 5 * time.Second

// orbitDatabaseVolumePattern is the substring orbit's own installer
// matches on when it refuses to start Compose over a database it cannot
// prove ownership of (scripts/install.sh, database-volume safety). It is
// deliberately the same string: this package reports what the engine
// will refuse, and inventing a second, subtly different rule here would
// mean explaining a refusal that doesn't happen — or worse, staying
// silent about one that does.
const orbitDatabaseVolumePattern = "orbit-db-data"

// DatabaseVolume is one Orbit database volume that exists on this
// machine. Name and Project are read verbatim from docker's own output
// and are never constructed, inferred or reformatted — a person may act
// on a volume name shown to them, so the only safe name to show is one
// docker itself just printed.
type DatabaseVolume struct {
	Name string
	// Project is the Compose project the volume belongs to, empty when
	// the volume carries no such label.
	Project string
}

// UnownedDatabaseVolumes reports the Orbit database volumes on this
// machine when targetDir holds no recognised deployment — the exact
// condition under which the engine refuses a fresh install:
//
//	An existing Orbit database volume requires a recognized deployment
//	with its preserved database credentials; refusing to start Compose.
//
// The check the engine makes is machine-wide, so a volume left by an old
// deployment in a different directory blocks a fresh install here. That
// refusal is correct — the new target has no preserved credentials to
// prove ownership, and starting Postgres against a database whose
// credentials are gone is data loss waiting to happen. What this
// function exists for is to let the launcher say so before the engine
// fails, rather than after.
//
// It returns nothing when targetDir does have a recognised deployment:
// that deployment's own volume is not a surprise and not a blocker.
//
// Every failure — no docker on PATH, no reachable daemon, no answer
// within dockerQueryTimeout, unreadable output — returns no volumes and
// no error. This is advisory pre-flight for a refusal the engine will
// make again on its own, so a detection problem must never be able to
// stand between someone and an install.
func UnownedDatabaseVolumes(ctx context.Context, targetDir string) []DatabaseVolume {
	if d, err := Detect(targetDir); err != nil || d != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, dockerQueryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "volume", "ls",
		"--filter", "name="+orbitDatabaseVolumePattern,
		"--format", `{{.Name}}	{{.Label "com.docker.compose.project"}}`)
	cmd.WaitDelay = pipeWaitDelay
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var volumes []DatabaseVolume
	for _, line := range strings.Split(string(out), "\n") {
		// Split before trimming: trimming first would strip the tab of a
		// line with an empty name and promote its project to the name.
		name, project, _ := strings.Cut(line, "\t")
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		volumes = append(volumes, DatabaseVolume{Name: name, Project: strings.TrimSpace(project)})
	}
	return volumes
}
