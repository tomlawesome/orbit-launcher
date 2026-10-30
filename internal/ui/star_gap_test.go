package ui

// Pins #270: no sky star is drawn in the cell directly before or after a
// content line's text. A star (`.`, `·`, `✦`, `●`) touching the end of a
// line reads as part of it -- `.../with several/long·` looked like part
// of the path Install was about to use. The sky must leave a clear cell
// on each side of every line of text, on every screen drawn over it.
//
// How content is told from sky, with no list of words to go stale: each
// screen is drawn twice at the same moment, once as it is and once with
// every star taken out of the sky. Every non-space cell of the second
// drawing is content (text, the caret, the mark, box lines). In the first
// drawing, the cell just before and just after each run of content cells
// must be a space.
//
// The sky turns slowly, so each screen is drawn at many ticks and at
// several heights: the stars land in different cells each time, and one
// is often next to the text in at least some of the frames.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
)

const sgLongDir = "/srv/containers/mail stack/orbit production/with several/long directories/under the data volume/orbit"

var (
	sgWidths  = []int{50, 80, 120}
	sgHeights = []int{24, 30, 40}
)

// sgTickStride is how far the sky is turned between frames, and sgFrames
// how many frames are taken: enough to cross many of its synchronised
// steps.
const (
	sgTickStride = 3
	sgFrames     = 140
)

// sgSkyGlyphs are what the sky draws, used only to count how many stars a
// run of frames held (never to decide what is content). Counting the
// star-free drawing's own glyphs off leaves the stars alone.
const sgSkyGlyphs = ".·✦●"

// sgGlyphs counts the cells of s holding a sky glyph.
func sgGlyphs(s string) int {
	n := 0
	for _, r := range s {
		if strings.ContainsRune(sgSkyGlyphs, r) {
			n++
		}
	}
	return n
}

// sgStarless empties the sky of every star, in whatever model or nested
// run holds one, keeping its size and clock so the layout does not move.
// It works on a copy of the model: only struct fields are entered, so
// nothing the copy shares with the original is written to.
func sgStarless(v reflect.Value) {
	if v.Type() == reflect.TypeOf(starfield.Model{}) {
		*(*starfield.Model)(unsafe.Pointer(v.UnsafeAddr())) = starfield.Model{}
		return
	}
	if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			sgStarless(v.Field(i))
		}
	}
}

// sgGaps compares one screen drawn with its sky (normal) against the same
// screen with no stars (bare), both stripped of escapes. It returns each
// place a star touches text, and any ways the two drawings disagree about
// content (which would make the comparison worthless).
func sgGaps(normal, bare string) (touch, broken []string) {
	nrows, brows := strings.Split(normal, "\n"), strings.Split(bare, "\n")
	if len(nrows) != len(brows) {
		return nil, []string{fmt.Sprintf("the drawings have %d and %d rows", len(nrows), len(brows))}
	}
	for y := range brows {
		n, b := []rune(nrows[y]), []rune(brows[y])
		if len(n) != len(b) {
			broken = append(broken, fmt.Sprintf("row %d is %d cells with stars and %d without", y, len(n), len(b)))
			continue
		}
		for x := 0; x < len(b); x++ {
			if b[x] == ' ' {
				continue
			}
			if n[x] != b[x] {
				broken = append(broken, fmt.Sprintf("row %d col %d: content %q is %q with stars", y, x, b[x], n[x]))
			}
		}
		for x := 0; x < len(b); x++ {
			if b[x] == ' ' || (x > 0 && b[x-1] != ' ') {
				continue // not the first cell of a run of content
			}
			end := x
			for end+1 < len(b) && b[end+1] != ' ' {
				end++
			}
			if x > 0 && n[x-1] != ' ' {
				touch = append(touch, fmt.Sprintf("row %d: %q sits right after the text starting at col %d: %s", y, n[x-1], x, strings.TrimRight(nrows[y], " ")))
			}
			if end+1 < len(n) && n[end+1] != ' ' {
				touch = append(touch, fmt.Sprintf("row %d: %q sits right after the text ending at col %d: %s", y, n[end+1], end, strings.TrimRight(nrows[y], " ")))
			}
			x = end
		}
	}
	return touch, broken
}

// sgScreen is one real screen: how to draw it at a size, as it is and
// with no stars, and how to turn its sky. hasSky is false for a screen
// that is drawn on a bare terminal.
type sgScreen struct {
	name   string
	hasSky bool
	build  func(t *testing.T, w, h int) (view, bare func() string, tick func())
}

