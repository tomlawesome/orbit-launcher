// Command orbit-launcher is a full-screen terminal application for
// installing, updating, repairing and removing an Orbit personal server.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/notices"
	"github.com/tomlawesome/orbit-launcher/internal/release"
	"github.com/tomlawesome/orbit-launcher/internal/ui"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, startApp))
}

// effectiveUID is the user the launcher runs as, for the root refusal in
// run. A variable so tests can check that refusal without a real root run.
var effectiveUID = os.Geteuid

// run is orbit-launcher's entire command-line surface: the --version and
// --licences short-circuits, the refusal to run as root, and otherwise
// the TUI itself. startApp is a seam for
// tests -- main always passes startApp itself; main_test.go substitutes a
// stub so the dispatch logic (which args launch the TUI, what exit code
// and output each path produces) can be checked without a real terminal.
// The TUI's own behaviour is exercised instead by test/pty, which spawns
// the real compiled binary under a real pty (docs/implementation-plan.md
// section 3.3) -- that is a better tool for proving a full-screen
// application works than an in-process unit test would be.
func run(args []string, stdout, stderr io.Writer, startApp func(stdout, stderr io.Writer) int) int {
	if len(args) > 0 && args[0] == "--version" {
		fmt.Fprintf(stdout, "orbit-launcher %s (%s)\n", release.DisplayVersion(release.Version), release.Revision)
		return 0
	}
	// The licence notices every copy of the binary owes its dependencies
	// (#182). Both spellings, since either is what someone will type.
	// Like --version it only prints text, so it answers as root too.
	if len(args) > 0 && (args[0] == "--licences" || args[0] == "--licenses") {
		fmt.Fprint(stdout, notices.Text)
		return 0
	}
	// Nothing the launcher or Orbit's installer does needs root, and
	// running as root only leaves root-owned deployment files, so root is
	// refused before anything is read or run (#291). --version and
	// --licences alone still answer: they read and run nothing.
	if effectiveUID() == 0 {
		fmt.Fprintln(stderr, rootRefusal(os.Getenv("SUDO_USER")))
		return 1
	}
	return startApp(stdout, stderr)
}

// rootRefusal is the one line the launcher prints when run as root:
// under sudo (SUDO_USER names someone other than root) it says to drop
// sudo, otherwise to use one's own account.
func rootRefusal(sudoUser string) string {
	how := "as your own user, the one who'll look after Orbit"
	if sudoUser != "" && sudoUser != "root" {
		how = "without sudo, as the user who'll look after Orbit"
	}
	return "orbit-launcher doesn't run as root. Run it again " + how + ". That user needs to be able to use Docker."
}

