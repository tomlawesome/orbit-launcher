package deploy

// Pins #314 (check the OIDC issuer against the provider's discovery
// document in the launcher). Written from the issue and its design note
// (2026-10-30) before the code: the provider is a local TLS server, and
// every answer names a made-up host the test client dials to it, so the
// tests never depend on how loopback addresses are judged.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testIssuerHost = "id.example.test"
	testIssuer     = "https://" + testIssuerHost + "/realms/orbit"
	discoveryPath  = "/realms/orbit/.well-known/openid-configuration"
)

// fakeProvider is a TLS discovery server that records the paths it was
// asked for.
type fakeProvider struct {
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
}

func newFakeProvider(t *testing.T, h http.HandlerFunc) *fakeProvider {
	t.Helper()
	p := &fakeProvider{}
	p.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.paths = append(p.paths, r.URL.Path)
		p.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeProvider) requested() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

// client trusts the server's certificate and dials it whatever host a
// URL names (the certificate is checked as example.com's, which it is).
func (p *fakeProvider) client() *http.Client {
	return dialingClient(p.srv.Listener.Addr().String(), true, p.srv)
}

func dialingClient(addr string, trust bool, srv *httptest.Server) *http.Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{ServerName: "example.com"},
	}
	if trust && srv != nil {
		tr.TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	}
	return &http.Client{Transport: tr}
}

// serveIssuer answers the discovery path with a document naming issuer.
func serveIssuer(issuer any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != discoveryPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": "https://" + testIssuerHost + "/certs"})
	}
}

func serveBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func checkWithin(t *testing.T, client *http.Client, answer string, limit time.Duration) IssuerCheck {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return CheckOidcIssuer(ctx, client, answer)
}

func requireUnchecked(t *testing.T, got IssuerCheck, reason string) {
	t.Helper()
	if got.Verdict != IssuerUnchecked || string(got.Reason) != reason {
		t.Fatalf("check = %+v, want unchecked with reason %q", got, reason)
	}
}

func TestIssuerLimits_AreOrbitsOwn(t *testing.T) {
	// Fault: limits drifting from Orbit's verifyOidcDiscovery (10 s,
	// 1 MiB) or the echo bound in the issue (2,048 bytes).
	if IssuerDiscoveryTimeout != 10*time.Second || IssuerDiscoveryMaxBytes != 1<<20 || IssuerEchoMaxBytes != 2048 {
		t.Fatalf("limits = %v, %d, %d; want 10s, 1 MiB, 2048", IssuerDiscoveryTimeout, IssuerDiscoveryMaxBytes, IssuerEchoMaxBytes)
	}
}

// Fault: an exact match reported as anything else, or the document
// fetched from a path other than <issuer>/.well-known/openid-configuration.
func TestCheckOidcIssuer_ExactMatchPasses(t *testing.T) {
	p := newFakeProvider(t, serveIssuer(testIssuer))
	got := checkWithin(t, p.client(), testIssuer, 5*time.Second)
	if got.Verdict != IssuerMatches || got.Withheld || got.Reason != "" {
		t.Fatalf("check = %+v, want a match", got)
	}
	if paths := p.requested(); len(paths) != 1 || paths[0] != discoveryPath {
		t.Fatalf("requested %q, want once %q", paths, discoveryPath)
	}
}

// Fault: a missing trailing slash reported as a different issuer, or the
// published value not offered.
func TestCheckOidcIssuer_MissingTrailingSlashIsOffered(t *testing.T) {
	p := newFakeProvider(t, serveIssuer(testIssuer+"/"))
	got := checkWithin(t, p.client(), testIssuer, 5*time.Second)
	if got.Verdict != IssuerSlashDiffers || got.Published != testIssuer+"/" || got.Withheld {
		t.Fatalf("check = %+v, want the slash case offering %q", got, testIssuer+"/")
	}
}

// Fault: an extra trailing slash missed, or the discovery URL built with
// two slashes when the answer ends in one.
func TestCheckOidcIssuer_ExtraTrailingSlashIsOffered(t *testing.T) {
	p := newFakeProvider(t, serveIssuer(testIssuer))
	got := checkWithin(t, p.client(), testIssuer+"/", 5*time.Second)
	if got.Verdict != IssuerSlashDiffers || got.Published != testIssuer || got.Withheld {
		t.Fatalf("check = %+v, want the slash case offering %q", got, testIssuer)
	}
	if paths := p.requested(); len(paths) != 1 || paths[0] != discoveryPath {
		t.Fatalf("requested %q, want once %q", paths, discoveryPath)
	}
}

