package ui

import (
	"fmt"
	"os"
)

// Diagnostics for the paths a person never sees taken.
//
// The in-console configuration path (configcollect.go) falls back to
// the terminal handoff whenever it cannot do the job, and it does so
// silently: the screen simply changes. That is right for an operator —
// the installer they land in is the one that works — and useless for
// CI, which needs to know *which* path a run took and why. So every
// fallback now says why first.

// logDiag writes one diagnostic line to stderr. Stderr is the only
// diagnostic sink this program has (cmd/orbit-launcher/main.go writes
// its fatal error the same way), so this is that one, not a second
// mechanism.
//
// Under the alt screen the line is invisible to an operator — the next
// repaint covers it, and a fallback repaints immediately. The live
// suite and the launcher-compat CI job run the binary on a real pty
// and keep everything written to that pty as an artifact, so the line
// survives exactly where it is needed.
//
// Callers pass fixed phrases, never a wrapped error and never anything
// the engine returned. A fetch error carries the source URL, and an
// answer carries the secret; the pty log is readable by anyone who can
// read the job's artifacts, which is why install.sh is served over
// loopback in the first place.
func logDiag(line string) {
	fmt.Fprintln(os.Stderr, "orbit-launcher:", line)
}

// requireInConsoleEnv demands the in-console configuration path: with
// it set, a fallback to the terminal handoff stops the run loudly
// instead of switching paths quietly.
//
// Default off, so an operator's experience is unchanged — a fallback
// still falls back. Only CI and the live suite set it, and only
// because a handoff there is not a graceful degradation but the
// failure the job exists to catch.
const requireInConsoleEnv = "ORBIT_LAUNCHER_REQUIRE_IN_CONSOLE_CONFIG"

// requireInConsoleConfig reports whether that demand is in force. Set
// to anything non-empty is on, matching every other ORBIT_LAUNCHER_*
// switch this program reads (cmd/orbit-launcher/main.go).
func requireInConsoleConfig() bool {
	return os.Getenv(requireInConsoleEnv) != ""
}
