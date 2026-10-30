// Which pipeline runs which checks (#306). Test once, then promote what
// passed: a merge request into dev runs the full set; the push that lands it
// runs nothing; the dev -> preview promotion adds only the live installs; the
// preview -> main promotion adds nothing new; a push to main offers only the
// release buttons; a push to a feature branch before its merge request opens
// runs nothing (owner, answer 5a). Scheduled (Renovate) and hand-started
// pipelines keep working.
//
// The tests are written from that table, not from the current rules. They
// evaluate `workflow:` and each job's `rules:` the way GitLab does for a
// given set of CI variables, then assert which jobs the pipeline creates and
// whether the live jobs wait for a person.
//
// The evaluator handles exactly the forms .gitlab-ci.yml uses in `if:`:
// `==`, `!=`, `=~ /re/`, `!~ /re/`, `&&`, `||`, parentheses, a bare `$VAR`,
// `"strings"` and `null`. A rule's `changes:` is not evaluated against a diff:
// each scenario states whether the changed files match (changesMatch), because
// the rule says which files, not what this pipeline changed. A rule key it
// does not know stops the test rather than being skipped. `variables:` set
// inside a rule are ignored.
package supplychain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ciVars is the CI variable set one pipeline is evaluated with.
type ciVars map[string]string

type ruleVerdict struct {
	matched    bool
	never      bool // when: never
	manual     bool
	allowFail  bool // allow_failure: true, or a manual job's implicit default
	allowFailS string
}

// ---- if: expression evaluator -------------------------------------------

type exprParser struct {
	toks []string
	pos  int
	vars ciVars
	src  string
}

var exprTokenRe = regexp.MustCompile(`^\s*(\|\||&&|==|!=|=~|!~|\(|\)|"[^"]*"|'[^']*'|/(?:\\.|[^/\\])*/[a-z]*|\$[A-Za-z_][A-Za-z0-9_]*|\$\{[A-Za-z_][A-Za-z0-9_]*\}|null|[A-Za-z0-9_.-]+)`)

func tokenize(t *testing.T, src string) []string {
	t.Helper()
	var toks []string
	rest := src
	for strings.TrimSpace(rest) != "" {
		m := exprTokenRe.FindStringSubmatch(rest)
		if m == nil {
			t.Fatalf("rules: cannot read %q in the condition %q", strings.TrimSpace(rest), src)
		}
		toks = append(toks, m[1])
		rest = rest[len(m[0]):]
	}
	return toks
}

// evalIf reports whether an `if:` expression is true under vars.
func evalIf(t *testing.T, src string, vars ciVars) bool {
	t.Helper()
	p := &exprParser{toks: tokenize(t, src), vars: vars, src: src}
	v := p.or(t)
	if p.pos != len(p.toks) {
		t.Fatalf("rules: trailing %q in the condition %q", p.toks[p.pos], src)
	}
	return v
}

func (p *exprParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *exprParser) or(t *testing.T) bool {
	v := p.and(t)
	for p.peek() == "||" {
		p.pos++
		r := p.and(t) // always parse; no short-circuit of syntax
		v = v || r
	}
	return v
}

func (p *exprParser) and(t *testing.T) bool {
	v := p.cmp(t)
	for p.peek() == "&&" {
		p.pos++
		r := p.cmp(t)
		v = v && r
	}
	return v
}

// operand returns a value and whether it is null/unset.
func (p *exprParser) operand(t *testing.T) (val string, isNull bool, isRegex bool) {
	tok := p.peek()
	if tok == "" {
		t.Fatalf("rules: the condition %q ends where a value was expected", p.src)
	}
	p.pos++
	switch {
	case tok == "null":
		return "", true, false
	case strings.HasPrefix(tok, "$"):
		name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(tok, "$"), "{"), "}")
		v, ok := p.vars[name]
		if !ok {
			return "", true, false
		}
		return v, false, false
	case tok[0] == '"' || tok[0] == '\'':
		return tok[1 : len(tok)-1], false, false
	case tok[0] == '/':
		return tok, false, true
	}
	t.Fatalf("rules: unexpected %q in the condition %q", tok, p.src)
	return
}

