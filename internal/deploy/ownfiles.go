package deploy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FileList is the manifest Orbit writes into every deployment (from
// Orbit's ai/orbit#1496): a "#" comment line, then one relative path per
// line of every file Orbit installed there, a directory it owns whole
// ending in "/", the manifest's own name included. Remove reads it to
// name exactly Orbit's files (#285); the launcher keeps no list of its
// own (owner, 2026-10-22), so a folder without it gets the whole-folder
// command, as before.
const FileList = ".orbit-files"

// The limits a FileList must meet, the ones install.sh applies to the
// engine's rollback journal, which has the same format.
const (
	fileListMaxBytes = 8 * 1024
	fileListMaxLines = 128
)

// fileListComponent is one path component a FileList line may hold.
var fileListComponent = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// keptFolders are never Orbit's to delete, whatever a FileList says:
// the backups and install.sh's rescued copies are the way back after a
// command that deletes the data volumes (owner, 2026-10-22, 20a).
var keptFolders = map[string]bool{"backups": true, "orbit-install-backups": true}

// Own is what Remove may name in a deployment folder: OwnFiles' answer.
// The zero Own means no usable FileList, which is the whole-folder
// command.
type Own struct {
	// Names are the FileList's paths that are present, relative to the
	// folder, in the list's order, without a trailing slash. None is
	// reached through a symbolic link.
	Names []string
	// Leftovers are the entries in the folder that are not Orbit's,
	// sorted: top-level names, and "scripts/mine.sh" for a foreign entry
	// inside a folder that holds Orbit's files. backups and
	// orbit-install-backups are always among them when present.
	Leftovers []string
	// ManifestIgnored is why a FileList that was there was refused
	// whole; nil when there was none or it was accepted.
	ManifestIgnored error
}

// OwnFiles reads targetDir's FileList and the folder as it stands. It
// only reads: nothing is changed. A missing FileList is the zero Own; a
// FileList failing any check (RequireTrustedPath, size, line count, the
// path pattern, never backups or orbit-install-backups) is ignored whole
// and says why in ManifestIgnored, with no names. The only error is
// ErrNoTargetDir.
func OwnFiles(targetDir string) (Own, error) {
	if targetDir == "" {
		return Own{}, ErrNoTargetDir
	}
	lines, err := readFileList(targetDir)
	if errors.Is(err, os.ErrNotExist) {
		return Own{}, nil
	}
	if err != nil {
		return Own{ManifestIgnored: err}, nil
	}
	names := presentNames(targetDir, lines)
	leftovers, err := leftoversOf(targetDir, names)
	if err != nil {
		return Own{ManifestIgnored: err}, nil
	}
	return Own{Names: names, Leftovers: leftovers}, nil
}

// readFileList returns the FileList's path lines, slashes trimmed, in
// order and without repeats, or why it is refused. A missing list is an
// os.ErrNotExist error.
func readFileList(dir string) ([]string, error) {
	if err := RequireTrustedPath(dir, FileList); err != nil {
		var untrusted *UntrustedPathError
		if !errors.As(err, &untrusted) && errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	f, err := os.Open(filepath.Join(filepath.Clean(dir), FileList)) //nolint:gosec // G304: the trusted-path check above
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, fileListMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	if len(data) > fileListMaxBytes {
		return nil, fmt.Errorf("%s is over %d bytes", FileList, fileListMaxBytes)
	}
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	n := 0
	for sc.Scan() {
		n++
		if n > fileListMaxLines {
			return nil, fmt.Errorf("%s is over %d lines", FileList, fileListMaxLines)
		}
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, err := fileListPath(line)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", FileList, n, err)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	return out, nil
}

// fileListPath checks one FileList line and returns it without its
// directory slash: relative, components of [A-Za-z0-9._-] joined by "/",
// no "." or "..", never backups, orbit-install-backups or under them.
func fileListPath(line string) (string, error) {
	if strings.HasPrefix(line, "/") {
		return "", errors.New("an absolute path")
	}
	name := strings.TrimSuffix(line, "/")
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if !fileListComponent.MatchString(p) {
			return "", errors.New("a path outside the allowed pattern")
		}
		if p == "." || p == ".." {
			return "", errors.New("a . or .. component")
		}
	}
	if keptFolders[parts[0]] {
		return "", errors.New("a path under " + parts[0])
	}
	return name, nil
}

// presentNames keeps the listed paths that exist, each reached only
// through real directories: a path under a symbolic link is not named,
// since rm would follow the link out of the folder. The last component
// may itself be a link: rm -rf removes the link, not what it names.
func presentNames(dir string, lines []string) []string {
	var out []string
	for _, name := range lines {
		if reachable(dir, name) {
			out = append(out, name)
		}
	}
	return out
}

func reachable(dir, name string) bool {
	parts := strings.Split(name, "/")
	path := filepath.Clean(dir)
	for i, p := range parts {
		path = filepath.Join(path, p)
		info, err := os.Lstat(path) //nolint:gosec // G703: Lstat of a checked name in the operator's folder
		if err != nil {
			return false
		}
		if i < len(parts)-1 && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return false
		}
	}
	return true
}

// leftoversOf lists what in dir is not Orbit's: every entry that is not
// a name, descending into a real directory that holds a name (config,
// scripts) to find a foreign entry there. Sorted.
func leftoversOf(dir string, names []string) ([]string, error) {
	named := map[string]bool{}
	holds := map[string]bool{} // directories with a name below them
	for _, n := range names {
		named[n] = true
		for p := filepath.Dir(n); p != "."; p = filepath.Dir(p) {
			holds[p] = true
		}
	}
	var out []string
	var walk func(rel string) error
	walk = func(rel string) error {
		entries, err := os.ReadDir(filepath.Join(dir, rel)) //nolint:gosec // G304: listing the operator's folder
		if err != nil {
			return err
		}
		for _, e := range entries {
			p := e.Name()
			if rel != "" {
				p = rel + "/" + p
			}
			switch {
			case named[p]:
			case holds[p] && e.IsDir() && e.Type()&os.ModeSymlink == 0:
				if err := walk(p); err != nil {
					return err
				}
			default:
				out = append(out, p)
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// rmdirFolders are the directories that hold Orbit's names without
// being names themselves, deepest first and otherwise in the order the
// names first reach them: Orbit owns the files in config and scripts,
// not the folders, so the command removes those only when empty.
func rmdirFolders(names []string) []string {
	named := map[string]bool{}
	for _, n := range names {
		named[n] = true
	}
	underName := func(p string) bool {
		for ; p != "."; p = filepath.Dir(p) {
			if named[p] {
				return true
			}
		}
		return false
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		for p := filepath.Dir(n); p != "."; p = filepath.Dir(p) {
			if !seen[p] && !underName(p) {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.Count(out[i], "/") > strings.Count(out[j], "/")
	})
	return out
}

// ownRemovalWords is RemovalCommandWords for a folder whose FileList
// was read:
//
//	(cd <folder> && docker compose --env-file .env-orbit down -v &&
//	 sudo rm -rf -- <names> && sudo rmdir -- config scripts) &&
//	 sudo rmdir -- <folder>
//
// One subshell, so every name is relative to the folder and nothing
// after a failed cd runs; rmdir refuses a folder that is not empty and
// says so, which stops the chain. The root and home folders get no
// final rmdir (owner, 2026-10-22, 21a).
func ownRemovalWords(targetDir string, own Own) []string {
	q := shellQuote(targetDir)
	words := []string{"(cd", q, "&&", "docker", "compose", "--env-file", EnvFile, "down", "-v",
		"&&", "sudo", "rm", "-rf", "--"}
	for _, n := range own.Names {
		words = append(words, shellQuote(n))
	}
	if dirs := rmdirFolders(own.Names); len(dirs) > 0 {
		words = append(words, "&&", "sudo", "rmdir", "--")
		for _, d := range dirs {
			words = append(words, shellQuote(d))
		}
	}
	words[len(words)-1] += ")"
	if refuseSharedFolder(targetDir) == nil {
		words = append(words, "&&", "sudo", "rmdir", "--", q)
	}
	return words
}
