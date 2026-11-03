package supplychain

// A tool version pinned in .gitlab-ci.yml that Renovate does not watch goes
// stale silently, and the dependency rule requires CI to check staleness
// (#328). Every *_VERSION variable (except GO_VERSION, which has its own
// manager, #179) must carry a `# renovate: datasource=... depName=...`
// comment on the line above, and renovate.json must have a custom manager
// whose matchStrings read those comments.

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var ciVersionVarRe = regexp.MustCompile(`^\s+([A-Z][A-Z0-9_]*_VERSION):\s`)
var renovateAnnotationRe = regexp.MustCompile(`^\s*# renovate: datasource=\S+ depName=\S+`)

func TestEveryCIToolPinCarriesARenovateAnnotation(t *testing.T) {
	lines := strings.Split(readRepoFile(t, ".gitlab-ci.yml"), "\n")
	var bad []string
	for i, l := range lines {
		m := ciVersionVarRe.FindStringSubmatch(l)
		if m == nil || m[1] == "GO_VERSION" {
			continue
		}
		if i == 0 || !renovateAnnotationRe.MatchString(lines[i-1]) {
			bad = append(bad, m[1])
		}
	}
	if len(bad) > 0 {
		t.Errorf("these .gitlab-ci.yml variables have no Renovate annotation on the line above, so Renovate never proposes a new version (#328):\n  %s\n"+
			"Add a line directly above each, e.g. `  # renovate: datasource=github-releases depName=owner/repo`\n"+
			"(use datasource=go with the module path for Go tools).",
			strings.Join(bad, "\n  "))
	}
}

func TestRenovateReadsCIAnnotations(t *testing.T) {
	var cfg struct {
		CustomManagers []struct {
			MatchStrings []string `json:"matchStrings"`
		} `json:"customManagers"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "renovate.json")), &cfg); err != nil {
		t.Fatalf("parsing renovate.json: %v", err)
	}
	for _, cm := range cfg.CustomManagers {
		for _, ms := range cm.MatchStrings {
			if strings.Contains(ms, "renovate: datasource=") {
				return
			}
		}
	}
	t.Errorf("renovate.json has no customManagers entry whose matchStrings contain `renovate: datasource=`, so the annotations in .gitlab-ci.yml are never read (#328)")
}
