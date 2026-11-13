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
	"syscall"
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

// listingMax bounds how many entries OwnFiles reads from one folder. A
// folder holding more is treated as one whose listing failed: the
// list's names still stand, but what else is there is not claimed.
const listingMax = 4096

// scratchExact and scratchPrefixes are the runtime names Orbit's engine,
// install.sh and the launcher leave in a deployment, fixed by contract
// (install.sh rollback_walk_folder; the launcher's own .env-orbit.tmp-*):
// the lock, a stale lock, staging and recovery folders, and the
// half-written .env-orbit copies. They are matched literally against the
// folder's listing, a prefix followed by at least one [A-Za-z0-9._-]
// character, so the command names the actual entries, never a glob.
var (
	scratchExact    = map[string]bool{".orbit-engine.lock": true}
	scratchPrefixes = []string{
		".orbit-engine.lock.stale-",
		".orbit-install-staging.",
		".orbit-repair-recovery.",
		EnvFile + ".installing.",
		EnvFile + ".updating.",
		EnvFile + ".migrating.",
		EnvFile + stagingSuffix,
	}
)

// isScratch reports whether a top-level entry name is a scratch name.
func isScratch(name string) bool {
	if scratchExact[name] {
		return true
	}
	for _, p := range scratchPrefixes {
		if rest, ok := strings.CutPrefix(name, p); ok && fileListComponent.MatchString(rest) {
			return true
		}
	}
	return false
}

// keptFolders are never Orbit's to delete, whatever a FileList says:
// the backups and install.sh's rescued copies are the way back after a
// command that deletes the data volumes (owner, 2026-10-22, 20a).
var keptFolders = map[string]bool{"backups": true, "orbit-install-backups": true}

// Own is what Remove may name in a deployment folder: OwnFiles' answer.
// The zero Own means no usable FileList, which is the whole-folder
// command.
type Own struct {
	// Listed is set when the folder's FileList was read and accepted:
	// the command is then file by file even when nothing it names is
	// present, and never the whole folder (owner, 2026-10-22, 20a).
	Listed bool
	// Names are the FileList's paths that are present, relative to the
	// folder, in the list's order, without a trailing slash, then the
	// scratch names present at the top level, in listing order. None is
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
	// ListingFailed is why the folder, or a folder holding Orbit's files,
	// could not be listed in full (unreadable, or over listingMax
	// entries). The list's names still stand; Leftovers is then empty,
	// and nothing is claimed about what else is there.
	ListingFailed error
}

// FileByFile reports whether own gives the file-by-file command: a list
// was accepted, or names were given.
func (own Own) FileByFile() bool { return own.Listed || len(own.Names) > 0 }

// OwnFiles reads targetDir's FileList and the folder as it stands. It
// only reads: nothing is changed. A folder without a FileList is the
// zero Own, whatever the folder itself is. A FileList failing any check
// (RequireTrustedPath, size, line count, the path pattern, never backups
// or orbit-install-backups) is ignored whole and says why in
// ManifestIgnored, with no names. The only error is ErrNoTargetDir.
func OwnFiles(targetDir string) (Own, error) {
	if targetDir == "" {
		return Own{}, ErrNoTargetDir
	}
	if _, err := os.Lstat(filepath.Join(targetDir, FileList)); errors.Is(err, os.ErrNotExist) { //nolint:gosec // G703: Lstat of the list's name in the operator's folder
		return Own{}, nil
	}
	lines, err := readFileList(targetDir)
	if err != nil {
		return Own{ManifestIgnored: err}, nil
	}
	own := Own{Listed: true, Names: presentNames(targetDir, lines)}
	top, err := listFolder(targetDir)
	for _, e := range top { // what was read stands, even from a listing cut short
		if isScratch(e.Name()) && !contains(own.Names, e.Name()) {
			own.Names = append(own.Names, e.Name())
		}
	}
	if err == nil {
		own.Leftovers, err = leftoversOf(targetDir, top, own.Names)
	}
	if err != nil {
		own.Leftovers, own.ListingFailed = nil, err
	}
	return own, nil
}

func contains(in []string, s string) bool {
	for _, v := range in {
		if v == s {
			return true
		}
	}
	return false
}

// listFolder lists dir, sorted, reading at most listingMax entries: a
// folder holding more answers what was read and an error.
func listFolder(dir string) ([]os.DirEntry, error) {
	f, err := os.Open(dir) //nolint:gosec // G304: listing the operator's folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(listingMax)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if len(entries) == listingMax {
		if more, _ := f.ReadDir(1); len(more) > 0 {
			return entries, fmt.Errorf("%s holds more than %d entries", dir, listingMax)
		}
	}
	return entries, nil
}

// readFileList returns the FileList's path lines, slashes trimmed, in
// order and without repeats, or why it is refused. The file is opened
// without following a link and checked again on the open descriptor, so
// the trust check and the read are of the same file.
func readFileList(dir string) ([]string, error) {
	dir = filepath.Clean(dir)
	path := filepath.Join(dir, FileList)
	if err := RequireTrustedPath(dir, FileList); err != nil {
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	checked, err := os.Lstat(path) //nolint:gosec // G703: the trusted-path check above
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0) //nolint:gosec // G304: the trusted-path check above
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileList, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(checked, opened) {
		return nil, &UntrustedPathError{Path: path, Reason: "it changed while it was being checked"}
	}
	if err := checkModeAndOwner(path, opened, launcherEUID()); err != nil {
		return nil, err
	}
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

// leftoversOf lists what in dir is not Orbit's, from its top-level
// listing top: every entry that is not a name, descending into a real
// directory that holds a name (config, scripts) to find a foreign entry
// there. Sorted. A folder that cannot be listed in full is an error.
func leftoversOf(dir string, top []os.DirEntry, names []string) ([]string, error) {
	named := map[string]bool{}
	holds := map[string]bool{} // directories with a name below them
	for _, n := range names {
		named[n] = true
		for p := filepath.Dir(n); p != "."; p = filepath.Dir(p) {
			holds[p] = true
		}
	}
	var out []string
	var walk func(rel string, entries []os.DirEntry) error
	walk = func(rel string, entries []os.DirEntry) error {
		for _, e := range entries {
			p := e.Name()
			if rel != "" {
				p = rel + "/" + p
			}
			switch {
			case named[p]:
			case holds[p] && e.IsDir() && e.Type()&os.ModeSymlink == 0:
				inner, err := listFolder(filepath.Join(dir, p))
				if err != nil {
					return err
				}
				if err := walk(p, inner); err != nil {
					return err
				}
			default:
				out = append(out, p)
			}
		}
		return nil
	}
	if err := walk("", top); err != nil {
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
// after a failed cd runs; with no names present the rm is left out, and
// with no names in a subfolder so is the first rmdir; rmdir refuses a folder that is not empty and
// says so, which stops the chain. The root and home folders get no
// final rmdir (owner, 2026-10-22, 21a).
func ownRemovalWords(targetDir string, own Own) []string {
	q := shellQuote(targetDir)
	words := []string{"(cd", q, "&&", "docker", "compose", "--env-file", EnvFile, "down", "-v"}
	if len(own.Names) > 0 {
		words = append(words, "&&", "sudo", "rm", "-rf", "--")
		for _, n := range own.Names {
			words = append(words, shellQuote(n))
		}
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
