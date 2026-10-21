package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// X-Q5 (#195): two workflows pinning one action at different commits cannot
// be cleared by regenerating the policy, because generating it keeps only
// the last pin and so hides the other. The check must say so, and must not
// end with the "Regenerate it" advice it gives for every other problem.
func TestCheckDoesNotAdviseRegeneratingForConflictingPins(t *testing.T) {
	root := goodRepo(t)
	serve(t, goodRoutes())
	if code, _, stderr := runCmd("-write"); code != 0 {
		t.Fatalf("setup: exit %d; stderr: %s", code, stderr)
	}
	other := strings.Repeat("4", 40)
	release := "      - uses: actions/checkout@" + other + " # v7.0.1\n"
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "release.yml"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCmd("-root", root)

	if code != 1 || stdout != "" {
		t.Fatalf("exit %d, stdout %q; want exit 1 and nothing on stdout", code, stdout)
	}
	if !strings.Contains(stderr, "ci.yml") || !strings.Contains(stderr, "release.yml") {
		t.Errorf("stderr does not name both workflows:\n%s", stderr)
	}
	low := strings.ToLower(stderr)
	if strings.Contains(low, "regenerat") || strings.Contains(stderr, "-write") {
		t.Errorf("stderr advises regenerating, which would hide one of the pins:\n%s", stderr)
	}
}
