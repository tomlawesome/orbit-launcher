package deploy

import (
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// #265: ForeignOwned reads, never changes, who owns the files Repair
// can't check. The contract (issue #265, note 41313):
//
//   - ForeignOwned(dir) returns Ownership{Account string; Files
//     []ForeignFile}, ForeignFile{Path, Owner string}.
//   - Account is the name of the account owning the deployment folder
//     (os/user lookup; "uid N" when unknown).
//   - Files lists .env-orbit and every entry under .orbit-secrets/
//     (walked with Lstat, symlinks not followed) whose owner differs from
//     the folder's; paths relative to the folder, sorted, each with its
//     owner's name (or "uid N"). A missing file is not foreign.
//
// The tests run unprivileged, where a file owned by another account
// cannot be made, and no test file shows an injectable owner lookup. So
// the foreign cases run only as root (they skip with a reason
// otherwise); everything else is checked as one user.

func fowUID(t *testing.T, path string) uint32 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Uid
}

// fowName is the name an owner is shown by: the account's login name,
// or "uid N" when no account has that uid.
func fowName(uid uint32) string {
	n := strconv.FormatUint(uint64(uid), 10)
	u, err := user.LookupId(n)
	if err != nil {
		return "uid " + n
	}
	return u.Username
}

func fowWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fowOwnDeployment is a folder whose .env-orbit and secrets all belong
// to the current account (the folder's owner).
func fowOwnDeployment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fowWrite(t, filepath.Join(dir, ".env-orbit"), "APP_URL=http://localhost\n")
	fowWrite(t, filepath.Join(dir, ".orbit-secrets", "db_password"), "x\n")
	fowWrite(t, filepath.Join(dir, ".orbit-secrets", "sub", "api_key"), "y\n")
	return dir
}

func TestForeignOwned_NothingForeignGivesNoFiles(t *testing.T) {
	own, err := ForeignOwned(fowOwnDeployment(t))
	if err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	if len(own.Files) != 0 {
		t.Errorf("Files = %+v, want none: every file belongs to the folder's owner", own.Files)
	}
}

func TestForeignOwned_AccountIsTheFolderOwnersName(t *testing.T) {
	dir := fowOwnDeployment(t)
	own, err := ForeignOwned(dir)
	if err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	if want := fowName(fowUID(t, dir)); own.Account != want {
		t.Errorf("Account = %q, want %q (the folder owner's name)", own.Account, want)
	}
	if cur, err := user.Current(); err == nil && own.Account != cur.Username {
		t.Errorf("Account = %q, but this test made the folder as %q", own.Account, cur.Username)
	}
}

func TestForeignOwned_MissingFilesAreNotForeign(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"empty folder": func(t *testing.T, dir string) {},
		"only .env-orbit": func(t *testing.T, dir string) {
			fowWrite(t, filepath.Join(dir, ".env-orbit"), "A=b\n")
		},
		"only .orbit-secrets": func(t *testing.T, dir string) {
			fowWrite(t, filepath.Join(dir, ".orbit-secrets", "s"), "x\n")
		},
		"empty .orbit-secrets": func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, ".orbit-secrets"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			build(t, dir)
			own, err := ForeignOwned(dir)
			if err != nil {
				t.Fatalf("a missing file must not be an error: %v", err)
			}
			if len(own.Files) != 0 {
				t.Errorf("Files = %+v, want none", own.Files)
			}
			if own.Account == "" {
				t.Error("Account is empty; it names the folder's owner whatever is missing")
			}
		})
	}
}

