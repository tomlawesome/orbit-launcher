package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/internal/clitest"
)

// X-Q7 (#195): the command takes no positional arguments. `calculateversion
// hotfix` (the flag written without its dash) used to print the next minor
// version, and a release would be cut at the wrong number. Any positional
// argument is a usage error: exit 2, nothing on stdout, the argument named
// on stderr.
func TestRunRejectsPositionalArgumentsWithExitTwo(t *testing.T) {
	for _, c := range []struct {
		args       []string
		positional string // the first positional argument, which stderr must name
	}{
		{[]string{"hotfix"}, "hotfix"},
		{[]string{"-hotfix", "hotfix"}, "hotfix"},
		{[]string{"hotfix", "-hotfix"}, "hotfix"},
		{[]string{"0.5.0"}, "0.5.0"},
		{[]string{"--", "hotfix"}, "hotfix"},
		{[]string{"a", "b"}, "a"},
	} {
		args := c.args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(args, fixedTags("v0.4.2"), &stdout, &stderr)
			if code != 2 {
				t.Errorf("exit code %d, want 2; stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q; a refused command line must not print a version", stdout.String())
			}
			if !strings.Contains(stderr.String(), c.positional) {
				t.Errorf("stderr = %q; want the unexpected argument %q named", stderr.String(), c.positional)
			}
		})
	}
}

// The command as the shell runs it, through the real main.
func TestMainRejectsAPositionalArgument(t *testing.T) {
	code, stdout, stderr := clitest.Main(t, main, "hotfix")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "hotfix") {
		t.Errorf("exit %d, stdout %q, stderr %q; want exit 2 and the argument named on stderr", code, stdout, stderr)
	}
}
