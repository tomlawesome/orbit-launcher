package ui

// Pins #272 Part 1 (note 40872): text the launcher did not write reaches
// the screen as visibleText, never raw. Control and format runes show as
// Go escapes (\r, \x1b, \u202e) and nothing but the LF between rows
// reaches the terminal. The sites are:
//
//   - the stale-volume screen: the volume name and the Compose project;
//   - the four error-text screens: an Install, Update or Repair run error
//     (engineRun's failed screen), Remove's stand-down error, Repair's
//     run error, and the unreadable-deployment screen;
//   - the console's event line: component, reason and an unknown state;
//   - Repair's plan: an unknown action word.
//
// An ordinary value reads as it did, and a long ordinary error still
// wraps at its limit and is shown whole. The engine's progress lines
// (cleanLine) are not touched.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

var screenEscapeWidths = []int{80, 120}

const (
	screenEscapeHeight = 40

	screenEscapeCtl     = "\x1b[2J\rEVIL"
	screenEscapeCtlText = `\x1b[2J\rEVIL`
	screenEscapeFmt     = "\u202eEVIL"
	screenEscapeFmtText = `\u202eEVIL`

	screenEscapeOrdinaryError = "open /srv/orbit/.env-orbit: permission denied"
)

// screenEscapeSite is one place a foreign value is shown. base is the
// ordinary value, tok its last whitespace-free word (the word an appended
// suffix lands on, so a wrapped row cannot split the suffix), and draw
// shows value at width and returns the screen with escapes stripped.
type screenEscapeSite struct {
	name  string
	base  string
	tok   string
	draw  func(t *testing.T, value string, width int) string
	isErr bool // an error-text screen, which also wraps (acceptance d)

	// noCR marks a value the engine's line parser splits at a CR, so a
	// CR cannot reach the screen at this site; ESC alone is used there.
	noCR bool
}

// ctl is the control-rune suffix for the site, and how it must read on
// the screen.
func (s screenEscapeSite) ctl() (raw, text string) {
	if s.noCR {
		return "\x1b[2JEVIL", `\x1b[2JEVIL`
	}
	return screenEscapeCtl, screenEscapeCtlText
}

func screenEscapeSites() []screenEscapeSite {
	sites := []screenEscapeSite{
		{
			name: "stale volume name", base: "orbit_postgres-data", tok: "orbit_postgres-data",
			draw: func(t *testing.T, v string, width int) string {
				return screenEscapeStale(t, deploy.DatabaseVolume{Name: v, Project: "orbit"}, width)
			},
		},
		{
			name: "stale volume project", base: "orbit", tok: "orbit",
			draw: func(t *testing.T, v string, width int) string {
				return screenEscapeStale(t, deploy.DatabaseVolume{Name: "orbit_postgres-data", Project: v}, width)
			},
		},
		{
			name: "console component", base: "postgres", tok: "postgres",
			draw: func(t *testing.T, v string, width int) string {
				return screenEscapeConsole(ev("database", v, "running", "database-auth-migration", "check"), width)
			},
		},
		{
			name: "console reason", base: "port 5432 in use", tok: "use",
			draw: func(t *testing.T, v string, width int) string {
				return screenEscapeConsole(ev("database", "postgres", "failed", v, "check"), width)
			},
		},
		{
			name: "console unknown state", base: "oscillating", tok: "oscillating",
			draw: func(t *testing.T, v string, width int) string {
				return screenEscapeConsole(ev("database", "postgres", v, "database-auth-migration", "check"), width)
			},
		},
		{
			name: "repair unknown action", base: "future-action", tok: "future-action", noCR: true,
			draw: func(t *testing.T, v string, width int) string {
				m := NewRepairModel(t.TempDir(), "v0.6.0")
				m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: screenEscapeHeight})
				m = repairLines(t, m, "plan action="+v+" resolves=migration-failed mutation=reversible backup=not-required")
				m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4})
				return screen(m)
			},
		},
	}
	for _, action := range []string{"install", "update", "repair"} {
		sites = append(sites, screenEscapeSite{
			name: action + " engine run error", base: screenEscapeOrdinaryError, tok: "denied", isErr: true,
			draw: func(t *testing.T, v string, width int) string {
				r := startedRun(t, action, engineRunSeams{})
				r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New(v)})
				if r.state != runFailed {
					t.Fatalf("%s run state = %v, want the failed screen", action, r.state)
				}
				return ansi.Strip(r.view(width, screenEscapeHeight))
			},
		})
	}
	sites = append(sites,
		screenEscapeSite{
			name: "remove stand-down error", base: screenEscapeOrdinaryError, tok: "denied", isErr: true,
			draw: screenEscapeRemoveFailed,
		},
		screenEscapeSite{
			name: "repair run error", base: screenEscapeOrdinaryError, tok: "denied", isErr: true,
			draw: func(t *testing.T, v string, width int) string {
				m := NewRepairModel(t.TempDir(), "v0.6.0")
				m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: screenEscapeHeight})
				m, _ = updateAs(t, m, repairReadyMsg{err: errors.New(v)})
				if m.state != repairError {
					t.Fatalf("repair state = %v, want the error screen", m.state)
				}
				return screen(m)
			},
		},
		screenEscapeSite{
			name: "unreadable deployment", base: screenEscapeOrdinaryError, tok: "denied", isErr: true,
			draw: func(t *testing.T, v string, width int) string {
				return ansi.Strip(viewUnreadableDeployment(newSky(width, screenEscapeHeight), width, screenEscapeHeight, "/opt/orbit", errors.New(v)))
			},
		},
	)
	return sites
}

