package release

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveManifest(t *testing.T, launcherTag string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"schema":"https://tomlawson.io/schemas/orbit-release-manifest/v1","version":"9.9.9","launcher":{"tag":%q,"commit":"0123456789012345678901234567890123456789"}}`, launcherTag)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCheckForUpdate_ReportsUpdateWhenLatestIsNewer(t *testing.T) {
	url := serveManifest(t, "v0.2.0")
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
	url := serveManifest(t, "v0.1.0")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when already current")
	}
}

func TestCheckForUpdate_NoUpdateWhenRunningVersionIsNewer(t *testing.T) {
	url := serveManifest(t, "v0.1.0")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.2.0")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when the running version is already ahead")
	}
}

func TestCheckForUpdate_IgnoresPreviewSuffixOnRunningVersion(t *testing.T) {
	url := serveManifest(t, "v0.1.0")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0-preview.abc123")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when the preview build matches the latest stable base version")
	}
}

func TestCheckForUpdate_NoErrorAndNoUpdateOnMissingStableManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	latest, hasUpdate, err := checkForUpdate(context.Background(), srv.URL, "0.1.0")
	if err != nil {
		t.Fatalf("expected no error for a 404 (no stable release manifest yet), got: %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when there's no manifest to compare against")
	}
	if latest != "" {
		t.Errorf("latest = %q, want empty", latest)
	}
}

func TestCheckForUpdate_NeverReportsAnUpdateForAnUnparseableRunningVersion(t *testing.T) {
	url := serveManifest(t, "v0.2.0")
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

func TestCheckForUpdate_ErrorsOnUnparseableLauncherTag(t *testing.T) {
	url := serveManifest(t, "not-a-version")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for an unparseable launcher.tag")
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false for an unparseable launcher.tag")
	}
}

func TestCheckForUpdate_RejectsLauncherTagWithPrereleaseSuffix(t *testing.T) {
	// launcher.tag must be a plain vX.Y.Z tag; anything with a suffix
	// fails the strict pattern even though parseSemver alone could parse
	// its numeric prefix.
	url := serveManifest(t, "v0.2.0-preview.abc123")
	_, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for a launcher.tag with a prerelease suffix")
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false for a launcher.tag with a prerelease suffix")
	}
}

func TestCheckForUpdate_NoUpdateOnMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"launcher": {`)
	}))
	defer srv.Close()

	_, hasUpdate, err := checkForUpdate(context.Background(), srv.URL, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false for malformed JSON")
	}
}

func TestCheckForUpdate_NoUpdateOnOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A huge JSON array that never closes before the cap bites, so
		// decoding fails instead of succeeding on a truncated document.
		fmt.Fprint(w, `{"launcher": {"tag": "`+strings.Repeat("v0.0.0", maxUpdateCheckResponseBytes)+`"}}`)
	}))
	defer srv.Close()

	_, hasUpdate, err := checkForUpdate(context.Background(), srv.URL, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for a body larger than the cap")
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false for a body larger than the cap")
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
	url := serveManifest(t, "v0.2.0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := checkForUpdate(ctx, url, "0.1.0")
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
}

func TestCheckForUpdate_RequestsOnlyTheGivenURL(t *testing.T) {
	// CheckForUpdate must never hit an orbit-launcher release endpoint —
	// #171 stopped publishing those, and #172 replaced that check with
	// Orbit's release manifest. checkForUpdate takes the URL as a
	// parameter and this test asserts the request lands only there.
	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.Host + r.URL.Path
		fmt.Fprint(w, `{"launcher": {"tag": "v0.1.0"}}`)
	}))
	defer srv.Close()

	if _, _, err := checkForUpdate(context.Background(), srv.URL, "0.1.0"); err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if requested == "" {
		t.Fatal("expected a request to be made")
	}
	if strings.Contains(manifestURL, "orbit-launcher") {
		t.Fatalf("manifestURL must not name an orbit-launcher release endpoint, got %q", manifestURL)
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
