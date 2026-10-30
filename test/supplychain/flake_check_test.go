// The shared flake check, adopted from ai/agent-infra (#310). The flake rule
// is only a rule while something reads docs/flakes.md: a copy of agent-infra's
// flake-check.sh sits at scripts/ci/flake-check.sh, and a blocking job in the
// cheapest stage, on the light lane, runs it on every pipeline that runs the
// checks (#306; owner, answer 3a) with the list
// of quarantined tests. These checks pin that adoption (agent-infra
// scripts/flake-check/README.md, "Adopt it in a project"). The sub half of a
// quarantined `TestX/sub` is checked statically against literals in TestX's
// own body; scripts/quarantine.txt's header states what that cannot see.
package supplychain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

const (
	flakeScript     = "scripts/ci/flake-check.sh"
	flakeSourcePath = "scripts/flake-check/flake-check.sh"
	flakeLog        = "docs/flakes.md"

	// SHA-256 of the source body (after the header) at the commit the header
	// names; refreshed with the copy, so a pipeline with no agent-infra
	// checkout still catches an edit.
	flakeSourceBodySHA256 = "6ea37ab97d38c151b3b5d3be611791219c0b1b553bb6c71852f965a26fffc77f"
)

// flakeCheckJob returns the name and effective form of the pipeline job that
// runs the flake check, found by what it runs rather than what it is called.
func flakeCheckJob(t *testing.T, doc *yamlDoc) (string, jobEffective) {
	t.Helper()
	for _, k := range doc.root.keys {
		switch k {
		case "stages", "workflow", "default", "variables", "include":
			continue
		}
		if strings.HasPrefix(k, ".") || doc.root.vals[k].kind != 'm' {
			continue
		}
		e, err := doc.effectiveJob(k)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(e.Script, "\n") {
			if l = strings.TrimSpace(l); !strings.HasPrefix(l, "#") && strings.Contains(l, flakeScript) {
				return k, e
			}
		}
	}
	t.Fatalf("no job in .gitlab-ci.yml runs %s (#310)", flakeScript)
	return "", jobEffective{}
}

// flakeCheckInvocation returns the job's script line that runs the check.
func flakeCheckInvocation(t *testing.T, doc *yamlDoc, job string) string {
	t.Helper()
	for _, l := range nonCommentScriptLines(t, doc, job) {
		if strings.Contains(l, flakeScript) {
			return l
		}
	}
	t.Fatalf("%s has no script line running %s", job, flakeScript)
	return ""
}

// leadingComment returns the script's header: its leading comment lines
// (shebang included), up to the first line that is code.
func leadingComment(src string) string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "#") || strings.TrimSpace(l) == "" {
			out = append(out, l)
			continue
		}
		break
	}
	return strings.Join(out, "\n")
}

// afterHeader returns the script from its first line of code on.
func afterHeader(src string) string {
	return strings.TrimPrefix(src, leadingComment(src))
}

// bodyHash is the hex SHA-256 of the script from its first line of code to
// the end, trailing newline included; the same bytes as
// `awk 'f||!/^#|^[[:space:]]*$/{f=1;print}' | sha256sum`.
func bodyHash(src string) string {
	sum := sha256.Sum256([]byte(strings.TrimPrefix(afterHeader(src), "\n")))
	return hex.EncodeToString(sum[:])
}

