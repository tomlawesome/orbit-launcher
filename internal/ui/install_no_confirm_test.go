package ui

// Pins #248: the "Ready to install" screen is gone. The profile screen
// names the install folder, Enter on Standard opens the development
// notice directly, and the notice's Esc and Back land on the profile
// screen. Everything is read through the rendered screen and key
// handling; the tests step through the flow without naming the retired
// screen's state.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const noConfirmDir = "/srv/orbit-248/stack"

// noConfirmModel is an install model for dir at width×height on a fake
// clock, sitting on the profile screen.
func noConfirmModel(t *testing.T, dir string, width, height int, seams engineRunSeams) (InstallModel, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	seams.now = clock.now
	m := NewInstallModel(dir, "v9.9.9")
	m.seams = seams
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	if m.state != installStateProfile {
		t.Fatalf("state = %v, want the profile screen", m.state)
	}
	return m, clock
}

// noConfirmNotice presses Enter on Standard and requires the notice, with
// the command that press returned.
func noConfirmNotice(t *testing.T, m InstallModel) (InstallModel, tea.Cmd) {
	t.Helper()
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if m.state != installStateNotice {
		t.Fatalf("Enter on Standard: state = %v, want the notice; screen:\n%s", m.state, screen(m))
	}
	return m, cmd
}

// onlyGlyphs are the starfield and caret marks that can share a line with
// prose; a token made only of these is not a word.
var onlyGlyphs = regexp.MustCompile(`^[.·✦●⟡*+'` + "`" + `▸]+$`)

// folderBlock is the words of the screen's folder line, which may wrap:
// from the line holding "Orbit installs into" to the next blank line.
func folderBlock(s string) (words []string, lines []string, ok bool) {
	all := strings.Split(s, "\n")
	for i, line := range all {
		if !strings.Contains(line, "Orbit installs into") {
			continue
		}
		for _, l := range all[i:] {
			if strings.TrimSpace(strings.Trim(l, " .·✦●⟡")) == "" {
				break
			}
			lines = append(lines, l)
			for _, w := range strings.Fields(l) {
				if onlyGlyphs.MatchString(w) {
					continue
				}
				// A sky star can sit in the cell right after a line's
				// last character (compositeScene leaves no gap; #270).
				words = append(words, strings.TrimRight(w, "·✦●⟡"))
			}
		}
		return words, lines, true
	}
	return nil, nil, false
}

func TestNoConfirm_ProfileScreenNamesTheResolvedDirectory(t *testing.T) {
	m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
	s := screen(m)
	if !strings.Contains(s, "Orbit installs into "+noConfirmDir) {
		t.Fatalf("profile screen lacks the labelled folder line:\n%s", s)
	}
	if strings.Contains(s, "/opt/orbit") {
		t.Errorf("profile screen shows a placeholder directory:\n%s", s)
	}
}

func TestNoConfirm_ProfileScreenFolderLineSitsBetweenHeaderAndRows(t *testing.T) {
	m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
	s := screen(m)
	at := func(sub string) int { return strings.Index(s, sub) }
	header, folder, standard, ai, full := at("Choose a deployment profile"), at("Orbit installs into"),
		at("Standard"), at("Agent"), at("Full")
	for name, i := range map[string]int{"header": header, "folder line": folder, "Standard": standard, "Agent": ai, "Full": full} {
		if i < 0 {
			t.Fatalf("profile screen lacks the %s:\n%s", name, s)
		}
	}
	if !(header < folder && folder < standard && standard < ai && ai < full) {
		t.Errorf("order must be header, folder line, rows:\n%s", s)
	}
	if !strings.Contains(s, "▸ Standard") {
		t.Errorf("the caret must start on Standard:\n%s", s)
	}
	// A blank line separates the folder line from the rows.
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.Contains(l, "Standard") && strings.Contains(l, "▸") {
			if i == 0 || strings.TrimSpace(strings.Trim(lines[i-1], " .·✦●⟡")) != "" {
				t.Errorf("a blank line must sit between the folder line and the rows:\n%s", s)
			}
		}
	}
}

