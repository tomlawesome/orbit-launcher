// Package review finds what a dependency carries beyond its own code, and
// checks each find against a recorded review (#176).
//
// go-licenses reads one licence per package: the nearest LICENSE file. That
// covers the package's own code, not what it carries from elsewhere --
// code copied in, tables generated from someone else's data, files it
// embeds into the binary. None of those has a licence field, which is how
// birdcage's Python gate passed OpenCanary as "BSD" while it shipped
// Synology's images and GPLv3 stylesheets (ai/birdcage#179). This package
// looks at the files of every package linked into the shipped binary, so
// only what really ships is reviewed.
//
// A review names an exact module@version, so a version bump stops it
// matching and the files are read again. A review matching nothing is
// stale and fails too, so the file never outlives what it describes.
package review

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Kind is why a file needs a review.
type Kind string

const (
	// Embed is a file the package embeds into the binary with //go:embed.
	Embed Kind = "embed"
	// Vendored is code that sits under a third_party or vendor directory,
	// or below a licence file of its own inside the module.
	Vendored Kind = "vendored"
	// Generated is a Go file carrying the standard "Code generated ... DO
	// NOT EDIT." header: usually a table built from someone else's data.
	Generated Kind = "generated"
	// Notice is a Go file whose comments say part of it came from
	// elsewhere ("ported from", "Licensed under", an SPDX tag).
	Notice Kind = "notice"
)

// Package is one linked package, as `go list -deps -json` describes it.
type Package struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	SFiles     []string // assembly, linked like Go code
	EmbedFiles []string
	Module     *Module
}

// Module is the module a Package belongs to.
type Module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
}

// Finding is one file that needs a review.
type Finding struct {
	Module  string // module@version
	Path    string // slash-separated, relative to the module root
	Kind    Kind
	Excerpt string // the line that triggered a Notice
}

func (f Finding) String() string {
	s := fmt.Sprintf("%s %s %s", f.Module, f.Path, f.Kind)
	if f.Excerpt != "" {
		s += fmt.Sprintf(" (%q)", f.Excerpt)
	}
	return s
}

// generatedHeader is the marker `go generate` tools write
// (https://go.dev/s/generatedcode).
var generatedHeader = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// provenance matches a comment saying where some of a file came from.
// It is deliberately narrow: "derived from" is left out because Go code
// says it of contexts and values far more often than of other people's
// code.
var provenance = regexp.MustCompile(`(?i)\b(ported|adapted|taken|copied|extracted|borrowed|translated) from\b|licen[cs]e agreement|unicode\.org/(license|copyright)|\bunder (the )?[\w.-]+ licen[cs]e\b|\blicensed under\b|SPDX-License-Identifier`)

