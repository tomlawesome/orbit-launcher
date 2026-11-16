// The shared job templates from ai/agent-infra are fetched with `include:`:
// private-details, the two batch-branch snippets catchup-check and
// batch-intake (ai/agent-infra#114), and dev-landing (ai/agent-infra#113).
// No other include is allowed.
//
// The first line below is the original comment, still true of each of them:
// the private-details job template is fetched with `include:`. A tag
// or branch ref can be deleted and recreated by any Maintainer of
// ai/agent-infra, so the ref must be the commit the tag names, with a
// comment saying which published tag that commit is (SEC-S1-3, #313, #245).
package supplychain

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var (
	includeCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	includeTagRE    = regexp.MustCompile(`(?:private-details|catchup-check|batch-intake|dev-landing)/v[0-9]+`)
)

// allowedIncludes maps each include's file to the snippet name its pinning
// comment must give as the tag (name/vN).
var allowedIncludes = map[string]string{
	"scripts/private-details/ci-template.yml": "private-details",
	"scripts/catchup-check/ci-template.yml":   "catchup-check",
	"scripts/batch-intake/ci-template.yml":    "batch-intake",
	"scripts/dev-landing/ci-template.yml":     "dev-landing",
}

// includeBlock returns the raw text from the `include:` line up to the next
// top-level key, `stages:`.
func includeBlock(t *testing.T, ciYAML string) string {
	t.Helper()
	lines := strings.Split(ciYAML, "\n")
	start, end := -1, -1
	for i, l := range lines {
		if start < 0 && l == "include:" {
			start = i
			continue
		}
		if start >= 0 && l == "stages:" {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		t.Fatal("no `include:` block followed by `stages:` in .gitlab-ci.yml")
	}
	return strings.Join(lines[start:end], "\n")
}

// includeTagsNamed returns every snippet tag (name/vN) found in a comment of
// the include block, in order.
func includeTagsNamed(block string) []string {
	var out []string
	for _, l := range strings.Split(block, "\n") {
		if i := strings.Index(l, "#"); i >= 0 {
			out = append(out, includeTagRE.FindAllString(l[i:], -1)...)
		}
	}
	return out
}

type includeEntry struct{ project, ref, file string }

// includeEntries returns the top-level include list, failing the test if it
// is not a list of mappings.
func includeEntries(t *testing.T) []includeEntry {
	t.Helper()
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	inc := doc.root.vals["include"]
	if inc == nil || inc.kind != 'q' {
		t.Fatal("the top-level `include` must be a list")
	}
	var out []includeEntry
	for i, e := range inc.items {
		if e.kind != 'm' {
			t.Fatalf("include entry %d is not a mapping", i+1)
		}
		get := func(k string) string {
			if e.vals[k] == nil {
				return ""
			}
			return e.vals[k].str
		}
		for _, k := range e.keys {
			if k != "project" && k != "ref" && k != "file" {
				t.Errorf("include entry %d uses %q: only project, ref and file are allowed", i+1, k)
			}
		}
		out = append(out, includeEntry{get("project"), get("ref"), get("file")})
	}
	return out
}

// Exactly the four allowed includes, each once, from ai/agent-infra, each
// pinned to a commit. Any other include, a repeat, or a tag/branch ref fails.
func TestIncludeIsPinnedToCommit(t *testing.T) {
	entries := includeEntries(t)
	if len(entries) != len(allowedIncludes) {
		t.Errorf("the top-level `include` has %d entries, want exactly %d (%v)", len(entries), len(allowedIncludes), allowedIncludes)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.project != "ai/agent-infra" {
			t.Errorf("include project = %q, want ai/agent-infra", e.project)
		}
		if _, ok := allowedIncludes[e.file]; !ok {
			t.Errorf("include file %q is not one of the allowed snippets %v", e.file, allowedIncludes)
		}
		if seen[e.file] {
			t.Errorf("include file %q appears twice", e.file)
		}
		seen[e.file] = true
		if !includeCommitRE.MatchString(e.ref) {
			t.Errorf("the include ref %q (%s) is not a commit: a tag or branch can be deleted and "+
				"recreated through the GitLab UI/API by any Maintainer of ai/agent-infra, so pin the "+
				"commit the tag names (git ls-remote <agent-infra> 'refs/tags/<name>/vN^{}')", e.ref, e.file)
		}
	}
	for f := range allowedIncludes {
		if !seen[f] {
			t.Errorf("the include of %s is missing", f)
		}
	}
}

// Each pinned include names, in a comment, the published tag its commit is,
// and the name in the tag is the snippet's own.
func TestIncludePinNamesItsTag(t *testing.T) {
	tags := includeTagsNamed(includeBlock(t, readRepoFile(t, ".gitlab-ci.yml")))
	got := map[string]int{}
	for _, tag := range tags {
		got[strings.SplitN(tag, "/", 2)[0]]++
	}
	for _, name := range allowedIncludes {
		if got[name] != 1 {
			t.Errorf("the include block must name the %s/vN tag its pin came from exactly once in a comment; found %d (all: %v)", name, got[name], tags)
		}
	}
}

func TestIncludePinMatchesTag(t *testing.T) {
	block := includeBlock(t, readRepoFile(t, ".gitlab-ci.yml"))
	tagFor := map[string]string{} // snippet name -> name/vN
	for _, tag := range includeTagsNamed(block) {
		tagFor[strings.SplitN(tag, "/", 2)[0]] = tag
	}
	dir := agentInfraDir()
	if dir == "" {
		t.Skip("no agent-infra checkout: set AGENT_INFRA_DIR")
	}
	for _, e := range includeEntries(t) {
		tag := tagFor[allowedIncludes[e.file]]
		if tag == "" {
			t.Errorf("no tag named in a comment for %s", e.file)
			continue
		}
		out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag+"^{commit}").Output()
		if err != nil {
			t.Skipf("the local agent-infra checkout (%s) does not have %s; fetch tags to run this check", dir, tag)
		}
		if sha := strings.TrimSpace(string(out)); sha != e.ref {
			t.Errorf("%s names %s on the local agent-infra checkout but the include pins %s: "+
				"either the tag was moved (tell the owner) or the pin is stale after a new tag", tag, sha, e.ref)
		}
	}
}