// agentInfraDir is the agent-infra checkout the copy is compared with:
// $AGENT_INFRA_DIR, else projects/agent-infra under the home directory.
func agentInfraDir() string {
	if d := os.Getenv("AGENT_INFRA_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "projects", "agent-infra")
}

var flakeCommitRE = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

// headerCommit returns the agent-infra commit the copy's header names: a
// 7-to-40 character hex word with at least one digit in it.
func headerCommit(header string) string {
	for _, m := range flakeCommitRE.FindAllString(header, -1) {
		if strings.ContainsAny(m, "0123456789") {
			return m
		}
	}
	return ""
}

func TestFlakeCheckScriptIsCommittedAndExecutable(t *testing.T) {
	p := filepath.Join(root, flakeScript)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("%s must exist: the copy of agent-infra's flake-check.sh (#310): %v", flakeScript, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("%s must be executable, mode is %v (#310)", flakeScript, info.Mode())
	}
	if body := readRepoFile(t, flakeScript); !strings.HasPrefix(body, "#!/bin/sh") {
		t.Errorf("%s must start with the #!/bin/sh line of the original (#310)", flakeScript)
	}
}

func TestFlakeCheckHeaderNamesItsSourceAndCommit(t *testing.T) {
	header := leadingComment(readRepoFile(t, flakeScript))
	if !strings.Contains(header, "agent-infra") {
		t.Errorf("the header must name agent-infra as the source (#310)")
	}
	if !strings.Contains(header, flakeSourcePath) {
		t.Errorf("the header must name the source path %s (#310)", flakeSourcePath)
	}
	if headerCommit(header) == "" {
		t.Errorf("the header must name the agent-infra commit the copy came from, a hex id of 7 to 40 characters (#310)")
	}
}

func TestFlakeCheckBodyIsAgentInfrasApartFromTheHeader(t *testing.T) {
	copyBody := readRepoFile(t, flakeScript)
	commit := headerCommit(leadingComment(copyBody))
	if commit == "" {
		t.Fatalf("%s names no agent-infra commit in its header (#310)", flakeScript)
	}
	repo := agentInfraDir()
	if _, err := os.Stat(filepath.Join(repo, ".git")); repo == "" || err != nil {
		t.Skip("no agent-infra checkout (set AGENT_INFRA_DIR); the pinned-hash test still checks the body, this one proves the pin is agent-infra's")
	}
	var out, stderr bytes.Buffer
	cmd := exec.Command("/usr/bin/git", "-C", repo, "show", commit+":"+flakeSourcePath)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the header names commit %s, but agent-infra has no %s there: %v\n%s", commit, flakeSourcePath, err, stderr.String())
	}
	if got, want := afterHeader(copyBody), afterHeader(out.String()); got != want {
		t.Errorf("%s differs from agent-infra's %s at %s after the header; a change is a new copy with a new commit, not an edit (#310)", flakeScript, flakeSourcePath, commit)
	}
	if got := bodyHash(out.String()); got != flakeSourceBodySHA256 {
		t.Errorf("flakeSourceBodySHA256 is stale: agent-infra's %s at %s hashes to %s after the header; refresh the pin from agent-infra together with the copy and its header commit (#310)", flakeSourcePath, commit, got)
	}
}

// Runners have no agent-infra checkout, so the comparison above skips there;
// this pin is what catches an edit to the copy on every pipeline.
func TestFlakeCheckBodyHashMatchesThePin(t *testing.T) {
	if got := bodyHash(readRepoFile(t, flakeScript)); got != flakeSourceBodySHA256 {
		t.Errorf("%s after the header hashes to %s, not the pinned flakeSourceBodySHA256 %s; the copy must stay agent-infra's verbatim, so a change is a new copy: update the header commit and the pin, both taken from agent-infra's %s (#310)", flakeScript, got, flakeSourceBodySHA256, flakeSourcePath)
	}
}

func TestFlakeCheckJobIsBlockingInTheFirstStageOnTheLightLane(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	job, e := flakeCheckJob(t, doc)

	stages := doc.root.vals["stages"]
	if stages == nil || stages.kind != 'q' || len(stages.items) == 0 {
		t.Fatal("the pipeline has no stages list")
	}
	first := stages.items[0].str
	if st := e.Keys["stage"]; st == nil || st.str != first {
		t.Errorf("%s must run in the cheapest stage, the first one, %q (#310); stage is %v", job, first, st)
	}
	if tags := e.Keys["tags"]; tags == nil || tags.dump() != `["light"]` {
		t.Errorf("%s must run on the shared light lane, `tags: [light]`; tags are %v (#310)", job, tags)
	}
	if af := e.Keys["allow_failure"]; af != nil && af.dump() != "false" && af.str != "false" {
		t.Errorf("%s must block: no allow_failure (#310); it has %s", job, af.dump())
	}
	if line := flakeCheckInvocation(t, doc, job); strings.Contains(line, "|| true") || strings.Contains(line, "|| :") {
		t.Errorf("%s must fail the job when the check fails; the line swallows its exit code: %s", job, line)
	}
}

