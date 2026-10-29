// The GitLab pipeline's repeated boilerplate lives in one place (#217, from
// #199 WI-13, findings SU-13 and SU-18): the docs-only skip and the apt
// prologue are YAML anchors, the image pull policy is written once in a
// hidden template that each job's image merges in with `<<:`, and the two
// live-job scanners share one `liveJobLines` helper. None of it may change
// what any job runs or when, so every job keeps its name, its effective
// script and every other key -- compared with the file as it stood at
// ce4b412, kept in testdata/ and read by ci_miniyaml_test.go with anchors,
// merge keys, `extends:` and `default:` resolved. The only differences are
// the ones #216, #198, #241, #273, #264, #236, #195, #292, #277, #193 and
// #263 make on purpose, listed in changedBy216, changedBy198, changedBy241,
// changedBy273, changedBy264, changedBy236, changedBy195, changedBy292,
// changedBy277, changedBy193, variablesChangedBy193 and changedBy263, and
// the job-token review's changedByTokenReview; and #183's changedBy183,
// which also adds the live-fedora job (addedBy183) and FEDORA_IMAGE; and
// #306's changedBy306 and workflowChangedBy306, which also add the promoted
// job (addedBy306).
package supplychain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
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

// What #264 does to the two release jobs in that list: release:version and
// release:gitlab are gone, and each is replaced by a pair for a patch and a
// minor button on main (releaseJobsAfter264, keyed by the old job they
// descend from). topLevelKeysAtCE4B412 stays as the fixture has it; every
// check that reads it knows about this one rename and nothing else.
var releaseJobsAfter264 = map[string][]string{
	"release:version": {"release:patch", "release:minor"},
	"release:gitlab":  {"release:gitlab:patch", "release:gitlab:minor"},
}

// releaseTemplatesAdded264 are the hidden templates that hold what the four
// release jobs share, so none of it is copied.
var releaseTemplatesAdded264 = []string{".release_version", ".release_gitlab"}

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

// What #245 adds on purpose: the shared private-details check from
// ai/agent-infra, brought in by `include:` and run as the template's
// `private-details` job, which .gitlab-ci.yml gives this pipeline's stage,
// runner, pull policy and history start point.
var addedBy245 = []string{"include", "private-details"}

// What #303 adds on purpose: live-prepare, the unprivileged job that reads
// and verifies Orbit's files for the two live legs with ORBIT_READ_TOKEN, so
// the privileged legs hold no credential worth taking (live_prepare_test.go
// checks it).
var addedBy303 = []string{"live-prepare"}

// What #183 adds on purpose: the Fedora leg of the live test, a second job
// extending the same .live_leg template as `live` (live_fedora_test.go checks
// that it differs from `live` only in its image, LIVE_DISTRO and package
// step).
var addedBy183 = []string{"live-fedora"}

// What #306 adds on purpose: the one cheap job a merge request into main
// runs, so "pipelines must succeed" has a pipeline to pass when the
// promotion into main otherwise runs nothing (ci_stages_test.go checks it).
var addedBy306 = []string{"promoted"}

