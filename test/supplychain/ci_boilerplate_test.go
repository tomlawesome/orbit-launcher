// The GitLab pipeline's repeated boilerplate lives in one place (#217, from
// #199 WI-13, findings SU-13 and SU-18): the docs-only skip and the apt
// prologue are YAML anchors, `default:` sets the image pull policy once, and
// the two live-job scanners share one `liveJobLines` helper. None of it may
// change what any job runs or when, so every job keeps its name and its
// effective rules and script -- compared with the file as it stood at ce4b412,
// kept in testdata/ and read by ci_miniyaml_test.go with anchors, merge keys,
// `extends:` and `default:` resolved. The only lines that differ are the ones
// #216 changes on purpose, listed in changedBy216.
package supplychain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// oldCIFixture is .gitlab-ci.yml at ce4b412, before the restructure.
const oldCIFixture = "testdata/gitlab-ci.ce4b412.txt"

// topLevelKeysAtCE4B412 is every top-level key of .gitlab-ci.yml at ce4b412:
// the pipeline keywords, the hidden templates and the jobs. Typed here, not
// read from the fixture, so the fixture cannot drift from the list unnoticed.
var topLevelKeysAtCE4B412 = []string{
	"stages", "workflow", "default", "variables",
	".go_cache", ".go_tarball", ".privileged_runner", ".docker_in_job",
	"classify", "fast", "deps", "gitleaks", "visual", "live",
	"renovate", "release:version", "release:gitlab",
}

func loadCI(t *testing.T, ciYAML string) *yamlDoc {
	t.Helper()
	doc, err := parseMiniYAML(ciYAML)
	if err != nil {
		t.Fatalf("reading the pipeline as YAML: %v", err)
	}
	if doc.root.kind != 'm' {
		t.Fatal("the pipeline is not a YAML mapping")
	}
	return doc
}

// nonCommentLines returns the file's lines with comment-only lines dropped,
// so a count of a literal ignores prose that quotes it.
func nonCommentLines(src string) []string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			out = append(out, l)
		}
	}
	return out
}

func countLinesContaining(src, needle string) int {
	n := 0
	for _, l := range nonCommentLines(src) {
		if strings.Contains(l, needle) {
			n++
		}
	}
	return n
}

// Passed already: guards the reader. The typed key list must match the
// fixture, and the fixture must parse, or every comparison below is empty.
func TestCIFixtureMatchesTypedKeyList(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, "test", "supplychain", oldCIFixture))
	got := append([]string(nil), doc.root.keys...)
	want := append([]string(nil), topLevelKeysAtCE4B412...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("the ce4b412 fixture's top-level keys are\n  %v\nbut the typed list says\n  %v", got, want)
	}
	live, err := doc.effectiveJob("live")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(live.Script, "\n")); n < 100 {
		t.Errorf("the fixture's `live` job reads as %d script lines, want over 100: the reader is dropping lines", n)
	}
}

// Passed already: every job and template keeps its name (and extras may only
// be hidden templates, which GitLab never runs). The restructure adds anchors;
// it must not rename, split or drop a job.
func TestCIJobNamesUnchanged(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	have := map[string]bool{}
	for _, k := range doc.root.keys {
		have[k] = true
	}
	typed := map[string]bool{}
	for _, k := range topLevelKeysAtCE4B412 {
		typed[k] = true
		if !have[k] {
			t.Errorf("top-level key %q from ce4b412 is gone from .gitlab-ci.yml: job names must not change (#217)", k)
		}
	}
	for _, k := range doc.root.keys {
		if !typed[k] && !strings.HasPrefix(k, ".") {
			t.Errorf("new top-level key %q is not a hidden template (it would be a new job): the restructure may add only `.`-prefixed templates (#217)", k)
		}
	}
}

