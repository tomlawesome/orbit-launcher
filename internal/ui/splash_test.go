package ui

import (
	"context"
	"errors"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// settled skips the arrival, as any keypress would — behaviour and view
// tests exercise the lit room unless they are about the arrival itself.
func settled(m SplashModel) SplashModel { m.introDone = true; return m }

func TestSplashModel_ArrowNavigationWraps(t *testing.T) {
	m := settled(NewSplashModel())

	if m.menu.sel != 0 {
		t.Fatalf("initial selection = %d, want 0", m.menu.sel)
	}

	updated, _ := m.Update(key(tea.KeyUp))
	m = updated.(SplashModel)
	if m.menu.sel != len(MainMenu)-1 {
		t.Errorf("Up from index 0 = %d, want wrap to %d", m.menu.sel, len(MainMenu)-1)
	}

	updated, _ = m.Update(key(tea.KeyDown))
	m = updated.(SplashModel)
	if m.menu.sel != 0 {
		t.Errorf("Down from the last index = %d, want wrap to 0", m.menu.sel)
	}
}

func TestSplashModel_EnterChoosesTheSelectedItemAndQuits(t *testing.T) {
	m := settled(NewSplashModel())
	m.menu.sel = 2 // Repair

	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(SplashModel)

	if m.Chosen != "Repair" {
		t.Errorf("Chosen = %q, want %q", m.Chosen, "Repair")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Enter to issue tea.Quit")
	}
}

// A digit moves the caret to its row and nothing else; Enter acts, as on
// every other menu (#228 C1). A stray digit must never close the
// launcher (#226).
func TestSplashModel_NumberKeyMovesTheCaretOnly(t *testing.T) {
	for i, item := range MainMenu {
		digit := rune('1' + i)
		t.Run(item.Label, func(t *testing.T) {
			m, _ := updateAs(t, settled(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26})
			m, cmd := updateAs(t, m, runeKey(digit))

			if m.menu.sel != i {
				t.Errorf("selected = %d, want %d (%s)", m.menu.sel, i, item.Label)
			}
			if m.Chosen != "" {
				t.Errorf("Chosen = %q after a digit, want empty: Enter acts, not the digit", m.Chosen)
			}
			if m.quitting {
				t.Error("a digit marked the splash as quitting")
			}
			if cmd != nil {
				t.Errorf("a digit issued a command (quit = %v), want none", isQuit(cmd))
			}
			if !m.userNavigated {
				t.Error("a digit is navigation: the health probe must stop moving the caret")
			}
			if !strings.Contains(screen(m), "▸ "+item.Label) {
				t.Errorf("the caret is not drawn on %s:\n%s", item.Label, screen(m))
			}
		})
	}
}

// After a digit, Enter acts on the row the caret is on.
func TestSplashModel_EnterAfterANumberKeyActs(t *testing.T) {
	for i, item := range MainMenu[:len(MainMenu)-1] { // Exit is below
		t.Run(item.Label, func(t *testing.T) {
			m, _ := updateAs(t, settled(NewSplashModel()), runeKey(rune('1'+i)))
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			if m.Chosen != item.Label {
				t.Errorf("Chosen = %q, want %q", m.Chosen, item.Label)
			}
			if !isQuit(cmd) {
				t.Error("Enter on a chosen row should issue tea.Quit (the app reads Chosen)")
			}
		})
	}
	t.Run("Exit", func(t *testing.T) {
		m, _ := updateAs(t, settled(NewSplashModel()), runeKey(rune('0'+len(MainMenu))))
		if m, cmd := updateAs(t, m, key(tea.KeyEnter)); !isQuit(cmd) || m.Chosen == "Install" {
			t.Errorf("Enter on Exit: quit = %v, Chosen = %q; want a quit", isQuit(cmd), m.Chosen)
		}
	})
}

// The health probe must not fight a caret the digit placed.
func TestSplashModel_DegradedNeverMovesTheCaretAfterANumberKey(t *testing.T) {
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), runeKey('4'))
	m, _ = updateAs(t, m, healthResultMsg{reason: deploy.HealthNotResponding})
	if m.menu.sel != 3 {
		t.Errorf("selected = %d, want 3 — the probe moved the caret a digit had placed", m.menu.sel)
	}
}