// What #310 adds on purpose: flake-check, the blocking job that runs the
// shared flake check from ai/agent-infra on docs/flakes.md in the first stage
// (flake_check_test.go checks it). It is a new job, so no fixture job's lines
// change and no changedBy310 rewrite is needed.
var addedBy310 = []string{"flake-check"}

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
		if renamed, ok := releaseJobsAfter264[k]; ok {
			// #264 retires this job; its successors are named, below.
			if have[k] {
				t.Errorf("top-level key %q is still in .gitlab-ci.yml: #264 replaces it with %v", k, renamed)
			}
			for _, n := range renamed {
				typed[n] = true
				if !have[n] {
					t.Errorf("%q, which #264 makes from %q, is missing from .gitlab-ci.yml", n, k)
				}
			}
			continue
		}
		if !have[k] {
			t.Errorf("top-level key %q from ce4b412 is gone from .gitlab-ci.yml: job names must not change (#217)", k)
		}
	}
	for _, k := range addedBy245 {
		typed[k] = true
		if !have[k] {
			t.Errorf("%q, which #245 adds, is missing from .gitlab-ci.yml", k)
		}
	}
	for _, k := range addedBy183 {
		typed[k] = true
		if !have[k] {
			t.Errorf("%q, which #183 adds, is missing from .gitlab-ci.yml", k)
		}
	}
	for _, k := range addedBy303 {
		typed[k] = true
		if !have[k] {
			t.Errorf("%q, which #303 adds, is missing from .gitlab-ci.yml", k)
		}
	}
	for _, k := range addedBy306 {
		typed[k] = true
		if !have[k] {
			t.Errorf("%q, which #306 adds, is missing from .gitlab-ci.yml", k)
		}
	}
	for _, k := range addedBy310 {
		typed[k] = true
		if !have[k] {
			t.Errorf("%q, which #310 adds, is missing from .gitlab-ci.yml", k)
		}
	}
	for _, k := range doc.root.keys {
		if !typed[k] && !strings.HasPrefix(k, ".") {
			t.Errorf("new top-level key %q is not a hidden template (it would be a new job): the restructure may add only `.`-prefixed templates (#217)", k)
		}
	}
	for _, k := range releaseTemplatesAdded264 {
		if !have[k] {
			t.Errorf("the hidden template %q that #264's release jobs share is missing", k)
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
		o, n := oldDoc.root.vals[k].dump(), newDoc.root.vals[k].dump()
		if k == "workflow" {
			o = workflowChangedBy306
		}
		if o != n {
			t.Errorf("top-level %q changed (#217)\nwant: %s\n got: %s", k, o, n)
		}
	}
	compareTopLevelVariables(t, oldDoc, newDoc)

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
		// A job #264 split is compared once for each job it became, so
		// every successor must differ from the old job only by what
		// changedBy264 declares for it.
		targets := []string{name}
		if renamed, ok := releaseJobsAfter264[name]; ok {
			targets = renamed
		}
		for _, target := range targets {
			got, err := newDoc.effectiveJob(target)
			if err != nil {
				t.Errorf("%s: %v", target, err)
				continue
			}
			compared++
			wantKeys := map[string]string{}
			for k, v := range want.Keys {
				wantKeys[k] = jobKeyDump(k, v)
			}
			wantKeys["script"] = want.Script
			applyChangedOnPurpose(t, target, wantKeys)
			gotKeys := map[string]string{"script": got.Script}
			for k, v := range got.Keys {
				gotKeys[k] = jobKeyDump(k, v)
			}
			for _, k := range unionOfKeys(wantKeys, gotKeys) {
				w, okW := wantKeys[k]
				g, okG := gotKeys[k]
				switch {
				case !okG:
					t.Errorf("job %q: %s is gone (#217)\nwas: %s", target, k, w)
				case !okW:
					t.Errorf("job %q: %s is new (#217)\nnow: %s", target, k, g)
				case w != g && k == "script":
					t.Errorf("job %q: effective script changed (#217)\nwant:\n%s\n got:\n%s", target, w, g)
				case w != g:
					t.Errorf("job %q: effective %s changed (#217)\nwant: %s\n got: %s", target, k, w, g)
				}
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

// isToolPinVariable reports whether a top-level variable is a tool pin that
// Renovate or a toolchain bump moves: an image, a version or a digest. Those
// are checked against GitHub's copies in ci_hosts_agree_test.go instead.
func isToolPinVariable(name string) bool {
	for _, suffix := range []string{"_IMAGE", "_VERSION", "_SHA256"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// compareTopLevelVariables checks the pipeline's `variables:` against the
// ce4b412 fixture: the same names, and the same value for every one that is
// not a tool pin. ORBIT_CHANNEL, ORBIT_PROJECT and the rest decide whether the
// update leg and the manifest check run, so a merge slip there must not pass
// quietly (#236).
func compareTopLevelVariables(t *testing.T, oldDoc, newDoc *yamlDoc) {
	t.Helper()
	oldV, newV := oldDoc.root.vals["variables"], newDoc.root.vals["variables"]
	if oldV == nil || oldV.kind != 'm' {
		t.Fatal("the fixture has no top-level variables mapping: the reader lost it")
	}
	if newV == nil || newV.kind != 'm' {
		t.Fatal(".gitlab-ci.yml has no top-level variables mapping")
	}
	names := map[string]string{}
	for _, m := range []*yamlNode{oldV, newV} {
		for _, k := range m.keys {
			names[k] = ""
		}
	}
	compared := 0
	for _, k := range unionOfKeys(names, nil) {
		o, inOld := oldV.vals[k]
		n, inNew := newV.vals[k]
		switch {
		case !inNew:
			t.Errorf("top-level variable %s is gone (#236)\nwas: %s", k, o.dump())
		case !inOld && k == variableAddedBy183:
			// the Fedora leg's image, a tool pin like UBUNTU_IMAGE
		case !inOld:
			t.Errorf("top-level variable %s is new (#236)\nnow: %s", k, n.dump())
		case isToolPinVariable(k):
		case variablesChangedBy193[k].new != "":
			if w := variablesChangedBy193[k]; o.dump() != w.old || n.dump() != w.new {
				t.Errorf("top-level variable %s should change from %s to %s (#193)\n got: %s -> %s", k, w.old, w.new, o.dump(), n.dump())
			}
		case o.dump() != n.dump():
			t.Errorf("top-level variable %s changed (#236)\nwant: %s\n got: %s", k, o.dump(), n.dump())
		default:
			compared++
		}
	}
	if compared < 5 {
		t.Fatalf("compared only %d non-pin variables: the reader is not finding them", compared)
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
// retired bootstrap script to every script. live's and visual's change
// routes also watch the checked-download script their Go tarball now comes
// through, so a change to that script alone still starts them. Each rewrite names its jobs and
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
	{
		jobs: []string{"live"},
		key:  "rules",
		old:  `"changes": [".gitlab-ci.yml"]`,
		new:  `"changes": [".gitlab-ci.yml", "scripts/ci/fetch-verified.sh"]`,
	},
	{
		jobs: []string{"visual"},
		key:  "rules",
		old:  `"changes": ["internal/ui/**/*", "test/visual/**/*", ".gitlab-ci.yml"]`,
		new:  `"changes": ["internal/ui/**/*", "test/visual/**/*", ".gitlab-ci.yml", "scripts/ci/fetch-verified.sh"]`,
	},
}

// What #198 changes on purpose: the live job fetches and verifies an earlier
// preview release for the update leg's first install, straight after it
// verifies the current one. Same form and the same exactly-once rule as
// changedBy216.
var changedBy198 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		old:  `  echo "the verified preview release manifest names image $(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST"); install.sh installs that"`,
		new: `  echo "the verified preview release manifest names image $(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST"); install.sh installs that"
  # The update leg's first install (#198): an earlier preview release,
  # so the update goes from one signed image to another. Its manifest
  # and signature are read by job ID, since a branch only ever names
  # its latest pipeline; the IDs come from whoever starts the pipeline
  # (ORBIT_FIRST_RECORD_IMAGE_JOB, ORBIT_FIRST_SIGN_EVIDENCE_JOB),
  # because this job's token cannot list ai/orbit's pipelines.
  # Verified exactly as above. Its install.sh comes from the commit the
  # manifest names and must match the sha256 the manifest records for
  # it, so script and image stay one generation. Unset, the leg skips.
  first_record="${ORBIT_FIRST_RECORD_IMAGE_JOB:-}"
  first_sign="${ORBIT_FIRST_SIGN_EVIDENCE_JOB:-}"
  if [ -z "$first_record" ] && [ -z "$first_sign" ]; then
    echo "no ORBIT_FIRST_RECORD_IMAGE_JOB / ORBIT_FIRST_SIGN_EVIDENCE_JOB: the update leg (#198) skips"
  else
    for job_id in "$first_record" "$first_sign"; do
      case "$job_id" in
        ''|*[!0-9]*) echo "ORBIT_FIRST_RECORD_IMAGE_JOB and ORBIT_FIRST_SIGN_EVIDENCE_JOB must both be ai/orbit job IDs (#198)"; exit 1 ;;
      esac
    done
    first_dir=.live/orbit/release/first
    mkdir -p "$first_dir"
    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_record}/artifacts/.orbit-supply-chain/orbit-release-manifest.json" \
      || { echo "could not read the earlier release manifest from ai/orbit job ${first_record} (#198)"; exit 1; }
    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_sign}/artifacts/.orbit-supply-chain/orbit-release-manifest.json.sig" \
      || { echo "could not read the earlier release signature from ai/orbit job ${first_sign} (#198)"; exit 1; }
    jq -e . "$first_dir/orbit-release-manifest.json" > /dev/null || { echo "ai/orbit job ${first_record} did not return a JSON release manifest (#198)"; exit 1; }
    [ -s "$first_dir/orbit-release-manifest.json.sig" ] || { echo "ai/orbit job ${first_sign} returned an empty signature (#198)"; exit 1; }
    COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
      bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json" \
      || { echo "the earlier release manifest from ai/orbit jobs ${first_record}/${first_sign} failed verification; nothing is installed from it (#198)"; exit 1; }
    jq -e '.channel == "preview"' "$first_dir/orbit-release-manifest.json" > /dev/null \
      || { echo "the earlier release manifest is not a preview release (#198)"; exit 1; }
    first_digest="$(jq -r .image.digest "$first_dir/orbit-release-manifest.json")"
    [ "$first_digest" != "$(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST")" ] \
      || { echo "the earlier release names the same image as the current one, ${first_digest}; an update between them proves nothing (#198)"; exit 1; }
    first_at="$(jq -r .recordedAt "$first_dir/orbit-release-manifest.json")"
    current_at="$(jq -r .recordedAt "$ORBIT_RELEASE_MANIFEST")"
    if [ "$first_at" = "$current_at" ] || [ "$(printf '%s\n%s\n' "$first_at" "$current_at" | sort | head -1)" != "$first_at" ]; then
      echo "the earlier release (recorded ${first_at}) is not older than the current one (recorded ${current_at}) (#198)"
      exit 1
    fi
    first_commit="$(jq -r .commit "$first_dir/orbit-release-manifest.json")"
    case "$first_commit" in
      *[!0-9a-f]*) first_commit="" ;;
    esac
    [ "${#first_commit}" -eq 40 ] || { echo "the earlier release manifest names no source commit (#198)"; exit 1; }
    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/install.sh" \
      "${CI_API_V4_URL}/projects/${project}/repository/files/scripts%2Finstall.sh/raw?ref=${first_commit}" \
      || { echo "could not fetch scripts/install.sh from ${ORBIT_PROJECT} at ${first_commit} (#198)"; exit 1; }
    [ "sha256:$(sha256sum "$first_dir/install.sh" | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' "$first_dir/orbit-release-manifest.json")" ] \
      || { echo "install.sh at ${first_commit} does not match the sha256 the earlier release manifest records for it (#198)"; exit 1; }
    export ORBIT_LIVE_FIRST_RELEASE_MANIFEST="$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json"
    export ORBIT_LIVE_FIRST_INSTALL_SCRIPT="$CI_PROJECT_DIR/$first_dir/install.sh"
    echo "the update leg (#198) installs ${first_digest} (ai/orbit ${first_commit}) first, then updates to the current preview"
  fi`,
	},
}

// What #241 changes on purpose: straight after the live job exports the
// verified preview manifest, it points install.sh at the registry the manifest
// names and logs in to it. Same form and the same exactly-once rule as
// changedBy216.
var changedBy241 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		old:  `  export ORBIT_RELEASE_MANIFEST="$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json"`,
		new: `  export ORBIT_RELEASE_MANIFEST="$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json"
  # Install what the manifest names (#241): its image is in the GitLab
  # registry that built it. install.sh ignores the manifest's
  # repository and defaults to ghcr.io, where the GitHub copy can lag
  # by up to two hours or never arrive. The token goes in on stdin.
  image_repository="$(jq -r .image.repository "$ORBIT_RELEASE_MANIFEST")"
  export ORBIT_REGISTRY="${image_repository%%/*}"
  export ORBIT_REPOSITORY="${image_repository#*/}"
  printf '%s' "$CI_JOB_TOKEN" | docker login "$ORBIT_REGISTRY" --username gitlab-ci-token --password-stdin
  echo "the manifest's image lives at ${image_repository}; install.sh pulls its digest from there, not from the GitHub mirror"`,
	},
}

// What #264 changes on purpose. The two release jobs become four: a patch
// and a minor button on main (not dev), each with its own follow-on tagging
// job, sharing hidden templates. Here `jobs` names the new jobs, whose
// starting point is the retired job they descend from (releaseJobsAfter264).
// The script is the old one with the version step choosing -hotfix by
// RELEASE_BUMP and the tip check reading main and naming it; the follow-on
// jobs differ only in the button they wait for. A rewrite with an empty
// `old` and a key adds that key, and fails if the old job already had it.
// Otherwise the same form and exactly-once rule as changedBy216.
var changedBy264 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{ // the buttons run on main pipelines, the follow-on jobs too
		jobs: []string{"release:patch", "release:minor", "release:gitlab:patch", "release:gitlab:minor"},
		key:  "rules",
		old:  `$CI_COMMIT_BRANCH == \"dev\"`,
		new:  `$CI_COMMIT_BRANCH == \"main\"`,
	},
	{ // shared through the hidden templates
		jobs: []string{"release:patch", "release:minor"},
		key:  "extends",
		new:  `".release_version"`,
	},
	{
		jobs: []string{"release:gitlab:patch", "release:gitlab:minor"},
		key:  "extends",
		new:  `".release_gitlab"`,
	},
	{ // the one thing that tells the two buttons apart
		jobs: []string{"release:patch"},
		key:  "variables",
		new:  `{"RELEASE_BUMP": "patch"}`,
	},
	{
		jobs: []string{"release:minor"},
		key:  "variables",
		new:  `{"RELEASE_BUMP": "minor"}`,
	},
	{ // each follow-on job waits for its own button
		jobs: []string{"release:gitlab:patch"},
		key:  "needs",
		old:  `"job": "release:version"`,
		new:  `"job": "release:patch"`,
	},
	{
		jobs: []string{"release:gitlab:minor"},
		key:  "needs",
		old:  `"job": "release:version"`,
		new:  `"job": "release:minor"`,
	},
	{ // patch takes the hotfix step, minor the ordinary one
		jobs: []string{"release:patch", "release:minor"},
		old:  `version="$(go run ./tools/calculateversion)"`,
		new: `case "$RELEASE_BUMP" in
  patch) version="$(go run ./tools/calculateversion -hotfix)" ;;
  minor) version="$(go run ./tools/calculateversion)" ;;
  *) echo "RELEASE_BUMP is '$RELEASE_BUMP', not patch or minor" >&2; exit 1 ;;
esac`,
	},
	{ // the tip check reads main
		jobs: []string{"release:patch", "release:minor"},
		old:  `dev_sha="$(git ls-remote origin refs/heads/dev | cut -f1)"`,
		new:  `tip_sha="$(git ls-remote origin refs/heads/main | cut -f1)"`,
	},
	{ // and is told which branch that is
		jobs: []string{"release:patch", "release:minor"},
		old:  `bash scripts/ci/release-tag-check.sh "$version" "$remote_tags" "$dev_sha" "$CI_COMMIT_SHA" > release.env`,
		new:  `bash scripts/ci/release-tag-check.sh "$version" "$remote_tags" main "$tip_sha" "$CI_COMMIT_SHA" > release.env`,
	},
	{ // the check is POSIX sh, so the release-cli image can run it too
		jobs: []string{"release:patch", "release:minor"},
		old:  `bash scripts/ci/release-tag-check.sh "$version" "$remote_tags" main "$tip_sha" "$CI_COMMIT_SHA" > release.env`,
		new:  `sh scripts/ci/release-tag-check.sh "$version" "$remote_tags" main "$tip_sha" "$CI_COMMIT_SHA" > release.env`,
	},
	{ // tagging runs one job at a time
		jobs: []string{"release:gitlab:patch", "release:gitlab:minor"},
		key:  "resource_group",
		new:  `"release"`,
	},
	{ // and re-checks under that lock, just before release-cli writes the tag
		jobs: []string{"release:gitlab:patch", "release:gitlab:minor"},
		old:  `echo "tagging $CI_COMMIT_SHA as $RELEASE_TAG"`,
		new: `sh scripts/ci/release-tag-check.sh "${RELEASE_TAG#v}" "$(git ls-remote --tags origin)" main "$(git ls-remote origin refs/heads/main | cut -f1)" "$CI_COMMIT_SHA"
echo "tagging $CI_COMMIT_SHA as $RELEASE_TAG"`,
	},
}

// What #273 changes on purpose: the live job asks ai/orbit's files endpoint
// for the branch by its full ref, so a tag of the same name cannot answer, and
// says why the two artifact reads stay on the plain name. Same form and the
// same exactly-once rule as changedBy216; these apply after #241's.
var changedBy273 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{ // the files lookups name the branch by its full ref
		jobs: []string{"live"},
		old: `gitlab_orbit_url() {
  printf '%s/projects/%s/repository/files/%s/raw?ref=%s' "$CI_API_V4_URL" "$project" "$(printf '%s' "$1" | sed 's#/#%2F#g')" "$ORBIT_BRANCH"
}`,
		new: `# The full ref, so a tag with the same name as the branch cannot answer
# (#273). The files endpoint accepts refs/heads/<branch>.
gitlab_orbit_ref="$(printf 'refs/heads/%s' "$ORBIT_BRANCH" | sed 's#/#%2F#g')"
gitlab_orbit_url() {
  printf '%s/projects/%s/repository/files/%s/raw?ref=%s' "$CI_API_V4_URL" "$project" "$(printf '%s' "$1" | sed 's#/#%2F#g')" "$gitlab_orbit_ref"
}`,
	},
	{ // the artifact reads cannot take a full ref
		jobs: []string{"live"},
		old: `  curl --fail --silent --show-error --location \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json \`,
		new: `  # The artifacts endpoint rejects full refs (404 for refs/heads/...), so
  # these two stay on the plain branch name; the pair is verified below
  # against the cosign.pub fetched by full ref above (#273).
  curl --fail --silent --show-error --location \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json \`,
	},
}

// What #236 changes on purpose: before the live job hands over the verified
// preview manifest, it checks that the branch tip's install.sh is the one the
// manifest records. Same form and the same exactly-once rule as changedBy216;
// these apply last.
var changedBy236 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		old:  `  export ORBIT_RELEASE_MANIFEST="$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json"`,
		new: `  # install.sh comes from the branch tip, the manifest from the branch's
  # latest preview pipeline, which trails the tip by minutes. In that
  # gap the run would pair a newer install.sh with the previous
  # commit's image, so it stops until the two are one release (#236).
  [ "sha256:$(sha256sum .live/install-script/install.sh | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' .live/orbit/release/orbit-release-manifest.json)" ] \
    || { echo "install.sh at the tip of ${ORBIT_BRANCH} is not the one the latest signed preview release records: ai/orbit's preview pipeline has not caught up with the branch yet; rerun this job once it has (#236)"; exit 1; }
  export ORBIT_RELEASE_MANIFEST="$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json"`,
	},
}

// What #195 (T2-Q1) changes on purpose: after the coverage gate, `fast` runs
// the packages whose tests skip as root again as an unprivileged user, so the
// unreadable-file branches are checked on GitLab too. Same form and the same
// exactly-once rule as changedBy216.
var changedBy195 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"fast"},
		old:  `go run ./tools/coveragefloor coverage.out`,
		new: `go run ./tools/coveragefloor coverage.out
bash scripts/ci/nonroot-tests.sh`,
	},
}

// What #292 changes on purpose: the launcher refuses root (#291), so the
// visual suite runs as an unprivileged user through
// scripts/ci/run-unprivileged.sh, and the live install runs as orbit-ci, a
// user in the job daemon's docker group, which also makes the registry login
// and has it removed in after_script. A change to that script alone starts
// the visual and live jobs. Same form and the same exactly-once rule as
// changedBy216.
var changedBy292 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		key:  "rules",
		old:  `"changes": [".gitlab-ci.yml", "scripts/ci/fetch-verified.sh"]`,
		new:  `"changes": [".gitlab-ci.yml", "scripts/ci/fetch-verified.sh", "scripts/ci/run-unprivileged.sh"]`,
	},
	{
		jobs: []string{"visual"},
		key:  "rules",
		old:  `"changes": ["internal/ui/**/*", "test/visual/**/*", ".gitlab-ci.yml", "scripts/ci/fetch-verified.sh"]`,
		new:  `"changes": ["internal/ui/**/*", "test/visual/**/*", ".gitlab-ci.yml", "scripts/ci/fetch-verified.sh", "scripts/ci/run-unprivileged.sh"]`,
	},
	{
		jobs: []string{"visual"},
		old: `cd test/visual
npm ci
npx playwright test`,
		new: `bash scripts/ci/run-unprivileged.sh --chdir test/visual \
  --keep test/visual/playwright-report --keep test/visual/test-results \
  -- bash -c 'npm ci && npx playwright test'`,
	},
	{
		jobs: []string{"live"},
		old:  `mkdir -p .live/install-script`,
		new: `mkdir -p .live/install-script
# The launcher refuses root (#291), so the install runs as orbit-ci, a
# user in the docker group of the daemon above (#292): the launcher,
# install.sh, the folder it installs into (the test's own temporary
# folder) and the image pulls are all that user's. Its Docker client
# config, where the registry login below writes the job token, is
# created here first so its owner and mode are known: orbit-ci, 0700.
useradd --create-home --shell /bin/bash --groups docker orbit-ci
install -d -m 0700 -o orbit-ci -g orbit-ci /home/orbit-ci/.docker
# The raw-log artefacts are written into .live by the test.
chown orbit-ci .live
setpriv --reuid=orbit-ci --regid=orbit-ci --init-groups -- docker info > /dev/null \
  || { echo "orbit-ci cannot reach the job's Docker daemon"; exit 1; }`,
	},
	{
		jobs: []string{"live"},
		old:  `  printf '%s' "$CI_JOB_TOKEN" | docker login "$ORBIT_REGISTRY" --username gitlab-ci-token --password-stdin`,
		new: `  # The login is per Docker client user, and the pulls are orbit-ci's
  # (#292), so it is orbit-ci that logs in: the token is written to
  # /home/orbit-ci/.docker/config.json, owned by orbit-ci, mode 0600,
  # inside this job's container only, and after_script removes it.
  printf '%s' "$CI_JOB_TOKEN" \
    | setpriv --reuid=orbit-ci --regid=orbit-ci --init-groups -- env HOME=/home/orbit-ci \
      docker login "$ORBIT_REGISTRY" --username gitlab-ci-token --password-stdin
  stat -c 'the registry login is in %n, owned by %U, mode %a' /home/orbit-ci/.docker/config.json`,
	},
	{
		jobs: []string{"live"},
		old: `go test -tags live -count=1 -v -timeout 30m ./test/live/...
docker ps -a || true`,
		new: `bash scripts/ci/run-unprivileged.sh --user orbit-ci -- \
  env -u CI_JOB_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL \
  go test -tags live -count=1 -v -timeout 30m ./test/live/...
rm -f /home/orbit-ci/.docker/config.json
docker ps -a || true`,
	},
}

// What #277 changes on purpose: the visual suite's config has only the
// list reporter, so no playwright-report folder is ever made; only
// test-results, where a failure's screenshots and diffs go, comes back.
// Same form and the same exactly-once rule as changedBy216.
var changedBy277 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"visual"},
		key:  "artifacts",
		old:  `"paths": ["test/visual/playwright-report/", "test/visual/test-results/"]`,
		new:  `"paths": ["test/visual/test-results/"]`,
	},
	{
		jobs: []string{"visual"},
		old: `bash scripts/ci/run-unprivileged.sh --chdir test/visual \
  --keep test/visual/playwright-report --keep test/visual/test-results \
  -- bash -c 'npm ci && npx playwright test'`,
		new: `bash scripts/ci/run-unprivileged.sh --chdir test/visual \
  --keep test/visual/test-results \
  -- bash -c 'npm ci && npx playwright test'`,
	},
}

// What #193 changes on purpose: the live job reads ai/orbit's branch and
// commit from orbit/pin.json, fetches install.sh, the verifier scripts and
// cosign.pub at the commit, and reads the manifest from the latest build of
// the pin's branch. ORBIT_BRANCH, set on a pipeline started by hand,
// overrides the pin, and the #236 refusal says how to bump it. A change to
// orbit/pin.json creates the job on the manual route. #234's
// "configuration tree" rides in the first entry. Same form and the same
// exactly-once rule as changedBy216; these apply after #277's.
var changedBy193 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{ // a bump of the pin creates the job on the manual route
		jobs: []string{"live"},
		key:  "rules",
		old:  `"changes": [".gitlab-ci.yml", "scripts/ci/fetch-verified.sh", "scripts/ci/run-unprivileged.sh"]`,
		new:  `"changes": [".gitlab-ci.yml", "scripts/ci/fetch-verified.sh", "scripts/ci/run-unprivileged.sh", "orbit/pin.json"]`,
	},
	{ // the mirror is read at the same commit; and #234's wording fix
		jobs: []string{"live"},
		old: `# same branch: GitLab pushes it after each Orbit merge, so it trails by
# minutes at most. The configure tree reaches the launcher through`,
		new: `# same commit: GitLab pushes it after each Orbit merge, so it trails by
# minutes at most. The configuration tree reaches the launcher through`,
	},
	{ // which Orbit: the pin, or ORBIT_BRANCH's tip as an override
		jobs: []string{"live"},
		old: `project="$(printf '%s' "$ORBIT_PROJECT" | sed 's#/#%2F#g')"
# The full ref, so a tag with the same name as the branch cannot answer
# (#273). The files endpoint accepts refs/heads/<branch>.
gitlab_orbit_ref="$(printf 'refs/heads/%s' "$ORBIT_BRANCH" | sed 's#/#%2F#g')"`,
		new: `#
# Which Orbit (#193): the commit orbit/pin.json names, so every run
# installs a fixed partner and an Orbit merge alone cannot turn this
# pipeline red. install.sh, the verifier scripts and cosign.pub are
# read at that commit; the release manifest comes from the latest
# build of the pin's branch, and the #236 check below stops the run
# unless the two are one release. scripts/bump-orbit-pin.sh <ref>
# moves the pin. ORBIT_BRANCH, set non-empty on a pipeline started by
# hand, overrides both: the files come from that branch's tip and the
# manifest from its latest build.
project="$(printf '%s' "$ORBIT_PROJECT" | sed 's#/#%2F#g')"
if [ -n "${ORBIT_BRANCH:-}" ]; then
  orbit_branch="$ORBIT_BRANCH"
  # The full ref, so a tag with the same name as the branch cannot
  # answer (#273). The files endpoint accepts refs/heads/<branch>.
  gitlab_orbit_ref="$(printf 'refs/heads/%s' "$ORBIT_BRANCH" | sed 's#/#%2F#g')"
  github_orbit_ref="$ORBIT_BRANCH"
  orbit_at="the tip of ${ORBIT_BRANCH}"
  orbit_mismatch="ai/orbit's ${ORBIT_BRANCH} pipeline has not caught up with the branch yet; rerun this job once it has"
  echo "ORBIT_BRANCH=${ORBIT_BRANCH} overrides orbit/pin.json: Orbit's files come from the tip of ${ORBIT_BRANCH} (#193)"
else
  orbit_branch="$(jq -r .branch orbit/pin.json)" || { echo "orbit/pin.json cannot be read (#193)"; exit 1; }
  orbit_commit="$(jq -r .commit orbit/pin.json)" || { echo "orbit/pin.json cannot be read (#193)"; exit 1; }
  case "$orbit_commit" in
    *[!0-9a-f]*) orbit_commit="" ;;
  esac
  if [ "${#orbit_commit}" -ne 40 ] || [ -z "$orbit_branch" ] || [ "$orbit_branch" = "null" ]; then
    echo "orbit/pin.json must name a branch and a 40-hex commit (#193)"
    exit 1
  fi
  gitlab_orbit_ref="$orbit_commit"
  github_orbit_ref="$orbit_commit"
  orbit_at="${orbit_commit} (orbit/pin.json)"
  orbit_mismatch="the pin and ai/orbit's latest ${orbit_branch} build differ: bump orbit/pin.json with scripts/bump-orbit-pin.sh ${orbit_branch}, or wait for that build if it is still running"
  echo "Orbit is pinned at ${orbit_commit} on ${orbit_branch} by orbit/pin.json (#193)"
fi`,
	},
	{
		jobs: []string{"live"},
		old:  `    echo "Orbit's files come from ${ORBIT_PROJECT} on GitLab, ${ORBIT_BRANCH}"`,
		new:  `    echo "Orbit's files come from ${ORBIT_PROJECT} on GitLab at ${orbit_at}"`,
	},
	{
		jobs: []string{"live"},
		old:  `        || { echo "could not fetch $1 from ${ORBIT_PROJECT} on GitLab at ${ORBIT_BRANCH}"; exit 1; }`,
		new:  `        || { echo "could not fetch $1 from ${ORBIT_PROJECT} on GitLab at ${orbit_at}"; exit 1; }`,
	},
	{
		jobs: []string{"live"},
		old:  `    echo "Orbit's files come from github.com/${ORBIT_GITHUB_REPO}, ${ORBIT_BRANCH} at $(git ls-remote "https://github.com/${ORBIT_GITHUB_REPO}.git" "refs/heads/${ORBIT_BRANCH}" | cut -f1)"`,
		new:  `    echo "Orbit's files come from github.com/${ORBIT_GITHUB_REPO} at ${orbit_at}"`,
	},
	{
		jobs: []string{"live"},
		old: `        "https://raw.githubusercontent.com/${ORBIT_GITHUB_REPO}/${ORBIT_BRANCH}/$1" \
        || { echo "could not fetch $1 from github.com/${ORBIT_GITHUB_REPO} at ${ORBIT_BRANCH}"; exit 1; }`,
		new: `        "https://raw.githubusercontent.com/${ORBIT_GITHUB_REPO}/${github_orbit_ref}/$1" \
        || { echo "could not fetch $1 from github.com/${ORBIT_GITHUB_REPO} at ${orbit_at}"; exit 1; }`,
	},
	{
		jobs: []string{"live"},
		old:  `head -1 .live/install-script/install.sh | grep -q '^#!' || { echo "ai/orbit did not return a script for ref ${ORBIT_BRANCH}"; exit 1; }`,
		new:  `head -1 .live/install-script/install.sh | grep -q '^#!' || { echo "ai/orbit did not return a script at ${orbit_at}"; exit 1; }`,
	},
	{
		jobs: []string{"live"},
		old:  `# same branch as install.sh, then hand the path over. Preview's`,
		new:  `# same commit as install.sh, then hand the path over. Preview's`,
	},
	{
		jobs: []string{"live"},
		old: `    head -1 ".live/orbit/scripts/ci/$script" | grep -q '^#!' || { echo "ai/orbit did not return a script for scripts/ci/$script at ${ORBIT_BRANCH}"; exit 1; }
  done
  head -1 .live/orbit/cosign.pub | grep -qx -e '-----BEGIN PUBLIC KEY-----' || { echo "ai/orbit did not return a public key for cosign.pub at ${ORBIT_BRANCH}"; exit 1; }
  # The artifacts endpoint rejects full refs (404 for refs/heads/...), so
  # these two stay on the plain branch name; the pair is verified below
  # against the cosign.pub fetched by full ref above (#273).`,
		new: `    head -1 ".live/orbit/scripts/ci/$script" | grep -q '^#!' || { echo "ai/orbit did not return a script for scripts/ci/$script at ${orbit_at}"; exit 1; }
  done
  head -1 .live/orbit/cosign.pub | grep -qx -e '-----BEGIN PUBLIC KEY-----' || { echo "ai/orbit did not return a public key for cosign.pub at ${orbit_at}"; exit 1; }
  # The artifacts endpoint rejects full refs (404 for refs/heads/...), so
  # these two stay on the plain branch name; the pair is verified below
  # against the cosign.pub fetched at the pinned commit (or, under
  # ORBIT_BRANCH, by full ref) above (#273).`,
	},
	{
		jobs: []string{"live"},
		old:  `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${ORBIT_BRANCH}/raw/.orbit-supply-chain/orbit-release-manifest.json?job=record_image"`,
		new:  `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/orbit-release-manifest.json?job=record_image"`,
	},
	{
		jobs: []string{"live"},
		old: `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${ORBIT_BRANCH}/raw/.orbit-supply-chain/orbit-release-manifest.json.sig?job=sign_evidence"
  jq -e . .live/orbit/release/orbit-release-manifest.json > /dev/null || { echo "ai/orbit's record_image artifact at ${ORBIT_BRANCH} is not a JSON release manifest"; exit 1; }
  [ -s .live/orbit/release/orbit-release-manifest.json.sig ] || { echo "ai/orbit's sign_evidence artifact at ${ORBIT_BRANCH} is an empty signature"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json" \
    || { echo "the preview release manifest at ${ORBIT_BRANCH} failed verification; nothing is installed from it (#194)"; exit 1; }
  # install.sh comes from the branch tip, the manifest from the branch's
  # latest preview pipeline, which trails the tip by minutes. In that
  # gap the run would pair a newer install.sh with the previous
  # commit's image, so it stops until the two are one release (#236).
  [ "sha256:$(sha256sum .live/install-script/install.sh | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' .live/orbit/release/orbit-release-manifest.json)" ] \
    || { echo "install.sh at the tip of ${ORBIT_BRANCH} is not the one the latest signed preview release records: ai/orbit's preview pipeline has not caught up with the branch yet; rerun this job once it has (#236)"; exit 1; }`,
		new: `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/orbit-release-manifest.json.sig?job=sign_evidence"
  jq -e . .live/orbit/release/orbit-release-manifest.json > /dev/null || { echo "ai/orbit's record_image artifact at ${orbit_branch} is not a JSON release manifest"; exit 1; }
  [ -s .live/orbit/release/orbit-release-manifest.json.sig ] || { echo "ai/orbit's sign_evidence artifact at ${orbit_branch} is an empty signature"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json" \
    || { echo "the preview release manifest at ${orbit_branch} failed verification; nothing is installed from it (#194)"; exit 1; }
  # install.sh comes from the pinned commit (#193), the manifest from
  # the latest preview pipeline of the pin's branch. Once that branch
  # moves on, or while the pinned commit's own build is still running,
  # the two differ, and the run would pair one commit's install.sh with
  # another's image, so it stops until they are one release (#236).
  # Under ORBIT_BRANCH install.sh is the branch tip, and the gap is
  # that branch's pipeline catching up.
  [ "sha256:$(sha256sum .live/install-script/install.sh | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' .live/orbit/release/orbit-release-manifest.json)" ] \
    || { echo "install.sh at ${orbit_at} is not the one the latest signed preview release records: ${orbit_mismatch} (#193, #236)"; exit 1; }`,
	},
}