// licenceFile matches the names go-licenses itself treats as licences.
var licenceFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|unlicense)(\.|-|_|$)`)

// Scan returns everything the non-main packages in pkgs carry that needs
// a review, sorted, one finding per file. A file that is both generated
// and carries a notice is reported as generated: the reviewer reads its
// header either way.
func Scan(pkgs []Package) ([]Finding, error) {
	seen := map[string]bool{}
	var out []Finding
	add := func(f Finding) {
		key := f.Module + " " + f.Path
		if !seen[key] {
			seen[key] = true
			out = append(out, f)
		}
	}
	for _, p := range pkgs {
		if p.Module == nil || p.Module.Main {
			continue
		}
		mod := p.Module.Path + "@" + p.Module.Version
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)

		for _, e := range p.EmbedFiles {
			add(Finding{Module: mod, Path: path.Join(rel, e), Kind: Embed})
		}

		vendored, err := isVendored(p.Module.Dir, rel)
		if err != nil {
			return nil, err
		}
		for _, name := range append(append([]string(nil), p.GoFiles...), p.SFiles...) {
			file := path.Join(rel, name)
			if vendored {
				add(Finding{Module: mod, Path: file, Kind: Vendored})
				continue
			}
			kind, excerpt, err := readGoFile(filepath.Join(p.Dir, name))
			if err != nil {
				return nil, err
			}
			if kind != "" {
				add(Finding{Module: mod, Path: file, Kind: kind, Excerpt: excerpt})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// isVendored reports whether the package directory rel (relative to the
// module root) is someone else's code: it sits under a third_party or
// vendor directory, or it or a directory between it and the module root
// holds a licence file of its own. The module root's licence is the one
// go-licenses already read.
func isVendored(moduleDir, rel string) (bool, error) {
	if rel == "." {
		return false, nil
	}
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if part == "third_party" || part == "vendor" {
			return true, nil
		}
	}
	for i := len(parts); i > 0; i-- {
		entries, err := os.ReadDir(filepath.Join(moduleDir, filepath.Join(parts[:i]...)))
		if err != nil {
			return false, err
		}
		for _, e := range entries {
			if !e.IsDir() && licenceFile.MatchString(e.Name()) {
				return true, nil
			}
		}
	}
	return false, nil
}

// readGoFile classifies one Go or assembly file (both use Go's comment
// syntax): Generated if its header says so,
// Notice if a comment line matches provenance, otherwise nothing.
func readGoFile(name string) (Kind, string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	inBlock, pastPackage := false, false
	var notice string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// The convention puts the generated marker before the package
		// clause; one after it is just text.
		if !pastPackage && generatedHeader.MatchString(line) {
			return Generated, "", nil
		}
		if !inBlock && strings.HasPrefix(line, "package ") {
			pastPackage = true
		}
		comment := ""
		switch {
		case inBlock:
			comment = line
			if strings.Contains(line, "*/") {
				inBlock = false
			}
		case strings.HasPrefix(line, "/*"):
			comment = line
			inBlock = !strings.Contains(line, "*/")
		default:
			if i := strings.Index(line, "//"); i >= 0 {
				comment = line[i:]
			}
		}
		if notice == "" && comment != "" && provenance.MatchString(comment) {
			notice = comment
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	if notice != "" {
		return Notice, notice, nil
	}
	return "", "", nil
}

// Review is one recorded review: every finding of Kind in Module whose
// path is Path, or sits under Path when Path ends in "/" ("./" is the
// whole module), has been read and its licence recorded.
type Review struct {
	Line    int
	Module  string // module@version, exact
	Path    string
	Kind    Kind
	Licence string
}

func (r Review) covers(f Finding) bool {
	if r.Module != f.Module || r.Kind != f.Kind {
		return false
	}
	if r.Path == "./" {
		return true
	}
	if strings.HasSuffix(r.Path, "/") {
		return strings.HasPrefix(f.Path, r.Path)
	}
	return r.Path == f.Path
}

// ParseReviews reads the review file: one review per line as
// "<module>@<version> <path> <kind> <licence or finding>", with blank
// lines and lines starting with # ignored.
func ParseReviews(text string) ([]Review, error) {
	var out []Review
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("line %d: want \"<module>@<version> <path> <kind> <licence>\", got %q", i+1, line)
		}
		if !strings.Contains(fields[0], "@") {
			return nil, fmt.Errorf("line %d: %q has no @version; a review names an exact version", i+1, fields[0])
		}
		kind := Kind(fields[2])
		switch kind {
		case Embed, Vendored, Generated, Notice:
		default:
			return nil, fmt.Errorf("line %d: unknown kind %q (want embed, vendored, generated or notice)", i+1, fields[2])
		}
		out = append(out, Review{Line: i + 1, Module: fields[0], Path: fields[1], Kind: kind, Licence: fields[3]})
	}
	return out, nil
}

// Check returns the findings no review covers, and the reviews that cover
// no finding.
func Check(findings []Finding, reviews []Review) (unreviewed []Finding, stale []Review) {
	used := make([]bool, len(reviews))
	for _, f := range findings {
		covered := false
		for i, r := range reviews {
			if r.covers(f) {
				used[i] = true
				covered = true
			}
		}
		if !covered {
			unreviewed = append(unreviewed, f)
		}
	}
	for i, r := range reviews {
		if !used[i] {
			stale = append(stale, r)
		}
	}
	return unreviewed, stale
}
