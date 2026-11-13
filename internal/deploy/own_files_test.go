package deploy

// #285: Remove deletes only Orbit's own files. The launcher reads the
// manifest Orbit writes into a deployment, .orbit-files, and keeps no
// list of its own (owner, 2026-10-22, note 41988): a folder without the
// manifest gets the whole-folder command, unchanged. The launcher never
// deletes anything itself; these tests only build and read.
//
// New names these tests expect (from the design, issue285.md):
//
//	type Own struct {
//		Names           []string // present Orbit files, relative, manifest order
//		Leftovers       []string // entries in the folder that are not Orbit's
//		ManifestIgnored error    // set when a manifest was there and refused
//	}
//	func OwnFiles(targetDir string) (Own, error)
//	func RemovalCommandWords(targetDir string, own Own) ([]string, error)
//	func RemovalCommand(targetDir string, own Own) (string, error)
//
// The zero Own means "no manifest": the whole-folder command.

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const ownManifestHeader = "# Files Orbit installed in this folder. orbit-launcher's Remove reads this list; do not edit.\n"

// ownStandard is today's Orbit, in the order the launcher is expected to
// name the files.
var ownStandard = []string{
	"docker-compose.yml",
	"docker-compose.mail.yml",
	"docker-compose.kek-rotation.yml",
	".env-orbit.example",
	"config/tika-config.json",
	"scripts/backup.sh",
	"scripts/configure.sh",
	"scripts/end-maintenance.sh",
	"scripts/export-recovery-bundle.sh",
	"scripts/import-recovery-bundle.sh",
	"scripts/installer-ui.sh",
	"scripts/repair.sh",
	"scripts/restore.sh",
	".env-orbit",
	".orbit-secrets/",
	".orbit-files",
}

// ownTree writes manifest (as given) and creates every path in files
// (a trailing slash makes a directory holding one file) in a fresh folder.
func ownTree(t *testing.T, manifest string, files ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "orbit")
	ownTreeAt(t, dir, manifest, files...)
	return dir
}

func ownTreeAt(t *testing.T, dir, manifest string, files ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if manifest != "" && f == ".orbit-files" {
			continue // written below, last, so the list is not overwritten
		}
		p := filepath.Join(dir, f)
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			p = filepath.Join(p, "inside")
		} else if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ".orbit-files"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func manifestOf(names ...string) string {
	return ownManifestHeader + strings.Join(names, "\n") + "\n"
}

// standardTree is today's Orbit installed, plus whatever else is passed.
func standardTree(t *testing.T, extra ...string) string {
	t.Helper()
	files := append([]string{}, ownStandard...)
	files = append(files, extra...)
	return ownTree(t, manifestOf(ownStandard...), files...)
}

func mustOwn(t *testing.T, dir string) Own {
	t.Helper()
	own, err := OwnFiles(dir)
	if err != nil {
		t.Fatalf("OwnFiles(%q): %v", dir, err)
	}
	return own
}

func trimSlashes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.TrimSuffix(s, "/"))
	}
	return out
}

func sorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func mustRemovalCommand2(t *testing.T, dir string, own Own) string {
	t.Helper()
	cmd, err := RemovalCommand(dir, own)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestOwnFiles_TodaysOrbitNamesEveryPresentManifestEntryInManifestOrder(t *testing.T) {
	dir := standardTree(t)
	own := mustOwn(t, dir)
	if own.ManifestIgnored != nil {
		t.Fatalf("ManifestIgnored = %v, want nil", own.ManifestIgnored)
	}
	if got, want := trimSlashes(own.Names), trimSlashes(ownStandard); !reflect.DeepEqual(got, want) {
		t.Errorf("Names =\n%q\nwant\n%q", got, want)
	}
	if len(own.Leftovers) != 0 {
		t.Errorf("Leftovers = %q, want none", own.Leftovers)
	}
}

func TestOwnFiles_ANameTheManifestListsButTheFolderLacksIsNotNamed(t *testing.T) {
	// A future Orbit's scripts/new.sh is named when it exists.
	dir := ownTree(t, manifestOf("docker-compose.yml", "scripts/new.sh", "scripts/gone.sh", ".orbit-files"),
		"docker-compose.yml", "scripts/new.sh")
	own := mustOwn(t, dir)
	want := []string{"docker-compose.yml", "scripts/new.sh", ".orbit-files"}
	if got := trimSlashes(own.Names); !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %q, want %q (scripts/gone.sh is not there, so not named)", got, want)
	}
}

