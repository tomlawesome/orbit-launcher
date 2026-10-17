// Pins and shared steps that the two CI hosts must keep in step (#216, from
// #199's SU-3, SU-5, SU-8, SU-9, SU-11, SU-12 and SU-15).
//
// .gitlab-ci.yml's variables are the source of truth; GitHub's workflows copy
// them by hand, and the policy file records the gitleaks pin. Nothing but a
// test ties those copies together, so a bump on one host passes quietly until
// the other host's advisory run trips over it. TestGoPinsAgree already does
// this for the Go pins; these tests do it for the rest.
//
// Read as text, for the reason ci_rules_test.go gives. Values are compared
// quote- and comment-tolerantly, because the hosts quote differently.
package supplychain

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/supplychainpolicy/policy"
)

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// pinValue strips a trailing YAML comment and one pair of matching quotes.
func pinValue(raw string) string {
	v := strings.TrimSpace(raw)
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return v
}

// gitlabPin returns one of .gitlab-ci.yml's top-level pipeline variables.
func gitlabPin(t *testing.T, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(name) + `:[ \t]+(\S.*)$`)
	m := re.FindStringSubmatch(readRepoFile(t, ".gitlab-ci.yml"))
	if m == nil {
		t.Fatalf(".gitlab-ci.yml has no top-level variable %s", name)
	}
	return pinValue(m[1])
}

// workflows returns every GitHub workflow file's text, keyed by file name.
func workflows(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		out[e.Name()] = readRepoFile(t, ".github", "workflows", e.Name())
	}
	return out
}

