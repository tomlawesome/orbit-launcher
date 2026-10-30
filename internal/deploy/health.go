package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// healthProbeTimeout bounds the splash's health probe. The probe is a
// background check whose answer only colours a status line, so a
// deployment that has not answered in two seconds reads as degraded
// rather than keeping a request open (#207: the limit lives beside the
// call, not in the UI).
const healthProbeTimeout = 2 * time.Second

// healthClient is the client the probe goes through; http.DefaultClient
// has no timeout at all. It never follows a redirect (#277 W1): a
// redirect would contact an address .env-orbit never named, and the 3xx
// answer already shows the deployment is responding.
var healthClient = &http.Client{
	Timeout: healthProbeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// HealthReason says why a deployment reads as degraded; the empty
// reason means it answered (#254).
type HealthReason string

// The reasons the splash's degraded drawer can show.
const (
	HealthResponding          HealthReason = ""
	HealthNotResponding       HealthReason = "not responding"
	HealthAddressNotFound     HealthReason = "address not found"
	HealthServerError         HealthReason = "server error"
	HealthCertificateRejected HealthReason = "certificate rejected"
	HealthUnchecked           HealthReason = "could not check"
	// HealthMaintenance is a deployment Orbit itself reports as closed to
	// users for maintenance: up and healthy, not degraded (#302).
	HealthMaintenance HealthReason = "closed to users"
)

// ProbeHealth asks the deployment at appURL whether it is responding.
// It first reads Orbit's own health answer, <APP_URL>/api/health, which
// tells a deployment closed for maintenance (HealthMaintenance) from a
// failing one (#302); Orbit answers its pages with 503 in maintenance, so
// the page alone would read as a server error. An Orbit without that
// answer (a 3xx or 4xx, or no status in it) is asked the old way: a GET
// against the app's own URL, where a 2xx to 4xx answer is responding and
// a 5xx a server error. A transport failure is named by its cause (a name
// that does not exist, certificate) or reads as not responding, and is
// not retried. This is deliberately coarse, a reachability check for the
// splash's state, not a per-service sweep. Both requests together give up
// after healthProbeTimeout, or sooner if ctx ends.
func ProbeHealth(ctx context.Context, appURL string) HealthReason {
	// APP_URL comes from a file the launcher may not have written, so
	// only a web address is ever asked; any other scheme is not checked.
	u, err := url.Parse(appURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return HealthUnchecked
	}
	// Go's client turns a user:password@ in the address into Basic auth;
	// the probe never needs credentials (a 401 already counts as
	// responding), so none are sent (#277 W1).
	u.User = nil
	ctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	if reason, decided := probeAPIHealth(ctx, *u); decided {
		return reason
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return HealthUnchecked
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return transportReason(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return HealthServerError
	}
	return HealthResponding
}

// probeAPIHealth reads Orbit's health answer under the app's URL. It
// decides when Orbit gave one (a 2xx naming a status, or a 5xx) or the
// request failed outright; anything else (a redirect, a 4xx from an Orbit
// without the endpoint, a body that is not its JSON) leaves the decision
// to the page probe.
func probeAPIHealth(ctx context.Context, u url.URL) (HealthReason, bool) {
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/health"
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return transportReason(err), true
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 500:
		return HealthServerError, true
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return "", false
	}
	var body struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) != nil || body.Status == "" {
		return "", false
	}
	if body.Status == "maintenance" {
		return HealthMaintenance, true
	}
	return HealthResponding, true
}

// transportReason names a failed request's cause.
func transportReason(err error) HealthReason {
	var dnsErr *net.DNSError
	var certVerify *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	// Only a resolver's not-found answer means the name does not exist; an
	// unreachable resolver, a timeout or SERVFAIL is also a DNSError, and
	// offline must not send the person to check their DNS record.
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return HealthAddressNotFound
	case errors.As(err, &certVerify), errors.As(err, &unknownAuth),
		errors.As(err, &hostname), errors.As(err, &invalid):
		return HealthCertificateRejected
	}
	return HealthNotResponding
}
