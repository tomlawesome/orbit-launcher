// Package readiness is the release-readiness check (#359, after
// ai/orbit#1860): the checks a promotion (dev -> preview, preview -> main)
// must pass before its merge request is opened. Everything outside the
// repository text -- git, the remote, the GitLab API, the Orbit mirror --
// comes through Lookups, so the checks are tested without any of it.
package readiness

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Result is one check's outcome.
type Result struct {
	ID      string
	OK      bool
	Message string
}

// Report is every check's result for one target, in the order they ran.
type Report struct {
	Target  string
	Results []Result
}

// Ready reports whether every check passed.
func (r Report) Ready() bool {
	if len(r.Results) == 0 {
		return false
	}
	for _, res := range r.Results {
		if !res.OK {
			return false
		}
	}
	return true
}

// Render is the text the command prints: ok/FAIL per check, then the verdict.
func (r Report) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Release readiness for a promotion into %s:\n", r.Target)
	var failed []string
	for _, res := range r.Results {
		mark := "ok  "
		if !res.OK {
			mark = "FAIL"
			failed = append(failed, res.ID)
		}
		fmt.Fprintf(&b, "  %s %s: %s\n", mark, res.ID, redact(res.Message))
	}
	if len(failed) == 0 && len(r.Results) > 0 {
		b.WriteString("Ready: every check passed.\n")
	} else {
		fmt.Fprintf(&b, "Not ready: %d of %d checks failed (%s). Fix each and run again; do not open the promotion until this passes.\n",
			len(failed), len(r.Results), strings.Join(failed, ", "))
	}
	return b.String()
}

// Commit is a non-merge commit in target..source and the files it touches.
type Commit struct {
	SHA   string
	Files []string
}

// Signoff is the verdict on a release record's sign-off.
type Signoff struct {
	Signed bool
	Why    string
	// InFile says the fault is in the record file's own Signed-off-by
	// line, so the fix is in the file, not in the commit's trailer.
	InFile bool
}

// Lookups is everything the checks read from outside the repository text.
// "Source" is the branch being promoted (the checkout's HEAD), "target" the
// branch it merges into.
type Lookups interface {
	// Tags lists the release tags (vX.Y.Z and any others).
	Tags() ([]string, error)
	// RemoteTagExists reports whether the remote holds the tag.
	RemoteTagExists(tag string) (bool, error)
	// SourceFile reads a file from the source branch.
	SourceFile(path string) (content string, found bool, err error)
	// GuideBlob is the git blob id of docs/releasing.md on the source branch.
	GuideBlob() (string, error)
	// RecordSignoff judges the sign-off of the record at path.
	RecordSignoff(path string) (Signoff, error)
	// ContainsTarget reports whether the target's tip is an ancestor of the source's.
	ContainsTarget() (bool, error)
	// MainTreeEqualsPreview reports whether merging the source into main
	// would leave exactly the source's tree.
	MainTreeEqualsPreview() (bool, error)
	// UncoveredCommits lists the non-merge commits in target..source that no
	// audit record's Audited-through covers.
	UncoveredCommits() ([]Commit, error)
	// AuditIssues lists the issues of the audit records whose Audited-through
	// is in target..source.
	AuditIssues() ([]int, error)
	// IssueState is a GitLab issue's state ("opened", "closed").
	IssueState(n int) (string, error)
	// OrbitPin is the commit orbit/pin.json names on the source branch.
	OrbitPin() (string, error)
	// OrbitBranchTip is the branch orbit/pin.json names and ai/orbit's tip of it.
	OrbitBranchTip() (branch, commit string, err error)
}

const (
	changelogPath = "CHANGELOG.md"
	guidePath     = "docs/releasing.md"
)