// Fault: any other difference passed as a match or as the slash case.
func TestCheckOidcIssuer_OtherMismatchIsReported(t *testing.T) {
	other := "https://login.example.test/realms/other"
	p := newFakeProvider(t, serveIssuer(other))
	got := checkWithin(t, p.client(), testIssuer, 5*time.Second)
	if got.Verdict != IssuerDiffers || got.Published != other || got.Withheld {
		t.Fatalf("check = %+v, want a different issuer naming %q", got, other)
	}
	// Differs by more than one slash: still not the slash case.
	p2 := newFakeProvider(t, serveIssuer(testIssuer+"//"))
	if got := checkWithin(t, p2.client(), testIssuer, 5*time.Second); got.Verdict != IssuerDiffers {
		t.Fatalf("two extra slashes: check = %+v, want a different issuer", got)
	}
}

// Fault: a refused connection treated as a match or a mismatch.
func TestCheckOidcIssuer_UnreachableIsUnchecked(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	got := checkWithin(t, dialingClient(addr, false, nil), testIssuer, 5*time.Second)
	requireUnchecked(t, got, "the address could not be reached")
}

// Fault: a certificate the client does not trust accepted, or reported
// as unreachable. Paired with ExactMatchPasses, which trusts it.
func TestCheckOidcIssuer_UntrustedCertificateIsUnchecked(t *testing.T) {
	p := newFakeProvider(t, serveIssuer(testIssuer))
	got := checkWithin(t, dialingClient(p.srv.Listener.Addr().String(), false, nil), testIssuer, 5*time.Second)
	requireUnchecked(t, got, "certificate rejected")
}

// Fault: a provider that never answers holds the check past its limit,
// or the timeout is called something else.
func TestCheckOidcIssuer_TimeoutIsUncheckedWithinItsLimit(t *testing.T) {
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	start := time.Now()
	got := checkWithin(t, p.client(), testIssuer, 100*time.Millisecond)
	if took := time.Since(start); took >= time.Second {
		t.Fatalf("the check took %v with a 100ms limit", took)
	}
	if got.Verdict != IssuerUnchecked || !strings.HasPrefix(string(got.Reason), "no answer within ") {
		t.Fatalf("check = %+v, want unchecked with \"no answer within <limit>\"", got)
	}
	// The effective limit is the context's 100ms, not the 10s default.
	if got.Reason == "no answer within 10s" {
		t.Fatalf("reason names the default limit, not the effective one: %q", got.Reason)
	}
}

// paddedDocument is a JSON document naming testIssuer, exactly size bytes.
func paddedDocument(t *testing.T, size int) string {
	t.Helper()
	head := `{"issuer":"` + testIssuer + `","pad":"`
	tail := `"}`
	n := size - len(head) - len(tail)
	if n < 0 {
		t.Fatalf("setup: %d bytes is too small", size)
	}
	return head + strings.Repeat("x", n) + tail
}

// serveChunked writes body without a Content-Length, so only reading
// it can find its size.
func serveChunked(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body[:64]))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(body[64:]))
	}
}

// Fault: no byte cap on the body, or one declared size trusted over the
// cap. Paired: a document of exactly the cap is read and matched.
func TestCheckOidcIssuer_OversizedBodyIsUnchecked(t *testing.T) {
	t.Run("a body one byte over the cap", func(t *testing.T) {
		p := newFakeProvider(t, serveChunked(paddedDocument(t, 1<<20+1)))
		requireUnchecked(t, checkWithin(t, p.client(), testIssuer, 5*time.Second), "the document is larger than 1 MiB")
	})
	t.Run("a body of exactly the cap is read", func(t *testing.T) {
		p := newFakeProvider(t, serveChunked(paddedDocument(t, 1<<20)))
		if got := checkWithin(t, p.client(), testIssuer, 5*time.Second); got.Verdict != IssuerMatches {
			t.Fatalf("check = %+v, want a match at exactly 1 MiB", got)
		}
	})
	t.Run("a Content-Length past the cap is refused before reading", func(t *testing.T) {
		// The body never arrives: a checker that reads it waits for its
		// time limit instead of refusing on the declared size.
		p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(1<<20+1))
			_, _ = w.Write([]byte(`{"issuer":"` + testIssuer + `","pad":"`))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		})
		start := time.Now()
		got := checkWithin(t, p.client(), testIssuer, 3*time.Second)
		requireUnchecked(t, got, "the document is larger than 1 MiB")
		if took := time.Since(start); took >= 2*time.Second {
			t.Fatalf("the declared size was not refused up front: took %v", took)
		}
	})
}

