package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

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

func TestDisplayVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty is dev", "", "dev"},
		{"literal dev stays dev", "dev", "dev"},
		{"bare version gets v-prefixed", "1.2.3", "v1.2.3"},
		{"already v-prefixed is untouched", "v1.2.3", "v1.2.3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := displayVersion(c.in); got != c.want {
				t.Errorf("displayVersion(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestDisplayVersion_NeverEmpty(t *testing.T) {
	// The splash corner must always show something; an empty string
	// there would be silently wrong, not visibly broken, so it is worth
	// a dedicated assertion.
	for _, in := range []string{"", "dev", "v0.0.0", "1.0.0"} {
		if got := displayVersion(in); strings.TrimSpace(got) == "" {
			t.Errorf("displayVersion(%q) returned a blank string", in)
		}
	}
}
