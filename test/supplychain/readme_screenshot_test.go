// Static checks for the README picture script (#373). The refusals are run in
// scripts/test/readme-screenshot.bats; nothing here runs a capture, a browser or
// the network.
//
// Expects:
//   - scripts/readme-screenshot.sh, the one command that writes
//     docs/images/launcher-menu.png.
//   - scripts/release-ldflags.sh, the single place the release link flags
//     live (-trimpath -s -w -buildid=, internal/release.Version and .Revision).
//     No release flags existed in the repo when #373 was written, so this file
//     is the new shared home; readme-screenshot.sh must call it and must not
//     carry its own copy.
//   - test/visual/tests/readme-screenshot.spec.ts, the Playwright capture,
//     skipped unless the environment variable ORBIT_README_SCREENSHOT is set;
//     the script sets it (to 1) and names the spec.
//     (readRepoFile is the helper in pins_test.go.)
//   - docs/releasing.md names scripts/readme-screenshot.sh.
package supplychain

import (
	"strings"
	"testing"
)

func TestReadmeScreenshotLdflagsLiveInOnePlace(t *testing.T) {
	shared := readRepoFile(t, "scripts/release-ldflags.sh")
	for _, want := range []string{
		"-trimpath", "-s -w", "-buildid=",
		"internal/release.Version", "internal/release.Revision",
	} {
		if !strings.Contains(shared, want) {
			t.Errorf("scripts/release-ldflags.sh lacks %q", want)
		}
	}
	shot := readRepoFile(t, "scripts/readme-screenshot.sh")
	if !strings.Contains(shot, "release-ldflags.sh") {
		t.Errorf("scripts/readme-screenshot.sh must take its flags from scripts/release-ldflags.sh")
	}
	for _, dup := range []string{"-buildid=", "internal/release.Version", "-trimpath"} {
		if strings.Contains(shot, dup) {
			t.Errorf("scripts/readme-screenshot.sh carries its own copy of %q; use release-ldflags.sh", dup)
		}
	}
}

func TestReadmeScreenshotSpecSkippedUnlessScriptSetsEnv(t *testing.T) {
	spec := readRepoFile(t, "test/visual/tests/readme-screenshot.spec.ts")
	if !strings.Contains(spec, "ORBIT_README_SCREENSHOT") || !strings.Contains(spec, "skip") {
		t.Errorf("the spec must skip itself unless ORBIT_README_SCREENSHOT is set")
	}
	shot := readRepoFile(t, "scripts/readme-screenshot.sh")
	if !strings.Contains(shot, "ORBIT_README_SCREENSHOT=1") {
		t.Errorf("the script must set ORBIT_README_SCREENSHOT=1 for the capture")
	}
	if !strings.Contains(shot, "readme-screenshot.spec.ts") {
		t.Errorf("the script must run readme-screenshot.spec.ts")
	}
	ci := readRepoFile(t, ".gitlab-ci.yml")
	pkg := readRepoFile(t, "test/visual/package.json")
	if strings.Contains(ci, "ORBIT_README_SCREENSHOT") || strings.Contains(pkg, "ORBIT_README_SCREENSHOT") {
		t.Errorf("CI and package.json must not set ORBIT_README_SCREENSHOT")
	}
}

func TestReadmeScreenshotPinsTtyd(t *testing.T) {
	shot := readRepoFile(t, "scripts/readme-screenshot.sh")
	if !strings.Contains(shot, "1.7.7") {
		t.Errorf("the script must check for ttyd 1.7.7")
	}
	ci := readRepoFile(t, ".gitlab-ci.yml")
	if !strings.Contains(ci, `TTYD_VERSION: "1.7.7"`) {
		t.Errorf(`.gitlab-ci.yml no longer pins TTYD_VERSION: "1.7.7"; move the script's check with it`)
	}
}

func TestReleasingDocNamesReadmeScreenshotScript(t *testing.T) {
	if !strings.Contains(readRepoFile(t, "docs/releasing.md"), "scripts/readme-screenshot.sh") {
		t.Errorf("docs/releasing.md must name scripts/readme-screenshot.sh in the release steps")
	}
}
