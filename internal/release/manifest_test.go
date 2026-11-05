package release

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #336. fetchManifest and Manifest are new (design note section 4).

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func manifestJSON(version, digest, launcherTag string) string {
	return fmt.Sprintf(`{"schema":"https://tomlawson.io/schemas/orbit-release-manifest/v1","version":%q,"image":{"repository":"ghcr.io/tomlawesome/orbit","digest":%q},"launcher":{"tag":%q,"commit":"0123456789012345678901234567890123456789"}}`, version, digest, launcherTag)
}

func serveBody(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetchManifest_ReturnsVersionDigestAndLauncherTag(t *testing.T) {
	url := serveBody(t, http.StatusOK, manifestJSON("v0.3.6", testDigest, "v0.2.1"))
	m, err := fetchManifest(context.Background(), url)
	if err != nil {
		t.Fatalf("fetchManifest: %v", err)
	}
	if m.Version != "v0.3.6" || m.Digest != testDigest || m.LauncherTag != "v0.2.1" {
		t.Errorf("manifest = %+v, want v0.3.6 / %s / v0.2.1", m, testDigest)
	}
}

func TestFetchManifest_RejectsAVersionThatIsNotPlainSemver(t *testing.T) {
	for _, v := range []string{"0.3.6", "v0.3.6-preview.1", "v03.6", "", "v0.3.6\n"} {
		url := serveBody(t, http.StatusOK, manifestJSON(v, testDigest, "v0.2.1"))
		m, err := fetchManifest(context.Background(), url)
		if err == nil {
			t.Errorf("version %q: want an error, got %+v", v, m)
		}
		if m.Version != "" {
			t.Errorf("version %q: a rejected manifest must carry no version, got %q", v, m.Version)
		}
	}
}

func TestFetchManifest_RejectsADigestWithoutSha256(t *testing.T) {
	for _, d := range []string{"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "sha256:abc", "md5:" + strings.Repeat("a", 64)} {
		url := serveBody(t, http.StatusOK, manifestJSON("v0.3.6", d, "v0.2.1"))
		if m, err := fetchManifest(context.Background(), url); err == nil {
			t.Errorf("digest %q: want an error, got %+v", d, m)
		}
	}
}

func TestFetchManifest_RejectsALauncherTagWithASuffix(t *testing.T) {
	url := serveBody(t, http.StatusOK, manifestJSON("v0.3.6", testDigest, "v0.2.1-preview.1"))
	if m, err := fetchManifest(context.Background(), url); err == nil {
		t.Errorf("want an error for a launcher tag with a suffix, got %+v", m)
	}
}

func TestFetchManifest_A404IsNotAnErrorAndYieldsNoVersion(t *testing.T) {
	url := serveBody(t, http.StatusNotFound, "")
	m, err := fetchManifest(context.Background(), url)
	if err != nil {
		t.Fatalf("a 404 (not published) must not be an error, got %v", err)
	}
	if m.Version != "" {
		t.Errorf("version = %q, want none", m.Version)
	}
}

func TestFetchManifest_RejectsABodyOver64KiB(t *testing.T) {
	// Valid JSON throughout, so only the cap can reject it.
	body := fmt.Sprintf(`{"pad":%q,"version":"v0.3.6","image":{"digest":%q},"launcher":{"tag":"v0.2.1"}}`, strings.Repeat("a", 1<<16), testDigest)
	url := serveBody(t, http.StatusOK, body)
	if m, err := fetchManifest(context.Background(), url); err == nil {
		t.Errorf("want an error for a body over 64 KiB, got %+v", m)
	}
}

func TestFetchManifest_ErrorsOnAnUnexpectedStatus(t *testing.T) {
	url := serveBody(t, http.StatusInternalServerError, "")
	if _, err := fetchManifest(context.Background(), url); err == nil {
		t.Error("want an error for a 500")
	}
}

func TestTargetLookupTimeoutIsTenSeconds(t *testing.T) {
	if targetLookupTimeout != 10*time.Second {
		t.Errorf("targetLookupTimeout = %v, want 10s", targetLookupTimeout)
	}
}

// A server that never answers is abandoned with an error. The 10 s bound
// itself is the constant above; waiting it out here would cost 10 s, so
// the call is ended through its context instead.
func TestFetchManifest_AbandonsAServerThatNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := fetchManifest(ctx, srv.URL); err == nil {
		t.Fatal("want an error from a server that never answers")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("fetchManifest took %v; it must give up when its context ends", time.Since(start))
	}
}

// #337 T-F1. The app sets no deadline of its own on the confirm screen's
// lookup, so the client's own Timeout is the only bound.

func TestTargetLookupClientCarriesTheTimeout(t *testing.T) {
	if targetLookupClient.Timeout != targetLookupTimeout {
		t.Errorf("targetLookupClient.Timeout = %v, want %v", targetLookupClient.Timeout, targetLookupTimeout)
	}
}

// silentServer never answers until the test ends.
func silentServer(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv.URL
}

func shortLookupClient(t *testing.T) {
	t.Helper()
	old := targetLookupClient
	targetLookupClient = &http.Client{Timeout: 100 * time.Millisecond}
	t.Cleanup(func() { targetLookupClient = old })
}

// returnsWithin fails the test if call has not returned within 3 s, so a
// missing client bound fails fast instead of hanging until go test's own
// timeout. The leaked call ends when the cleanup releases the server.
func returnsWithin(t *testing.T, what string, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("%s: want an error from a server that never answers", what)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s still waiting after 3s; the client's own timeout must end it", what)
	}
}

func TestFetchManifest_ClientTimeoutBoundsASilentServerWithNoContextDeadline(t *testing.T) {
	url := silentServer(t)
	shortLookupClient(t)
	returnsWithin(t, "fetchManifest", func() error {
		_, err := fetchManifest(context.Background(), url)
		return err
	})
}

func TestResolveTarget_ClientTimeoutBoundsASilentServerWithNoContextDeadline(t *testing.T) {
	url := silentServer(t)
	shortLookupClient(t)
	targetEnv(t, "", "", url)
	returnsWithin(t, "ResolveTarget", func() error {
		_, err := ResolveTarget(context.Background())
		return err
	})
}
