package deploy

import (
	"context"
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

// ProbeHealth reports whether the deployment at appURL is responding: a
// single GET against the app's own URL, healthy iff it answers with any
// non-server-error status. This is deliberately coarse — a reachability
// check for the splash's alive/degraded state, not a per-service health
// sweep. It gives up after healthProbeTimeout, or sooner if ctx ends,
// and any transport error (refused, DNS, TLS, timeout) simply reads as
// degraded.
func ProbeHealth(ctx context.Context, appURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appURL, nil)
	if err != nil {
		return false
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}
