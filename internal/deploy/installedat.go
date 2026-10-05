package deploy

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// InstalledAt reports when this deployment was installed: the time
// Docker created its database volume, which Compose names
// <project>_orbit-db-data and nothing changes after the first install.
// It is read from Docker, the system of record, rather than kept as a
// second copy anywhere — .env-orbit's modification time is not an
// install date, because reconfiguring or repairing rewrites that file.
//
// The volume name is constructed here, which DatabaseVolume's rule
// against invented names does not forbid: that rule protects names shown
// to a person, and this one is only ever passed to docker.
//
// Every failure — nil deployment, no docker on PATH, no reachable
// daemon, no such volume, output that is not a time — returns the zero
// time and no error. The date is reassurance on a confirm screen, so
// when Docker cannot say, the screen leaves it out rather than guess.
func InstalledAt(ctx context.Context, d *Deployment) time.Time {
	if d == nil || d.Project == "" {
		return time.Time{}
	}
	volume := d.Project + "_" + orbitDatabaseVolumePattern
	out, err := exec.CommandContext(ctx, "docker", "volume", "inspect",
		"--format", "{{.CreatedAt}}", volume).Output()
	if err != nil {
		return time.Time{}
	}
	created, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return time.Time{}
	}
	return created
}