// Passed already (it must stay passing): each job's effective script -- the
// lines it runs, before_script, script, after_script, in order -- and every
// other key it has (image, extends and the cache it brings, needs, rules,
// timeout, tags, artifacts, variables, when, allow_failure, interruptible,
// and whatever `default:` supplies) are what they were at ce4b412. This is
// the check that the anchors changed no behaviour.
func TestCIEffectiveRulesAndScriptsUnchanged(t *testing.T) {
	oldDoc := loadCI(t, readRepoFile(t, "test", "supplychain", oldCIFixture))
	newDoc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))

	// The pipeline-wide keys that decide when anything runs. `variables:`
	// holds the pins Renovate moves, and `default:` is compared through each
	// job, so neither is compared whole.
	for _, k := range []string{"stages", "workflow"} {
		if o, n := oldDoc.root.vals[k].dump(), newDoc.root.vals[k].dump(); o != n {
			t.Errorf("top-level %q changed (#217)\nwant: %s\n got: %s", k, o, n)
		}
	}

	compared := 0
	for _, name := range topLevelKeysAtCE4B412 {
		switch name {
		case "stages", "workflow", "default", "variables":
			continue
		}
		if oldDoc.root.vals[name].kind != 'm' {
			continue // an anchored scalar such as .go_tarball: no rules, no script
		}
		want, err := oldDoc.effectiveJob(name)
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if name == "fast" {
			want.Script = fastPlaceholderFirst(t, want.Script)
		}
		got, err := newDoc.effectiveJob(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		compared++
		wantKeys := map[string]string{}
		for k, v := range want.Keys {
			wantKeys[k] = jobKeyDump(k, v)
		}
		wantKeys["script"] = want.Script
		applyChangedBy216(t, name, wantKeys)
		gotKeys := map[string]string{"script": got.Script}
		for k, v := range got.Keys {
			gotKeys[k] = jobKeyDump(k, v)
		}
		for _, k := range unionOfKeys(wantKeys, gotKeys) {
			w, okW := wantKeys[k]
			g, okG := gotKeys[k]
			switch {
			case !okG:
				t.Errorf("job %q: %s is gone (#217)\nwas: %s", name, k, w)
			case !okW:
				t.Errorf("job %q: %s is new (#217)\nnow: %s", name, k, g)
			case w != g && k == "script":
				t.Errorf("job %q: effective script changed (#217)\nwant:\n%s\n got:\n%s", name, w, g)
			case w != g:
				t.Errorf("job %q: effective %s changed (#217)\nwant: %s\n got: %s", name, k, w, g)
			}
		}
	}
	// A reader that found no jobs would pass the loop above vacuously.
	if compared < 8 {
		t.Fatalf("compared only %d jobs: the reader is not finding them", compared)
	}
	if want, _ := oldDoc.effectiveJob("fast"); !strings.Contains(want.Script, "DOCS_ONLY") {
		t.Fatal("the fixture's `fast` job does not carry the docs-only skip: the reader lost it")
	}
	if want, _ := oldDoc.effectiveJob("deps"); want.Keys["cache"] == nil || want.Keys["tags"] == nil {
		t.Fatal("the fixture's `deps` job reads with no cache or no tags: `extends:` or `default:` is not being applied")
	}
}

// jobKeyDump renders one job key for comparison. A literal image name keeps
// its repository but not its tag or digest: Renovate moves those, and a
// version bump is not a restructure, while a different image still differs.
// Images named by a variable (`$GO_IMAGE`) compare as written.
func jobKeyDump(key string, v *yamlNode) string {
	if key != "image" {
		return v.dump()
	}
	strip := func(name string) string {
		if strings.HasPrefix(name, "$") {
			return name
		}
		name, _, _ = strings.Cut(name, "@")
		if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
			name = name[:i]
		}
		return name
	}
	if v.kind == 's' {
		return (&yamlNode{kind: 's', str: strip(v.str)}).dump()
	}
	if v.kind != 'm' || v.vals["name"] == nil {
		return v.dump()
	}
	c := &yamlNode{kind: 'm', keys: v.keys, vals: map[string]*yamlNode{}}
	for k, x := range v.vals {
		c.vals[k] = x
	}
	c.vals["name"] = &yamlNode{kind: 's', str: strip(v.vals["name"].str)}
	return c.dump()
}

