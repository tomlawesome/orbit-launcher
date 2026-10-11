package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/internal/clitest"
)

// #233: coveragefloor runs on the shared command skeleton
// (tools/internal/cli), so these tests drive the real main as the command
// line would (clitest.Main) and pin only what a user sees: exit code,
// stdout and stderr. 2 is a wrong argument count or a bad flag, 1 is
// unreadable input or a failed ratchet, 0 is a pass or -h.
//
// Each case runs the real main in a child process, so the tests do not
// depend on how the command is split into functions.

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", p, err)
	}
	return p
}

const samplePassingProfile = `mode: atomic
example.com/mod/pkg/a.go:1.1,2.2 10 10
`

const samplePassingFloors = `example.com/mod/pkg 100.0
`

const sampleFailingProfile = `mode: atomic
example.com/mod/pkg/a.go:1.1,2.2 10 0
`

const defaultFloors = ".github/coverage-floors.txt"

// coveragefloor runs the command as `go run ./tools/coveragefloor args...`
// would.
func coveragefloor(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	return clitest.Main(t, main, args...)
}

func TestCommand_PassExitsZero(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile)
	floors := writeTemp(t, "floors.txt", samplePassingFloors)

	code, stdout, stderr := coveragefloor(t, profile, floors)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "example.com/mod/pkg") {
		t.Errorf("stdout missing package report: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr should be empty on success, got %q", stderr)
	}
}

func TestCommand_RegressionExitsOne(t *testing.T) {
	profile := writeTemp(t, "coverage.out", sampleFailingProfile)
	floors := writeTemp(t, "floors.txt", samplePassingFloors)

	code, stdout, stderr := coveragefloor(t, profile, floors)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "example.com/mod/pkg") {
		t.Errorf("stderr missing the failing package: %q", stderr)
	}
}

// A floor naming a package the profile no longer has is a failed ratchet.
func TestCommand_StaleFloorExitsOne(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile)
	floors := writeTemp(t, "floors.txt", samplePassingFloors+"example.com/mod/gone 50.0\n")

	code, _, stderr := coveragefloor(t, profile, floors)

	if code != 1 || !strings.Contains(stderr, "example.com/mod/gone") {
		t.Fatalf("exit code = %d, stderr %q; want 1 naming the stale floor", code, stderr)
	}
}

// Unreadable input is a failure of the work (exit 1), as in every other
// tool; it used to be 2.
func TestCommand_MissingProfileExitsOne(t *testing.T) {
	floors := writeTemp(t, "floors.txt", samplePassingFloors)

	code, stdout, stderr := coveragefloor(t, filepath.Join(t.TempDir(), "does-not-exist.out"), floors)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr=%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q; an unreadable profile must report nothing", stdout)
	}
	if !strings.HasPrefix(stderr, "coveragefloor: ") {
		t.Errorf("stderr missing the command's error prefix: %q", stderr)
	}
}

func TestCommand_MissingFloorsFileExitsOne(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile)

	code, _, stderr := coveragefloor(t, profile, filepath.Join(t.TempDir(), "does-not-exist.txt"))

	if code != 1 || !strings.HasPrefix(stderr, "coveragefloor: ") {
		t.Fatalf("exit code = %d, stderr %q; want 1 and the error", code, stderr)
	}
}

// A package the profile measures but the floors file does not name is
// reported, and passes: a floor is only ever added by review (see
// package floor).
func TestCommand_PackageWithoutFloorIsReportedAndPasses(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile+"example.com/mod/other/b.go:1.1,2.2 4 0\n")
	floors := writeTemp(t, "floors.txt", samplePassingFloors)

	code, stdout, stderr := coveragefloor(t, profile, floors)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "example.com/mod/other") || !strings.Contains(stdout, "got   0.0%  (no floor set)") {
		t.Errorf("stdout does not report the unfloored package as such: %q", stdout)
	}
	if !strings.Contains(stdout, "1 package(s) checked against "+floors) {
		t.Errorf("stdout's summary does not name the one floor and its file: %q", stdout)
	}
}

// Input that cannot be parsed still fails closed, never a pass. It is
// input the work could not use, so it exits 1 (see the issue's reading of
// "unreadable input").
func TestCommand_MalformedInputFailsClosedWithExitOne(t *testing.T) {
	cases := map[string][2]string{
		"profile with no mode header": {"example.com/mod/pkg/a.go:1.1,2.2 10 10\n", samplePassingFloors},
		"floor that is not a number":  {samplePassingProfile, "example.com/mod/pkg high\n"},
	}
	for name, in := range cases {
		code, stdout, stderr := coveragefloor(t, writeTemp(t, "coverage.out", in[0]), writeTemp(t, "floors.txt", in[1]))
		if code != 1 || !strings.HasPrefix(stderr, "coveragefloor: ") || stdout != "" {
			t.Errorf("%s: exit code = %d, stdout %q, stderr %q; want 1 and the error alone", name, code, stdout, stderr)
		}
	}
}

