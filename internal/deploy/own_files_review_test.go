package deploy

// #285 review fixes. Owner 20a and note 41988: a folder with an accepted
// .orbit-files never gets the whole-folder "rm -rf <folder>"; only a
// folder without the list does. These tests only build and read.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func wholeFolderRm(cmd, dir string) bool {
	return strings.Contains(cmd, "rm -rf "+shellQuote(dir)) || strings.HasSuffix(cmd, "rm -rf "+dir)
}

func requireNotRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permissions do not bind root")
	}
}

// 1: an accepted list that names nothing present still means "never the
// whole folder"; backups and other leftovers survive.
func TestRemovalCommand_AcceptedListNamingNothingPresentIsNeverTheWholeFolder(t *testing.T) {
	dir := ownTree(t, manifestOf("docker-compose.yml", "scripts/backup.sh"), "backups/a.tar", "notes.txt")
	own := mustOwn(t, dir)
	if own.ManifestIgnored != nil {
		t.Fatalf("ManifestIgnored = %v, want nil", own.ManifestIgnored)
	}
	cmd := mustRemovalCommand2(t, dir, own)
	if wholeFolderRm(cmd, dir) {
		t.Errorf("an accepted list got the whole-folder command:\n%s", cmd)
	}
	if strings.Contains(cmd, "rm -rf") {
		t.Errorf("nothing is present to delete, but the command has rm -rf:\n%s", cmd)
	}
	if want := "sudo rmdir -- " + shellQuote(dir); !strings.HasSuffix(cmd, want) {
		t.Errorf("command does not end with the rmdir tail %q:\n%s", want, cmd)
	}
	for _, kept := range []string{"backups", "notes.txt"} {
		if strings.Contains(cmd, kept) {
			t.Errorf("command names %q, which must survive:\n%s", kept, cmd)
		}
	}
}

func TestRemovalCommand_AcceptedListNamingNothingButWithScratchRemovesOnlyTheScratch(t *testing.T) {
	dir := ownTree(t, manifestOf("docker-compose.yml"), "backups/a.tar", ".orbit-engine.lock")
	own := mustOwn(t, dir)
	cmd := mustRemovalCommand2(t, dir, own)
	if wholeFolderRm(cmd, dir) {
		t.Errorf("whole-folder command for an accepted list:\n%s", cmd)
	}
	if !strings.Contains(cmd, "sudo rm -rf -- .orbit-engine.lock") {
		t.Errorf("the present scratch file is not named:\n%s", cmd)
	}
	if strings.Contains(cmd, "backups") {
		t.Errorf("command names backups:\n%s", cmd)
	}
	if want := "sudo rmdir -- " + shellQuote(dir); !strings.HasSuffix(cmd, want) {
		t.Errorf("command does not end with %q:\n%s", want, cmd)
	}
}

// 2: a listing that fails is not a refused list.
func TestOwnFiles_AnUnreadableFolderIsNotARefusedList(t *testing.T) {
	requireNotRoot(t)
	for _, sub := range []string{"config", "scripts", "."} {
		t.Run(sub, func(t *testing.T) {
			dir := standardTree(t)
			target := filepath.Join(dir, sub)
			// Search but no read: Lstat of a child works, listing fails.
			if err := os.Chmod(target, 0o300); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(target, 0o755) })
			own := mustOwn(t, dir)
			if own.ManifestIgnored != nil {
				t.Errorf("ManifestIgnored = %v, want nil: a failed listing is not a refused list", own.ManifestIgnored)
			}
			if len(own.Leftovers) != 0 {
				t.Errorf("Leftovers = %q, want none when the listing failed", own.Leftovers)
			}
			if sub == "." {
				// The folder has search but no read bit: the list is still openable.
				if len(own.Names) == 0 {
					t.Fatalf("Names empty: the list's present names must still be used")
				}
			} else if !containsAll(own.Names, "docker-compose.yml") {
				t.Errorf("Names = %q, want the list's present names", own.Names)
			}
			cmd := mustRemovalCommand2(t, dir, own)
			if wholeFolderRm(cmd, dir) {
				t.Errorf("an unreadable folder got the whole-folder command:\n%s", cmd)
			}
			if !strings.Contains(cmd, "sudo rm -rf -- docker-compose.yml") {
				t.Errorf("command lost the list's names:\n%s", cmd)
			}
		})
	}
}

