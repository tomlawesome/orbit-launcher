package supplychain

// Checksum pins (#347): a *_VERSION in .gitlab-ci.yml followed directly by a
// *_SHA256 of the same prefix is read by Renovate's github-release-attachments
// datasource, with the checksum captured as currentDigest, so one merge
// request moves the version and its checksum together. Renovate applies each
// matchString to the whole file, so these tests do the same.
//
// GO_VERSION is excluded throughout: it has its own manager (#179) and its
// own checksum handling.

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type checksumBlock struct {
	prefix     string // e.g. BATS
	version    string // unquoted value
	sha        string
	annotation string // line above VERSION, "" if none
	text       string // annotation + VERSION + SHA256 lines as in the file
	start      int    // offset of the annotation's `#` in the file
	verStart   int    // offset of the VERSION line start
	verEnd     int    // offset of the VERSION line end
	shaEnd     int    // offset of the end of the SHA256 value
}

type renovateManager struct {
	Description        string   `json:"description"`
	MatchStrings       []string `json:"matchStrings"`
	DatasourceTemplate string   `json:"datasourceTemplate"`
}

var (
	ciVersionLineRe = regexp.MustCompile(`^\s+([A-Z][A-Z0-9_]*)_VERSION:\s+"?([^\s"]+)"?\s*$`)
	ciSHALineRe     = regexp.MustCompile(`^\s+([A-Z][A-Z0-9_]*)_SHA256:\s+"?([0-9a-f]+)"?\s*$`)
)

// ciPinBlocks returns the VERSION lines with a same-prefix SHA256 line next
// (withSHA) and those without (noSHA).
func ciPinBlocks(t *testing.T) (file string, withSHA []checksumBlock, noSHA []checksumBlock) {
	t.Helper()
	file = readRepoFile(t, ".gitlab-ci.yml")
	lines := strings.Split(file, "\n")
	offs := make([]int, len(lines))
	o := 0
	for i, l := range lines {
		offs[i] = o
		o += len(l) + 1
	}
	for i, l := range lines {
		m := ciVersionLineRe.FindStringSubmatch(l)
		if m == nil || m[1] == "GO" {
			continue
		}
		b := checksumBlock{prefix: m[1], version: m[2], verStart: offs[i], verEnd: offs[i] + len(l)}
		if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "# renovate:") {
			b.annotation = lines[i-1]
		}
		if i+1 < len(lines) {
			if s := ciSHALineRe.FindStringSubmatch(lines[i+1]); s != nil && s[1] == m[1] {
				b.sha = s[2]
				b.shaEnd = offs[i+1] + strings.Index(lines[i+1], s[2]) + len(s[2])
				first := i
				if b.annotation != "" {
					first = i - 1
					b.start = offs[first] + strings.Index(lines[first], "#")
				} else {
					b.start = offs[i]
				}
				b.text = strings.Join(lines[first:i+2], "\n")
				withSHA = append(withSHA, b)
				continue
			}
		}
		noSHA = append(noSHA, b)
	}
	return file, withSHA, noSHA
}

// checksumExcluded are the blocks that keep datasource=github-releases and
// the general annotation manager (#347 item 1): BATS downloads the source
// archive, and GITLEAKS and TTYD are copied into the GitHub workflows.
var checksumExcluded = map[string]bool{"BATS": true, "GITLEAKS": true, "TTYD": true}

func isChecksumManager(m renovateManager) bool {
	return m.DatasourceTemplate == "github-release-attachments" ||
		strings.Contains(strings.Join(m.MatchStrings, "\n"), "github-release-attachments")
}

func overlapsVersionLine(t *testing.T, file string, m renovateManager, b checksumBlock) bool {
	t.Helper()
	for _, re := range compileMatchStrings(t, m) {
		for _, loc := range re.FindAllStringIndex(file, -1) {
			if loc[0] < b.verEnd && loc[1] > b.verStart {
				return true
			}
		}
	}
	return false
}

