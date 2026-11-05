package release

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// #336. ResolveTarget, Target and ErrUnresolvable are new (design note
// sections 1 and 4).

const (
	latestPath = "/releases/latest/download/orbit-release-manifest.json"
	pinnedPath = "/releases/download/v0.3.5/orbit-release-manifest.json"
)

type manifestServer struct {
	URL  string
	mu   sync.Mutex
	hits []string
}

func (s *manifestServer) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

// serveReleases answers the latest folder with v0.3.6 and the v0.3.5
// folder with v0.3.5, and records every path asked for.
func serveReleases(t *testing.T) *manifestServer {
	t.Helper()
	s := &manifestServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.Path)
		s.mu.Unlock()
		switch r.URL.Path {
		case latestPath:
			w.Write([]byte(manifestJSON("v0.3.6", testDigest, "v0.2.1")))
		case pinnedPath:
			w.Write([]byte(manifestJSON("v0.3.5", testDigest, "v0.2.1")))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// targetEnv sets the three variables ResolveTarget reads; "" unsets.
func targetEnv(t *testing.T, manifestFile, channel, base string) {
	t.Helper()
	for k, v := range map[string]string{
		"ORBIT_RELEASE_MANIFEST":               manifestFile,
		"ORBIT_CHANNEL":                        channel,
		"ORBIT_INSTALL_TEST_MANIFEST_BASE_URL": base,
	} {
		t.Setenv(k, v)
		if v == "" {
			os.Unsetenv(k)
		}
	}
}

func writeManifestFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "orbit-release-manifest.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveTarget_ManifestFileWinsOverEveryChannel(t *testing.T) {
	for _, channel := range []string{"", "latest", "v0.3.5", "preview"} {
		s := serveReleases(t)
		file := writeManifestFile(t, manifestJSON("v0.4.0", testDigest, "v0.2.1"))
		targetEnv(t, file, channel, s.URL)

		got, err := ResolveTarget(context.Background())
		if err != nil {
			t.Fatalf("channel %q: ResolveTarget: %v", channel, err)
		}
		if got.Version != "v0.4.0" || got.Pin {
			t.Errorf("channel %q: target = %+v, want v0.4.0 with Pin false", channel, got)
		}
		if n := len(s.paths()); n != 0 {
			t.Errorf("channel %q: made %d requests (%v), want none with a manifest file", channel, n, s.paths())
		}
	}
}

func TestResolveTarget_UnsetOrLatestChannelReadsLatestAndPins(t *testing.T) {
	for _, channel := range []string{"", "latest"} {
		s := serveReleases(t)
		targetEnv(t, "", channel, s.URL)

		got, err := ResolveTarget(context.Background())
		if err != nil {
			t.Fatalf("channel %q: ResolveTarget: %v", channel, err)
		}
		if got.Version != "v0.3.6" || !got.Pin {
			t.Errorf("channel %q: target = %+v, want v0.3.6 with Pin true", channel, got)
		}
		if p := s.paths(); len(p) != 1 || p[0] != latestPath {
			t.Errorf("channel %q: requests = %v, want just %s", channel, p, latestPath)
		}
	}
}

func TestResolveTarget_VersionChannelReadsThatReleaseAndDoesNotPin(t *testing.T) {
	s := serveReleases(t)
	targetEnv(t, "", "v0.3.5", s.URL)

	got, err := ResolveTarget(context.Background())
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.Version != "v0.3.5" || got.Pin {
		t.Errorf("target = %+v, want v0.3.5 with Pin false", got)
	}
	if p := s.paths(); len(p) != 1 || p[0] != pinnedPath {
		t.Errorf("requests = %v, want just %s", p, pinnedPath)
	}
}

func TestResolveTarget_PreviewAndOtherChannelsAreUnresolvableWithoutARequest(t *testing.T) {
	for _, channel := range []string{"preview", "nightly"} {
		s := serveReleases(t)
		targetEnv(t, "", channel, s.URL)

		got, err := ResolveTarget(context.Background())
		if !errors.Is(err, ErrUnresolvable) {
			t.Errorf("channel %q: err = %v, want ErrUnresolvable", channel, err)
		}
		if got.Version != "" {
			t.Errorf("channel %q: version = %q, want none", channel, got.Version)
		}
		if n := len(s.paths()); n != 0 {
			t.Errorf("channel %q: made requests %v, want none", channel, s.paths())
		}
	}
}

func TestResolveTarget_UsesTheBaseURLOverrideWhenSet(t *testing.T) {
	s := serveReleases(t)
	targetEnv(t, "", "", s.URL)
	if _, err := ResolveTarget(context.Background()); err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if len(s.paths()) == 0 {
		t.Error("the override server was never asked")
	}
}

func TestResolveTarget_AnUnreadableOrMalformedManifestFileIsAnErrorNeverAVersion(t *testing.T) {
	cases := map[string]string{
		"missing":         filepath.Join(t.TempDir(), "nope.json"),
		"not json":        writeManifestFile(t, "not json"),
		"bad version":     writeManifestFile(t, manifestJSON("0.3.6", testDigest, "v0.2.1")),
		"preview version": writeManifestFile(t, manifestJSON("v0.3.6-preview.1", testDigest, "v0.2.1")),
	}
	for name, file := range cases {
		s := serveReleases(t)
		targetEnv(t, file, "", s.URL)
		got, err := ResolveTarget(context.Background())
		if err == nil {
			t.Errorf("%s: want an error, got %+v", name, got)
		}
		if got.Version != "" {
			t.Errorf("%s: version = %q, want none", name, got.Version)
		}
	}
}

func TestResolveTarget_AFailedFetchIsAnErrorNeverAVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{broken"))
	}))
	t.Cleanup(srv.Close)
	targetEnv(t, "", "", srv.URL)
	got, err := ResolveTarget(context.Background())
	if err == nil || got.Version != "" {
		t.Errorf("got %+v, %v; want an error and no version", got, err)
	}
}
