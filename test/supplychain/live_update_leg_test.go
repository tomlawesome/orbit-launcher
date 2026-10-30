// The live job's earlier release for the update leg (#198). The leg installs
// an earlier Orbit preview release first, then updates to the current one,
// so the update goes from one signed image to another. The job reads that
// release's manifest and signature from the previous build ai/orbit's
// record_image keeps under .orbit-supply-chain/previous/ (#263), or by job
// ID when a hand-started pipeline names two, verifies them exactly as it
// verifies the current manifest (#194), and takes install.sh from the commit
// the manifest names only if its sha256 matches the one the signed manifest
// records, so script and image stay one generation. Since #303 the reads
// and checks are `live-prepare`'s and the `live` leg exports what it hands
// over.
//
// Read as text, for the reason ci_rules_test.go gives.
package supplychain

import (
	"regexp"
	"strings"
	"testing"
)

func TestLiveUpdateLegGetsAVerifiedEarlierRelease(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	text := strings.Join(job, "\n")
	leg := strings.Join(liveJob(t, ci, "live"), "\n")
	for _, c := range []struct{ want, why string }{
		{"export ORBIT_LIVE_FIRST_RELEASE_MANIFEST=",
			"hand the leg the verified earlier manifest"},
		{"export ORBIT_LIVE_FIRST_INSTALL_SCRIPT=",
			"hand the leg the checked earlier install.sh"},
	} {
		if !strings.Contains(leg, c.want) {
			t.Errorf("the `live` job must contain %q: %s (#198, #303)", c.want, c.why)
		}
	}

	for _, c := range []struct{ want, why string }{
		{"/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json?job=record_image",
			"read the previous build's manifest by branch from record_image by default (#263)"},
		{"/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json.sig?job=record_image",
			"read its signature from record_image too (#263)"},
		{`echo "update leg: previous build ${first_version} ${first_digest} from previous/"`,
			"say which predecessor it installed, in a line a watcher can grep (#263)"},
		{`echo "update leg: previous build from job IDs ${first_record}/${first_sign}"`,
			"say when the job IDs overrode it, in a line a watcher can grep (#263)"},
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
	} {
		if !strings.Contains(text, c.want) {
			t.Errorf("the `live-prepare` job must contain %q: %s (#198, #303)", c.want, c.why)
		}
	}

	// Each refusal stops the job. A request for an update leg that cannot
	// be met must not turn into a quiet skip of that leg.
	for _, refusal := range []string{
		"must both be ai/orbit job IDs",
		`{ echo "$no_predecessor"; exit 1; }`,
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
			t.Errorf("the `live-prepare` job must refuse with %q (#198, #303)", refusal)
		}
	}

	// No predecessor fails the job (#263): the old quiet skip is gone, and
	// the failure says how to override with the two job IDs.
	if strings.Contains(text, "the update leg (#198) skips") {
		t.Error("the `live-prepare` job must not skip the update leg any more: no readable predecessor fails it (#263)")
	}
	if !strings.Contains(text, `no_predecessor="ai/orbit's latest ${orbit_branch} build has no readable predecessor`) ||
		!strings.Contains(text, "start the pipeline by hand with ORBIT_FIRST_RECORD_IMAGE_JOB and ORBIT_FIRST_SIGN_EVIDENCE_JOB") {
		t.Error("the `live-prepare` job's no-predecessor failure must name ai/orbit's latest build and the job-ID override (#263)")
	}

	// The Go leg reads the exports by these names; a rename on one side
	// alone would skip the leg for ever without failing anything.
	goLeg := readRepoFile(t, "test", "live", "update_test.go")
	for _, name := range []string{`"ORBIT_LIVE_FIRST_RELEASE_MANIFEST"`, `"ORBIT_LIVE_FIRST_INSTALL_SCRIPT"`} {
		if !strings.Contains(goLeg, name) {
			t.Errorf("test/live/update_test.go must read %s, the name the `live` job exports (#198)", name)
		}
	}
}

// The job IDs are an override only (#263, note 40743's Low). A numeric
// default in the pipeline's or the live job's variables would pin the leg
// to one old build whose artifacts expire, and silently replace previous/.
func TestLiveUpdateLegJobIDsHaveNoDefault(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	live, err := doc.effectiveJob("live")
	if err != nil {
		t.Fatal(err)
	}
	prep, err := doc.effectiveJob("live-prepare")
	if err != nil {
		t.Fatal(err)
	}
	numeric := regexp.MustCompile(`[0-9]`)
	for where, vars := range map[string]*yamlNode{
		"the top-level variables":          doc.root.vals["variables"],
		"the live job's variables":         live.Keys["variables"],
		"the live-prepare job's variables": prep.Keys["variables"],
	} {
		if where == "the live-prepare job's variables" && vars == nil {
			continue // no variables of its own is no default either
		}
		if vars == nil || vars.kind != 'm' {
			t.Fatalf("%s did not read as a mapping", where)
		}
		for _, name := range []string{"ORBIT_FIRST_RECORD_IMAGE_JOB", "ORBIT_FIRST_SIGN_EVIDENCE_JOB"} {
			if v, ok := vars.vals[name]; ok && numeric.MatchString(v.dump()) {
				t.Errorf("%s give %s a default, %s: the job IDs are an override set on a hand-started pipeline (#263)", where, name, v.dump())
			}
		}
	}
}