// During the arrival any key skips it and is swallowed: a digit does not
// leave, and does not move the caret either.
func TestSplashModel_NumberKeyDuringTheArrivalSkipsItAndIsSwallowed(t *testing.T) {
	m := NewSplashModel()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, cmd := updateAs(t, m, runeKey('5'))
	if isQuit(cmd) || m.Chosen != "" || m.quitting {
		t.Fatalf("a digit during the arrival left (quit = %v, Chosen = %q)", isQuit(cmd), m.Chosen)
	}
	if !m.introDone {
		t.Error("the digit did not skip the arrival")
	}
	if m.menu.sel != 0 {
		t.Errorf("selected = %d: the skipping key must be swallowed, not move the caret", m.menu.sel)
	}
}

func TestSplashModel_NumberKeyBeyondMenuLengthIsIgnored(t *testing.T) {
	m := settled(NewSplashModel())

	updated, cmd := m.Update(runeKey('9'))
	m = updated.(SplashModel)

	if m.Chosen != "" {
		t.Errorf("Chosen = %q, want empty (out-of-range number key)", m.Chosen)
	}
	if cmd != nil {
		t.Error("expected no command for an out-of-range number key")
	}
}

func TestSplashModel_CtrlCQuitsWithoutChoosing(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{ctrlC()} {
		m := settled(NewSplashModel())
		m.menu.sel = 1

		updated, cmd := m.Update(k)
		m = updated.(SplashModel)

		if m.Chosen != "" {
			t.Errorf("%v: Chosen = %q, want empty", k, m.Chosen)
		}
		if !m.quitting {
			t.Errorf("%v: expected quitting to be true", k)
		}
		if cmd == nil || cmd() != tea.Quit() {
			t.Errorf("%v: expected tea.Quit", k)
		}
	}
}

// Esc on the lit menu does nothing (#226, answer 3a): backing out stops
// at the menu, so pressing Esc repeatedly never closes the launcher.
func TestSplashModel_EscDoesNothingOnTheLitMenu(t *testing.T) {
	m := settled(NewSplashModel())
	m.menu.sel = 1
	updated, cmd := m.Update(key(tea.KeyEsc))
	m = updated.(SplashModel)

	if m.Chosen != "" || m.quitting || cmd != nil {
		t.Fatalf("Esc on the menu: Chosen = %q, quitting = %v, cmd = %v; want it to do nothing", m.Chosen, m.quitting, cmd != nil)
	}
	if m.menu.sel != 1 {
		t.Errorf("Esc moved the selection to %d, want it left on 1", m.menu.sel)
	}
	updated, _ = m.Update(key(tea.KeyDown))
	if got := updated.(SplashModel).menu.sel; got != 2 {
		t.Errorf("Down after Esc moved to %d, want 2: the menu is not live", got)
	}
}

func TestSplashModel_EscDuringTheArrivalSkipsItAndIsSwallowed(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)
	if m.introDone {
		t.Fatal("the arrival should be playing on a fresh animated splash")
	}

	updated, cmd := m.Update(key(tea.KeyEsc))
	m = updated.(SplashModel)
	if isQuit(cmd) || m.quitting || m.Chosen != "" {
		t.Fatalf("Esc during the arrival quit or chose: quit=%v quitting=%v Chosen=%q", isQuit(cmd), m.quitting, m.Chosen)
	}
	if !m.introDone {
		t.Error("Esc during the arrival must skip it, like any other key")
	}
	if !strings.Contains(m.View().Content, "Install") {
		t.Error("after the skip, the lit menu must be showing")
	}
}

// There is no q shortcut (#226): leaving takes the Exit row or Ctrl-C.
// On the lit menu q is an unbound key.
func TestSplashModel_QDoesNothingOnTheLitMenu(t *testing.T) {
	for _, r := range []rune{'q', 'Q'} {
		m := settled(NewSplashModel())
		m.menu.sel = 1
		updated, cmd := m.Update(runeKey(r))
		m = updated.(SplashModel)

		if m.Chosen != "" {
			t.Errorf("%q: Chosen = %q, want empty", r, m.Chosen)
		}
		if m.quitting {
			t.Errorf("%q: the menu is quitting", r)
		}
		if cmd != nil {
			t.Errorf("%q: issued a command (quit = %v), want none", r, isQuit(cmd))
		}
		if m.menu.sel != 1 {
			t.Errorf("%q: selection moved to %d, want it left on 1", r, m.menu.sel)
		}
	}
}

