package release

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Manifest is what the launcher reads from an Orbit release manifest
// (the schema orbit's scripts/ci/write-release-manifest.sh writes): the
// Orbit release's version, its image digest, and the launcher tag it
// ships. Each field has passed its strict pattern; a manifest that fails
// any of them is an error, never a Manifest.
type Manifest struct {
	Version, Digest, LauncherTag string
}

// rawManifest is the manifest as decoded, before any field is checked.
type rawManifest struct {
	Version string `json:"version"`
	Image   struct {
		Digest string `json:"digest"`
	} `json:"image"`
	Launcher struct {
		Tag string `json:"tag"`
	} `json:"launcher"`
}

// versionPattern is the shape Orbit stamps a release with, and the one
// install.sh holds a manifest's version and a pinned channel to.
var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// digestPattern is an image digest as the manifest names it.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// targetLookupTimeout bounds the confirm screen's release lookup. Longer
// than the splash notice's updateCheckTimeout: here the answer changes
// what the person agrees to, and they are reading a screen meanwhile
// (#336; #207: the limit lives beside the call).
const targetLookupTimeout = 10 * time.Second

// targetLookupClient is the client the release lookup goes through.
var targetLookupClient = &http.Client{Timeout: targetLookupTimeout}

// getManifest asks url for a release manifest through client and decodes
// it, reading at most maxUpdateCheckResponseBytes. found is false, with
// no error, for a 404: no release has published one there. This is the
// one request, cap and parser every manifest read shares; the callers
// check the fields they use.
func getManifest(ctx context.Context, client *http.Client, url string) (raw rawManifest, found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return rawManifest{}, false, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return rawManifest{}, false, fmt.Errorf("fetch release manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return rawManifest{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return rawManifest{}, false, fmt.Errorf("fetch release manifest: unexpected status %s", resp.Status)
	}
	raw, err = decodeManifest(resp.Body)
	return raw, err == nil, err
}

// decodeManifest decodes a manifest from r, reading at most
// maxUpdateCheckResponseBytes of it.
func decodeManifest(r io.Reader) (rawManifest, error) {
	var raw rawManifest
	if err := json.NewDecoder(io.LimitReader(r, maxUpdateCheckResponseBytes)).Decode(&raw); err != nil {
		return rawManifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	return raw, nil
}

// checked holds every field of raw to its pattern.
func (raw rawManifest) checked() (Manifest, error) {
	switch {
	case !versionPattern.MatchString(raw.Version):
		return Manifest{}, fmt.Errorf("release manifest: version %q is not a plain vX.Y.Z release", raw.Version)
	case !digestPattern.MatchString(raw.Image.Digest):
		return Manifest{}, fmt.Errorf("release manifest: image digest %q is not a sha256 digest", raw.Image.Digest)
	case !launcherTagPattern.MatchString(raw.Launcher.Tag):
		return Manifest{}, fmt.Errorf("release manifest: launcher.tag %q is not a plain vX.Y.Z tag", raw.Launcher.Tag)
	}
	return Manifest{Version: raw.Version, Digest: raw.Image.Digest, LauncherTag: raw.Launcher.Tag}, nil
}

// fetchManifest reads the release manifest at url, giving up after
// targetLookupTimeout or sooner if ctx ends. A 404 is no error and no
// manifest (not published). It verifies no signature: what it returns is
// shown, never downloaded or run.
func fetchManifest(ctx context.Context, url string) (Manifest, error) {
	raw, found, err := getManifest(ctx, targetLookupClient, url)
	if err != nil || !found {
		return Manifest{}, err
	}
	return raw.checked()
}

// readManifestFile is fetchManifest's parser on a manifest file.
func readManifestFile(path string) (Manifest, error) {
	// The path is the person's own ORBIT_RELEASE_MANIFEST, the file
	// install.sh will read; reading it to show its version is the point.
	f, err := os.Open(filepath.Clean(path)) //nolint:gosec // G304: the person's own manifest path, read for display only
	if err != nil {
		return Manifest{}, fmt.Errorf("read release manifest: %w", err)
	}
	defer f.Close()
	raw, err := decodeManifest(f)
	if err != nil {
		return Manifest{}, err
	}
	return raw.checked()
}

// The variables install.sh picks its release by, which ResolveTarget
// reads the same way.
const (
	releaseManifestEnv  = "ORBIT_RELEASE_MANIFEST"
	channelEnv          = "ORBIT_CHANNEL"
	manifestBaseURLEnv  = "ORBIT_INSTALL_TEST_MANIFEST_BASE_URL"
	defaultManifestBase = "https://github.com/tomlawesome/orbit"
	manifestAsset       = "orbit-release-manifest.json"
)

// ErrUnresolvable is ResolveTarget's answer for a channel that names no
// release folder (preview, or anything else install.sh's self-fetch
// refuses too).
var ErrUnresolvable = errors.New("the release channel names no release to read")

// errNotPublished is ResolveTarget's answer when the release folder has
// no manifest (a 404).
var errNotPublished = errors.New("release manifest: not published")

// Target is the Orbit release an update would install. Pin is true when
// the engine run must be pinned to Version (ORBIT_CHANNEL=Version) for
// install.sh to install exactly that release; it is false when
// install.sh already reads the same manifest (a manifest file, or a
// channel already pinned).
type Target struct {
	Version string
	Pin     bool
}

// ResolveTarget names the Orbit release install.sh would install from
// this process's environment, in install.sh's own precedence (#336):
// ORBIT_RELEASE_MANIFEST's file; else ORBIT_CHANNEL unset or "latest",
// the latest release's manifest, pinned; else a vX.Y.Z channel, that
// release's manifest; else ErrUnresolvable, asking nothing. The base is
// ORBIT_INSTALL_TEST_MANIFEST_BASE_URL when set, as install.sh's is.
//
// Show the plan, then apply exactly that plan: the confirm screen names
// Version, and a pinned run makes install.sh verify and install that
// release or refuse. Any failure is an error and no version, never a
// guess. The lookup gives up after targetLookupTimeout.
func ResolveTarget(ctx context.Context) (Target, error) {
	if file := os.Getenv(releaseManifestEnv); file != "" {
		m, err := readManifestFile(file)
		if err != nil {
			return Target{}, err
		}
		return Target{Version: m.Version}, nil
	}

	base := strings.TrimRight(cmp.Or(os.Getenv(manifestBaseURLEnv), defaultManifestBase), "/")

	var url string
	pin := false
	switch channel := os.Getenv(channelEnv); {
	case channel == "" || channel == "latest":
		url, pin = base+"/releases/latest/download/"+manifestAsset, true
	case versionPattern.MatchString(channel):
		url = base + "/releases/download/" + channel + "/" + manifestAsset
	default:
		return Target{}, ErrUnresolvable
	}

	// A 404 is no error to fetchManifest but no version either: here it
	// is a failure like any other, never a guess.
	m, err := fetchManifest(ctx, url)
	if err != nil || m.Version == "" {
		return Target{}, cmp.Or(err, errNotPublished)
	}
	return Target{Version: m.Version, Pin: pin}, nil
}

// NewerThan reports whether a is a later release than b, both plain
// vX.Y.Z; with either in any other shape it claims no relation (#336:
// installed newer than the target is the one installer refusal the
// launcher knows in advance).
func NewerThan(a, b string) bool {
	if !versionPattern.MatchString(a) || !versionPattern.MatchString(b) {
		return false
	}
	av, okA := parseSemver(a)
	bv, okB := parseSemver(b)
	return okA && okB && bv.less(av)
}
