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
	lines := strings.Split(ciYAML, "\n")

	start := -1
	for i, l := range lines {
		if l == "live:" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("no top-level `live:` job in .gitlab-ci.yml")
	}

	var job []string
	for _, l := range lines[start+1:] {
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
			break // next top-level key: the job has ended
		}
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
