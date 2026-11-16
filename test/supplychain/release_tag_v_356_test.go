package supplychain

// #356: for TRIVY, GOLANGCI_LINT, ZIZMOR and ACTIONLINT the *_VERSION value is
// the GitHub release tag, `v` included, because the github-release-attachments
// datasource asks GitHub for releases/tags/<currentValue>. The annotations
// carry no extractVersion. Download URLs use ${X_VERSION} as the tag; file
// names that hold the bare version use ${X_VERSION#v}.

import (
	"regexp"
	"strings"
	"testing"
)

var releaseTagPins = []string{"TRIVY", "GOLANGCI_LINT", "ZIZMOR", "ACTIONLINT"}

// namesWithBareVersion are the pins whose tarball name contains the version.
var namesWithBareVersion = []string{"TRIVY", "GOLANGCI_LINT", "ACTIONLINT"}

var vTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func releaseTagBlocks(t *testing.T) map[string]checksumBlock {
	t.Helper()
	_, withSHA, _ := ciPinBlocks(t)
	got := map[string]checksumBlock{}
	for _, b := range withSHA {
		got[b.prefix] = b
	}
	for _, p := range releaseTagPins {
		if _, ok := got[p]; !ok {
			t.Fatalf("%s_VERSION directly above %s_SHA256 not found in .gitlab-ci.yml", p, p)
		}
	}
	return got
}

func TestReleaseTagPinsKeepLeadingV(t *testing.T) {
	blocks := releaseTagBlocks(t)
	for _, p := range releaseTagPins {
		if v := blocks[p].version; !vTagRe.MatchString(v) {
			t.Errorf("%s_VERSION is %q; it must be the release tag with its leading v, like \"v1.2.3\" (#356)", p, v)
		}
	}
}

func TestReleaseTagAnnotationsHaveNoExtractVersion(t *testing.T) {
	blocks := releaseTagBlocks(t)
	for _, p := range releaseTagPins {
		a := blocks[p].annotation
		if !strings.Contains(a, "datasource=github-release-attachments") {
			t.Errorf("%s_VERSION annotation %q lost datasource=github-release-attachments", p, a)
		}
		if strings.Contains(a, "extractVersion") {
			t.Errorf("%s_VERSION annotation must not carry extractVersion (the pin is the release tag): %q (#356)", p, a)
		}
	}
}

func TestReleaseTagDownloadURLsUseTagDirectly(t *testing.T) {
	file := readRepoFile(t, ".gitlab-ci.yml")
	for _, p := range releaseTagPins {
		if bad := "/download/v${" + p + "_VERSION"; strings.Contains(file, bad) {
			t.Errorf("a download URL contains %q: the tag already has its v, so this asks for vv... (#356)", bad)
		}
		if want := "/download/${" + p + "_VERSION}/"; !strings.Contains(file, want) {
			t.Errorf("no download URL contains %q (#356)", want)
		}
	}
}

func TestReleaseTagFileNamesUseBareVersion(t *testing.T) {
	file := readRepoFile(t, ".gitlab-ci.yml")
	for _, p := range releaseTagPins {
		// Every use of the variable other than its definition must be either
		// the tag in a download URL or the bare form ${X_VERSION#v}.
		re := regexp.MustCompile(`(.{0,12})\$\{` + p + `_VERSION(#v)?\}`)
		for _, m := range re.FindAllStringSubmatch(file, -1) {
			if m[2] == "" && !strings.HasSuffix(m[1], "/download/") {
				t.Errorf("%q uses ${%s_VERSION} outside a download tag, so a file name would contain the v; use ${%s_VERSION#v} (#356)", m[0], p, p)
			}
		}
	}
	for _, p := range namesWithBareVersion {
		if !strings.Contains(file, "${"+p+"_VERSION#v}") {
			t.Errorf("the %s tarball name must be built from ${%s_VERSION#v} (#356)", p, p)
		}
	}
}

func TestChecksumManagerCapturesVPrefixedValue(t *testing.T) {
	file, _, _ := ciPinBlocks(t)
	blocks := releaseTagBlocks(t)
	for _, p := range releaseTagPins {
		b := blocks[p]
		found := false
		for _, m := range renovateManagers(t) {
			if !isChecksumManager(m) {
				continue
			}
			for _, re := range compileMatchStrings(t, m) {
				names := re.SubexpNames()
				for _, loc := range re.FindAllStringSubmatchIndex(file, -1) {
					if loc[0] > b.start || loc[1] < b.shaEnd {
						continue
					}
					for i, n := range names {
						if n == "currentValue" && loc[2*i] >= 0 {
							v := file[loc[2*i]:loc[2*i+1]]
							if v == b.version && vTagRe.MatchString(v) {
								found = true
							}
						}
					}
				}
			}
		}
		if !found {
			t.Errorf("%s: the checksum manager does not capture a v-prefixed currentValue (pin is %q) (#356)", p, b.version)
		}
	}
}
