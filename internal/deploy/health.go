package deploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"time"
)

// healthProbeTimeout bounds the splash's health probe. The probe is a
// background check whose answer only colours a status line, so a
// deployment that has not answered in two seconds reads as degraded
// rather than keeping a request open (#207: the limit lives beside the
// call, not in the UI).
const healthProbeTimeout = 2 * time.Second

// healthClient is the client the probe goes through; http.DefaultClient
// has no timeout at all.
var healthClient = &http.Client{Timeout: healthProbeTimeout}

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appURL, nil)
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
