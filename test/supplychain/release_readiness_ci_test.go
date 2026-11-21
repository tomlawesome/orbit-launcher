// The release-readiness job (#359, after ai/orbit#1860): on a promotion --
// dev -> preview or preview -> main, and nothing else -- `release_readiness`
// runs first, and nothing else in the pipeline starts before it has passed.
// It replaces #306's `promoted`, the cheap job that gave preview -> main a
// pipeline. Written from the specification, with ci_stages_test.go's
// pipelineFor simulator.
package supplychain

import (
	"fmt"
	"strings"
	"testing"
)

const rrJob = "release_readiness"

// promotions are the two merge requests the job belongs to.
var promotions = []struct{ source, target string }{
	{"dev", "preview"},
	{"preview", "main"},
}

func ciStages(t *testing.T, doc *yamlDoc) []string {
	t.Helper()
	n := doc.root.vals["stages"]
	if n == nil || n.kind != 'q' {
		t.Fatal(".gitlab-ci.yml has no stages list")
	}
	var out []string
	for _, it := range n.items {
		out = append(out, it.str)
	}
	return out
}

func stageOf(t *testing.T, doc *yamlDoc, job string) int {
	t.Helper()
	eff, err := doc.effectiveJob(job)
	if err != nil {
		t.Fatalf("job %s: %v", job, err)
	}
	stage := "test"
	if s := eff.Keys["stage"]; s != nil && s.kind == 's' {
		stage = s.str
	}
	for i, s := range ciStages(t, doc) {
		if s == stage {
			return i
		}
	}
	t.Fatalf("job %s names stage %q, which is not in stages:", job, stage)
	return -1
}

// needsOf returns the jobs a job names in needs:, and whether it has the key.
func needsOf(t *testing.T, doc *yamlDoc, job string) (names []string, has bool) {
	t.Helper()
	eff, err := doc.effectiveJob(job)
	if err != nil {
		t.Fatalf("job %s: %v", job, err)
	}
	n := eff.Keys["needs"]
	if n == nil {
		return nil, false
	}
	if n.kind != 'q' {
		t.Fatalf("job %s: needs: is not a list", job)
	}
	for _, it := range n.items {
		switch it.kind {
		case 's':
			names = append(names, it.str)
		case 'm':
			if j := it.vals["job"]; j != nil {
				names = append(names, j.str)
			}
		}
	}
	return names, true
}

// waitsFor reports whether job cannot start before target has finished:
// through needs: (directly or through the jobs it needs), or, with no
// needs:, by being in a later stage than target.
func waitsFor(t *testing.T, doc *yamlDoc, jobs map[string]pipelineJob, job, target string, seen map[string]bool) bool {
	t.Helper()
	if seen[job] {
		return false
	}
	seen[job] = true
	needs, has := needsOf(t, doc, job)
	if !has {
		return stageOf(t, doc, job) > stageOf(t, doc, target)
	}
	for _, n := range needs {
		if n == target {
			return true
		}
		if _, in := jobs[n]; !in {
			continue // an optional need the pipeline does not create
		}
		if waitsFor(t, doc, jobs, n, target, seen) {
			return true
		}
	}
	return false
}

func TestReleaseReadiness_RunsOnBothPromotions(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, p := range promotions {
		for _, changes := range []bool{false, true} {
			jobs := pipelineFor(t, doc, mrVars(p.source, p.target), changes)
			j, ok := jobs[rrJob]
			if !ok {
				t.Errorf("%s -> %s (changes=%v) must create `%s`; it creates %v", p.source, p.target, changes, rrJob, jobNames(jobs))
				continue
			}
			if j.manual || !j.blocking {
				t.Errorf("`%s` on %s -> %s must run by itself and block the merge; got manual=%v blocking=%v", rrJob, p.source, p.target, j.manual, j.blocking)
			}
		}
	}
}

func TestReleaseReadiness_IsInTheFirstStageWithNoNeeds(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	if _, err := doc.effectiveJob(rrJob); err != nil {
		t.Fatalf("%v: .gitlab-ci.yml has no `%s` job", err, rrJob)
	}
	if got := stageOf(t, doc, rrJob); got != 0 {
		t.Errorf("`%s` must be in the first stage (%s); it is in %s", rrJob, ciStages(t, doc)[0], ciStages(t, doc)[got])
	}
	needs, has := needsOf(t, doc, rrJob)
	if !has || len(needs) != 0 {
		t.Errorf("`%s` must say `needs: []` so it starts at once; needs=%v present=%v", rrJob, needs, has)
	}
}

func TestReleaseReadiness_EveryOtherJobWaitsForIt(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, p := range promotions {
		jobs := pipelineFor(t, doc, mrVars(p.source, p.target), false)
		if _, ok := jobs[rrJob]; !ok {
			t.Errorf("%s -> %s: no `%s` job, so nothing waits for it", p.source, p.target, rrJob)
			continue
		}
		checked := 0
		for _, n := range jobNames(jobs) {
			if n == rrJob {
				continue
			}
			checked++
			if !waitsFor(t, doc, jobs, n, rrJob, map[string]bool{}) {
				t.Errorf("%s -> %s: `%s` can start before `%s` has passed: it needs it (directly or through another job) or must be in a later stage with no needs:", p.source, p.target, n, rrJob)
			}
		}
		if p.target == "preview" && checked < len(liveFamily) {
			t.Errorf("dev -> preview: expected the live family beside `%s`, checked only %d other jobs", rrJob, checked)
		}
	}
}