// testdata/batch-snippets.yml, which the stage tests evaluate, is a copy of
// three templates' rules. Each snippet's section records the agent-infra commit
// it was copied from in a `# copied-from: <name> <sha>` line.
const snippetsFile = "testdata/batch-snippets.yml"

var copiedFromRE = regexp.MustCompile(`(?m)^# copied-from: ([a-z-]+) ([0-9a-f]{40})$`)

func includeRefFor(t *testing.T, name string) string {
	t.Helper()
	for _, e := range includeEntries(t) {
		if allowedIncludes[e.file] == name {
			return e.ref
		}
	}
	t.Fatalf("no include for %s", name)
	return ""
}

// Needs no checkout, so it runs in CI: when an include moves to a new commit
// the copy must be re-checked against it and its recorded commit updated.
func TestBatchSnippetsCopyIsOfThePinnedCommit(t *testing.T) {
	data := readRepoFile(t, "test", "supplychain", snippetsFile)
	recorded := map[string]string{}
	for _, m := range copiedFromRE.FindAllStringSubmatch(data, -1) {
		recorded[m[1]] = m[2]
	}
	for _, name := range snippetNames {
		got, ok := recorded[name]
		if !ok {
			t.Errorf("%s has no `# copied-from: %s <sha>` line", snippetsFile, name)
			continue
		}
		if want := includeRefFor(t, name); got != want {
			t.Errorf("%s was copied from %s's commit %s but .gitlab-ci.yml pins %s: compare the copy with the new template and update the recorded commit",
				snippetsFile, name, got, want)
		}
	}
}

// snippetNames are the snippets copied into the testdata. snippetKeys lists
// the top-level keys of each that the copy must equal.
var snippetNames = []string{"catchup-check", "batch-intake", "dev-landing"}

var snippetKeys = map[string][]string{
	"catchup-check": {"catchup-check"},
	"batch-intake":  {"batch-intake"},
	"dev-landing":   {"dev-landing", "dev-landing-check"},
}

// snippetSection returns the text of the top-level key `.name:` up to the next
// top-level key, comments dropped, folded-scalar markers removed and
// whitespace folded to single spaces, so a template's `if: >-` block and the
// copy's one-line `if:` compare equal. ok is false when the key is missing.
func snippetSection(src, name string) (string, bool) {
	var keep []string
	in := false
	for _, l := range strings.Split(src, "\n") {
		top := l != "" && l[0] != ' ' && l[0] != '#' && l[0] != '-'
		if top {
			in = strings.HasPrefix(l, "."+name+":")
		}
		if in && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		return "", false
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.Join(keep, "\n"), ">-", "")), " "), true
}

// With an agent-infra checkout: the whole rule set of each snippet (both
// workflow rule and job rule: their conditions, the variables and
// `when: never`) equals the template's at the pinned commit.
func TestBatchSnippetsMatchTheTemplates(t *testing.T) {
	dir := agentInfraDir()
	if dir == "" {
		t.Skip("no agent-infra checkout: set AGENT_INFRA_DIR")
	}
	data := readRepoFile(t, "test", "supplychain", snippetsFile)
	for _, name := range snippetNames {
		ref := includeRefFor(t, name)
		out, err := exec.Command("git", "-C", dir, "show", ref+":scripts/"+name+"/ci-template.yml").Output()
		if err != nil {
			t.Skipf("the local agent-infra checkout (%s) does not have %s; fetch it to run this check", dir, ref)
		}
		for _, key := range snippetKeys[name] {
			want, ok := snippetSection(string(out), key)
			if !ok {
				t.Errorf("the template %s at %s has no .%s: key", name, ref, key)
				continue
			}
			got, ok := snippetSection(data, key)
			if !ok {
				t.Errorf("%s has no .%s: key", snippetsFile, key)
				continue
			}
			if got != want {
				t.Errorf("%s's .%s differs from the template at %s\ncopy:     %s\ntemplate: %s", snippetsFile, key, ref, got, want)
			}
		}
	}
}
