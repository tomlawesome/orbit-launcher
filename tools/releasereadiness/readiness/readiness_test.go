package readiness

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// stub is a Lookups holding a repository in the ready state: the highest tag
// is v0.4.6, the CHANGELOG names 0.4.7, the record is complete and signed.
type stub struct {
	tags        []string
	remoteTags  map[string]bool
	files       map[string]string
	guideBlob   string
	signoff     Signoff
	contains    bool
	mainTree    bool
	uncovered   []Commit
	auditIssues []int
	issueState  map[int]string
	pin         string
	tipBranch   string
	tip         string
	errs        map[string]error // method name -> error
}

const (
	goodChangelog = "# Changelog\n\n## [Unreleased]\n\n## [0.4.7] - 2026-11-20\n\n### Fixed\n\n- A fix. (#354)\n\n## [0.4.6] - 2026-10-01\n\n### Added\n\n- Old.\n"
	goodGuide     = "# Releasing\n\n## What you do, in short\n\n1. Open dev -> preview.\n2. Manual test.\n3. Open preview -> main.\n\n## Cutting a tag\n"
	recordPath    = "docs/releases/v0.4.7.md"
)

func goodRecord() string {
	return strings.Join([]string{
		"version: v0.4.7",
		"guide-blob: blob111",
		"guide-commit: c0ffee",
		"step 1: done: https://gitlab.example/mr/1",
		"step 2: not-needed: owner decided",
		"step 3: after-merge",
		"Signed-off-by: Ada Tester",
	}, "\n") + "\n"
}

func newStub() *stub {
	return &stub{
		tags:       []string{"v0.4.5", "v0.4.6"},
		remoteTags: map[string]bool{},
		files: map[string]string{
			"CHANGELOG.md":      goodChangelog,
			"docs/releasing.md": goodGuide,
			recordPath:          goodRecord(),
		},
		guideBlob:   "blob111",
		signoff:     Signoff{Signed: true},
		contains:    true,
		mainTree:    true,
		auditIssues: []int{362},
		issueState:  map[int]string{362: "closed"},
		pin:         "aaaaaaa1",
		tipBranch:   "preview",
		tip:         "aaaaaaa1",
		errs:        map[string]error{},
	}
}

func (s *stub) Tags() ([]string, error) { return s.tags, s.errs["Tags"] }
func (s *stub) RemoteTagExists(tag string) (bool, error) {
	return s.remoteTags[tag], s.errs["RemoteTagExists"]
}
func (s *stub) SourceFile(p string) (string, bool, error) {
	if e := s.errs["SourceFile:"+p]; e != nil {
		return "", false, e
	}
	c, ok := s.files[p]
	return c, ok, nil
}
func (s *stub) GuideBlob() (string, error) { return s.guideBlob, s.errs["GuideBlob"] }
func (s *stub) RecordSignoff(string) (Signoff, error) {
	return s.signoff, s.errs["RecordSignoff"]
}
func (s *stub) ContainsTarget() (bool, error) { return s.contains, s.errs["ContainsTarget"] }
func (s *stub) MainTreeEqualsPreview() (bool, error) {
	return s.mainTree, s.errs["MainTreeEqualsPreview"]
}
func (s *stub) UncoveredCommits() ([]Commit, error) { return s.uncovered, s.errs["UncoveredCommits"] }
func (s *stub) AuditIssues() ([]int, error)         { return s.auditIssues, s.errs["AuditIssues"] }
func (s *stub) IssueState(n int) (string, error)    { return s.issueState[n], s.errs["IssueState"] }
func (s *stub) OrbitPin() (string, error)           { return s.pin, s.errs["OrbitPin"] }
func (s *stub) OrbitBranchTip() (string, string, error) {
	return s.tipBranch, s.tip, s.errs["OrbitBranchTip"]
}

func run(t *testing.T, target string, s *stub) Report {
	t.Helper()
	var rep Report
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Run panicked: %v", r)
			}
		}()
		rep = Run(target, t.TempDir(), s)
	}()
	return rep
}