func unionOfKeys(a, b map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// The one line #217 moves on purpose. fast and deps share one docs-only skip,
// so fast's empty profile cannot stay inside it: fast writes it as its own
// step just before the skip instead. scripts/coverage.sh overwrites it when
// the gate runs, so the only change anyone sees is that a fast job failing
// before coverage.sh now uploads an empty profile instead of none -- which is
// what the artifact's `when: always` asked for anyway.
const fastPlaceholder = `printf 'mode: set\n' > coverage.out`

// fastPlaceholderFirst rewrites ce4b412's fast script the way #217 does:
// the placeholder line leaves the docs-only `if` and runs just before it.
// It fails unless the fixture has exactly that shape, so it cannot quietly
// excuse any other change.
func fastPlaceholderFirst(t *testing.T, script string) string {
	t.Helper()
	const skipIf = `if [ "${DOCS_ONLY:-false}" = "true" ]; then`
	inside := "  " + fastPlaceholder
	lines := strings.Split(script, "\n")
	var out []string
	moved, placed := 0, 0
	for _, l := range lines {
		switch l {
		case inside:
			moved++
			continue
		case skipIf:
			out = append(out, fastPlaceholder)
			placed++
		}
		out = append(out, l)
	}
	if moved != 1 || placed != 1 {
		t.Fatalf("ce4b412's fast script should hold the placeholder once inside one docs-only skip; found %d placeholders and %d skips", moved, placed)
	}
	return strings.Join(out, "\n")
}

// What #216 changes on purpose, and nothing else (#199 SU-12, SU-15).
// Every pinned download used to type its own `curl` and `sha256sum -c`; each
// now calls scripts/ci/fetch-verified.sh with the same URL, the same digest
// variable and the same destination. fast's shellcheck step widens from the
// retired bootstrap script to every script. Each rewrite names its jobs and
// the key it edits ("" for the script, matched as whole lines; any other key
// matched in its dump), and must find its old text exactly once in each, so
// it cannot excuse any other change.
var changedBy216 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{ // the Go tarball, through the .go_tarball anchor
		jobs: []string{"visual", "live"},
		old: `curl --fail --silent --show-error --location --output "/tmp/${tarball}" \
  "https://go.dev/dl/${tarball}"
echo "${GO_SHA256}  /tmp/${tarball}" | sha256sum -c -`,
		new: `bash scripts/ci/fetch-verified.sh "https://go.dev/dl/${tarball}" \
  "${GO_SHA256}" "/tmp/${tarball}"`,
	},
	{
		jobs: []string{"fast"},
		old:  `shellcheck scripts/get-orbit-launcher.sh`,
		new:  `shellcheck scripts/*.sh scripts/ci/*.sh`,
	},
	{
		jobs: []string{"fast"},
		old: `curl --fail --silent --show-error --location --output /tmp/bats.tar.gz \
  "https://github.com/bats-core/bats-core/archive/refs/tags/v${BATS_VERSION}.tar.gz"
echo "${BATS_SHA256}  /tmp/bats.tar.gz" | sha256sum -c -`,
		new: `bash scripts/ci/fetch-verified.sh \
  "https://github.com/bats-core/bats-core/archive/refs/tags/v${BATS_VERSION}.tar.gz" \
  "${BATS_SHA256}" /tmp/bats.tar.gz`,
	},
	{
		jobs: []string{"gitleaks"},
		old: `curl --fail --silent --show-error --location --output "/tmp/${tarball}" \
  "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/${tarball}"
echo "${GITLEAKS_SHA256}  /tmp/${tarball}" | sha256sum -c -`,
		new: `bash scripts/ci/fetch-verified.sh \
  "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/${tarball}" \
  "${GITLEAKS_SHA256}" "/tmp/${tarball}"`,
	},
	{
		jobs: []string{"visual"},
		old: `curl --fail --silent --show-error --location --output /usr/local/bin/ttyd \
  "https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.x86_64"
echo "${TTYD_SHA256}  /usr/local/bin/ttyd" | sha256sum -c -`,
		new: `bash scripts/ci/fetch-verified.sh \
  "https://github.com/tsl0922/ttyd/releases/download/${TTYD_VERSION}/ttyd.x86_64" \
  "${TTYD_SHA256}" /usr/local/bin/ttyd`,
	},
}

// applyChangedBy216 rewrites one ce4b412 job's keys the way #216 does. Each
// rewrite for this job must match exactly once; a key it names that the job
// lacks is a failure too, so no entry can go unused.
func applyChangedBy216(t *testing.T, job string, keys map[string]string) {
	t.Helper()
	for _, r := range changedBy216 {
		applies := false
		for _, j := range r.jobs {
			applies = applies || j == job
		}
		if !applies {
			continue
		}
		key, from, to, text := r.key, r.old, r.new, ""
		if key == "" {
			key = "script"
			from, to = "\n"+from+"\n", "\n"+to+"\n"
			text = "\n" + keys[key] + "\n"
		} else {
			text = keys[key]
		}
		if n := strings.Count(text, from); n != 1 {
			t.Fatalf("ce4b412's %s %s should hold this once, found %d:\n%s", job, key, n, r.old)
		}
		text = strings.Replace(text, from, to, 1)
		if r.key == "" {
			text = text[1 : len(text)-1]
		}
		keys[key] = text
	}
}

