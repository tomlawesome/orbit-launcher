package floor

import (
	"strings"
	"testing"
)

func mustParseProfile(t *testing.T, body string) map[string]Coverage {
	t.Helper()
	cov, err := ParseProfile(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseProfile: unexpected error: %v", err)
	}
	return cov
}

func mustParseFloors(t *testing.T, body string) map[string]Floor {
	t.Helper()
	fl, err := ParseFloors(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseFloors: unexpected error: %v", err)
	}
	return fl
}

// --- ParseProfile ---

func TestParseProfile_BasicPackageSplit(t *testing.T) {
	// Two files in the same package, one file in another: statements
	// must be attributed by directory, not by file.
	body := `mode: atomic
example.com/mod/pkg/a.go:1.1,2.2 2 1
example.com/mod/pkg/b.go:3.1,4.2 3 0
example.com/mod/other/c.go:1.1,2.2 5 5
`
	cov := mustParseProfile(t, body)
	if len(cov) != 2 {
		t.Fatalf("expected 2 packages, got %d: %+v", len(cov), cov)
	}
	pkg := cov["example.com/mod/pkg"]
	if pkg.Total != 5 || pkg.Covered != 2 {
		t.Errorf("pkg: got Total=%d Covered=%d, want Total=5 Covered=2", pkg.Total, pkg.Covered)
	}
	other := cov["example.com/mod/other"]
	if other.Total != 5 || other.Covered != 5 {
		t.Errorf("other: got Total=%d Covered=%d, want Total=5 Covered=5", other.Total, other.Covered)
	}
}

func TestParseProfile_BlockDedupe(t *testing.T) {
	// The same block position appears three times, as it would when a
	// merged profile combines more than one test run touching the same
	// package indirectly. It must be counted once towards Total, and
	// Covered if ANY occurrence had a nonzero count -- here the middle
	// occurrence does.
	body := `mode: atomic
example.com/mod/pkg/a.go:1.1,2.2 4 0
example.com/mod/pkg/a.go:1.1,2.2 4 7
example.com/mod/pkg/a.go:1.1,2.2 4 0
`
	cov := mustParseProfile(t, body)
	pkg := cov["example.com/mod/pkg"]
	if pkg.Total != 4 {
		t.Fatalf("Total = %d, want 4 (the block counted once, not three times)", pkg.Total)
	}
	if pkg.Covered != 4 {
		t.Fatalf("Covered = %d, want 4 (covered because one occurrence had count > 0)", pkg.Covered)
	}
}

func TestParseProfile_BlockDedupeNeverCovered(t *testing.T) {
	body := `mode: set
example.com/mod/pkg/a.go:1.1,2.2 4 0
example.com/mod/pkg/a.go:1.1,2.2 4 0
`
	cov := mustParseProfile(t, body)
	pkg := cov["example.com/mod/pkg"]
	if pkg.Total != 4 || pkg.Covered != 0 {
		t.Fatalf("got Total=%d Covered=%d, want Total=4 Covered=0", pkg.Total, pkg.Covered)
	}
}