func (p *exprParser) cmp(t *testing.T) bool {
	if p.peek() == "(" {
		p.pos++
		v := p.or(t)
		if p.peek() != ")" {
			t.Fatalf("rules: missing ) in the condition %q", p.src)
		}
		p.pos++
		return v
	}
	lv, lnull, _ := p.operand(t)
	switch p.peek() {
	case "==", "!=":
		op := p.toks[p.pos]
		p.pos++
		rv, rnull, _ := p.operand(t)
		eq := (lnull && rnull) || (!lnull && !rnull && lv == rv)
		// GitLab treats an unset variable as equal to null only; an empty
		// string is not null, but it is a value that compares unequal to it.
		if op == "==" {
			return eq
		}
		return !eq
	case "=~", "!~":
		op := p.toks[p.pos]
		p.pos++
		rv, _, isRe := p.operand(t)
		if !isRe {
			t.Fatalf("rules: %s needs a /regex/ on the right in %q", op, p.src)
		}
		end := strings.LastIndex(rv, "/")
		pat, flags := rv[1:end], rv[end+1:]
		if strings.Contains(flags, "i") {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			t.Fatalf("rules: bad regex %s in %q: %v", rv, p.src, err)
		}
		m := !lnull && re.MatchString(lv)
		if op == "=~" {
			return m
		}
		return !m
	}
	// A bare variable: true when set and non-empty.
	return !lnull && lv != ""
}

// ---- rules ---------------------------------------------------------------

// scalarOf returns a scalar child of a mapping node, "" and false if absent.
func scalarOf(n *yamlNode, key string) (string, bool) {
	if n == nil || n.kind != 'm' {
		return "", false
	}
	v, ok := n.vals[key]
	if !ok || v.kind != 's' {
		return "", false
	}
	return v.str, true
}

// evalRules returns the verdict of the first rule that matches, and whether
// any did. A nil rules node means "no rules:" (the caller decides).
func evalRules(t *testing.T, where string, rules *yamlNode, vars ciVars, changesMatch bool) ruleVerdict {
	t.Helper()
	if rules.kind != 'q' {
		t.Fatalf("%s: rules: did not read as a list", where)
	}
	for i, r := range rules.items {
		if r.kind != 'm' {
			t.Fatalf("%s: rule %d did not read as a mapping", where, i+1)
		}
		for _, k := range r.keys {
			switch k {
			case "if", "when", "changes", "allow_failure", "variables":
			default:
				t.Fatalf("%s: rule %d uses %q, which this evaluator does not know", where, i+1, k)
			}
		}
		if cond, ok := scalarOf(r, "if"); ok && !evalIf(t, cond, vars) {
			continue
		}
		if _, ok := r.vals["changes"]; ok && !changesMatch {
			continue
		}
		v := ruleVerdict{matched: true}
		when, _ := scalarOf(r, "when")
		switch when {
		case "never":
			v.never = true
		case "manual":
			v.manual = true
			v.allowFail = true // GitLab: a manual job may fail by default...
		case "", "on_success", "always", "delayed":
		default:
			t.Fatalf("%s: rule %d has when: %s", where, i+1, when)
		}
		if af, ok := scalarOf(r, "allow_failure"); ok {
			v.allowFail = af == "true" // ...unless the rule says otherwise
			v.allowFailS = af
		}
		return v
	}
	return ruleVerdict{}
}

// pipelineJob is one job the pipeline creates.
type pipelineJob struct {
	manual   bool // waits for a person
	blocking bool // a failure, or a wait, stops the merge
}

// pipelineFor returns the jobs created for a pipeline with these variables;
// nil when `workflow:` creates no pipeline at all.
func pipelineFor(t *testing.T, doc *yamlDoc, vars ciVars, changesMatch bool) map[string]pipelineJob {
	t.Helper()
	all := ciVars{}
	if gv := doc.root.vals["variables"]; gv != nil && gv.kind == 'm' {
		for _, k := range gv.keys {
			if gv.vals[k].kind == 's' {
				all[k] = gv.vals[k].str
			}
		}
	}
	for k, v := range vars {
		all[k] = v
	}

	if wf := doc.root.vals["workflow"]; wf != nil && wf.kind == 'm' {
		if rules := wf.vals["rules"]; rules != nil {
			v := evalRules(t, "workflow", rules, all, changesMatch)
			if !v.matched || v.never {
				return nil
			}
		}
	}

	jobs := map[string]pipelineJob{}
	reserved := map[string]bool{"include": true, "stages": true, "workflow": true, "default": true, "variables": true}
	for _, name := range doc.root.keys {
		if reserved[name] || strings.HasPrefix(name, ".") {
			continue
		}
		eff, err := doc.effectiveJob(name)
		if err != nil {
			t.Fatalf("reading job %s: %v", name, err)
		}
		rules := eff.Keys["rules"]
		if rules == nil {
			jobs[name] = pipelineJob{blocking: true}
			continue
		}
		v := evalRules(t, "job "+name, rules, all, changesMatch)
		if !v.matched || v.never {
			continue
		}
		jobs[name] = pipelineJob{manual: v.manual, blocking: !v.allowFail}
	}
	return jobs
}

