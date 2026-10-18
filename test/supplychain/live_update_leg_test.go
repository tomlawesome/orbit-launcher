// The live job's earlier release for the update leg (#198). The leg installs
// an earlier Orbit preview release first, then updates to the current one,
// so the update goes from one signed image to another. The job reads that
// release's manifest and signature from ai/orbit by job ID, verifies them
// exactly as it verifies the current manifest (#194), and takes install.sh
// from the commit the manifest names only if its sha256 matches the one the
// signed manifest records, so script and image stay one generation.
//
// Read as text, for the reason ci_rules_test.go gives.
package supplychain

import (
	"strings"
	"testing"
)

func TestLiveUpdateLegGetsAVerifiedEarlierRelease(t *testing.T) {
	job := liveJob(t, readRepoFile(t, ".gitlab-ci.yml"))
	text := strings.Join(job, "\n")

	for _, c := range []struct{ want, why string }{
		{"/jobs/${first_record}/artifacts/.orbit-supply-chain/orbit-release-manifest.json",
			"read the earlier manifest from the record_image job named by ID"},
		{"/jobs/${first_sign}/artifacts/.orbit-supply-chain/orbit-release-manifest.json.sig",
			"read its signature from the sign_evidence job named by ID"},
		{`bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json"`,
			"verify the earlier manifest with Orbit's own verifier, as the current one is"},
		{`/repository/files/scripts%2Finstall.sh/raw?ref=${first_commit}`,
			"take install.sh from the commit the signed manifest names"},
		{`jq -r '.files["install.sh"]'`,
			"check that install.sh against the sha256 the signed manifest records"},
		{"export ORBIT_LIVE_FIRST_RELEASE_MANIFEST=",
			"hand the leg the verified earlier manifest"},
		{"export ORBIT_LIVE_FIRST_INSTALL_SCRIPT=",
			"hand the leg the checked earlier install.sh"},
	} {
		if !strings.Contains(text, c.want) {
			t.Errorf("the `live` job must contain %q: %s (#198)", c.want, c.why)
		}
	}

	// Each refusal stops the job. A request for an update leg that cannot
	// be met must not turn into a quiet skip of that leg.
	for _, refusal := range []string{
		"must both be ai/orbit job IDs",
		"failed verification; nothing is installed from it",
		"is not a preview release",
		"names the same image as the current one",
		"is not older than the current one",
		"names no source commit",
		"does not match the sha256 the earlier release manifest records",
	} {
		found := false
		for i, l := range job {
			if !strings.Contains(l, refusal) {
				continue
			}
			found = true
			next := ""
			if i+1 < len(job) {
				next = job[i+1]
			}
			if !strings.Contains(l, "exit 1") && !strings.HasPrefix(next, "exit 1") {
				t.Errorf("the refusal %q must exit 1\nline: %s", refusal, l)
			}
		}
		if !found {
			t.Errorf("the `live` job must refuse with %q (#198)", refusal)
		}
	}

	// The Go leg reads the exports by these names; a rename on one side
	// alone would skip the leg for ever without failing anything.
	leg := readRepoFile(t, "test", "live", "update_test.go")
	for _, name := range []string{`"ORBIT_LIVE_FIRST_RELEASE_MANIFEST"`, `"ORBIT_LIVE_FIRST_INSTALL_SCRIPT"`} {
		if !strings.Contains(leg, name) {
			t.Errorf("test/live/update_test.go must read %s, the name the `live` job exports (#198)", name)
		}
	}
}
