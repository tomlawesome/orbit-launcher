package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// latestReleaseURL asks GitHub for the newest *stable* release — a
// pre-release like preview-latest never satisfies this endpoint, so a
// person running a preview build is never told "update available"
// against another preview.
const latestReleaseURL = "https://api.github.com/repos/tomlawesome/orbit-launcher/releases/latest"

// maxUpdateCheckResponseBytes bounds the response body; the release
// metadata GitHub returns is a few KB at most.
const maxUpdateCheckResponseBytes = 1 << 16

// CheckForUpdate asks GitHub for the newest stable release tag and
// reports it against the running binary's Version. hasUpdate is only
// ever true when both versions parsed as semver and the latest is
// strictly newer — an unparseable running Version (e.g. "dev", or a
// preview build's "-preview.<sha>" suffix already stripped before
// comparison) never produces a false positive.
//
// A 404 (no stable release published yet) is not an error — there is
// simply nothing to report — and returns hasUpdate=false with a nil
// error, same as when the binary is already current.
func CheckForUpdate(ctx context.Context) (latestVersion string, hasUpdate bool, err error) {
	return checkForUpdate(ctx, latestReleaseURL, Version)
}

func checkForUpdate(ctx context.Context, url, runningVersion string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

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

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpdateCheckResponseBytes)).Decode(&body); err != nil {
		return "", false, fmt.Errorf("decode release metadata: %w", err)
	}

	latest, ok := parseSemver(body.TagName)
	if !ok {
		return "", false, fmt.Errorf("check for update: could not parse release tag %q", body.TagName)
	}
	current, ok := parseSemver(runningVersion)
	if !ok {
		// A dev or otherwise unparseable running version has nothing
		// reliable to compare against — report the tag but never claim
		// an update is available.
		return body.TagName, false, nil
	}

	return body.TagName, current.less(latest), nil
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
