// Package floor is the coverage ratchet's engine: parse a Go coverage
// profile and a floors file, compute per-package statement coverage, and
// report every rule it breaks.
//
// This checks STATEMENT coverage: every percentage here and in
// .github/coverage-floors.txt is "how many statements ran at least once".
// That is the only number Go's own tooling produces. An untaken branch IS
// caught, because a body that never runs is uncovered statements -- a
// function whose true path is never taken reports less than 100%, not
// 100%. What statement coverage cannot see is a compound condition:
// `if a && b` reports 100% having only ever run (true, true) and (false,
// true) -- `b` was never independently decisive, and no number here says
// so. Modelled on gauntlet's scripts/coverage-floor.py, which this
// package deliberately mirrors in spirit (same model mismatch, same
// fail-closed exit on malformed input), reimplemented in Go per
// orbit-launcher's "no new third-party dependencies" rule.
//
// Per-package coverage is computed by attributing each block in the
// profile to the directory of its source file -- which, in a Go coverage
// profile, already *is* the package's import path -- and summing
// statement counts: a package's coverage is
// (statements with count > 0) / (statements). A block that appears more
// than once in a merged profile (the same package touched by more than
// one test run, or `-count>1` reruns merged together) is counted once,
// not once per occurrence; see ParseProfile.
//
// Rules this package enforces:
//   - a package below its floor: coverage regressed, or a floor was set
//     wrong.
//   - a floor naming a package the profile no longer has: the package was
//     renamed, removed, or lost its only test file, so the number in the
//     floors file is no longer a measurement of anything.
//
// It deliberately does NOT fail when the profile has a package with no
// floor recorded: a package with no tests still shows up in a coverage
// profile at 0%, and the floors file is only ever added to for packages
// that have tests (the floors file is the one place that growth is
// reviewed, not something this package enforces at run time).
package floor

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Coverage is one package's statement totals.
type Coverage struct {
	Total   int // statements across every block attributed to the package
	Covered int // of those, statements in a block whose count was > 0
}

// Percent is the package's statement coverage, 0 to 100. A package with
// no statements at all (Total == 0) reports 0, not NaN or 100 --
// ParseProfile never actually produces one of these (a package with no
// executable statements has no blocks and so is never a map key at all),
// but Percent stays total so a future caller that assembles a Coverage
// by hand cannot divide by zero.
func (c Coverage) Percent() float64 {
	if c.Total == 0 {
		return 0
	}
	return 100 * float64(c.Covered) / float64(c.Total)
}

// Floor is one recorded line from the floors file.
type Floor struct {
	Package string
	Percent float64
	Line    int // 1-based line number in the floors file, for error messages
}

// block identifies one coverage block's source position. Two blocks at
// the same position in a merged profile describe the same statements
// counted twice -- true whenever more than one test binary touches the
// same package indirectly, or `-count>1` reruns are concatenated into one
// profile.
type block struct {
	file                                 string
	startLine, startCol, endLine, endCol int
}