func jobNames(jobs map[string]pipelineJob) []string {
	var out []string
	for n := range jobs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ---- scenarios -----------------------------------------------------------

var (
	liveFamily     = []string{"live-prepare", "live", "live-fedora"}
	releaseJobs    = []string{"release:patch", "release:minor", "release:gitlab:patch", "release:gitlab:minor"}
	checkJobsToDev = []string{"classify", "fast", "deps", "gitleaks", "private-details", "visual"}
)

func mrVars(source, target string) ciVars {
	return ciVars{
		"CI_PIPELINE_SOURCE":                  "merge_request_event",
		"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": source,
		"CI_MERGE_REQUEST_TARGET_BRANCH_NAME": target,
		"CI_MERGE_REQUEST_EVENT_TYPE":         "",
		"CI_MERGE_REQUEST_LABELS":             "",
		"CI_PROJECT_PATH":                     "ai/orbit-launcher",
		"CI_DEFAULT_BRANCH":                   "dev",
	}
}

func pushVars(branch string) ciVars {
	return ciVars{
		"CI_PIPELINE_SOURCE": "push",
		"CI_COMMIT_BRANCH":   branch,
		"CI_COMMIT_REF_NAME": branch,
		"CI_PROJECT_PATH":    "ai/orbit-launcher",
		"CI_DEFAULT_BRANCH":  "dev",
	}
}

func assertAbsent(t *testing.T, jobs map[string]pipelineJob, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, ok := jobs[n]; ok {
			t.Errorf("the pipeline must not create `%s` here; it creates %v", n, jobNames(jobs))
		}
	}
}

func assertPresent(t *testing.T, jobs map[string]pipelineJob, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, ok := jobs[n]; !ok {
			t.Errorf("the pipeline must create `%s` here; it creates %v", n, jobNames(jobs))
		}
	}
}

// A merge request into dev runs the checks; the live installs only on a label
// or a pin change, as before.
func TestCIStages_AMergeRequestIntoDevRunsTheChecks(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))

	// Changed files match the `changes:` rules (visual's paths, the pin).
	jobs := pipelineFor(t, doc, mrVars("feat/x", "dev"), true)
	assertPresent(t, jobs, checkJobsToDev...)
	assertAbsent(t, jobs, releaseJobs...)
	assertAbsent(t, jobs, "renovate")
	for _, n := range []string{"live", "live-fedora"} {
		if j, ok := jobs[n]; ok && !j.manual {
			t.Errorf("`%s` must not start by itself on a merge request into dev without the label: "+
				"the pin-change route is manual (as today)", n)
		}
	}

	// No changed file matches: the checks without a `changes:` rule still
	// run, and the live jobs are not created at all.
	jobs = pipelineFor(t, doc, mrVars("feat/x", "dev"), false)
	assertPresent(t, jobs, "classify", "fast", "deps", "gitleaks", "private-details")
	assertAbsent(t, jobs, "visual")
	assertAbsent(t, jobs, liveFamily...)
	assertAbsent(t, jobs, releaseJobs...)
}

func TestCIStages_TheLiveLabelOnAMergeRequestIntoDevStartsTheLiveInstalls(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	vars := mrVars("feat/x", "dev")
	vars["CI_MERGE_REQUEST_LABELS"] = "run-live-matrix"
	for _, changes := range []bool{false, true} {
		jobs := pipelineFor(t, doc, vars, changes)
		assertPresent(t, jobs, liveFamily...)
		for _, n := range liveFamily {
			if j := jobs[n]; j.manual {
				t.Errorf("`%s` must start by itself when the merge request carries the run-live-matrix label (changes=%v)", n, changes)
			}
		}
		assertPresent(t, jobs, checkJobsToDev[:5]...)
	}
}