// The wait is checked, not assumed: a job added with `needs: []` to either
// promotion pipeline would start at once, so the test must catch it.
func TestReleaseReadiness_TheWaitCheckCatchesAJobWithEmptyNeeds(t *testing.T) {
	src := readRepoFile(t, ".gitlab-ci.yml") + `
rogue_job:
  stage: fast
  needs: []
  script:
    - echo hi
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
`
	doc := loadCI(t, src)
	for _, p := range promotions {
		jobs := pipelineFor(t, doc, mrVars(p.source, p.target), false)
		if _, ok := jobs["rogue_job"]; !ok {
			t.Fatalf("%s -> %s: the rogue job was not created; the check's fixture is wrong", p.source, p.target)
		}
		if _, ok := jobs[rrJob]; !ok {
			t.Errorf("%s -> %s: no `%s`, so the wait check has nothing to guard", p.source, p.target, rrJob)
			continue
		}
		if waitsFor(t, doc, jobs, "rogue_job", rrJob, map[string]bool{}) {
			t.Errorf("%s -> %s: a job with `needs: []` was judged to wait for `%s`", p.source, p.target, rrJob)
		}
	}
}

func TestReleaseReadiness_IsAbsentEverywhereElse(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	// The job must exist for its absence to mean something.
	if _, ok := pipelineFor(t, doc, mrVars("dev", "preview"), false)[rrJob]; !ok {
		t.Fatalf("`%s` is not created on dev -> preview", rrJob)
	}
	mrs := []struct{ source, target string }{
		{"feat/x", "dev"},
		{"batch-a", "dev"},
		{"batch-b", "dev"},
		{"feat/x", "batch-a"},
		{"feat/x", "batch-b"},
		{"chore/catchup-2026-11-05", "batch-a"},
		{"chore/catchup-2026-11-05", "batch-b"},
		{"hotfix/y", "preview"},
		{"feat/x", "preview"},
		{"hotfix/y", "main"},
		{"feat/x", "main"},
		{"dev", "main"},
		{"preview", "dev"},  // back-merge
		{"main", "preview"}, // back-merge
		{"main", "dev"},
		{"main", "batch-a"},
	}
	for _, m := range mrs {
		for _, changes := range []bool{false, true} {
			jobs := pipelineFor(t, doc, mrVars(m.source, m.target), changes)
			if _, ok := jobs[rrJob]; ok {
				t.Errorf("%s -> %s (changes=%v) is not a promotion: `%s` must not run", m.source, m.target, changes, rrJob)
			}
		}
	}
	pushes := []string{"main", "dev", "preview", "feat/x", "batch-a"}
	for _, b := range pushes {
		if _, ok := pipelineFor(t, doc, pushVars(b), false)[rrJob]; ok {
			t.Errorf("a push to %s must not run `%s`", b, rrJob)
		}
	}
	for _, src := range []string{"schedule", "web"} {
		vars := pushVars("dev")
		vars["CI_PIPELINE_SOURCE"] = src
		vars["RENOVATE"] = "true"
		vars["RUN_LIVE"] = "true"
		if _, ok := pipelineFor(t, doc, vars, false)[rrJob]; ok {
			t.Errorf("a %s pipeline must not run `%s`", src, rrJob)
		}
	}
}

func TestReleaseReadiness_PromotedIsGone(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	if _, ok := doc.root.vals["promoted"]; ok {
		t.Error("`promoted` must be removed: `release_readiness` now gives preview -> main its pipeline")
	}
	for _, p := range promotions {
		jobs := pipelineFor(t, doc, mrVars(p.source, p.target), false)
		assertAbsent(t, jobs, "promoted")
		if _, ok := jobs[rrJob]; !ok {
			t.Errorf("%s -> %s: `promoted` is gone but `%s` does not take its place; jobs %s", p.source, p.target, rrJob, fmt.Sprint(jobNames(jobs)))
		}
	}
}

// live-prepare names the job, optional, so the legs (which need live-prepare)
// wait for it transitively, and a pipeline without it still builds.
func TestReleaseReadiness_LivePrepareNeedsItOptionally(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	eff, err := doc.effectiveJob("live-prepare")
	if err != nil {
		t.Fatal(err)
	}
	needs := eff.Keys["needs"]
	if needs == nil || needs.kind != 'q' {
		t.Fatal("live-prepare has no needs: list")
	}
	for _, it := range needs.items {
		if it.kind == 'm' && it.vals["job"] != nil && it.vals["job"].str == rrJob {
			if o := it.vals["optional"]; o == nil || o.str != "true" {
				t.Errorf("live-prepare must need `%s` with `optional: true`", rrJob)
			}
			return
		}
	}
	t.Errorf("live-prepare must list `%s` in needs:", rrJob)
}

// Review of 9500af3: the credential drop leaves origin anonymous, so the
// job fetches the remote's tags before it, and tells the tool not to fetch.
func TestReleaseReadiness_FetchesTheTagsBeforeTheDropAndRunsWithNoFetch(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	eff, err := doc.effectiveJob(rrJob)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(eff.Script, "\n")
	fetch, drop, tool := -1, -1, -1
	for i, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "git fetch") && strings.Contains(l, "refs/tags/*:refs/remote-tags/origin/*") && strings.Contains(l, "--force"):
			fetch = i
		case strings.HasPrefix(l, "git remote set-url origin"):
			drop = i
		case strings.HasPrefix(l, "go run ./tools/releasereadiness") || strings.HasPrefix(l, `RELEASE_READINESS_TOKEN="$keep" go run ./tools/releasereadiness`): // #365
			tool = i
			if !strings.Contains(l, "-no-fetch") {
				t.Errorf("the tool must run with -no-fetch after the drop: %q", l)
			}
		}
	}
	if fetch < 0 || drop < 0 || tool < 0 || !(fetch < drop && drop < tool) {
		t.Errorf("want the tag fetch, then the credential drop, then the tool; got lines %d, %d, %d in:\n%s", fetch, drop, tool, eff.Script)
	}
}