// Fault: an HTML login page (or anything else not JSON) read as a
// document, or reported as a different issuer.
func TestCheckOidcIssuer_NotJSONIsUnchecked(t *testing.T) {
	p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>Sign in</title>"))
	})
	requireUnchecked(t, checkWithin(t, p.client(), testIssuer, 5*time.Second), "the answer is not a JSON document")
}

// Fault: a document without a string issuer compared as if it had an
// empty one (a "different issuer"), or a number taken for a string.
func TestCheckOidcIssuer_NoIssuerIsUnchecked(t *testing.T) {
	for _, body := range []string{`{"jwks_uri":"x"}`, `{"issuer":5}`} {
		t.Run(body, func(t *testing.T) {
			p := newFakeProvider(t, serveBody(body))
			requireUnchecked(t, checkWithin(t, p.client(), testIssuer, 5*time.Second), "the document names no issuer")
		})
	}
}

// hopProvider redirects /hop/<n>/... to /hop/<n+1>/... over https until
// n reaches hops, then serves a document naming the answer.
func hopProvider(t *testing.T, hops int) *fakeProvider {
	t.Helper()
	return newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/hop/%d/.well-known/openid-configuration", &n); err != nil {
			http.NotFound(w, r)
			return
		}
		if n < hops {
			http.Redirect(w, r, fmt.Sprintf("https://%s/hop/%d/.well-known/openid-configuration", testIssuerHost, n+1), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": "https://" + testIssuerHost + "/hop/0"})
	})
}

// Fault: following a redirect to plain http. Paired: an https redirect
// is followed.
func TestCheckOidcIssuer_RedirectOffHTTPSIsUnchecked(t *testing.T) {
	t.Run("to http", func(t *testing.T) {
		p := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://"+testIssuerHost+discoveryPath, http.StatusFound)
		})
		requireUnchecked(t, checkWithin(t, p.client(), testIssuer, 5*time.Second), "redirected to a non-https address")
		if n := len(p.requested()); n != 1 {
			t.Fatalf("the provider saw %d requests, want only the first", n)
		}
	})
	t.Run("to https is followed", func(t *testing.T) {
		p := hopProvider(t, 1)
		if got := checkWithin(t, p.client(), "https://"+testIssuerHost+"/hop/0", 5*time.Second); got.Verdict != IssuerMatches {
			t.Fatalf("check = %+v, want a match after one https redirect", got)
		}
	})
}

// Fault: no redirect limit, or Go's default (10 requests, so only 9
// redirects) used instead of Orbit's 10 redirects. Paired: 10 hops pass.
func TestCheckOidcIssuer_TooManyRedirectsIsUnchecked(t *testing.T) {
	answer := "https://" + testIssuerHost + "/hop/0"
	t.Run("10 redirects are followed", func(t *testing.T) {
		if got := checkWithin(t, hopProvider(t, 10).client(), answer, 5*time.Second); got.Verdict != IssuerMatches {
			t.Fatalf("check = %+v, want a match after 10 https redirects", got)
		}
	})
	t.Run("the 11th is refused", func(t *testing.T) {
		got := checkWithin(t, hopProvider(t, 11).client(), answer, 5*time.Second)
		if got.Verdict != IssuerUnchecked || got.Reason == "" {
			t.Fatalf("check = %+v, want unchecked with a reason", got)
		}
	})
}

// Fault: a hostile provider's issuer handed to the screen. Paired: a
// different but showable issuer of exactly the bound is published.
func TestCheckOidcIssuer_HostileIssuerIsWithheld(t *testing.T) {
	longest := "https://" + testIssuerHost + "/" + strings.Repeat("a", 2048-len("https://"+testIssuerHost+"/"))
	for name, published := range map[string]string{
		"escape sequence":  testIssuer + "\x1b[31m",
		"2,049 bytes":      longest + "a",
		"not an https URL": "http://login.example.test/realms/orbit",
	} {
		t.Run(name, func(t *testing.T) {
			p := newFakeProvider(t, serveIssuer(published))
			got := checkWithin(t, p.client(), testIssuer, 5*time.Second)
			if got.Verdict != IssuerDiffers || got.Published != "" || !got.Withheld {
				t.Fatalf("check = %+v, want a different issuer, withheld, nothing published", got)
			}
		})
	}
	t.Run("2,048 bytes is shown", func(t *testing.T) {
		if len(longest) != 2048 {
			t.Fatalf("setup: %d bytes", len(longest))
		}
		p := newFakeProvider(t, serveIssuer(longest))
		got := checkWithin(t, p.client(), testIssuer, 5*time.Second)
		if got.Verdict != IssuerDiffers || got.Published != longest || got.Withheld {
			t.Fatalf("check = %+v, want the 2,048-byte issuer published", got)
		}
	})
}

