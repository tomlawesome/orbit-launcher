package launcher

// #219 (WI-15), SU-19 and SU-7's buildBinary: the black-box suites share one
// launcher harness, test/internal/launcher, instead of each retyping the
// build, the hermetic environment and the fake install.sh server. These
// tests read source, because the harness does not exist yet.

import (
	"go/ast"
	"regexp"
	"strings"
	"testing"
)

const launcherDir = "test/internal/launcher"

func launcherSources(t *testing.T) []goFile { return loadDir(t, launcherDir, false) }

func ptySources(t *testing.T) []goFile { return loadAll(t, "test/pty") }

func liveSources(t *testing.T) []goFile { return loadAll(t, "test/live") }

// The scanners must see what they claim to see, or every "none found" below
// would pass vacuously.
func TestScanHelpers_RecogniseThePatternsTheChecksRelyOn(t *testing.T) {
	gf := parseSrc(t, `package p
import ("os";"os/exec";"net/http/httptest";"path/filepath")
func build() { exec.Command("go", "build"); exec.CommandContext(ctx, "go", "build") }
func other() { exec.Command("git", "status") }
func path(t T) { t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH")) }
func notPrepend(t T) { t.Setenv("PATH", t.TempDir()) }
func docker() { os.WriteFile(filepath.Join(d, "docker"), nil, 0o755) }
func tree() { os.MkdirAll(filepath.Join(d, "scripts"), 0o755) }
func server(t T) {
	httptest.NewServer(h)
	n.Add(1)
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", u)
}
const split = "a" + "b"
`)
	files := []goFile{gf}
	if got := countCalls(files, isGoToolchainCall); got != 2 {
		t.Errorf("go toolchain calls = %d, want 2", got)
	}
	if got := countCalls(files, isPathPrepend); got != 1 {
		t.Errorf("PATH prepends = %d, want 1 (and not the TempDir-only one)", got)
	}
	if got := funcDecls(files, writesFakeDocker); len(got) != 1 {
		t.Errorf("fake docker writers = %v, want 1", got)
	}
	if got := funcDecls(files, makesScriptsDir); len(got) != 1 {
		t.Errorf("scripts-dir makers = %v, want 1", got)
	}
	if got := funcDecls(files, runsCountingScriptServer); len(got) != 1 {
		t.Errorf("counting script servers = %v, want 1", got)
	}
	if got := funcDecls(files, runsCountingServer); len(got) != 1 {
		t.Errorf("counting servers = %v, want 1", got)
	}
	if got := literalHits(files, func(s string) bool { return s == "ab" }); len(got) != 1 {
		t.Errorf(`"a" + "b" was not read as one string: %v`, got)
	}
}

// SU-19: the harness package exists, with the three entry points the
// decisions name, each declared once.
func TestLauncherHarness_PackageExistsWithItsThreeEntryPoints(t *testing.T) {
	files := launcherSources(t)
	if len(files) == 0 {
		t.Fatalf("%s has no non-test Go files: the shared launcher harness does not exist", launcherDir)
	}
	for _, gf := range files {
		if name := gf.file.Name.Name; name != "launcher" {
			t.Errorf("%s declares package %q, want launcher", gf.rel, name)
		}
	}
	for _, name := range []string{"BuildBinary", "HermeticEnv", "ServeScript"} {
		if got := funcNamed(files, name); len(got) != 1 {
			t.Errorf("%s declared %d times (%v), want exactly once", name, len(got), got)
		}
	}
}

// SU-19 / Done "go build runs once per test package": the only place in the
// suites that runs the Go toolchain is the harness, and it runs it once.
func TestBuildBinary_GoBuildIsRunInOnePlaceOnly(t *testing.T) {
	for _, suite := range []struct {
		name  string
		files []goFile
	}{{"test/pty", ptySources(t)}, {"test/live", liveSources(t)}} {
		if n := countCalls(suite.files, isGoToolchainCall); n != 0 {
			t.Errorf("%s runs the go toolchain itself in %d place(s); it should call launcher.BuildBinary", suite.name, n)
		}
	}
	if n := countCalls(launcherSources(t), isGoToolchainCall); n != 1 {
		t.Errorf("%s runs the go toolchain in %d place(s), want exactly 1", launcherDir, n)
	}
}