var profileLine = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$`)

// ParseProfile reads a Go coverage profile, as `go test -coverprofile`
// writes it, and returns each package's statement coverage keyed by
// import path.
//
// Identical blocks (see the block type) are merged before counting:
// whichever occurrence has the higher count decides whether the block is
// covered. Since a block's count is never negative, "covered in at least
// one occurrence" is the same answer whichever of sum, max, or logical-OR
// merges the duplicates -- so this does not need to know whether the
// profile's mode is "set", "count", or "atomic" to get the right answer.
func ParseProfile(r io.Reader) (map[string]Coverage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("reading coverage profile: %w", err)
		}
		return nil, fmt.Errorf("coverage profile is empty")
	}
	if !strings.HasPrefix(sc.Text(), "mode: ") {
		return nil, fmt.Errorf("coverage profile: first line %q is not a %q header", sc.Text(), "mode: ...")
	}

	type seen struct {
		pkg     string
		numStmt int
		covered bool
	}
	blocks := map[block]seen{}

	lineNo := 1
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		m := profileLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("coverage profile line %d: does not match <file>:<line>.<col>,<line>.<col> <numStmt> <count>: %q", lineNo, line)
		}
		startLine, _ := strconv.Atoi(m[2])
		startCol, _ := strconv.Atoi(m[3])
		endLine, _ := strconv.Atoi(m[4])
		endCol, _ := strconv.Atoi(m[5])
		numStmt, err := strconv.Atoi(m[6])
		if err != nil {
			return nil, fmt.Errorf("coverage profile line %d: statement count %q: %w", lineNo, m[6], err)
		}
		count, err := strconv.Atoi(m[7])
		if err != nil {
			return nil, fmt.Errorf("coverage profile line %d: hit count %q: %w", lineNo, m[7], err)
		}

		key := block{file: m[1], startLine: startLine, startCol: startCol, endLine: endLine, endCol: endCol}
		s, ok := blocks[key]
		if !ok {
			s.pkg = path.Dir(m[1])
			s.numStmt = numStmt
		} else if s.numStmt != numStmt {
			return nil, fmt.Errorf("coverage profile line %d: block %s has %d statements, an earlier occurrence had %d",
				lineNo, m[1], numStmt, s.numStmt)
		}
		if count > 0 {
			s.covered = true
		}
		blocks[key] = s
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading coverage profile: %w", err)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("coverage profile has no data after the mode header")
	}

	out := map[string]Coverage{}
	for _, s := range blocks {
		c := out[s.pkg]
		c.Total += s.numStmt
		if s.covered {
			c.Covered += s.numStmt
		}
		out[s.pkg] = c
	}
	return out, nil
}

// ParseFloors reads the floors file: blank lines and `#`-prefixed
// comments are skipped, every other line is `<import path> <percent>`.
func ParseFloors(r io.Reader) (map[string]Floor, error) {
	floors := map[string]Floor{}
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("floors file line %d: expected '<import path> <percent>', got %q", lineNo, line)
		}
		pct, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("floors file line %d: floor %q for %s is not a number: %w", lineNo, fields[1], fields[0], err)
		}
		if prev, dup := floors[fields[0]]; dup {
			return nil, fmt.Errorf("floors file line %d: %s already has a floor, set on line %d", lineNo, fields[0], prev.Line)
		}
		floors[fields[0]] = Floor{Package: fields[0], Percent: pct, Line: lineNo}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading floors file: %w", err)
	}
	if len(floors) == 0 {
		return nil, fmt.Errorf("floors file has no '<import path> <percent>' lines")
	}
	return floors, nil
}

// Result is one measured package's coverage, and the floor (if any) it
// was checked against.
type Result struct {
	Package  string
	Got      float64
	Floor    float64
	HasFloor bool
}

// Check compares measured coverage against floors and returns a Result
// per measured package (sorted by import path, for stable output) plus
// every problem: a package below its floor, or a floor naming a package
// absent from the profile.
func Check(measured map[string]Coverage, floors map[string]Floor) (results []Result, problems []string) {
	pkgs := make([]string, 0, len(measured))
	for p := range measured {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		got := measured[pkg].Percent()
		r := Result{Package: pkg, Got: got}
		if f, ok := floors[pkg]; ok {
			r.HasFloor = true
			r.Floor = f.Percent
			if got < f.Percent {
				problems = append(problems, fmt.Sprintf("%s: got %.1f%%, floor %.1f%%", pkg, got, f.Percent))
			}
		}
		results = append(results, r)
	}

	floorNames := make([]string, 0, len(floors))
	for p := range floors {
		floorNames = append(floorNames, p)
	}
	sort.Strings(floorNames)
	for _, pkg := range floorNames {
		if _, ok := measured[pkg]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: floor %.1f%% set, but no package by that import path is in the profile (stale floor -- removed, renamed, or no longer tested)",
				pkg, floors[pkg].Percent))
		}
	}
	return results, problems
}