// startApp builds and runs the TUI program to completion. stdout is
// unused: tea.NewProgram writes straight to the real terminal rather than
// through an injectable writer, which is also why this function itself
// is not unit-tested (see run's doc comment) -- the parameter stays so
// startApp's signature matches what run expects.
func startApp(stdout, stderr io.Writer) int {
	app := ui.NewAppModel()
	if os.Getenv(deploy.NoAnimationEnv) != "" {
		app = ui.NewAppModelNoAnimation()
	}
	if os.Getenv(deploy.NoUpdateCheckEnv) == "" {
		app = app.WithUpdateCheck(release.CheckForUpdate)
	}
	app = app.WithVersion(release.DisplayVersion(release.Version))
	// The health probe hits only the user's own deployment (its APP_URL),
	// resolving the splash's alive/degraded state; the env gate mirrors
	// ORBIT_LAUNCHER_NO_UPDATE_CHECK so tests stay offline-deterministic.
	var probe func(context.Context, string) deploy.HealthReason
	if os.Getenv(deploy.NoHealthProbeEnv) == "" {
		probe = deploy.ProbeHealth
	}
	app = app.WithDeploymentStatus(probe)
	// Install's stale-database-volume pre-flight (issue #105) asks the
	// local Docker daemon what volumes exist, so its answer depends on
	// the machine the test runs on — and on any machine that has ever
	// run Orbit it legitimately interrupts the profile screen. Same
	// hermeticity gate as the two above, for the same reason.
	if os.Getenv(deploy.NoVolumeCheckEnv) != "" {
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

	// Bubble Tea's own handler turns SIGTERM into an immediate quit that
	// skips the engine's stop and the temp-file cleanup, so it is off and
	// forwardSignals is the one handler. SIGKILL cannot be caught.
	program := tea.NewProgram(app, tea.WithoutSignalHandler())
	sender.Attach(program)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	stopSignals, interrupted := watchSignals(sigs, program.Send)
	final, err := program.Run()
	signal.Stop(sigs)
	stopSignals()
	// Whatever screen ended the program, a removal of a run's staged
	// files still waiting on a configuration import is finished before
	// the process exits, or it would die half done (#277).
	ui.FinishDeferredCleanup()
	if err == nil && interrupted() {
		// An interrupt from outside still ends the launcher as one, as
		// it did when Bubble Tea's handler turned it into ErrInterrupted;
		// it only waits for the stop to clean up first.
		err = tea.ErrInterrupted
	}
	if err != nil {
		fmt.Fprintln(stderr, "orbit-launcher:", err)
		if app, ok := final.(ui.AppModel); ok && app.ExitNote() != "" {
			fmt.Fprintln(stderr, "\norbit-launcher:", app.ExitNote())
		}
		return 1
	}
	// A Ctrl-C stop's note can only be read once the alternate screen
	// is gone, so it is printed here rather than by the TUI. It starts
	// on a line of its own: keys typed before the launcher took the
	// terminal were echoed onto the normal screen, where it lands. A
	// non-empty note means a stop outlived its grace: say so and exit 1.
	// A clean stop prints nothing and exits 0 (decision 20a).
	if app, ok := final.(ui.AppModel); ok {
		if note := app.ExitNote(); note != "" {
			fmt.Fprintln(stderr, "\norbit-launcher:", note)
			return 1
		}
		// SIGTERM or SIGHUP quit an Update that had already started
		// applying and left it to finish: say so, and exit as a shell
		// reports a signal (#304, owner decision 10a). SIGINT stopped
		// the same way is the interrupted exit above.
		if sig, ok := app.SignalStop().(syscall.Signal); ok && (sig == syscall.SIGTERM || sig == syscall.SIGHUP) {
			fmt.Fprintln(stderr, "stopped during an update that had already started applying; run orbit-launcher and choose Repair to check Orbit")
			return 128 + int(sig)
		}
	}
	return 0
}

// forwardSignals reads sigs and hands send the message for each one, so
// the launcher stops the way Ctrl-C stops it: the engine gets its polite
// stop and grace, and the staged install.sh and configuration tree are
// removed. SIGHUP (a dropped SSH session) and SIGTERM are a
// ui.SignalQuitMsg: unlike the key, it is not dropped by a step that
// ignores Ctrl-C (it quits once that step finishes), and repeating it
// never forces a stop under way. SIGINT from outside (kill -INT,
// timeout -s INT; #277) is the same message the first time, and the
// Ctrl-C key itself after that, so a second one forces the stop as a
// second Ctrl-C does. While install.sh holds the terminal, a SIGINT is
// the person's Ctrl-C to install.sh and is dropped, not counted. stop
// ends the forwarding and returns once the goroutine has; it is safe to
// call twice.
func forwardSignals(sigs <-chan os.Signal, send func(tea.Msg)) (stop func()) {
	stop, _ = watchSignals(sigs, send)
	return stop
}

// watchSignals is forwardSignals, also reporting whether a SIGINT was
// forwarded, which makes the launcher's exit an interrupted one.
func watchSignals(sigs <-chan os.Signal, send func(tea.Msg)) (stop func(), interrupted func() bool) {
	quit, done := make(chan struct{}), make(chan struct{})
	var interrupts atomic.Int32
	go func() {
		defer close(done)
		for {
			select {
			case <-quit:
				return
			case sig := <-sigs:
				switch sig {
				case syscall.SIGHUP, syscall.SIGTERM:
					send(ui.SignalQuitMsg{Signal: sig})
				case syscall.SIGINT:
					if ui.TerminalHandedOver() {
						continue
					}
					if interrupts.Add(1) == 1 {
						send(ui.SignalQuitMsg{Signal: sig})
					} else {
						send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
					}
				}
			}
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() { close(quit) })
		<-done
	}
	return stop, func() bool { return interrupts.Load() > 0 }
}