// githubPin finds a pin on GitHub's side. A named `NAME: value` line anywhere
// in the workflows wins; otherwise the typed-in form is read with fallback,
// whose first group is the value. Either way every copy found must agree.
func githubPin(t *testing.T, name string, fallback *regexp.Regexp) string {
	t.Helper()
	named := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:[ \t]+(\S.*)$`)
	var found []string
	for _, body := range workflows(t) {
		for _, m := range named.FindAllStringSubmatch(body, -1) {
			found = append(found, pinValue(m[1]))
		}
	}
	if len(found) == 0 && fallback != nil {
		for _, body := range workflows(t) {
			for _, m := range fallback.FindAllStringSubmatch(body, -1) {
				found = append(found, m[1])
			}
		}
	}
	if len(found) == 0 {
		t.Fatalf("no GitHub workflow pins %s (looked for a `%s:` line and for %v)", name, name, fallback)
	}
	for _, v := range found[1:] {
		if v != found[0] {
			t.Fatalf("the GitHub workflows pin %s to different values: %q", name, found)
		}
	}
	return found[0]
}

// joinContinuations folds shell line continuations so a command split over
// lines is one logical line.
func joinContinuations(text string) string {
	return regexp.MustCompile(`\\\n[ \t]*`).ReplaceAllString(text, " ")
}

// runLines returns each non-comment logical line, trimmed, with a leading
// YAML `- ` and `run: ` removed, so `- run: cmd`, `run: cmd` and `- cmd` read
// alike.
func runLines(text string) []string {
	var out []string
	for _, l := range strings.Split(joinContinuations(text), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "#") || l == "" {
			continue
		}
		l = strings.TrimPrefix(l, "- ")
		l = strings.TrimPrefix(l, "run: ")
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

func requireDigest(t *testing.T, what, v string) {
	t.Helper()
	if !hexDigest.MatchString(v) {
		t.Fatalf("%s is %q, not a 64-character lowercase hex digest", what, v)
	}
}

// ---- SU-9: every tool pin agrees across hosts -------------------------------

func TestStaticcheckPinAgreesAcrossHosts(t *testing.T) {
	gl := gitlabPin(t, "STATICCHECK_VERSION")
	gh := githubPin(t, "STATICCHECK_VERSION", regexp.MustCompile(`staticcheck@([^\s"']+)`))
	if gl != gh {
		t.Errorf("staticcheck is pinned to %q in .gitlab-ci.yml and %q in the GitHub workflows; "+
			"move them together", gl, gh)
	}
}

func TestBatsPinAgreesAcrossHosts(t *testing.T) {
	ver := gitlabPin(t, "BATS_VERSION")
	sum := gitlabPin(t, "BATS_SHA256")
	requireDigest(t, "BATS_SHA256 in .gitlab-ci.yml", sum)

	gh := githubPin(t, "BATS_VERSION", regexp.MustCompile(
		`(?:--branch v|bats-core/archive/refs/tags/v)(\d+\.\d+\.\d+)`))
	if gh != ver {
		t.Errorf("bats is %q in .gitlab-ci.yml and %q in the GitHub workflows", ver, gh)
	}
	// SU-3: GitHub used to `git clone --branch` with no checksum. The digest
	// must now be on GitHub too, typed in or through a BATS_SHA256 variable.
	if !strings.Contains(workflows(t)["ci.yml"], sum) {
		t.Errorf("ci.yml does not carry .gitlab-ci.yml's BATS_SHA256 (%s): "+
			"GitHub must fetch bats by checksum, as GitLab does", sum)
	}
}

func TestTtydPinAgreesAcrossHosts(t *testing.T) {
	ver := gitlabPin(t, "TTYD_VERSION")
	sum := gitlabPin(t, "TTYD_SHA256")
	requireDigest(t, "TTYD_SHA256 in .gitlab-ci.yml", sum)

	gh := githubPin(t, "TTYD_VERSION", regexp.MustCompile(
		`tsl0922/ttyd/releases/download/(\d+\.\d+\.\d+)/`))
	if gh != ver {
		t.Errorf("ttyd is %q in .gitlab-ci.yml and %q in the GitHub workflows", ver, gh)
	}
	// SU-5: GitHub's visual job installed ttyd with no checksum at all.
	if !strings.Contains(workflows(t)["visual-regression.yml"], sum) {
		t.Errorf("visual-regression.yml does not carry .gitlab-ci.yml's TTYD_SHA256 (%s): "+
			"GitHub must check ttyd's digest, as GitLab does", sum)
	}
}

// Passed already: the guard the change must keep. GitHub's gitleaks pin
// agrees with GitLab's today; the quoting differs, which is what SU-8 is about.
func TestGitleaksPinAgreesAcrossHostsAndPolicy(t *testing.T) {
	ver := gitlabPin(t, "GITLEAKS_VERSION")
	sum := gitlabPin(t, "GITLEAKS_SHA256")
	requireDigest(t, "GITLEAKS_SHA256 in .gitlab-ci.yml", sum)

	if gh := githubPin(t, "GITLEAKS_VERSION", nil); gh != ver {
		t.Errorf("gitleaks is %q in .gitlab-ci.yml and %q in the GitHub workflows", ver, gh)
	}
	if gh := githubPin(t, "GITLEAKS_SHA256", nil); gh != sum {
		t.Errorf("gitleaks digest is %q in .gitlab-ci.yml and %q in the GitHub workflows", sum, gh)
	}

	pol, err := policy.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, tool := range pol.Tools {
		if tool.Name != "gitleaks" {
			continue
		}
		seen = true
		if tool.Version != ver || tool.SHA256 != sum {
			t.Errorf("the policy records gitleaks %s / %s; .gitlab-ci.yml pins %s / %s",
				tool.Version, tool.SHA256, ver, sum)
		}
	}
	if !seen {
		t.Error("the policy has no gitleaks tool entry, so the pin is checked against nothing")
	}
}

// Passed already for gitleaks, the one tool the policy records today. If the
// policy later records bats, ttyd or staticcheck too, they must match GitLab.
func TestPolicyToolsMatchGitLabPins(t *testing.T) {
	pol, err := policy.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string][2]string{
		"gitleaks":    {"GITLEAKS_VERSION", "GITLEAKS_SHA256"},
		"bats":        {"BATS_VERSION", "BATS_SHA256"},
		"ttyd":        {"TTYD_VERSION", "TTYD_SHA256"},
		"staticcheck": {"STATICCHECK_VERSION", ""},
	}
	checked := 0
	for _, tool := range pol.Tools {
		v, ok := vars[strings.ToLower(tool.Name)]
		if !ok {
			continue
		}
		checked++
		if want := gitlabPin(t, v[0]); strings.TrimPrefix(tool.Version, "v") != strings.TrimPrefix(want, "v") {
			t.Errorf("the policy records %s %s; .gitlab-ci.yml's %s is %s", tool.Name, tool.Version, v[0], want)
		}
		if v[1] != "" && tool.SHA256 != "" {
			if want := gitlabPin(t, v[1]); tool.SHA256 != want {
				t.Errorf("the policy records %s digest %s; .gitlab-ci.yml's %s is %s", tool.Name, tool.SHA256, v[1], want)
			}
		}
	}
	if checked == 0 {
		t.Error("the policy records none of the pinned tools; this check would pass vacuously")
	}
}

// ---- SU-5: GitHub's visual job runs in the pinned Playwright image ----------

// visualContainerImage reads the `container:` of visual-regression.yml's job,
// written either inline or as an `image:` key beneath it.
func visualContainerImage(t *testing.T) string {
	t.Helper()
	lines := strings.Split(workflows(t)["visual-regression.yml"], "\n")
	for i, l := range lines {
		m := regexp.MustCompile(`^( {4})container:\s*(.*)$`).FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if v := pinValue(m[2]); v != "" {
			return v
		}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" || strings.HasPrefix(strings.TrimSpace(next), "#") {
				continue
			}
			if !strings.HasPrefix(next, "      ") { // left the container's keys
				break
			}
			if im := regexp.MustCompile(`^\s+image:\s*(\S.*)$`).FindStringSubmatch(next); im != nil {
				return pinValue(im[1])
			}
		}
	}
	t.Fatal("visual-regression.yml's job has no `container:` (SU-5): the screenshots must be " +
		"taken in the same pinned Playwright image as GitLab, or a font difference shows on one host only")
	return ""
}