// Run runs every check for target ("preview" or "main"). Every check runs;
// a lookup error fails only its own check. repoDir is the checkout the
// lookups read; the checks themselves read only through l.
func Run(target, repoDir string, l Lookups) Report {
	_ = repoDir
	c := &checker{l: l, target: target, source: "dev"}
	if target == "main" {
		c.source = "preview"
	}
	c.readChangelog()

	var checks []struct {
		id string
		fn func() (bool, string)
	}
	add := func(id string, fn func() (bool, string)) {
		checks = append(checks, struct {
			id string
			fn func() (bool, string)
		}{id, fn})
	}
	switch target {
	case "preview":
		add("version", c.version)
		add("tag-free", c.tagFree)
		add("changelog", c.changelog)
		add("contains-target", c.containsTarget)
		add("audit", c.audit)
		add("orbit-pin", c.orbitPin)
		add("release-record", c.releaseRecord)
	case "main":
		add("tag-free", c.tagFree)
		add("changelog", c.changelog)
		add("audit", c.audit)
		add("main-tree-equals-preview", c.mainTree)
		add("release-record", c.releaseRecord)
		add("button", c.button)
	}

	rep := Report{Target: target}
	for _, ch := range checks {
		ok, msg := safely(ch.fn)
		rep.Results = append(rep.Results, Result{ID: ch.id, OK: ok, Message: redact(msg)})
	}
	if len(rep.Results) == 0 {
		rep.Results = append(rep.Results, Result{ID: "target", Message: fmt.Sprintf("unknown target %q: want preview or main", target)})
	}
	return rep
}

// safely runs one check; a panic fails that check instead of the run.
func safely(fn func() (bool, string)) (ok bool, msg string) {
	defer func() {
		if r := recover(); r != nil {
			ok, msg = false, fmt.Sprintf("could not check: internal error: %v", r)
		}
	}()
	return fn()
}

type checker struct {
	l              Lookups
	target, source string

	clVersion  string   // the CHANGELOG's newest release version, "" if none
	clProblems []string // CheckChangelog's problems
	clErr      error    // reading CHANGELOG.md failed
	clFound    bool
}

func (c *checker) readChangelog() {
	src, found, err := c.l.SourceFile(changelogPath)
	c.clErr, c.clFound = err, found
	if err != nil || !found {
		return
	}
	c.clVersion, c.clProblems = CheckChangelog(src)
}

func couldNot(err error) string { return "could not check: " + err.Error() }

// needVersion is the message of a check that cannot run without a version.
func (c *checker) needVersion() string {
	return "fix the version check first: CHANGELOG.md on " + c.source + " names no release version"
}

// next is the next patch and next minor version after the highest tag, as
// tools/calculateversion computes them (-hotfix and plain); 0.1.0 for both
// before the first stable tag.
func next(tags []string) (patch, minor string) {
	var best [3]int
	found := false
	for _, t := range tags {
		m := stableTag.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		var v [3]int
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+1])
		}
		if !found || less(best, v) {
			best, found = v, true
		}
	}
	if !found {
		return "0.1.0", "0.1.0"
	}
	return fmt.Sprintf("%d.%d.%d", best[0], best[1], best[2]+1), fmt.Sprintf("%d.%d.0", best[0], best[1]+1)
}

var stableTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (c *checker) version() (bool, string) {
	if c.clErr != nil {
		return false, couldNot(c.clErr)
	}
	tags, err := c.l.Tags()
	if err != nil {
		return false, couldNot(err)
	}
	p, m := next(tags)
	if c.clVersion != "" && (c.clVersion == p || c.clVersion == m) {
		kind := "next patch"
		if c.clVersion == m && c.clVersion != p {
			kind = "next minor"
		}
		return true, c.clVersion + " is the " + kind
	}
	got := "nothing"
	if c.clVersion != "" {
		got = c.clVersion
	}
	return false, fmt.Sprintf("CHANGELOG.md's newest release heading is %s; it must be the next patch %s or next minor %s: rename `## [Unreleased]` to `## [%s] - YYYY-MM-DD` on a release/v%s branch into dev (git fetch --tags first)", got, p, m, p, p)
}

func (c *checker) tagFree() (bool, string) {
	if c.clErr != nil {
		return false, couldNot(c.clErr)
	}
	if c.clVersion == "" {
		return false, c.needVersion()
	}
	tag := "v" + c.clVersion
	exists, err := c.l.RemoteTagExists(tag)
	if err != nil {
		return false, couldNot(err)
	}
	if exists {
		return false, "the tag " + tag + " already exists: this version shipped. Fetch the tags and pick the next version"
	}
	return true, "no tag " + tag + " on the remote yet"
}

