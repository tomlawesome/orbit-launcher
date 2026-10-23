package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
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
)

// ProbeHealth asks the deployment at appURL whether it is responding: a
// single GET against the app's own URL. A 2xx to 4xx answer is
// responding, a 5xx is a server error, and a transport failure is named
// by its cause (a name that does not exist, certificate) or reads as not responding. This is
// deliberately coarse, a reachability check for the splash's
// alive/degraded state, not a per-service sweep. It gives up after
// healthProbeTimeout, or sooner if ctx ends.
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
