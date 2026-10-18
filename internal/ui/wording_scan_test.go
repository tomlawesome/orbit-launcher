package ui

// Pins #230 (wording, #200 C8; owner decision 11a): failure titles in
// one grammar, contractions in on-screen prose, one middle-dot spacing
// written once as a style constant, one spelling, one term for the
// deployment's directory. Wording that applies everywhere is pinned as
// a scan of every non-test Go file under internal/ui (go/parser string
// literals, so comments and identifiers never trip a rule); the
// screens are then checked by what a person reads. Each detector has a
// positive control proving it can see the pattern it forbids.

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

type wordingLiteral struct {
	pos   string
	value string
}

// wordingLiterals returns every string literal in the UI's non-test
// source (import paths excluded), keyed with its position and the
// package-relative file slash path.
func wordingLiterals(t *testing.T) []struct {
	file string
	wordingLiteral
} {
	t.Helper()
	var out []struct {
		file string
		wordingLiteral
	}
	fset := token.NewFileSet()
	for _, path := range uiSourceFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if _, isImport := n.(*ast.ImportSpec); isImport {
				return false
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				v = lit.Value
			}
			out = append(out, struct {
				file string
				wordingLiteral
			}{strings.ReplaceAll(path, "\\", "/"), wordingLiteral{fset.Position(lit.Pos()).String(), v}})
			return true
		})
	}
	return out
}

// wordingSpelledOut are the spelled-out forms the owner's rule turns
// into contractions in on-screen prose and titles (will not -> won't,
// cannot -> can't, could not -> couldn't). "is not" and "has not" are
// left out on purpose: the owner-approved notice copy uses "is not".
// Errors from deploy are shown verbatim and live in another
// package, so they are not in this scan.
var wordingSpelledOut = []string{"will not", "cannot", "could not"}

// wordingAllowedCannot is the one warning that keeps its weight (remove's
// "it cannot be undone").
const wordingAllowedCannot = "it cannot be undone"

func spelledOutForms(lit string) []string {
	l := strings.ToLower(lit)
	l = strings.ReplaceAll(l, wordingAllowedCannot, "")
	var hits []string
	for _, f := range wordingSpelledOut {
		if strings.Contains(l, f) {
			hits = append(hits, f)
		}
	}
	return hits
}

func TestWording_ContractionDetectorSeesWhatItForbids(t *testing.T) {
	for _, bad := range []string{"The installer will not start", "Name cannot be empty", "Could not stand down Orbit", "Orbit will not answer"} {
		if len(spelledOutForms(bad)) == 0 {
			t.Errorf("detector missed %q", bad)
		}
	}
	for _, ok := range []string{"won't start", "can't be empty", "Stand-down stopped", "Removing it cannot be undone", "isn't ready", "upgrading it is not guaranteed"} {
		if h := spelledOutForms(ok); len(h) != 0 {
			t.Errorf("detector flagged %q: %v", ok, h)
		}
	}
}

// On-screen prose and titles use contractions; the sign-in and notice
// copy already do. No string literal in the UI spells them out, except
// the one warning that keeps its weight.
func TestWording_NoSpelledOutNegativesOnScreen(t *testing.T) {
	for _, l := range wordingLiterals(t) {
		if hits := spelledOutForms(l.value); len(hits) > 0 {
			t.Errorf("%s: %q spells out %v; write the contraction (won't, can't, couldn't)", l.pos, l.value, hits)
		}
	}
}

// Failure titles use one grammar, "<Flow word> stopped". The old
// spellings must not survive anywhere in the UI source.
func TestWording_NoOldFailureTitleInSource(t *testing.T) {
	old := []string{"Installation stopped", "Could not stand down", "couldn't run"}
	for _, l := range wordingLiterals(t) {
		for _, o := range old {
			if strings.Contains(l.value, o) {
				t.Errorf("%s: %q still uses the old failure-title grammar (%q)", l.pos, l.value, o)
			}
		}
	}
}

// One spelling (British, as everywhere else on screen) and one term for
// the deployment's directory. Orbit's own quoted engine message lives in
// internal/deploy and is out of this scan.
func TestWording_OneSpellingAndOneTermForTheDirectory(t *testing.T) {
	forbidden := map[string]string{
		"recogniz":         "write recognis... (British spelling on screen)",
		"target directory": `write "deployment directory" (identifiers keep targetDir)`,
	}
	for _, l := range wordingLiterals(t) {
		low := strings.ToLower(l.value)
		for f, why := range forbidden {
			if strings.Contains(low, f) {
				t.Errorf("%s: %q contains %q: %s", l.pos, l.value, f, why)
			}
		}
	}
}

// --- middle dot ---------------------------------------------------------------

var dotTwoSpaces = regexp.MustCompile(`\s{2,}·|·\s{2,}`)

// The separator is one space either side, written once as a style
// constant: no literal anywhere in the UI pads the dot with two spaces,
// no literal outside package style spells " · " itself (they use the
// constant), and package style spells it exactly once.
func TestWording_MiddleDotIsOneSpaceWrittenOnceInStyle(t *testing.T) {
	inStyle := 0
	for _, l := range wordingLiterals(t) {
		if dotTwoSpaces.MatchString(l.value) {
			t.Errorf("%s: %q pads the middle dot with two spaces; one space everywhere", l.pos, l.value)
		}
		switch {
		case strings.HasPrefix(l.file, "starfield/"):
			// glyph runes for the sky, not a separator
		case strings.HasPrefix(l.file, "style/"):
			if l.value == " · " {
				inStyle++
			}
		default:
			if strings.Contains(l.value, " ·") || strings.Contains(l.value, "· ") {
				t.Errorf("%s: %q spells the separator itself; use the style constant", l.pos, l.value)
			}
		}
	}
	if inStyle != 1 {
		t.Errorf(`package style spells the separator " · " %d times, want exactly 1 (a constant)`, inStyle)
	}
}