func TestFlakeCheckJobRunsOnMergeRequests(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	job, e := flakeCheckJob(t, doc)
	for _, k := range []string{"only", "except"} {
		if e.Keys[k] != nil {
			t.Errorf("%s must not use %s: it has to run on every pipeline that runs the checks (#310)", job, k)
		}
	}
	if w := e.Keys["when"]; w != nil && w.str != "on_success" {
		t.Errorf("%s must run on its own, not `when: %s` (#310)", job, w.str)
	}
	rules := e.Keys["rules"]
	if rules == nil {
		return // no rules of its own: it runs whenever the pipeline does
	}
	runs := false
	for _, r := range rules.items {
		if w := r.vals["when"]; w != nil && w.str != "on_success" && w.str != "always" {
			t.Errorf("%s has a rule with `when: %s`; the check must always run and block (#310): %s", job, w.str, r.dump())
		}
		cond := ""
		if c := r.vals["if"]; c != nil {
			cond = c.str
		}
		if r.vals["changes"] != nil || r.vals["exists"] != nil {
			continue // a path filter would let some merge requests skip it
		}
		if cond == "" || strings.Contains(cond, "merge_request_event") {
			if w := r.vals["when"]; w == nil || w.str != "never" {
				runs = true
			}
		}
	}
	if !runs {
		t.Errorf("%s has rules but none runs it on a merge request pipeline; the check must run on every pipeline that runs the checks (#310): %s", job, rules.dump())
	}
}

func TestFlakeCheckJobChecksTheLogWithTheQuarantineList(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	job, _ := flakeCheckJob(t, doc)
	line := flakeCheckInvocation(t, doc, job)
	if !strings.Contains(line, flakeLog) {
		t.Errorf("%s must run the check on %s: %s (#310)", job, flakeLog, line)
	}
	fields := strings.Fields(line)
	list := ""
	for i, f := range fields {
		if f == "--quarantined" && i+1 < len(fields) {
			list = fields[i+1]
		} else if strings.HasPrefix(f, "--quarantined=") {
			list = strings.TrimPrefix(f, "--quarantined=")
		}
	}
	if list == "" {
		t.Fatalf("%s must pass the list of quarantined tests with --quarantined, or the check cannot tell whether a third sighting left the gate (#310): %s", job, line)
	}
	// The list comes from whatever skips the tests: either a committed file
	// the skip also reads, or one an earlier step of the job writes.
	if _, err := os.Stat(filepath.Join(root, list)); err == nil {
		return
	}
	written := false
	for _, l := range nonCommentScriptLines(t, doc, job) {
		if strings.Contains(l, flakeScript) {
			break
		}
		if strings.Contains(l, ">") && strings.Contains(l, list) {
			written = true
		}
	}
	if !written {
		t.Errorf("%s passes --quarantined %s, which is neither a committed file nor written by an earlier step of the job (#310)", job, list)
	}
}

// TestFlakeCheckPassesTheCurrentLog runs the check exactly as the job's script
// line does (same interpreter, same arguments, the repository root as the
// working directory), so the quarantine list it reads is the committed
// scripts/quarantine.txt, not a list the test makes up: a test-only empty list
// would let a bad entry in the real one through (#310).
func TestFlakeCheckPassesTheCurrentLog(t *testing.T) {
	if _, err := os.Stat(filepath.Join(root, flakeScript)); err != nil {
		t.Fatalf("%s must exist (#310): %v", flakeScript, err)
	}
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	job, _ := flakeCheckJob(t, doc)
	line := flakeCheckInvocation(t, doc, job)
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.Contains(fields[1], flakeScript) {
		t.Fatalf("%s runs the check as something other than `<shell> %s <args>`, which this test cannot repeat: %s", job, flakeScript, line)
	}
	cmd := exec.Command(fields[0], fields[1:]...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the flake check fails on the committed %s with the job's own arguments (%s), so the job would not be green when it lands (#310): %v\n%s", flakeLog, line, err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "NOTE\tsummary\t") {
		t.Errorf("the check's last line must be its summary note, got %q", last)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "ERROR\t") {
			t.Errorf("the check reports an error on the committed %s: %s", flakeLog, l)
		}
	}
}

