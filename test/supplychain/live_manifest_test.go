// The live job's preview install (#194). Orbit's install.sh no longer fetches
// a preview release manifest on its own (ai/orbit#1107, ADR-0031): a preview
// install needs one handed over, already verified, in ORBIT_RELEASE_MANIFEST.
// Since #303 two jobs share that handover: `live-prepare`, unprivileged and
// holding the read token, reads the manifest and its signature from
// ai/orbit's preview pipeline and verifies them with Orbit's own verifier;
// the `live` leg, which holds no token worth taking, exports the path to the
// files it hands over. Orbit's GitHub mirror carries no pipeline artifacts,
// so a preview run that could only reach the mirror must fail, never skip
// (#164).
//
// Read as text, for the reason ci_rules_test.go gives.
package supplychain

import (
	"strings"
	"testing"
)

// liveJob returns a live job's lines, from `<name>:` to the next top-level
// key, with comment lines and blank lines dropped and indentation trimmed.
func liveJob(t *testing.T, ciYAML, name string) []string {
	t.Helper()
	var job []string
	for _, l := range liveJobLines(t, ciYAML, name) {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		job = append(job, trimmed)
	}
	if len(job) == 0 {
		t.Fatalf("the `%s` job is empty", name)
	}
	return job
}

func TestLivePreviewInstallGetsAVerifiedManifest(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	if !strings.Contains(strings.Join(liveJob(t, ci, "live"), "\n"), "export ORBIT_RELEASE_MANIFEST=") {
		t.Error("the `live` job must contain \"export ORBIT_RELEASE_MANIFEST=\": hand install.sh the verified manifest: it refuses to self-fetch preview (ai/orbit#1107)")
	}
	job := liveJob(t, ci, "live-prepare")
	text := strings.Join(job, "\n")

	for _, c := range []struct{ want, why string }{
		{"verify-release-manifest.sh",
			"verify the manifest with Orbit's own verifier before handing it over"},
		{"/raw/.orbit-supply-chain/orbit-release-manifest.json?job=record_image",
			"read the manifest from Orbit's record_image job artifacts"},
		{"/raw/.orbit-supply-chain/orbit-release-manifest.json.sig?job=sign_evidence",
			"read the signature from Orbit's sign_evidence job artifacts"},
	} {
		if !strings.Contains(text, c.want) {
			t.Errorf("the `live-prepare` job must contain %q: %s (#194, #303)", c.want, c.why)
		}
	}

	installsOpenssl := false
	for _, l := range job {
		if strings.Contains(l, "apt-get install") && strings.Contains(l+" ", " openssl ") {
			installsOpenssl = true
		}
	}
	if !installsOpenssl {
		t.Error("the `live-prepare` job must apt-get install openssl: verify-release-manifest.sh checks the signature with openssl as well as cosign (#194, #303)")
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
		t.Errorf("the `live-prepare` job must refuse a preview run whose Orbit files come from the GitHub mirror, saying %q (#194, #303)", refusal)
	}
}

// The branch tip's install.sh must be the one the signed preview release
// records (#236). The job takes install.sh from the tip of ORBIT_BRANCH but
// the manifest from the branch's latest preview pipeline, which trails the
// tip by minutes; without this check a run in that gap pairs a newer
// install.sh with the previous commit's image. Since #303 `live-prepare`
// makes the check, and a mismatch stops it before it hands anything over;
// `live` exports the manifest only after checking that live-prepare handed
// it over.
func TestLivePreviewInstallScriptMatchesTheManifest(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	const (
		hash     = `sha256sum .live/install-script/install.sh`
		recorded = `jq -r '.files["install.sh"]' .live/orbit/release/orbit-release-manifest.json`
		refusal  = "is not the one the latest signed preview release records"
		handover = "dotenv"
	)
	check, stop, envFile := -1, -1, -1
	for i, l := range job {
		switch {
		case strings.Contains(l, hash) && strings.Contains(l, recorded):
			check = i
		case strings.Contains(l, refusal):
			stop = i
			if !strings.Contains(l, "exit 1") {
				t.Errorf("the install.sh/manifest refusal must exit 1 (#236)\nline: %s", l)
			}
		case strings.Contains(l, "> .live/prepare.env") && envFile < 0:
			envFile = i
		}
	}
	if check < 0 {
		t.Fatalf("the `live-prepare` job must compare %q with %q before handing anything over (#236, #303)", hash, recorded)
	}
	if stop != check+1 {
		t.Errorf("the line after the install.sh/manifest comparison must refuse with %q (#236)", refusal)
	}
	if envFile < 0 || check > envFile {
		t.Errorf("the install.sh/manifest comparison must come before live-prepare writes .live/prepare.env, its %s handover (#236, #303)", handover)
	}

	leg := liveJob(t, ci, "live")
	handed, export := -1, -1
	for i, l := range leg {
		switch {
		case strings.Contains(l, "live-prepare did not hand over") && strings.Contains(l, "exit 1") && handed < 0:
			handed = i
		case strings.HasPrefix(l, "export ORBIT_RELEASE_MANIFEST="):
			export = i
		}
	}
	if handed < 0 || export < 0 || handed > export {
		t.Error("the `live` job must stop, saying live-prepare did not hand over the files, before `export ORBIT_RELEASE_MANIFEST=` (#303)")
	}
}

// Where the tokens reach (#193, #263, #303). Orbit's verifier is Orbit's own
// code fetched at run time, so it runs with the job token and the read token
// stripped from its environment; the Orbit branch must be a plain name before
// any URL is built from it; and the registry login goes only to this
// GitLab's registry.
func TestLiveJobTokenStaysWhereItIsNeeded(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	prep := liveJob(t, ci, "live-prepare")
	verifies, branchCheck, firstURL := 0, -1, -1
	for i, l := range prep {
		switch {
		case strings.HasPrefix(l, "bash .live/orbit/scripts/ci/verify-release-manifest.sh"):
			verifies++
			if i == 0 || !strings.HasPrefix(prep[i-1], "env -u CI_JOB_TOKEN ") || !strings.Contains(prep[i-1], " -u ORBIT_READ_TOKEN ") {
				t.Errorf("verify-release-manifest.sh must run under `env -u CI_JOB_TOKEN ... -u ORBIT_READ_TOKEN ...` (#303)\nline: %s", l)
			}
		case strings.HasPrefix(l, `orbit_branch_ok "$ORBIT_BRANCH"`) && branchCheck < 0:
			branchCheck = i
		case strings.Contains(l, "CI_API_V4_URL") && firstURL < 0:
			firstURL = i
		}
	}
	if verifies != 2 {
		t.Errorf("found %d verify-release-manifest.sh runs in `live-prepare`, want 2 (the current and the earlier release)", verifies)
	}
	if branchCheck < 0 || firstURL < 0 || branchCheck > firstURL {
		t.Error("the `live-prepare` job must check ORBIT_BRANCH with orbit_branch_ok before it builds any API URL (#193)")
	}

	login, registryCheck := -1, -1
	for i, l := range liveJob(t, ci, "live") {
		switch {
		case strings.Contains(l, `[ "$ORBIT_REGISTRY" = "$CI_REGISTRY" ]`) && strings.Contains(l, "exit 1"):
			registryCheck = i
		case strings.Contains(l, "docker login"):
			login = i
		}
	}
	if registryCheck < 0 || login < 0 || registryCheck > login {
		t.Error("the `live` job must refuse a manifest registry other than $CI_REGISTRY before `docker login` (#193)")
	}
}