// Fault: an http answer fetched (the shape rule skipped). Paired: a
// well-shaped https answer is fetched once.
func TestCheckOidcIssuer_NeverRequestsAnHTTPAnswer(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		serveIssuer("http://"+testIssuerHost+"/realms/orbit")(w, r)
	}))
	t.Cleanup(plain.Close)
	answer := "http://" + testIssuerHost + "/realms/orbit"
	if WellShapedIssuer(answer) {
		t.Fatalf("WellShapedIssuer(%q) = true, want false", answer)
	}
	got := checkWithin(t, dialingClient(plain.Listener.Addr().String(), false, nil), answer, 5*time.Second)
	mu.Lock()
	n := requests
	mu.Unlock()
	if n != 0 {
		t.Fatalf("an http answer was requested %d times", n)
	}
	if got.Verdict == IssuerMatches {
		t.Fatalf("check = %+v: an unchecked http answer reported as a match", got)
	}

	p := newFakeProvider(t, serveIssuer(testIssuer))
	checkWithin(t, p.client(), testIssuer, 5*time.Second)
	if n := len(p.requested()); n != 1 {
		t.Fatalf("a well-shaped answer was requested %d times, want once", n)
	}
}

// Fault: the shape rule drifting from Orbit's isValidOidcIssuer (https
// only; no credentials, query or fragment; a host).
func TestWellShapedIssuer(t *testing.T) {
	for _, s := range []string{"https://h/p", testIssuer, testIssuer + "/", "https://h"} {
		if !WellShapedIssuer(s) {
			t.Errorf("WellShapedIssuer(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"https://u@h", "https://u:p@h/p", "https://h?q", "https://h#f", "https://", "ftp://h", "https://h p", "http://h/p", ""} {
		if WellShapedIssuer(s) {
			t.Errorf("WellShapedIssuer(%q) = true, want false", s)
		}
	}
}

// Fault: control characters or over-long values allowed onto the screen.
// Paired: an ordinary issuer, and one of exactly 2,048 bytes, are shown.
func TestEchoableIssuer(t *testing.T) {
	longest := "https://" + testIssuerHost + "/" + strings.Repeat("a", 2048-len("https://"+testIssuerHost+"/"))
	for _, s := range []string{testIssuer, longest} {
		if !EchoableIssuer(s) {
			t.Errorf("EchoableIssuer(%d bytes %.40q) = false, want true", len(s), s)
		}
	}
	for name, s := range map[string]string{
		"escape":      testIssuer + "\x1b[31m",
		"bell":        testIssuer + "\x07",
		"NUL":         testIssuer + "\x00",
		"newline":     testIssuer + "\n",
		"tab":         testIssuer + "\t",
		"DEL":         testIssuer + "\x7f",
		"C1 CSI":      testIssuer + "\u009b31m",
		"2,049 bytes": longest + "a",
	} {
		if EchoableIssuer(s) {
			t.Errorf("EchoableIssuer(%s) = true, want false", name)
		}
	}
}

// Fault: two slashes, or none, between the issuer and .well-known.
func TestDiscoveryURL_JoinsWithExactlyOneSlash(t *testing.T) {
	for in, want := range map[string]string{
		"https://h/p":  "https://h/p/.well-known/openid-configuration",
		"https://h/p/": "https://h/p/.well-known/openid-configuration",
		"https://h":    "https://h/.well-known/openid-configuration",
		"https://h/":   "https://h/.well-known/openid-configuration",
	} {
		if got := DiscoveryURL(in); got != want {
			t.Errorf("DiscoveryURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Fault: the checker setting its time limit or redirect rule on the
// caller's client instead of a copy.
func TestCheckOidcIssuer_LeavesTheCallersClientAlone(t *testing.T) {
	p := newFakeProvider(t, serveIssuer(testIssuer))
	client := p.client()
	checkWithin(t, client, testIssuer, 5*time.Second)
	if client.Timeout != 0 || client.CheckRedirect != nil {
		t.Fatalf("the caller's client was changed: Timeout %v, CheckRedirect set %v", client.Timeout, client.CheckRedirect != nil)
	}
}