// "Once per test package" is a sync.Once (or OnceValue/OnceValues) around
// the build, not a build per call that the doc comment calls "once".
func TestBuildBinary_IsBuiltOncePerPackage(t *testing.T) {
	files := launcherSources(t)
	if len(funcNamed(files, "BuildBinary")) == 0 {
		t.Fatal("launcher.BuildBinary does not exist")
	}
	once := false
	for _, gf := range files {
		ast.Inspect(gf.file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sync" && strings.HasPrefix(sel.Sel.Name, "Once") {
					once = true
				}
			}
			return true
		})
	}
	if !once {
		t.Error("the harness never uses sync.Once/OnceValue/OnceValues: nothing makes the build run once per package")
	}
}

// With one build shared by every test in the package, the binary cannot
// live in the first caller's t.TempDir: it is deleted when that test ends,
// and the next test finds nothing to run.
func TestBuildBinary_BinaryOutlivesTheTestThatBuiltIt(t *testing.T) {
	files := launcherSources(t)
	if len(funcNamed(files, "BuildBinary")) == 0 {
		t.Fatal("launcher.BuildBinary does not exist")
	}
	for _, gf := range files {
		for _, c := range calls(gf.file, func(c *ast.CallExpr) bool { _, n := callee(c); return n == "TempDir" }) {
			t.Errorf("%s:%v calls TempDir: a once-built binary in the first test's directory vanishes with that test",
				gf.rel, gf.fset.Position(c.Pos()).Line)
		}
	}
}

// SU-7: pty's comment claimed "once per test run" for a function that built
// on every call. The words must now be true or gone.
func TestBuildBinary_NoStaleOncePerTestRunClaim(t *testing.T) {
	files := launcherSources(t)
	if len(funcNamed(files, "BuildBinary")) == 0 {
		t.Error("launcher.BuildBinary does not exist")
	}
	for _, gf := range files {
		for _, d := range gf.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "BuildBinary" {
				if fd.Doc == nil || !strings.Contains(strings.ToLower(fd.Doc.Text()), "once") {
					t.Error("BuildBinary's doc comment does not say it builds once, which is now the point of it")
				}
			}
		}
	}
	for _, gf := range ptySources(t) {
		for _, cg := range gf.file.Comments {
			if strings.Contains(cg.Text(), "once per test run") {
				t.Errorf("%s still says \"once per test run\" (SU-7): %q", gf.rel, strings.TrimSpace(cg.Text()))
			}
		}
	}
}

// The coverage build (#169) is part of the one build: its flags and the
// GOCOVERDIR hand-off sit with the go build, not in a suite.
func TestBuildBinary_CoverageBuildLivesWithTheBuild(t *testing.T) {
	has := func(files []goFile, s string) bool {
		return len(literalHits(files, func(v string) bool { return strings.Contains(v, s) })) > 0
	}
	harness := launcherSources(t)
	for _, want := range []string{"ORBIT_LAUNCHER_BINARY_COVERDIR", "-coverpkg=github.com/tomlawesome/orbit-launcher/...", "GOCOVERDIR"} {
		if !has(harness, want) {
			t.Errorf("%s never mentions %q", launcherDir, want)
		}
	}
	for _, want := range []string{"ORBIT_LAUNCHER_BINARY_COVERDIR", "-coverpkg"} {
		if has(ptySources(t), want) {
			t.Errorf("test/pty still carries %q: the coverage build belongs to the harness", want)
		}
	}
}

// Both suites reach the binary through the harness; pty's private builder
// is gone, and live still honours its own pre-built-artifact override.
func TestBuildBinary_SuitesCallTheHarness(t *testing.T) {
	isBuild := func(c *ast.CallExpr) bool { p, n := callee(c); return p == "launcher" && n == "BuildBinary" }
	pty := ptySources(t)
	if n := countCalls(pty, isBuild); n < 10 {
		t.Errorf("test/pty calls launcher.BuildBinary %d times, want one per spawning test (16 buildBinary calls today)", n)
	}
	if got := funcNamed(pty, "buildBinary"); len(got) != 0 {
		t.Errorf("test/pty still declares its own buildBinary: %v", got)
	}
	live := liveSources(t)
	if n := countCalls(live, isBuild); n != 1 {
		t.Errorf("test/live calls launcher.BuildBinary %d times, want 1 (from binaryPath)", n)
	}
	if len(literalHits(live, func(s string) bool { return s == "ORBIT_LAUNCHER_LIVE_BINARY" })) == 0 {
		t.Error("test/live lost the ORBIT_LAUNCHER_LIVE_BINARY override for a pre-built artifact")
	}
}

var hermeticSetting = regexp.MustCompile(`^(TERM|NO_COLOR|ORBIT_LAUNCHER_NO_(UPDATE_CHECK|HEALTH_PROBE|VOLUME_CHECK|ANIMATION))=`)

