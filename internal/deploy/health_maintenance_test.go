package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// #302: ProbeHealth asks <APP_URL>/api/health first, so an instance that is
// deliberately closed (its home page answers 503) reads as "closed to
// users" rather than as a server error. These tests use one server whose
// handler counts the hits per path.

type pathCounter struct {
	mu   sync.Mutex
	hits map[string]int
	auth map[string]string
}

func (c *pathCounter) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hits == nil {
		c.hits = map[string]int{}
		c.auth = map[string]string{}
	}
	c.hits[r.URL.Path]++
	if a := r.Header.Get("Authorization"); a != "" {
		c.auth[r.URL.Path] = a
	}
}

func (c *pathCounter) n(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits[path]
}

func (c *pathCounter) authFor(path string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.auth[path]
}

// healthServer answers /api/health with apiStatus and apiBody and every
// other path (the home page) with rootStatus, counting hits per path.
func healthServer(t *testing.T, apiStatus int, apiBody string, rootStatus int) (*httptest.Server, *pathCounter) {
	t.Helper()
	c := &pathCounter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.record(r)
		if r.URL.Path == "/api/health" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(apiStatus)
			_, _ = w.Write([]byte(apiBody))
			return
		}
		w.WriteHeader(rootStatus)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestProbeHealth_MaintenanceReasonText(t *testing.T) {
	if got := string(HealthMaintenance); got != "closed to users" {
		t.Errorf("HealthMaintenance = %q, want %q", got, "closed to users")
	}
}

func TestProbeHealth_APIHealthMaintenanceReadsAsClosed(t *testing.T) {
	srv, c := healthServer(t, http.StatusOK, `{"status":"maintenance"}`, http.StatusServiceUnavailable)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthMaintenance {
		t.Errorf("status maintenance = %q, want %q", got, HealthMaintenance)
	}
	if n := c.n("/api/health"); n != 1 {
		t.Errorf("/api/health requested %d times, want 1", n)
	}
	if n := c.n("/"); n != 0 {
		t.Errorf("/ requested %d times, want 0 once /api/health answered", n)
	}
}

func TestProbeHealth_APIHealthMaintenanceWithExtraFields(t *testing.T) {
	srv, _ := healthServer(t, http.StatusOK, `{"status":"maintenance","since":"2026-10-01","detail":{"a":1}}`, http.StatusServiceUnavailable)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthMaintenance {
		t.Errorf("maintenance with extra fields = %q, want %q", got, HealthMaintenance)
	}
}

func TestProbeHealth_APIHealthReadyReadsAsResponding(t *testing.T) {
	// The home page is deliberately 503 here: the answer must come from
	// /api/health alone.
	srv, c := healthServer(t, http.StatusOK, `{"status":"ready"}`, http.StatusServiceUnavailable)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthResponding {
		t.Errorf("status ready = %q, want %q", got, HealthResponding)
	}
	if n := c.n("/"); n != 0 {
		t.Errorf("/ requested %d times, want 0", n)
	}
}

func TestProbeHealth_APIHealthAnyOtherStatusWordIsResponding(t *testing.T) {
	for _, word := range []string{"ok", "starting", "degraded", "up"} {
		srv, c := healthServer(t, http.StatusOK, `{"status":"`+word+`"}`, http.StatusServiceUnavailable)
		if got := ProbeHealth(context.Background(), srv.URL); got != HealthResponding {
			t.Errorf("status %q = %q, want %q", word, got, HealthResponding)
		}
		if n := c.n("/"); n != 0 {
			t.Errorf("status %q: / requested %d times, want 0", word, n)
		}
	}
}

func TestProbeHealth_APIHealth2xxOtherThan200Counts(t *testing.T) {
	srv, c := healthServer(t, http.StatusAccepted, `{"status":"maintenance"}`, http.StatusOK)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthMaintenance {
		t.Errorf("202 maintenance = %q, want %q", got, HealthMaintenance)
	}
	if n := c.n("/"); n != 0 {
		t.Errorf("/ requested %d times, want 0", n)
	}
}

func TestProbeHealth_APIHealth5xxIsServerError(t *testing.T) {
	for _, code := range []int{500, 502, 503, 504} {
		// The home page says 200 to prove it is not consulted.
		srv, c := healthServer(t, code, `{"status":"ready"}`, http.StatusOK)
		if got := ProbeHealth(context.Background(), srv.URL); got != HealthServerError {
			t.Errorf("/api/health %d = %q, want %q", code, got, HealthServerError)
		}
		if n := c.n("/"); n != 0 {
			t.Errorf("/api/health %d: / requested %d times, want 0", code, n)
		}
	}
}