// Positional arguments are read after flag parsing: one or two, no more.
// A wrong count is a usage error (exit 2) that reads no file and names
// the command on stderr.
func TestCommand_WrongArgumentCountIsAUsageError(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile)
	floors := writeTemp(t, "floors.txt", samplePassingFloors)
	cases := map[string][]string{
		"no arguments":    {},
		"three arguments": {profile, floors, "extra"},
	}
	for name, args := range cases {
		code, stdout, stderr := coveragefloor(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(strings.ToLower(stderr), "usage") || !strings.Contains(stderr, "coveragefloor") {
			t.Errorf("%s: exit code = %d, stdout %q, stderr %q; want 2 and usage naming coveragefloor on stderr", name, code, stdout, stderr)
		}
	}
}

// A bad flag behaves as the other tools' do: exit 2, the flag named on
// stderr, nothing on stdout, and the ratchet never runs.
func TestCommand_BadFlagExitsTwo(t *testing.T) {
	profile := writeTemp(t, "coverage.out", sampleFailingProfile)
	floors := writeTemp(t, "floors.txt", samplePassingFloors)

	code, stdout, stderr := coveragefloor(t, "-bogus", profile, floors)

	if code != 2 || stdout != "" || !strings.Contains(stderr, "flag provided but not defined: -bogus") {
		t.Errorf("exit code = %d, stdout %q, stderr %q; want 2 naming the bad flag on stderr", code, stdout, stderr)
	}
}

// -h is help, not a profile file name: usage on stderr, exit 0, and the
// ratchet never runs.
func TestCommand_HelpPrintsUsageAndExitsZero(t *testing.T) {
	for _, flagName := range []string{"-h", "-help"} {
		code, stdout, stderr := coveragefloor(t, flagName)
		if code != 0 || stdout != "" || !strings.Contains(strings.ToLower(stderr), "usage") || !strings.Contains(stderr, "coveragefloor") {
			t.Errorf("%s: exit code = %d, stdout %q, stderr %q; want 0 and usage naming coveragefloor on stderr", flagName, code, stdout, stderr)
		}
	}
}

// With only a profile, the floors come from .github/coverage-floors.txt
// in the directory it runs in -- the module root, as CI runs it
// (`go run ./tools/coveragefloor coverage.out`); a second argument
// replaces that file.
func TestCommand_FloorsFileDefaultsToTheRepoOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, defaultFloors), []byte(samplePassingFloors), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	profile := writeTemp(t, "coverage.out", samplePassingProfile)

	if code, stdout, stderr := coveragefloor(t, profile); code != 0 || !strings.Contains(stdout, "checked against "+defaultFloors) {
		t.Fatalf("default floors: exit code = %d, stdout %q, stderr %q; want 0 against %s", code, stdout, stderr, defaultFloors)
	}

	stricter := writeTemp(t, "floors.txt", "example.com/mod/pkg 100.0\nexample.com/mod/gone 50.0\n")
	if code, _, stderr := coveragefloor(t, profile, stricter); code != 1 || !strings.Contains(stderr, "example.com/mod/gone") {
		t.Fatalf("explicit floors: exit code = %d, stderr %q; want 1 and the stale floor, proving the second argument was read", code, stderr)
	}
}

// With no floors file at the default path the command fails (1), rather
// than passing on nothing.
func TestCommand_MissingDefaultFloorsFileExitsOne(t *testing.T) {
	t.Chdir(t.TempDir())
	profile := writeTemp(t, "coverage.out", samplePassingProfile)

	code, _, stderr := coveragefloor(t, profile)

	if code != 1 || !strings.HasPrefix(stderr, "coveragefloor: ") {
		t.Fatalf("exit code = %d, stderr %q; want 1 and the error", code, stderr)
	}
}

// The skeleton is the one place a flag set is built and ErrHelp mapped
// (see tools/internal/cli's shared_run_test.go). Like its four siblings,
// coveragefloor calls cli.Run, under the import name cli, and does not
// build a flag set itself.
func TestSourceUsesTheSharedSkeleton(t *testing.T) {
	const cliImport = "github.com/tomlawesome/orbit-launcher/tools/internal/cli"
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		local := ""
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path != cliImport {
				continue
			}
			local = "cli"
			if imp.Name != nil {
				local = imp.Name.Name
			}
			if local != "cli" {
				t.Errorf("%s imports the skeleton as %q, want the one name cli", name, local)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case local != "" && id.Name == local && sel.Sel.Name == "Run":
				calls++
			case id.Name == "flag" && (sel.Sel.Name == "NewFlagSet" || sel.Sel.Name == "ErrHelp"):
				t.Errorf("%s uses flag.%s; that is cli.Run's job", name, sel.Sel.Name)
			}
			return true
		})
	}
	if calls == 0 {
		t.Error("tools/coveragefloor does not call cli.Run")
	}
}
