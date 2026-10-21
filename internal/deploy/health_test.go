package deploy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestProbeHealth_RespondingOnAnyNonServerErrorStatus(t *testing.T) {
	for _, code := range []int{200, 302, 401, 404} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		if got := ProbeHealth(context.Background(), srv.URL); got != HealthResponding {
			t.Errorf("status %d = %q, want responding (the app answered)", code, got)
		}
		srv.Close()
	}
}

func TestProbeHealth_ServerErrorOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthServerError {
		t.Errorf("502 = %q, want %q", got, HealthServerError)
	}
}

func TestProbeHealth_NotRespondingWhenRefused(t *testing.T) {
	if got := ProbeHealth(context.Background(), "http://127.0.0.1:1"); got != HealthNotResponding {
		t.Errorf("connection refused = %q, want %q", got, HealthNotResponding)
	}
}

func TestProbeHealth_AddressNotFoundOnDNSFailure(t *testing.T) {
	if got := ProbeHealth(context.Background(), "http://orbit.invalid/"); got != HealthAddressNotFound {
		t.Errorf("unresolvable host = %q, want %q", got, HealthAddressNotFound)
	}
}

func TestProbeHealth_CertificateRejectedOnUntrustedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if got := ProbeHealth(context.Background(), srv.URL); got != HealthCertificateRejected {
		t.Errorf("untrusted certificate = %q, want %q", got, HealthCertificateRejected)
	}
}

func TestProbeHealth_NotRespondingOnContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := ProbeHealth(ctx, srv.URL); got != HealthNotResponding {
		t.Errorf("timed-out probe = %q, want %q", got, HealthNotResponding)
	}
	if time.Since(start) > time.Second {
		t.Error("probe did not respect the context deadline")
	}
}

func TestProbeHealth_UncheckedOnMalformedURL(t *testing.T) {
	if got := ProbeHealth(context.Background(), "http://bad\x7fhost/"); got != HealthUnchecked {
		t.Errorf("an unusable APP_URL = %q, want %q", got, HealthUnchecked)
	}
}

func TestTransportReason_DNSFlagsDecide(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want HealthReason
	}{
		{"not found", &net.DNSError{Err: "no such host", Name: "orbit.invalid", IsNotFound: true}, HealthAddressNotFound},
		{"network unreachable", &net.DNSError{Err: "dial udp 192.0.2.53:53: connect: network is unreachable", Name: "orbit.example", IsTemporary: true}, HealthNotResponding},
		{"timeout", &net.DNSError{Err: "i/o timeout", Name: "orbit.example", IsTimeout: true, IsTemporary: true}, HealthNotResponding},
		{"server misbehaving", &net.DNSError{Err: "server misbehaving", Name: "orbit.example", IsTemporary: true}, HealthNotResponding},
		{"no flags", &net.DNSError{Err: "lookup: cannot unmarshal DNS message", Name: "orbit.example"}, HealthNotResponding},
		{"connection refused", errors.New("connection refused"), HealthNotResponding},
	}
	for _, c := range cases {
		if got := transportReason(c.err); got != c.want {
			t.Errorf("%s (bare): got %q, want %q", c.name, got, c.want)
		}
		wrapped := &url.Error{Op: "Get", URL: "https://orbit.example/", Err: c.err}
		if got := transportReason(wrapped); got != c.want {
			t.Errorf("%s (wrapped): got %q, want %q", c.name, got, c.want)
		}
	}
}
