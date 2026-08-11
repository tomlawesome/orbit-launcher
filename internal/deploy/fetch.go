package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
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

// FetchInstallScript downloads the current install.sh from orbit's stable
// branch. It never touches disk or executes anything — see Install for
// that — and is deliberately re-fetched every time rather than cached, so
// a person always installs against the current, real script.
func FetchInstallScript(ctx context.Context) ([]byte, error) {
	return fetchInstallScript(ctx, installScriptURL)
}

func fetchInstallScript(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch install.sh: %w", err)
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