// A symlink is looked at as itself (Lstat), never through: a link to a
// file or folder owned by another account is the link's owner's entry,
// and nothing behind it is walked.
func TestForeignOwned_SymlinksAreNotFollowed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the folder and /etc are both root-owned, so following a link would change nothing visible")
	}
	if fowUID(t, "/etc/passwd") == uint32(os.Getuid()) {
		t.Skip("/etc/passwd is owned by the current account here; it can't stand for a foreign file")
	}
	t.Run("links inside the secrets folder", func(t *testing.T) {
		dir := fowOwnDeployment(t)
		secrets := filepath.Join(dir, ".orbit-secrets")
		if err := os.Symlink("/etc/passwd", filepath.Join(secrets, "filelink")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc", filepath.Join(secrets, "dirlink")); err != nil {
			t.Fatal(err)
		}
		own, err := ForeignOwned(dir)
		if err != nil {
			t.Fatalf("ForeignOwned: %v", err)
		}
		if len(own.Files) != 0 {
			t.Errorf("Files = %+v: a link's target was followed", own.Files)
		}
	})
	t.Run(".env-orbit is a link", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink("/etc/passwd", filepath.Join(dir, ".env-orbit")); err != nil {
			t.Fatal(err)
		}
		own, err := ForeignOwned(dir)
		if err != nil {
			t.Fatalf("ForeignOwned: %v", err)
		}
		if len(own.Files) != 0 {
			t.Errorf("Files = %+v: .env-orbit's target was followed", own.Files)
		}
	})
	t.Run(".orbit-secrets is a link", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink("/etc", filepath.Join(dir, ".orbit-secrets")); err != nil {
			t.Fatal(err)
		}
		own, err := ForeignOwned(dir)
		if err != nil {
			t.Fatalf("ForeignOwned: %v", err)
		}
		if len(own.Files) != 0 {
			t.Errorf("Files = %+v: the secrets link was walked into", own.Files)
		}
	})
}

type fowEntry struct {
	mode  os.FileMode
	size  int64
	mtime int64
	uid   uint32
}

func fowSnapshot(t *testing.T, dir string) map[string]fowEntry {
	t.Helper()
	out := map[string]fowEntry{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out[p] = fowEntry{info.Mode(), info.Size(), info.ModTime().UnixNano(), info.Sys().(*syscall.Stat_t).Uid}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestForeignOwned_ReadsOnly(t *testing.T) {
	dir := fowOwnDeployment(t)
	before := fowSnapshot(t, dir)
	if _, err := ForeignOwned(dir); err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	if after := fowSnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("the folder changed under a read-only check:\nbefore %v\nafter  %v", before, after)
	}
}

// fowRoot skips unless the test can give files to other accounts.
func fowRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root: a file owned by another account can't be created unprivileged, and ForeignOwned has no injectable owner lookup")
	}
}

func fowChown(t *testing.T, path string, uid int) {
	t.Helper()
	if err := os.Lchown(path, uid, -1); err != nil {
		t.Skipf("cannot give %s to uid %d here: %v", path, uid, err)
	}
}

