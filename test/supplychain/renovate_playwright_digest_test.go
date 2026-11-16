package supplychain

// Playwright image digest rule (#347, spec item 5): digest-only updates of
// mcr.microsoft.com/playwright are turned off, so each pin of the image gets
// one upgrade, the version update with its own digest. Renovate applies
// packageRules in order and the later rule wins, so the disabling rule must
// come after every rule that could re-enable the package, and the playwright
// group must keep covering version updates of the image and @playwright/test.

import (
	"encoding/json"
	"reflect"
	"testing"
)

type renovatePkgRule struct {
	Description      string   `json:"description"`
	MatchPackageName []string `json:"matchPackageNames"`
	MatchUpdateTypes []string `json:"matchUpdateTypes"`
	Enabled          *bool    `json:"enabled"`
	GroupName        string   `json:"groupName"`
}

func (r renovatePkgRule) matches(pkg string) bool {
	for _, n := range r.MatchPackageName {
		if n == pkg {
			return true
		}
	}
	return false
}

func TestRenovatePlaywrightImageDigestUpdatesDisabled(t *testing.T) {
	const image = "mcr.microsoft.com/playwright"
	var cfg struct {
		PackageRules []renovatePkgRule `json:"packageRules"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "renovate.json")), &cfg); err != nil {
		t.Fatalf("renovate.json: %v", err)
	}
	rules := cfg.PackageRules

	groupIdx, disableIdx := -1, -1
	for i, r := range rules {
		if r.GroupName == "playwright" {
			groupIdx = i
		}
		if r.matches(image) && reflect.DeepEqual(r.MatchUpdateTypes, []string{"digest"}) &&
			r.Enabled != nil && !*r.Enabled {
			disableIdx = i
		}
	}

	// (c) the playwright group still covers both packages, not digest-only.
	if groupIdx < 0 {
		t.Fatalf("no packageRule with groupName \"playwright\"")
	}
	g := rules[groupIdx]
	if !g.matches(image) || !g.matches("@playwright/test") {
		t.Errorf("playwright group rule must list %s and @playwright/test, has %v", image, g.MatchPackageName)
	}
	if reflect.DeepEqual(g.MatchUpdateTypes, []string{"digest"}) {
		t.Errorf("playwright group rule must not be restricted to digest updates")
	}
	if g.Enabled != nil && !*g.Enabled {
		t.Errorf("playwright group rule must not be disabled")
	}

	// (a) a rule disables digest-only updates of the image.
	if disableIdx < 0 {
		t.Fatalf("no packageRule matches %s with matchUpdateTypes [\"digest\"] and enabled false", image)
	}

	// (b) it comes after the group rule and no later rule re-enables the image.
	if disableIdx < groupIdx {
		t.Errorf("digest-disabling rule (index %d) must come after the playwright group rule (index %d): the later rule wins", disableIdx, groupIdx)
	}
	for i := disableIdx + 1; i < len(rules); i++ {
		if rules[i].matches(image) && rules[i].Enabled != nil && *rules[i].Enabled {
			t.Errorf("rule %d, after the disabling rule, re-enables %s", i, image)
		}
	}
}
