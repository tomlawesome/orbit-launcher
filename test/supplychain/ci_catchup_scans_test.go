// A catch-up merge request (a chore/catchup-* branch into batch-a or batch-b)
// must still be scanned for secrets and private details: the two scan jobs
// do not carry the catchup-check skip rule, and the include is pinned to
// catchup-check/v3, which sets CATCHUP_CHECK="true" (orbit-launcher #343,
// ai/agent-infra#156).
package supplychain

import (
	"strings"
	"testing"
)

const catchupSkipRef = "!reference [.catchup-check, job_rule]"

// jobRuleItems returns the rules of a job as written, one string per item
// (`!reference` tags are scalars; mappings are dumped).
func jobRuleItems(t *testing.T, doc *yamlDoc, job string) []string {
	t.Helper()
	eff, err := doc.effectiveJob(job)
	if err != nil {
		t.Fatalf("job %s: %v", job, err)
	}
	rules := eff.Keys["rules"]
	if rules == nil || rules.kind != 'q' {
		t.Fatalf("job %s has no rules list", job)
	}
	var out []string
	for _, it := range rules.items {
		if it.kind == 's' {
			out = append(out, it.str)
		} else {
			out = append(out, it.dump())
		}
	}
	return out
}

func hasCatchupSkip(items []string) bool {
	for _, s := range items {
		if strings.Contains(s, catchupSkipRef) {
			return true
		}
	}
	return false
}

func TestCatchupScans_GitleaksRunsOnACatchupMergeRequest(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	if hasCatchupSkip(jobRuleItems(t, doc, "gitleaks")) {
		t.Errorf("gitleaks rules contain %q: the scan would be skipped on a catch-up merge request", catchupSkipRef)
	}
}

func TestCatchupScans_PrivateDetailsRunsOnACatchupMergeRequest(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	if hasCatchupSkip(jobRuleItems(t, doc, "private-details")) {
		t.Errorf("private-details rules contain %q: the scan would be skipped on a catch-up merge request", catchupSkipRef)
	}
}

// The other jobs that skip on a catch-up today still do.
func TestCatchupScans_EveryOtherJobKeepsTheSkip(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	n := 0
	for _, name := range doc.root.keys {
		if name == "gitleaks" || name == "private-details" || strings.HasPrefix(name, ".") {
			continue
		}
		job := doc.root.vals[name]
		if job == nil || job.kind != 'm' || job.vals["rules"] == nil {
			continue
		}
		if strings.Contains(job.vals["rules"].dump(), catchupSkipRef) {
			n++
		}
	}
	if n < 4 {
		t.Errorf("only %d jobs other than the two scans carry %q, want at least 4 (classify, deps, fast, visual keep it)", n, catchupSkipRef)
	}
	for _, name := range []string{"classify", "deps", "fast", "visual"} {
		if !hasCatchupSkip(jobRuleItems(t, doc, name)) {
			t.Errorf("job %s lost %q", name, catchupSkipRef)
		}
	}
}

func TestCatchupScans_IncludeIsPinnedToV3(t *testing.T) {
	const want = "020e31a597c87e790e8121b7bb13729120aa3405"
	if got := includeRefFor(t, "catchup-check"); got != want {
		t.Errorf("the catchup-check include pins %q, want the catchup-check/v3 commit %s", got, want)
	}
	if !strings.Contains(readRepoFile(t, ".gitlab-ci.yml"), want+"  # catchup-check/v3") {
		t.Errorf("the pin %s must carry the comment `# catchup-check/v3`", want)
	}
}