// --- the quarantine list is what the live legs skip (#310, #313) -----------

const quarantineFile = "scripts/quarantine.txt"
const quarantineSkipScript = "scripts/ci/quarantine-skip.sh"

// skipIsGuarded reports whether the go test line's -skip flag only appears
// when the pattern is not empty: an empty -skip pattern skips every test. The
// guard is the `${name:+-skip "$name"}` form on the line itself, or an
// `if [ -n` on an earlier line.
func skipIsGuarded(lines []string, goTest int) bool {
	if strings.Contains(lines[goTest], ":+-skip") {
		return true
	}
	for _, l := range lines[:goTest] {
		if strings.Contains(l, "if [ -n") {
			return true
		}
	}
	return false
}

func TestLiveLegsSkipTheQuarantinedTests(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for _, job := range []string{"live", "live-fedora"} {
		lines := nonCommentScriptLines(t, doc, job)
		skipAt, goTest := -1, -1
		for i, l := range lines {
			if skipAt < 0 && strings.Contains(l, quarantineSkipScript) {
				skipAt = i
			}
			if strings.Contains(l, "go test") && strings.Contains(l, "-tags live") {
				goTest = i
			}
		}
		if goTest < 0 {
			t.Errorf("%s has no `go test -tags live` line", job)
			continue
		}
		if skipAt < 0 {
			t.Errorf("%s never runs %s, so a quarantined test still runs in the live leg and can block the gate (#310)", job, quarantineSkipScript)
		} else if skipAt > goTest {
			t.Errorf("%s runs %s after the go test line that needs its output (#310)", job, quarantineSkipScript)
		}
		if !strings.Contains(lines[goTest], "-skip") {
			t.Errorf("%s: the go test line has no -skip, so quarantined tests run in the live leg (#310): %s", job, lines[goTest])
		} else if !skipIsGuarded(lines, goTest) {
			t.Errorf("%s: -skip must be conditional on a non-empty pattern (`${name:+-skip \"$name\"}` or an `if [ -n` before it), because an empty -skip pattern skips every test (#310): %s", job, lines[goTest])
		}
	}
}

