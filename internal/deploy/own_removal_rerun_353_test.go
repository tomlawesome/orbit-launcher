package deploy

// #353: every subfolder rmdir in the Remove command is skipped when that
// folder is already gone, so the command can be pasted again after the
// leftovers that stopped it have been deleted.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardedRmdir353 is one subfolder step, as the issue writes it.
func guardedRmdir353(d string) string {
	return "{ test ! -d " + d + " || sudo rmdir -- " + d + "; }"
}

func sudoPath353(t *testing.T) string {
	t.Helper()
	return pathWith(t, map[string]string{
		"docker": "exit 0\n",
		"sudo":   `exec "$@"` + "\n",
	})
}

func TestRemovalCommand353_EachSubfolderRmdirIsGuardedByTestNotDir(t *testing.T) {
	dir := standardTree(t)
	got := mustRemovalCommand2(t, dir, mustOwn(t, dir))
	q := shellQuote(dir)
	want := " && " + guardedRmdir353("config") + " && " + guardedRmdir353("scripts") + ") && sudo rmdir -- " + q
	if !strings.HasSuffix(got, want) {
		t.Errorf("tail\n got: %s\nwant suffix: %s", got, want)
	}
	if strings.Contains(got, "rmdir -- config scripts") {
		t.Errorf("old unguarded multi-folder rmdir is still there:\n%s", got)
	}
}

func TestRemovalCommand353_HomeFolderEndsAfterTheGuardedSubfolderSteps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ownTreeAt(t, home, manifestOf(ownStandard...), ownStandard...)
	cmd, err := RemovalCommand(home, mustOwn(t, home))
	if err != nil {
		t.Fatal(err)
	}
	if want := guardedRmdir353("scripts") + ")"; !strings.HasSuffix(cmd, want) {
		t.Errorf("home command should end %q:\n%s", want, cmd)
	}
}

func TestRemovalCommand353_NestedSubfoldersAreEachGuardedDeepestFirst(t *testing.T) {
	dir := ownTree(t, manifestOf("docker-compose.yml", "config/deep/er/x.json", ".orbit-files"),
		"docker-compose.yml", "config/deep/er/x.json", ".orbit-files")
	got := mustRemovalCommand2(t, dir, mustOwn(t, dir))
	want := " && " + guardedRmdir353("config/deep/er") + " && " + guardedRmdir353("config/deep") +
		" && " + guardedRmdir353("config") + ") && sudo rmdir -- " + shellQuote(dir)
	if !strings.HasSuffix(got, want) {
		t.Errorf("tail\n got: %s\nwant suffix: %s", got, want)
	}
}

func TestRemovalCommand353_PastingAgainAfterLeftoversAreDeletedRemovesTheFolder(t *testing.T) {
	path := sudoPath353(t)
	dir := standardTree(t, "backups/")
	cmd := mustRemovalCommand2(t, dir, mustOwn(t, dir))

	if err := runRemoval351(t, cmd, path); err == nil {
		t.Fatalf("first run exited 0 with backups/ still in the folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); err != nil {
		t.Fatalf("first run touched backups/: %v", err)
	}
	for _, gone := range []string{"config", "scripts"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Fatalf("first run left %s", gone)
		}
	}

	if err := os.RemoveAll(filepath.Join(dir, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := runRemoval351(t, cmd, path); err != nil {
		t.Errorf("second run failed: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("second run left %s behind", dir)
	}
}

func TestRemovalCommand353_NonEmptySubfolderStillStopsTheChainAndKeepsTheFolder(t *testing.T) {
	path := sudoPath353(t)
	dir := standardTree(t, "config/mine.json")
	cmd := mustRemovalCommand2(t, dir, mustOwn(t, dir))

	if err := runRemoval351(t, cmd, path); err == nil {
		t.Errorf("exit 0 although config/ still holds a file")
	}
	if _, err := os.Stat(filepath.Join(dir, "config", "mine.json")); err != nil {
		t.Errorf("the operator's file was touched: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("install folder removed despite a non-empty subfolder: %v", err)
	}
}