func TestProbeHealth_APIHealth404FallsBackToTheAppURL(t *testing.T) {
	// An older Orbit has no /api/health.
	srv, c := healthServer(t, http.StatusNotFound, `not found`, http.StatusOK)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthResponding {
		t.Errorf("404 then 200 = %q, want %q", got, HealthResponding)
	}
	if n := c.n("/"); n != 1 {
		t.Errorf("/ requested %d times, want 1 (fallback)", n)
	}

	srv, c = healthServer(t, http.StatusNotFound, `not found`, http.StatusServiceUnavailable)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthServerError {
		t.Errorf("404 then 503 = %q, want %q", got, HealthServerError)
	}
	if n := c.n("/"); n != 1 {
		t.Errorf("/ requested %d times, want 1 (fallback)", n)
	}
}

func TestProbeHealth_APIHealth4xxFallsBack(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusTeapot} {
		// A 4xx body that looks like maintenance must not count.
		srv, c := healthServer(t, code, `{"status":"maintenance"}`, http.StatusOK)
		if got := ProbeHealth(context.Background(), srv.URL); got != HealthResponding {
			t.Errorf("/api/health %d then 200 = %q, want %q", code, got, HealthResponding)
		}
		if n := c.n("/"); n != 1 {
			t.Errorf("/api/health %d: / requested %d times, want 1", code, n)
		}
	}
}

func TestProbeHealth_APIHealthNonJSONBodyFallsBack(t *testing.T) {
	srv, c := healthServer(t, http.StatusOK, `<html>welcome</html>`, http.StatusServiceUnavailable)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthServerError {
		t.Errorf("non-JSON 200 then 503 = %q, want %q", got, HealthServerError)
	}
	if n := c.n("/"); n != 1 {
		t.Errorf("/ requested %d times, want 1", n)
	}

	srv, _ = healthServer(t, http.StatusOK, `<html>welcome</html>`, http.StatusOK)
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthResponding {
		t.Errorf("non-JSON 200 then 200 = %q, want %q", got, HealthResponding)
	}
}

func TestProbeHealth_APIHealthJSONWithoutStatusFallsBack(t *testing.T) {
	for name, body := range map[string]string{
		"no status field":   `{"version":"1.2.3"}`,
		"empty status":      `{"status":""}`,
		"null status":       `{"status":null}`,
		"empty object":      `{}`,
		"empty body":        ``,
		"array not object":  `["maintenance"]`,
		"status wrong type": `{"status":5}`,
	} {
		srv, c := healthServer(t, http.StatusOK, body, http.StatusServiceUnavailable)
		if got := ProbeHealth(context.Background(), srv.URL); got != HealthServerError {
			t.Errorf("%s: = %q, want %q (fallback sees the 503)", name, got, HealthServerError)
		}
		if n := c.n("/"); n != 1 {
			t.Errorf("%s: / requested %d times, want 1", name, n)
		}
	}
}

func TestProbeHealth_APIHealthRedirectIsNotFollowed(t *testing.T) {
	c := &pathCounter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.record(r)
		switch r.URL.Path {
		case "/api/health":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		case "/elsewhere":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"maintenance"}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	got := ProbeHealth(context.Background(), srv.URL)
	if n := c.n("/elsewhere"); n != 0 {
		t.Errorf("redirect target requested %d times, want 0 (redirects are not followed)", n)
	}
	if got != HealthResponding {
		t.Errorf("302 from /api/health then 200 on / = %q, want %q", got, HealthResponding)
	}
	if n := c.n("/"); n != 1 {
		t.Errorf("/ requested %d times, want 1 (fallback)", n)
	}
}

