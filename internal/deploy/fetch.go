package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// installScriptURL points at orbit's stable line, not a moving
// development branch — matching the guidance install.sh itself prints
// for non-interactive re-runs. install.sh resolves everything else it
// needs (compose files) from the exact revision recorded in the Docker
// image it pulls, so fetching only this one file, fresh, at the moment
// of commit, is sufficient — see docs/implementation-plan.md section 5
// Wave 2 for why this isn't vendored.
const installScriptURL = "https://raw.githubusercontent.com/tomlawesome/orbit/main/scripts/install.sh"

// maxInstallScriptBytes bounds the download — install.sh is a few tens of
// KB; anything wildly larger signals something is wrong rather than a
// script to run.
const maxInstallScriptBytes = 1 << 20 // 1 MiB

// scriptFetchTimeout bounds every script download. The UI starts these
// fetches with a background context and shows only the console clock
// while it waits, so on a network that accepts the connection and never
// answers the launcher sat there indefinitely (#147). install.sh is a few
// tens of KB from a CDN: thirty seconds is generous, and past it the
// person is better served by a failed screen that says so.
const scriptFetchTimeout = 30 * time.Second

// scriptFetchClient is the one client every script fetch goes through, so
// the deadline applies to install.sh, repair.sh and the configure tree
// alike. http.DefaultClient has no timeout at all.
var scriptFetchClient = &http.Client{Timeout: scriptFetchTimeout}

// FetchInstallScript downloads the current install.sh from orbit's stable
// branch. It never touches disk or executes anything — see Install for
// that — and is deliberately re-fetched every time rather than cached, so
// a person always installs against the current, real script.
//
// ORBIT_LAUNCHER_INSTALL_SCRIPT_URL overrides the source URL. It exists
// only so CI can run the real install flow against orbit's develop/preview
// branches to catch drift before it reaches main; a real install never has
// this set, so it always runs the same stable script a person would get.
func FetchInstallScript(ctx context.Context) ([]byte, error) {
	url := installScriptURL
	if override := os.Getenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL"); override != "" {
		url = override
	}
	return fetchInstallScript(ctx, url)
}

// fetchError words a transport failure for the failed screen. A deadline
// hit is the one case a person can act on without reading the wrapped
// error, so it gets its own sentence.
func fetchError(what string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || isClientTimeout(err) {
		return fmt.Errorf("fetch %s: the server did not answer within %s — check the network and try again", what, scriptFetchTimeout)
	}
	return fmt.Errorf("fetch %s: %w", what, err)
}

// isClientTimeout recognises http.Client's own Timeout expiring, which it
// reports as a url.Error with Timeout() true rather than as
// context.DeadlineExceeded.
func isClientTimeout(err error) bool {
	var uerr *url.Error
	return errors.As(err, &uerr) && uerr.Timeout()
}

func fetchInstallScript(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := scriptFetchClient.Do(req)
	if err != nil {
		return nil, fetchError("install.sh", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch install.sh: unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInstallScriptBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read install.sh: %w", err)
	}
	if len(body) > maxInstallScriptBytes {
		return nil, fmt.Errorf("install.sh is larger than the %d byte limit; refusing to run an unexpectedly large script", maxInstallScriptBytes)
	}
	if !bytes.HasPrefix(body, []byte("#!")) {
		return nil, fmt.Errorf("fetched content does not look like a script (no shebang) — refusing to run it")
	}

	return body, nil
}
