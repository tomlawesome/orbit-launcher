// The live job's rules (#192). The job runs the pipeline's UBUNTU_IMAGE on the
// privileged runner, and its third route fires on any merge-request edit to
// .gitlab-ci.yml -- including one that moves that image. That route must wait
// for a person to start it, and must block the merge until they do; the
// RUN_LIVE and run-live-matrix routes are a person's own request and stay
// automatic.
//
// go.mod carries no YAML library and this check is not worth adding one, so the
// rules block is read as text: the rules are written one `- if:` item each,
// with their keys indented under it.
package supplychain

import (
	"strings"
	"testing"
)

// liveRules returns the `live` job's rules as one string per `- ` item, with
// comment lines dropped.
func liveRules(t *testing.T, ciYAML string) []string {
	t.Helper()
	lines := strings.Split(ciYAML, "\n")

	start := -1
	for i, l := range lines {
		if l == "live:" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("no top-level `live:` job in .gitlab-ci.yml")
	}

	var rules []string
	var cur []string
	inRules := false
	for _, l := range lines[start+1:] {
		trimmed := strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
			break // next top-level key: the job has ended
		}
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		if indent == 2 {
			if inRules {
				break // the next job key after rules:
			}
			inRules = trimmed == "rules:"
			continue
		}
		if !inRules {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && indent == 4 {
			if cur != nil {
				rules = append(rules, strings.Join(cur, "\n"))
			}
			cur = []string{trimmed}
			continue
		}
		cur = append(cur, trimmed)
	}
	if cur != nil {
		rules = append(rules, strings.Join(cur, "\n"))
	}
	if len(rules) == 0 {
		t.Fatal("the `live` job has no rules: block, or it is not indented as expected")
	}
	return rules
}

func hasLine(rule, want string) bool {
	for _, l := range strings.Split(rule, "\n") {
		if l == want {
			return true
		}
	}
	return false
}

func TestLiveCIChangeRouteIsAManualGate(t *testing.T) {
	rules := liveRules(t, readRepoFile(t, ".gitlab-ci.yml"))

	var changeRule, runLive, label string
	for _, r := range rules {
		switch {
		case strings.Contains(r, "changes:") && hasLine(r, "- .gitlab-ci.yml"):
			changeRule = r
		case strings.Contains(r, "$RUN_LIVE"):
			runLive = r
		case strings.Contains(r, "run-live-matrix"):
			label = r
		}
	}

	if changeRule == "" {
		t.Fatal("the `live` job has no rule with `changes: [.gitlab-ci.yml]` (#164)")
	}
	if !hasLine(changeRule, "when: manual") {
		t.Errorf("the `live` job's .gitlab-ci.yml-change rule must carry `when: manual`: "+
			"it runs the pipeline's image on the privileged runner, so a person starts it (#192)\nrule:\n%s", changeRule)
	}
	if !hasLine(changeRule, "allow_failure: false") {
		t.Errorf("the `live` job's .gitlab-ci.yml-change rule must carry `allow_failure: false`, "+
			"so the merge request waits for the job instead of skipping it (#192)\nrule:\n%s", changeRule)
	}

	for name, r := range map[string]string{"RUN_LIVE": runLive, "run-live-matrix label": label} {
		if r == "" {
			t.Errorf("the `live` job has no %s rule", name)
			continue
		}
		if strings.Contains(r, "when: manual") {
			t.Errorf("the `live` job's %s rule must stay automatic: a person already asked for the run (#192)\nrule:\n%s", name, r)
		}
	}
}