func containsAll(in []string, want ...string) bool {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// 3: no .orbit-files means the zero Own and today's command, whatever
// the folder itself looks like.
func TestOwnFiles_NoListInASymlinkedOrWorldWritableFolderIsStillTheZeroOwn(t *testing.T) {
	t.Run("symlinked folder", func(t *testing.T) {
		real := ownTree(t, "", "docker-compose.yml", ".env-orbit", "notes.txt")
		link := filepath.Join(t.TempDir(), "orbit-link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		assertTodaysCommand(t, link)
	})
	t.Run("world-writable folder", func(t *testing.T) {
		dir := ownTree(t, "", "docker-compose.yml", ".env-orbit", "notes.txt")
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		assertTodaysCommand(t, dir)
	})
}

func assertTodaysCommand(t *testing.T, dir string) {
	t.Helper()
	own, err := OwnFiles(dir)
	if err != nil {
		t.Fatalf("OwnFiles: %v", err)
	}
	if len(own.Names) != 0 || len(own.Leftovers) != 0 || own.ManifestIgnored != nil {
		t.Errorf("OwnFiles = %+v, want the zero Own (ManifestIgnored nil)", own)
	}
	want := "docker compose --project-directory " + shellQuote(dir) +
		" --env-file " + shellQuote(filepath.Join(dir, ".env-orbit")) +
		" down -v && sudo rm -rf " + shellQuote(dir)
	if got := mustRemovalCommand2(t, dir, own); got != want {
		t.Errorf("command\n got: %s\nwant: %s", got, want)
	}
}

// 4: scratch names fixed by contract, matched against the real listing.
var ownScratchPresent = []string{
	".orbit-engine.lock",
	".orbit-engine.lock.stale-1699999999",
	".orbit-install-staging.abc123/",
	".orbit-repair-recovery.7f/",
	".env-orbit.installing.99",
	".env-orbit.updating.99",
	".env-orbit.migrating.99",
	".env-orbit.tmp-xyz",
}

var ownScratchLookalikes = []string{
	".orbit-engine.lockx",
	"x.orbit-install-staging.1",
	".orbit-repair-recovery",
	".env-orbit.tmp",
	".env-orbit.installed.1",
}

func TestOwnFiles_ScratchNamesAreNamedLiterallyAndNeverLeftovers(t *testing.T) {
	dir := standardTree(t, append(append([]string{}, ownScratchPresent...), ownScratchLookalikes...)...)
	own := mustOwn(t, dir)
	if own.ManifestIgnored != nil {
		t.Fatalf("ManifestIgnored = %v", own.ManifestIgnored)
	}
	for _, s := range trimSlashes(ownScratchPresent) {
		if !containsAll(own.Names, s) {
			t.Errorf("Names lacks scratch entry %q: %q", s, own.Names)
		}
		if containsAll(own.Leftovers, s) {
			t.Errorf("scratch entry %q is shown as a leftover", s)
		}
	}
	for _, s := range ownScratchLookalikes {
		if containsAll(own.Names, s) {
			t.Errorf("look-alike %q is named", s)
		}
		if !containsAll(own.Leftovers, s) {
			t.Errorf("look-alike %q is not a leftover: %q", s, own.Leftovers)
		}
	}
	// Manifest names first, in manifest order, then the rest.
	want := trimSlashes(ownStandard)
	if len(own.Names) < len(want) {
		t.Fatalf("Names = %q", own.Names)
	}
	for i, w := range want {
		if own.Names[i] != w {
			t.Errorf("Names[%d] = %q, want %q (manifest order first)", i, own.Names[i], w)
		}
	}
	cmd := mustRemovalCommand2(t, dir, own)
	for _, s := range trimSlashes(ownScratchPresent) {
		if !strings.Contains(cmd, " "+shellQuote(s)) {
			t.Errorf("command does not name %q:\n%s", s, cmd)
		}
	}
	if strings.ContainsAny(cmd, "*?[") {
		t.Errorf("command has a glob:\n%s", cmd)
	}
	words := strings.Fields(cmd)
	for _, s := range ownScratchLookalikes {
		for _, w := range words {
			if w == shellQuote(s) {
				t.Errorf("command names look-alike %q:\n%s", s, cmd)
			}
		}
	}
}

func TestOwnFiles_ScratchNamesAreOnlyAbsentWhenAbsent(t *testing.T) {
	dir := standardTree(t)
	own := mustOwn(t, dir)
	if !reflectEqual(own.Names, trimSlashes(ownStandard)) {
		t.Errorf("Names = %q, want only the list's names (no scratch present)", own.Names)
	}
}

func reflectEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 5: a folder with a great many entries still gives a correct command
// without hanging, and none of the extras is named.
func TestOwnFiles_AHugeFolderStillGivesACorrectCommand(t *testing.T) {
	dir := standardTree(t)
	for i := 0; i < 10000; i++ {
		p := filepath.Join(dir, "extra-"+strings.Repeat("0", 5-len(itoa(i)))+itoa(i))
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	own := mustOwn(t, dir)
	cmd := mustRemovalCommand2(t, dir, own)
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s", d)
	}
	if own.ManifestIgnored != nil {
		t.Errorf("ManifestIgnored = %v", own.ManifestIgnored)
	}
	if !reflectEqual(own.Names, trimSlashes(ownStandard)) {
		t.Errorf("Names = %d entries, want the list's %d", len(own.Names), len(ownStandard))
	}
	if strings.Contains(cmd, "extra-") || wholeFolderRm(cmd, dir) {
		t.Errorf("command names extras or is the whole-folder one (len %d)", len(cmd))
	}
	if !strings.Contains(cmd, "sudo rm -rf -- docker-compose.yml") ||
		!strings.HasSuffix(cmd, "sudo rmdir -- "+shellQuote(dir)) {
		t.Errorf("command is not the file-by-file one")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