// Foreign files are listed relative to the folder, sorted, each with the
// owner's name; files the folder's owner holds are not.
func TestForeignOwned_ListsForeignFilesRelativeAndSorted(t *testing.T) {
	fowRoot(t)
	dir := t.TempDir() // owned by root
	root := filepath.Join(dir, ".orbit-secrets")
	for _, f := range []string{
		".env-orbit",
		".orbit-secrets/zeta",
		".orbit-secrets/alpha",
		".orbit-secrets/own_file",
		".orbit-secrets/sub/deep",
		".orbit-secrets/sub/own_deep",
	} {
		fowWrite(t, filepath.Join(dir, f), "x\n")
	}
	fowChown(t, filepath.Join(dir, ".env-orbit"), 54321)
	fowChown(t, filepath.Join(root, "zeta"), 54322)
	fowChown(t, filepath.Join(root, "alpha"), 54323)
	fowChown(t, filepath.Join(root, "sub"), 54324) // an entry under the secrets folder: listed too
	fowChown(t, filepath.Join(root, "sub", "deep"), 54325)

	own, err := ForeignOwned(dir)
	if err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	want := []ForeignFile{
		{".env-orbit", fowName(54321)},
		{".orbit-secrets/alpha", fowName(54323)},
		{".orbit-secrets/sub", fowName(54324)},
		{".orbit-secrets/sub/deep", fowName(54325)},
		{".orbit-secrets/zeta", fowName(54322)},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	if !reflect.DeepEqual(own.Files, want) {
		t.Errorf("Files =\n  %+v\nwant\n  %+v", own.Files, want)
	}
	for _, f := range own.Files {
		if filepath.IsAbs(f.Path) || strings.HasPrefix(f.Path, dir) {
			t.Errorf("path %q is not relative to the folder", f.Path)
		}
	}
	if want := fowName(0); own.Account != want {
		t.Errorf("Account = %q, want %q (the folder's owner)", own.Account, want)
	}
}

// An owner with no account is the bare number for the folder (so the
// chown command can use it as is) and "uid N" for the files (display).
func TestForeignOwned_UnknownAccountIsTheBareUIDForChownAndUidNForDisplay(t *testing.T) {
	fowRoot(t)
	const folderUID, fileUID = 54399, 54398
	if _, err := user.LookupId(strconv.Itoa(folderUID)); err == nil {
		t.Skipf("uid %d has an account here", folderUID)
	}
	if _, err := user.LookupId(strconv.Itoa(fileUID)); err == nil {
		t.Skipf("uid %d has an account here", fileUID)
	}
	dir := t.TempDir()
	fowWrite(t, filepath.Join(dir, ".env-orbit"), "A=b\n")
	fowChown(t, dir, folderUID)
	fowChown(t, filepath.Join(dir, ".env-orbit"), fileUID)

	own, err := ForeignOwned(dir)
	if err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	if own.Account != "54399" {
		t.Errorf("Account = %q, want %q", own.Account, "54399")
	}
	want := []ForeignFile{{".env-orbit", "uid 54398"}}
	if !reflect.DeepEqual(own.Files, want) {
		t.Errorf("Files = %+v, want %+v", own.Files, want)
	}
}

// A link owned by another account is reported as itself, and what it
// points at is not walked.
func TestForeignOwned_ForeignSymlinkIsListedNotFollowed(t *testing.T) {
	fowRoot(t)
	dir := t.TempDir()
	secrets := filepath.Join(dir, ".orbit-secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	fowWrite(t, filepath.Join(target, "inside"), "x\n")
	fowChown(t, filepath.Join(target, "inside"), 54330)
	if err := os.Symlink(target, filepath.Join(secrets, "link")); err != nil {
		t.Fatal(err)
	}
	fowChown(t, filepath.Join(secrets, "link"), 54331)

	own, err := ForeignOwned(dir)
	if err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	want := []ForeignFile{{".orbit-secrets/link", fowName(54331)}}
	if !reflect.DeepEqual(own.Files, want) {
		t.Errorf("Files = %+v, want only the link itself: %+v", own.Files, want)
	}
}

// The chown command names the owner as one bare word, whether it is an
// account name or a number, and never carries the display text.
func TestChownCommandWords_NumericOwnerIsOneBareWord(t *testing.T) {
	files := []ForeignFile{{".env-orbit", "uid 54398"}}
	cases := []struct {
		account string
		want    []string
	}{
		{"54399", []string{"sudo", "chown", "-R", "54399:", "/opt/orbit/.env-orbit"}},
		{"alice", []string{"sudo", "chown", "-R", "alice:", "/opt/orbit/.env-orbit"}},
	}
	for _, c := range cases {
		got := ChownCommandWords("/opt/orbit", Ownership{Account: c.account, Files: files})
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("account %q: words = %q, want %q", c.account, got, c.want)
		}
		for _, w := range got {
			if strings.Contains(w, " ") || strings.Contains(w, "uid") {
				t.Errorf("account %q: word %q has a space or the text \"uid\"", c.account, w)
			}
		}
	}
}

// The folder's owner, as ForeignOwned gives it, is never the display
// form, so it can go straight into a chown command.
func TestForeignOwned_AccountIsNeverTheDisplayForm(t *testing.T) {
	dir := t.TempDir()
	fowWrite(t, filepath.Join(dir, ".env-orbit"), "A=b\n")
	own, err := ForeignOwned(dir)
	if err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	if own.Account == "" || strings.Contains(own.Account, " ") || strings.HasPrefix(own.Account, "uid ") {
		t.Errorf("Account = %q, want a name or bare number with no space", own.Account)
	}
}
