// The shared job templates from ai/agent-infra are fetched with `include:`:
// private-details, and the two batch-branch snippets catchup-check and
// batch-intake (ai/agent-infra#114). No other include is allowed.
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
	includeTagRE    = regexp.MustCompile(`(?:private-details|catchup-check|batch-intake)/v[0-9]+`)
)

// allowedIncludes maps each include's file to the snippet name its pinning
// comment must give as the tag (name/vN).
var allowedIncludes = map[string]string{
	"scripts/private-details/ci-template.yml": "private-details",
	"scripts/catchup-check/ci-template.yml":   "catchup-check",
	"scripts/batch-intake/ci-template.yml":    "batch-intake",
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

// includeTagsNamed returns every private-details/vN found in a comment of
// the include block.
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

// Exactly the three allowed includes, each once, from ai/agent-infra, each
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
			t.Logf("the local agent-infra checkout (%s) does not have %s; fetch tags to run this check", dir, tag)
			continue
		}
		if sha := strings.TrimSpace(string(out)); sha != e.ref {
			t.Errorf("%s names %s on the local agent-infra checkout but the include pins %s: "+
				"either the tag was moved (tell the owner) or the pin is stale after a new tag", tag, sha, e.ref)
		}
	}
}

// testdata/batch-snippets.yml, which the stage tests evaluate, must say what
// the pinned templates say. Each condition there, joined onto one line, has to
// appear in the template at the tag, whitespace folded.
func TestBatchSnippetsMatchTheTemplates(t *testing.T) {
	dir := agentInfraDir()
	if dir == "" {
		t.Skip("no agent-infra checkout: set AGENT_INFRA_DIR")
	}
	fold := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	data := readRepoFile(t, "test", "supplychain", "testdata", "batch-snippets.yml")
	for _, name := range []string{"catchup-check", "batch-intake"} {
		var ref string
		for _, e := range includeEntries(t) {
			if allowedIncludes[e.file] == name {
				ref = e.ref
			}
		}
		out, err := exec.Command("git", "-C", dir, "show", ref+":scripts/"+name+"/ci-template.yml").Output()
		if err != nil {
			t.Logf("the local agent-infra checkout does not have %s; skipping %s", ref, name)
			continue
		}
		tmpl := fold(string(out))
		// The snippet's section of the testdata runs from its key to the next.
		sec := data[strings.Index(data, "."+name+":"):]
		if i := strings.Index(sec[1:], "\n."); i >= 0 {
			sec = sec[:i+1]
		}
		n := 0
		for _, l := range strings.Split(sec, "\n") {
			l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
			if cond, ok := strings.CutPrefix(l, "if: "); ok {
				n++
				if !strings.Contains(tmpl, fold(cond)) {
					t.Errorf("testdata/batch-snippets.yml's %s condition is not in the template at %s:\n%s", name, ref, cond)
				}
			}
		}
		if n != 2 {
			t.Errorf("expected 2 conditions for %s in the testdata, found %d", name, n)
		}
	}
}