func TestCIStages_APinChangeOnAMergeRequestIntoDevWaitsForAPersonAndBlocksTheMerge(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	jobs := pipelineFor(t, doc, mrVars("feat/x", "dev"), true)
	for _, n := range []string{"live", "live-fedora"} {
		j, ok := jobs[n]
		if !ok {
			t.Errorf("`%s` must exist (as a manual job) on a merge request into dev whose files match the pin-change rule", n)
			continue
		}
		if !j.manual || !j.blocking {
			t.Errorf("`%s` on the pin-change route must be manual and blocking; got manual=%v blocking=%v", n, j.manual, j.blocking)
		}
	}
}

// The push that lands a merge request into dev adds nothing: semi-linear
// history means the merge request tested exactly what lands.
func TestCIStages_ThePushThatLandsAMergeRequestIntoDevRunsNothing(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, changes := range []bool{false, true} {
		jobs := pipelineFor(t, doc, pushVars("dev"), changes)
		if len(jobs) != 0 {
			t.Errorf("a push to dev must create no jobs (changes=%v); it creates %v", changes, jobNames(jobs))
		}
	}
}

// A promotion carries exactly the code dev passed, so only the live installs
// add evidence there.
func TestCIStages_APromotionToPreviewRunsOnlyTheLiveInstalls(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, changes := range []bool{false, true} {
		for _, labels := range []string{"", "run-live-matrix"} {
			vars := mrVars("dev", "preview")
			vars["CI_MERGE_REQUEST_LABELS"] = labels
			jobs := pipelineFor(t, doc, vars, changes)
			where := fmt.Sprintf("changes=%v labels=%q", changes, labels)
			assertPresent(t, jobs, liveFamily...)
			assertAbsent(t, jobs, "fast", "deps", "visual")
			assertAbsent(t, jobs, releaseJobs...)
			assertAbsent(t, jobs, "renovate")
			for _, n := range liveFamily {
				j := jobs[n]
				if j.manual {
					t.Errorf("`%s` must run automatically on dev -> preview (%s), not wait for a person", n, where)
				}
				if !j.blocking {
					t.Errorf("`%s` must block the dev -> preview merge (%s): allow_failure must be false", n, where)
				}
			}
		}
	}
}

// The preview -> main promotion reuses preview's results. Exactly one cheap
// job, `promoted`, exists so "pipelines must succeed" has a pipeline to pass.
func TestCIStages_APromotionToMainAddsNothingNew(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, changes := range []bool{false, true} {
		jobs := pipelineFor(t, doc, mrVars("preview", "main"), changes)
		assertPresent(t, jobs, "promoted")
		assertAbsent(t, jobs, "classify", "fast", "deps", "visual", "gitleaks", "private-details", "flake-check")
		assertAbsent(t, jobs, liveFamily...)
		assertAbsent(t, jobs, releaseJobs...)
		assertAbsent(t, jobs, "renovate")
		if len(jobs) != 1 {
			t.Errorf("a preview -> main merge request must create exactly one job, `promoted` (changes=%v); it creates %v", changes, jobNames(jobs))
		}
	}
}

// A promotion is the move from the stage before: only dev -> preview gets the
// live installs and no checks; only preview -> main gets `promoted`.
func TestCIStages_AMergeRequestFromDevIntoPreviewRunsTheLiveInstallsAndNoChecks(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, changes := range []bool{false, true} {
		jobs := pipelineFor(t, doc, mrVars("dev", "preview"), changes)
		assertPresent(t, jobs, liveFamily...)
		for _, n := range liveFamily {
			if j, ok := jobs[n]; ok && j.manual {
				t.Errorf("`%s` must run automatically on dev -> preview (changes=%v), not wait for a person", n, changes)
			}
		}
		assertAbsent(t, jobs, "classify", "fast", "deps", "gitleaks", "private-details", "visual", "flake-check", "promoted")
	}
}

