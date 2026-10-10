package deploy

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// #207 (#199 WI-3: EN-3, EN-4). Every outbound call owns its time limit
// in the package that makes it; the UI only cancels. A detached engine
// script is built one way, and cancelling it stops its whole process
// group without waiting on a pipe a child still holds.

// defaultClientUses lists every use, in the non-test Go files of dir, of
// http.DefaultClient or of a package-level net/http helper that sends
// through it (http.Get, Head, Post, PostForm). Such a call has no time
// limit of its own.
func defaultClientUses(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	implicit := map[string]bool{"DefaultClient": true, "Get": true, "Head": true, "Post": true, "PostForm": true}
	var found []string
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		httpName := ""
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "net/http" {
				httpName = "http"
				if imp.Name != nil {
					httpName = imp.Name.Name
				}
			}
		}
		if httpName == "" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == httpName && implicit[sel.Sel.Name] {
				found = append(found, fset.Position(sel.Pos()).String()+": http."+sel.Sel.Name)
			}
			return true
		})
	}
	return found
}

// Done-criterion of #207: no http.DefaultClient in deploy. A call through
// it has no limit unless every caller remembers to bring one.
func TestDeploySource_NeverUsesTheDefaultHTTPClient(t *testing.T) {
	for _, use := range defaultClientUses(t, ".") {
		t.Errorf("%s: deploy's outbound calls own their limit; http.DefaultClient has none", use)
	}
}

// neverAnswers is a server that accepts the request and never replies
// until the test ends.
func neverAnswers(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv.URL
}

// EN-3: ProbeHealth owns its limit (the 2 s leaves the splash). Called
// with no deadline at all, a probe of a silent server still gives up on
// its own and reads as degraded.
func TestProbeHealth_GivesUpOnItsOwnWithoutACallerDeadline(t *testing.T) {
	url := neverAnswers(t)

	done := make(chan bool, 1)
	go func() { done <- ProbeHealth(context.Background(), url) }()

	select {
	case alive := <-done:
		if alive {
			t.Error("a server that never answered read as alive")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("ProbeHealth is still waiting after 6s with no caller deadline: it has no limit of its own")
	}
}

// EN-3: a caller's deadline shorter than the fetch's own limit is the
// limit actually applied, so the error must not claim the launcher's own
// 30 s.
func TestFetchInstallScript_ACallersDeadlineIsNotReportedAsTheLaunchersOwn(t *testing.T) {
	url := neverAnswers(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := fetchInstallScript(ctx, url)
	if err == nil {
		t.Fatal("expected an error from a server that never answered")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("error should say the server did not answer, got: %v", err)
	}
	if strings.Contains(err.Error(), "30s") {
		t.Errorf("the caller's 150ms deadline was reported as the launcher's own 30s: %v", err)
	}
}

// EN-3: fetchError words the deadline it was given, so a fetch whose own
// limit is 100 ms says 100 ms.
func TestFetchInstallScript_TimeoutNamesTheLimitApplied(t *testing.T) {
	url := neverAnswers(t)
	restore := setScriptFetchTimeout(t, 100*time.Millisecond)
	defer restore()

	_, err := fetchInstallScript(context.Background(), url)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "100ms") {
		t.Errorf("error should name the 100ms limit that applied, got: %v", err)
	}
	if strings.Contains(err.Error(), "30s") {
		t.Errorf("error names 30s, a limit that did not apply: %v", err)
	}
}

// recordedPIDs reads the process ids a fake script appended to path, one
// per line.
func recordedPIDs(t *testing.T, path string) []int {
	t.Helper()
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, field := range strings.Fields(string(body)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("pid file %s holds %q", path, field)
		}
		pids = append(pids, pid)
	}
	return pids
}

// killRecordedOnCleanup kills every process recorded in path when the
// test ends, so a failing test leaves nothing running.
func killRecordedOnCleanup(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		for _, pid := range recordedPIDs(t, path) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

// waitForPIDs waits until path records at least n processes.
func waitForPIDs(t *testing.T, path string, n int) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pids := recordedPIDs(t, path); len(pids) >= n {
			return pids
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fake script never recorded %d processes in %s", n, path)
	return nil
}

// running reports whether pid is a live process; a zombie (exited,
// waiting to be reaped) counts as stopped.
func running(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return false
	}
	return s[i+2] != 'Z' && s[i+2] != 'X'
}

