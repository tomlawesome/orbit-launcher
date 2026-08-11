package deploy

import (
	"context"
	"net/http"
)

// ProbeHealth reports whether the deployment at appURL is responding: a
// single GET against the app's own URL, healthy iff it answers with any
// non-server-error status. This is deliberately coarse — a reachability
// check for the splash's alive/degraded state, not a per-service health
// sweep; the caller supplies the timeout via ctx and any transport error
// (refused, DNS, TLS, timeout) simply reads as degraded.
func ProbeHealth(ctx context.Context, appURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}
