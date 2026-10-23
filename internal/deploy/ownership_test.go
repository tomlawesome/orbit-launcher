package deploy

import (
	"errors"
	"os"
	"os/exec"
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

// fowRoot skips unless the test can give files to other accounts. Root
// is only that tool here: each test sets the launcher to the operator it
// means (the folder's owner), since the launcher never runs as root (#291).
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
	dir := t.TempDir()
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
	asOperatorOf(t, dir, operatorUID)
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
	// Account is the chown form: the name, or the bare uid if unnamed.
	account := strconv.Itoa(operatorUID)
	if u, err := user.LookupId(account); err == nil {
		account = u.Username
	}
	if own.Account != account {
		t.Errorf("Account = %q, want %q (the folder's owner)", own.Account, account)
	}
}

// An owner with no account is the bare number for the folder (so the
// chown command can use it as is) and "uid N" for the files (display).
func TestForeignOwned_UnknownAccountIsTheBareUIDForChownAndUidNForDisplay(t *testing.T) {
	fowRoot(t)
	const folderUID, fileUID, folderGID = 54399, 54398, 54397
	if _, err := user.LookupId(strconv.Itoa(folderUID)); err == nil {
		t.Skipf("uid %d has an account here", folderUID)
	}
	if _, err := user.LookupGroupId(strconv.Itoa(folderGID)); err == nil {
		t.Skipf("gid %d names a group here", folderGID)
	}
	if _, err := user.LookupId(strconv.Itoa(fileUID)); err == nil {
		t.Skipf("uid %d has an account here", fileUID)
	}
	dir := t.TempDir()
	fowWrite(t, filepath.Join(dir, ".env-orbit"), "A=b\n")
	if err := os.Lchown(dir, folderUID, folderGID); err != nil {
		t.Skipf("cannot give %s to uid %d gid %d here: %v", dir, folderUID, folderGID, err)
	}
	asLauncherUser(t, folderUID)
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
	if own.Group != "54397" {
		t.Errorf("Group = %q, want %q (the folder's gid)", own.Group, "54397")
	}
	if words := ChownCommandWords(dir, own); len(words) < 4 || words[3] != "54399:54397" {
		t.Errorf("words = %q, want the fourth word to be %q", words, "54399:54397")
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
	asOperatorOf(t, dir, operatorUID)
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

// The chown command's owner spec: an account with no name is the bare
// number and carries the folder's group (a trailing ":" would not be
// accepted by chown for a number); a named account keeps its login-group
// form; and the display text never appears.
func TestChownCommandWords_NumericOwnerCarriesTheFolderGroup(t *testing.T) {
	files := []ForeignFile{{".env-orbit", "uid 54398"}}
	cases := []struct {
		own  Ownership
		want []string
	}{
		{Ownership{Account: "54399", Group: "1000"}, []string{"sudo", "chown", "-R", "54399:1000", "/opt/orbit/.env-orbit"}},
		{Ownership{Account: "alice", Group: "1000"}, []string{"sudo", "chown", "-R", "alice:", "/opt/orbit/.env-orbit"}},
		{Ownership{Account: "54399"}, []string{"sudo", "chown", "-R", "54399", "/opt/orbit/.env-orbit"}},
	}
	for _, c := range cases {
		c.own.Files = files
		got := ChownCommandWords("/opt/orbit", c.own)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("account %q group %q: words = %q, want %q", c.own.Account, c.own.Group, got, c.want)
		}
		allDigits := c.own.Account != "" && strings.Trim(c.own.Account, "0123456789") == ""
		for _, w := range got {
			if strings.Contains(w, " ") || strings.Contains(w, "uid") {
				t.Errorf("account %q group %q: word %q has a space or the text \"uid\"", c.own.Account, c.own.Group, w)
			}
			if allDigits && strings.HasSuffix(w, ":") {
				t.Errorf("account %q group %q: word %q ends in \":\" for a numeric account", c.own.Account, c.own.Group, w)
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

// The folder's group is its gid as a decimal number: the part of the
// chown spec that follows the colon when the owner has no name.
func TestForeignOwned_GroupIsTheFolderGid(t *testing.T) {
	dir := fowOwnDeployment(t)
	own, err := ForeignOwned(dir)
	if err != nil {
		t.Fatalf("ForeignOwned: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.FormatUint(uint64(info.Sys().(*syscall.Stat_t).Gid), 10)
	if own.Group != want {
		t.Errorf("Group = %q, want %q (the folder's gid)", own.Group, want)
	}
	if own.Group == "" || strings.Trim(own.Group, "0123456789") != "" {
		t.Errorf("Group = %q, want all digits with no space", own.Group)
	}
}

func fowGID(t *testing.T, path string) uint32 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Gid
}

// fowRunChown runs the command ChownCommandWords gives, less its leading
// sudo, with no shell, and fails the test unless chown exits 0 silently.
func fowRunChown(t *testing.T, dir string, own Ownership) {
	t.Helper()
	words := ChownCommandWords(dir, own)
	if len(words) < 3 || words[0] != "sudo" {
		t.Fatalf("words = %q, want a sudo command", words)
	}
	words = words[1:]
	var stderr strings.Builder
	cmd := exec.Command(words[0], words[1:]...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%q: %v; stderr: %s", words, err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("%q: stderr = %q, want empty", words, stderr.String())
	}
}

// The words must be a spec the real chown accepts and carries out, for
// an owner with no name and for a named one.
func TestChownCommandWords_SpecIsAcceptedByChown(t *testing.T) {
	fowRoot(t)
	if _, err := exec.LookPath("chown"); err != nil {
		t.Skip("no chown on PATH")
	}
	t.Run("owner with no name", func(t *testing.T) {
		const folderUID, fileUID, folderGID = 54399, 54398, 54397
		if _, err := user.LookupId(strconv.Itoa(folderUID)); err == nil {
			t.Skipf("uid %d has an account here", folderUID)
		}
		if _, err := user.LookupId(strconv.Itoa(fileUID)); err == nil {
			t.Skipf("uid %d has an account here", fileUID)
		}
		if _, err := user.LookupGroupId(strconv.Itoa(folderGID)); err == nil {
			t.Skipf("gid %d names a group here", folderGID)
		}
		dir := t.TempDir()
		env := filepath.Join(dir, ".env-orbit")
		key := filepath.Join(dir, ".orbit-secrets", "key")
		fowWrite(t, env, "A=b\n")
		fowWrite(t, key, "x\n")
		if err := os.Lchown(dir, folderUID, folderGID); err != nil {
			t.Skipf("cannot give %s to uid %d gid %d here: %v", dir, folderUID, folderGID, err)
		}
		asOperatorOf(t, dir, folderUID)
		fowChown(t, env, fileUID)
		fowChown(t, key, fileUID)

		own, err := ForeignOwned(dir)
		if err != nil {
			t.Fatalf("ForeignOwned: %v", err)
		}
		fowRunChown(t, dir, own)
		for _, p := range []string{env, key} {
			if got := fowUID(t, p); got != folderUID {
				t.Errorf("%s: uid = %d, want %d", p, got, folderUID)
			}
			if got := fowGID(t, p); got != folderGID {
				t.Errorf("%s: gid = %d, want %d", p, got, folderGID)
			}
		}
	})
	t.Run("named owner", func(t *testing.T) {
		// A named operator other than root: the launcher never runs as
		// root (#291), so a root-owned folder is not one it may use.
		u := fowNamedAccount(t)
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			t.Fatal(err)
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		env := filepath.Join(dir, ".env-orbit")
		fowWrite(t, env, "A=b\n")
		asOperatorOf(t, dir, uid)
		fowChown(t, env, 54398)

		own, err := ForeignOwned(dir)
		if err != nil {
			t.Fatalf("ForeignOwned: %v", err)
		}
		if own.Account != u.Username {
			t.Skipf("Account = %q, want %q for this case", own.Account, u.Username)
		}
		fowRunChown(t, dir, own)
		if got := fowUID(t, env); got != uint32(uid) {
			t.Errorf("uid = %d, want %d", got, uid)
		}
		if got := fowGID(t, env); got != uint32(gid) {
			t.Errorf("gid = %d, want %d (%s's login group)", got, gid, u.Username)
		}
	})
}

// fowNamedAccount is an account other than root that has a name, for
// the chown "<name>:" form; the test skips where there is none.
func fowNamedAccount(t *testing.T) *user.User {
	t.Helper()
	for _, name := range []string{"nobody", "daemon", "bin"} {
		if u, err := user.Lookup(name); err == nil && u.Uid != "0" {
			return u
		}
	}
	t.Skip("no named account other than root here")
	return nil
}

// fowRefusal checks err is the trust refusal for path, containing reason,
// and that no advice came with it.
func fowRefusal(t *testing.T, own Ownership, err error, path, reason string) {
	t.Helper()
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) {
		t.Fatalf("err = %v, want an *UntrustedPathError", err)
	}
	if untrusted.Path != path {
		t.Errorf("refusal Path = %q, want %q", untrusted.Path, path)
	}
	if reason != "" && !strings.Contains(untrusted.Reason, reason) {
		t.Errorf("refusal Reason = %q, want it to contain %q", untrusted.Reason, reason)
	}
	if len(own.Files) != 0 {
		t.Errorf("Files = %+v, want none with a refusal", own.Files)
	}
}

// The chown advice is only for a folder the launcher's account may use.
// Another account's folder is the trust refusal, with nothing to paste.
func TestForeignOwned_AnotherAccountsFolderIsRefusedNotAdvised(t *testing.T) {
	dir := fowOwnDeployment(t)
	asLauncherUser(t, os.Geteuid()+1)
	own, err := ForeignOwned(dir)
	fowRefusal(t, own, err, dir, "not by the user running the launcher")
	if own.Account != "" {
		t.Errorf("Account = %q, want empty with a refusal", own.Account)
	}
}

// The rule refuses only what the trust check refuses: the owner still
// gets the advice.
func TestForeignOwned_TheOwnerStillGetsTheAdvice(t *testing.T) {
	for name, uid := range map[string]int{"the folder's owner": os.Geteuid()} {
		t.Run(name, func(t *testing.T) {
			dir := fowOwnDeployment(t)
			asLauncherUser(t, uid)
			own, err := ForeignOwned(dir)
			if err != nil {
				t.Fatalf("ForeignOwned: %v", err)
			}
			if want := fowName(fowUID(t, dir)); own.Account != want {
				t.Errorf("Account = %q, want %q (the folder owner's name)", own.Account, want)
			}
			if len(own.Files) != 0 {
				t.Errorf("Files = %+v, want none", own.Files)
			}
		})
	}
}

// A folder the trust check refuses for any other reason is refused here
// too, not walked.
func TestForeignOwned_RefusesWhatTrustedRootRefuses(t *testing.T) {
	asLauncherUser(t, os.Geteuid())
	t.Run("writable by everyone", func(t *testing.T) {
		dir := fowOwnDeployment(t)
		chmod(t, dir, 0o777)
		own, err := ForeignOwned(dir)
		fowRefusal(t, own, err, dir, "writable by everyone")
	})
	t.Run("a symbolic link", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(fowOwnDeployment(t), link); err != nil {
			t.Fatal(err)
		}
		own, err := ForeignOwned(link)
		fowRefusal(t, own, err, link, "symbolic link")
	})
}

// Files an operator owns inside another account's folder are not handed
// over as a chown command for that operator; the folder's owner is still
// told.
func TestForeignOwned_AnOperatorsFilesInAnothersFolderAreNotHandedOver(t *testing.T) {
	fowRoot(t)
	const folderUID, fileUID = 54399, 54398
	dir := fowOwnDeployment(t)
	fowChown(t, dir, folderUID)
	entries := []string{
		".env-orbit",
		".orbit-secrets",
		".orbit-secrets/db_password",
		".orbit-secrets/sub",
		".orbit-secrets/sub/api_key",
	}
	for _, e := range entries {
		fowChown(t, filepath.Join(dir, e), fileUID)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	gid := strconv.Itoa(int(info.Sys().(*syscall.Stat_t).Gid))
	var wantFiles []ForeignFile
	for _, e := range entries {
		wantFiles = append(wantFiles, ForeignFile{e, "uid 54398"})
	}
	sort.Slice(wantFiles, func(i, j int) bool { return wantFiles[i].Path < wantFiles[j].Path })

	t.Run("an operator who does not own the folder", func(t *testing.T) {
		asLauncherUser(t, fileUID)
		own, err := ForeignOwned(dir)
		fowRefusal(t, own, err, dir, "")
		for _, w := range ChownCommandWords(dir, own) {
			if strings.HasSuffix(w, "/.env-orbit") || strings.HasSuffix(w, "/.orbit-secrets") {
				t.Errorf("command word %q names an item: nothing should be handed over", w)
			}
		}
	})
	// No "root" case: the launcher refuses to run as root (#291).
	for name, uid := range map[string]int{"the folder's owner": folderUID} {
		t.Run(name, func(t *testing.T) {
			asLauncherUser(t, uid)
			own, err := ForeignOwned(dir)
			if err != nil {
				t.Fatalf("ForeignOwned: %v", err)
			}
			if own.Account != "54399" {
				t.Errorf("Account = %q, want %q", own.Account, "54399")
			}
			if own.Group != gid {
				t.Errorf("Group = %q, want %q (the folder's gid)", own.Group, gid)
			}
			if !reflect.DeepEqual(own.Files, wantFiles) {
				t.Errorf("Files =\n  %+v\nwant\n  %+v", own.Files, wantFiles)
			}
			if words := ChownCommandWords(dir, own); len(words) < 4 || words[3] != "54399:"+gid {
				t.Errorf("words = %q, want the fourth word to be %q", words, "54399:"+gid)
			}
		})
	}
}
