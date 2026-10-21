package main

// #236 item 13, #195 SEC-4 remainder and round 5 ("config tree and staged
// install.sh left when the launcher is stopped by SIGHUP or SIGTERM"):
// a hangup (a dropped SSH session) or a terminate must run the same
// cleanup as Ctrl-C. SIGKILL cannot be caught and is not handled.
//
// This file does not compile until the seam exists; it is kept apart so
// that the failure is recorded in one place. The seam it names:
//
//	// forwardSignals reads sigs and, for SIGHUP and SIGTERM, hands send
//	// the message that makes the app do what Ctrl-C does. stop ends the
//	// forwarding (the goroutine returns) and is safe to call twice.
//	func forwardSignals(sigs <-chan os.Signal, send func(tea.Msg)) (stop func())
//
// startApp is expected to feed it from signal.Notify(ch, SIGHUP, SIGTERM)
// and to send through the program's Send; the message may be the Ctrl-C
// key press itself or a new type the app treats the same way. The test
// holds the app to that, not to a type.
//
// What a unit test cannot show, for the builder to verify by hand: that
// bubbletea's own signal handler (it turns SIGTERM into a quit and SIGINT
// into an interrupt before any model sees them) does not win the race
// against the forwarded message, for example by starting the program with
// tea.WithoutSignalHandler; and that `kill -TERM` and `kill -HUP` on a
// launcher mid-install leave no orbit-launcher-install-* file or
// orbit-launcher-config-* directory in TMPDIR (test/pty has a fake
// engine for this).

import (
	"os"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ui"
)

func TestForwardSignals_HangupAndTerminateLeaveTheWayCtrlCDoes(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	base := ui.NewAppModelNoAnimation()
	if _, cmd := base.Update(ctrlC); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("setup: Ctrl+C on the idle app does not quit, so there is nothing to compare a signal with")
	}

	for name, sig := range map[string]os.Signal{"SIGHUP": syscall.SIGHUP, "SIGTERM": syscall.SIGTERM} {
		t.Run(name, func(t *testing.T) {
			sigs := make(chan os.Signal, 1)
			sent := make(chan tea.Msg, 4)
			stop := forwardSignals(sigs, func(m tea.Msg) { sent <- m })
			defer stop()

			sigs <- sig

			select {
			case msg := <-sent:
				_, cmd := base.Update(msg)
				if cmd == nil || cmd() != tea.Quit() {
					t.Fatalf("the forwarded message %#v does not make the app leave as Ctrl+C does", msg)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s was not forwarded to the app", name)
			}
		})
	}
}

func TestForwardSignals_StopEndsTheForwarding(t *testing.T) {
	sigs := make(chan os.Signal, 1)
	sent := make(chan tea.Msg, 4)
	stop := forwardSignals(sigs, func(m tea.Msg) { sent <- m })
	stop()
	stop() // twice is safe

	sigs <- syscall.SIGTERM
	select {
	case msg := <-sent:
		t.Fatalf("a signal after stop was forwarded: %#v", msg)
	case <-time.After(300 * time.Millisecond):
	}
}
