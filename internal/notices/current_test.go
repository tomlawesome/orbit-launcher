package notices_test

import (
	"os/exec"
	"strings"
	"testing"
)

// #218 (licence tools): after the licence-file rule and the tool skeleton
// are merged, the committed THIRD_PARTY_NOTICES.txt must still be exactly
// what tools/licencenotices builds from what is linked. The same check CI's
// deps job runs, kept here so the merge cannot land without it. Passes
// already: it guards behaviour the change has to keep.
func TestCommittedNoticesAreCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list for every shipped platform")
	}
	cmd := exec.Command("go", "run", "./tools/licencenotices", "-check")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "is current") {
		t.Fatalf("licencenotices -check: %v\n%s", err, out)
	}
}
