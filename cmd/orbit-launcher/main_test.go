package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/notices"
	"github.com/tomlawesome/orbit-launcher/internal/release"
)

func TestRun_VersionFlag(t *testing.T) {
	startAppCalled := false
	stub := func(stdout, stderr io.Writer) int {
		startAppCalled = true
		return 99
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr, stub)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if startAppCalled {
		t.Error("--version must not start the TUI")
	}
	want := "orbit-launcher " + release.Version + " (" + release.Revision + ")\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

// --licences prints the notices the binary embeds (#182), in full, and
// never starts the TUI.
func TestRun_LicencesFlag(t *testing.T) {
	for _, flag := range []string{"--licences", "--licenses"} {
		started := false
		stub := func(stdout, stderr io.Writer) int { started = true; return 99 }
		var stdout, stderr bytes.Buffer
		if code := run([]string{flag}, &stdout, &stderr, stub); code != 0 {
			t.Errorf("%s: exit code = %d, want 0", flag, code)
		}
		if started {
			t.Errorf("%s must not start the TUI", flag)
		}
		if stdout.String() != notices.Text {
			t.Errorf("%s: stdout is not the embedded notices", flag)
		}
		for _, want := range []string{"Go standard library and runtime", "charm.land/bubbletea/v2", "UNICODE LICENSE V3"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("%s: notices lack %q", flag, want)
			}
		}
	}
}

func TestRun_NoArgsStartsApp(t *testing.T) {
	var gotStdout, gotStderr io.Writer
	stub := func(stdout, stderr io.Writer) int {
		gotStdout, gotStderr = stdout, stderr
		return 7
	}

	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr, stub)

	if code != 7 {
		t.Fatalf("exit code = %d, want 7 (startApp's return value)", code)
	}
	if gotStdout != &stdout || gotStderr != &stderr {
		t.Error("run must pass its own stdout/stderr through to startApp")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want untouched by run itself", stdout.String())
	}
}

func TestRun_UnrecognisedArgStartsApp(t *testing.T) {
	// Only the literal "--version" short-circuits; anything else today
	// falls through to the TUI, same as before this was split out of
	// main(). This pins that behaviour so a future change to the
	// dispatch condition is a deliberate decision, not a slip.
	called := false
	stub := func(stdout, stderr io.Writer) int {
		called = true
		return 0
	}

	var stdout, stderr bytes.Buffer
	run([]string{"--help"}, &stdout, &stderr, stub)

	if !called {
		t.Error("an argument other than --version must still start the TUI")
	}
}