func renovateManagers(t *testing.T) []renovateManager {
	t.Helper()
	var cfg struct {
		CustomManagers []renovateManager `json:"customManagers"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "renovate.json")), &cfg); err != nil {
		t.Fatalf("parsing renovate.json: %v", err)
	}
	return cfg.CustomManagers
}

// compileMatchStrings compiles a manager's matchStrings with Go's regexp,
// which accepts Renovate's (?<name>...) groups unchanged.
func compileMatchStrings(t *testing.T, m renovateManager) []*regexp.Regexp {
	t.Helper()
	var res []*regexp.Regexp
	for _, ms := range m.MatchStrings {
		re, err := regexp.Compile(ms)
		if err != nil {
			t.Fatalf("matchString does not compile in Go: %v\n  %s", err, ms)
		}
		res = append(res, re)
	}
	return res
}

func TestChecksumPinsCarryAttachmentsAnnotation(t *testing.T) {
	_, withSHA, _ := ciPinBlocks(t)
	if len(withSHA) == 0 {
		t.Fatal("found no *_VERSION followed by a *_SHA256 in .gitlab-ci.yml; the parser is broken or the pins moved")
	}
	for _, b := range withSHA {
		if checksumExcluded[b.prefix] {
			if !strings.Contains(b.annotation, "datasource=github-releases ") && !strings.HasSuffix(b.annotation, "datasource=github-releases") {
				t.Errorf("%s_VERSION must keep datasource=github-releases (excluded in #347 item 1) but the line above it is %q", b.prefix, b.annotation)
			}
			continue
		}
		if !strings.Contains(b.annotation, "datasource=github-release-attachments") {
			t.Errorf("%s_VERSION is followed by %s_SHA256 but the line above it is %q, not a `# renovate: datasource=github-release-attachments depName=<owner/repo>` annotation (#347)", b.prefix, b.prefix, b.annotation)
		}
	}
}

func TestChecksumExclusionsAreRealBlocks(t *testing.T) {
	_, withSHA, _ := ciPinBlocks(t)
	seen := map[string]bool{}
	for _, b := range withSHA {
		seen[b.prefix] = true
	}
	for p := range checksumExcluded {
		if !seen[p] {
			t.Errorf("%s is listed as excluded but is not a *_VERSION directly above *_SHA256 in .gitlab-ci.yml", p)
		}
	}
}

func TestRenovateReadsChecksumPinBlocks(t *testing.T) {
	file, withSHA, _ := ciPinBlocks(t)
	managers := renovateManagers(t)
	for _, b := range withSHA {
		if checksumExcluded[b.prefix] {
			continue
		}
		found := false
		var why string
		for _, m := range managers {
			for _, re := range compileMatchStrings(t, m) {
				names := re.SubexpNames()
				for _, loc := range re.FindAllStringSubmatchIndex(file, -1) {
					if loc[0] > b.start || loc[1] < b.shaEnd {
						continue // does not cover the block
					}
					got := map[string]string{}
					for i, n := range names {
						if n != "" && loc[2*i] >= 0 {
							got[n] = file[loc[2*i]:loc[2*i+1]]
						}
					}
					if got["currentValue"] != b.version || got["currentDigest"] != b.sha {
						why = "a manager covers the block but captures currentValue=" + got["currentValue"] + " currentDigest=" + got["currentDigest"]
						continue
					}
					if m.DatasourceTemplate == "github-release-attachments" || got["datasource"] == "github-release-attachments" {
						found = true
					} else {
						why = "a manager captures the pin but its datasource is not github-release-attachments"
					}
				}
			}
		}
		if !found {
			if why == "" {
				why = "no customManagers matchString matches the block"
			}
			t.Errorf("%s: %s; want currentValue=%q currentDigest=%q with datasource github-release-attachments for block:\n%s (#347)", b.prefix, why, b.version, b.sha, b.text)
		}
	}
}

func TestChecksumPinsAreProposedOnce(t *testing.T) {
	file, withSHA, _ := ciPinBlocks(t)
	managers := renovateManagers(t)
	for _, b := range withSHA {
		var checksum, other []string
		for i, m := range managers {
			if !overlapsVersionLine(t, file, m, b) {
				continue
			}
			name := "customManagers[" + strconv.Itoa(i) + "] " + firstWords(m.Description)
			if isChecksumManager(m) {
				checksum = append(checksum, name)
			} else {
				other = append(other, name)
			}
		}
		if checksumExcluded[b.prefix] {
			if len(checksum) > 0 {
				t.Errorf("%s_VERSION is excluded from the checksum manager (#347) but is read by %v", b.prefix, checksum)
			}
			if len(other) == 0 {
				t.Errorf("%s_VERSION is excluded from the checksum manager but no other manager reads it, so it would go unwatched", b.prefix)
			}
			continue
		}
		if len(other) > 0 {
			t.Errorf("%s_VERSION line is also matched by another manager, so the pin is proposed twice: %v (#347)", b.prefix, other)
		}
	}
}

func TestVersionPinsWithoutChecksumStayWatched(t *testing.T) {
	file, _, noSHA := ciPinBlocks(t)
	managers := renovateManagers(t)
	for _, b := range noSHA {
		if b.annotation == "" {
			continue // item 1 of #328 covers missing annotations
		}
		matched := false
		for _, m := range managers {
			for _, re := range compileMatchStrings(t, m) {
				for _, loc := range re.FindAllStringIndex(file, -1) {
					if loc[0] < b.verEnd && loc[1] > b.verStart {
						matched = true
					}
				}
			}
		}
		if !matched {
			t.Errorf("%s_VERSION has no checksum line and is matched by no customManagers entry (#347 item 3)", b.prefix)
		}
	}
}

func firstWords(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}