func TestSplashModel_QDuringTheArrivalSkipsItAndIsSwallowed(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)
	if m.introDone {
		t.Fatal("the arrival should be playing on a fresh animated splash")
	}

	updated, cmd := m.Update(runeKey('q'))
	m = updated.(SplashModel)
	if isQuit(cmd) || m.quitting || m.Chosen != "" {
		t.Fatalf("q during the arrival quit or chose: quit=%v quitting=%v Chosen=%q", isQuit(cmd), m.quitting, m.Chosen)
	}
	if !m.introDone {
		t.Error("q during the arrival must skip it, like any other key")
	}
	if m.menu.sel != 0 {
		t.Error("the skipping q must be swallowed")
	}
	if !strings.Contains(m.View().Content, "Install") {
		t.Error("after the skip, the lit menu must be showing")
	}
}

func TestNewSplashModelNoAnimation_InitIssuesNoTickCommand(t *testing.T) {
	m := NewSplashModelNoAnimation()
	if cmd := m.Init(); cmd != nil {
		t.Error("expected Init() to issue no command in no-animation mode")
	}
}

func TestSplashModel_InitIssuesATickCommand(t *testing.T) {
	m := NewSplashModel()
	if cmd := m.Init(); cmd == nil {
		t.Error("expected Init() to issue a tick command by default")
	}
}

func TestSplashModel_ViewIsEmptyBeforeFirstWindowSize(t *testing.T) {
	m := NewSplashModel()
	if view := m.View().Content; view != "" {
		t.Errorf("View() before any WindowSizeMsg = %q, want empty", view)
	}
}

func TestSplashModel_DefaultConstructorNeverChecksForUpdates(t *testing.T) {
	m := NewSplashModel()
	if m.checkForUpdate != nil {
		t.Error("expected checkForUpdate to be nil by default — no constructor should opt into a network side effect on render")
	}
	// Init() must therefore issue no update-check command either — the
	// only remaining possible command is the animation tick.
	if cmd := m.Init(); cmd == nil {
		t.Error("expected the tick command still to run") // sanity: Init isn't just returning nil for an unrelated reason
	}
}

func TestSplashModel_InitIssuesNoCommandsWhenAnimationAndUpdateCheckAreBothOff(t *testing.T) {
	m := NewSplashModelNoAnimation()
	m.checkForUpdate = nil
	if cmd := m.Init(); cmd != nil {
		t.Error("expected Init() to issue no command with animation and the update check both off")
	}
}

func TestSplashModel_InitIssuesAnUpdateCheckCommandWhenConfigured(t *testing.T) {
	m := NewSplashModelNoAnimation()
	called := false
	m.checkForUpdate = func(context.Context) (string, bool, error) {
		called = true
		return "v9.9.9", true, nil
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected Init() to issue the update-check command")
	}
	msg := cmd()
	if !called {
		t.Error("expected checkForUpdate to have been invoked")
	}
	got, ok := msg.(updateAvailableMsg)
	if !ok {
		t.Fatalf("msg = %#v, want updateAvailableMsg", msg)
	}
	if got.version != "v9.9.9" {
		t.Errorf("version = %q, want v9.9.9", got.version)
	}
}

func TestSplashModel_UpdateCheckErrorProducesNoMessage(t *testing.T) {
	m := NewSplashModelNoAnimation()
	m.checkForUpdate = func(context.Context) (string, bool, error) {
		return "", false, errors.New("network unreachable")
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected Init() to issue the update-check command")
	}
	if msg := cmd(); msg != nil {
		t.Errorf("msg = %#v, want nil on error — an update check failure must never surface as a user-facing error", msg)
	}
}

func TestSplashModel_UpdateCheckNoUpdateProducesNoMessage(t *testing.T) {
	m := NewSplashModelNoAnimation()
	m.checkForUpdate = func(context.Context) (string, bool, error) {
		return "", false, nil
	}

	cmd := m.Init()
	if msg := cmd(); msg != nil {
		t.Errorf("msg = %#v, want nil when already current", msg)
	}
}