// screenEscapeRemoveFailed shows Remove's failed screen for a stand-down
// that returned the error v.
func screenEscapeRemoveFailed(t *testing.T, v string, width int) string {
	t.Helper()
	m := NewRemoveModel(&deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"})
	m.standDown = func(context.Context, string) error { return errors.New(v) }
	m.clipboard = io.Discard
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: screenEscapeHeight})
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("confirming ran nothing")
	}
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateFailed {
		t.Fatalf("remove state = %v, want the failed screen", m.state)
	}
	return screen(m)
}

// screenEscapeStale shows Install's stale-volume screen for one volume.
func screenEscapeStale(t *testing.T, vol deploy.DatabaseVolume, width int) string {
	t.Helper()
	m := NewInstallModel("/opt/orbit", "v0.1.0")
	m.checkVolumes = staleVolumeCheck(vol)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: screenEscapeHeight}, m.Init()())
	if m.state != installStateStaleVolume {
		t.Fatalf("install state = %v, want the stale-volume screen", m.state)
	}
	return screen(m)
}

// screenEscapeConsole shows the console holding one engine event.
func screenEscapeConsole(e engine.EventMsg, width int) string {
	c := testConsole().setSize(width, 26)
	c = c.observeEvent(e.Event)
	return ansi.Strip(c.view(width, 26))
}

// screenEscapeWordsInOrder reports whether the whitespace-separated words of text
// appear, in order, among the words of s (the starfield, carets and box
// edges between them do not matter, and a wrap may move the words to
// another row).
func screenEscapeWordsInOrder(s, text string) bool {
	have := strings.Fields(s)
	i := 0
	for _, want := range strings.Fields(text) {
		for i < len(have) && have[i] != want {
			i++
		}
		if i == len(have) {
			return false
		}
		i++
	}
	return true
}

// a. A value holding ESC and CR (ESC alone where a CR cannot get through
// the engine's line parser) shows as text and nothing else reaches
// the terminal, at both widths, for every site.
func TestScreenEscape_ControlRunesShowAsEscapes(t *testing.T) {
	for _, site := range screenEscapeSites() {
		for _, width := range screenEscapeWidths {
			t.Run(fmt.Sprintf("%s/%d", site.name, width), func(t *testing.T) {
				raw, text := site.ctl()
				want := site.tok + text
				got := site.draw(t, site.base+raw, width)
				if r := identityStray(got); r != 0 {
					t.Errorf("%s at %d columns: %U reached the screen:\n%q", site.name, width, r, got)
				}
				if !strings.Contains(got, want) {
					t.Errorf("%s at %d columns: screen lacks the escaped text %s:\n%q", site.name, width, want, got)
				}
			})
		}
	}
}