// sgModel wraps a bubbletea model: sized, set up by prep, viewed as plain
// text, ticked by the sky's own message.
func sgModel[T tea.Model](m T, w, h int, prep func(*T)) (view, bare func() string, tick func()) {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = updated.(T)
	if prep != nil {
		prep(&m)
	}
	view = func() string { return ansi.Strip(m.View().Content) }
	bare = func() string {
		c := m
		sgStarless(reflect.ValueOf(&c).Elem())
		return ansi.Strip(c.View().Content)
	}
	tick = func() {
		next, _ := m.Update(tickMsg{})
		m = next.(T)
	}
	return view, bare, tick
}

func sgDeployment() *deploy.Deployment {
	return &deploy.Deployment{TargetDir: sgLongDir, AppURL: "https://mail.example.com", Image: "registry.example.com/orbit:1.2.3"}
}

func sgScreens() []sgScreen {
	return []sgScreen{
		{
			name: "InstallProfile", hasSky: true,
			build: func(t *testing.T, w, h int) (func() string, func() string, func()) {
				return sgModel(NewInstallModel(sgLongDir, "v1"), w, h, nil)
			},
		},
		{
			name: "InstallNotice", hasSky: false, // the notice fills a bare terminal
			build: func(t *testing.T, w, h int) (func() string, func() string, func()) {
				clock := newFakeClock()
				m := NewInstallModel(sgLongDir, "v1")
				m.seams = engineRunSeams{now: clock.now}
				return sgModel(m, w, h, func(m *InstallModel) {
					next, _ := m.Update(key(tea.KeyEnter)) // Standard -> notice
					*m = next.(InstallModel)
					if m.state != installStateNotice {
						t.Fatalf("Enter on Standard: state = %v, want the notice", m.state)
					}
				})
			},
		},
		{
			name: "UpdateConfirm", hasSky: true,
			build: func(t *testing.T, w, h int) (func() string, func() string, func()) {
				return sgModel(NewUpdateModel(sgDeployment(), sgLongDir, "v1"), w, h, func(m *UpdateModel) { m.state = updateStateConfirm })
			},
		},
		{
			name: "RemoveConfirm", hasSky: true,
			build: func(t *testing.T, w, h int) (func() string, func() string, func()) {
				return sgModel(NewRemoveModel(sgDeployment()), w, h, nil)
			},
		},
		{
			name: "ConfigurationPrompt", hasSky: true,
			build: func(t *testing.T, w, h int) (func() string, func() string, func()) {
				r := newEngineRun("install", t.TempDir(), "Install — Standard", "v9.9.9")
				r.width, r.height = w, h
				r.console = newConsole("Install", "v9.9.9", time.Now).setSize(w, h)
				r.state = runConfigCollect
				r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt field=APP_URL kind=url required=true attempt=1"}})
				return func() string { return ansi.Strip(r.view(w, h)) },
					func() string {
						c := r
						sgStarless(reflect.ValueOf(&c).Elem())
						return ansi.Strip(c.view(w, h))
					},
					func() { r, _ = r.update(tickMsg{}) }
			},
		},
		{
			name: "RepairDiagnosis", hasSky: true,
			build: func(t *testing.T, w, h int) (func() string, func() string, func()) {
				return sgModel(NewRepairModel("/opt/orbit", "v1"), w, h, func(m *RepairModel) {
					m.state = repairDiagnosis
					m.diagnosis = &engine.Diagnosis{Result: engine.ResultAttention, Checked: 12, Skipped: 1}
					m.findings = []engine.Finding{{Class: "secret-missing", Target: "compose-file", Severity: "fail"}}
					m.planActions = []engine.PlanAction{{Action: "fix-permissions", Resolves: "secret-missing", Mutation: "reversible", Backup: "not-required"}}
					m.planSummary = &engine.PlanSummary{Result: "ready", Actions: 1}
					m.exitCode = engine.ExitPlanAvailable
				})
			},
		},
		{
			name: "RepairError", hasSky: true,
			build: func(t *testing.T, w, h int) (func() string, func() string, func()) {
				return sgModel(NewRepairModel(sgLongDir, "v1"), w, h, func(m *RepairModel) {
					m.state = repairError
					m.runErr = fmt.Errorf("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. " +
						"Is the docker daemon running? permission denied while trying to connect to the docker API")
				})
			},
		},
	}
}

// sgReport keeps a failing case readable: the first few touches, and how
// many there were.
func sgReport(t *testing.T, label string, found []string) {
	t.Helper()
	if len(found) == 0 {
		return
	}
	shown := found
	if len(shown) > 2 {
		shown = shown[:2]
	}
	t.Errorf("%s: %d place(s) where a star touches text; first:\n  %s", label, len(found), strings.Join(shown, "\n  "))
}