func TestSplashModel_UpdateAvailableMsgIsShownOnScreen(t *testing.T) {
	m := settled(NewSplashModel())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(SplashModel)

	if strings.Contains(m.View().Content, "update available") {
		t.Fatal("did not expect an update notice before one is received")
	}

	updated, _ = m.Update(updateAvailableMsg{version: "v9.9.9"})
	m = updated.(SplashModel)

	view := m.View().Content
	if !strings.Contains(view, "update available") || !strings.Contains(view, "v9.9.9") {
		t.Errorf("expected the view to show the update notice, got:\n%s", view)
	}
}

func TestSplashModel_ViewIsEmptyAfterQuitting(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(SplashModel)
	updated, _ = m.Update(ctrlC())
	m = updated.(SplashModel)

	if view := m.View().Content; view != "" {
		t.Errorf("View() after quitting = %q, want empty", view)
	}
}

func seedDeployment(m SplashModel) SplashModel {
	m.introDone = true
	m.fqdn = "mail.example.com"
	m.appURL = "https://mail.example.com"
	m.state = stateUnknown
	m.menu.sel = menuUpdate
	return m
}

func TestSplashModel_HealthResultAliveKeepsUpdatePreselected(t *testing.T) {
	m := seedDeployment(NewSplashModel())

	updated, _ := m.Update(healthResultMsg{reason: deploy.HealthResponding})
	m = updated.(SplashModel)

	if m.state != stateAlive {
		t.Errorf("state = %v, want stateAlive", m.state)
	}
	if m.menu.sel != menuUpdate {
		t.Errorf("selected = %d, want Update to stay preselected", m.menu.sel)
	}
}

func TestSplashModel_HealthResultDegradedPreselectsRepair(t *testing.T) {
	m := seedDeployment(NewSplashModel())

	updated, _ := m.Update(healthResultMsg{reason: deploy.HealthNotResponding})
	m = updated.(SplashModel)

	if m.state != stateDegraded {
		t.Errorf("state = %v, want stateDegraded", m.state)
	}
	if m.menu.sel != menuRepair {
		t.Errorf("selected = %d, want Repair preselected on a degraded deployment", m.menu.sel)
	}
}

func TestSplashModel_DegradedNeverMovesTheCaretAfterTheUserNavigates(t *testing.T) {
	m := seedDeployment(NewSplashModel())

	updated, _ := m.Update(key(tea.KeyDown)) // the user takes over
	m = updated.(SplashModel)
	navigatedTo := m.menu.sel

	updated, _ = m.Update(healthResultMsg{reason: deploy.HealthNotResponding})
	m = updated.(SplashModel)

	if m.state != stateDegraded {
		t.Errorf("state = %v, want stateDegraded (display still updates)", m.state)
	}
	if m.menu.sel != navigatedTo {
		t.Errorf("selected = %d, want %d — the probe must never fight the user's hands", m.menu.sel, navigatedTo)
	}
}

func TestSplashModel_ViewShowsIdentityBlockPerState(t *testing.T) {
	base := settled(NewSplashModel())
	updated, _ := base.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	base = updated.(SplashModel)

	if view := base.View().Content; !strings.Contains(view, "dormant") {
		t.Error("dormant view must show the status word under the wordmark")
	}

	m := seedDeployment(base)
	if view := m.View().Content; !strings.Contains(view, "mail.example.com") {
		t.Error("unknown-health view must show the FQDN")
	} else if strings.Contains(view, "alive") || strings.Contains(view, "degraded") {
		t.Error("unknown-health view must never guess a status word")
	}

	updated, _ = m.Update(healthResultMsg{reason: deploy.HealthResponding})
	if view := updated.(SplashModel).View().Content; !strings.Contains(view, "alive") {
		t.Error("alive view must show the status word")
	}

	updated, _ = m.Update(healthResultMsg{reason: deploy.HealthNotResponding})
	if view := updated.(SplashModel).View().Content; !strings.Contains(view, "degraded") {
		t.Error("degraded view must show the status word")
	}
}