// b. A format rune (right-to-left override) shows as its escape text.
func TestScreenEscape_FormatRuneShowsAsEscape(t *testing.T) {
	for _, site := range screenEscapeSites() {
		for _, width := range screenEscapeWidths {
			t.Run(fmt.Sprintf("%s/%d", site.name, width), func(t *testing.T) {
				want := site.tok + screenEscapeFmtText
				got := site.draw(t, site.base+screenEscapeFmt, width)
				if r := identityStray(got); r != 0 {
					t.Errorf("%s at %d columns: %U reached the screen:\n%q", site.name, width, r, got)
				}
				if !strings.Contains(got, want) {
					t.Errorf("%s at %d columns: screen lacks the escaped text %s:\n%q", site.name, width, want, got)
				}
			})
		}
	}
}

// c. An ordinary value reads exactly as before: its words, whole and in
// order, and no escape text.
func TestScreenEscape_OrdinaryValueIsUnchanged(t *testing.T) {
	for _, site := range screenEscapeSites() {
		for _, width := range screenEscapeWidths {
			t.Run(fmt.Sprintf("%s/%d", site.name, width), func(t *testing.T) {
				got := site.draw(t, site.base, width)
				if !screenEscapeWordsInOrder(got, site.base) {
					t.Errorf("%s at %d columns: screen lacks %q whole and in order:\n%s", site.name, width, site.base, got)
				}
				if strings.Contains(got, `\x`) || strings.Contains(got, `\u`) || strings.Contains(got, `\r`) {
					t.Errorf("%s at %d columns: an ordinary value gained escape text:\n%s", site.name, width, got)
				}
				if r := identityStray(got); r != 0 {
					t.Errorf("%s at %d columns: %U on the screen:\n%q", site.name, width, r, got)
				}
			})
		}
	}
}

// The stale-volume screen with a name and a project both ordinary shows
// the project label as well as the name (guards the project site above).
func TestScreenEscape_OrdinaryStaleVolumeShowsNameAndProject(t *testing.T) {
	for _, width := range screenEscapeWidths {
		got := screenEscapeStale(t, deploy.DatabaseVolume{Name: "orbit_postgres-data", Project: "orbit"}, width)
		for _, want := range []string{"orbit_postgres-data", "orbit", "Continue anyway"} {
			if !strings.Contains(got, want) {
				t.Errorf("stale-volume screen at %d columns lacks %q:\n%s", width, want, got)
			}
		}
	}
}

// The console's ordinary failed line reads as before.
func TestScreenEscape_OrdinaryConsoleFailedLineIsUnchanged(t *testing.T) {
	for _, width := range screenEscapeWidths {
		got := screenEscapeConsole(ev("database", "postgres", "failed", "port 5432 in use", "check"), width)
		if !strings.Contains(got, "postgres failed — port 5432 in use") {
			t.Errorf("console at %d columns: want the failed line as before:\n%s", width, got)
		}
	}
}

// d. Error text still wraps at its limit, and a long ordinary error is
// still shown whole: every word in order, over more than one row, and no
// row wider than the screen.
func TestScreenEscape_LongErrorStillWrapsAndShowsWhole(t *testing.T) {
	words := make([]string, 24)
	for i := range words {
		words[i] = "alpha" + string(rune('a'+i%26)) + "x"
	}
	long := "could not finish: " + strings.Join(words, " ")
	for _, site := range screenEscapeSites() {
		if !site.isErr {
			continue
		}
		for _, width := range screenEscapeWidths {
			t.Run(fmt.Sprintf("%s/%d", site.name, width), func(t *testing.T) {
				got := site.draw(t, long, width)
				if !screenEscapeWordsInOrder(got, long) {
					t.Errorf("%s at %d columns: the long error is not shown whole:\n%s", site.name, width, got)
				}
				rows := 0
				for _, row := range strings.Split(got, "\n") {
					if w := visibleWidth(row); w > width {
						t.Errorf("%s at %d columns: a row is %d cells wide:\n%q", site.name, width, w, row)
					}
					if strings.Contains(row, "alpha") {
						rows++
					}
				}
				if rows < 2 {
					t.Errorf("%s at %d columns: the long error sits on %d row(s), want it wrapped:\n%s", site.name, width, rows, got)
				}
			})
		}
	}
}