func TestGitHubVisualJobRunsInThePinnedPlaywrightImage(t *testing.T) {
	want := gitlabPin(t, "PLAYWRIGHT_IMAGE")
	if !strings.Contains(want, "@sha256:") {
		t.Fatalf("PLAYWRIGHT_IMAGE %q carries no digest; the comparison below would prove little", want)
	}
	if got := visualContainerImage(t); got != want {
		t.Errorf("visual-regression.yml runs in %q; .gitlab-ci.yml's PLAYWRIGHT_IMAGE is %q", got, want)
	}
}

// ---- SU-3: drift the verdict fixes ------------------------------------------

func TestGitHubTimeoutsMatchGitLab(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for _, c := range []struct{ job, file string }{
		{"fast", "ci.yml"},
		{"visual", "visual-regression.yml"},
	} {
		block := regexp.MustCompile(`(?ms)^` + c.job + `:\n(.*?)^\S`).FindStringSubmatch(ci)
		if block == nil {
			t.Fatalf(".gitlab-ci.yml has no `%s:` job", c.job)
		}
		gl := regexp.MustCompile(`(?m)^  timeout:\s*(\d+)m\s*$`).FindStringSubmatch(block[1])
		gh := regexp.MustCompile(`(?m)^\s*timeout-minutes:\s*(\d+)\s*$`).FindStringSubmatch(workflows(t)[c.file])
		if gl == nil || gh == nil {
			t.Fatalf("cannot read the %s job's timeout on both hosts (gitlab %v, github %v)", c.job, gl, gh)
		}
		if gl[1] != gh[1] || gl[1] != "15" {
			t.Errorf("the %s job times out after %sm on GitLab and %sm on GitHub; #199 SU-3 aligns both at 15m",
				c.job, gl[1], gh[1])
		}
	}
}

