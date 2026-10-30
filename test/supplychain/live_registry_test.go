// The live job's preview install pulls the image from the registry that built
// it (#241). The preview manifest comes from ai/orbit's GitLab pipeline, and
// its image.repository names the GitLab registry path the image was pushed to.
// install.sh ignores that field and pulls from ghcr.io unless told otherwise,
// but the ghcr.io copy only appears once the GitHub mirror has copied it, up to
// two hours later and never if that run fails. So, straight after the job
// exports ORBIT_RELEASE_MANIFEST, it points install.sh at the registry the
// manifest names (ORBIT_REGISTRY, ORBIT_REPOSITORY) and logs in to it with the
// job token. The stable route stays on install.sh's default registry, so all
// of this sits inside the preview block.
//
// Read as text, for the reason ci_rules_test.go gives.
package supplychain

import (
	"strings"
	"testing"
)

// previewBlockAfterManifestExport returns the `live` job's lines (indentation
// kept, comments and blanks dropped) that follow the `export
// ORBIT_RELEASE_MANIFEST=` line and belong to the same shell block as it: the
// scan stops at the first line indented less than the export. The preview
// block is not otherwise marked in the job, so "same block as the manifest
// export" is how these tests delimit it.
func previewBlockAfterManifestExport(t *testing.T, ciYAML string) []string {
	t.Helper()
	var lines []string
	for _, l := range liveJobLines(t, ciYAML, "live") {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, l)
	}
	indentOf := func(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }
	for i, l := range lines {
		if !strings.Contains(l, "export ORBIT_RELEASE_MANIFEST=") {
			continue
		}
		var block []string
		for _, next := range lines[i+1:] {
			if indentOf(next) < indentOf(l) {
				break // the preview block has ended
			}
			block = append(block, strings.TrimSpace(next))
		}
		return block
	}
	t.Fatal("the `live` job has no `export ORBIT_RELEASE_MANIFEST=` line")
	return nil
}

func TestLivePreviewInstallPullsFromTheManifestsRegistry(t *testing.T) {
	block := previewBlockAfterManifestExport(t, readRepoFile(t, ".gitlab-ci.yml"))

	readsRepository := false
	for _, l := range block {
		if strings.Contains(l, ".image.repository") && strings.Contains(l, "ORBIT_RELEASE_MANIFEST") {
			readsRepository = true
		}
	}
	if !readsRepository {
		t.Error("after `export ORBIT_RELEASE_MANIFEST=`, the `live` job's preview block must read " +
			"`.image.repository` from \"$ORBIT_RELEASE_MANIFEST\": install.sh ignores the manifest's " +
			"repository and would pull from the GitHub copy, which may not exist yet (#241)")
	}

	for _, name := range []string{"ORBIT_REGISTRY", "ORBIT_REPOSITORY"} {
		found := false
		for _, l := range block {
			if strings.HasPrefix(l, "export "+name+"=") {
				found = true
			}
		}
		if !found {
			t.Errorf("after `export ORBIT_RELEASE_MANIFEST=`, the `live` job's preview block must "+
				"`export %s=` (derived from the manifest's .image.repository), so install.sh pulls "+
				"from the registry that built the image (#241)", name)
		}
	}
}

func TestLivePreviewInstallLogsInToTheManifestsRegistryViaStdin(t *testing.T) {
	block := previewBlockAfterManifestExport(t, readRepoFile(t, ".gitlab-ci.yml"))

	found := false
	for _, l := range block {
		_, login, ok := strings.Cut(l, "docker login")
		if !ok {
			continue
		}
		found = true
		if !strings.Contains(login, "--password-stdin") {
			t.Errorf("the `docker login` must read the token with --password-stdin (#241)\nline: %s", l)
		}
		// The token may be piped into the command, but must not be an
		// argument of it: arguments show in process listings and logs.
		if strings.Contains(login, "CI_JOB_TOKEN") {
			t.Errorf("$CI_JOB_TOKEN must not appear on the `docker login` command line itself (#241)\nline: %s", l)
		}
		for _, banned := range []string{"--password ", "--password=", " -p "} {
			if strings.Contains(login+" ", banned) {
				t.Errorf("the `docker login` must not take the password as an argument (%q) (#241)\nline: %s", banned, l)
			}
		}
		if !strings.Contains(login, "ORBIT_REGISTRY") {
			t.Errorf("the `docker login` must log in to \"$ORBIT_REGISTRY\", the registry install.sh will pull from (#241)\nline: %s", l)
		}
	}
	if !found {
		t.Error("after `export ORBIT_RELEASE_MANIFEST=`, the `live` job's preview block must `docker login` " +
			"to the manifest's registry: the GitLab registry needs the job token to be pulled from (#241)")
	}
}

// The stable route must stay on install.sh's default registry (ghcr.io), so
// the exports and the login appear once, in the preview block, and nowhere
// else in the job.
func TestLiveRegistryOverrideStaysInThePreviewBlock(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	block := previewBlockAfterManifestExport(t, ci)
	job := liveJob(t, ci, "live")

	count := func(lines []string, prefix string) int {
		n := 0
		for _, l := range lines {
			if strings.HasPrefix(l, prefix) || (prefix == "docker login" && strings.Contains(l, "docker login")) {
				n++
			}
		}
		return n
	}
	for _, prefix := range []string{"export ORBIT_REGISTRY=", "export ORBIT_REPOSITORY=", "docker login"} {
		inBlock, inJob := count(block, prefix), count(job, prefix)
		if inBlock != 1 {
			t.Errorf("the preview block (after `export ORBIT_RELEASE_MANIFEST=`) must contain %q exactly once, found %d (#241)", prefix, inBlock)
		}
		if inJob != inBlock {
			t.Errorf("%q appears %d time(s) in the `live` job but %d in the preview block: "+
				"the stable route must stay on the default registry (#241)", prefix, inJob, inBlock)
		}
	}
}