func TestNoConfirm_LongDirectoryWrapsAtEveryWidthAndIsNeverCut(t *testing.T) {
	for _, w := range []int{50, 80, 120} {
		t.Run(fmt.Sprintf("%d", w), func(t *testing.T) {
			m, _ := noConfirmModel(t, c9LongDir, w, 30, engineRunSeams{})
			s := screen(m)
			words, lines, ok := folderBlock(s)
			if !ok {
				t.Fatalf("no folder line:\n%s", s)
			}
			want := strings.Fields("Orbit installs into " + c9LongDir)
			if strings.Join(words, " ") != strings.Join(want, " ") {
				t.Errorf("folder line words = %q\nwant %q\nscreen:\n%s", strings.Join(words, " "), strings.Join(want, " "), s)
			}
			for _, l := range strings.Split(s, "\n") {
				if n := utf8.RuneCountInString(l); n > w {
					t.Errorf("a line is %d cells at width %d: %q", n, w, l)
				}
			}
			if w == 50 && len(lines) < 2 {
				t.Errorf("the long path must wrap at 50 columns, folder block = %q", lines)
			}
		})
	}
}

func TestNoConfirm_NoPlaceholderDirectoryAndNoReadyScreenInUISource(t *testing.T) {
	var sawLiterals int
	for _, lit := range wordingLiterals(t) {
		sawLiterals++
		if strings.Contains(lit.value, "/opt/orbit") {
			t.Errorf("%s: placeholder directory in UI source: %q", lit.pos, lit.value)
		}
		if strings.Contains(strings.ToLower(lit.value), "ready to install") {
			t.Errorf("%s: the retired screen's title is still in UI source: %q", lit.pos, lit.value)
		}
	}
	if sawLiterals == 0 {
		t.Fatal("the scan saw no string literals, so it proves nothing")
	}
}

func TestNoConfirm_EnterOnStandardOpensTheNoticeAtItsStartWithoutTouchingTheEngine(t *testing.T) {
	engineCalled := false
	m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineCalled = true
			return nil, nil, errors.New("must not be called")
		},
	})
	m, cmd := noConfirmNotice(t, m)
	s := screen(m)
	for _, want := range []string{"A note before you install", noticeTitle, "0:20"} {
		if !strings.Contains(s, want) {
			t.Errorf("notice lacks %q:\n%s", want, s)
		}
	}
	if cmd == nil {
		t.Fatal("opening the notice must arm its countdown tick")
	}
	tick, ok := cmd().(noticeTickMsg)
	if !ok {
		t.Fatalf("the command yielded %T, want only the notice's tick", tick)
	}
	if !tick.start.Equal(m.notice.start) {
		t.Errorf("tick start = %v, want the notice's %v", tick.start, m.notice.start)
	}
	if engineCalled {
		t.Error("Enter on Standard must not start or prepare the engine")
	}
	if m.notice.endSeen {
		t.Error("at 80x26 the scroll gate must start closed")
	}
}

func TestNoConfirm_ReadyToInstallAppearsOnNoScreen(t *testing.T) {
	stale := NewInstallModel(noConfirmDir, "v9.9.9")
	stale.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})
	stale, _ = updateAs(t, stale, tea.WindowSizeMsg{Width: 80, Height: 26}, stale.Init()())
	if stale.state != installStateStaleVolume {
		t.Fatalf("state = %v, want the stale-volume screen", stale.state)
	}

	profile, clock := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{
		prepareEngine: fakeEngineStreaming(t, ev("host", "host", "completed", "host-tools", "check")),
	})
	unavailable, _ := updateAs(t, profile, key(tea.KeyDown), key(tea.KeyEnter))
	notice, _ := noConfirmNotice(t, profile)
	back, _ := updateAs(t, notice, key(tea.KeyEsc))

	// The run: pass the notice's gates and phrase.
	clock.advance(notice.noticeDur())
	run, _ := updateAs(t, notice, key(tea.KeyEnd), tea.KeyPressMsg{Text: noticePhrase})
	run, _ = updateAs(t, run, key(tea.KeyEnter))

	screens := map[string]string{
		"stale volume": screen(stale), "profile": screen(profile), "unavailable": screen(unavailable),
		"notice": screen(notice), "profile after Back": screen(back), "running": screen(run),
	}
	for name, s := range screens {
		for _, banned := range []string{"Ready to install", "Install now"} {
			if strings.Contains(s, banned) {
				t.Errorf("%s screen shows %q:\n%s", name, banned, s)
			}
		}
	}
	if run.state != installStateRunning {
		t.Errorf("accepting the notice: state = %v, want running", run.state)
	}
}