// anchorsHolding returns the anchors whose resolved value contains needle,
// with the top-level keys that alias each.
func anchorsHolding(doc *yamlDoc, needle string) map[string][]string {
	out := map[string][]string{}
	for name, a := range doc.anchors {
		if !strings.Contains(a.node.dump(), needle) {
			continue
		}
		var users []string
		for k := range a.usedBy {
			users = append(users, k)
		}
		sort.Strings(users)
		out[name] = users
	}
	return out
}

// The docs-only skip was written out in `fast` and `deps` (SU-13). It is one
// anchor now, written once and used by both.
func TestDocsOnlySkipIsOneAnchor(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	doc := loadCI(t, ci)

	// The anchor's own line is a definition, not a repeat; every other
	// mention of the test means a job still carries a hand-written copy.
	if n := countLinesContaining(ci, `DOCS_ONLY:-false`); n != 1 {
		t.Errorf("the docs-only test `${DOCS_ONLY:-false}` is written on %d lines of .gitlab-ci.yml; want exactly 1, inside the anchor (SU-13, #217)", n)
	}

	holders := anchorsHolding(doc, "DOCS_ONLY:-false")
	if len(holders) == 0 {
		t.Fatal("no YAML anchor holds the docs-only skip (SU-13, #217)")
	}
	used := false
	for name, users := range holders {
		have := map[string]bool{}
		for _, u := range users {
			have[u] = true
		}
		if have["fast"] && have["deps"] {
			used = true
			continue
		}
		t.Logf("anchor %q is used by %v", name, users)
	}
	if !used {
		t.Error("no anchor holding the docs-only skip is used by both `fast` and `deps` (SU-13, #217)")
	}

	// The pair to the absence above: both jobs really do skip on DOCS_ONLY
	// once the anchor is expanded, and fast alone still writes coverage.out.
	fast, err := doc.effectiveJob("fast")
	if err != nil {
		t.Fatal(err)
	}
	deps, err := doc.effectiveJob("deps")
	if err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]jobEffective{"fast": fast, "deps": deps} {
		if !strings.Contains(e.Script, `DOCS_ONLY:-false`) {
			t.Errorf("job %q no longer skips on DOCS_ONLY after anchor expansion", name)
		}
	}
	if !strings.Contains(fast.Script, "coverage.out") {
		t.Error("job `fast` must still write coverage.out when it skips: the artifact is declared `when: always`")
	}
}

// `export DEBIAN_FRONTEND=noninteractive` and `apt-get update -qq` led four
// jobs' setup (SU-13). They are one anchor now, used by all four.
func TestAptPrologueIsOneAnchor(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	doc := loadCI(t, ci)

	for _, line := range []string{"export DEBIAN_FRONTEND=noninteractive", "apt-get update -qq"} {
		if n := countLinesContaining(ci, line); n != 1 {
			t.Errorf("%q is written on %d lines of .gitlab-ci.yml; want exactly 1, inside the anchor (SU-13, #217)", line, n)
		}
	}

	holders := anchorsHolding(doc, "apt-get update -qq")
	if len(holders) == 0 {
		t.Fatal("no YAML anchor holds the apt prologue (SU-13, #217)")
	}
	want := []string{"classify", "fast", "gitleaks", "live"}
	best := ""
	for name, users := range holders {
		have := map[string]bool{}
		for _, u := range users {
			have[u] = true
		}
		ok := true
		for _, w := range want {
			ok = ok && have[w]
		}
		if ok {
			best = name
		}
	}
	if best == "" {
		t.Errorf("no anchor holding the apt prologue is used by all of %v (SU-13, #217); anchors found: %v", want, holders)
	}

	// Each of the four still installs its own packages after the prologue.
	for job, pkg := range map[string]string{
		"classify": "git", "fast": "shellcheck", "gitleaks": "ca-certificates curl git", "live": "ca-certificates curl git docker.io docker-compose-v2 python3 jq openssl",
	} {
		e, err := doc.effectiveJob(job)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(e.Script, "apt-get install -y -qq --no-install-recommends "+pkg) {
			t.Errorf("job %q must still install %q after the prologue", job, pkg)
		}
	}
}