// What #193 changes on purpose in the top-level variables: ORBIT_BRANCH
// loses its default, because orbit/pin.json names the Orbit branch now and
// ORBIT_BRANCH is only an override set on a pipeline started by hand.
var variablesChangedBy193 = map[string]struct{ old, new string }{
	"ORBIT_BRANCH": {`"preview"`, `""`},
}

// What #263 changes on purpose: the update leg reads the previous preview
// build's manifest and signature from .orbit-supply-chain/previous/ in the
// latest record_image artifacts of the pin's branch, keeps the by-ID read
// only as a hand override, fails instead of skipping when there is no
// readable predecessor, and names the predecessor in one greppable line.
// Same form and the same exactly-once rule as changedBy216; these apply
// after #193's.
var changedBy263 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		old: `  # The update leg's first install (#198): an earlier preview release,
  # so the update goes from one signed image to another. Its manifest
  # and signature are read by job ID, since a branch only ever names
  # its latest pipeline; the IDs come from whoever starts the pipeline
  # (ORBIT_FIRST_RECORD_IMAGE_JOB, ORBIT_FIRST_SIGN_EVIDENCE_JOB),
  # because this job's token cannot list ai/orbit's pipelines.
  # Verified exactly as above. Its install.sh comes from the commit the
  # manifest names and must match the sha256 the manifest records for
  # it, so script and image stay one generation. Unset, the leg skips.
  first_record="${ORBIT_FIRST_RECORD_IMAGE_JOB:-}"
  first_sign="${ORBIT_FIRST_SIGN_EVIDENCE_JOB:-}"
  if [ -z "$first_record" ] && [ -z "$first_sign" ]; then
    echo "no ORBIT_FIRST_RECORD_IMAGE_JOB / ORBIT_FIRST_SIGN_EVIDENCE_JOB: the update leg (#198) skips"
  else`,
		new: `  # The update leg's first install (#198): the preview build before
  # the current one, so the update goes from one signed image to
  # another. ai/orbit's record_image keeps that build's signed manifest
  # and signature under .orbit-supply-chain/previous/ in its artifacts
  # (#263), read here by branch like the current manifest. A pipeline
  # started by hand with ORBIT_FIRST_RECORD_IMAGE_JOB and
  # ORBIT_FIRST_SIGN_EVIDENCE_JOB overrides that: the record_image and
  # sign_evidence job IDs of an earlier signed ai/orbit preview
  # pipeline, read by ID instead. Either way the earlier release is
  # verified exactly as above, and its install.sh comes from the
  # commit its manifest names and must match the sha256 the manifest
  # records for it, so script and image stay one generation. The leg
  # never skips: no readable predecessor stops the job.
  first_record="${ORBIT_FIRST_RECORD_IMAGE_JOB:-}"
  first_sign="${ORBIT_FIRST_SIGN_EVIDENCE_JOB:-}"
  first_dir=.live/orbit/release/first
  mkdir -p "$first_dir"
  if [ -n "$first_record" ] || [ -n "$first_sign" ]; then`,
	},
	{
		jobs: []string{"live"},
		old: `    first_dir=.live/orbit/release/first
    mkdir -p "$first_dir"`,
		new: `    first_from="ai/orbit jobs ${first_record}/${first_sign}"`,
	},
	{
		jobs: []string{"live"},
		old: `    jq -e . "$first_dir/orbit-release-manifest.json" > /dev/null || { echo "ai/orbit job ${first_record} did not return a JSON release manifest (#198)"; exit 1; }
    [ -s "$first_dir/orbit-release-manifest.json.sig" ] || { echo "ai/orbit job ${first_sign} returned an empty signature (#198)"; exit 1; }
    COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
      bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json" \
      || { echo "the earlier release manifest from ai/orbit jobs ${first_record}/${first_sign} failed verification; nothing is installed from it (#198)"; exit 1; }
    jq -e '.channel == "preview"' "$first_dir/orbit-release-manifest.json" > /dev/null \
      || { echo "the earlier release manifest is not a preview release (#198)"; exit 1; }
    first_digest="$(jq -r .image.digest "$first_dir/orbit-release-manifest.json")"
    [ "$first_digest" != "$(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST")" ] \
      || { echo "the earlier release names the same image as the current one, ${first_digest}; an update between them proves nothing (#198)"; exit 1; }
    first_at="$(jq -r .recordedAt "$first_dir/orbit-release-manifest.json")"
    current_at="$(jq -r .recordedAt "$ORBIT_RELEASE_MANIFEST")"
    if [ "$first_at" = "$current_at" ] || [ "$(printf '%s\n%s\n' "$first_at" "$current_at" | sort | head -1)" != "$first_at" ]; then
      echo "the earlier release (recorded ${first_at}) is not older than the current one (recorded ${current_at}) (#198)"
      exit 1
    fi
    first_commit="$(jq -r .commit "$first_dir/orbit-release-manifest.json")"
    case "$first_commit" in
      *[!0-9a-f]*) first_commit="" ;;
    esac
    [ "${#first_commit}" -eq 40 ] || { echo "the earlier release manifest names no source commit (#198)"; exit 1; }
    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/install.sh" \
      "${CI_API_V4_URL}/projects/${project}/repository/files/scripts%2Finstall.sh/raw?ref=${first_commit}" \
      || { echo "could not fetch scripts/install.sh from ${ORBIT_PROJECT} at ${first_commit} (#198)"; exit 1; }
    [ "sha256:$(sha256sum "$first_dir/install.sh" | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' "$first_dir/orbit-release-manifest.json")" ] \
      || { echo "install.sh at ${first_commit} does not match the sha256 the earlier release manifest records for it (#198)"; exit 1; }
    export ORBIT_LIVE_FIRST_RELEASE_MANIFEST="$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json"
    export ORBIT_LIVE_FIRST_INSTALL_SCRIPT="$CI_PROJECT_DIR/$first_dir/install.sh"
    echo "the update leg (#198) installs ${first_digest} (ai/orbit ${first_commit}) first, then updates to the current preview"
  fi`,
		new: `  else
    first_from="previous/ of ai/orbit's latest ${orbit_branch} build"
    no_predecessor="ai/orbit's latest ${orbit_branch} build has no readable predecessor (.orbit-supply-chain/previous/ in its record_image artifacts); to run the update leg from an earlier build instead, start the pipeline by hand with ORBIT_FIRST_RECORD_IMAGE_JOB and ORBIT_FIRST_SIGN_EVIDENCE_JOB set to that signed preview pipeline's record_image and sign_evidence job IDs (#263)"
    # By branch name, as the current manifest above: the artifacts
    # endpoint rejects full refs (#273). Both files are record_image's.
    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json?job=record_image" \
      || { echo "$no_predecessor"; exit 1; }
    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json.sig?job=record_image" \
      || { echo "$no_predecessor"; exit 1; }
  fi
  jq -e . "$first_dir/orbit-release-manifest.json" > /dev/null || { echo "${first_from} did not return a JSON release manifest (#198)"; exit 1; }
  [ -s "$first_dir/orbit-release-manifest.json.sig" ] || { echo "${first_from} returned an empty signature (#198)"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json" \
    || { echo "the earlier release manifest from ${first_from} failed verification; nothing is installed from it (#198)"; exit 1; }
  jq -e '.channel == "preview"' "$first_dir/orbit-release-manifest.json" > /dev/null \
    || { echo "the earlier release manifest is not a preview release (#198)"; exit 1; }
  first_digest="$(jq -r .image.digest "$first_dir/orbit-release-manifest.json")"
  [ "$first_digest" != "$(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST")" ] \
    || { echo "the earlier release names the same image as the current one, ${first_digest}; an update between them proves nothing (#198)"; exit 1; }
  first_at="$(jq -r .recordedAt "$first_dir/orbit-release-manifest.json")"
  current_at="$(jq -r .recordedAt "$ORBIT_RELEASE_MANIFEST")"
  if [ "$first_at" = "$current_at" ] || [ "$(printf '%s\n%s\n' "$first_at" "$current_at" | sort | head -1)" != "$first_at" ]; then
    echo "the earlier release (recorded ${first_at}) is not older than the current one (recorded ${current_at}) (#198)"
    exit 1
  fi
  first_commit="$(jq -r .commit "$first_dir/orbit-release-manifest.json")"
  case "$first_commit" in
    *[!0-9a-f]*) first_commit="" ;;
  esac
  [ "${#first_commit}" -eq 40 ] || { echo "the earlier release manifest names no source commit (#198)"; exit 1; }
  curl --fail --silent --show-error --location \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output "$first_dir/install.sh" \
    "${CI_API_V4_URL}/projects/${project}/repository/files/scripts%2Finstall.sh/raw?ref=${first_commit}" \
    || { echo "could not fetch scripts/install.sh from ${ORBIT_PROJECT} at ${first_commit} (#198)"; exit 1; }
  [ "sha256:$(sha256sum "$first_dir/install.sh" | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' "$first_dir/orbit-release-manifest.json")" ] \
    || { echo "install.sh at ${first_commit} does not match the sha256 the earlier release manifest records for it (#198)"; exit 1; }
  export ORBIT_LIVE_FIRST_RELEASE_MANIFEST="$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json"
  export ORBIT_LIVE_FIRST_INSTALL_SCRIPT="$CI_PROJECT_DIR/$first_dir/install.sh"
  first_version="$(jq -r .version "$first_dir/orbit-release-manifest.json")"
  if [ -n "$first_record" ]; then
    echo "update leg: previous build from job IDs ${first_record}/${first_sign}"
  else
    echo "update leg: previous build ${first_version} ${first_digest} from previous/"
  fi
  echo "the update leg (#198) installs ${first_version} ${first_digest} (ai/orbit ${first_commit}) first, then updates to the current preview"`,
	},
}