func TestOwnFiles_NoManifestIsTheZeroOwnAndNoError(t *testing.T) {
	dir := ownTree(t, "", "docker-compose.yml", ".env-orbit", "notes.txt")
	own, err := OwnFiles(dir)
	if err != nil {
		t.Fatalf("OwnFiles without a manifest: %v, want no error", err)
	}
	if len(own.Names) != 0 || len(own.Leftovers) != 0 || own.ManifestIgnored != nil {
		t.Errorf("OwnFiles without a manifest = %+v, want the zero Own (no built-in list, note 41988)", own)
	}
	// The zero Own is today's command, byte for byte.
	got, err := RemovalCommand(dir, own)
	if err != nil {
		t.Fatal(err)
	}
	want := "docker compose --project-directory " + shellQuote(dir) +
		" --env-file " + shellQuote(filepath.Join(dir, ".env-orbit")) +
		" down -v && sudo rm -rf " + shellQuote(dir)
	if got != want {
		t.Errorf("command for a folder without .orbit-files\n got: %s\nwant: %s", got, want)
	}
}

func TestOwnFiles_LeftoversAreEverythingThatIsNotOrbits(t *testing.T) {
	dir := standardTree(t,
		"backups/a.tar", "orbit-install-backups/rescued", "notes.txt",
		"docker-compose.override.yml", "scripts/mine.sh")
	own := mustOwn(t, dir)
	want := []string{"backups", "docker-compose.override.yml", "notes.txt", "orbit-install-backups", "scripts/mine.sh"}
	if got := sorted(trimSlashes(own.Leftovers)); !reflect.DeepEqual(got, want) {
		t.Errorf("Leftovers = %q, want %q", got, want)
	}
	for _, n := range own.Names {
		for _, l := range want {
			if strings.TrimSuffix(n, "/") == l {
				t.Errorf("%q is both a name to delete and a leftover", n)
			}
		}
	}
}

// A hostile or damaged list is refused whole, and with no built-in list to
// fall back on the folder gets today's whole-folder command.
func TestOwnFiles_ARefusedManifestIsIgnoredWhole(t *testing.T) {
	long := ownManifestHeader
	for i := 0; i < 200; i++ {
		long += "file" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "\n"
	}
	cases := []struct {
		name     string
		manifest string
	}{
		{"absolute path", manifestOf("docker-compose.yml", "/etc/passwd")},
		{"dot-dot component", manifestOf("docker-compose.yml", "../x")},
		{"dot-dot inside", manifestOf("docker-compose.yml", "config/../../x")},
		{"backups", manifestOf("docker-compose.yml", "backups/")},
		{"under backups", manifestOf("docker-compose.yml", "backups/a.tar")},
		{"orbit-install-backups", manifestOf("docker-compose.yml", "orbit-install-backups/")},
		{"over 8 KiB", manifestOf("docker-compose.yml") + "# " + strings.Repeat("x", 9*1024) + "\n"},
		{"over 128 lines", long},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := ownTree(t, c.manifest, "docker-compose.yml", "notes.txt")
			own := mustOwn(t, dir)
			if own.ManifestIgnored == nil {
				t.Errorf("ManifestIgnored is nil, want the refusal")
			}
			if len(own.Names) != 0 {
				t.Errorf("Names = %q from a refused list, want none", own.Names)
			}
			got := mustRemovalCommand2(t, dir, own)
			if want := mustRemovalCommand(t, dir); got != want {
				t.Errorf("command after a refused list\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestOwnFiles_AManifestThatIsASymlinkOrWorldWritableIsIgnoredWhole(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		dir := ownTree(t, "", "docker-compose.yml")
		real := filepath.Join(t.TempDir(), "real")
		if err := os.WriteFile(real, []byte(manifestOf("docker-compose.yml")), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(dir, ".orbit-files")); err != nil {
			t.Fatal(err)
		}
		own := mustOwn(t, dir)
		if own.ManifestIgnored == nil || len(own.Names) != 0 {
			t.Errorf("symlinked manifest: %+v, want it ignored whole", own)
		}
	})
	t.Run("world-writable", func(t *testing.T) {
		dir := ownTree(t, manifestOf("docker-compose.yml", ".orbit-files"), "docker-compose.yml")
		if err := os.Chmod(filepath.Join(dir, ".orbit-files"), 0o666); err != nil {
			t.Fatal(err)
		}
		own := mustOwn(t, dir)
		if own.ManifestIgnored == nil || len(own.Names) != 0 {
			t.Errorf("world-writable manifest: %+v, want it ignored whole", own)
		}
	})
}

// A listed path whose folder is a symlink must never be named: rm would
// follow it out of the deployment.
func TestOwnFiles_ANameUnderASymlinkedFolderIsNotNamed(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "tika-config.json"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := ownTree(t, manifestOf("docker-compose.yml", "config/tika-config.json", ".orbit-files"), "docker-compose.yml")
	if err := os.Symlink(outside, filepath.Join(dir, "config")); err != nil {
		t.Fatal(err)
	}
	own := mustOwn(t, dir)
	for _, n := range own.Names {
		if strings.HasPrefix(n, "config") {
			t.Errorf("Names holds %q, which is reached through a symlink out of the folder", n)
		}
	}
	if cmd, err := RemovalCommand(dir, own); err == nil && strings.Contains(cmd, "config/tika-config.json") {
		t.Errorf("the command names a file behind a symlink: %s", cmd)
	}
	if _, err := os.Stat(filepath.Join(outside, "tika-config.json")); err != nil {
		t.Errorf("the file outside the folder was touched: %v", err)
	}
}

// Whatever the manifest says, a name reaches the command only as one word
// quoted the way shellQuote quotes it; a list too odd to quote is refused
// whole. Either is safe; an unquoted metacharacter is not.
func TestRemovalCommand_HostileNamesAreQuotedOrRefused(t *testing.T) {
	for _, name := range []string{
		"a b.sh", "semi;rm -rf ~", "$(touch pwned)", "it's.sh", "back`tick`", "glob*", "new\nline",
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(name, "\n") {
				// One path per line: the list reads "new" and "line", neither
				// present, so neither is named and no raw newline reaches the command.
				dir := ownTree(t, manifestOf("docker-compose.yml", name, ".orbit-files"), "docker-compose.yml")
				own := mustOwn(t, dir)
				cmd := mustRemovalCommand2(t, dir, own)
				for _, w := range strings.Fields(cmd) {
					if w == "new" || w == "line" || w == shellQuote("new") || w == shellQuote("line") {
						t.Errorf("absent name %q is in the command:\n%s", w, cmd)
					}
				}
				if strings.Contains(cmd, "\n") {
					t.Errorf("a raw newline reached the command: %q", cmd)
				}
				return
			}
			dir := ownTree(t, manifestOf("docker-compose.yml", name, ".orbit-files"), "docker-compose.yml", name)
			own := mustOwn(t, dir)
			cmd := mustRemovalCommand2(t, dir, own)
			if own.ManifestIgnored != nil {
				if len(own.Names) != 0 {
					t.Errorf("refused list still names %q", own.Names)
				}
				return
			}
			if !strings.Contains(cmd, " "+shellQuote(name)+" ") && !strings.HasSuffix(cmd, " "+shellQuote(name)) {
				t.Errorf("%q is in the command but not as one quoted word %s:\n%s", name, shellQuote(name), cmd)
			}
			if strings.Contains(cmd, "\n") {
				t.Errorf("a raw newline reached the command: %q", cmd)
			}
		})
	}
}