// `pull_policy: if-not-present` was written out in every job's image (SU-13).
// It is written once now, in a hidden template's anchor that each job merges
// into its own image. Not in `default:`: every job here names its own image,
// and GitLab does not merge `default: image:` into a job's `image:` --
// "Default configuration does not merge with job configuration. If the job
// already has a keyword defined, the job keyword takes precedence and the
// default configuration is not used." (docs.gitlab.com/ci/yaml/#default).
func TestPullPolicyIsWrittenOnceAndEveryJobKeepsIt(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	doc := loadCI(t, ci)

	if n := countLinesContaining(ci, "pull_policy:"); n != 1 {
		t.Errorf("`pull_policy:` is written on %d lines of .gitlab-ci.yml; want exactly 1 (SU-13, #217)", n)
	}

	// The one line sits in a hidden template, under an anchor.
	var holders []string
	for name, a := range doc.anchors {
		if pp, ok := a.node.find("pull_policy"); ok && pp.str == "if-not-present" {
			holders = append(holders, name)
		}
	}
	if len(holders) != 1 {
		t.Fatalf("anchors holding `pull_policy: if-not-present`: %v; want exactly one (SU-13, #217)", holders)
	}
	for _, k := range doc.root.keys {
		if strings.HasPrefix(k, ".") {
			continue
		}
		if _, ok := doc.root.vals[k].find("pull_policy"); ok && !doc.anchors[holders[0]].usedBy[k] {
			t.Errorf("%q carries a pull_policy that is not the anchor's (SU-13, #217)", k)
		}
	}

	// Every job's effective image -- its own, or `default:`'s whole when it
	// sets none -- keeps the policy.
	jobs := 0
	for _, k := range doc.root.keys {
		switch k {
		case "stages", "workflow", "default", "variables":
			continue
		}
		if strings.HasPrefix(k, ".") || doc.root.vals[k].kind != 'm' {
			continue
		}
		jobs++
		merged, err := doc.extendsMerge(doc.root.vals[k], 0)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		img, set := merged.vals["image"]
		if !set {
			if def, ok := doc.root.vals["default"]; ok {
				img, set = def.vals["image"]
			}
		}
		if !set {
			t.Errorf("job %q has no image, so no pull policy (SU-13, #217)", k)
			continue
		}
		if img.kind != 'm' {
			t.Errorf("job %q's image is a bare name with no pull policy (SU-13, #217)", k)
			continue
		}
		if pp, ok := img.vals["pull_policy"]; !ok || pp.str != "if-not-present" {
			t.Errorf("job %q's effective image does not pull if-not-present (SU-13, #217)", k)
		}
	}
	if jobs < 9 {
		t.Fatalf("checked only %d jobs: the reader is not finding them", jobs)
	}
}

// ciTestFiles parses every _test.go file in the package, apart from this
// item's own, so the scan sees only the existing and the builder's files.
func ciTestFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	dir := filepath.Join(root, "test", "supplychain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "ci_boilerplate") || strings.HasPrefix(name, "ci_miniyaml") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		out[name] = f
	}
	return out
}

func callsTo(f *ast.File, fn string) int {
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		if c, ok := node.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == fn {
				n++
			}
		}
		return true
	})
	return n
}

// SU-18: ci_rules_test and live_manifest_test each carried their own copy of
// the loop that finds `live:` and stops at the next top-level key. One
// `liveJobLines` helper does it now and both tests read through it.
func TestLiveJobLinesIsDeclaredOnceAndUsedByBothScanners(t *testing.T) {
	files := ciTestFiles(t)

	var declaredIn []string
	for name, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "liveJobLines" {
				declaredIn = append(declaredIn, name)
			}
		}
	}
	sort.Strings(declaredIn)
	if len(declaredIn) != 1 {
		t.Fatalf("liveJobLines is declared in %v; want exactly one declaration in test/supplychain (SU-18, #217)", declaredIn)
	}

	for _, name := range []string{"ci_rules_test.go", "live_manifest_test.go"} {
		f, ok := files[name]
		if !ok {
			t.Fatalf("%s is gone", name)
		}
		if callsTo(f, "liveJobLines") == 0 {
			t.Errorf("%s does not call liveJobLines: it must read the live job through the shared helper (SU-18, #217)", name)
		}
	}
}

// Nothing but the helper may carry the "find the `live:` line" scan. The
// literal is how both old copies recognised the job.
func TestOnlyLiveJobLinesLooksForTheLiveKey(t *testing.T) {
	files := ciTestFiles(t)
	helperFound := false
	for name, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			isHelper := fd.Recv == nil && fd.Name.Name == "liveJobLines"
			helperFound = helperFound || isHelper
			if isHelper {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"live:"` {
					t.Errorf("%s: %s repeats the scan for the \"live:\" line; use liveJobLines (SU-18, #217)", name, fd.Name.Name)
				}
				return true
			})
		}
	}
	// Without the helper the loop above proves nothing.
	if !helperFound {
		t.Fatal("liveJobLines is not declared, so the absence checked here is vacuous (SU-18, #217)")
	}
}
