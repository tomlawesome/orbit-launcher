// Package launcher is the black-box suites' shared harness (#219, SU-19):
// the one build of cmd/orbit-launcher, the hermetic environment every
// spawn starts from, and the stand-in for Orbit's file tree that serves
// install.sh. test/pty and test/live use it; test/visual, in TypeScript,
// cannot.
//
// The contract a suite checks stays typed in that suite: the development
// notice's phrase and how long it is waited out are not here, because a
// harness that took them from the product would agree with any change to
// them.
package launcher

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// buildDir holds the package's one binary. Run creates it before any test
// starts and removes it after the last one ends; a test's own TempDir
// would not do, as it goes when the test that happened to build first
// ends, while later tests still run the binary.
var buildDir string

// Run is a suite's TestMain body: it runs the package's tests and then
// removes the binary BuildBinary built for them.
//
//	func TestMain(m *testing.M) { os.Exit(launcher.Run(m)) }
func Run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "orbit-launcher-test-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher: make the build directory: %v\n", err)
		return 1
	}
	buildDir = dir
	defer func() { _ = os.RemoveAll(dir) }()
	return m.Run()
}

// binaryCoverDirEnv is set by scripts/coverage.sh so the binary's own runs
// count towards coverage (#169). It cannot be GOCOVERDIR itself: `go test
// -cover` points GOCOVERDIR at a directory of its own for each test
// process.
const binaryCoverDirEnv = "ORBIT_LAUNCHER_BINARY_COVERDIR"

var build = sync.OnceValues(func() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(buildDir, "orbit-launcher")
	args := []string{"build", "-o", bin}
	if dir := os.Getenv(binaryCoverDirEnv); dir != "" {
		args = append(args, "-cover", "-covermode=atomic", "-coverpkg=github.com/tomlawesome/orbit-launcher/...")
		// Every spawn passes os.Environ() on (see HermeticEnv), so
		// setting GOCOVERDIR here reaches each binary the suite starts.
		if err := os.Setenv("GOCOVERDIR", dir); err != nil {
			return "", fmt.Errorf("point the binary's coverage at %s: %w", dir, err)
		}
	}
	cmd := exec.Command("go", append(args, "./cmd/orbit-launcher")...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, out)
	}
	return bin, nil
})

// BuildBinary compiles cmd/orbit-launcher once per test package and
// returns its path, so the suites exercise the binary a release would
// ship, not a stand-in, without paying for a build in every test. The
// package's TestMain must call Run, which owns the binary's directory.
func BuildBinary(t testing.TB) string {
	t.Helper()
	if buildDir == "" {
		t.Fatal("launcher.BuildBinary needs the package's TestMain to call launcher.Run")
	}
	bin, err := build()
	if err != nil {
		t.Fatalf("build orbit-launcher: %v", err)
	}
	return bin
}

// moduleRoot is the nearest directory above the working directory (a
// suite's own package directory under go test) that holds go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the working directory")
		}
		dir = parent
	}
}

// Behaviour is something the launcher does that reaches past what a
// black-box test controls. HermeticEnv turns each one off unless the
// caller keeps it; its value is the variable that turns it off.
type Behaviour string

const (
	// UpdateCheck asks GitHub for a newer release: whether GitHub is
	// reachable from the runner is not what a suite tests, the same
	// reason every other test here mocks its network calls.
	UpdateCheck Behaviour = "ORBIT_LAUNCHER_NO_UPDATE_CHECK"
	// HealthProbe requests the detected deployment's APP_URL. Kept by
	// suites that need its result to land mid-run, as CI's does.
	HealthProbe Behaviour = "ORBIT_LAUNCHER_NO_HEALTH_PROBE"
	// VolumeCheck reads the host's Docker volumes before an install
	// (#105): a volume left behind by an earlier run would put a screen
	// in front of the install that no suite expects. It has its own unit
	// and seam tests instead of tests on machine state.
	VolumeCheck Behaviour = "ORBIT_LAUNCHER_NO_VOLUME_CHECK"
	// RunningCheck asks the host's Docker what runs under the
	// deployment's project (#316): a container left by another run, or
	// started from another folder, would put rows and refusals on screens
	// no suite expects. It has its own unit and seam tests instead of
	// tests on machine state.
	RunningCheck Behaviour = "ORBIT_LAUNCHER_NO_RUNNING_CHECK"
	// Animation is the splash's arrival (#120): keys are swallowed until
	// it ends, so a selection sent too early is eaten as the skip. Its
	// own behaviour is internal/ui's to test and its look test/visual's.
	Animation Behaviour = "ORBIT_LAUNCHER_NO_ANIMATION"
)

// HermeticEnv is the environment a suite starts the launcher with: the
// test process's own, a plain xterm with NO_COLOR (the suites prove
// behaviour, not appearance, which is test/visual's job), and every
// Behaviour off except those in keep. A caller that keeps one says why at
// the call.
func HermeticEnv(keep ...Behaviour) []string {
	env := append(os.Environ(), "TERM=xterm", "NO_COLOR=1")
	for _, b := range []Behaviour{UpdateCheck, HealthProbe, VolumeCheck, RunningCheck, Animation} {
		if !slices.Contains(keep, b) {
			env = append(env, string(b)+"=1")
		}
	}
	return env
}

// InstallScriptPath is where ServeScript serves install.sh, as Orbit's raw
// file tree does.
const InstallScriptPath = "/scripts/install.sh"

// File is one more file in the tree ServeScript serves; Path is from the
// tree's root ("/scripts/repair.sh").
type File struct {
	Path, Body string
}

// ServeScript stands up a local stand-in for Orbit's raw file tree that
// serves script as install.sh, plus any other files, and answers 404 for
// everything else. It returns install.sh's URL, for
// ORBIT_LAUNCHER_INSTALL_SCRIPT_URL -- the override Orbit's
// launcher-install-compat gate uses. The server closes when the test ends.
func ServeScript(t testing.TB, script string, also ...File) string {
	t.Helper()
	files := map[string]string{InstallScriptPath: script}
	for _, f := range also {
		files[f.Path] = f.Body
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL + InstallScriptPath
}
