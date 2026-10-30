// The live install's privilege split (#303). The two live legs run the
// branch's tests on the privileged runner as orbit-ci, whose docker group
// makes them root in the job container, so nothing in that container is
// hidden from them. They must therefore hold no credential worth taking:
// `live-prepare`, an unprivileged job on the shared runner, reads Orbit's
// files with ORBIT_READ_TOKEN (a read-only token scoped to its own
// `orbit-reads` environment), verifies them and hands them to the legs as
// artifacts. These checks pin that split: only live-prepare sends a token
// header, the legs never use the job token, and live-prepare exists whenever
// a leg does.
package supplychain

import (
	"strings"
	"testing"
)

// nonCommentScriptLines returns a job's effective script lines that are not
// shell comments, trimmed.
func nonCommentScriptLines(t *testing.T, doc *yamlDoc, job string) []string {
	t.Helper()
	e, err := doc.effectiveJob(job)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(e.Script, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func TestLiveLegsUseNoJobToken(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, job := range []string{"live", "live-fedora"} {
		lines := nonCommentScriptLines(t, doc, job)
		if len(lines) < 20 {
			t.Fatalf("%s reads as %d script lines: the reader is losing them", job, len(lines))
		}
		for _, l := range lines {
			for _, header := range []string{"JOB-TOKEN", "PRIVATE-TOKEN"} {
				if strings.Contains(l, header) {
					t.Errorf("%s sends a %s header; only live-prepare reads ai/orbit (#303)\nline: %s", job, header, l)
				}
			}
			if strings.HasPrefix(l, "unset ") {
				continue // dropping a token is not using it
			}
			for _, name := range []string{"CI_JOB_TOKEN", "ORBIT_READ_TOKEN"} {
				if strings.Contains(strings.ReplaceAll(l, "-u "+name, ""), name) {
					t.Errorf("%s uses %s outside an `env -u` wrapper: the legs' tests can read anything in their container (#303)\nline: %s", job, name, l)
				}
			}
		}
	}
}

func TestOnlyLivePrepareSendsATokenHeader(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	headers := 0
	for _, k := range doc.root.keys {
		switch k {
		case "stages", "workflow", "default", "variables":
			continue
		}
		if strings.HasPrefix(k, ".") || doc.root.vals[k].kind != 'm' {
			continue
		}
		lines := nonCommentScriptLines(t, doc, k)
		for i, l := range lines {
			if !strings.Contains(l, "JOB-TOKEN") && !strings.Contains(l, "PRIVATE-TOKEN") {
				continue
			}
			if k != "live-prepare" {
				t.Errorf("%s sends a token header; only live-prepare reads ai/orbit (#303)\nline: %s", k, l)
				continue
			}
			headers++
			if !strings.Contains(l, `--header "PRIVATE-TOKEN: ${ORBIT_READ_TOKEN}"`) {
				t.Errorf("live-prepare must read ai/orbit with ORBIT_READ_TOKEN as a PRIVATE-TOKEN header, never the job token (#303)\nline: %s", l)
			}
			// curl re-sends a custom header to whatever host a redirect
			// names (#298), so the curl this header belongs to refuses one.
			curl := ""
			for j := i; j >= 0; j-- {
				if strings.Contains(lines[j], "curl ") {
					curl = lines[j]
					break
				}
			}
			if !strings.Contains(curl, "--max-redirs 0") {
				t.Errorf("the curl sending live-prepare's token must refuse redirects with --max-redirs 0 (#298)\ncurl: %s", curl)
			}
		}
	}
	// The probe, the GitLab fetch helper, the two current manifest reads,
	// four earlier-release reads and the earlier install.sh.
	if headers < 9 {
		t.Errorf("live-prepare sends its token on %d lines, want at least 9: the reads moved somewhere unchecked (#303)", headers)
	}
}

func TestLivePrepareIsUnprivilegedAndScopedToOrbitReads(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	prep, err := doc.effectiveJob("live-prepare")
	if err != nil {
		t.Fatalf("%v: #303 adds the live-prepare job", err)
	}
	if tags := prep.Keys["tags"]; tags == nil || tags.dump() != `["light"]` {
		t.Errorf("live-prepare must run on the shared light runner, `tags: [light]`, never the privileged one; tags are %v (#303)", tags)
	}
	if env := prep.Keys["environment"]; env == nil || env.dump() != `{"action": "prepare", "name": "orbit-reads"}` {
		t.Errorf("live-prepare must be in environment orbit-reads (action prepare), the only one ORBIT_READ_TOKEN is scoped to (#303)")
	}
	for _, job := range []string{"live", "live-fedora"} {
		e, err := doc.effectiveJob(job)
		if err != nil {
			t.Fatal(err)
		}
		if env := e.Keys["environment"]; env == nil || env.dump() != `{"action": "prepare", "name": "live-pulls"}` {
			t.Errorf("%s must stay in environment live-pulls, so it never receives ORBIT_READ_TOKEN (#301, #303)", job)
		}
		needs := ""
		if n := e.Keys["needs"]; n != nil {
			needs = n.dump()
		}
		if !strings.Contains(needs, `{"artifacts": "true", "job": "live-prepare"}`) {
			t.Errorf("%s must need live-prepare with its artifacts, which carry Orbit's files (#303); needs are %s", job, needs)
		}
	}
	// Not the whole of .live/orbit/: Orbit's verifier caches its cosign
	// download under .live/orbit/.orbit-cosign, which the legs never use.
	if got := prep.Keys["artifacts"].dump(); got != `{"expire_in": "7 days", "paths": [".live/install-script/", ".live/orbit/release/"], "reports": {"dotenv": ".live/prepare.env"}}` {
		t.Errorf("live-prepare must hand over exactly .live/install-script/, .live/orbit/release/ and the dotenv .live/prepare.env; artifacts are %s (#303)", got)
	}
}

// live-prepare must exist whenever a leg does, or the leg's `needs:` breaks
// the pipeline. It starts by itself on every route but the change route,
// where it holds ORBIT_READ_TOKEN and would run a bumped image before anyone
// reviewed it: there it is manual and blocking like the legs, so the legs'
// `needs:` waits for a person to start it (#192, #303, #304).
func TestLivePrepareRunsWheneverALegDoesAndWaitsOnTheChangeRoute(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	doc := loadCI(t, ci)
	prep, err := doc.effectiveJob("live-prepare")
	if err != nil {
		t.Fatal(err)
	}
	live, err := doc.effectiveJob("live")
	if err != nil {
		t.Fatal(err)
	}
	pr, lr := prep.Keys["rules"], live.Keys["rules"]
	if pr == nil || lr == nil || pr.kind != 'q' || lr.kind != 'q' {
		t.Fatal("live-prepare and live must both have a rules list")
	}
	if len(pr.items) != len(lr.items) {
		t.Fatalf("live-prepare has %d rules and live %d: they must be the same routes (#303)", len(pr.items), len(lr.items))
	}
	for i := range lr.items {
		want := &yamlNode{kind: 'm', vals: map[string]*yamlNode{}}
		for _, k := range lr.items[i].keys {
			if k == "when" || k == "allow_failure" {
				continue
			}
			want.keys = append(want.keys, k)
			want.vals[k] = lr.items[i].vals[k]
		}
		// Compared without the gates; the checks below pin those (#304).
		got := &yamlNode{kind: 'm', vals: map[string]*yamlNode{}}
		for _, k := range pr.items[i].keys {
			if k == "when" || k == "allow_failure" {
				continue
			}
			got.keys = append(got.keys, k)
			got.vals[k] = pr.items[i].vals[k]
		}
		if got.dump() != want.dump() {
			t.Errorf("live-prepare's rule %d is %s, want live's route without its manual gate, %s (#303)", i+1, got.dump(), want.dump())
		}
	}
	routes := pr.dump()
	for _, w := range []string{`$RUN_LIVE == \"true\"`, "run-live-matrix", `".gitlab-ci.yml"`} {
		if !strings.Contains(routes, w) {
			t.Errorf("live-prepare's rules must carry the %s route (#303)", w)
		}
	}
	// Only the change route (the rule with `changes:`, which also covers a
	// hotfix into preview or main that edits the CI files, #313) waits for a
	// person, and it must block so the legs, which need live-prepare, cannot
	// start without it (#304). Every other route stays automatic: the label,
	// RUN_LIVE and the dev -> preview promotion are a person's request or a
	// reviewed promotion.
	for i, r := range pr.items {
		_, changes := r.vals["changes"]
		when, hasWhen := scalarOf(r, "when")
		af, hasAF := scalarOf(r, "allow_failure")
		switch {
		case changes:
			if !hasWhen || when != "manual" {
				t.Errorf("live-prepare's change route (rule %d) must be `when: manual`: it holds ORBIT_READ_TOKEN and runs the pipeline's image before anyone has reviewed it (#304); rule: %s", i+1, r.dump())
			}
			if !hasAF || af != "false" {
				t.Errorf("live-prepare's change route (rule %d) must carry `allow_failure: false`, or the legs may start without it (#304); rule: %s", i+1, r.dump())
			}
		case hasWhen:
			t.Errorf("live-prepare's rule %d must stay automatic, only the change route waits (#304); rule: %s", i+1, r.dump())
		}
	}

	// It stops where the legs would: the same skip gate first.
	gate := func(job string) string {
		merged, err := doc.extendsMerge(doc.root.vals[job], 0)
		if err != nil {
			t.Fatal(err)
		}
		bs := merged.vals["before_script"]
		if bs == nil || bs.kind != 'q' || len(bs.items) == 0 {
			t.Fatalf("%s has no before_script", job)
		}
		return bs.items[0].dump()
	}
	if gate("live-prepare") != gate("live") || !strings.Contains(gate("live"), "LIVE_PINS_CHANGED") {
		t.Error("live-prepare must open its before_script with the legs' skip gate (#164, #303)")
	}
	if strings.Contains(strings.Join(liveJobLines(t, ci, "live-prepare"), "\n"), ".privileged_runner") {
		t.Error("live-prepare must not extend .privileged_runner (#303)")
	}
}