// githubStepContaining returns the text of the workflow step whose lines
// include needle: from its `- ` line to the line before the next step.
func githubStepContaining(src, needle string) string {
	lines := strings.Split(src, "\n")
	hit := -1
	for i, l := range lines {
		if strings.Contains(l, needle) && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			hit = i
			break
		}
	}
	if hit < 0 {
		return ""
	}
	start, indent := 0, 0
	for i := hit; i >= 0; i-- {
		if tl := strings.TrimLeft(lines[i], " "); strings.HasPrefix(tl, "- ") {
			start, indent = i, len(lines[i])-len(tl)
			break
		}
	}
	end := len(lines)
	for i := hit + 1; i < len(lines); i++ {
		if tl := strings.TrimLeft(lines[i], " "); strings.HasPrefix(tl, "- ") && len(lines[i])-len(tl) <= indent {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func TestGitHubLiveWorkflowSkipsTheQuarantinedTests(t *testing.T) {
	src, ok := workflows(t)["live-install-test.yml"]
	if !ok {
		t.Fatal("no .github/workflows/live-install-test.yml")
	}
	step := githubStepContaining(src, "go test -tags live")
	if step == "" {
		t.Fatal("live-install-test.yml has no step running `go test -tags live`")
	}
	lines := strings.Split(step, "\n")
	goTest := -1
	for i, l := range lines {
		if strings.Contains(l, "go test -tags live") {
			goTest = i
		}
	}
	if !strings.Contains(step, quarantineSkipScript) {
		t.Errorf("the live step never runs %s, so a quarantined test still runs there (#310):\n%s", quarantineSkipScript, step)
	}
	if !strings.Contains(lines[goTest], "-skip") {
		t.Errorf("the live step's go test command has no -skip (#310): %s", lines[goTest])
	} else if !skipIsGuarded(lines, goTest) {
		t.Errorf("the live step's -skip must be conditional on a non-empty pattern, because an empty -skip pattern skips every test (#310): %s", lines[goTest])
	}
}

// --- every quarantined name is a test that exists --------------------------

// quarantinedTests returns the test names in a quarantine list: the part of
// each non-comment, non-blank line before its first `/` outside brackets.
func quarantinedTests(list string) []string {
	var out []string
	for _, l := range strings.Split(list, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		depth, cut := 0, len(l)
		for i, r := range l {
			if r == '[' {
				depth++
			} else if r == ']' && depth > 0 {
				depth--
			} else if r == '/' && depth == 0 {
				cut = i
				break
			}
		}
		out = append(out, l[:cut])
	}
	return out
}

// splitQuarantineLine splits one quarantine line at every `/` outside
// brackets: the parent test name first, then each subtest segment.
func splitQuarantineLine(l string) []string {
	var out []string
	depth, from := 0, 0
	for i, r := range l {
		if r == '[' {
			depth++
		} else if r == ']' && depth > 0 {
			depth--
		} else if r == '/' && depth == 0 {
			out = append(out, l[from:i])
			from = i + 1
		}
	}
	return append(out, l[from:])
}

// quarantineLines returns the non-comment, non-blank lines of a list.
func quarantineLines(list string) []string {
	var out []string
	for _, l := range strings.Split(list, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// quarantinedSubtests maps each quarantined parent test (cut as in
// quarantinedTests) to the subtest segments after it, nil for a bare `TestX`.
func quarantinedSubtests(list string) map[string][]string {
	out := map[string][]string{}
	for _, l := range quarantineLines(list) {
		parts := splitQuarantineLine(l)
		out[parts[0]] = parts[1:]
	}
	return out
}

// goTestNameRewrite spells a subtest name the way go test prints and matches
// it: every space rune becomes `_` (testing/match.go's rewrite, spaces only).
func goTestNameRewrite(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return '_'
		}
		return r
	}, s)
}

var goStringLiteral = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// subtestsWithoutLiteral returns the quarantine lines with a subtest segment
// that no double-quoted literal in the parent's body spells after Go's
// rewrite. Subtests are named only at run time and `go test -list` prints
// top-level tests only, so this is a source check: a parent body cannot find
// is the top-level check's job and is skipped here.
func subtestsWithoutLiteral(list string, body func(string) (string, bool)) []string {
	known := map[string]map[string]bool{}
	var out []string
	for _, l := range quarantineLines(list) {
		parts := splitQuarantineLine(l)
		parent, segs := parts[0], parts[1:]
		if len(segs) == 0 {
			continue
		}
		set, seen := known[parent]
		if !seen {
			if b, ok := body(parent); ok {
				set = map[string]bool{}
				for _, m := range goStringLiteral.FindAllStringSubmatch(b, -1) {
					lit, err := strconv.Unquote(m[0])
					if err != nil {
						lit = m[1]
					}
					set[goTestNameRewrite(lit)] = true
				}
			}
			known[parent] = set
		}
		if set == nil {
			continue
		}
		for _, s := range segs {
			if !set[s] {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// testFuncBody returns the source of the test function name in any _test.go
// file under root, from `func name(` to the first line that is just `}`.
func testFuncBody(root, name string) (string, bool) {
	re := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)
	var body string
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		loc := re.FindIndex(src)
		if loc == nil {
			return nil
		}
		rest := string(src[loc[0]:])
		if end := strings.Index(rest, "\n}\n"); end >= 0 {
			rest = rest[:end+3]
		}
		body, found = rest, true
		return fs.SkipAll
	})
	return body, found
}

// goTestLister prints the names `go test -list` finds for a pattern, one
// invocation per call; tags and packages say where to look.
type goTestLister func(tags, pattern string, pkgs ...string) string

// quarantinedWithoutTests returns the quarantined test names that no test in
// the module has: a name that matches nothing skips nothing, and the flake
// check would trust a gate that is not there. With no names it runs nothing.
func quarantinedWithoutTests(list string, lister goTestLister) []string {
	names := quarantinedTests(list)
	if len(names) == 0 {
		return nil
	}
	esc := make([]string, len(names))
	for i, n := range names {
		esc[i] = regexp.QuoteMeta(n)
	}
	pattern := "^(" + strings.Join(esc, "|") + ")$"
	found := map[string]bool{}
	for _, out := range []string{
		lister("", pattern, "./..."),
		lister("live", pattern, "./test/live/..."),
	} {
		for _, l := range strings.Split(out, "\n") {
			found[strings.TrimSpace(l)] = true
		}
	}
	var missing []string
	for _, n := range names {
		if !found[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

func TestQuarantinedNamesMatcherReportsANameWithNoTest(t *testing.T) {
	list := "# a comment\n\nTestReal/Sub\nUnidentified internal/ui failure\n"
	lister := func(tags, pattern string, pkgs ...string) string {
		if tags == "" {
			return "TestReal\nok  \texample.com/x\t0.01s\n"
		}
		return "ok  \texample.com/y\t0.01s\n"
	}
	missing := quarantinedWithoutTests(list, lister)
	if len(missing) != 1 || missing[0] != "Unidentified internal" {
		t.Errorf("a list naming `Unidentified internal` must report exactly that as naming no test, got %q", missing)
	}
	if got := quarantinedTests("Parent[a/b]/Sub\n"); len(got) != 1 || got[0] != "Parent[a/b]" {
		t.Errorf("the name stops at the first unbracketed `/`, got %q", got)
	}
	called := false
	if m := quarantinedWithoutTests("# nothing quarantined\n\n", func(string, string, ...string) string { called = true; return "" }); len(m) != 0 || called {
		t.Errorf("an empty list passes without running go; missing %q, ran go %v", m, called)
	}
}

func TestEveryQuarantinedNameIsATestThatExists(t *testing.T) {
	list := readRepoFile(t, quarantineFile)
	lister := func(tags, pattern string, pkgs ...string) string {
		args := []string{"test"}
		if tags != "" {
			args = append(args, "-tags", tags)
		}
		args = append(args, "-list", pattern)
		args = append(args, pkgs...)
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	for _, n := range quarantinedWithoutTests(list, lister) {
		t.Errorf("%s names %q, which is no test in the module (go test -list finds nothing): it skips nothing, and the flake check trusts a gate that is not there (#310)", quarantineFile, n)
	}
}

// --- every quarantined subtest is named in its parent ----------------------

func TestQuarantinedSubtestsSplitsAtUnbracketedSlashes(t *testing.T) {
	got := quarantinedSubtests("TestA\nTestB/x\nTestC/one/two\nParent[a/b]/Sub\n# c\n\n")
	want := map[string][]string{
		"TestA":       nil,
		"TestB":       {"x"},
		"TestC":       {"one", "two"},
		"Parent[a/b]": {"Sub"},
	}
	if len(got) != len(want) {
		t.Errorf("each non-comment line gives one parent, so want %d parents, got %d: %q", len(want), len(got), got)
	}
	for parent, segs := range want {
		g, ok := got[parent]
		if !ok {
			t.Errorf("parent %q is missing: the cut at the first unbracketed `/` must match quarantinedTests, or the sub check looks up the wrong function", parent)
			continue
		}
		if strings.Join(g, "\x00") != strings.Join(segs, "\x00") || len(g) != len(segs) {
			t.Errorf("parent %q: want segments %q, got %q; a wrong split checks the wrong half of the name", parent, segs, g)
		}
	}
}

func TestGoTestNameRewriteTurnsSpacesIntoUnderscores(t *testing.T) {
	for in, want := range map[string]string{
		"a far deadline leaves the budget alone": "a_far_deadline_leaves_the_budget_alone",
		"tab\there":                              "tab_here",
		"nb space":                               "nb_space",
		"Install":                                "Install",
	} {
		if got := goTestNameRewrite(in); got != want {
			t.Errorf("goTestNameRewrite(%q) = %q, want %q: go test prints every space rune as `_`, so a name written with spaces matches nothing", in, got, want)
		}
	}
}

func TestSubtestsWithoutLiteralReportsASubNamedNowhereInTheParent(t *testing.T) {
	realBody := "{\n\tt.Run(\"Install\", func(t *testing.T) {})\n\tfor _, c := range []struct{ name string }{{\"a near deadline caps it\"}} {\n\t\tt.Run(c.name, nil)\n\t}\n}"
	var asked []string
	body := func(parent string) (string, bool) {
		asked = append(asked, parent)
		if parent == "TestReal" {
			return realBody, true
		}
		return "", false
	}
	run := func(list string) []string {
		asked = nil
		return subtestsWithoutLiteral(list, body)
	}

	for _, ok := range []string{
		"TestReal/Install",
		"TestReal/a_near_deadline_caps_it",
	} {
		if got := run(ok + "\n"); len(got) != 0 {
			t.Errorf("%q is spelled by a literal in the parent's body, so nothing should be reported, got %q", ok, got)
		}
	}
	for _, bad := range []string{
		"TestReal/a near deadline caps it",
		"TestReal/typo",
		"TestReal/Install/typo",
	} {
		got := run(bad + "\n")
		if len(got) != 1 || got[0] != bad {
			t.Errorf("%q must be reported as the full original line, got %q: -skip would match nothing", bad, got)
		}
	}
	if got := run("TestReal\n"); len(got) != 0 || len(asked) != 0 {
		t.Errorf("a line with no subtest needs no body: reported %q, body asked for %q", got, asked)
	}
	if got := run("TestGone/x\n"); len(got) != 0 {
		t.Errorf("a parent with no function is the top-level check's job, not this one's; reported %q", got)
	}
	run("Parent[a/b]/Sub\n")
	if len(asked) != 1 || asked[0] != "Parent[a/b]" {
		t.Errorf("body must be asked for the parent exactly as written (a -run pattern), got %q", asked)
	}
}

func TestTestFuncBodyFindsTheParentInTheModule(t *testing.T) {
	b, ok := testFuncBody(root, "TestEveryQuarantinedNameIsATestThatExists")
	if !ok || !strings.Contains(b, "quarantinedWithoutTests") {
		t.Errorf("testFuncBody must find this package's own test and its body; found %v, body %q", ok, b)
	}
	b, ok = testFuncBody(root, "TestLive_InstallHealthyEndpointThenRemove")
	if !ok || !strings.Contains(b, `"Install"`) {
		t.Errorf("testFuncBody must find the test in test/live (a _test.go file with or without the live tag); found %v, body has no \"Install\" literal", ok)
	}
	if _, ok := testFuncBody(root, "TestNoSuchThing"); ok {
		t.Errorf("a name no test has must report false, or a typo in the parent passes this check")
	}
}

func TestEveryQuarantinedSubtestIsNamedInItsParent(t *testing.T) {
	list := readRepoFile(t, quarantineFile)
	for _, l := range subtestsWithoutLiteral(list, func(p string) (string, bool) { return testFuncBody(root, p) }) {
		parent := quarantinedTests(l)[0]
		t.Errorf("%s names %q, but no string literal in %s's body spells that subtest (Go prints spaces as underscores): -skip would match nothing while the flake check trusts the quarantine (#310); fix the name, or quarantine the parent", quarantineFile, l, parent)
	}
}

func TestQuarantineHeaderStatesWhatTheSubtestCheckCanSee(t *testing.T) {
	header := leadingComment(readRepoFile(t, quarantineFile))
	for _, phrase := range []string{"spaces as underscores", "quarantine the parent"} {
		if !strings.Contains(header, phrase) {
			t.Errorf("%s's header must say %q: the sub half of a name is checked only against literals in the parent's body, and the writer needs to know how Go spells it and what to do when it is named elsewhere (#310)", quarantineFile, phrase)
		}
	}
}

// --- every quarantined name has earned its place in docs/flakes.md ---------

var (
	flakeSightingLine = regexp.MustCompile(`^- \d{4}-\d{2}-\d{2} · `)
	flakeIssueRef     = regexp.MustCompile(`#[0-9]+|/issues/[0-9]+|/work_items/[0-9]+`)
)

// quarantineLogProblems returns one message per line of the quarantine list
// that no heading of the flake log justifies. The flake check matches a list
// line as a substring of a heading (scripts/ci/flake-check.sh), and only
// warns when a quarantined name matches no heading, or errors for a heading
// that has three sightings and no quarantine entry -- never the other way
// round. A test may leave the gate only after a third sighting (docs/flakes.md)
// with the issue linked from its heading, so this requires, for each list
// line, a heading that contains it, has at least three dated sighting lines,
// and links an issue (SEC-SP1-2, #310).
func quarantineLogProblems(list, flakes string) []string {
	type heading struct {
		text      string
		sightings int
	}
	var headings []heading
	for _, l := range strings.Split(flakes, "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "## "):
			headings = append(headings, heading{text: strings.TrimPrefix(l, "## ")})
		case len(headings) > 0 && flakeSightingLine.MatchString(l):
			headings[len(headings)-1].sightings++
		}
	}
	var problems []string
	for _, name := range quarantineLines(list) {
		var why []string
		ok := false
		for _, h := range headings {
			if !strings.Contains(h.text, name) {
				continue
			}
			switch {
			case h.sightings < 3:
				why = append(why, fmt.Sprintf("heading %q has %d sighting line(s), want at least 3", h.text, h.sightings))
			case !flakeIssueRef.MatchString(h.text):
				why = append(why, fmt.Sprintf("heading %q links no issue (#N)", h.text))
			default:
				ok = true
			}
		}
		if ok {
			continue
		}
		if len(why) == 0 {
			why = []string{"no heading contains it"}
		}
		problems = append(problems, fmt.Sprintf("%q: %s", name, strings.Join(why, "; ")))
	}
	return problems
}

func TestQuarantineLogProblemsCatchesEachShortfall(t *testing.T) {
	const flakes = `# Flakes

## TestThree (test/live) #12

- 2026-10-01 · aaaaaaa · local · symptom
- 2026-10-02 · bbbbbbb · local · symptom
- 2026-10-03 · ccccccc · local · symptom

## TestTwo #13

- 2026-10-01 · aaaaaaa · local · symptom
- 2026-10-02 · bbbbbbb · local · symptom
- a note, not a sighting

## TestNoIssue

- 2026-10-01 · aaaaaaa · local · symptom
- 2026-10-02 · bbbbbbb · local · symptom
- 2026-10-03 · ccccccc · local · symptom
`
	for _, tc := range []struct {
		name, list, want string
	}{
		{"empty list", "# nothing\n\n", ""},
		{"justified", "TestThree\n", ""},
		{"justified with a subtest", "TestThree (test/live)\n", ""},
		{"two sightings", "TestTwo\n", "has 2 sighting"},
		{"no issue link", "TestNoIssue\n", "links no issue"},
		{"no heading", "TestAbsent\n", "no heading contains it"},
	} {
		got := strings.Join(quarantineLogProblems(tc.list, flakes), "\n")
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: problems = %q, want one containing %q", tc.name, got, tc.want)
		}
	}
}

func TestEveryQuarantinedNameHasThreeSightingsAndAnIssueInTheFlakeLog(t *testing.T) {
	for _, p := range quarantineLogProblems(readRepoFile(t, quarantineFile), readRepoFile(t, flakeLog)) {
		t.Errorf("%s: %s: a test leaves the gate only after a third sighting with its issue linked from the heading (%s, #310)", quarantineFile, p, flakeLog)
	}
}