// Any source other than the stage before is not a promotion: it is an
// ordinary change aimed at a later branch, and it runs the same checks as the
// same branch aimed at dev. The comparison is against the simulated dev
// pipeline, so the test does not hard-code the list of checks.
func TestCIStages_AMergeRequestIntoPreviewOrMainFromAnyOtherBranchRunsTheSameChecksAsIntoDev(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	cases := []struct{ source, target string }{
		{"feature/x", "preview"},
		{"hotfix/y", "preview"},
		{"feature/x", "main"},
		{"hotfix/y", "main"},
		{"dev", "main"},
	}
	for _, c := range cases {
		for _, changes := range []bool{false, true} {
			where := fmt.Sprintf("%s -> %s (changes=%v)", c.source, c.target, changes)
			want := pipelineFor(t, doc, mrVars(c.source, "dev"), changes)
			got := pipelineFor(t, doc, mrVars(c.source, c.target), changes)

			// The dev pipeline is the yardstick; make sure it is the checks.
			assertPresent(t, want, "classify", "fast", "deps", "gitleaks", "private-details")

			assertAbsent(t, got, "promoted")
			assertAbsent(t, got, releaseJobs...)
			assertAbsent(t, got, "renovate")
			for _, n := range jobNames(want) {
				w := want[n]
				g, ok := got[n]
				if !ok {
					t.Errorf("%s: the pipeline must create `%s`, as a merge request into dev does; it creates %v", where, n, jobNames(got))
					continue
				}
				if g != w {
					t.Errorf("%s: `%s` must behave as on a merge request into dev (manual=%v blocking=%v); got manual=%v blocking=%v",
						where, n, w.manual, w.blocking, g.manual, g.blocking)
				}
			}
			for _, n := range jobNames(got) {
				if _, ok := want[n]; !ok {
					t.Errorf("%s: the pipeline must not create `%s`, which a merge request into dev does not; dev creates %v",
						where, n, jobNames(want))
				}
			}
			// The privileged live legs are never automatic here. live-prepare
			// is left to the comparison with dev above: on a pin change it
			// starts by itself there too, as the unprivileged fetch step.
			for _, n := range []string{"live", "live-fedora"} {
				if j, ok := got[n]; ok && !j.manual {
					t.Errorf("%s: `%s` must be absent or manual (when: manual) without the run-live-matrix label", where, n)
				}
			}
		}
	}
}

// A push to main offers only the release jobs.
func TestCIStages_APushToMainOffersOnlyTheReleaseButtons(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, changes := range []bool{false, true} {
		jobs := pipelineFor(t, doc, pushVars("main"), changes)
		if len(jobs) == 0 {
			t.Fatalf("a push to main must offer the release jobs (changes=%v); it creates none", changes)
		}
		for n := range jobs {
			if !strings.HasPrefix(n, "release:") {
				t.Errorf("a push to main must create only release:* jobs (changes=%v); it creates `%s`", changes, n)
			}
		}
	}
}

// Before a merge request opens, a push to a feature branch runs nothing; the
// merge request runs the same checks (answer 5a). With one open, the merge
// request pipeline runs and the branch pipeline stays out of its way.
func TestCIStages_APushToAFeatureBranchRunsNothing(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, branch := range []string{"feat/306-stages", "chore/306-ci-stages", "fix/x"} {
		for _, open := range []string{"", "ai/orbit-launcher!241"} {
			for _, changes := range []bool{false, true} {
				vars := pushVars(branch)
				vars["CI_OPEN_MERGE_REQUESTS"] = open
				if open == "" {
					delete(vars, "CI_OPEN_MERGE_REQUESTS")
				}
				jobs := pipelineFor(t, doc, vars, changes)
				if len(jobs) != 0 {
					t.Errorf("a push to %s (open merge requests %q, changes=%v) must create no jobs; it creates %v",
						branch, open, changes, jobNames(jobs))
				}
			}
		}
	}
}

// Renovate's schedule keeps working.
func TestCIStages_TheRenovateScheduleStillRunsRenovate(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	vars := pushVars("dev")
	vars["CI_PIPELINE_SOURCE"] = "schedule"
	vars["RENOVATE"] = "true"
	jobs := pipelineFor(t, doc, vars, false)
	assertPresent(t, jobs, "renovate")
	assertAbsent(t, jobs, "release:patch", "release:minor", "release:gitlab:patch", "release:gitlab:minor")
	if j := jobs["renovate"]; j.manual {
		t.Error("`renovate` on its schedule must start by itself")
	}
}

// A pipeline started by hand with RUN_LIVE=true runs the live installs
// without a person starting each job.
func TestCIStages_AHandStartedRunLivePipelineRunsTheLiveInstalls(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	vars := pushVars("dev")
	vars["CI_PIPELINE_SOURCE"] = "web"
	vars["RUN_LIVE"] = "true"
	jobs := pipelineFor(t, doc, vars, false)
	assertPresent(t, jobs, liveFamily...)
	for _, n := range liveFamily {
		if j := jobs[n]; j.manual {
			t.Errorf("`%s` must run by itself on a hand-started RUN_LIVE=true pipeline", n)
		}
	}
}
