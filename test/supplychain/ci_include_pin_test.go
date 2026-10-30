// The shared private-details job template is fetched with `include:`. A tag
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
	includeTagRE    = regexp.MustCompile(`private-details/v[0-9]+`)
)

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
func includeTagsNamed(block string) []string {
	var out []string
	for _, l := range strings.Split(block, "\n") {
		if i := strings.Index(l, "#"); i >= 0 {
			out = append(out, includeTagRE.FindAllString(l[i:], -1)...)
		}
	}
	return out
}

func includeRef(t *testing.T) string {
	t.Helper()
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	inc := doc.root.vals["include"]
	if inc == nil || inc.kind != 'q' || len(inc.items) != 1 {
		t.Fatal("the top-level `include` must be a list with exactly one entry")
	}
	e := inc.items[0]
	if e.kind != 'm' || e.vals["ref"] == nil {
		t.Fatal("the include entry is not a mapping with a ref")
	}
	return e.vals["ref"].str
}

func TestIncludeIsPinnedToCommit(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	inc := doc.root.vals["include"]
	if inc == nil || inc.kind != 'q' || len(inc.items) != 1 {
		t.Fatal("the top-level `include` must be a list with exactly one entry")
	}
	e := inc.items[0]
	if e.kind != 'm' {
		t.Fatal("the include entry is not a mapping")
	}
	get := func(k string) string {
		if e.vals[k] == nil {
			return ""
		}
		return e.vals[k].str
	}
	if got := get("project"); got != "ai/agent-infra" {
		t.Errorf("include project = %q, want ai/agent-infra", got)
	}
	if got := get("file"); got != "scripts/private-details/ci-template.yml" {
		t.Errorf("include file = %q, want scripts/private-details/ci-template.yml", got)
	}
	if ref := get("ref"); !includeCommitRE.MatchString(ref) {
		t.Errorf("the include ref %q is not a commit: a tag or branch can be deleted and "+
			"recreated through the GitLab UI/API by any Maintainer of ai/agent-infra, so pin the "+
			"commit the tag names (git ls-remote <agent-infra> 'refs/tags/private-details/v3^{}')", ref)
	}
}

func TestIncludePinNamesItsTag(t *testing.T) {
	block := includeBlock(t, readRepoFile(t, ".gitlab-ci.yml"))
	if tags := includeTagsNamed(block); len(tags) != 1 {
		t.Errorf("the include pins a commit but no comment in the include block names the "+
			"private-details/vN tag it came from (found %d tag names in comments: %v)", len(tags), tags)
	}
}

func TestIncludePinMatchesTag(t *testing.T) {
	tags := includeTagsNamed(includeBlock(t, readRepoFile(t, ".gitlab-ci.yml")))
	if len(tags) != 1 {
		t.Fatalf("need exactly one private-details/vN tag named in a comment of the include block, found %v", tags)
	}
	tag := tags[0]
	dir := agentInfraDir()
	if dir == "" {
		t.Skip("no agent-infra checkout: set AGENT_INFRA_DIR")
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag+"^{commit}").Output()
	if err != nil {
		t.Skipf("the local agent-infra checkout (%s) does not have %s; fetch tags to run this check", dir, tag)
	}
	sha := strings.TrimSpace(string(out))
	if ref := includeRef(t); sha != ref {
		t.Errorf("%s names %s on the local agent-infra checkout but the include pins %s: "+
			"either the tag was moved (tell the owner) or the pin is stale after a new tag", tag, sha, ref)
	}
}