func (c *checker) changelog() (bool, string) {
	if c.clErr != nil {
		return false, couldNot(c.clErr)
	}
	if !c.clFound {
		return false, "CHANGELOG.md is missing on " + c.source
	}
	if len(c.clProblems) > 0 {
		return false, strings.Join(c.clProblems, "; ")
	}
	return true, "the " + c.clVersion + " section has entries and Unreleased is empty"
}

func (c *checker) containsTarget() (bool, string) {
	ok, err := c.l.ContainsTarget()
	if err != nil {
		return false, couldNot(err)
	}
	if !ok {
		return false, fmt.Sprintf("%s does not contain %s, so the semi-linear merge would be refused: merge the open %s -> %s back-merge first", c.source, c.target, c.target, c.source)
	}
	return true, c.source + " contains " + c.target
}

func (c *checker) mainTree() (bool, string) {
	ok, err := c.l.MainTreeEqualsPreview()
	if err != nil {
		return false, couldNot(err)
	}
	if !ok {
		return false, "main is not contained in preview (its last promotion merge commit is missing), so the merge would be refused or would change files: merge the main -> preview back-merge first"
	}
	return true, "main gets preview's exact tree"
}

// releaseOnly reports whether a path is one a release may change without an
// audit: the records, the changelog and the audit records themselves.
func releaseOnly(path string) bool {
	return strings.HasPrefix(path, "docs/releases/") || path == changelogPath || strings.HasPrefix(path, ".agents/reviews/")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (c *checker) audit() (bool, string) {
	var problems []string
	commits, err := c.l.UncoveredCommits()
	if err != nil {
		problems = append(problems, couldNot(err))
	} else {
		var bad []string
		for _, cm := range commits {
			only := len(cm.Files) > 0
			for _, f := range cm.Files {
				if !releaseOnly(f) {
					only = false
					break
				}
			}
			if !only {
				bad = append(bad, short(cm.SHA))
			}
		}
		if len(bad) > 0 {
			problems = append(problems, fmt.Sprintf("commits %s are in no audit's Audited-through range: audit them (batch audit) before promoting", strings.Join(bad, ", ")))
		}
	}
	issues, err := c.l.AuditIssues()
	if err != nil {
		problems = append(problems, couldNot(err))
	} else {
		sort.Ints(issues)
		for _, n := range issues {
			state, err := c.l.IssueState(n)
			switch {
			case err != nil:
				problems = append(problems, couldNot(err))
			case state != "closed":
				problems = append(problems, fmt.Sprintf("audit issue #%d is still open: close it with its evidence first", n))
			}
		}
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; ")
	}
	return true, fmt.Sprintf("every commit in %s..%s is audited or release-only", c.target, c.source)
}

func (c *checker) orbitPin() (bool, string) {
	pin, err := c.l.OrbitPin()
	if err != nil {
		return false, couldNot(err)
	}
	branch, tip, err := c.l.OrbitBranchTip()
	if err != nil {
		return false, couldNot(err)
	}
	if !strings.EqualFold(pin, tip) || pin == "" {
		return false, fmt.Sprintf("orbit/pin.json names %s, but ai/orbit's %s branch is at %s: run scripts/bump-orbit-pin.sh %s on dev, or wait for that build", short(pin), branch, short(tip), branch)
	}
	return true, "orbit/pin.json names ai/orbit's " + branch + " tip " + short(tip)
}

func (c *checker) releaseRecord() (bool, string) {
	if c.clErr != nil {
		return false, couldNot(c.clErr)
	}
	if c.clVersion == "" {
		return false, c.needVersion()
	}
	path := "docs/releases/v" + c.clVersion + ".md"
	src, found, err := c.l.SourceFile(path)
	if err != nil {
		return false, couldNot(err)
	}
	if !found {
		return false, path + " is missing: read docs/releasing.md, copy docs/releases/TEMPLATE.md, fill one line per step and commit it with git commit --trailer \"Signed-off-by: <your name>\""
	}
	rec := ParseRecord(src)
	var problems []string

	if strings.TrimPrefix(rec.Version, "v") != c.clVersion {
		problems = append(problems, fmt.Sprintf("its version line says %q, but CHANGELOG.md's version is %s", rec.Version, c.clVersion))
	}

	if so, err := c.l.RecordSignoff(path); err != nil {
		problems = append(problems, "could not check the sign-off: "+err.Error())
	} else if !so.Signed {
		why := so.Why
		if why == "" {
			why = "the record is not signed"
		}
		// A fault in the file's own Signed-off-by line is fixed in the file;
		// the --trailer hint is only for a commit missing its trailer.
		hint := "commit it with git commit --trailer \"Signed-off-by: <your name>\""
		if so.InFile {
			hint = "fix the Signed-off-by line in " + path
		}
		problems = append(problems, "not signed: "+why+" ("+hint+")")
	}

	if blob, err := c.l.GuideBlob(); err != nil {
		problems = append(problems, "could not check the guide blob: "+err.Error())
	} else if rec.GuideBlob != blob {
		problems = append(problems, fmt.Sprintf("guide changed; re-read docs/releasing.md and regenerate the record (its guide-blob is %q, the guide's is %s)", rec.GuideBlob, blob))
	}

	problems = append(problems, rec.Problems...)
	guide, found, err := c.l.SourceFile(guidePath)
	switch {
	case err != nil:
		problems = append(problems, "could not check the steps: "+err.Error())
	case !found:
		problems = append(problems, "could not check the steps: docs/releasing.md is missing")
	default:
		steps, err := ParseGuideSteps(guide)
		if err != nil {
			problems = append(problems, "could not check the steps: "+err.Error())
		} else {
			problems = append(problems, stepProblems(rec.Steps, len(steps))...)
		}
	}

	if len(problems) > 0 {
		return false, path + ": " + strings.Join(problems, "; ")
	}
	return true, path + " is complete and signed"
}

// stepProblems compares the counted step lines with the guide's n steps:
// one each, none extra, none repeated, in order.
func stepProblems(steps []Step, n int) []string {
	var out []string
	seen := map[int]bool{}
	last := 0
	ordered := true
	for _, s := range steps {
		switch {
		case s.N < 1 || s.N > n:
			out = append(out, fmt.Sprintf("step %d is not a step of the guide (it has %d)", s.N, n))
			continue
		case seen[s.N]:
			out = append(out, fmt.Sprintf("step %d is listed more than once", s.N))
			continue
		}
		seen[s.N] = true
		if s.N < last {
			ordered = false
		}
		last = s.N
	}
	if !ordered {
		out = append(out, fmt.Sprintf("the steps are out of order: list 1 to %d in order", n))
	}
	for k := 1; k <= n; k++ {
		if !seen[k] {
			out = append(out, fmt.Sprintf("step %d is left out: give it done: <link or job id>, not-needed: <owner decision> or after-merge", k))
		}
	}
	return out
}

// button names the release button to press on main after the merge. It
// fails when neither button gives the CHANGELOG version (tags moved since
// the preview promotion, for example by a hotfix): the release job would
// then find no CHANGELOG section and stop after main took the merge.
func (c *checker) button() (bool, string) {
	if c.clVersion == "" {
		return true, "cannot tell which release button to press until CHANGELOG.md names the version"
	}
	tags, err := c.l.Tags()
	if err != nil {
		return true, "cannot tell which release button to press: " + err.Error()
	}
	p, m := next(tags)
	switch c.clVersion {
	case p:
		return true, "after the merge press release:patch (" + p + ")"
	case m:
		return true, "after the merge press release:minor (" + m + ")"
	}
	return false, fmt.Sprintf("neither button gives %s (release:patch gives %s, release:minor %s): add a CHANGELOG section for one of them on preview first", c.clVersion, p, m)
}

// secretName is an environment variable whose value must never be printed.
var secretName = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|PRIVATE|CREDENTIAL|API_KEY|_KEY$)`)

// redact replaces the value of every credential-like environment variable
// in msg, so no lookup error can print one.
func redact(msg string) string {
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || len(value) < 6 || !secretName.MatchString(name) {
			continue
		}
		msg = strings.ReplaceAll(msg, value, "[redacted]")
	}
	return msg
}