// The header says GitLab reproduces GitHub "job for job"; `deps` has no
// GitHub counterpart on purpose (AGENTS.md), and the header must say so.
func TestGitLabHeaderNamesTheJobGitHubLacks(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	header, _, ok := strings.Cut(ci, "\nstages:")
	if !ok {
		t.Fatal("no `stages:` key to end the header at")
	}
	if !strings.Contains(header, "job for job") {
		t.Skip("the header no longer claims 'job for job', so there is no exception to name")
	}
	if !strings.Contains(header, "deps") {
		t.Error("the header claims to reproduce GitHub 'job for job' but does not name `deps`, " +
			"the GitLab-only job, as an exception")
	}
}

// ---- SU-15: shellcheck covers every script on both hosts --------------------

const shellcheckLine = "shellcheck scripts/*.sh scripts/ci/*.sh"

func TestBothHostsShellcheckEveryScript(t *testing.T) {
	for name, body := range map[string]string{
		".github/workflows/ci.yml": workflows(t)["ci.yml"],
		".gitlab-ci.yml":           readRepoFile(t, ".gitlab-ci.yml"),
	} {
		var ok bool
		for _, l := range runLines(body) {
			if l == shellcheckLine {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s has no `%s` step: scripts/ci/*.sh and scripts/coverage.sh were never checked", name, shellcheckLine)
		}
	}
}

// Anti-vacuity for the line above: its globs must reach the scripts, the new
// one included, and the scripts must pass.
func TestShellcheckGlobsReachTheScriptsAndTheyPass(t *testing.T) {
	var files []string
	for _, g := range []string{"scripts/*.sh", "scripts/ci/*.sh"} {
		m, err := filepath.Glob(filepath.Join(root, g))
		if err != nil || len(m) == 0 {
			t.Fatalf("%s matches nothing (%v)", g, err)
		}
		files = append(files, m...)
	}
	var haveFetch bool
	for _, f := range files {
		if filepath.Base(f) == "fetch-verified.sh" {
			haveFetch = true
		}
	}
	if !haveFetch {
		t.Error("scripts/ci/fetch-verified.sh does not exist (SU-12)")
	}
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck is not installed here")
	}
	if out, err := exec.Command("shellcheck", files...).CombinedOutput(); err != nil {
		t.Errorf("shellcheck refuses the scripts:\n%s", out)
	}
}

// ---- SU-12: one checked-download script, used by both hosts -----------------

const fetchVerified = "scripts/ci/fetch-verified.sh"

func TestFetchVerifiedScriptExists(t *testing.T) {
	body := readRepoFile(t, filepath.FromSlash(fetchVerified))
	if !strings.HasPrefix(body, "#!") {
		t.Errorf("%s has no interpreter line", fetchVerified)
	}
	// Refusing a wrong digest is the whole job; the bats file proves it runs,
	// this proves the file is the one CI would call.
	if !strings.Contains(body, "sha256sum") {
		t.Errorf("%s never mentions sha256sum, so it cannot be checking a digest", fetchVerified)
	}
}

// Every download once typed its own `echo digest | sha256sum -c -`; the second
// opinion on a download is the other host, not a second typing of that line.
func TestNoHostTypesItsOwnChecksumCheck(t *testing.T) {
	files := map[string]string{".gitlab-ci.yml": readRepoFile(t, ".gitlab-ci.yml")}
	for n, b := range workflows(t) {
		files[".github/workflows/"+n] = b
	}
	hand := regexp.MustCompile(`sha256sum\s+(-c|--check)\b|shasum\s+.*(-c|--check)\b`)
	for name, body := range files {
		for _, l := range runLines(body) {
			if hand.MatchString(l) {
				t.Errorf("%s types its own checksum check (%q); download through %s (SU-12)", name, l, fetchVerified)
			}
		}
	}
}

// usesFetchVerified reports whether some logical line calls the script and
// names either the digest variable or the digest itself.
func usesFetchVerified(body string, names ...string) bool {
	for _, l := range runLines(body) {
		if !strings.Contains(l, "fetch-verified.sh") {
			continue
		}
		for _, n := range names {
			if n != "" && strings.Contains(l, n) {
				return true
			}
		}
	}
	return false
}

func TestEveryDownloadGoesThroughFetchVerified(t *testing.T) {
	gl := readRepoFile(t, ".gitlab-ci.yml")
	for _, v := range []string{"GO_SHA256", "BATS_SHA256", "TTYD_SHA256", "GITLEAKS_SHA256"} {
		if !usesFetchVerified(gl, v, gitlabPin(t, v)) {
			t.Errorf(".gitlab-ci.yml downloads with %s but not through %s", v, fetchVerified)
		}
	}

	wf := workflows(t)
	for _, c := range []struct{ file, variable, gitlabVar string }{
		{"ci.yml", "BATS_SHA256", "BATS_SHA256"},
		{"visual-regression.yml", "TTYD_SHA256", "TTYD_SHA256"},
		{"secret-scan.yml", "GITLEAKS_SHA256", "GITLEAKS_SHA256"},
	} {
		if !usesFetchVerified(wf[c.file], c.variable, gitlabPin(t, c.gitlabVar)) {
			t.Errorf("%s downloads with %s but not through %s", c.file, c.variable, fetchVerified)
		}
	}
}

// ---- SU-11: one label, and the live pin list matches the docs ---------------

var labelToken = regexp.MustCompile(`[A-Za-z][A-Za-z0-9-]+`)

// labelOf returns the longest word in a label pattern, which for these
// patterns is the label itself (the rest is regex punctuation).
func labelOf(pattern string) string {
	best := ""
	for _, w := range labelToken.FindAllString(pattern, -1) {
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}

func TestLiveLabelRegexesNameOneLabel(t *testing.T) {
	gl := readRepoFile(t, ".gitlab-ci.yml")
	rule := regexp.MustCompile(`CI_MERGE_REQUEST_LABELS\s*=~\s*/([^/\n]+)/`).FindStringSubmatch(gl)
	if rule == nil {
		t.Fatal("no `$CI_MERGE_REQUEST_LABELS =~ /.../` rule in .gitlab-ci.yml")
	}
	grep := regexp.MustCompile(`grep -q '([^']*)'`).FindAllStringSubmatch(joinContinuations(gl), -1)
	var shell string
	for _, m := range grep {
		if strings.Contains(m[1], labelOf(rule[1])) {
			shell = m[1]
		}
	}
	if shell == "" {
		t.Fatalf("no `grep -q '...'` in .gitlab-ci.yml names the rule's label %q: the job's own check must read the same label", labelOf(rule[1]))
	}
	ghm := regexp.MustCompile(`labels\.\*\.name,\s*'([^']+)'`).FindStringSubmatch(workflows(t)["live-install-test.yml"])
	if ghm == nil {
		t.Fatal("live-install-test.yml has no `labels.*.name, '<label>'` condition")
	}

	label := labelOf(rule[1])
	if got := labelOf(shell); got != label {
		t.Errorf("the rule names %q but the shell check names %q", label, got)
	}
	if ghm[1] != label {
		t.Errorf("GitLab's rule names %q but GitHub's condition names %q", label, ghm[1])
	}
	// The documentation names it too, so a rename cannot leave AGENTS.md behind.
	if !strings.Contains(readRepoFile(t, "AGENTS.md"), "`"+label+"`") {
		t.Errorf("AGENTS.md does not mention `%s`", label)
	}
	// Any other run-live label anywhere would be a second one.
	for name, body := range map[string]string{".gitlab-ci.yml": gl, "live-install-test.yml": workflows(t)["live-install-test.yml"]} {
		for _, w := range regexp.MustCompile(`run-live[A-Za-z0-9-]*`).FindAllString(body, -1) {
			if w != label {
				t.Errorf("%s names a second live label %q beside %q", name, w, label)
			}
		}
	}
}

// The two GitLab regexes are different dialects of one rule (RE2 for `rules:`,
// grep's BRE for the job's own check). Run both on the same label lists.
func TestLiveLabelRegexesSelectTheSameLabelLists(t *testing.T) {
	if _, err := exec.LookPath("grep"); err != nil {
		t.Skip("no grep")
	}
	gl := readRepoFile(t, ".gitlab-ci.yml")
	rule := regexp.MustCompile(`CI_MERGE_REQUEST_LABELS\s*=~\s*/([^/\n]+)/`).FindStringSubmatch(gl)
	if rule == nil {
		t.Fatal("no `$CI_MERGE_REQUEST_LABELS =~ /.../` rule in .gitlab-ci.yml")
	}
	label := labelOf(rule[1])
	var shell string
	for _, m := range regexp.MustCompile(`grep -q '([^']*)'`).FindAllStringSubmatch(joinContinuations(gl), -1) {
		if strings.Contains(m[1], label) {
			shell = m[1]
		}
	}
	if shell == "" {
		t.Fatalf("no `grep -q` pattern names %q", label)
	}
	re := regexp.MustCompile(rule[1])
	for _, c := range []struct {
		labels string
		want   bool
	}{
		{label, true},
		{"bug," + label, true},
		{label + ",bug", true},
		{"a," + label + ",b", true},
		{"", false},
		{"bug", false},
		{"no" + label, false},
		{label + "2", false},
		{"x-" + label, false},
	} {
		if got := re.MatchString(c.labels); got != c.want {
			t.Errorf("the rule's regex on %q = %v, want %v", c.labels, got, c.want)
		}
		cmd := exec.Command("grep", "-q", shell)
		cmd.Stdin = strings.NewReader(c.labels)
		got := cmd.Run() == nil
		if got != c.want {
			t.Errorf("the shell check on %q = %v, want %v", c.labels, got, c.want)
		}
	}
}

func TestLivePinKeysMatchAgentsMd(t *testing.T) {
	script := readRepoFile(t, "scripts", "ci", "classify-changed-paths.sh")
	m := regexp.MustCompile(`(?m)^LIVE_PIN_KEYS='([^']+)'$`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("no LIVE_PIN_KEYS='...' line in classify-changed-paths.sh")
	}
	keys := strings.Split(m[1], "|")
	for _, k := range keys {
		// Every key must be a real variable, or the classifier watches nothing.
		gitlabPin(t, k)
	}

	// The paragraph of AGENTS.md that describes the live job's triggers.
	var para string
	for _, p := range strings.Split(readRepoFile(t, "AGENTS.md"), "\n\n") {
		if strings.Contains(p, "run-live-matrix") && strings.Contains(p, "RUN_LIVE") {
			para = p
		}
	}
	if para == "" {
		t.Fatal("no AGENTS.md paragraph names both run-live-matrix and RUN_LIVE")
	}
	documented := map[string]bool{}
	for _, tok := range regexp.MustCompile("`([A-Z][A-Z0-9_]+)`").FindAllStringSubmatch(para, -1) {
		if tok[1] != "RUN_LIVE" { // a trigger, not a pin
			documented[tok[1]] = true
		}
	}
	for _, k := range keys {
		if !documented[k] {
			t.Errorf("classify-changed-paths.sh watches %s but AGENTS.md's live-job paragraph does not name it", k)
		}
		delete(documented, k)
	}
	for k := range documented {
		t.Errorf("AGENTS.md names `%s` as a live pin but LIVE_PIN_KEYS does not watch it", k)
	}
}

// The live job installs Go through the checked-download script, so a change
// to that script alone is a change to what live runs on, exactly as a moved
// GO_SHA256 is (#216). Three places must agree that it starts live: the
// classifier's LIVE_PIN_PATHS (the in-job gate), the live job's manual
// change route (which creates the job), and AGENTS.md's live paragraph.
func TestCheckedDownloadScriptStartsTheLiveJob(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	live, err := loadCI(t, ci).effectiveJob("live")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(live.Script, "bash "+fetchVerified+" ") {
		t.Fatalf("the live job no longer downloads through %s; revisit LIVE_PIN_PATHS", fetchVerified)
	}

	script := readRepoFile(t, "scripts", "ci", "classify-changed-paths.sh")
	m := regexp.MustCompile(`(?m)^LIVE_PIN_PATHS='([^']+)'$`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("no LIVE_PIN_PATHS='...' line in classify-changed-paths.sh: a change to " + fetchVerified + " alone never sets LIVE_PINS_CHANGED (#216)")
	}
	paths := strings.Split(m[1], "|")
	watched := false
	for _, p := range paths {
		watched = watched || p == fetchVerified
		readRepoFile(t, filepath.FromSlash(p)) // a path that does not exist watches nothing
	}
	if !watched {
		t.Errorf("LIVE_PIN_PATHS %v does not name %s (#216)", paths, fetchVerified)
	}

	var changeRule string
	for _, r := range liveRules(t, ci) {
		if strings.Contains(r, "changes:") && hasLine(r, "- .gitlab-ci.yml") {
			changeRule = r
		}
	}
	var para string
	for _, p := range strings.Split(readRepoFile(t, "AGENTS.md"), "\n\n") {
		if strings.Contains(p, "run-live-matrix") && strings.Contains(p, "RUN_LIVE") {
			para = p
		}
	}
	for _, p := range paths {
		if !hasLine(changeRule, "- "+p) {
			t.Errorf("the live job's change route does not list %s, so a change to it alone never creates the job (#216)\nrule:\n%s", p, changeRule)
		}
		if !strings.Contains(para, "`"+p+"`") {
			t.Errorf("AGENTS.md's live-job paragraph does not name `%s` as a live trigger (#216)", p)
		}
	}
}

// The same holds for every job that starts on file changes: if it downloads
// through the checked-download script, each of its `changes:` routes lists
// that script, or a change to it alone never runs the job (#216). Jobs with
// no `changes:` route run on every pipeline and need nothing.
func TestJobsDownloadingThroughFetchVerifiedStartWhenItChanges(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	checked := 0
	for _, name := range doc.root.keys {
		if strings.HasPrefix(name, ".") || doc.root.vals[name].kind != 'm' {
			continue
		}
		job, err := doc.effectiveJob(name)
		if err != nil {
			continue // stages, workflow, default, variables
		}
		if !strings.Contains(job.Script, "bash "+fetchVerified+" ") {
			continue
		}
		rules := job.Keys["rules"]
		if rules == nil {
			continue
		}
		for i, r := range rules.items {
			ch, ok := r.vals["changes"]
			if r.kind != 'm' || !ok {
				continue
			}
			checked++
			listed := false
			for _, it := range ch.items {
				listed = listed || it.str == fetchVerified
			}
			if !listed {
				t.Errorf("job %q downloads through %s, but its rule %d's `changes:` does not list it (#216)", name, fetchVerified, i+1)
			}
		}
	}
	if checked < 2 {
		t.Fatalf("checked %d `changes:` routes; want at least visual's and live's", checked)
	}
}

// ---- SU-8: the real repository through the policy package -------------------

// Passed already: GitleaksPin must keep returning clean values for the real
// files, whatever the quoting. Fixture cases for each combination live in
// tools/supplychainpolicy/policy.
func TestGitleaksPinOnTheRealFilesIsUnquoted(t *testing.T) {
	version, sum, err := policy.GitleaksPin(root)
	if err != nil {
		t.Fatal(err)
	}
	if version != gitlabPin(t, "GITLEAKS_VERSION") || sum != gitlabPin(t, "GITLEAKS_SHA256") {
		t.Errorf("GitleaksPin = %q / %q, not .gitlab-ci.yml's pin", version, sum)
	}
}
