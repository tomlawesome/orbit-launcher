// Command orbit-launcher is a full-screen terminal application for
// installing, updating, repairing and removing an Orbit personal server.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/release"
	"github.com/tomlawesome/orbit-launcher/internal/ui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("orbit-launcher %s (%s)\n", release.Version, release.Revision)
		return
	}

	app := ui.NewAppModel()
	if os.Getenv("ORBIT_LAUNCHER_NO_ANIMATION") != "" {
		app = ui.NewAppModelNoAnimation()
	}
	if os.Getenv("ORBIT_LAUNCHER_NO_UPDATE_CHECK") == "" {
		app = app.WithUpdateCheck(release.CheckForUpdate)
	}
	app = app.WithVersion(displayVersion(release.Version))
	// The health probe hits only the user's own deployment (its APP_URL),
	// resolving the splash's alive/degraded state; the env gate mirrors
	// ORBIT_LAUNCHER_NO_UPDATE_CHECK so tests stay offline-deterministic.
	var probe func(context.Context, string) bool
	if os.Getenv("ORBIT_LAUNCHER_NO_HEALTH_PROBE") == "" {
		probe = deploy.ProbeHealth
	}
	app = app.WithDeploymentStatus(probe)

	program := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "orbit-launcher:", err)
		os.Exit(1)
	}
}

// displayVersion formats the release version for the splash's corner:
// always v-prefixed, never a bare "dev" masquerading as a release.
func displayVersion(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}