func TestProbeHealth_APIHealthJoinsTheAppURLPath(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		want   string
	}{
		{"", "/api/health"},
		{"/", "/api/health"},
		{"/orbit", "/orbit/api/health"},
		{"/orbit/", "/orbit/api/health"},
		{"/a/b", "/a/b/api/health"},
	} {
		c := &pathCounter{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.record(r)
			if r.URL.Path == tc.want {
				_, _ = w.Write([]byte(`{"status":"maintenance"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		got := ProbeHealth(context.Background(), srv.URL+tc.suffix)
		srv.Close()
		if c.n(tc.want) != 1 {
			t.Errorf("APP_URL suffix %q: %s requested %d times, want 1", tc.suffix, tc.want, c.n(tc.want))
		}
		if got != HealthMaintenance {
			t.Errorf("APP_URL suffix %q = %q, want %q", tc.suffix, got, HealthMaintenance)
		}
	}
}

func TestProbeHealth_APIHealthSendsNoCredentials(t *testing.T) {
	srv, c := healthServer(t, http.StatusOK, `{"status":"ready"}`, http.StatusOK)
	withCreds := strings.Replace(srv.URL, "http://", "http://admin:s3cret@", 1)

	if got := ProbeHealth(context.Background(), withCreds); got != HealthResponding {
		t.Errorf("APP_URL with credentials = %q, want %q", got, HealthResponding)
	}
	if n := c.n("/api/health"); n != 1 {
		t.Fatalf("/api/health requested %d times, want 1", n)
	}
	if a := c.authFor("/api/health"); a != "" {
		t.Errorf("/api/health received an Authorization header %q, want none", a)
	}
}

func TestProbeHealth_APIHealthFallbackAlsoSendsNoCredentials(t *testing.T) {
	srv, c := healthServer(t, http.StatusNotFound, `nope`, http.StatusOK)
	withCreds := strings.Replace(srv.URL, "http://", "http://admin:s3cret@", 1)

	_ = ProbeHealth(context.Background(), withCreds)
	if a := c.authFor("/"); a != "" {
		t.Errorf("fallback request received an Authorization header %q, want none", a)
	}
	if a := c.authFor("/api/health"); a != "" {
		t.Errorf("/api/health received an Authorization header %q, want none", a)
	}
}

func TestProbeHealth_APIHealthHangGivesNotRespondingWithoutASecondRequest(t *testing.T) {
	release := make(chan struct{})
	c := &pathCounter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.record(r)
		if r.URL.Path == "/api/health" {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	done := make(chan HealthReason, 1)
	go func() { done <- ProbeHealth(context.Background(), srv.URL) }()

	select {
	case got := <-done:
		if got != HealthNotResponding {
			t.Errorf("hanging /api/health = %q, want %q", got, HealthNotResponding)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProbeHealth still waiting after 5s: the single time limit (2s) was not kept")
	}
	if n := c.n("/"); n != 0 {
		t.Errorf("/ requested %d times after /api/health timed out, want 0", n)
	}
}

func TestProbeHealth_APIHealthTransportErrorGivesTheUsualReason(t *testing.T) {
	// A closed port: the same reason today's probe gives, and no fallback
	// (there is nobody to ask).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	if got := ProbeHealth(context.Background(), url); got != HealthNotResponding {
		t.Errorf("connection refused = %q, want %q", got, HealthNotResponding)
	}
}

func TestProbeHealth_APIHealthStaysInsideOneTimeLimitOnSlowFallback(t *testing.T) {
	// /api/health answers 404 at once; the home page hangs. The whole
	// probe is still bounded by the one limit.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	done := make(chan HealthReason, 1)
	start := time.Now()
	go func() { done <- ProbeHealth(context.Background(), srv.URL) }()

	select {
	case got := <-done:
		if got != HealthNotResponding {
			t.Errorf("hanging fallback = %q, want %q", got, HealthNotResponding)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("probe took %v, want within the single 2s limit", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProbeHealth still waiting after 5s")
	}
}

func TestProbeHealth_NonHTTPStaysUncheckedWithMaintenanceSupport(t *testing.T) {
	for _, u := range []string{"file:///etc/hostname", "ftp://192.0.2.10/", "ssh://192.0.2.10/"} {
		if got := ProbeHealth(context.Background(), u); got != HealthUnchecked {
			t.Errorf("ProbeHealth(%q) = %q, want %q", u, got, HealthUnchecked)
		}
	}
}

func TestProbeHealth_APIHealthKeepsExistingReasonStrings(t *testing.T) {
	for reason, want := range map[HealthReason]string{
		HealthResponding:          "",
		HealthNotResponding:       "not responding",
		HealthAddressNotFound:     "address not found",
		HealthServerError:         "server error",
		HealthCertificateRejected: "certificate rejected",
		HealthUnchecked:           "could not check",
	} {
		if string(reason) != want {
			t.Errorf("reason %q, want %q", string(reason), want)
		}
	}
}
