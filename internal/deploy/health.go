package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	// HealthUnexpectedAnswer is an answer at the health address that is
	// not Orbit's: another service on the port (#341).
	HealthUnexpectedAnswer HealthReason = "unexpected answer"
	// HealthMaintenance is a deployment Orbit itself reports as closed to
	// users for maintenance: up and healthy, not degraded (#302).
	HealthMaintenance HealthReason = "closed to users"
)

// LocalHealthURL is the address the deployment answers its health check
// on from this machine: its published port, not the public APP_URL, which
// a server often cannot reach itself (no NAT loopback, #341). The address
// is ORBIT_BIND_ADDRESS, else the .env-orbit value, else 0.0.0.0, and the
// port ORBIT_PORT the same way, default 3000; the process environment wins
// over the file as it does for Compose, and an empty value counts as
// unset. A wildcard address (0.0.0.0, ::) is reached through loopback,
// the rule Orbit's own scripts apply. The address must be an IP literal
// and the port 1-65535, else an error. Always plain http: TLS belongs to
// the reverse proxy, outside this check.
func LocalHealthURL(d *Deployment) (string, error) {
	if d == nil {
		return "", errors.New("no deployment")
	}
	addr := firstSet(os.Getenv("ORBIT_BIND_ADDRESS"), d.BindAddress, "0.0.0.0")
	port := firstSet(os.Getenv("ORBIT_PORT"), d.Port, "3000")
	addr = strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	ip := net.ParseIP(addr)
	if ip == nil {
		return "", fmt.Errorf("ORBIT_BIND_ADDRESS %q is not an IP address", addr)
	}
	if ip.IsUnspecified() {
		if ip.To4() != nil {
			ip = net.IPv4(127, 0, 0, 1)
		} else {
			ip = net.IPv6loopback
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("ORBIT_PORT %q is not a port number", port)
	}
	return "http://" + net.JoinHostPort(ip.String(), port) + "/api/health", nil
}

// firstSet is the first non-empty value.
func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ProbeHealth is the application-level half of the splash's composite
// check (#341); Docker's view of the containers is the other. It asks
// Orbit at healthURL, the deployment's own health
// address (LocalHealthURL), whether it is responding, from that one
// answer alone: no path is added and there is no page fallback (#341).
// A 2xx whose JSON status is ready is responding, maintenance is
// HealthMaintenance (#302), degraded or any 5xx a server error; any other
// answer (a redirect, a 4xx, a body that is not Orbit's JSON) is
// HealthUnexpectedAnswer, so a stranger on the port is not called alive.
// A transport failure is named by its cause or reads as not responding,
// and is not retried. The request gives up after healthProbeTimeout, or
// sooner if ctx ends.
func ProbeHealth(ctx context.Context, healthURL string) HealthReason {
	// The URL is not trusted to be ours, so only a web address is ever
	// asked; any other scheme is not checked.
	u, err := url.Parse(healthURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return HealthUnchecked
	}
	// Go's client turns a user:password@ in the address into Basic auth;
	// the probe never needs credentials, so none are sent (#277 W1).
	u.User = nil
	ctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
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
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return HealthUnexpectedAnswer
	}
	var body struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) != nil {
		return HealthUnexpectedAnswer
	}
	switch body.Status {
	case "ready":
		return HealthResponding
	case "maintenance":
		return HealthMaintenance
	case "degraded":
		return HealthServerError
	}
	// Orbit sends only the three words above; anything else (an empty
	// status, or another service's "ok" on a stale ORBIT_PORT) is not
	// Orbit's answer, so a stranger on the port is not called alive.
	return HealthUnexpectedAnswer
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