func TestRemovalCommand_EmptyListLinesNameNothing(t *testing.T) {
	dir := ownTree(t, ownManifestHeader+"docker-compose.yml\n\n\nconfig/tika-config.json\n.orbit-files\n",
		"docker-compose.yml", "config/tika-config.json")
	own := mustOwn(t, dir)
	for _, n := range own.Names {
		if strings.Trim(n, "/.") == "" {
			t.Errorf("Names holds %q, made from an empty line", n)
		}
	}
	words, err := RemovalCommandWords(dir, own)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range words {
		if w == "''" || w == "." || w == "./" {
			t.Errorf("word %q came from an empty line: %q", w, words)
		}
	}
}

// The command, exactly as the design shows it.
func TestRemovalCommand_TodaysOrbitIsFileByFileThenRmdir(t *testing.T) {
	dir := standardTree(t)
	got := mustRemovalCommand2(t, dir, mustOwn(t, dir))
	q := shellQuote(dir)
	want := "(cd " + q + " && docker compose --env-file .env-orbit down -v && sudo rm -rf -- " +
		"docker-compose.yml docker-compose.mail.yml docker-compose.kek-rotation.yml .env-orbit.example " +
		"config/tika-config.json scripts/backup.sh scripts/configure.sh scripts/end-maintenance.sh " +
		"scripts/export-recovery-bundle.sh scripts/import-recovery-bundle.sh scripts/installer-ui.sh " +
		"scripts/repair.sh scripts/restore.sh .env-orbit .orbit-secrets .orbit-files " +
		"&& sudo rmdir -- config scripts) && sudo rmdir -- " + q
	if got != want {
		t.Errorf("command\n got: %s\nwant: %s", got, want)
	}
}