func TestStarGap_ScreensLeaveAClearCellBesideText(t *testing.T) {
	for _, s := range sgScreens() {
		for _, w := range sgWidths {
			t.Run(fmt.Sprintf("%s/%d", s.name, w), func(t *testing.T) {
				t.Parallel() // each case builds its own models and shares nothing
				var touches []string
				starsSeen, contentSeen := 0, 0
				for _, h := range sgHeights {
					view, bare, tick := s.build(t, w, h)
					for f := 0; f < sgFrames; f++ {
						normal, empty := view(), bare()
						touch, broken := sgGaps(normal, empty)
						if len(broken) > 0 {
							t.Fatalf("height %d frame %d: the star-free drawing is not the same screen (%d differences), first: %s", h, f, len(broken), broken[0])
						}
						for _, x := range touch {
							touches = append(touches, fmt.Sprintf("height %d frame %d %s", h, f, x))
						}
						contentSeen += len(strings.Fields(empty))
						starsSeen += sgGlyphs(normal) - sgGlyphs(empty)
						for i := 0; i < sgTickStride; i++ {
							tick()
						}
					}
				}
				if contentSeen == 0 {
					t.Fatal("the star-free drawing is empty: nothing was checked")
				}
				if s.hasSky && starsSeen < sgFrames {
					t.Fatalf("only %d stars across %d frames: the sky is too thin to prove anything", starsSeen, sgFrames*len(sgHeights))
				}
				sgReport(t, fmt.Sprintf("%s at %d columns", s.name, w), touches)
			})
		}
	}
}

// skyBlock is what every flow screen is drawn with. Whatever the seed or
// the turn of the sky, a block of centred text keeps a clear cell on both
// sides. Several seeds, because each seed puts the stars elsewhere. The
// star-free drawing is the same call with the stars taken out.
func TestStarGap_SkyBlockKeepsAClearCellAnySeedAnyTick(t *testing.T) {
	content := strings.Join([]string{
		"Choose a deployment profile",
		"Orbit installs into /srv/containers/mail/orbit",
		"watch it validate, pull the image, and start the containers",
		"▸ Standard",
		"Cancel",
	}, "\n")
	for _, w := range sgWidths {
		t.Run(fmt.Sprintf("%d", w), func(t *testing.T) {
			t.Parallel() // each width draws its own skies and shares nothing
			var touches []string
			for seed := int64(1); seed <= 20; seed++ {
				star := starfield.New(w, 30, seed)
				for f := 0; f < 60; f++ {
					bareSky := star
					// No stars and no planets: planets are drawn from the size, not
					// from Stars, and must not count as text.
					bareSky.Stars = nil
					bareSky.Width, bareSky.Height = 0, 0
					touch, broken := sgGaps(ansi.Strip(skyBlock(star, w, 30, content)), ansi.Strip(skyBlock(bareSky, w, 30, content)))
					if len(broken) > 0 {
						t.Fatalf("the star-free drawing is not the same screen, first: %s", broken[0])
					}
					for _, x := range touch {
						touches = append(touches, fmt.Sprintf("seed %d frame %d %s", seed, f, x))
					}
					for i := 0; i < sgTickStride; i++ {
						star = star.Advance()
					}
				}
			}
			sgReport(t, fmt.Sprintf("skyBlock at %d columns", w), touches)
		})
	}
}

// The checker itself: it must flag a star fused to either end of a run
// of content and let a one-cell gap, a lone star and a full-width line
// through, or a green result above would mean nothing.
func TestStarGap_CheckerFlagsOnlyAStarTouchingText(t *testing.T) {
	bare := "    the data volume    "
	for _, c := range []struct {
		normal string
		flags  bool
	}{
		{"    the data volume·   ", true},
		{"   ✦the data volume    ", true},
		{"   .the data volume.   ", true},
		{"  · the data volume ●  ", false},
		{"·   the data volume   .", false},
		{"  ·✦                   ", false},
		{"    the data volume    ", false},
	} {
		touch, broken := sgGaps(c.normal, bare)
		if c.normal == "  ·✦                   " {
			// a sky-only row has no content run: compare against a row of spaces
			touch, broken = sgGaps(c.normal, strings.Repeat(" ", len([]rune(c.normal))))
		}
		if len(broken) > 0 {
			t.Errorf("%q: unexpected disagreement %v", c.normal, broken)
		}
		if got := len(touch) > 0; got != c.flags {
			t.Errorf("%q: flagged = %v, want %v", c.normal, got, c.flags)
		}
	}
	// A line that fills the row has no cell outside it to check.
	if touch, broken := sgGaps("full width text", "full width text"); len(touch)+len(broken) != 0 {
		t.Errorf("a full-width line was flagged: %v %v", touch, broken)
	}
	// Content that differs between the drawings is the test's fault, not a pass.
	if _, broken := sgGaps("   x  ", "   y  "); len(broken) == 0 {
		t.Error("a cell of content that changed between the drawings was not reported")
	}
}