// lingeringCheckTree is a configure tree whose configure.sh --check
// starts a background child that inherits stdout and sleeps, then waits
// for it. It records its own pid and the child's in the returned file.
func lingeringCheckTree(t *testing.T) (dir, pidFile string) {
	t.Helper()
	dir = t.TempDir()
	pidFile = filepath.Join(t.TempDir(), "pids")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\n" +
		"echo $$ >> '" + pidFile + "'\n" +
		"sleep 30 &\n" +
		"echo $! >> '" + pidFile + "'\n" +
		"wait\n"
	if err := os.WriteFile(filepath.Join(dir, "scripts", "configure.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	killRecordedOnCleanup(t, pidFile)
	return dir, pidFile
}

// EN-4 done-criterion of #207: a cancelled RunConfigCheck whose child
// still holds stdout returns within the wait delay, not when the child
// finally lets go of the pipe.
func TestRunConfigCheck_CancelledCheckReturnsWhileAChildHoldsStdout(t *testing.T) {
	dir, pidFile := lingeringCheckTree(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RunConfigCheck(ctx, dir)
		done <- err
	}()

	waitForPIDs(t, pidFile, 2)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled check returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunConfigCheck is still waiting 5s after cancel: a child holding stdout keeps it open")
	}
}

// EN-4: the detached command's Cancel stops the whole process group, so
// cancelling a check leaves none of its children running.
func TestRunConfigCheck_CancelStopsTheChecksChildren(t *testing.T) {
	dir, pidFile := lingeringCheckTree(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, _ = RunConfigCheck(ctx, dir) }()

	pids := waitForPIDs(t, pidFile, 2)
	child := pids[1]
	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for running(child) {
		if time.Now().After(deadline) {
			t.Fatalf("the check's background child (pid %d) is still running 5s after cancel: only bash was stopped", child)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hangingDockerOnPath puts a docker on PATH that never answers, and
// kills every one it started when the test ends. It returns the file each
// started docker records its pid in.
func hangingDockerOnPath(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	pidFile := filepath.Join(binDir, "pids")
	script := "#!/usr/bin/env bash\necho $$ >> '" + pidFile + "'\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	killRecordedOnCleanup(t, pidFile)
	return pidFile
}

// EN-3: StandDown gets a bound of its own (5 min by default) instead of
// running for as long as Docker takes. The test shortens the bound with
// setStandDownTimeout(t, d), a hook the implementation provides beside
// setScriptFetchTimeout: it sets StandDown's limit to d and restores the
// default when the test ends. A stand-down that runs out of time fails
// with an error naming the limit, and the docker it started is stopped.
func TestStandDown_GivesUpAtItsOwnLimitWhenDockerHangs(t *testing.T) {
	pidFile := hangingDockerOnPath(t)
	setStandDownTimeout(t, 200*time.Millisecond)
	dir := t.TempDir()

	done := make(chan error, 1)
	go func() { done <- StandDown(context.Background(), dir) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stand-down whose docker never finished returned no error")
		}
		if !strings.Contains(err.Error(), "200ms") {
			t.Errorf("error should name the 200ms limit that applied, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StandDown is still waiting 5s into a 200ms limit with no caller deadline: it has no bound of its own")
	}

	pids := recordedPIDs(t, pidFile)
	if len(pids) == 0 {
		t.Fatal("StandDown never started docker")
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range pids {
		for running(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("the docker StandDown started (pid %d) is still running after the limit ran out", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// EN-3: deploy owns one limit for Docker queries (the two 5 s leave the
// UI). With no caller deadline, a docker that never answers still ends
// the install-date lookup, which reads as "unknown".
func TestInstalledAt_GivesUpOnItsOwnWhenDockerHangs(t *testing.T) {
	hangingDockerOnPath(t)

	done := make(chan time.Time, 1)
	go func() {
		done <- InstalledAt(context.Background(), &Deployment{TargetDir: "/opt/orbit", Project: "orbit"})
	}()

	select {
	case got := <-done:
		if !got.IsZero() {
			t.Errorf("InstalledAt = %v from a docker that never answered; want the zero time", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("InstalledAt is still waiting after 10s with no caller deadline: the Docker query has no limit of its own")
	}
}

// EN-3: the volume query shares that limit.
func TestUnownedDatabaseVolumes_GivesUpOnItsOwnWhenDockerHangs(t *testing.T) {
	hangingDockerOnPath(t)
	dir := t.TempDir()

	done := make(chan []DatabaseVolume, 1)
	go func() { done <- UnownedDatabaseVolumes(context.Background(), dir) }()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("UnownedDatabaseVolumes = %+v from a docker that never answered; want none", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UnownedDatabaseVolumes is still waiting after 10s with no caller deadline: the Docker query has no limit of its own")
	}
}
