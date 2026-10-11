// Package testutil holds the test helpers that two or more product
// packages' tests need (#219, SU-20): a fake executable on PATH, a script
// source that counts what is fetched from it, the .env-orbit fixture and
// a scripts/ tree stager. Helpers one package needs stay in that
// package's helpers_test.go.
//
// It imports neither internal/deploy nor internal/ui: their tests are in
// package, so either import would be a cycle.
package testutil

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// FakeExecutable puts an executable called name, running script, in front
// of the real ones on PATH for the rest of the test, and returns the
// directory it is in. The test cannot be parallel (t.Setenv).
func FakeExecutable(t testing.TB, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// CountingScriptSource points the install.sh override
// (ORBIT_LAUNCHER_INSTALL_SCRIPT_URL) at a server that counts every
// request and serves, for any path, a script that must never be run, so a
// test can prove nothing was fetched. The test cannot be parallel
// (t.Setenv).
func CountingScriptSource(t testing.TB) *atomic.Int32 {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("#!/usr/bin/env bash\necho 'fetched — never run this'\nexit 0\n"))
	}))
	t.Cleanup(server.Close)
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", server.URL+"/scripts/install.sh")
	return &requests
}

// EnvOrbit is the .env-orbit of a detected deployment: an APP_URL and a
// pinned image, the two fields the launcher shows.
const EnvOrbit = "APP_URL=https://mail.example.com\nORBIT_IMAGE=ghcr.io/tomlawesome/orbit@sha256:abc\n"

// WriteEnvOrbit writes EnvOrbit, then any extra lines, to path with the
// mode install.sh gives the file. The caller names the file with deploy's
// EnvFile: this package cannot import deploy, and the name is typed only
// there (#208).
func WriteEnvOrbit(t testing.TB, path string, extra ...string) {
	t.Helper()
	body := EnvOrbit
	for _, line := range extra {
		body += line + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the env fixture: %v", err)
	}
}

// File is one file of a tree StageTree writes: its path from the tree's
// root ("scripts/repair.sh", ".env-orbit.example"), its body and its mode.
type File struct {
	Path string
	Body string
	Mode os.FileMode
}

// StageTree makes dir/scripts with scriptsMode and writes files into dir,
// either at its top or in scripts/: a deployment as install.sh leaves it,
// or the configuration tree it hands over. Every mode is set exactly, with
// Chmod, so a test gets the modes it names whatever the umask.
func StageTree(t testing.TB, dir string, scriptsMode os.FileMode, files ...File) {
	t.Helper()
	scripts := filepath.Join(dir, "scripts")
	if err := os.Mkdir(scripts, scriptsMode); err != nil {
		t.Fatalf("stage scripts/: %v", err)
	}
	if err := os.Chmod(scripts, scriptsMode); err != nil {
		t.Fatalf("stage scripts/: %v", err)
	}
	for _, f := range files {
		if parent := filepath.Dir(f.Path); parent != "." && parent != "scripts" {
			t.Fatalf("stage %s: StageTree writes only the tree's top and scripts/", f.Path)
		}
		path := filepath.Join(dir, f.Path)
		if err := os.WriteFile(path, []byte(f.Body), f.Mode); err != nil {
			t.Fatalf("stage %s: %v", f.Path, err)
		}
		if err := os.Chmod(path, f.Mode); err != nil {
			t.Fatalf("stage %s: %v", f.Path, err)
		}
	}
}
