package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
