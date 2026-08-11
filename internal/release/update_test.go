package release

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveTagName(t *testing.T, tag string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name": %q}`, tag)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCheckForUpdate_ReportsUpdateWhenLatestIsNewer(t *testing.T) {
	url := serveTagName(t, "v0.2.0")
	latest, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if !hasUpdate {
		t.Error("expected hasUpdate = true when latest is newer")
	}
	if latest != "v0.2.0" {
		t.Errorf("latest = %q, want v0.2.0", latest)
	}
}

func TestCheckForUpdate_NoUpdateWhenAlreadyCurrent(t *testing.T) {
	url := serveTagName(t, "v0.1.0")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when already current")
	}
}

func TestCheckForUpdate_NoUpdateWhenRunningVersionIsNewer(t *testing.T) {
	url := serveTagName(t, "v0.1.0")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.2.0")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when the running version is already ahead")
	}
}

func TestCheckForUpdate_IgnoresPreviewSuffixOnRunningVersion(t *testing.T) {
	url := serveTagName(t, "v0.1.0")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0-preview.abc123")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when the preview build matches the latest stable base version")
	}
}

func TestCheckForUpdate_NoErrorAndNoUpdateOnMissingStableRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	latest, hasUpdate, err := checkForUpdate(context.Background(), srv.URL, "0.1.0")
	if err != nil {
		t.Fatalf("expected no error for a 404 (no stable release yet), got: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when there's no stable release to compare against")
	}
	if latest != "" {
		t.Errorf("latest = %q, want empty", latest)
	}
}

func TestCheckForUpdate_NeverReportsAnUpdateForAnUnparseableRunningVersion(t *testing.T) {
	url := serveTagName(t, "v0.2.0")
	latest, hasUpdate, err := checkForUpdate(context.Background(), url, "dev")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false for an unparseable running version like \"dev\"")
	}
	if latest != "v0.2.0" {
		t.Errorf("latest = %q, want v0.2.0 (still reported even without a comparison)", latest)
	}
}

func TestCheckForUpdate_ErrorsOnUnparseableReleaseTag(t *testing.T) {
	url := serveTagName(t, "not-a-version")
	_, _, err := checkForUpdate(context.Background(), url, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for an unparseable release tag")
	}
}

func TestCheckForUpdate_ErrorsOnUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _, err := checkForUpdate(context.Background(), srv.URL, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestCheckForUpdate_RespectsContextCancellation(t *testing.T) {
	url := serveTagName(t, "v0.2.0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := checkForUpdate(ctx, url, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}

func TestParseSemver(t *testing.T) {
	cases := []struct {
		input string
		want  semver
		ok    bool
	}{
		{"v1.2.3", semver{1, 2, 3}, true},
		{"1.2.3", semver{1, 2, 3}, true},
		{"v0.1.0-preview.abc123", semver{0, 1, 0}, true},
		{"v1.2", semver{}, false},
		{"not-a-version", semver{}, false},
		{"", semver{}, false},
	}
	for _, c := range cases {
		got, ok := parseSemver(c.input)
		if ok != c.ok {
			t.Errorf("parseSemver(%q) ok = %v, want %v", c.input, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parseSemver(%q) = %+v, want %+v", c.input, got, c.want)
		}
	}
}
