// Command orbit-launcher is a full-screen terminal application for
// installing, updating, repairing and removing an Orbit personal server.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

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

	program := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "orbit-launcher:", err)
		os.Exit(1)
	}
}