func TestRemovalCommand_NeverNamesBackupsOrLeftovers_AndStillEndsInRmdir(t *testing.T) {
	dir := standardTree(t, "backups/a.tar", "orbit-install-backups/x", "notes.txt", "scripts/mine.sh")
	cmd := mustRemovalCommand2(t, dir, mustOwn(t, dir))
	for _, bad := range []string{"backups", "notes.txt", "mine.sh", "rm -rf " + shellQuote(dir)} {
		if strings.Contains(cmd, bad) {
			t.Errorf("command names %q, which is not Orbit's:\n%s", bad, cmd)
		}
	}
	if !strings.HasSuffix(cmd, ") && sudo rmdir -- "+shellQuote(dir)) {
		t.Errorf("the folder is not removed by rmdir, which refuses a folder that is not empty:\n%s", cmd)
	}
}

func TestRemovalCommand_QuotesAFolderWithASpace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Drive", "orbit")
	ownTreeAt(t, dir, manifestOf(ownStandard...), ownStandard...)
	cmd := mustRemovalCommand2(t, dir, mustOwn(t, dir))
	q := shellQuote(dir)
	if !strings.HasPrefix(cmd, "(cd "+q+" && ") || !strings.HasSuffix(cmd, " && sudo rmdir -- "+q) {
		t.Errorf("the folder is not one quoted word at both ends:\n%s", cmd)
	}
}

func TestRemovalCommand_IsItsWordsJoined_ForAManifestFolder(t *testing.T) {
	dir := standardTree(t)
	own := mustOwn(t, dir)
	words, err := RemovalCommandWords(dir, own)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := mustRemovalCommand2(t, dir, own), strings.Join(words, " "); got != want {
		t.Errorf("command is not its words joined:\n%s\n%s", got, want)
	}
	for _, w := range words {
		if strings.ContainsAny(w, "*?") {
			t.Errorf("word %q is a glob; the command names actual entries", w)
		}
	}
}

// 21a: a home or root folder gets the same file-by-file command; the
// folder itself is never removed.
func TestRemovalCommand_HomeFolderGetsFileByFileAndNoFolderRmdir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ownTreeAt(t, home, manifestOf(ownStandard...), ownStandard...)
	cmd, err := RemovalCommand(home, mustOwn(t, home))
	if err != nil {
		t.Fatalf("RemovalCommand for the home folder with a manifest: %v, want the file-by-file command", err)
	}
	if !strings.Contains(cmd, "sudo rm -rf -- docker-compose.yml") {
		t.Errorf("not file by file:\n%s", cmd)
	}
	if !strings.HasSuffix(cmd, "sudo rmdir -- config scripts)") {
		t.Errorf("a home folder's command must end after the config and scripts rmdir, with no folder rmdir:\n%s", cmd)
	}
	if strings.Contains(cmd, "rmdir -- "+shellQuote(home)) {
		t.Errorf("the home folder itself is offered for removal:\n%s", cmd)
	}
}

func TestRemovalCommand_RootFolderGetsFileByFileAndNoFolderRmdir(t *testing.T) {
	own := Own{Names: []string{"docker-compose.yml", ".env-orbit", ".orbit-files"}}
	cmd, err := RemovalCommand("/", own)
	if err != nil {
		t.Fatalf("RemovalCommand(\"/\", own): %v, want the file-by-file command", err)
	}
	if !strings.HasPrefix(cmd, "(cd / && ") || !strings.Contains(cmd, "rm -rf -- docker-compose.yml") {
		t.Errorf("not the file-by-file command from /:\n%s", cmd)
	}
	if strings.Contains(cmd, "rmdir -- /") || strings.Contains(cmd, "rm -rf /") {
		t.Errorf("the root folder is offered for removal:\n%s", cmd)
	}
}

// Home with a refused manifest has no list: still no whole-folder command.
func TestRemovalCommand_HomeFolderWithARefusedManifestStillRefusesTheWholeFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ownTreeAt(t, home, manifestOf("/etc/passwd"))
	if cmd, err := RemovalCommand(home, mustOwn(t, home)); err == nil {
		t.Errorf("RemovalCommand = %q, want ErrHomeFolder", cmd)
	}
}

// The launcher only builds and reads; it deletes nothing.
func TestOwnFiles_AndTheCommandDeleteNothing(t *testing.T) {
	dir := standardTree(t, "backups/a.tar", "notes.txt")
	before := listTree(t, dir)
	mustRemovalCommand2(t, dir, mustOwn(t, dir))
	if after := listTree(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("the folder changed:\nbefore %q\nafter  %q", before, after)
	}
}

func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// removal_property_test.go: the launcher must never start rmdir either.
func TestDestructiveArgs_RmdirIsOneToo(t *testing.T) {
	if !destructiveArgs("rmdir") {
		t.Error("destructiveArgs(\"rmdir\") = false; the launcher must never pass rmdir to a process")
	}
}
