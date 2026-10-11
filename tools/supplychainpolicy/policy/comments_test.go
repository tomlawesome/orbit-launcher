package policy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/supplychainpolicy/policy"
)

// SU-7: three comments that no longer match what the repository does.
// They are plain text, so they are checked as text. Each "gone" check is
// paired with the replacement being present, so deleting the sentence
// outright does not pass.

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Renovate replaced Dependabot (.gitlab-ci.yml's renovate job), so the
// currency sentence must not credit Dependabot -- in the committed policy
// file, in the generator that writes it, or in the package that names it.
func TestPolicyCommentsNameRenovateNotDependabot(t *testing.T) {
	for _, rel := range []string{
		".github/supply-chain-policy.json",
		"tools/supplychainpolicy/main.go",
		"tools/supplychainpolicy/policy/policy.go",
	} {
		text := repoFile(t, rel)
		if strings.Contains(strings.ToLower(text), "dependabot") {
			t.Errorf("%s still says Dependabot drives currency; Renovate does", rel)
		}
	}
	if !strings.Contains(repoFile(t, ".github/supply-chain-policy.json"), "Renovate") {
		t.Error("the policy file's comment does not say who drives currency (Renovate)")
	}
}

// The generator's fileComment and the committed JSON's _comment are one
// text typed twice; regenerating from the committed file must reproduce
// it byte for byte, so correcting one without the other fails here.
// Passes already: it guards behaviour the change has to keep.
func TestCommittedPolicyIsWhatTheGeneratorWrites(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	p, err := policy.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := policy.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := repoFile(t, policy.PolicyPath); got != string(want) {
		t.Error("the committed policy file differs from what Marshal writes for it (its _comment and the generator's comment disagree?)")
	}
}

// .github/coverage-floors.txt says where the floors check is wired. Both
// CIs run scripts/coverage.sh -race, not a bare go test -coverprofile.
func TestCoverageFloorsCommentNamesTheRealWiring(t *testing.T) {
	text := repoFile(t, ".github/coverage-floors.txt")
	if strings.Contains(text, "go test -coverprofile=coverage.out ./...") {
		t.Error("coverage-floors.txt still says the check follows a bare `go test -coverprofile`")
	}
	// The wiring sentence opens the file; later lines may mention the script
	// for other reasons, so look only at the opening comment paragraph.
	head, _, _ := strings.Cut(text, "\n#\n")
	if !strings.Contains(head, "scripts/coverage.sh") {
		t.Error("the opening comment of coverage-floors.txt does not name scripts/coverage.sh, which both CIs run")
	}
}

// The live test's comment cites the CI artifact glob for the raw log. The
// real globs are .live/live-raw.log.* (GitLab) and live-raw.log.* (GitHub).
func TestLiveTestCommentCitesTheRealArtifactGlob(t *testing.T) {
	text := repoFile(t, "test/live/live_test.go")
	if strings.Contains(text, ".orbit-launcher-live-raw.log") {
		t.Error("live_test.go still cites the old artifact glob .orbit-launcher-live-raw.log*")
	}
	if !strings.Contains(text, "live-raw.log.*") {
		t.Error("live_test.go does not cite the real artifact glob live-raw.log.*")
	}
}