// SU-19: the hermetic environment's six variables are spelled out once, in
// HermeticEnv, with the reason for each deviation next to the call that
// makes it.
func TestHermeticEnv_TheSixVariablesAreTypedOnlyInTheHarness(t *testing.T) {
	harness := launcherSources(t)
	for _, name := range []string{"TERM", "NO_COLOR", "ORBIT_LAUNCHER_NO_UPDATE_CHECK", "ORBIT_LAUNCHER_NO_HEALTH_PROBE",
		"ORBIT_LAUNCHER_NO_VOLUME_CHECK", "ORBIT_LAUNCHER_NO_ANIMATION"} {
		if len(literalHits(harness, func(s string) bool { return strings.Contains(s, name) })) == 0 {
			t.Errorf("the harness never spells out %s", name)
		}
	}
	for _, gf := range append(ptySources(t), liveSources(t)...) {
		for _, s := range stringConstants(gf.file) {
			if hermeticSetting.MatchString(s) {
				t.Errorf("%s retypes %q: use launcher.HermeticEnv", gf.rel, s)
			}
		}
	}
}

// Every spawn in the suites takes its environment from HermeticEnv: pty's
// three starters (splash, console, CI-shaped) and live's session.
func TestHermeticEnv_EverySpawnUsesIt(t *testing.T) {
	isHermetic := func(c *ast.CallExpr) bool { p, n := callee(c); return p == "launcher" && n == "HermeticEnv" }
	byFile := map[string]int{}
	for _, gf := range append(ptySources(t), liveSources(t)...) {
		byFile[gf.rel] = countCalls([]goFile{gf}, isHermetic)
	}
	for _, rel := range []string{"test/pty/splash_test.go", "test/pty/console_test.go", "test/pty/pumpdrop_test.go", "test/live/live_test.go"} {
		if byFile[rel] < 1 {
			t.Errorf("%s spawns the launcher without launcher.HermeticEnv", rel)
		}
	}
}

// SU-19: the fake install.sh server is the harness's. serveScript was
// serveOrbitTree with one path, so the harness has one server and the
// suites have none of their own.
func TestServeScript_OneFakeInstallServerInTheHarness(t *testing.T) {
	isServer := func(c *ast.CallExpr) bool { p, n := callee(c); return p == "httptest" && n == "NewServer" }
	if n := countCalls(launcherSources(t), isServer); n != 1 {
		t.Errorf("the harness starts an HTTP server in %d place(s), want exactly 1", n)
	}
	pty := ptySources(t)
	if n := countCalls(pty, isServer); n != 0 {
		t.Errorf("test/pty starts %d HTTP server(s) of its own; use launcher.ServeScript", n)
	}
	for _, name := range []string{"serveScript", "serveOrbitTree"} {
		if got := funcNamed(pty, name); len(got) != 0 {
			t.Errorf("test/pty still declares %s: %v", name, got)
		}
	}
	isServe := func(c *ast.CallExpr) bool { p, n := callee(c); return p == "launcher" && n == "ServeScript" }
	if n := countCalls(pty, isServe); n < 5 {
		t.Errorf("test/pty calls launcher.ServeScript %d times, want it used by every fetching test", n)
	}
}

// Passed already: the decisions keep the notice's phrase and duration typed
// in the black-box tests, because they are the contract, and a harness that
// computed them from the product would agree with any change.
func TestContractLiterals_StayTypedInTheSuitesNotTheHarness(t *testing.T) {
	const phrase = "I've read this and I understand"
	has := func(files []goFile, pred func(string) bool) bool { return len(literalHits(files, pred)) > 0 }
	isPhrase := func(s string) bool { return strings.Contains(s, phrase) }
	for _, rel := range []string{"test/pty", "test/live"} {
		files := loadAll(t, rel)
		if !has(files, isPhrase) {
			t.Errorf("%s no longer types the notice phrase", rel)
		}
		found := false
		for _, gf := range files {
			ast.Inspect(gf.file, func(n ast.Node) bool {
				if vs, ok := n.(*ast.ValueSpec); ok {
					for _, id := range vs.Names {
						if id.Name == "noticeWait" {
							found = true
						}
					}
				}
				return true
			})
		}
		if !found {
			t.Errorf("%s no longer declares noticeWait", rel)
		}
	}
	if has(launcherSources(t), isPhrase) {
		t.Error("the harness types the notice phrase; contract literals stay in the tests")
	}
}
