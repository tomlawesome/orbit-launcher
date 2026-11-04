package ui

import (
	"strings"
	"testing"
)

// #332: with no stderr line that starts "Orbit installer: ", an
// update-path stop shows the stderr tail as every other failure does.
func TestEngineRun_UpdatePathWithoutInstallerLineShowsTheTail(t *testing.T) {
	screen := updatePathStop(t, "docker: some unrelated warning")

	if !strings.Contains(flat(screen), "some unrelated warning") {
		t.Errorf("the stop screen lacks the stderr tail:\n%s", screen)
	}
	wantNoRerun(t, screen)
}
