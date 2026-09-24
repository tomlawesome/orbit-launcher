package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// manifestURL points at the newest *stable* Orbit release's
// orbit-release-manifest.json asset. Since #171 the launcher no longer
// publishes its own GitHub releases — it ships inside Orbit's stable
// releases instead (ADR-0031) — so "is a newer launcher available" is
// answered by reading the "launcher" field of Orbit's manifest, not by
// asking for a launcher release directly. The manifest's schema is
// written by orbit's scripts/ci/write-release-manifest.sh.
const manifestURL = "https://github.com/tomlawesome/orbit/releases/latest/download/orbit-release-manifest.json"

// maxUpdateCheckResponseBytes bounds the response body. The manifest is
// untrusted input: it is only ever parsed for a couple of string fields
// and nothing it names is downloaded or executed as part of this check,
// but the read is still capped defensively against a huge or malformed
// response.
const maxUpdateCheckResponseBytes = 1 << 16

// launcherTagPattern is the strict shape the manifest's launcher.tag must
// take. write-release-manifest.sh writes it straight from
// launcher/pin.json's "tag", itself constrained to a plain vMAJOR.MINOR.PATCH
// tag with no prerelease suffix — anything else means either a manifest
// this launcher doesn't understand, or an untrusted response worth
// rejecting outright, never a version to compare against.
var launcherTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// CheckForUpdate asks Orbit's latest stable release manifest for the
// launcher tag it was built against and reports it against the running
// binary's Version. hasUpdate is only ever true when both versions parsed
// as semver and the manifest's is strictly newer — an unparseable running
// Version (e.g. "dev", or a preview build's "-preview.<sha>" suffix
// already stripped before comparison) never produces a false positive.
//
// The launcher's own version and Orbit's own version are different
// numbers living in the same manifest; this only ever compares against
// the manifest's "launcher.tag", never its top-level "version".
//
// This never downloads or runs anything the manifest names — it is a
// notice only. A 404 (no stable Orbit release has published a manifest
// yet), an oversized or malformed body, or a launcher.tag that doesn't
// match launcherTagPattern are all treated the same as "nothing to
// report" — hasUpdate=false — same as when the binary is already
// current.
func CheckForUpdate(ctx context.Context) (latestVersion string, hasUpdate bool, err error) {
	return checkForUpdate(ctx, manifestURL, Version)
}

func checkForUpdate(ctx context.Context, url, runningVersion string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("check for update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("check for update: unexpected status %s", resp.Status)
	}

	var manifest struct {
		Launcher struct {
			Tag string `json:"tag"`
		} `json:"launcher"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpdateCheckResponseBytes)).Decode(&manifest); err != nil {
		return "", false, fmt.Errorf("decode release manifest: %w", err)
	}

	tag := manifest.Launcher.Tag
	if !launcherTagPattern.MatchString(tag) {
		return "", false, fmt.Errorf("check for update: manifest launcher.tag %q is not a plain vX.Y.Z tag", tag)
	}

	latest, ok := parseSemver(tag)
	if !ok {
		return "", false, fmt.Errorf("check for update: could not parse launcher tag %q", tag)
	}
	current, ok := parseSemver(runningVersion)
	if !ok {
		// A dev or otherwise unparseable running version has nothing
		// reliable to compare against — report the tag but never claim
		// an update is available.
		return tag, false, nil
	}

	return tag, current.less(latest), nil
}

type semver struct{ major, minor, patch int }

func (a semver) less(b semver) bool {
	if a.major != b.major {
		return a.major < b.major
	}
	if a.minor != b.minor {
		return a.minor < b.minor
	}
	return a.patch < b.patch
}

// parseSemver parses a "vMAJOR.MINOR.PATCH" or "MAJOR.MINOR.PATCH" tag,
// ignoring any "-preview.<sha>"-style prerelease suffix.
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	var err error
	if v.major, err = strconv.Atoi(parts[0]); err != nil {
		return semver{}, false
	}
	if v.minor, err = strconv.Atoi(parts[1]); err != nil {
		return semver{}, false
	}
	if v.patch, err = strconv.Atoi(parts[2]); err != nil {
		return semver{}, false
	}
	return v, true
}
