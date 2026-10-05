// Command orbit-launcher is a full-screen terminal application for
// installing, updating, repairing and removing an Orbit personal server.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/notices"
	"github.com/tomlawesome/orbit-launcher/internal/release"
	"github.com/tomlawesome/orbit-launcher/internal/ui"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, startApp))
}

// run is orbit-launcher's entire command-line surface: the --version
// and --licences short-circuits, and otherwise the TUI itself. startApp is a seam for
// tests -- main always passes startApp itself; main_test.go substitutes a
// stub so the dispatch logic (which args launch the TUI, what exit code
// and output each path produces) can be checked without a real terminal.
// The TUI's own behaviour is exercised instead by test/pty, which spawns
// the real compiled binary under a real pty (docs/implementation-plan.md
// section 3.3) -- that is a better tool for proving a full-screen
// application works than an in-process unit test would be.
func run(args []string, stdout, stderr io.Writer, startApp func(stdout, stderr io.Writer) int) int {
	if len(args) > 0 && args[0] == "--version" {
		fmt.Fprintf(stdout, "orbit-launcher %s (%s)\n", release.Version, release.Revision)
		return 0
	}
	// The licence notices every copy of the binary owes its dependencies
	// (#182). Both spellings, since either is what someone will type.
	if len(args) > 0 && (args[0] == "--licences" || args[0] == "--licenses") {
		fmt.Fprint(stdout, notices.Text)
		return 0
	}
	return startApp(stdout, stderr)
}

// startApp builds and runs the TUI program to completion. stdout is
// unused: tea.NewProgram writes straight to the real terminal rather than
// through an injectable writer, which is also why this function itself
// is not unit-tested (see run's doc comment) -- the parameter stays so
// startApp's signature matches what run expects.
func startApp(stdout, stderr io.Writer) int {
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
	// Install's stale-database-volume pre-flight (issue #105) asks the
	// local Docker daemon what volumes exist, so its answer depends on
	// the machine the test runs on — and on any machine that has ever
	// run Orbit it legitimately interrupts the profile screen. Same
	// hermeticity gate as the two above, for the same reason.
	if os.Getenv("ORBIT_LAUNCHER_NO_VOLUME_CHECK") != "" {
		app = app.WithoutVolumeCheck()
	}

	// The engine stream reader pushes into the event loop rather than
	// being asked for each message (#159), and tea.NewProgram takes the
	// model, so the program cannot be built into it. Build the sender
	// first, attach it once the program exists.
	sender := ui.NewProgramSender()
	app = app.WithSender(sender.Send)

	// The alternate screen is asked for on the model, not the program:
	// it is a property of the view now — see ui.AppModel.View.
	app = app.WithAltScreen()

	program := tea.NewProgram(app)
	sender.Attach(program)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(stderr, "orbit-launcher:", err)
		return 1
	}
	return 0
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
