// The update leg's first install uses the earlier release's own launcher
// (#307). A real deployment on the earlier preview release was installed
// with the launcher that shipped with it, so the leg installs the earlier
// release with that launcher, then updates and removes with the launcher
// under test. `live-prepare` finds the earlier launcher's archive among
// ai/orbit's `build_launcher` job artifacts with ORBIT_READ_TOKEN, keeps it
// only if its sha256 equals the one the signed earlier manifest records for
// it, fails (never falls back to the current launcher, never skips) when
// none matches, and hands it on under .live/orbit/release/first/. Each leg
// re-checks that sha256, extracts the binary and exports its path as
// ORBIT_LIVE_FIRST_LAUNCHER.
//
// Read as text, for the reason ci_rules_test.go gives.
package supplychain

import (
	"strings"
	"testing"
)

const earlierLauncherArchive = "orbit-launcher_linux_amd64.tar.gz"

// failsAfter reports whether, at or after the first line of job containing
// marker, some line containing every word in words stops the job with
// `exit 1` on that line or the next.
func failsAfter(job []string, marker string, words ...string) bool {
	from := -1
	for i, l := range job {
		if strings.Contains(l, marker) {
			from = i
			break
		}
	}
	if from < 0 {
		return false
	}
	for i := from; i < len(job); i++ {
		l := strings.ToLower(job[i])
		ok := true
		for _, w := range words {
			if !strings.Contains(l, strings.ToLower(w)) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		next := ""
		if i+1 < len(job) {
			next = job[i+1]
		}
		if strings.Contains(job[i], "exit 1") || strings.HasPrefix(next, "exit 1") {
			return true
		}
	}
	return false
}

func TestLivePrepareFindsTheEarlierLauncherInOrbitsBuildLauncherJob(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	text := strings.Join(liveJob(t, ci, "live-prepare"), "\n")
	for _, c := range []struct{ want, why string }{
		{"build_launcher", "look for the archive among ai/orbit's build_launcher job artifacts"},
		{earlierLauncherArchive, "name the archive it looks for"},
		{"ORBIT_READ_TOKEN", "read ai/orbit with the read-only token"},
	} {
		if !strings.Contains(text, c.want) {
			t.Errorf("the `live-prepare` job must contain %q: %s (#307)", c.want, c.why)
		}
	}
}

func TestLivePrepareKeepsAnEarlierLauncherOnlyIfItsSha256MatchesTheSignedManifest(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	text := strings.Join(liveJob(t, ci, "live-prepare"), "\n")
	manifestEntry := `.files["` + earlierLauncherArchive + `"]`
	if !strings.Contains(text, manifestEntry) {
		t.Errorf("the `live-prepare` job must read %s from the verified earlier manifest: the archive is kept only if its sha256 equals it (#307)", manifestEntry)
	}
	if !strings.Contains(text, "sha256sum") {
		t.Error("the `live-prepare` job must compute the archive's sha256 to compare it with the manifest's (#307)")
	}
}

func TestLivePrepareFailsWhenNoEarlierLauncherMatches(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	if !failsAfter(job, "build_launcher", "launcher") {
		t.Error("the `live-prepare` job must exit 1 with a message about the launcher when no archive from build_launcher matches the manifest's sha256 (#307)")
	}
	if !failsAfter(job, "build_launcher", "launcher", "commit") {
		t.Error("the `live-prepare` job's no-earlier-launcher failure must name the commit it looked for (#307)")
	}
}

func TestLivePrepareNeverFallsBackFromTheEarlierLauncherOrSkipsTheLeg(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for _, l := range liveJob(t, ci, "live-prepare") {
		low := strings.ToLower(l)
		if !strings.Contains(low, "launcher") {
			continue
		}
		for _, bad := range []string{"fall back", "falling back", "fallback", "skip", "current launcher instead"} {
			if strings.Contains(low, bad) && !strings.Contains(low, "never") && !strings.Contains(low, "not ") {
				t.Errorf("the `live-prepare` job must never fall back to another launcher or skip the update leg when the earlier one is missing (#307)\nline: %s", l)
			}
		}
	}
}

func TestLivePrepareHandsTheEarlierLauncherOnWithTheFirstReleaseFiles(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	doc := loadCI(t, ci)
	found := false
	for _, l := range liveJob(t, ci, "live-prepare") {
		if strings.Contains(l, earlierLauncherArchive) &&
			(strings.Contains(l, "first_dir") || strings.Contains(l, ".live/orbit/release/first")) {
			found = true
		}
	}
	if !found {
		t.Errorf("the `live-prepare` job must place %s with the other first-release files, under .live/orbit/release/first/ (#307)", earlierLauncherArchive)
	}
	prep, err := doc.effectiveJob("live-prepare")
	if err != nil {
		t.Fatal(err)
	}
	if got := prep.Keys["artifacts"].dump(); !strings.Contains(got, `".live/orbit/release/"`) {
		t.Errorf("the `live-prepare` job's artifacts must include .live/orbit/release/, which carries first/; artifacts are %s (#307)", got)
	}
}

func TestLiveLegsRecheckTheEarlierLauncherAgainstTheManifest(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	manifestEntry := `.files["` + earlierLauncherArchive + `"]`
	for _, name := range []string{"live", "live-fedora"} {
		leg := strings.Join(liveJob(t, ci, name), "\n")
		for _, c := range []struct{ want, why string }{
			{earlierLauncherArchive, "use the handed-over archive"},
			{manifestEntry, "re-check its sha256 against the signed manifest, as it does the other handed-over files"},
			{"sha256sum", "compute the archive's sha256"},
		} {
			if !strings.Contains(leg, c.want) {
				t.Errorf("the `%s` job must contain %q: %s (#307)", name, c.want, c.why)
			}
		}
	}
}

func TestLiveLegsExportTheEarlierLauncherAndSayWhichOne(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for _, name := range []string{"live", "live-fedora"} {
		leg := strings.Join(liveJob(t, ci, name), "\n")
		for _, c := range []struct{ want, why string }{
			{"export ORBIT_LIVE_FIRST_LAUNCHER=", "hand the test the earlier launcher's binary"},
			{"update leg: earlier launcher", "say which earlier launcher it installed, in a line a watcher can grep"},
			{"orbit-launcher", "extract the orbit-launcher binary from the archive"},
		} {
			if !strings.Contains(leg, c.want) {
				t.Errorf("the `%s` job must contain %q: %s (#307)", name, c.want, c.why)
			}
		}
	}
}

func TestLiveLegsKeepUsingNoTokenToFetchTheEarlierLauncher(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for _, name := range []string{"live", "live-fedora"} {
		for _, l := range liveJob(t, ci, name) {
			if strings.Contains(l, earlierLauncherArchive) && (strings.Contains(l, "curl ") || strings.Contains(l, "ORBIT_READ_TOKEN")) {
				t.Errorf("the `%s` job must take the earlier launcher from live-prepare's artifacts, not fetch it (#303, #307)\nline: %s", name, l)
			}
		}
	}
}

func TestLiveUpdateTestInstallsEarlierWithTheEarlierLauncherAndFailsWithoutIt(t *testing.T) {
	goLeg := readRepoFile(t, "test", "live", "update_test.go")
	const env = `"ORBIT_LIVE_FIRST_LAUNCHER"`
	if !strings.Contains(goLeg, env) {
		t.Fatalf("test/live/update_test.go must read %s, the name the `live` job exports (#307)", env)
	}
	lines := strings.Split(goLeg, "\n")
	for i, l := range lines {
		if !strings.Contains(l, "ORBIT_LIVE_FIRST_LAUNCHER") {
			continue
		}
		// The read is followed, within a few lines, by the checks on it.
		end := i + 8
		if end > len(lines) {
			end = len(lines)
		}
		for _, near := range lines[i:end] {
			if strings.Contains(near, "t.Skip") {
				t.Errorf("test/live/update_test.go must fail, not skip, when ORBIT_LIVE_FIRST_LAUNCHER is missing (#307)\nline: %s", near)
			}
		}
	}
	if !strings.Contains(goLeg, "InstallEarlier") {
		t.Error("test/live/update_test.go must keep the InstallEarlier step (#307)")
	}
}
