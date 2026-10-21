package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// X-Q3 (#195): the report prints coverage at a coarser precision than the
// check compares at, so a printed 85.0 can fail an 85.0 floor and a failure
// can read "got 85.0%, floor 85.0%". These tests read the numbers a
// maintainer would read and use them the way a maintainer would.

// profileFor builds a profile in which pkg has `covered` of `total`
// statements covered, as two blocks (one run, one not).
func profileFor(pkg string, covered, total int) string {
	p := "mode: atomic\n"
	if covered > 0 {
		p += fmt.Sprintf("%s/a.go:1.1,2.2 %d 1\n", pkg, covered)
	}
	if total-covered > 0 {
		p += fmt.Sprintf("%s/a.go:3.1,4.2 %d 0\n", pkg, total-covered)
	}
	return p
}

var (
	gotRE   = regexp.MustCompile(`got\s+([0-9]+(?:\.[0-9]+)?)%`)
	floorRE = regexp.MustCompile(`floor\s+([0-9]+(?:\.[0-9]+)?)%`)
)

// printed returns the first number the regexp finds on the report line for
// pkg in out.
func printed(t *testing.T, re *regexp.Regexp, out, pkg string) float64 {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, pkg+" ") {
			continue
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			t.Fatalf("parsing %q: %v", m[1], err)
		}
		return v
	}
	t.Fatalf("no %v number on the report line for %s in:\n%s", re, pkg, out)
	return 0
}

// The number printed for an unfloored package, written into the floors
// file as that package's floor, must pass: it is how a new floor is set.
func TestCommand_PrintedCoverageCanBeUsedAsItsOwnFloor(t *testing.T) {
	const pkg = "example.com/mod/pkg"
	const other = "example.com/mod/other"
	for _, c := range []struct {
		name           string
		covered, total int
	}{
		{"two thirds", 2, 3},
		{"one third", 1, 3},
		{"84.96 percent", 8496, 10000},
		{"99.99 percent", 9999, 10000},
		{"exact", 17, 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			profile := writeTemp(t, "coverage.out", profileFor(pkg, c.covered, c.total)+profileFor(other, 1, 1)[len("mode: atomic\n"):])

			code, stdout, stderr := coveragefloor(t, profile, writeTemp(t, "floors0.txt", other+" 100.0\n"))
			if code != 0 {
				t.Fatalf("first run: exit code = %d, stderr %q; want 0", code, stderr)
			}
			got := printed(t, gotRE, stdout, pkg)

			floors := writeTemp(t, "floors1.txt", other+" 100.0\n"+pkg+" "+strconv.FormatFloat(got, 'f', -1, 64)+"\n")
			code, stdout, stderr = coveragefloor(t, profile, floors)
			if code != 0 {
				t.Errorf("a floor of %v, the number printed for %s, failed: exit code = %d, stdout %q, stderr %q", got, pkg, code, stdout, stderr)
			}
		})
	}
}

// The report and the verdict agree: it never reads "got 85.0%, floor 85.0%"
// and fails, and never reads got below floor and passes.
func TestCommand_PrintedNumbersAgreeWithTheVerdict(t *testing.T) {
	const pkg = "example.com/mod/pkg"
	for _, c := range []struct {
		name           string
		covered, total int
		floor          string
	}{
		{"84.96 against 85.0", 8496, 10000, "85.0"},
		{"66.67 against 66.7", 2, 3, "66.7"},
		{"66.67 against 66.6", 2, 3, "66.6"},
		{"exactly on the floor", 17, 20, "85.0"},
		{"well below", 1, 2, "85.0"},
		{"well above", 99, 100, "85.0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			profile := writeTemp(t, "coverage.out", profileFor(pkg, c.covered, c.total))
			floors := writeTemp(t, "floors.txt", pkg+" "+c.floor+"\n")

			code, stdout, stderr := coveragefloor(t, profile, floors)

			got, floor := printed(t, gotRE, stdout, pkg), printed(t, floorRE, stdout, pkg)
			if passed := code == 0; passed != (got >= floor) {
				t.Errorf("exit code %d, but the report reads got %v%%, floor %v%%\nstdout: %s\nstderr: %s", code, got, floor, stdout, stderr)
			}
			if code != 0 {
				for _, line := range strings.Split(stderr, "\n") {
					if !strings.Contains(line, pkg+": got") {
						continue
					}
					g, f := gotRE.FindStringSubmatch(line), floorRE.FindStringSubmatch(line)
					if g == nil || f == nil {
						t.Fatalf("failure line %q does not give both numbers", line)
					}
					gv, _ := strconv.ParseFloat(g[1], 64)
					fv, _ := strconv.ParseFloat(f[1], 64)
					if gv >= fv {
						t.Errorf("failure line %q reads as at or above its floor", line)
					}
				}
			}
		})
	}
}

// T2-Q6 / X-Q2 (#195): a floor that is not a usable percentage fails the
// command closed. NaN would never fail the comparison; a negative would
// never fail either.
func TestCommand_UnusableFloorExitsOne(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile)
	for _, bad := range []string{"NaN", "nan", "-1", "-0.5", "-Inf"} {
		t.Run(bad, func(t *testing.T) {
			floors := writeTemp(t, "floors.txt", "example.com/mod/pkg "+bad+"\n")

			code, stdout, stderr := coveragefloor(t, profile, floors)

			if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "coveragefloor: ") {
				t.Errorf("floor %s: exit code = %d, stdout %q, stderr %q; want 1 and the error alone", bad, code, stdout, stderr)
			}
		})
	}
}