func TestSplashModel_FootIsOneCentredVersionLineAndNothingElse(t *testing.T) {
	m := settled(NewSplashModel())
	m.version = "v0.1.0"
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)

	view := m.View().Content
	lines := strings.Split(view, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "orbit-launcher v0.1.0") {
		t.Errorf("foot must carry the launcher version, got %q", last)
	}
	if strings.Contains(view, "navigate") || strings.Contains(view, "esc quit") {
		t.Error("the keybind hint is gone for good — no navigation instructions anywhere")
	}
	if strings.Contains(view, "personal server launcher") {
		t.Error("the old tagline must be gone")
	}

	// With a detected deployment the orbit version joins the same line.
	m = seedDeployment(m)
	m.orbitVersion = "v1.2.0"
	lines = strings.Split(m.View().Content, "\n")
	last = lines[len(lines)-1]
	if !strings.Contains(last, "orbit-launcher v0.1.0 · orbit v1.2.0") {
		t.Errorf("foot must carry both versions once orbit is known, got %q", last)
	}
}

func TestSplashModel_AnyKeySkipsTheArrivalAndIsSwallowed(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)

	if m.introDone {
		t.Fatal("the arrival should be playing on a fresh animated splash")
	}
	if strings.Contains(m.View().Content, "Install") {
		t.Fatal("the menu must not be visible at the start of the arrival")
	}

	updated, _ = m.Update(key(tea.KeyDown))
	m = updated.(SplashModel)
	if !m.introDone {
		t.Error("any key must skip the arrival")
	}
	if m.menu.sel != 0 {
		t.Error("the skipping key must be swallowed, not treated as navigation")
	}
	if !strings.Contains(m.View().Content, "Install") {
		t.Error("after the skip, the lit room must be fully there")
	}
}

func TestSplashModel_ArrivalFinishesOnItsOwnAfterEnoughTicks(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)

	for i := 0; i < 80; i++ { // 80 ticks ≈ 9.6s > introEnd
		updated, _ = m.Update(tickMsg{})
		m = updated.(SplashModel)
	}
	if !m.introDone {
		t.Error("the arrival must conclude by itself")
	}
	if !strings.Contains(m.View().Content, "Install") || !strings.Contains(m.View().Content, "dormant") {
		t.Error("the settled view must follow the arrival")
	}
}

func TestSplashModel_NoAnimationNeverPlaysTheArrival(t *testing.T) {
	m := NewSplashModelNoAnimation()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)
	if !strings.Contains(m.View().Content, "Install") {
		t.Error("reduced motion goes straight to the lit room")
	}
}

func TestSplashModel_ArrivalShowsTheWordsInOrder(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)

	var sawGet, sawInto, sawOrbitAlone bool
	for i := 0; i < 80; i++ {
		view := m.View().Content
		if strings.Contains(view, "Get") {
			sawGet = true
		}
		if strings.Contains(view, "Into") {
			sawInto = true
		}
		if strings.Contains(view, "O R B I T") && !strings.Contains(view, "Install") {
			sawOrbitAlone = true
		}
		updated, _ = m.Update(tickMsg{})
		m = updated.(SplashModel)
	}
	if !sawGet || !sawInto || !sawOrbitAlone {
		t.Errorf("arrival beats missing: Get=%v Into=%v OrbitAlone=%v", sawGet, sawInto, sawOrbitAlone)
	}
}

// A terminal reporting zero or one rows leaves no body to draw; the
// splash must render without panicking and recover at the next real size.
func TestSplashModel_ZeroHeightWindowDoesNotPanic(t *testing.T) {
	for _, h := range []int{0, 1} {
		updated, _ := NewSplashModel().Update(tea.WindowSizeMsg{Width: 80, Height: h})
		m := updated.(SplashModel)
		if view := m.View().Content; view != "" {
			t.Errorf("intro View() at height %d = %q, want empty", h, view)
		}

		updated, _ = NewSplashModelNoAnimation().Update(tea.WindowSizeMsg{Width: 80, Height: h})
		still := updated.(SplashModel)
		_ = still.View()

		updated, _ = still.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
		if view := updated.(SplashModel).View().Content; !strings.Contains(view, "Install") {
			t.Errorf("View() after resizing from height %d to 26 lacks the menu: %q", h, view)
		}
	}
}
