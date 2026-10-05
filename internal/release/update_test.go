package release

import (
	"context"
	"fmt"
	"io"
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

// fakeTransport answers every request in-process, so the exported
// CheckForUpdate (which hard-codes manifestURL and http.DefaultClient) can
// be driven without the network. It records the URL it was asked for.
type fakeTransport struct {
	requested string
	body      string
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.requested = r.URL.String()
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    r,
	}, nil
}

// useFakeTransport swaps http.DefaultClient's transport for the test's
// duration. Not parallel-safe, so no test here calls t.Parallel.
func useFakeTransport(t *testing.T, body string) *fakeTransport {
	t.Helper()
	fake := &fakeTransport{body: body}
	prev := http.DefaultClient.Transport
	http.DefaultClient.Transport = fake
	t.Cleanup(func() { http.DefaultClient.Transport = prev })
	return fake
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	prev := Version
	Version = v
	t.Cleanup(func() { Version = prev })
}

func TestCheckForUpdate_AsksOrbitsManifestAndComparesTheBuiltInVersion(t *testing.T) {
	fake := useFakeTransport(t, `{"version":"9.9.9","launcher":{"tag":"v0.3.0"}}`)
	setVersion(t, "0.2.0")

	latest, hasUpdate, err := CheckForUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if fake.requested != manifestURL {
		t.Errorf("requested %q, want Orbit's release manifest %q", fake.requested, manifestURL)
	}
	if latest != "v0.3.0" || !hasUpdate {
		t.Errorf("got (%q, %v), want (v0.3.0, true): the launcher tag, not Orbit's own version, against Version", latest, hasUpdate)
	}
}

func TestCheckForUpdate_DevBuildNeverClaimsAnUpdate(t *testing.T) {
	useFakeTransport(t, `{"launcher":{"tag":"v9.0.0"}}`)
	setVersion(t, "dev")

	latest, hasUpdate, err := CheckForUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if hasUpdate {
		t.Error("a dev build has nothing to compare against and must never claim an update")
	}
	if latest != "v9.0.0" {
		t.Errorf("latest = %q, want v9.0.0", latest)
	}
}

func TestCheckForUpdate_ErrorsOnAnUnbuildableURL(t *testing.T) {
	_, hasUpdate, err := checkForUpdate(context.Background(), "http://bad\x7fhost/manifest.json", "0.1.0")
	if err == nil || !strings.Contains(err.Error(), "build request") {
		t.Fatalf("expected a build-request error, got %v", err)
	}
	if hasUpdate {
		t.Error("expected hasUpdate = false when no request could be made")
	}
}

// A tag can match the vX.Y.Z pattern and still not be a usable number:
// digits too long for an int. That must be refused, not silently read as
// some other version.
func TestCheckForUpdate_ErrorsOnALauncherTagTooLargeToParse(t *testing.T) {
	url := serveManifest(t, "v99999999999999999999.0.0")
	latest, hasUpdate, err := checkForUpdate(context.Background(), url, "0.1.0")
	if err == nil || !strings.Contains(err.Error(), "could not parse") {
		t.Fatalf("expected a parse error, got %v", err)
	}
	if hasUpdate || latest != "" {
		t.Errorf("got (%q, %v), want (\"\", false)", latest, hasUpdate)
	}
}

// Major beats minor and patch: comparing field by field in the wrong
// order would tell someone on 1.0.0 that 0.9.9 is an upgrade.
func TestCheckForUpdate_ComparesMajorBeforeMinorAndPatch(t *testing.T) {
	cases := []struct {
		running, latest string
		want            bool
	}{
		{"1.0.0", "v0.9.9", false},
		{"0.9.9", "v1.0.0", true},
		{"1.2.9", "v1.3.0", true},
		{"1.3.0", "v1.2.9", false},
		{"1.2.3", "v1.2.4", true},
		{"1.2.4", "v1.2.3", false},
	}
	for _, c := range cases {
		url := serveManifest(t, c.latest)
		_, hasUpdate, err := checkForUpdate(context.Background(), url, c.running)
		if err != nil {
			t.Fatalf("running %s, latest %s: %v", c.running, c.latest, err)
		}
		if hasUpdate != c.want {
			t.Errorf("running %s, latest %s: hasUpdate = %v, want %v", c.running, c.latest, hasUpdate, c.want)
		}
	}
}

func TestParseSemver_RejectsANonNumericField(t *testing.T) {
	for _, input := range []string{"vx.2.3", "v1.x.3", "v1.2.x", "v1.2.3.4"} {
		if got, ok := parseSemver(input); ok {
			t.Errorf("parseSemver(%q) = %+v, true; want a refusal", input, got)
		}
	}
}
