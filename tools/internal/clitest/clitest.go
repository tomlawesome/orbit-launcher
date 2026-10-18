// Package clitest runs a tool's real main in a child copy of the test
// binary (#199 SU-17), so the wiring main does -- os.Args into the tool's
// command, its result into os.Exit -- is tested, not only what it calls.
// It is the standard "re-run the test binary as the command" pattern, as
// testscript's RunMain uses. Only tests import it.
package clitest

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"regexp"
	"testing"
)

const (
	envTest = "ORBIT_TOOLS_CLITEST_MAIN"
	envArgs = "ORBIT_TOOLS_CLITEST_ARGS"
)

// Main runs main as the command line `<tool> args...` would, in a child
// process, and returns its exit code and output. Call it from a top-level
// test: the child re-enters that same test, where Main calls main instead
// of starting another child. A main that returns rather than exiting
// reads as exit 0.
func Main(t *testing.T, main func(), args ...string) (code int, stdout, stderr string) {
	t.Helper()
	if os.Getenv(envTest) == t.Name() {
		var childArgs []string
		if err := json.Unmarshal([]byte(os.Getenv(envArgs)), &childArgs); err != nil {
			t.Fatal(err)
		}
		os.Args = append(os.Args[:1:1], childArgs...)
		main()
		os.Exit(0)
	}

	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(), envTest+"="+t.Name(), envArgs+"="+string(encoded))
	// main ends in os.Exit, so the child's coverage is written by the
	// runtime's exit hook, which reads GOCOVERDIR rather than the test
	// flag. scripts/coverage.sh merges only the -test.gocoverdir
	// directory, so point the child there.
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+f.Value.String())
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("running main in a child: %v", err)
	}
	return code, out.String(), errOut.String()
}
