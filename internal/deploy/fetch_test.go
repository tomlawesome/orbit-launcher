package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setScriptFetchTimeout swaps the shared fetch client for one with a short
// deadline and returns the restore function.
func setScriptFetchTimeout(t *testing.T, d time.Duration) func() {
	t.Helper()
	prev := scriptFetchClient
	scriptFetchClient = &http.Client{Timeout: d}
	return func() { scriptFetchClient = prev }
}

func TestFetchInstallScript_ReturnsBodyOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/usr/bin/env bash\necho hello\n"))
	}))
	defer srv.Close()

	body, err := fetchInstallScript(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchInstallScript: %v", err)
	}
	if !strings.Contains(string(body), "echo hello") {
		t.Errorf("body = %q, want it to contain the script content", body)
	}
}

func TestFetchInstallScript_RejectsNon200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchInstallScript(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}

func TestFetchInstallScript_RejectsContentWithoutAShebang(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>this is not a script</html>"))
	}))
	defer srv.Close()

	_, err := fetchInstallScript(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for content without a shebang")
	}
}

func TestFetchInstallScript_RejectsOversizedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/usr/bin/env bash\n"))
		oversized := make([]byte, maxInstallScriptBytes+1)
		w.Write(oversized)
	}))
	defer srv.Close()

	_, err := fetchInstallScript(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for oversized content")
	}
}

func TestFetchInstallScript_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/usr/bin/env bash\n"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fetchInstallScript(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}

func TestFetchInstallScript_URLOverrideRedirectsAwayFromOrbitMain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/usr/bin/env bash\necho from-override\n"))
	}))
	defer srv.Close()

	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", srv.URL)

	body, err := FetchInstallScript(context.Background())
	if err != nil {
		t.Fatalf("FetchInstallScript: %v", err)
	}
	if !strings.Contains(string(body), "from-override") {
		t.Errorf("body = %q, want the overridden server's content, not orbit's real main branch", body)
	}
}

func TestFetchInstallScript_PathOverrideSkipsTheNetworkEntirely(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no HTTP request should be made when ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH is set")
	}))
	defer srv.Close()

	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", srv.URL)

	dir := t.TempDir()
	path := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\necho from-local-file\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH", path)

	body, err := FetchInstallScript(context.Background())
	if err != nil {
		t.Fatalf("FetchInstallScript: %v", err)
	}
	if !strings.Contains(string(body), "from-local-file") {
		t.Errorf("body = %q, want the local file's content", body)
	}
}

func TestFetchInstallScript_PathOverrideRefusesAMissingFile(t *testing.T) {
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH", filepath.Join(t.TempDir(), "does-not-exist.sh"))

	_, err := FetchInstallScript(context.Background())
	if err == nil {
		t.Fatal("expected an error for a missing ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH")
	}
}

func TestFetchInstallScript_PathOverrideRefusesAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n"), 0o000); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions don't block reads")
	}
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH", path)

	_, err := FetchInstallScript(context.Background())
	if err == nil {
		t.Fatal("expected an error for an unreadable ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH")
	}
}

func TestFetchInstallScript_PathOverrideRefusesAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH", path)

	_, err := FetchInstallScript(context.Background())
	if err == nil {
		t.Fatal("expected an error for an empty ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH")
	}
}

func TestFetchInstallScript_UnsetPathKeepsDownloading(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Write([]byte("#!/usr/bin/env bash\necho from-network\n"))
	}))
	defer srv.Close()

	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", srv.URL)

	body, err := FetchInstallScript(context.Background())
	if err != nil {
		t.Fatalf("FetchInstallScript: %v", err)
	}
	if !hit {
		t.Error("expected the launcher to fall back to downloading when the path override is unset")
	}
	if !strings.Contains(string(body), "from-network") {
		t.Errorf("body = %q, want the downloaded content", body)
	}
}

// A server that accepts the connection and never answers is what a
// firewalled or black-holed network looks like from the launcher's side
// (#147). The fetch must give up on its own: the UI calls it with a
// background context, so nothing else will.
func TestFetchInstallScript_GivesUpWhenTheServerNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	restore := setScriptFetchTimeout(t, 100*time.Millisecond)
	defer restore()

	done := make(chan error, 1)
	go func() {
		_, err := fetchInstallScript(context.Background(), srv.URL)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("error should say the server did not answer, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch is still waiting after 2s: it has no deadline of its own")
	}
}

func TestFetchFile_GivesUpWhenTheServerNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	restore := setScriptFetchTimeout(t, 100*time.Millisecond)
	defer restore()

	done := make(chan error, 1)
	go func() {
		_, err := fetchFile(context.Background(), srv.URL)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fetch is still waiting after 2s: it has no deadline of its own")
	}
}