func TestNoConfirm_EscAndBackOnTheNoticeLandOnTheProfileScreenAndReentryIsFresh(t *testing.T) {
	for _, tc := range []struct {
		name string
		back func(*testing.T, InstallModel) InstallModel
	}{
		{"Esc", func(t *testing.T, m InstallModel) InstallModel {
			m, _ = updateAs(t, m, key(tea.KeyEsc))
			return m
		}},
		{"Enter on Back", func(t *testing.T, m InstallModel) InstallModel {
			if m.notice.focus != noticeFocusBack {
				t.Fatalf("the caret must start on Back, focus = %v", m.notice.focus)
			}
			m, _ = updateAs(t, m, key(tea.KeyEnter))
			return m
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, clock := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
			m, _ = noConfirmNotice(t, m)
			first := m.notice.start

			// Leave the gate open, so re-entry has something to close.
			m, _ = updateAs(t, m, key(tea.KeyEnd))
			if !m.notice.endSeen {
				t.Fatal("End must open the scroll gate")
			}

			m = tc.back(t, m)
			if m.state != installStateProfile {
				t.Fatalf("state = %v, want the profile screen", m.state)
			}
			s := screen(m)
			if !strings.Contains(s, "Choose a deployment profile") || !strings.Contains(s, "▸ Standard") || m.profile.sel != 0 {
				t.Errorf("Standard must still be selected on the profile screen (sel=%d):\n%s", m.profile.sel, s)
			}
			if !strings.Contains(s, "Orbit installs into "+noConfirmDir) {
				t.Errorf("the profile screen must show the folder again:\n%s", s)
			}
			if m.View().MouseMode != tea.MouseModeNone {
				t.Error("mouse reporting is for the notice only")
			}

			clock.advance(30 * time.Second)
			m, cmd := noConfirmNotice(t, m)
			if !m.notice.start.Equal(clock.now()) || m.notice.start.Equal(first) {
				t.Errorf("re-entry start = %v, want a fresh start at %v", m.notice.start, clock.now())
			}
			if !strings.Contains(screen(m), "0:20") {
				t.Errorf("re-entry must restart the countdown at 0:20:\n%s", screen(m))
			}
			if m.notice.endSeen || m.notice.offset != 0 || !m.notice.typed.empty() {
				t.Errorf("re-entry must close the scroll gate and reset the scroll and box (endSeen=%v offset=%d typed=%q)",
					m.notice.endSeen, m.notice.offset, m.notice.typed.String())
			}
			if tick, ok := cmd().(noticeTickMsg); !ok || !tick.start.Equal(m.notice.start) {
				t.Errorf("re-entry tick = %#v, want one carrying the new start", tick)
			}
			if _, cmd := m.Update(noticeTickMsg{start: first}); cmd != nil {
				t.Error("a tick from the earlier visit must be dropped, not re-armed")
			}
			if _, cmd := m.Update(noticeTickMsg{start: m.notice.start}); cmd == nil {
				t.Error("the current visit's tick must re-arm while the countdown runs")
			}
		})
	}
}

func TestNoConfirm_TickFromAVisitThatWasLeftIsDroppedOnTheProfileScreen(t *testing.T) {
	m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
	m, _ = noConfirmNotice(t, m)
	first := m.notice.start
	m, _ = updateAs(t, m, key(tea.KeyEsc))
	if m.state != installStateProfile {
		t.Fatalf("state = %v, want the profile screen", m.state)
	}
	m, cmd := updateAs(t, m, noticeTickMsg{start: first})
	if cmd != nil || m.state != installStateProfile {
		t.Errorf("a late tick must change nothing on the profile screen (cmd=%v, state=%v)", cmd != nil, m.state)
	}
}

