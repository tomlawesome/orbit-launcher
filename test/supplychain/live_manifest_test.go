// The live job's preview install (#194). Orbit's install.sh no longer fetches
// a preview release manifest on its own (ai/orbit#1107, ADR-0031): a preview
// install needs one handed over, already verified, in ORBIT_RELEASE_MANIFEST.
// The job does that handover: it reads the manifest and its signature from
// ai/orbit's preview pipeline, verifies them with Orbit's own verifier, and
// exports the path. Orbit's GitHub mirror carries no pipeline artifacts, so a
// preview run that could only reach the mirror must fail, never skip (#164).
//
// Read as text, for the reason ci_rules_test.go gives.
package supplychain

import (
	"strings"
	"testing"
)

// liveJob returns the `live` job's lines, from `live:` to the next top-level
// key, with comment lines and blank lines dropped and indentation trimmed.
func liveJob(t *testing.T, ciYAML string) []string {
	t.Helper()
	var job []string
	for _, l := range liveJobLines(t, ciYAML) {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		job = append(job, trimmed)
	}
	if len(job) == 0 {
		t.Fatal("the `live` job is empty")
	}
	return job
}

func TestLivePreviewInstallGetsAVerifiedManifest(t *testing.T) {
	job := liveJob(t, readRepoFile(t, ".gitlab-ci.yml"))
	text := strings.Join(job, "\n")

	for _, c := range []struct{ want, why string }{
		{"export ORBIT_RELEASE_MANIFEST=",
			"hand install.sh the verified manifest: it refuses to self-fetch preview (ai/orbit#1107)"},
		{"verify-release-manifest.sh",
			"verify the manifest with Orbit's own verifier before handing it over"},
		{"/raw/.orbit-supply-chain/orbit-release-manifest.json?job=record_image",
			"read the manifest from Orbit's record_image job artifacts"},
		{"/raw/.orbit-supply-chain/orbit-release-manifest.json.sig?job=sign_evidence",
			"read the signature from Orbit's sign_evidence job artifacts"},
	} {
		if !strings.Contains(text, c.want) {
			t.Errorf("the `live` job must contain %q: %s (#194)", c.want, c.why)
		}
	}

	installsOpenssl := false
	for _, l := range job {
		if strings.Contains(l, "apt-get install") && strings.Contains(l+" ", " openssl ") {
			installsOpenssl = true
		}
	}
	if !installsOpenssl {
		t.Error("the `live` job must apt-get install openssl: verify-release-manifest.sh checks the signature with openssl as well as cosign (#194)")
	}

	const refusal = "the preview release manifest cannot be read from"
	found := false
	for i, l := range job {
		if !strings.Contains(l, refusal) {
			continue
		}
		found = true
		prev := ""
		if i > 0 {
			prev = job[i-1]
		}
		if !strings.Contains(prev, `"$orbit_source" = "github"`) {
			t.Errorf("the preview-manifest refusal must sit directly under the github-source check, got %q before it", prev)
		}
		if i+1 >= len(job) || job[i+1] != "exit 1" {
			t.Errorf("the preview-manifest refusal must be followed by `exit 1`: "+
				"an image bump must never go green uninstalled (#164)\nline: %s", l)
		}
	}
	if !found {
		t.Errorf("the `live` job must refuse a preview run whose Orbit files come from the GitHub mirror, saying %q (#194)", refusal)
	}
}

// The branch tip's install.sh must be the one the signed preview release
// records (#236). The job takes install.sh from the tip of ORBIT_BRANCH but
// the manifest from the branch's latest preview pipeline, which trails the
// tip by minutes; without this check a run in that gap pairs a newer
// install.sh with the previous commit's image. The check sits before the
// manifest is handed over, and a mismatch stops the job.
func TestLivePreviewInstallScriptMatchesTheManifest(t *testing.T) {
	job := liveJob(t, readRepoFile(t, ".gitlab-ci.yml"))
	const (
		hash     = `sha256sum .live/install-script/install.sh`
		recorded = `jq -r '.files["install.sh"]' .live/orbit/release/orbit-release-manifest.json`
		refusal  = "is not the one the latest signed preview release records"
	)
	check, stop, export := -1, -1, -1
	for i, l := range job {
		switch {
		case strings.Contains(l, hash) && strings.Contains(l, recorded):
			check = i
		case strings.Contains(l, refusal):
			stop = i
			if !strings.Contains(l, "exit 1") {
				t.Errorf("the install.sh/manifest refusal must exit 1 (#236)\nline: %s", l)
			}
		case strings.HasPrefix(l, "export ORBIT_RELEASE_MANIFEST="):
			export = i
		}
	}
	if check < 0 {
		t.Fatalf("the `live` job must compare %q with %q before installing (#236)", hash, recorded)
	}
	if stop != check+1 {
		t.Errorf("the line after the install.sh/manifest comparison must refuse with %q (#236)", refusal)
	}
	if export < 0 || check > export {
		t.Error("the install.sh/manifest comparison must come before `export ORBIT_RELEASE_MANIFEST=` (#236)")
	}
}

// Where the job token reaches (#193, #263). Orbit's verifier is Orbit's own
// code fetched at run time, so it runs with the token stripped from its
// environment; the registry login sends the token only to this GitLab's
// registry; and the Orbit branch must be a plain name before any URL is built
// from it.
func TestLiveJobTokenStaysWhereItIsNeeded(t *testing.T) {
	job := liveJob(t, readRepoFile(t, ".gitlab-ci.yml"))
	verifies, login, registryCheck, branchCheck, firstURL := 0, -1, -1, -1, -1
	for i, l := range job {
		switch {
		case strings.HasPrefix(l, "bash .live/orbit/scripts/ci/verify-release-manifest.sh"):
			verifies++
			if i == 0 || !strings.HasPrefix(job[i-1], "env -u CI_JOB_TOKEN ") {
				t.Errorf("verify-release-manifest.sh must run under `env -u CI_JOB_TOKEN ...`\nline: %s", l)
			}
		case strings.Contains(l, `[ "$ORBIT_REGISTRY" = "$CI_REGISTRY" ]`) && strings.Contains(l, "exit 1"):
			registryCheck = i
		case strings.Contains(l, "docker login"):
			login = i
		case strings.HasPrefix(l, `orbit_branch_ok "$ORBIT_BRANCH"`) && branchCheck < 0:
			branchCheck = i
		case strings.Contains(l, "CI_API_V4_URL") && firstURL < 0:
			firstURL = i
		}
	}
	if verifies != 2 {
		t.Errorf("found %d verify-release-manifest.sh runs, want 2 (the current and the earlier release)", verifies)
	}
	if registryCheck < 0 || login < 0 || registryCheck > login {
		t.Error("the `live` job must refuse a manifest registry other than $CI_REGISTRY before `docker login` (#193)")
	}
	if branchCheck < 0 || firstURL < 0 || branchCheck > firstURL {
		t.Error("the `live` job must check ORBIT_BRANCH with orbit_branch_ok before it builds any API URL (#193)")
	}
}