func ids(rep Report) []string {
	var out []string
	for _, r := range rep.Results {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

func failedIDs(rep Report) []string {
	var out []string
	for _, r := range rep.Results {
		if !r.OK {
			out = append(out, r.ID)
		}
	}
	sort.Strings(out)
	return out
}

func get(t *testing.T, rep Report, id string) Result {
	t.Helper()
	for _, r := range rep.Results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no check %q in the report (checks: %v)", id, ids(rep))
	return Result{}
}

func wantFail(t *testing.T, rep Report, id string, substrs ...string) {
	t.Helper()
	r := get(t, rep, id)
	if r.OK {
		t.Errorf("%s: want FAIL, got ok", id)
		return
	}
	for _, s := range substrs {
		if !strings.Contains(r.Message, s) {
			t.Errorf("%s: message %q lacks %q", id, r.Message, s)
		}
	}
}

func wantOK(t *testing.T, rep Report, id string) {
	t.Helper()
	if r := get(t, rep, id); !r.OK {
		t.Errorf("%s: want ok, got FAIL: %s", id, r.Message)
	}
}

var (
	previewChecks = []string{"audit", "changelog", "contains-target", "orbit-pin", "release-record", "tag-free", "version"}
	mainChecks    = []string{"audit", "button", "changelog", "main-tree-equals-preview", "release-record", "tag-free"}
)

func TestRun_EachLegRunsExactlyItsChecksAndAllPassWhenReady(t *testing.T) {
	for target, want := range map[string][]string{"preview": previewChecks, "main": mainChecks} {
		rep := run(t, target, newStub())
		if got := ids(rep); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s runs %v, want %v", target, got, want)
		}
		if f := failedIDs(rep); len(f) != 0 {
			for _, id := range f {
				t.Errorf("%s: %s failed when ready: %s", target, id, get(t, rep, id).Message)
			}
		}
		if !rep.Ready() {
			t.Errorf("%s: Ready() = false with every check passing", target)
		}
		if out := rep.Render(); !strings.Contains(out, "Ready: every check passed.") {
			t.Errorf("%s: report lacks the Ready line:\n%s", target, out)
		}
	}
}

func breakEverything() *stub {
	s := newStub()
	s.files["CHANGELOG.md"] = "# Changelog\n\n## [Unreleased]\n"
	s.contains = false
	s.mainTree = false
	s.uncovered = []Commit{{SHA: "1234567890abcdef", Files: []string{"main.go"}}}
	s.orbitWrong()
	delete(s.files, recordPath)
	return s
}

func (s *stub) orbitWrong() { s.pin, s.tip = "1111111", "2222222" }

func TestRun_EveryCheckRunsWhenTheFirstFailsAndTheReportListsAllFailures(t *testing.T) {
	for target, all := range map[string][]string{"preview": previewChecks, "main": mainChecks} {
		rep := run(t, target, breakEverything())
		if got := ids(rep); fmt.Sprint(got) != fmt.Sprint(all) {
			t.Errorf("%s: ran %v, want all of %v", target, got, all)
		}
		if rep.Ready() {
			t.Errorf("%s: Ready() with failures", target)
		}
		failed := failedIDs(rep)
		if len(failed) < 4 {
			t.Errorf("%s: only %v failed; the broken repository fails at least four checks", target, failed)
		}
		out := rep.Render()
		for _, id := range failed {
			if !strings.Contains(out, id) {
				t.Errorf("%s: report does not list failed check %s:\n%s", target, id, out)
			}
		}
		want := fmt.Sprintf("Not ready: %d of %d checks failed", len(failed), len(all))
		if !strings.Contains(out, want) {
			t.Errorf("%s: report lacks %q:\n%s", target, want, out)
		}
		if !strings.Contains(out, "do not open the promotion until this passes") {
			t.Errorf("%s: report lacks the closing instruction:\n%s", target, out)
		}
		if !strings.Contains(out, "FAIL") {
			t.Errorf("%s: report has no FAIL line:\n%s", target, out)
		}
		for _, r := range rep.Results {
			if !r.OK && strings.TrimSpace(r.Message) == "" {
				t.Errorf("%s: failed check %s has an empty message", target, r.ID)
			}
		}
	}
}

func TestRun_AnyOneFailureMakesTheReportNotReady(t *testing.T) {
	cases := map[string]func(*stub){
		"tag-free":        func(s *stub) { s.remoteTags["v0.4.7"] = true },
		"contains-target": func(s *stub) { s.contains = false },
		"orbit-pin":       func(s *stub) { s.pin = "9999999" },
	}
	for id, mutate := range cases {
		s := newStub()
		mutate(s)
		rep := run(t, "preview", s)
		if rep.Ready() {
			t.Errorf("%s failing: Ready() is true", id)
		}
		if got := failedIDs(rep); fmt.Sprint(got) != fmt.Sprint([]string{id}) {
			t.Errorf("%s failing: failed checks %v, want only that one", id, got)
		}
	}
}

func TestRun_ALookupErrorFailsOnlyItsOwnCheckAndNamesTheError(t *testing.T) {
	cases := []struct {
		target, method, check string
	}{
		{"preview", "Tags", "version"},
		{"preview", "RemoteTagExists", "tag-free"},
		{"preview", "ContainsTarget", "contains-target"},
		{"preview", "UncoveredCommits", "audit"},
		{"preview", "AuditIssues", "audit"},
		{"preview", "OrbitPin", "orbit-pin"},
		{"preview", "OrbitBranchTip", "orbit-pin"},
		{"preview", "RecordSignoff", "release-record"},
		{"preview", "GuideBlob", "release-record"},
		{"main", "MainTreeEqualsPreview", "main-tree-equals-preview"},
		{"main", "RemoteTagExists", "tag-free"},
		{"main", "UncoveredCommits", "audit"},
	}
	for _, c := range cases {
		s := newStub()
		s.errs[c.method] = errors.New("boom-" + c.method)
		rep := run(t, c.target, s)
		if got := failedIDs(rep); fmt.Sprint(got) != fmt.Sprint([]string{c.check}) {
			t.Errorf("%s %s error: failed %v, want only %s", c.target, c.method, got, c.check)
			continue
		}
		wantFail(t, rep, c.check, "could not check", "boom-"+c.method)
	}
	// An error from IssueState fails the audit check only.
	s := newStub()
	s.errs["IssueState"] = errors.New("boom-IssueState")
	rep := run(t, "preview", s)
	if got := failedIDs(rep); fmt.Sprint(got) != fmt.Sprint([]string{"audit"}) {
		t.Errorf("IssueState error: failed %v, want only audit", got)
	}
	wantFail(t, rep, "audit", "boom-IssueState")
}

func TestVersion_FailsWhenTheChangelogVersionIsNeitherNextPatchNorNextMinor(t *testing.T) {
	for _, v := range []string{"0.4.9", "0.4.6", "0.6.0", "1.0.0", "0.4.8"} {
		s := newStub()
		s.files["CHANGELOG.md"] = strings.Replace(goodChangelog, "0.4.7", v, 1)
		rep := run(t, "preview", s)
		wantFail(t, rep, "version", v, "0.4.7", "0.5.0", "release/v0.4.7")
	}
}

func TestVersion_FailsWhenThereIsNoReleaseHeading(t *testing.T) {
	s := newStub()
	s.files["CHANGELOG.md"] = "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- x\n"
	rep := run(t, "preview", s)
	wantFail(t, rep, "version", "nothing")
}

func TestVersion_PassesForTheNextPatchAndForTheNextMinor(t *testing.T) {
	for _, v := range []string{"0.4.7", "0.5.0"} {
		s := newStub()
		s.files["CHANGELOG.md"] = strings.Replace(goodChangelog, "0.4.7", v, 1)
		s.files["docs/releases/v"+v+".md"] = strings.Replace(goodRecord(), "v0.4.7", "v"+v, 1)
		rep := run(t, "preview", s)
		wantOK(t, rep, "version")
	}
}

func TestVersion_ChecksThatNeedTheVersionSayToFixItFirstWhenItIsMissing(t *testing.T) {
	s := newStub()
	s.files["CHANGELOG.md"] = "# Changelog\n\n## [Unreleased]\n"
	rep := run(t, "preview", s)
	wantFail(t, rep, "tag-free", "fix the version check first")
	wantFail(t, rep, "release-record", "fix the version check first")
	rep = run(t, "main", s)
	wantFail(t, rep, "tag-free", "fix the version check first")
	wantFail(t, rep, "release-record", "fix the version check first")
}

func TestTagFree_FailsWhenTheTagExistsOnBothLegs(t *testing.T) {
	for _, target := range []string{"preview", "main"} {
		s := newStub()
		s.remoteTags["v0.4.7"] = true
		rep := run(t, target, s)
		wantFail(t, rep, "tag-free", "v0.4.7", "already exists")
	}
}

func TestContainsTarget_FailsWithTheBackMergeInstruction(t *testing.T) {
	s := newStub()
	s.contains = false
	wantFail(t, run(t, "preview", s), "contains-target", "does not contain", "back-merge")
}

func TestMainTree_FailsWithTheBackMergeInstruction(t *testing.T) {
	s := newStub()
	s.mainTree = false
	wantFail(t, run(t, "main", s), "main-tree-equals-preview", "main is not contained in preview", "back-merge")
}

func TestAudit_FailsNamingUncoveredCommits(t *testing.T) {
	for _, target := range []string{"preview", "main"} {
		s := newStub()
		s.uncovered = []Commit{
			{SHA: "abcdef0123456789", Files: []string{"internal/x.go"}},
			{SHA: "fedcba9876543210", Files: []string{"docs/releases/v0.4.7.md", "main.go"}},
		}
		wantFail(t, run(t, target, s), "audit", "abcdef0", "fedcba9", "Audited-through")
	}
}

func TestAudit_PassesWhenTheOnlyUncoveredCommitsTouchReleaseFiles(t *testing.T) {
	s := newStub()
	s.uncovered = []Commit{
		{SHA: "aaaaaaa000", Files: []string{"docs/releases/v0.4.7.md", "docs/releases/TEMPLATE.md"}},
		{SHA: "bbbbbbb000", Files: []string{"CHANGELOG.md"}},
		{SHA: "ccccccc000", Files: []string{".agents/reviews/batch-3.md"}},
		{SHA: "ddddddd000", Files: []string{"CHANGELOG.md", "docs/releases/v0.4.7.md", ".agents/reviews/x.md"}},
	}
	wantOK(t, run(t, "preview", s), "audit")
}

func TestAudit_AMixedCommitIsStillUncovered(t *testing.T) {
	s := newStub()
	s.uncovered = []Commit{{SHA: "eeeeeee000", Files: []string{"CHANGELOG.md", "internal/x.go"}}}
	wantFail(t, run(t, "preview", s), "audit", "eeeeeee")
}

func TestAudit_FailsNamingAnOpenAuditIssue(t *testing.T) {
	s := newStub()
	s.issueState[362] = "opened"
	wantFail(t, run(t, "preview", s), "audit", "#362", "still open")
	s.issueState[362] = "open"
	wantFail(t, run(t, "main", s), "audit", "#362", "still open")
}

func TestOrbitPin_FailsNamingBothCommitsAndTheBumpCommand(t *testing.T) {
	s := newStub()
	s.pin, s.tipBranch, s.tip = "1111111aaaa", "preview", "2222222bbbb"
	wantFail(t, run(t, "preview", s), "orbit-pin", "1111111", "2222222", "scripts/bump-orbit-pin.sh preview")
}

func TestOrbitPin_PassesWhenPinIsTheBranchTip(t *testing.T) {
	wantOK(t, run(t, "preview", newStub()), "orbit-pin")
}

func TestReleaseRecord_Missing(t *testing.T) {
	for _, target := range []string{"preview", "main"} {
		s := newStub()
		delete(s.files, recordPath)
		wantFail(t, run(t, target, s), "release-record", "docs/releases/v0.4.7.md is missing", "docs/releasing.md", "TEMPLATE.md", "Signed-off-by")
	}
}

func TestReleaseRecord_Unsigned(t *testing.T) {
	s := newStub()
	s.signoff = Signoff{Signed: false, Why: "the Signed-off-by line is an email address"}
	wantFail(t, run(t, "preview", s), "release-record", "email address")
	// An unsigned verdict with no reason still fails, and says something.
	s.signoff = Signoff{Signed: false}
	wantFail(t, run(t, "preview", s), "release-record", "sign")
}

func TestReleaseRecord_AnOlderGuideBlob(t *testing.T) {
	s := newStub()
	s.guideBlob = "blob222"
	wantFail(t, run(t, "preview", s), "release-record", "guide changed")
}

func TestReleaseRecord_StepLeftOut(t *testing.T) {
	s := newStub()
	s.files[recordPath] = strings.Replace(goodRecord(), "step 2: not-needed: owner decided\n", "", 1)
	wantFail(t, run(t, "preview", s), "release-record", "step 2")
}

func TestReleaseRecord_StepWithPlaceholderEvidenceCountsAsLeftOut(t *testing.T) {
	s := newStub()
	s.files[recordPath] = strings.Replace(goodRecord(), "done: https://gitlab.example/mr/1", "done: <link or job id>", 1)
	wantFail(t, run(t, "preview", s), "release-record", "step 1")
}

func TestReleaseRecord_ExtraStep(t *testing.T) {
	s := newStub()
	s.files[recordPath] = strings.Replace(goodRecord(), "Signed-off-by", "step 4: after-merge\nSigned-off-by", 1)
	wantFail(t, run(t, "preview", s), "release-record", "step 4")
}

func TestReleaseRecord_RepeatedStep(t *testing.T) {
	s := newStub()
	s.files[recordPath] = strings.Replace(goodRecord(), "step 3: after-merge\n", "step 3: after-merge\nstep 3: after-merge\n", 1)
	wantFail(t, run(t, "preview", s), "release-record", "step 3")
}

func TestReleaseRecord_OutOfOrder(t *testing.T) {
	s := newStub()
	r := goodRecord()
	r = strings.Replace(r, "step 1: done: https://gitlab.example/mr/1\nstep 2: not-needed: owner decided\n", "step 2: not-needed: owner decided\nstep 1: done: https://gitlab.example/mr/1\n", 1)
	s.files[recordPath] = r
	wantFail(t, run(t, "preview", s), "release-record", "order")
}

func TestReleaseRecord_VersionLineDiffers(t *testing.T) {
	s := newStub()
	s.files[recordPath] = strings.Replace(goodRecord(), "version: v0.4.7", "version: v0.4.8", 1)
	wantFail(t, run(t, "preview", s), "release-record", "v0.4.8", "0.4.7")
}

func TestReleaseRecord_IsCheckedOnMainToo(t *testing.T) {
	s := newStub()
	s.guideBlob = "blob222"
	wantFail(t, run(t, "main", s), "release-record", "guide changed")
	s = newStub()
	delete(s.files, recordPath)
	wantFail(t, run(t, "main", s), "release-record", "missing")
}

func TestButton_NamesPatchForAPatchVersionAndMinorForAMinorVersion(t *testing.T) {
	rep := run(t, "main", newStub())
	r := get(t, rep, "button")
	if !r.OK || !strings.Contains(r.Message, "release:patch") {
		t.Errorf("0.4.7 after v0.4.6: ok=%v message %q, want release:patch", r.OK, r.Message)
	}
	s := newStub()
	s.files["CHANGELOG.md"] = strings.Replace(goodChangelog, "0.4.7", "0.5.0", 1)
	s.files["docs/releases/v0.5.0.md"] = strings.Replace(goodRecord(), "v0.4.7", "v0.5.0", 1)
	r = get(t, run(t, "main", s), "button")
	if !r.OK || !strings.Contains(r.Message, "release:minor") || strings.Contains(r.Message, "release:patch") {
		t.Errorf("0.5.0 after v0.4.6: ok=%v message %q, want release:minor only", r.OK, r.Message)
	}
}

func TestButton_FailsWhenTheChangelogVersionIsNeitherWhatPatchNorMinorWouldGive(t *testing.T) {
	s := newStub()
	s.tags = []string{"v0.4.6", "v0.4.7"}
	// CHANGELOG's newest heading is 0.4.7, which is already tagged; the
	// buttons would give 0.4.8 (patch) or 0.5.0 (minor).
	wantFail(t, run(t, "main", s), "button", "0.4.8", "0.5.0")
}

func TestNoMessageContainsATokenValueFromTheEnvironment(t *testing.T) {
	const secret = "placeholder-secret-one"
	const other = "placeholder-secret-two"
	t.Setenv("GITLAB_TOKEN", secret)
	t.Setenv("CI_JOB_TOKEN", other)
	s := newStub()
	s.errs["RemoteTagExists"] = errors.New("ls-remote failed with " + secret)
	s.errs["IssueState"] = errors.New("GET issue: " + other + " rejected")
	s.errs["OrbitBranchTip"] = errors.New("fetch " + secret + " and " + other)
	s.files["CHANGELOG.md"] = goodChangelog
	rep := run(t, "preview", s)
	wantFail(t, rep, "tag-free", "could not check")
	wantFail(t, rep, "audit", "could not check")
	wantFail(t, rep, "orbit-pin", "could not check")
	for _, r := range rep.Results {
		for _, v := range []string{secret, other} {
			if strings.Contains(r.Message, v) {
				t.Errorf("%s message leaks a token value: %q", r.ID, r.Message)
			}
		}
	}
	out := rep.Render()
	for _, v := range []string{secret, other} {
		if strings.Contains(out, v) {
			t.Errorf("the rendered report leaks a token value:\n%s", out)
		}
	}
}