func TestNoConfirm_EscOnTheProfileScreenGoesToTheMenu(t *testing.T) {
	m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
	m, cmd := updateAs(t, m, key(tea.KeyEsc))
	if cmd != nil || !leftForMenu(m.Outcome()) {
		t.Fatalf("Esc on the profile screen must go to the menu and run nothing (cmd=%v outcome=%+v)", cmd != nil, m.Outcome())
	}
}

func TestNoConfirm_DocumentsAgentAndFullStillReachTheUnavailableScreenAndBackReturns(t *testing.T) {
	for _, downs := range []int{1, 2, 3} { // Documents, Agent, Full
		for _, back := range []tea.KeyPressMsg{key(tea.KeyEnter), key(tea.KeyEsc)} {
			m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
			for range downs {
				m, _ = updateAs(t, m, key(tea.KeyDown))
			}
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			if cmd != nil || m.state != installStateUnavailableProfile {
				t.Fatalf("%d downs: state = %v, want the unavailable-profile screen, no command", downs, m.state)
			}
			if !strings.Contains(screen(m), "This profile isn't available yet") {
				t.Errorf("unavailable screen:\n%s", screen(m))
			}
			m, _ = updateAs(t, m, back)
			if m.state != installStateProfile || !strings.Contains(screen(m), "Orbit installs into "+noConfirmDir) {
				t.Errorf("%v must return to the profile screen with the folder line, state = %v", back, m.state)
			}
		}
	}
}

func TestNoConfirm_CtrlCQuitsFromTheProfileAndNoticeScreens(t *testing.T) {
	m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
	if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
		t.Error("Ctrl+C on the profile screen did not quit")
	}
	n, _ := noConfirmNotice(t, m)
	if _, cmd := updateAs(t, n, ctrlC()); !isQuit(cmd) {
		t.Error("Ctrl+C on the notice did not quit")
	}
}

func TestNoConfirm_StaleVolumeScreenIsUnchangedAndContinueLeadsToTheProfileThenTheNotice(t *testing.T) {
	stale := func(t *testing.T) InstallModel {
		m := NewInstallModel(noConfirmDir, "v9.9.9")
		m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})
		m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26}, m.Init()())
		if m.state != installStateStaleVolume {
			t.Fatalf("state = %v, want the stale-volume screen", m.state)
		}
		return m
	}

	m := stale(t)
	s := screen(m)
	for _, want := range []string{"orbit-db-data", "Continue anyway", "Cancel"} {
		if !strings.Contains(s, want) {
			t.Errorf("stale-volume screen lacks %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "▸ Continue anyway") {
		t.Errorf("Continue anyway must be preselected:\n%s", s)
	}

	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.state != installStateProfile || !strings.Contains(screen(m), "Orbit installs into "+noConfirmDir) {
		t.Fatalf("Continue anyway must lead to the profile screen with the folder line, state = %v:\n%s", m.state, screen(m))
	}
	m, _ = noConfirmNotice(t, m)
	if !strings.Contains(screen(m), "A note before you install") {
		t.Errorf("the notice must follow the profile screen:\n%s", screen(m))
	}

	c, cmd := updateAs(t, stale(t), key(tea.KeyDown), key(tea.KeyEnter))
	if cmd != nil || !leftForMenu(c.Outcome()) {
		t.Errorf("Cancel must go back to the menu, starting nothing (cmd=%v outcome=%+v)", cmd != nil, c.Outcome())
	}
}

func TestNoConfirm_ALateStaleVolumeFindingNeverInterruptsTheNotice(t *testing.T) {
	m := NewInstallModel(noConfirmDir, "v9.9.9")
	m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	// Enter waits for the check (#267), so a finding can only reach the
	// notice after a clean answer has let the flow through: a stray one
	// must still never pull it back.
	m, _ = updateAs(t, m, staleVolumesMsg{})
	m, _ = noConfirmNotice(t, m)
	m, _ = updateAs(t, m, m.Init()())
	if m.state != installStateNotice {
		t.Errorf("a late pre-flight finding pulled the flow back to state %v", m.state)
	}
}