func TestWording_MiddleDotDetectorSeesTwoSpaces(t *testing.T) {
	if !dotTwoSpaces.MatchString("Standard  ·  recommended") || !dotTwoSpaces.MatchString("a ·  b") {
		t.Fatal("detector missed a two-space dot")
	}
	if dotTwoSpaces.MatchString("15 checked · 0 skipped") {
		t.Fatal("detector flagged a one-space dot")
	}
}

// The two screens that moved: the profile rows and the stale-volume
// line read with a single space round the dot. (Exact rows, not a
// pattern: the sky draws its own dots round the text.)
func TestWording_ProfileRowsAndVolumeLineUseOneSpaceDot(t *testing.T) {
	check := func(name, s string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: missing %q:\n%s", name, w, s)
			}
			if old := strings.Replace(w, " · ", "  ·  ", 1); strings.Contains(s, old) {
				t.Errorf("%s: still pads the dot with two spaces: %q", name, old)
			}
		}
	}

	m, _ := newTestInstallModel(engineRunSeams{})
	check("profile rows", screen(m),
		"Standard · mail, documents, calendar",
		"AI · adds a local model for search & suggestions",
		"Full · AI features plus every optional service")

	v := NewInstallModel("/opt/orbit", "v0.1.0")
	v.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "old-tree_orbit-db-data", Project: "old-tree"})
	updated, _ := v.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ = updated.Update(v.Init()())
	check("volume line", screen(updated), "old-tree_orbit-db-data · Compose project old-tree")
}

// --- failure titles on the screens --------------------------------------------

// Every flow's stop screen says "<Flow word> stopped" as its title.
func TestWording_FailureTitlesAreFlowWordStopped(t *testing.T) {
	// The sky draws dots beside the title, so the title is found as
	// whole words on a line, not as the line's end.
	titleOn := func(s, title string) bool {
		return regexp.MustCompile(`(^|\s)` + regexp.QuoteMeta(title) + `(\s|$)`).MatchString(s)
	}
	cases := []struct {
		name, screen, title string
		not                 []string
	}{
		{"install", func() string {
			r := startedRun(t, "install", engineRunSeams{})
			r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New("fetch install.sh: timed out")})
			return runScreen(r)
		}(), "Install stopped", []string{"Installation stopped"}},
		{"update", func() string {
			r := startedRun(t, "update", engineRunSeams{})
			r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New("fetch install.sh: timed out")})
			return runScreen(r)
		}(), "Update stopped", []string{"Installation stopped", "Install stopped"}},
		{"stand-down", func() string {
			m := newTestRemoveModel(func(context.Context, string) error { return errors.New("docker daemon not running") })
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m, _ = updateAs(t, m, cmd())
			return screen(m)
		}(), "Stand-down stopped", []string{"Could not stand down Orbit"}},
		{"diagnosis", func() string {
			m, _ := updateAs(t, sizedRepair(t), repairReadyMsg{err: errors.New("fetch repair.sh: connection refused")})
			return screen(m)
		}(), "Diagnosis stopped", []string{"Diagnosis couldn't run", "Could not"}},
		{"repairs", func() string {
			m := sizedRepair(t)
			m.state = repairExecuting
			m.stream = sleepingStream(t)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})
			return screen(m)
		}(), "Repairs stopped", []string{"Diagnosis stopped"}},
		{"credential rotation", func() string {
			m := sizedRepair(t)
			m.state = repairRotating
			m.stream = sleepingStream(t)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})
			return screen(m)
		}(), "Credential rotation stopped", []string{"Diagnosis stopped"}},
	}
	for _, c := range cases {
		if !titleOn(c.screen, c.title) {
			t.Errorf("%s: no line ends with the title %q:\n%s", c.name, c.title, c.screen)
		}
		for _, n := range c.not {
			if strings.Contains(c.screen, n) {
				t.Errorf("%s: still shows %q:\n%s", c.name, n, c.screen)
			}
		}
	}
}

// "it cannot be undone" is the one spelled-out form that stays.
func TestWording_RemoveWarningKeepsItsWeight(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error { return nil })
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m, _ = updateAs(t, m, cmd())
	if s := screen(m); !strings.Contains(s, "it cannot be undone") {
		t.Errorf("the stood-down screen lost its warning:\n%s", s)
	}
}

// The directory is "deployment directory" on a repair finding line.
func TestWording_FindingLineSaysDeploymentDirectory(t *testing.T) {
	got := findingLine(engine.Finding{Class: "not-orbit-directory", Target: "directory", Severity: "warn"})
	s := strings.ToLower(ansi.Strip(got))
	if !strings.Contains(s, "deployment directory") || strings.Contains(s, "target directory") {
		t.Errorf("finding line = %q, want deployment directory and no target directory", s)
	}
	if !strings.Contains(s, "no recognisable orbit installation") {
		t.Errorf("finding line = %q, want the British spelling", s)
	}
}
