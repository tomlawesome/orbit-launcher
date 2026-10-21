package deploy

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Ownership is who holds a deployment's configuration (#265): the
// account owning the deployment folder, and the files Repair can't check
// because another account owns them.
type Ownership struct {
	// Account is the folder owner's name, or "uid N" when no account
	// has that uid.
	Account string
	// Files are the foreign files, relative to the folder and sorted.
	Files []ForeignFile
}

// ForeignFile is one file owned by an account other than the folder's.
type ForeignFile struct {
	Path, Owner string
}

// ForeignOwned reads, never changes, who owns dir's .env-orbit and every
// entry under its .orbit-secrets: those whose owner differs from the
// folder's are listed. Each is looked at with Lstat, so a symlink is its
// own entry and nothing behind it is walked. A missing file is not
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
	own := Ownership{Account: name(folder)}
	add := func(rel string, info os.FileInfo) {
		if uid := ownerOf(info); uid != folder {
			own.Files = append(own.Files, ForeignFile{Path: filepath.ToSlash(rel), Owner: name(uid)})
		}
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
	// as itself and not entered.
	secrets := filepath.Join(dir, SecretsDir)
	err = filepath.WalkDir(secrets, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == secrets && os.IsNotExist(err) {
				return fs.SkipAll
			}
			return err
		}
		if path == secrets {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		add(rel, info)
		return nil
	})
	return sorted(), err
}

// ChownCommandWords returns the shell command that gives own's files
// back to own.Account, as shell words each already quoted: `sudo chown
// -R <Account>: <dir>/.env-orbit <dir>/.orbit-secrets`, naming only the
// top-level items that hold a foreign file. The trailing colon with no
// group sets the account's own login group. Quoted as
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
	words := []string{"sudo", "chown", "-R", shellQuote(own.Account + ":")}
	if env {
		words = append(words, shellQuote(filepath.Join(dir, EnvFile)))
	}
	if secrets {
		words = append(words, shellQuote(filepath.Join(dir, SecretsDir)))
	}
	return words
}