// What the review of the job token's reach changes on purpose (#193, #263):
// the Orbit branch, from the pin or ORBIT_BRANCH, must be a plain name
// before it reaches a URL that carries the token, and its "/" is encoded in
// the artifacts path; Orbit's verifier runs without the token in its
// environment; and the registry login goes only to this GitLab's registry.
// Same form and the same exactly-once rule as changedBy216; these apply
// after #263's.
var changedByTokenReview = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		old: `# manifest from its latest build.
project="$(printf '%s' "$ORBIT_PROJECT" | sed 's#/#%2F#g')"
if [ -n "${ORBIT_BRANCH:-}" ]; then`,
		new: `# manifest from its latest build. ORBIT_BRANCH moves the trust root
# (cosign.pub and the verifier) to that branch's tip, so only start one
# against a branch you trust.
#
# The branch goes into API URLs that carry the job token, so it must
# be a plain name in the charset scripts/bump-orbit-pin.sh accepts,
# with no "..", whichever of the two supplied it.
orbit_branch_ok() {
  case "$1" in
    ''|-*|.*|/*|refs/*|*..*|*[!A-Za-z0-9._/-]*) return 1 ;;
  esac
}
project="$(printf '%s' "$ORBIT_PROJECT" | sed 's#/#%2F#g')"
if [ -n "${ORBIT_BRANCH:-}" ]; then
  orbit_branch_ok "$ORBIT_BRANCH" \
    || { echo "ORBIT_BRANCH must be a plain branch name (letters, digits, . _ / -, no ..), not ${ORBIT_BRANCH} (#193)"; exit 1; }`,
	},
	{
		jobs: []string{"live"},
		old: `  if [ "${#orbit_commit}" -ne 40 ] || [ -z "$orbit_branch" ] || [ "$orbit_branch" = "null" ]; then
    echo "orbit/pin.json must name a branch and a 40-hex commit (#193)"`,
		new: `  if [ "${#orbit_commit}" -ne 40 ] || ! orbit_branch_ok "$orbit_branch"; then
    echo "orbit/pin.json must name a plain branch name (letters, digits, . _ / -, no ..) and a 40-hex commit (#193)"`,
	},
	{
		jobs: []string{"live"},
		old: `  echo "Orbit is pinned at ${orbit_commit} on ${orbit_branch} by orbit/pin.json (#193)"
fi`,
		new: `  echo "Orbit is pinned at ${orbit_commit} on ${orbit_branch} by orbit/pin.json (#193)"
fi
# The artifacts-by-ref endpoint takes the bare branch name, with "/"
# encoded like any path segment.
orbit_branch_path="$(printf '%s' "$orbit_branch" | sed 's#/#%2F#g')"`,
	},
	{
		jobs: []string{"live"},
		old:  `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/orbit-release-manifest.json?job=record_image"`,
		new:  `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/orbit-release-manifest.json?job=record_image"`,
	},
	{
		jobs: []string{"live"},
		old:  `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/orbit-release-manifest.json.sig?job=sign_evidence"`,
		new:  `    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/orbit-release-manifest.json.sig?job=sign_evidence"`,
	},
	{
		jobs: []string{"live"},
		old: `  [ -s .live/orbit/release/orbit-release-manifest.json.sig ] || { echo "ai/orbit's sign_evidence artifact at ${orbit_branch} is an empty signature"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \`,
		new: `  [ -s .live/orbit/release/orbit-release-manifest.json.sig ] || { echo "ai/orbit's sign_evidence artifact at ${orbit_branch} is an empty signature"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL \`,
	},
	{
		jobs: []string{"live"},
		old:  `  export ORBIT_REPOSITORY="${image_repository#*/}"`,
		new: `  export ORBIT_REPOSITORY="${image_repository#*/}"
  # The job token goes only to this GitLab's own registry (#193).
  [ "$ORBIT_REGISTRY" = "$CI_REGISTRY" ] || { echo "the manifest names image registry ${ORBIT_REGISTRY}, not this GitLab's ${CI_REGISTRY}; the job token is not sent there (#193)"; exit 1; }`,
	},
	{
		jobs: []string{"live"},
		old:  `      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json?job=record_image" \`,
		new:  `      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json?job=record_image" \`,
	},
	{
		jobs: []string{"live"},
		old:  `      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json.sig?job=record_image" \`,
		new:  `      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json.sig?job=record_image" \`,
	},
	{
		jobs: []string{"live"},
		old: `  [ -s "$first_dir/orbit-release-manifest.json.sig" ] || { echo "${first_from} returned an empty signature (#198)"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \`,
		new: `  [ -s "$first_dir/orbit-release-manifest.json.sig" ] || { echo "${first_from} returned an empty signature (#198)"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL \`,
	},
}

// What #183 changes on purpose. The live job's body moves into the hidden
// .live_leg template that `live` and `live-fedora` both extend, so `live`
// now extends that (which extends what it used to), names its distribution
// in LIVE_DISTRO and says it in one line, and its artifact name comes from
// the job name -- live-raw-log still, live-fedora-raw-log for the other leg.
// The skip gate moves into the .live_gate anchor each leg runs first; its
// lines are unchanged. Same form and the same exactly-once rule as
// changedBy216; these apply last.
var changedBy183 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		key:  "extends",
		old:  `[".privileged_runner", ".go_cache"]`,
		new:  `".live_leg"`,
	},
	{
		jobs: []string{"live"},
		key:  "variables",
		old:  `{"ORBIT_LAUNCHER_LIVE_RAW_LOG": "$CI_PROJECT_DIR/.live/live-raw.log"}`,
		new:  `{"LIVE_DISTRO": "ubuntu", "ORBIT_LAUNCHER_LIVE_RAW_LOG": "$CI_PROJECT_DIR/.live/live-raw.log"}`,
	},
	{
		jobs: []string{"live"},
		key:  "artifacts",
		old:  `"name": "live-raw-log"`,
		new:  `"name": "$CI_JOB_NAME-raw-log"`,
	},
	{
		jobs: []string{"live"},
		old:  `mkdir -p .live/install-script`,
		new: `echo "live leg: ${LIVE_DISTRO} ($(. /etc/os-release && printf '%s %s' "$ID" "$VERSION_ID"))"
mkdir -p .live/install-script`,
	},
}

// #300 gives `fast` 25 minutes: successful runs take 11-15 minutes and
// 15m timed out twice on a busy host. Applies after #183.
var changedBy300 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"fast"},
		key:  "timeout",
		old:  `"15m"`,
		new:  `"25m"`,
	},
}

// #298: every curl that sends the job token refuses a redirect
// (--max-redirs 0), because curl re-sends a custom header such as
// JOB-TOKEN to whatever host a redirect names. This GitLab answers these
// reads directly (checked 2026-10-28), so a redirect is now a loud
// failure rather than a leak. Applies after #300.
var changedBy298 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		old: `  probe="$(curl --silent --location --output /dev/null --write-out '%{http_code}' \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" "$(gitlab_orbit_url scripts/install.sh)" || true)"`,
		new: `  probe="$(curl --silent --location --max-redirs 0 --output /dev/null --write-out '%{http_code}' \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" "$(gitlab_orbit_url scripts/install.sh)" || true)"`,
	},
	{
		jobs: []string{"live"},
		old: `      curl --fail --silent --show-error --location \
        --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \`,
		new: `      curl --fail --silent --show-error --location --max-redirs 0 \
        --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \`,
	},
	{
		jobs: []string{"live"},
		old: `  curl --fail --silent --show-error --location \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json \`,
		new: `  curl --fail --silent --show-error --location --max-redirs 0 \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json \`,
	},
	{
		jobs: []string{"live"},
		old: `  curl --fail --silent --show-error --location \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json.sig \`,
		new: `  curl --fail --silent --show-error --location --max-redirs 0 \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json.sig \`,
	},
	{
		jobs: []string{"live"},
		old: `    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_record}/artifacts/.orbit-supply-chain/orbit-release-manifest.json" \`,
		new: `    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_record}/artifacts/.orbit-supply-chain/orbit-release-manifest.json" \`,
	},
	{
		jobs: []string{"live"},
		old: `    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_sign}/artifacts/.orbit-supply-chain/orbit-release-manifest.json.sig" \`,
		new: `    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_sign}/artifacts/.orbit-supply-chain/orbit-release-manifest.json.sig" \`,
	},
	{
		jobs: []string{"live"},
		old: `    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json?job=record_image" \`,
		new: `    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json?job=record_image" \`,
	},
	{
		jobs: []string{"live"},
		old: `    curl --fail --silent --show-error --location \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json.sig?job=record_image" \`,
		new: `    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json.sig?job=record_image" \`,
	},
	{
		jobs: []string{"live"},
		old: `  curl --fail --silent --show-error --location \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output "$first_dir/install.sh" \`,
		new: `  curl --fail --silent --show-error --location --max-redirs 0 \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output "$first_dir/install.sh" \`,
	},
}

// #301: `fast` drops the job token and the registry deploy token before
// any step runs; the live job logs in to the registry with a read-only
// deploy token scoped to its `live-pulls` environment instead of the job
// token, and the daemons it leaves running beside the tests start
// without either. Applies after #298.
var changedBy301 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"fast"},
		old:  `export DEBIAN_FRONTEND=noninteractive`,
		new: `unset CI_JOB_TOKEN CI_BUILD_TOKEN CI_REGISTRY_PASSWORD CI_DEPENDENCY_PROXY_PASSWORD CI_REPOSITORY_URL ORBIT_REGISTRY_USER ORBIT_REGISTRY_TOKEN
git remote set-url origin "${CI_PROJECT_URL}.git" 2>/dev/null || true
export DEBIAN_FRONTEND=noninteractive`,
	},
	{
		jobs: []string{"live"},
		key:  "environment",
		new:  `{"action": "prepare", "name": "live-pulls"}`,
	},
	{
		jobs: []string{"live"},
		old:  `nohup dockerd --data-root "$docker_data_root" > /tmp/dockerd.log 2>&1 &`,
		new: `# The daemon outlives every step and its environment is readable through
# it, so it starts without the job's credentials (#301).
nohup env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \
  dockerd --data-root "$docker_data_root" > /tmp/dockerd.log 2>&1 &`,
	},
	{
		jobs: []string{"live"},
		old:  `# config, where the registry login below writes the job token, is`,
		new:  `# config, where the registry login below writes its deploy token, is`,
	},
	{
		jobs: []string{"live"},
		old: `useradd --create-home --shell /bin/bash --groups docker orbit-ci
install -d -m 0700 -o orbit-ci -g orbit-ci /home/orbit-ci/.docker`,
		new: `useradd --create-home --shell /bin/bash --groups docker orbit-ci
# The clone's origin URL can carry the job token; the tests read the
# checkout as orbit-ci and nothing here fetches (#301).
git remote set-url origin "${CI_PROJECT_URL}.git" 2>/dev/null || :
install -d -m 0700 -o orbit-ci -g orbit-ci /home/orbit-ci/.docker`,
	},
	{
		jobs: []string{"live"},
		old: `  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json" \`,
		new: `  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json" \`,
	},
	{
		jobs: []string{"live"},
		old: `  # The job token goes only to this GitLab's own registry (#193).
  [ "$ORBIT_REGISTRY" = "$CI_REGISTRY" ] || { echo "the manifest names image registry ${ORBIT_REGISTRY}, not this GitLab's ${CI_REGISTRY}; the job token is not sent there (#193)"; exit 1; }`,
		new: `  # The registry login goes only to this GitLab's own registry (#193).
  [ "$ORBIT_REGISTRY" = "$CI_REGISTRY" ] || { echo "the manifest names image registry ${ORBIT_REGISTRY}, not this GitLab's ${CI_REGISTRY}; the registry login is not sent there (#193)"; exit 1; }`,
	},
	{
		jobs: []string{"live"},
		old: `  # (#292), so it is orbit-ci that logs in: the token is written to
  # /home/orbit-ci/.docker/config.json, owned by orbit-ci, mode 0600,
  # inside this job's container only, and after_script removes it.
  printf '%s' "$CI_JOB_TOKEN" \`,
		new: `  # (#292), so it is orbit-ci that logs in. The tests run as orbit-ci
  # too and can read its login (#301), so it is not the job token but
  # a read-only deploy token on ai/orbit (scope read_registry), held in
  # the masked variables ORBIT_REGISTRY_USER and ORBIT_REGISTRY_TOKEN:
  # leaked, it reads Orbit's images and nothing else. It is written to
  # /home/orbit-ci/.docker/config.json, mode 0600, inside this job's
  # container only, and after_script removes it.
  [ -n "${ORBIT_REGISTRY_USER:-}" ] && [ -n "${ORBIT_REGISTRY_TOKEN:-}" ] \
    || { echo "ORBIT_REGISTRY_USER and ORBIT_REGISTRY_TOKEN are not set: the live job pulls Orbit's image with a read-only deploy token, not the job token (#301)"; exit 1; }
  printf '%s' "$ORBIT_REGISTRY_TOKEN" \`,
	},
	{
		jobs: []string{"live"},
		old:  `      docker login "$ORBIT_REGISTRY" --username gitlab-ci-token --password-stdin`,
		new:  `      docker login "$ORBIT_REGISTRY" --username "$ORBIT_REGISTRY_USER" --password-stdin`,
	},
	{
		jobs: []string{"live"},
		old: `  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json" \`,
		new: `  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json" \`,
	},
	{
		jobs: []string{"live"},
		old:  `python3 -m http.server --bind 127.0.0.1 --directory .live/install-script 8765 > /tmp/install-script-server.log 2>&1 &`,
		new: `# Runs beside the tests, so without the credentials (#301).
env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \
  python3 -m http.server --bind 127.0.0.1 --directory .live/install-script 8765 > /tmp/install-script-server.log 2>&1 &`,
	},
	{
		jobs: []string{"live"},
		old: `bash scripts/ci/run-unprivileged.sh --user orbit-ci -- \
  env -u CI_JOB_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL \
  go test -tags live -count=1 -v -timeout 30m ./test/live/...`,
		new: `bash scripts/ci/run-unprivileged.sh --user orbit-ci -- \
  env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \
  go test -tags live -count=1 -v -timeout 30m ./test/live/...`,
	},
}

// #303, the CI security fixes from the v0.4.3 audit. Applies after #301.
//
// C1: the live legs no longer read ai/orbit. live-prepare (addedBy303), an
// unprivileged job on the shared runner, fetches and verifies Orbit's files
// with ORBIT_READ_TOKEN and hands them over as artifacts; the legs need it,
// check that the files arrived and keep only the exports, the registry
// login and the log lines. The #301 comments are rewritten to say what the
// env -u wrappers really do: orbit-ci's docker group makes the tests root
// in the container, so nothing in it is hidden from them. The renovate job
// gets an environment so its two tokens can be scoped to it.
//
// C2: fast, deps, visual, classify and gitleaks drop the credentials they
// do not need before running any branch or dependency code.
var changedBy303 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{ // C1
		jobs: []string{"live"},
		key:  "needs",
		old:  `[{"artifacts": "true", "job": "classify", "optional": "true"}]`,
		new:  `[{"artifacts": "true", "job": "classify", "optional": "true"}, {"artifacts": "true", "job": "live-prepare"}]`,
	},
	{ // C1
		jobs: []string{"live"},
		old: `# The daemon outlives every step and its environment is readable through
# it, so it starts without the job's credentials (#301).
nohup env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \`,
		new: `# The daemon outlives every step, so it starts without the job's
# credentials (#301). That keeps them out of its logs and dumps; it does
# not hide them from the tests, which can read this whole container
# (see .live_leg, #303).
nohup env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \`,
	},
	{ // C1
		jobs: []string{"live"},
		old: `useradd --create-home --shell /bin/bash --groups docker orbit-ci
# The clone's origin URL can carry the job token; the tests read the
# checkout as orbit-ci and nothing here fetches (#301).
git remote set-url origin "${CI_PROJECT_URL}.git" 2>/dev/null || :`,
		new: `useradd --create-home --shell /bin/bash --groups docker orbit-ci
# The clone's origin URL can carry the job token, and nothing here
# fetches (#301). That keeps it out of the checkout's config, not out
# of the tests' reach (see .live_leg, #303).
git remote set-url origin "${CI_PROJECT_URL}.git" 2>/dev/null || :`,
	},
	{ // C1: the whole fetch-and-verify block moves to live-prepare
		jobs: []string{"live"},
		old: `# Where the files come from (#173): ai/orbit on this GitLab, read with
# the job token. That token carries the access of whoever started the
# pipeline, and Renovate's project bot can never read another project,
# so on its merge requests GitLab answers 403. Then, and only then,
# install.sh comes from Orbit's public GitHub mirror instead, at the
# same commit: GitLab pushes it after each Orbit merge, so it trails by
# minutes at most. The configuration tree reaches the launcher through
# install.sh's own handover (#190); the only other files fetched are the
# preview release manifest and what verifies it (#194, below).
# ORBIT_FILES_SOURCE=github forces the mirror, to prove this path; with
# a preview channel that now fails, so prove it on a stable one.
#
# Which Orbit (#193): the commit orbit/pin.json names, so every run
# installs a fixed partner and an Orbit merge alone cannot turn this
# pipeline red. install.sh, the verifier scripts and cosign.pub are
# read at that commit; the release manifest comes from the latest
# build of the pin's branch, and the #236 check below stops the run
# unless the two are one release. scripts/bump-orbit-pin.sh <ref>
# moves the pin. ORBIT_BRANCH, set non-empty on a pipeline started by
# hand, overrides both: the files come from that branch's tip and the
# manifest from its latest build. ORBIT_BRANCH moves the trust root
# (cosign.pub and the verifier) to that branch's tip, so only start one
# against a branch you trust.
#
# The branch goes into API URLs that carry the job token, so it must
# be a plain name in the charset scripts/bump-orbit-pin.sh accepts,
# with no "..", whichever of the two supplied it.
orbit_branch_ok() {
  case "$1" in
    ''|-*|.*|/*|refs/*|*..*|*[!A-Za-z0-9._/-]*) return 1 ;;
  esac
}
project="$(printf '%s' "$ORBIT_PROJECT" | sed 's#/#%2F#g')"
if [ -n "${ORBIT_BRANCH:-}" ]; then
  orbit_branch_ok "$ORBIT_BRANCH" \
    || { echo "ORBIT_BRANCH must be a plain branch name (letters, digits, . _ / -, no ..), not ${ORBIT_BRANCH} (#193)"; exit 1; }
  orbit_branch="$ORBIT_BRANCH"
  # The full ref, so a tag with the same name as the branch cannot
  # answer (#273). The files endpoint accepts refs/heads/<branch>.
  gitlab_orbit_ref="$(printf 'refs/heads/%s' "$ORBIT_BRANCH" | sed 's#/#%2F#g')"
  github_orbit_ref="$ORBIT_BRANCH"
  orbit_at="the tip of ${ORBIT_BRANCH}"
  orbit_mismatch="ai/orbit's ${ORBIT_BRANCH} pipeline has not caught up with the branch yet; rerun this job once it has"
  echo "ORBIT_BRANCH=${ORBIT_BRANCH} overrides orbit/pin.json: Orbit's files come from the tip of ${ORBIT_BRANCH} (#193)"
else
  orbit_branch="$(jq -r .branch orbit/pin.json)" || { echo "orbit/pin.json cannot be read (#193)"; exit 1; }
  orbit_commit="$(jq -r .commit orbit/pin.json)" || { echo "orbit/pin.json cannot be read (#193)"; exit 1; }
  case "$orbit_commit" in
    *[!0-9a-f]*) orbit_commit="" ;;
  esac
  if [ "${#orbit_commit}" -ne 40 ] || ! orbit_branch_ok "$orbit_branch"; then
    echo "orbit/pin.json must name a plain branch name (letters, digits, . _ / -, no ..) and a 40-hex commit (#193)"
    exit 1
  fi
  gitlab_orbit_ref="$orbit_commit"
  github_orbit_ref="$orbit_commit"
  orbit_at="${orbit_commit} (orbit/pin.json)"
  orbit_mismatch="the pin and ai/orbit's latest ${orbit_branch} build differ: bump orbit/pin.json with scripts/bump-orbit-pin.sh ${orbit_branch}, or wait for that build if it is still running"
  echo "Orbit is pinned at ${orbit_commit} on ${orbit_branch} by orbit/pin.json (#193)"
fi
# The artifacts-by-ref endpoint takes the bare branch name, with "/"
# encoded like any path segment.
orbit_branch_path="$(printf '%s' "$orbit_branch" | sed 's#/#%2F#g')"
gitlab_orbit_url() {
  printf '%s/projects/%s/repository/files/%s/raw?ref=%s' "$CI_API_V4_URL" "$project" "$(printf '%s' "$1" | sed 's#/#%2F#g')" "$gitlab_orbit_ref"
}
orbit_source="${ORBIT_FILES_SOURCE:-auto}"
if [ "$orbit_source" = "auto" ]; then
  probe="$(curl --silent --location --max-redirs 0 --output /dev/null --write-out '%{http_code}' \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" "$(gitlab_orbit_url scripts/install.sh)" || true)"
  case "$probe" in
    401|403) orbit_source=github ;;
    *) orbit_source=gitlab ;;
  esac
  echo "ai/orbit on GitLab answered ${probe} for install.sh"
fi
case "$orbit_source" in
  gitlab)
    echo "Orbit's files come from ${ORBIT_PROJECT} on GitLab at ${orbit_at}"
    fetch_orbit_file() {
      curl --fail --silent --show-error --location --max-redirs 0 \
        --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
        --output "${3:-.live/install-script}/$2" "$(gitlab_orbit_url "$1")" \
        || { echo "could not fetch $1 from ${ORBIT_PROJECT} on GitLab at ${orbit_at}"; exit 1; }
    }
    ;;
  github)
    echo "Orbit's files come from github.com/${ORBIT_GITHUB_REPO} at ${orbit_at}"
    fetch_orbit_file() {
      curl --fail --silent --show-error --location \
        --output "${3:-.live/install-script}/$2" \
        "https://raw.githubusercontent.com/${ORBIT_GITHUB_REPO}/${github_orbit_ref}/$1" \
        || { echo "could not fetch $1 from github.com/${ORBIT_GITHUB_REPO} at ${orbit_at}"; exit 1; }
    }
    ;;
  *)
    echo "ORBIT_FILES_SOURCE must be auto, gitlab or github, not ${orbit_source}"
    exit 1
    ;;
esac
fetch_orbit_file scripts/install.sh install.sh
head -1 .live/install-script/install.sh | grep -q '^#!' || { echo "ai/orbit did not return a script at ${orbit_at}"; exit 1; }
# A preview install gets its release manifest from this job, not from
# install.sh (#194). install.sh fetches a manifest on its own only for
# the stable channel, whose manifest is a GitHub release asset; for
# preview it refuses and wants one handed over, already verified, in
# ORBIT_RELEASE_MANIFEST (ai/orbit#1107, ADR-0031). So the job does
# what get-orbit.sh does for stable: read the manifest and its
# signature, verify both with Orbit's own verifier and key from the
# same commit as install.sh, then hand the path over. Preview's
# manifest exists only as job artifacts of ai/orbit's preview pipeline
# (record_image writes it, sign_evidence its signature), read with the
# job token like the files above.
#
# The GitHub mirror has no pipeline artifacts, so a preview run that
# can only reach the mirror fails here rather than skipping: a skip
# would let an image bump go green with nothing installed into it,
# which is how #164 happened. A pipeline the owner starts by hand with
# RUN_LIVE=true carries the owner's access and reads ai/orbit directly.
if [ "${ORBIT_CHANNEL:-}" = "preview" ]; then
  if [ "$orbit_source" = "github" ]; then
    echo "the preview release manifest cannot be read from ${ORBIT_PROJECT} with this job's token, and the GitHub mirror has none; start the pipeline by hand with RUN_LIVE=true (#194)"
    exit 1
  fi
  mkdir -p .live/orbit/scripts/ci .live/orbit/release
  fetch_orbit_file scripts/ci/verify-release-manifest.sh verify-release-manifest.sh .live/orbit/scripts/ci
  fetch_orbit_file scripts/ci/ensure-cosign.sh ensure-cosign.sh .live/orbit/scripts/ci
  fetch_orbit_file cosign.pub cosign.pub .live/orbit
  for script in verify-release-manifest.sh ensure-cosign.sh; do
    head -1 ".live/orbit/scripts/ci/$script" | grep -q '^#!' || { echo "ai/orbit did not return a script for scripts/ci/$script at ${orbit_at}"; exit 1; }
  done
  head -1 .live/orbit/cosign.pub | grep -qx -e '-----BEGIN PUBLIC KEY-----' || { echo "ai/orbit did not return a public key for cosign.pub at ${orbit_at}"; exit 1; }
  # The artifacts endpoint rejects full refs (404 for refs/heads/...), so
  # these two stay on the plain branch name; the pair is verified below
  # against the cosign.pub fetched at the pinned commit (or, under
  # ORBIT_BRANCH, by full ref) above (#273).
  curl --fail --silent --show-error --location --max-redirs 0 \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json \
    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/orbit-release-manifest.json?job=record_image"
  curl --fail --silent --show-error --location --max-redirs 0 \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output .live/orbit/release/orbit-release-manifest.json.sig \
    "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/orbit-release-manifest.json.sig?job=sign_evidence"
  jq -e . .live/orbit/release/orbit-release-manifest.json > /dev/null || { echo "ai/orbit's record_image artifact at ${orbit_branch} is not a JSON release manifest"; exit 1; }
  [ -s .live/orbit/release/orbit-release-manifest.json.sig ] || { echo "ai/orbit's sign_evidence artifact at ${orbit_branch} is an empty signature"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json" \
    || { echo "the preview release manifest at ${orbit_branch} failed verification; nothing is installed from it (#194)"; exit 1; }
  # install.sh comes from the pinned commit (#193), the manifest from
  # the latest preview pipeline of the pin's branch. Once that branch
  # moves on, or while the pinned commit's own build is still running,
  # the two differ, and the run would pair one commit's install.sh with
  # another's image, so it stops until they are one release (#236).
  # Under ORBIT_BRANCH install.sh is the branch tip, and the gap is
  # that branch's pipeline catching up.
  [ "sha256:$(sha256sum .live/install-script/install.sh | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' .live/orbit/release/orbit-release-manifest.json)" ] \
    || { echo "install.sh at ${orbit_at} is not the one the latest signed preview release records: ${orbit_mismatch} (#193, #236)"; exit 1; }
  export ORBIT_RELEASE_MANIFEST="$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json"
  # Install what the manifest names (#241): its image is in the GitLab
  # registry that built it. install.sh ignores the manifest's
  # repository and defaults to ghcr.io, where the GitHub copy can lag
  # by up to two hours or never arrive. The token goes in on stdin.
  image_repository="$(jq -r .image.repository "$ORBIT_RELEASE_MANIFEST")"
  export ORBIT_REGISTRY="${image_repository%%/*}"
  export ORBIT_REPOSITORY="${image_repository#*/}"
  # The registry login goes only to this GitLab's own registry (#193).
  [ "$ORBIT_REGISTRY" = "$CI_REGISTRY" ] || { echo "the manifest names image registry ${ORBIT_REGISTRY}, not this GitLab's ${CI_REGISTRY}; the registry login is not sent there (#193)"; exit 1; }
  # The login is per Docker client user, and the pulls are orbit-ci's
  # (#292), so it is orbit-ci that logs in. The tests run as orbit-ci
  # too and can read its login (#301), so it is not the job token but
  # a read-only deploy token on ai/orbit (scope read_registry), held in
  # the masked variables ORBIT_REGISTRY_USER and ORBIT_REGISTRY_TOKEN:
  # leaked, it reads Orbit's images and nothing else. It is written to
  # /home/orbit-ci/.docker/config.json, mode 0600, inside this job's
  # container only, and after_script removes it.
  [ -n "${ORBIT_REGISTRY_USER:-}" ] && [ -n "${ORBIT_REGISTRY_TOKEN:-}" ] \
    || { echo "ORBIT_REGISTRY_USER and ORBIT_REGISTRY_TOKEN are not set: the live job pulls Orbit's image with a read-only deploy token, not the job token (#301)"; exit 1; }
  printf '%s' "$ORBIT_REGISTRY_TOKEN" \
    | setpriv --reuid=orbit-ci --regid=orbit-ci --init-groups -- env HOME=/home/orbit-ci \
      docker login "$ORBIT_REGISTRY" --username "$ORBIT_REGISTRY_USER" --password-stdin
  stat -c 'the registry login is in %n, owned by %U, mode %a' /home/orbit-ci/.docker/config.json
  echo "the manifest's image lives at ${image_repository}; install.sh pulls its digest from there, not from the GitHub mirror"
  echo "the verified preview release manifest names image $(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST"); install.sh installs that"
  # The update leg's first install (#198): the preview build before
  # the current one, so the update goes from one signed image to
  # another. ai/orbit's record_image keeps that build's signed manifest
  # and signature under .orbit-supply-chain/previous/ in its artifacts
  # (#263), read here by branch like the current manifest. A pipeline
  # started by hand with ORBIT_FIRST_RECORD_IMAGE_JOB and
  # ORBIT_FIRST_SIGN_EVIDENCE_JOB overrides that: the record_image and
  # sign_evidence job IDs of an earlier signed ai/orbit preview
  # pipeline, read by ID instead. Either way the earlier release is
  # verified exactly as above, and its install.sh comes from the
  # commit its manifest names and must match the sha256 the manifest
  # records for it, so script and image stay one generation. The leg
  # never skips: no readable predecessor stops the job.
  first_record="${ORBIT_FIRST_RECORD_IMAGE_JOB:-}"
  first_sign="${ORBIT_FIRST_SIGN_EVIDENCE_JOB:-}"
  first_dir=.live/orbit/release/first
  mkdir -p "$first_dir"
  if [ -n "$first_record" ] || [ -n "$first_sign" ]; then
    for job_id in "$first_record" "$first_sign"; do
      case "$job_id" in
        ''|*[!0-9]*) echo "ORBIT_FIRST_RECORD_IMAGE_JOB and ORBIT_FIRST_SIGN_EVIDENCE_JOB must both be ai/orbit job IDs (#198)"; exit 1 ;;
      esac
    done
    first_from="ai/orbit jobs ${first_record}/${first_sign}"
    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_record}/artifacts/.orbit-supply-chain/orbit-release-manifest.json" \
      || { echo "could not read the earlier release manifest from ai/orbit job ${first_record} (#198)"; exit 1; }
    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/${first_sign}/artifacts/.orbit-supply-chain/orbit-release-manifest.json.sig" \
      || { echo "could not read the earlier release signature from ai/orbit job ${first_sign} (#198)"; exit 1; }
  else
    first_from="previous/ of ai/orbit's latest ${orbit_branch} build"
    no_predecessor="ai/orbit's latest ${orbit_branch} build has no readable predecessor (.orbit-supply-chain/previous/ in its record_image artifacts); to run the update leg from an earlier build instead, start the pipeline by hand with ORBIT_FIRST_RECORD_IMAGE_JOB and ORBIT_FIRST_SIGN_EVIDENCE_JOB set to that signed preview pipeline's record_image and sign_evidence job IDs (#263)"
    # By branch name, as the current manifest above: the artifacts
    # endpoint rejects full refs (#273). Both files are record_image's.
    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json?job=record_image" \
      || { echo "$no_predecessor"; exit 1; }
    curl --fail --silent --show-error --location --max-redirs 0 \
      --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
      --output "$first_dir/orbit-release-manifest.json.sig" \
      "${CI_API_V4_URL}/projects/${project}/jobs/artifacts/${orbit_branch_path}/raw/.orbit-supply-chain/previous/orbit-release-manifest.json.sig?job=record_image" \
      || { echo "$no_predecessor"; exit 1; }
  fi
  jq -e . "$first_dir/orbit-release-manifest.json" > /dev/null || { echo "${first_from} did not return a JSON release manifest (#198)"; exit 1; }
  [ -s "$first_dir/orbit-release-manifest.json.sig" ] || { echo "${first_from} returned an empty signature (#198)"; exit 1; }
  COSIGN_PUBLIC_KEY="$CI_PROJECT_DIR/.live/orbit/cosign.pub" \
    env -u CI_JOB_TOKEN -u CI_BUILD_TOKEN -u CI_REGISTRY_PASSWORD -u CI_DEPENDENCY_PROXY_PASSWORD -u CI_REPOSITORY_URL -u ORBIT_REGISTRY_TOKEN \
    bash .live/orbit/scripts/ci/verify-release-manifest.sh "$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json" \
    || { echo "the earlier release manifest from ${first_from} failed verification; nothing is installed from it (#198)"; exit 1; }
  jq -e '.channel == "preview"' "$first_dir/orbit-release-manifest.json" > /dev/null \
    || { echo "the earlier release manifest is not a preview release (#198)"; exit 1; }
  first_digest="$(jq -r .image.digest "$first_dir/orbit-release-manifest.json")"
  [ "$first_digest" != "$(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST")" ] \
    || { echo "the earlier release names the same image as the current one, ${first_digest}; an update between them proves nothing (#198)"; exit 1; }
  first_at="$(jq -r .recordedAt "$first_dir/orbit-release-manifest.json")"
  current_at="$(jq -r .recordedAt "$ORBIT_RELEASE_MANIFEST")"
  if [ "$first_at" = "$current_at" ] || [ "$(printf '%s\n%s\n' "$first_at" "$current_at" | sort | head -1)" != "$first_at" ]; then
    echo "the earlier release (recorded ${first_at}) is not older than the current one (recorded ${current_at}) (#198)"
    exit 1
  fi
  first_commit="$(jq -r .commit "$first_dir/orbit-release-manifest.json")"
  case "$first_commit" in
    *[!0-9a-f]*) first_commit="" ;;
  esac
  [ "${#first_commit}" -eq 40 ] || { echo "the earlier release manifest names no source commit (#198)"; exit 1; }
  curl --fail --silent --show-error --location --max-redirs 0 \
    --header "JOB-TOKEN: ${CI_JOB_TOKEN}" \
    --output "$first_dir/install.sh" \
    "${CI_API_V4_URL}/projects/${project}/repository/files/scripts%2Finstall.sh/raw?ref=${first_commit}" \
    || { echo "could not fetch scripts/install.sh from ${ORBIT_PROJECT} at ${first_commit} (#198)"; exit 1; }
  [ "sha256:$(sha256sum "$first_dir/install.sh" | cut -d' ' -f1)" = "$(jq -r '.files["install.sh"]' "$first_dir/orbit-release-manifest.json")" ] \
    || { echo "install.sh at ${first_commit} does not match the sha256 the earlier release manifest records for it (#198)"; exit 1; }
  export ORBIT_LIVE_FIRST_RELEASE_MANIFEST="$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json"
  export ORBIT_LIVE_FIRST_INSTALL_SCRIPT="$CI_PROJECT_DIR/$first_dir/install.sh"
  first_version="$(jq -r .version "$first_dir/orbit-release-manifest.json")"
  if [ -n "$first_record" ]; then
    echo "update leg: previous build from job IDs ${first_record}/${first_sign}"
  else
    echo "update leg: previous build ${first_version} ${first_digest} from previous/"
  fi
  echo "the update leg (#198) installs ${first_version} ${first_digest} (ai/orbit ${first_commit}) first, then updates to the current preview"`,
		new: `# Orbit's files come from live-prepare (#303), which read and verified
# them on the shared runner; this leg reads nothing from ai/orbit.
[ -s .live/install-script/install.sh ] \
  || { echo "live-prepare did not hand over install.sh / the release manifests; its log says why, or its artifacts expired: retry it (#303)"; exit 1; }
echo "install.sh and the release manifests come from live-prepare (Orbit's files from ${ORBIT_SOURCE:-an unrecorded source})"
if [ "${ORBIT_CHANNEL:-}" = "preview" ]; then
  first_dir=.live/orbit/release/first
  for handed in .live/orbit/release/orbit-release-manifest.json "$first_dir/orbit-release-manifest.json" "$first_dir/install.sh"; do
    [ -s "$handed" ] || { echo "live-prepare did not hand over install.sh / the release manifests (no ${handed}); its log says why (#303)"; exit 1; }
  done
  export ORBIT_RELEASE_MANIFEST="$CI_PROJECT_DIR/.live/orbit/release/orbit-release-manifest.json"
  # Install what the manifest names (#241): its image is in the GitLab
  # registry that built it. install.sh ignores the manifest's
  # repository and defaults to ghcr.io, where the GitHub copy can lag
  # by up to two hours or never arrive. The token goes in on stdin.
  image_repository="$(jq -r .image.repository "$ORBIT_RELEASE_MANIFEST")"
  export ORBIT_REGISTRY="${image_repository%%/*}"
  export ORBIT_REPOSITORY="${image_repository#*/}"
  # The registry login goes only to this GitLab's own registry (#193).
  [ "$ORBIT_REGISTRY" = "$CI_REGISTRY" ] || { echo "the manifest names image registry ${ORBIT_REGISTRY}, not this GitLab's ${CI_REGISTRY}; the registry login is not sent there (#193)"; exit 1; }
  # The login is per Docker client user, and the pulls are orbit-ci's
  # (#292), so it is orbit-ci that logs in. Nothing in this container
  # is hidden from the tests (see .live_leg, #303), so the login is
  # not the job token but a read-only deploy token on ai/orbit (scope
  # read_registry), held in the masked variables ORBIT_REGISTRY_USER
  # and ORBIT_REGISTRY_TOKEN (#301): leaked, it reads Orbit's images,
  # which are public on ghcr.io anyway. It is written to
  # /home/orbit-ci/.docker/config.json, mode 0600, inside this job's
  # container only, and after_script removes it.
  [ -n "${ORBIT_REGISTRY_USER:-}" ] && [ -n "${ORBIT_REGISTRY_TOKEN:-}" ] \
    || { echo "ORBIT_REGISTRY_USER and ORBIT_REGISTRY_TOKEN are not set: the live job pulls Orbit's image with a read-only deploy token, not the job token (#301)"; exit 1; }
  printf '%s' "$ORBIT_REGISTRY_TOKEN" \
    | setpriv --reuid=orbit-ci --regid=orbit-ci --init-groups -- env HOME=/home/orbit-ci \
      docker login "$ORBIT_REGISTRY" --username "$ORBIT_REGISTRY_USER" --password-stdin
  stat -c 'the registry login is in %n, owned by %U, mode %a' /home/orbit-ci/.docker/config.json
  echo "the manifest's image lives at ${image_repository}; install.sh pulls its digest from there, not from the GitHub mirror"
  echo "the verified preview release manifest names image $(jq -r .image.digest "$ORBIT_RELEASE_MANIFEST"); install.sh installs that"
  # The update leg's first install (#198), checked by live-prepare
  # exactly as the current release is.
  export ORBIT_LIVE_FIRST_RELEASE_MANIFEST="$CI_PROJECT_DIR/$first_dir/orbit-release-manifest.json"
  export ORBIT_LIVE_FIRST_INSTALL_SCRIPT="$CI_PROJECT_DIR/$first_dir/install.sh"
  echo "the update leg (#198) installs ${FIRST_VERSION:-?} ${FIRST_DIGEST:-?} (ai/orbit ${FIRST_COMMIT:-?}, ${FIRST_FROM:-?}) first, then updates to the current preview"`,
	},
	{ // C1
		jobs: []string{"renovate"},
		key:  "environment",
		new:  `{"action": "prepare", "name": "renovate"}`,
	},
	// C2: every job that runs branch or dependency code and needs no token
	// drops the credentials first, through the .drop_job_credentials
	// anchor, and fast's list (now that anchor) names the two Renovate
	// tokens as a backstop to their environment scope.
	{
		jobs: []string{"fast"},
		old:  `unset CI_JOB_TOKEN CI_BUILD_TOKEN CI_REGISTRY_PASSWORD CI_DEPENDENCY_PROXY_PASSWORD CI_REPOSITORY_URL ORBIT_REGISTRY_USER ORBIT_REGISTRY_TOKEN`,
		new:  `unset CI_JOB_TOKEN CI_BUILD_TOKEN CI_REGISTRY_PASSWORD CI_DEPENDENCY_PROXY_PASSWORD CI_REPOSITORY_URL ORBIT_REGISTRY_USER ORBIT_REGISTRY_TOKEN RENOVATE_TOKEN GITHUB_COM_TOKEN ORBIT_READ_TOKEN`,
	},
	{ // the drop fails the job rather than skip (#303)
		jobs: []string{"fast"},
		old:  `git remote set-url origin "${CI_PROJECT_URL}.git" 2>/dev/null || true`,
		new:  `git remote set-url origin "${CI_PROJECT_URL}.git"`,
	},
	{
		jobs: []string{"deps"},
		old:  `if [ "${DOCS_ONLY:-false}" = "true" ]; then`,
		new: dropJobCredentials303 + `
if [ "${DOCS_ONLY:-false}" = "true" ]; then`,
	},
	{
		jobs: []string{"visual"},
		old:  `tarball="go${GO_VERSION}.linux-amd64.tar.gz"`,
		new: dropJobCredentials303 + `
tarball="go${GO_VERSION}.linux-amd64.tar.gz"`,
	},
	// classify and gitleaks run on $UBUNTU_IMAGE, which ships no git, so
	// the drop comes after their git install (#303).
	{
		jobs: []string{"classify"},
		old:  `apt-get install -y -qq --no-install-recommends git`,
		new: `apt-get install -y -qq --no-install-recommends git
` + dropJobCredentials303,
	},
	{
		jobs: []string{"gitleaks"},
		old:  `apt-get install -y -qq --no-install-recommends ca-certificates curl git`,
		new: `apt-get install -y -qq --no-install-recommends ca-certificates curl git
` + dropJobCredentials303,
	},
}

// #307: the update leg's first install runs the launcher that shipped with
// the earlier release. live-prepare (addedBy303, not pinned line by line)
// keeps its archive only if the sha256 equals the signed earlier manifest's;
// the live leg, which live-fedora extends, checks that again, unpacks the
// binary where orbit-ci can run it and exports its path. Applies after #303.
var changedBy307 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{
		jobs: []string{"live"},
		old:  `  for handed in .live/orbit/release/orbit-release-manifest.json "$first_dir/orbit-release-manifest.json" "$first_dir/install.sh"; do`,
		new:  `  for handed in .live/orbit/release/orbit-release-manifest.json "$first_dir/orbit-release-manifest.json" "$first_dir/install.sh" "$first_dir/orbit-launcher_linux_amd64.tar.gz"; do`,
	},
	{
		jobs: []string{"live"},
		old:  `  export ORBIT_LIVE_FIRST_INSTALL_SCRIPT="$CI_PROJECT_DIR/$first_dir/install.sh"`,
		new: `  export ORBIT_LIVE_FIRST_INSTALL_SCRIPT="$CI_PROJECT_DIR/$first_dir/install.sh"
  # The launcher that shipped with that release installs it (#307).
  # live-prepare kept the archive only if its sha256 equals the one
  # the signed manifest records; it is checked again here, as the
  # other handed-over files are, then unpacked where orbit-ci can
  # read and run it.
  first_archive="$CI_PROJECT_DIR/$first_dir/orbit-launcher_linux_amd64.tar.gz"
  first_launcher_sha="$(jq -r '.files["orbit-launcher_linux_amd64.tar.gz"]' "$ORBIT_LIVE_FIRST_RELEASE_MANIFEST")"
  [ "sha256:$(sha256sum "$first_archive" | cut -d' ' -f1)" = "$first_launcher_sha" ] \
    || { echo "the earlier launcher archive does not match the sha256 the earlier release manifest records for it (${first_launcher_sha}) (#307)"; exit 1; }
  first_members="$(tar -tzf "$first_archive" | grep -E '(^|/)orbit-launcher$' || :)"
  [ -n "$first_members" ] && [ "$(printf '%s\n' "$first_members" | wc -l)" -eq 1 ] \
    || { echo "the earlier launcher archive holds no single orbit-launcher binary (#307)"; exit 1; }
  case "$first_members" in
    /*|*..*) echo "the earlier launcher archive names an unsafe path, ${first_members} (#307)"; exit 1 ;;
  esac
  first_launcher_dir=/tmp/orbit-earlier-launcher
  rm -rf "$first_launcher_dir"
  install -d -m 0755 "$first_launcher_dir"
  tar -xzf "$first_archive" -C "$first_launcher_dir" --no-same-owner -- "$first_members"
  chmod -R a+rX "$first_launcher_dir"
  [ -f "$first_launcher_dir/$first_members" ] && [ -x "$first_launcher_dir/$first_members" ] \
    || { echo "the earlier launcher archive's orbit-launcher is not an executable file (#307)"; exit 1; }
  export ORBIT_LIVE_FIRST_LAUNCHER="$first_launcher_dir/$first_members"
  echo "update leg: earlier launcher $(jq -r .launcher.tag "$ORBIT_LIVE_FIRST_RELEASE_MANIFEST") ($(jq -r .launcher.commit "$ORBIT_LIVE_FIRST_RELEASE_MANIFEST")) ${first_launcher_sha}"`,
	},
}

// The two lines of the .drop_job_credentials anchor (#303 C2).
const dropJobCredentials303 = `unset CI_JOB_TOKEN CI_BUILD_TOKEN CI_REGISTRY_PASSWORD CI_DEPENDENCY_PROXY_PASSWORD CI_REPOSITORY_URL ORBIT_REGISTRY_USER ORBIT_REGISTRY_TOKEN RENOVATE_TOKEN GITHUB_COM_TOKEN ORBIT_READ_TOKEN
git remote set-url origin "${CI_PROJECT_URL}.git"`

// #306: each pipeline runs only what an earlier stage could not (the table is
// in ci_stages_test.go, which checks the behaviour). workflow: now creates a
// pipeline only for a merge request into dev, preview or main, a push to
// main, Renovate's schedule and a pipeline started by hand; the "branch with
// an open merge request" rule goes, because no branch push creates one. The
// checks run on a merge request into dev and on a hand-started pipeline;
// visual's and the live jobs' merge-request routes name their target; the
// live jobs gain the automatic route for a merge request into preview.
// Applies after #307.
const workflowChangedBy306 = `{"rules": [{"if": "$CI_COMMIT_TAG", "when": "never"}, {"if": "$CI_PIPELINE_SOURCE == \"merge_request_event\" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME =~ /^(dev|preview|main)$/"}, {"if": "$CI_PIPELINE_SOURCE == \"push\" && $CI_COMMIT_BRANCH == \"main\""}, {"if": "$CI_PIPELINE_SOURCE == \"schedule\" && $RENOVATE == \"true\""}, {"if": "$CI_PIPELINE_SOURCE == \"web\" || $CI_PIPELINE_SOURCE == \"api\""}]}`

var changedBy306 = []struct {
	jobs     []string
	key      string
	old, new string
}{
	{ // the .checks_rules anchor: added keys, none of these jobs had rules
		jobs: []string{"classify", "fast", "deps", "gitleaks"},
		key:  "rules",
		new:  `[{"if": "$CI_PIPELINE_SOURCE == \"merge_request_event\" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME == \"dev\""}, {"if": "$CI_PIPELINE_SOURCE == \"web\" || $CI_PIPELINE_SOURCE == \"api\""}]`,
	},
	{ // classify only ever sees a merge request into dev now, so the
		// "not main" exception is dead
		jobs: []string{"classify"},
		old: `if [ "${CI_PIPELINE_SOURCE:-}" = "merge_request_event" ] \
  && [ "${CI_MERGE_REQUEST_TARGET_BRANCH_NAME:-}" != "main" ]; then`,
		new: `if [ "${CI_PIPELINE_SOURCE:-}" = "merge_request_event" ]; then`,
	},
	{
		jobs: []string{"visual"},
		key:  "rules",
		old:  `"if": "$CI_PIPELINE_SOURCE == \"merge_request_event\""}]`,
		new:  `"if": "$CI_PIPELINE_SOURCE == \"merge_request_event\" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME == \"dev\""}]`,
	},
	{ // the promotion into preview: automatic and blocking
		jobs: []string{"live"},
		key:  "rules",
		old:  `run-live-matrix(,|$)/"}, {"allow_failure"`,
		new:  `run-live-matrix(,|$)/"}, {"if": "$CI_PIPELINE_SOURCE == \"merge_request_event\" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME == \"preview\""}, {"allow_failure"`,
	},
	{ // the pin-change route belongs to merge requests into dev
		jobs: []string{"live"},
		key:  "rules",
		old:  `"if": "$CI_PIPELINE_SOURCE == \"merge_request_event\"", "when": "manual"}`,
		new:  `"if": "$CI_PIPELINE_SOURCE == \"merge_request_event\" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME == \"dev\"", "when": "manual"}`,
	},
}

// The one top-level variable #183 adds: the Fedora leg's image.
const variableAddedBy183 = "FEDORA_IMAGE"

// applyChangedOnPurpose rewrites one ce4b412 job's keys the way #216, then
// #198, then #241, then #273, then #264, then #236, then #195, then #292, then #277, then #193, then #263, then the token review, then #183, then #300, then #298, then #301, then #303, then #307, then #306 do. Each rewrite for this job must
// match exactly once; a key it names that the job lacks is a failure too (bar
// the additions changedBy264 declares), so no entry can go unused.
func applyChangedOnPurpose(t *testing.T, job string, keys map[string]string) {
	t.Helper()
	all := append([]struct {
		jobs     []string
		key      string
		old, new string
	}(nil), changedBy216...)
	all = append(all, changedBy198...)
	all = append(all, changedBy241...)
	all = append(all, changedBy273...)
	all = append(all, changedBy264...)
	all = append(all, changedBy236...)
	all = append(all, changedBy195...)
	all = append(all, changedBy292...)
	all = append(all, changedBy277...)
	all = append(all, changedBy193...)
	all = append(all, changedBy263...)
	all = append(all, changedByTokenReview...)
	all = append(all, changedBy183...)
	all = append(all, changedBy300...)
	all = append(all, changedBy298...)
	all = append(all, changedBy301...)
	all = append(all, changedBy303...)
	all = append(all, changedBy307...)
	all = append(all, changedBy306...)
	for _, r := range all {
		applies := false
		for _, j := range r.jobs {
			applies = applies || j == job
		}
		if !applies {
			continue
		}
		if r.key != "" && r.old == "" { // an added key
			if _, has := keys[r.key]; has {
				t.Fatalf("%s already has %s at ce4b412; the rewrite adding it is wrong", job, r.key)
			}
			keys[r.key] = r.new
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

// The four release jobs share their work through two hidden templates (#264):
// the version script and the release-cli job body are each written once, and
// each button differs from the other only in RELEASE_BUMP. The check runs
// once in each template, with sh: the button's, and the tagging job's again
// under `resource_group: release`, so two presses cannot both tag.
func TestReleaseJobsShareTheirBodies(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for needle, want := range map[string]int{
		"release-tag-check.sh":                       2,
		"sh scripts/ci/release-tag-check.sh":         2,
		"bash scripts/ci/release-tag-check.sh":       0,
		"resource_group:":                            1,
		"calculateversion -hotfix":                   1,
		"registry.gitlab.com/gitlab-org/release-cli": 1,
		"tag_name:": 1,
	} {
		if n := countLinesContaining(ci, needle); n != want {
			t.Errorf("%q is written on %d lines of .gitlab-ci.yml; want exactly %d, inside a release template (#264)", needle, n, want)
		}
	}
	doc := loadCI(t, ci)
	bump := map[string]string{"release:patch": "patch", "release:minor": "minor"}
	for job, want := range bump {
		e, err := doc.effectiveJob(job)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.Keys["variables"].dump(); got != `{"RELEASE_BUMP": "`+want+`"}` {
			t.Errorf("%s variables are %s, want only RELEASE_BUMP=%s", job, got, want)
		}
		if e.Keys["needs"] != nil {
			t.Errorf("%s has `needs:`; the button must not be playable before every earlier stage passes (#178)", job)
		}
		if w := e.Keys["rules"].dump(); !strings.Contains(w, `"when": "manual"`) || !strings.Contains(w, `"allow_failure": "true"`) {
			t.Errorf("%s must be a manual, allow_failure button: %s", job, w)
		}
	}
	for follow, button := range map[string]string{"release:gitlab:patch": "release:patch", "release:gitlab:minor": "release:minor"} {
		e, err := doc.effectiveJob(follow)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := e.Keys["needs"].dump(), `[{"artifacts": "true", "job": "`+button+`"}]`; got != want {
			t.Errorf("%s needs %s, want %s", follow, got, want)
		}
		if strings.Contains(e.Keys["rules"].dump(), `"when"`) {
			t.Errorf("%s must run by itself once its button has passed, not be a second button", follow)
		}
		if rg := e.Keys["resource_group"]; rg == nil || rg.dump() != `"release"` {
			t.Errorf("%s has no `resource_group: release`; tagging must run one job at a time (#264)", follow)
		}
		if first, _, _ := strings.Cut(e.Script, "\n"); !strings.HasPrefix(first, "sh scripts/ci/release-tag-check.sh ") {
			t.Errorf("%s does not re-run the tag check first, under its lock; first line is %q (#264)", follow, first)
		}
	}
	for job := range bump {
		e, err := doc.effectiveJob(job)
		if err != nil {
			t.Fatal(err)
		}
		if e.Keys["resource_group"] != nil {
			t.Errorf("%s has a resource_group; only the tagging jobs take the lock (#264)", job)
		}
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
	// The placeholder line itself, ahead of the skip: other fast lines name
	// coverage.out too (the coverage floor reads it), so a mere mention
	// proves nothing.
	placeholderAt, skipAt := -1, -1
	for i, l := range strings.Split(fast.Script, "\n") {
		switch {
		case l == fastPlaceholder && placeholderAt < 0:
			placeholderAt = i
		case strings.Contains(l, `DOCS_ONLY:-false`) && skipAt < 0:
			skipAt = i
		}
	}
	if placeholderAt < 0 || placeholderAt > skipAt {
		t.Errorf("job `fast` must write the empty profile (%s) before the docs-only skip: the artifact is declared `when: always`", fastPlaceholder)
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

// The credential drop's two lines (#303): the anchor that resets origin, its
// lines, and the jobs that alias it.
const dropSetURL = `git remote set-url origin "${CI_PROJECT_URL}.git"`

func dropJobCredentialsAnchor(t *testing.T, doc *yamlDoc) (lines []string, jobs []string) {
	t.Helper()
	holders := anchorsHolding(doc, "git remote set-url origin")
	if len(holders) != 1 {
		t.Fatalf("want exactly one anchor resetting the origin URL (.drop_job_credentials, #303); found %v", holders)
	}
	for name, users := range holders {
		entries, err := doc.anchors[name].node.flatScripts()
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			for _, l := range strings.Split(e, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					lines = append(lines, l)
				}
			}
		}
		for _, u := range users {
			if !strings.HasPrefix(u, ".") {
				jobs = append(jobs, u)
			}
		}
	}
	return lines, jobs
}

// The drop rewrites the clone's origin URL with git, so it can only work
// once git is in the image (#303). $UBUNTU_IMAGE and $FEDORA_IMAGE ship no
// git; a job on either has to install it first, and nothing but package
// installs (which run no branch code and read no token) may run before the
// drop.
func TestDropJobCredentialsRunsWhereGitExists(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	anchorLines, jobs := dropJobCredentialsAnchor(t, doc)
	have := map[string]bool{}
	for _, j := range jobs {
		have[j] = true
	}
	for _, want := range []string{"classify", "deps", "fast", "gitleaks", "visual"} {
		if !have[want] {
			t.Errorf("job %q no longer uses the credential drop (#303); users: %v", want, jobs)
		}
	}
	unsetLine := ""
	for _, l := range anchorLines {
		if strings.HasPrefix(l, "unset ") {
			unsetLine = l
		}
	}
	prologue := map[string]bool{}
	for name := range anchorsHolding(doc, "apt-get update -qq") {
		entries, err := doc.anchors[name].node.flatScripts()
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			for _, l := range strings.Split(e, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					prologue[l] = true
				}
			}
		}
	}
	noGit := map[string]bool{"$UBUNTU_IMAGE": true, "$FEDORA_IMAGE": true}
	gitInstall := regexp.MustCompile(`^(apt-get|dnf) install\b.*(^|\s)git(\s|$)`)
	pkgInstall := regexp.MustCompile(`^(apt-get|dnf) install\b`)
	for _, job := range jobs {
		e, err := doc.effectiveJob(job)
		if err != nil {
			t.Fatal(err)
		}
		image := ""
		if img := e.Keys["image"]; img != nil {
			if img.kind == 'm' {
				if n := img.vals["name"]; n != nil {
					image = n.str
				}
			} else {
				image = img.str
			}
		}
		lines := strings.Split(e.Script, "\n")
		at := -1
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "git remote set-url origin") {
				at = i
				break
			}
		}
		if at < 0 {
			t.Errorf("job %q aliases the credential drop but never resets origin (#303)", job)
			continue
		}
		installed := false
		for _, l := range lines[:at] {
			l = strings.TrimSpace(l)
			switch {
			case gitInstall.MatchString(l):
				installed = true
			case l == unsetLine, prologue[l], pkgInstall.MatchString(l):
			default:
				t.Errorf("job %q runs %q before the credential drop; only package installs may (#303)", job, l)
			}
		}
		if noGit[image] && !installed {
			t.Errorf("job %q runs on %s, which ships no git, and does not install git before `git remote set-url` (line %d), so the clone keeps the job token (#303)", job, image, at+1)
		}
	}
}

// A hygiene step that cannot run must stop the job, never skip (#303): the
// set-url line carries no `2>/dev/null`, `|| true` or `|| :`.
func TestDropJobCredentialsFailsClosed(t *testing.T) {
	lines, _ := dropJobCredentialsAnchor(t, loadCI(t, readRepoFile(t, ".gitlab-ci.yml")))
	found := false
	for _, l := range lines {
		if !strings.HasPrefix(l, "git remote set-url origin") {
			continue
		}
		found = true
		if l != dropSetURL {
			t.Errorf("the credential drop's set-url line is %q; want exactly %q, so a job that cannot reset origin fails (#303)", l, dropSetURL)
		}
	}
	for _, l := range lines {
		for _, bad := range []string{"2>/dev/null", "|| true", "|| :"} {
			if strings.Contains(l, bad) {
				t.Errorf("the credential drop's line %q holds %q; it must fail the job, not skip (#303)", l, bad)
			}
		}
	}
	if !found {
		t.Error("the credential drop has no set-url line (#303)")
	}
}

// The drop's unset line names every credential GitLab injects or this
// project defines that the five jobs do not use (#301, #303).
func TestDropJobCredentialsUnsetsEveryToken(t *testing.T) {
	lines, _ := dropJobCredentialsAnchor(t, loadCI(t, readRepoFile(t, ".gitlab-ci.yml")))
	named := map[string]bool{}
	for _, l := range lines {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "unset" {
			for _, n := range f[1:] {
				named[n] = true
			}
		}
	}
	for _, want := range []string{
		"CI_JOB_TOKEN", "CI_BUILD_TOKEN", "CI_REGISTRY_PASSWORD", "CI_DEPENDENCY_PROXY_PASSWORD",
		"CI_REPOSITORY_URL", "ORBIT_REGISTRY_USER", "ORBIT_REGISTRY_TOKEN", "RENOVATE_TOKEN",
		"GITHUB_COM_TOKEN", "ORBIT_READ_TOKEN",
	} {
		if !named[want] {
			t.Errorf("the credential drop does not unset %s (#303)", want)
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

	// Every job's effective image keeps the policy, through the merge anchor
	// in its own image. A job with no image of its own would run `default:`'s
	// image whole, so that is where the check looks then -- and `default:`
	// sets no image here, so such a job fails below.
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
