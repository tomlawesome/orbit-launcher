package ui

// v0.4.3 group B, W5 (ratified design, issue #277): "move them out of the
// folder, then install again" is never said as the cause. The launcher
// cannot tell a Docker refusal from a not-empty one, so the listing line
// carries the fact and the next-step line is the general one:
//
//	in the folder: notes.txt, old-site/ and 3 more — the installer needs it empty
//	fix what the installer names above, then install again
//	to use another folder, exit and run the launcher from there
//
// An Install stopped before writing, with files in the folder, shows these
// three lines and never "move them out". With an empty listing the first
// line is absent and the other two show. The listing leaves out .env-orbit,
// .orbit-secrets and a real orbit-install-backups folder (Orbit's
// pre-provisioned rule). Update shows none of them.
//
// Written from the specification alone. The folder is read for the
// listing by a command (L3), which stoppedInstall's drive runs.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var gbStDockerRefused = []string{"Orbit installer: Docker is required."}

// gbStThreeLines checks the listing line (names, exactly) then the two
// general lines, in that order, on the screen.
func gbStThreeLines(t *testing.T, s, names string) {
	t.Helper()
	words := flat(s)
	order := []string{
		"in the folder: " + names + " — " + gbStListingTail,
		stepFixNamedAbove,
		stepAnotherFolder,
	}
	at := -1
	for _, want := range order {
		i := strings.Index(words, want)
		if i < 0 {
			t.Errorf("%q is missing from the screen:\n%s", want, s)
			continue
		}
		if i < at {
			t.Errorf("%q is out of order:\n%s", want, s)
		}
		at = i
	}
	if strings.Contains(words, "move them out") {
		t.Errorf("the screen says \"move them out\":\n%s", s)
	}
}

// Files in the folder: the three lines, whichever refusal the installer
// named (the launcher cannot tell them apart).
func TestGroupB_W5_AFolderWithFilesShowsTheThreeLinesWhateverTheRefusal(t *testing.T) {
	for name, stderr := range map[string][]string{
		"not empty":       notEmptyStderr,
		"Docker required": gbStDockerRefused,
	} {
		t.Run(name, func(t *testing.T) {
			dir := folderWith(t, "notes.txt", "old-site/")
			s := screen(stoppedInstall(t, dir, hostRefused, exit1(stderr...)))
			gbStThreeLines(t, s, "notes.txt, old-site/")
			if got := screenLabels(s); !slices.Equal(got, rowsAgain) {
				t.Errorf("rows = %q, want %q", got, rowsAgain)
			}
		})
	}
}

// More than three entries: three names and a count, then the same tail.
func TestGroupB_W5_ManyEntriesListThreeCountTheRestAndKeepTheTail(t *testing.T) {
	dir := folderWith(t, "e/", "c.md", "a.md", "d.md", "b/", ".env-orbit")
	s := screen(stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...)))
	gbStThreeLines(t, s, "a.md, b/, c.md and 2 more")
	if strings.Contains(flat(s), "d.md") {
		t.Errorf("the listing names more than three entries:\n%s", s)
	}
}

// An empty listing (only the installer's own files): the first line is
// absent, the other two show.
func TestGroupB_W5_AnEmptyListingLeavesTheOtherTwoLines(t *testing.T) {
	dir := folderWith(t, ".env-orbit", ".orbit-secrets/", "orbit-install-backups/")
	s := screen(stoppedInstall(t, dir, hostRefused, exit1(gbStDockerRefused...)))
	words := flat(s)
	for _, gone := range []string{"in the folder", gbStListingTail, "move them out"} {
		if strings.Contains(words, gone) {
			t.Errorf("the screen shows %q for an empty listing:\n%s", gone, s)
		}
	}
	i, j := strings.Index(words, stepFixNamedAbove), strings.Index(words, stepAnotherFolder)
	if i < 0 || j < 0 || i > j {
		t.Errorf("want %q then %q (found at %d, %d):\n%s", stepFixNamedAbove, stepAnotherFolder, i, j, s)
	}
}

// The listing leaves out .env-orbit, .orbit-secrets and a real
// orbit-install-backups folder; a file or a link of that name is listed.
func TestGroupB_W5_TheListingExcludesTheInstallersOwnEntries(t *testing.T) {
	t.Run("own entries hidden", func(t *testing.T) {
		dir := folderWith(t, "notes.txt", ".env-orbit", ".orbit-secrets/", "orbit-install-backups/")
		s := screen(stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...)))
		gbStThreeLines(t, s, "notes.txt")
		for _, hidden := range []string{".env-orbit", ".orbit-secrets", "orbit-install-backups"} {
			if strings.Contains(flat(s), hidden) {
				t.Errorf("the listing shows %q, which is the installer's own:\n%s", hidden, s)
			}
		}
	})
	t.Run("a file of that name is listed", func(t *testing.T) {
		dir := folderWith(t, "a", "orbit-install-backups")
		s := screen(stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...)))
		gbStThreeLines(t, s, "a, orbit-install-backups")
	})
	t.Run("a link of that name is listed", func(t *testing.T) {
		dir := folderWith(t, "a")
		if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "orbit-install-backups")); err != nil {
			t.Fatal(err)
		}
		s := screen(stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...)))
		gbStThreeLines(t, s, "a, orbit-install-backups")
	})
}

// Update shows none of the three lines, with files in the folder or not.
func TestGroupB_W5_UpdateShowsNoneOfTheThreeLines(t *testing.T) {
	for name, msgs := range map[string][]any{
		"retry":  {hostDone, imageRetry, exit1("Orbit installer: Could not pull the image.")},
		"abort":  {hostAborted, exit1("Orbit installer: Update requires a recognized existing Orbit deployment.")},
		"refuse": {hostRefused, exit1(notEmptyStderr...)},
	} {
		t.Run(name, func(t *testing.T) {
			dir := folderWith(t, "notes.txt", "old-site/")
			s := screen(stoppedUpdate(t, dir, msgs...))
			if !strings.Contains(s, "Update stopped") {
				t.Fatalf("not an Update stopped screen:\n%s", s)
			}
			for _, gone := range []string{"in the folder", gbStListingTail, stepFixNamedAbove, stepAnotherFolder, "move them out", "notes.txt"} {
				if strings.Contains(flat(s), gone) {
					t.Errorf("Update's screen shows %q:\n%s", gone, s)
				}
			}
		})
	}
}

// No stopped screen, for any way a run stops, says "move them out".
func TestGroupB_W5_NoStoppedScreenSaysMoveThemOut(t *testing.T) {
	for _, c := range stopCases() {
		t.Run(c.name, func(t *testing.T) {
			m, _ := c.build(t)
			if s := screen(m); strings.Contains(flat(s), "move them out") {
				t.Errorf("the screen says \"move them out\":\n%s", s)
			}
		})
	}
}