func TestParseProfile_MalformedLineErrors(t *testing.T) {
	// Each malformed line sits alongside one well-formed line, so a
	// defect that silently skips the bad line instead of rejecting it
	// cannot hide behind the separate "no data in the profile" check --
	// the profile below always has data.
	const good = "example.com/mod/pkg/good.go:1.1,2.2 1 1\n"
	cases := map[string]string{
		"no mode header": good,
		"garbage line": "mode: atomic\n" + good + `this is not a coverage line
`,
		"missing count field": "mode: atomic\n" + good + `example.com/mod/pkg/a.go:1.1,2.2 4
`,
		"non-numeric statement count": "mode: atomic\n" + good + `example.com/mod/pkg/a.go:1.1,2.2 four 0
`,
		"non-numeric hit count": "mode: atomic\n" + good + `example.com/mod/pkg/a.go:1.1,2.2 4 zero
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseProfile(strings.NewReader(body)); err == nil {
				t.Fatalf("ParseProfile(%q): expected an error, got nil", body)
			}
		})
	}
}

func TestParseProfile_StructuralErrors(t *testing.T) {
	cases := map[string]string{
		"empty profile":        "",
		"header only, no data": "mode: atomic\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseProfile(strings.NewReader(body)); err == nil {
				t.Fatalf("ParseProfile(%q): expected an error, got nil", body)
			}
		})
	}
}

func TestParseProfile_InconsistentStatementCount(t *testing.T) {
	// The same block position claiming two different statement counts
	// cannot come from the same source line; that is corruption, not a
	// legitimate merge.
	body := `mode: atomic
example.com/mod/pkg/a.go:1.1,2.2 4 1
example.com/mod/pkg/a.go:1.1,2.2 9 1
`
	if _, err := ParseProfile(strings.NewReader(body)); err == nil {
		t.Fatal("expected an error for inconsistent NumStmt on the same block, got nil")
	}
}

// --- ParseFloors ---

func TestParseFloors_Basic(t *testing.T) {
	body := `# a comment
example.com/mod/pkg 84.9

example.com/mod/other 91.4
`
	fl := mustParseFloors(t, body)
	if len(fl) != 2 {
		t.Fatalf("expected 2 floors, got %d: %+v", len(fl), fl)
	}
	if fl["example.com/mod/pkg"].Percent != 84.9 {
		t.Errorf("pkg floor = %v, want 84.9", fl["example.com/mod/pkg"].Percent)
	}
}

func TestParseFloors_MalformedLineErrors(t *testing.T) {
	cases := map[string]string{
		"no percent field":  "example.com/mod/pkg\n",
		"extra field":       "example.com/mod/pkg 84.9 extra\n",
		"non-numeric floor": "example.com/mod/pkg not-a-number\n",
		"empty file":        "",
		"only comments":     "# nothing here\n\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFloors(strings.NewReader(body)); err == nil {
				t.Fatalf("ParseFloors(%q): expected an error, got nil", body)
			}
		})
	}
}

func TestParseFloors_DuplicatePackageErrors(t *testing.T) {
	body := `example.com/mod/pkg 80.0
example.com/mod/pkg 90.0
`
	if _, err := ParseFloors(strings.NewReader(body)); err == nil {
		t.Fatal("expected an error for a package listed twice, got nil")
	}
}

// --- Check ---

func TestCheck_AtOrAbovePasses(t *testing.T) {
	measured := map[string]Coverage{"pkg": {Total: 100, Covered: 85}}
	floors := map[string]Floor{"pkg": {Package: "pkg", Percent: 85.0}}
	results, problems := Check(measured, floors)
	if len(problems) != 0 {
		t.Fatalf("expected no problems at exactly the floor, got %v", problems)
	}
	if len(results) != 1 || results[0].Got != 85 || results[0].Floor != 85 || !results[0].HasFloor {
		t.Fatalf("unexpected result: %+v", results)
	}

	// Comfortably above also passes.
	measured["pkg"] = Coverage{Total: 100, Covered: 99}
	_, problems = Check(measured, floors)
	if len(problems) != 0 {
		t.Fatalf("expected no problems above the floor, got %v", problems)
	}
}

func TestCheck_BelowFloorFails(t *testing.T) {
	measured := map[string]Coverage{"pkg": {Total: 100, Covered: 80}}
	floors := map[string]Floor{"pkg": {Package: "pkg", Percent: 85.0}}
	_, problems := Check(measured, floors)
	if len(problems) != 1 {
		t.Fatalf("expected exactly 1 problem for a regression, got %v", problems)
	}
	if !strings.Contains(problems[0], "pkg") || !strings.Contains(problems[0], "80.0") || !strings.Contains(problems[0], "85.0") {
		t.Errorf("problem message %q does not name the package and both percentages", problems[0])
	}
}

func TestCheck_StaleFloorFails(t *testing.T) {
	measured := map[string]Coverage{"pkg": {Total: 100, Covered: 90}}
	floors := map[string]Floor{
		"pkg":     {Package: "pkg", Percent: 50.0},
		"removed": {Package: "removed", Percent: 99.0},
	}
	_, problems := Check(measured, floors)
	if len(problems) != 1 {
		t.Fatalf("expected exactly 1 problem for the stale floor, got %v", problems)
	}
	if !strings.Contains(problems[0], "removed") || !strings.Contains(problems[0], "stale") {
		t.Errorf("problem message %q does not name the stale package", problems[0])
	}
}

func TestCheck_NoFloorIsNotAProblem(t *testing.T) {
	// A measured package with no floor (e.g. one that just gained its
	// first test file) is reported, not failed -- adding its floor is a
	// reviewed change to the floors file, not something this gate forces.
	measured := map[string]Coverage{"untracked": {Total: 10, Covered: 1}}
	results, problems := Check(measured, map[string]Floor{})
	if len(problems) != 0 {
		t.Fatalf("expected no problems for an unfloored package, got %v", problems)
	}
	if len(results) != 1 || results[0].HasFloor {
		t.Fatalf("unexpected result: %+v", results)
	}
}

func TestCoveragePercent(t *testing.T) {
	cases := []struct {
		name string
		c    Coverage
		want float64
	}{
		{"zero total", Coverage{}, 0},
		{"half covered", Coverage{Total: 10, Covered: 5}, 50},
		{"fully covered", Coverage{Total: 7, Covered: 7}, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.c.Percent(); got != c.want {
				t.Errorf("Percent() = %v, want %v", got, c.want)
			}
		})
	}
}
