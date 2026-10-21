package deploy

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Ownership is who holds a deployment's configuration (#265): the
// account owning the deployment folder, and the files Repair can't check
// because another account owns them.
type Ownership struct {
	// Account is the folder owner, as chown takes it: the account name,
	// or the bare numeric uid when no account has that uid (a static
	// binary reads only /etc/passwd, so an account from a directory
	// service is unnamed here); see Group.
	Account string
	// Group is the folder's group as a bare numeric gid. The chown command
	// uses it only when Account is a number: chown's "<owner>:" form looks
	// the login group up by name, so a numeric owner needs the group spelt
	// out, and the folder's own group is what its files should carry.
	Group string
	// Files are the foreign files, relative to the folder and sorted.
	Files []ForeignFile
}

// ForeignFile is one file owned by an account other than the folder's.
type ForeignFile struct {
	Path, Owner string
}

// ForeignOwned reads, never changes, who owns dir's .env-orbit, its
// .orbit-secrets and every entry under it: those whose owner differs
// from the folder's are listed. Each is looked at with Lstat, so a
// symlink is its own entry and nothing behind it is walked. A missing file is not
// foreign. The folder is Lstat'd too, as trustedRoot does. An error
// comes with whatever was read before it.
func ForeignOwned(dir string) (Ownership, error) {
	dir = filepath.Clean(dir)
	info, err := os.Lstat(dir)
	if err != nil {
		return Ownership{}, err
	}
	names := map[int]string{}
	name := func(uid int) string {
		if n, ok := names[uid]; ok {
			return n
		}
		id := strconv.Itoa(uid)
		n := "uid " + id
		if u, err := user.LookupId(id); err == nil {
			n = u.Username
		}
		names[uid] = n
		return n
	}
	folder := ownerOf(info)
	// Account goes into the chown command, so it is never the shown
	// "uid N" form: chown accepts a bare uid where no name resolves.
	account := strconv.Itoa(folder)
	if u, err := user.LookupId(account); err == nil {
		account = u.Username
	}
	own := Ownership{Account: account, Group: strconv.Itoa(groupOf(info))}
	add := func(rel string, info os.FileInfo) bool {
		uid := ownerOf(info)
		if uid == folder {
			return false
		}
		own.Files = append(own.Files, ForeignFile{Path: filepath.ToSlash(rel), Owner: name(uid)})
		return true
	}
	sorted := func() Ownership {
		sort.Slice(own.Files, func(i, j int) bool { return own.Files[i].Path < own.Files[j].Path })
		return own
	}

	if info, err := os.Lstat(filepath.Join(dir, EnvFile)); err == nil {
		add(EnvFile, info)
	} else if !os.IsNotExist(err) {
		return sorted(), err
	}

	// WalkDir Lstats every entry and never follows a link, the root
	// included: a .orbit-secrets that is a link is reported to the walk
	// as itself and not entered. A directory another account owns is
	// listed as itself, .orbit-secrets included, and one that can't be
	// read (0700, say) is left at that: chown -R gives back all of it,
	// and an error there would let Repair run.
	secrets := filepath.Join(dir, SecretsDir)
	foreignDirs := map[string]bool{}
	err = filepath.WalkDir(secrets, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == secrets && os.IsNotExist(err) {
				return fs.SkipAll
			}
			if d != nil && d.IsDir() && foreignDirs[path] {
				return fs.SkipDir
			}
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if add(rel, info) && d.IsDir() {
			foreignDirs[path] = true
		}
		return nil
	})
	return sorted(), err
}

// groupOf is ownerOf for the group: the gid, or -1 where the platform
// has none.
func groupOf(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Gid)
	}
	return -1
}

// ChownCommandWords returns the shell command that gives own's files
// back to own.Account, as shell words each already quoted: `sudo chown
// -R <Account>: <dir>/.env-orbit <dir>/.orbit-secrets`, or `<uid>:<gid>`
// when the owner has no name (see chownSpec), naming only the top-level
// items that hold a foreign file. The trailing colon with no group sets
// the account's own login group. Quoted as
// RemovalCommandWords quotes, because the line is pasted into a shell.
// It names no items when own has no files.
func ChownCommandWords(dir string, own Ownership) []string {
	var env, secrets bool
	for _, f := range own.Files {
		top, _, _ := strings.Cut(f.Path, "/")
		switch top {
		case EnvFile:
			env = true
		case SecretsDir:
			secrets = true
		}
	}
	words := []string{"sudo", "chown", "-R", shellQuote(chownSpec(own))}
	if env {
		words = append(words, shellQuote(filepath.Join(dir, EnvFile)))
	}
	if secrets {
		words = append(words, shellQuote(filepath.Join(dir, SecretsDir)))
	}
	return words
}

// chownSpec is the owner word. "<name>:" gives the files to the account
// and its login group. GNU chown looks that group up by name and
// rejects "54399:" as an invalid spec even when uid 54399 has an
// account, so a numeric owner is paired with the folder's own group
// instead ("54399:1000"), or stands alone when no group is known.
func chownSpec(own Ownership) string {
	if strings.Trim(own.Account, "0123456789") != "" {
		return own.Account + ":"
	}
	if own.Group == "" {
		return own.Account
	}
	return own.Account + ":" + own.Group
}
