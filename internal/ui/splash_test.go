package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }
func runeKey(r rune) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// settled skips the arrival, as any keypress would — behaviour and view
// tests exercise the lit room unless they are about the arrival itself.
func settled(m SplashModel) SplashModel { m.introDone = true; return m }

func TestSplashModel_ArrowNavigationWraps(t *testing.T) {
	m := settled(NewSplashModel())

	if m.selected != 0 {
		t.Fatalf("initial selection = %d, want 0", m.selected)
	}

	updated, _ := m.Update(key(tea.KeyUp))
	m = updated.(SplashModel)
	if m.selected != len(MainMenu)-1 {
		t.Errorf("Up from index 0 = %d, want wrap to %d", m.selected, len(MainMenu)-1)
	}

	updated, _ = m.Update(key(tea.KeyDown))
	m = updated.(SplashModel)
	if m.selected != 0 {
		t.Errorf("Down from the last index = %d, want wrap to 0", m.selected)
	}
}

func TestSplashModel_EnterChoosesTheSelectedItemAndQuits(t *testing.T) {
	m := settled(NewSplashModel())
	m.selected = 2 // Repair

	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(SplashModel)

	if m.Chosen != "Repair" {
		t.Errorf("Chosen = %q, want %q", m.Chosen, "Repair")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Enter to issue tea.Quit")
	}
}

func TestSplashModel_NumberKeyJumpsAndChooses(t *testing.T) {
	m := settled(NewSplashModel())

	updated, cmd := m.Update(runeKey('4')) // 1-indexed: Remove
	m = updated.(SplashModel)

	if m.selected != 3 {
		t.Errorf("selected = %d, want 3 (Remove)", m.selected)
	}
	if m.Chosen != "Remove" {
		t.Errorf("Chosen = %q, want %q", m.Chosen, "Remove")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected a number key to issue tea.Quit")
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

func TestSplashModel_EscapeAndCtrlCQuitWithoutChoosing(t *testing.T) {
	for _, k := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		m := settled(NewSplashModel())
		m.selected = 1

		updated, cmd := m.Update(key(k))
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

func TestSplashModel_QLowercaseQuitsWithoutChoosing(t *testing.T) {
	m := settled(NewSplashModel())
	updated, cmd := m.Update(runeKey('q'))
	m = updated.(SplashModel)

	if m.Chosen != "" {
		t.Errorf("Chosen = %q, want empty", m.Chosen)
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected 'q' to issue tea.Quit")
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
	if view := m.View(); view != "" {
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

	if strings.Contains(m.View(), "update available") {
		t.Fatal("did not expect an update notice before one is received")
	}

	updated, _ = m.Update(updateAvailableMsg{version: "v9.9.9"})
	m = updated.(SplashModel)

	view := m.View()
	if !strings.Contains(view, "update available") || !strings.Contains(view, "v9.9.9") {
		t.Errorf("expected the view to show the update notice, got:\n%s", view)
	}
}

func TestSplashModel_ViewIsEmptyAfterQuitting(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(SplashModel)
	updated, _ = m.Update(key(tea.KeyEsc))
	m = updated.(SplashModel)

	if view := m.View(); view != "" {
		t.Errorf("View() after quitting = %q, want empty", view)
	}
}

func seedDeployment(m SplashModel) SplashModel {
	m.introDone = true
	m.fqdn = "mail.example.com"
	m.appURL = "https://mail.example.com"
	m.state = stateUnknown
	m.selected = menuUpdate
	return m
}

func TestSplashModel_HealthResultAliveKeepsUpdatePreselected(t *testing.T) {
	m := seedDeployment(NewSplashModel())

	updated, _ := m.Update(healthResultMsg{healthy: true})
	m = updated.(SplashModel)

	if m.state != stateAlive {
		t.Errorf("state = %v, want stateAlive", m.state)
	}
	if m.selected != menuUpdate {
		t.Errorf("selected = %d, want Update to stay preselected", m.selected)
	}
}

func TestSplashModel_HealthResultDegradedPreselectsRepair(t *testing.T) {
	m := seedDeployment(NewSplashModel())

	updated, _ := m.Update(healthResultMsg{healthy: false})
	m = updated.(SplashModel)

	if m.state != stateDegraded {
		t.Errorf("state = %v, want stateDegraded", m.state)
	}
	if m.selected != menuRepair {
		t.Errorf("selected = %d, want Repair preselected on a degraded deployment", m.selected)
	}
}

func TestSplashModel_DegradedNeverMovesTheCaretAfterTheUserNavigates(t *testing.T) {
	m := seedDeployment(NewSplashModel())

	updated, _ := m.Update(key(tea.KeyDown)) // the user takes over
	m = updated.(SplashModel)
	navigatedTo := m.selected

	updated, _ = m.Update(healthResultMsg{healthy: false})
	m = updated.(SplashModel)

	if m.state != stateDegraded {
		t.Errorf("state = %v, want stateDegraded (display still updates)", m.state)
	}
	if m.selected != navigatedTo {
		t.Errorf("selected = %d, want %d — the probe must never fight the user's hands", m.selected, navigatedTo)
	}
}

func TestSplashModel_ViewShowsIdentityBlockPerState(t *testing.T) {
	base := settled(NewSplashModel())
	updated, _ := base.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	base = updated.(SplashModel)

	if view := base.View(); !strings.Contains(view, "dormant") {
		t.Error("dormant view must show the status word under the wordmark")
	}

	m := seedDeployment(base)
	if view := m.View(); !strings.Contains(view, "mail.example.com") {
		t.Error("unknown-health view must show the FQDN")
	} else if strings.Contains(view, "alive") || strings.Contains(view, "degraded") {
		t.Error("unknown-health view must never guess a status word")
	}

	updated, _ = m.Update(healthResultMsg{healthy: true})
	if view := updated.(SplashModel).View(); !strings.Contains(view, "alive") {
		t.Error("alive view must show the status word")
	}

	updated, _ = m.Update(healthResultMsg{healthy: false})
	if view := updated.(SplashModel).View(); !strings.Contains(view, "degraded") {
		t.Error("degraded view must show the status word")
	}
}

func TestSplashModel_FootIsOneCentredVersionLineAndNothingElse(t *testing.T) {
	m := settled(NewSplashModel())
	m.version = "v0.1.0"
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)

	view := m.View()
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
	lines = strings.Split(m.View(), "\n")
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
	if strings.Contains(m.View(), "Install") {
		t.Fatal("the menu must not be visible at the start of the arrival")
	}

	updated, _ = m.Update(key(tea.KeyDown))
	m = updated.(SplashModel)
	if !m.introDone {
		t.Error("any key must skip the arrival")
	}
	if m.selected != 0 {
		t.Error("the skipping key must be swallowed, not treated as navigation")
	}
	if !strings.Contains(m.View(), "Install") {
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
	if !strings.Contains(m.View(), "Install") || !strings.Contains(m.View(), "dormant") {
		t.Error("the settled view must follow the arrival")
	}
}

func TestSplashModel_NoAnimationNeverPlaysTheArrival(t *testing.T) {
	m := NewSplashModelNoAnimation()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)
	if !strings.Contains(m.View(), "Install") {
		t.Error("reduced motion goes straight to the lit room")
	}
}

func TestSplashModel_ArrivalShowsTheWordsInOrder(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SplashModel)

	var sawGet, sawInto, sawOrbitAlone bool
	for i := 0; i < 80; i++ {
		view := m.View()
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
