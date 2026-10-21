package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// #254: the degraded status word opens a drawer saying why.

var allReasons = []struct {
	name   string
	reason deploy.HealthReason
	text   string
}{
	{"not responding", deploy.HealthNotResponding, "not responding"},
	{"address not found", deploy.HealthAddressNotFound, "address not found"},
	{"server error", deploy.HealthServerError, "server error"},
	{"certificate rejected", deploy.HealthCertificateRejected, "certificate rejected"},
	{"unchecked", deploy.HealthUnchecked, "could not check"},
}

func TestSplashDegraded_ReasonTextsAreExact(t *testing.T) {
	want := map[deploy.HealthReason]string{
		deploy.HealthResponding:          "",
		deploy.HealthNotResponding:       "not responding",
		deploy.HealthAddressNotFound:     "address not found",
		deploy.HealthServerError:         "server error",
		deploy.HealthCertificateRejected: "certificate rejected",
		deploy.HealthUnchecked:           "could not check",
	}
	for r, text := range want {
		if string(r) != text {
			t.Errorf("reason %q, want %q", string(r), text)
		}
	}
}

// degradedSplash is a settled 80x26 splash with a detected deployment
// that has received a health result.
func degradedSplash(t *testing.T, msg healthResultMsg) SplashModel {
	t.Helper()
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26})
	m, _ = updateAs(t, m, msg)
	return m
}

func degradedWith(t *testing.T, reason deploy.HealthReason) SplashModel {
	t.Helper()
	return degradedSplash(t, healthResultMsg{reason: reason})
}

func lineWith(s, sub string) (string, bool) {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l, true
		}
	}
	return "", false
}

func lineIndex(s, sub string) int {
	for i, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

// centreCol is the middle column of a row's visible text.
// sky is what a screen row may carry besides its text: padding and the
// starfield's glyphs. Rows are read with it taken off their ends.
const sky = " .·✦●"

func centreCol(line string) float64 {
	trimmed := strings.TrimRight(line, sky)
	text := strings.TrimLeft(trimmed, sky)
	lead := utf8.RuneCountInString(trimmed) - utf8.RuneCountInString(text)
	return float64(lead) + float64(utf8.RuneCountInString(text))/2
}

func assertCentredWith(t *testing.T, s, a, b string) {
	t.Helper()
	la, ok := lineWith(s, a)
	if !ok {
		t.Fatalf("no row with %q:\n%s", a, s)
	}
	lb, ok := lineWith(s, b)
	if !ok {
		t.Fatalf("no row with %q:\n%s", b, s)
	}
	d := centreCol(la) - centreCol(lb)
	if d < -1 || d > 1 {
		t.Errorf("%q centre %.1f vs %q centre %.1f: off by more than one cell:\n%s", a, centreCol(la), b, centreCol(lb), s)
	}
}

// caretRows lists the rows (trimmed) that start with the caret glyph.
func caretRows(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if row := strings.Trim(l, sky); strings.HasPrefix(row, style.SymbolSelected) {
			out = append(out, row)
		}
	}
	return out
}

func assertCaretOn(t *testing.T, s, word string) {
	t.Helper()
	rows := caretRows(s)
	if len(rows) != 1 || !strings.Contains(rows[0], word) {
		t.Errorf("caret rows = %q, want exactly one, on %q:\n%s", rows, word, s)
	}
}

func onInstall(t *testing.T, m SplashModel) SplashModel {
	t.Helper()
	m, _ = updateAs(t, m, runeKey('1'))
	return m
}

func onDegradedRow(t *testing.T) SplashModel {
	t.Helper()
	m := onInstall(t, degradedWith(t, deploy.HealthNotResponding))
	m, _ = updateAs(t, m, key(tea.KeyUp))
	return m
}

func openDrawer(t *testing.T, m SplashModel) SplashModel {
	t.Helper()
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	return m
}

func TestSplashDegraded_ReasonMakesItDegradedWithRepairPreselected(t *testing.T) {
	m := degradedWith(t, deploy.HealthServerError)
	if m.state != stateDegraded {
		t.Errorf("state = %v, want degraded", m.state)
	}
	if m.menu.sel != menuRepair {
		t.Errorf("selected = %d, want Repair", m.menu.sel)
	}
	if !strings.Contains(screen(m), "degraded") {
		t.Errorf("screen lacks the word degraded:\n%s", screen(m))
	}
}

func TestSplashDegraded_RespondingMakesItAliveWithUpdatePreselected(t *testing.T) {
	m := degradedWith(t, deploy.HealthResponding)
	if m.state != stateAlive {
		t.Errorf("state = %v, want alive", m.state)
	}
	if m.menu.sel != menuUpdate {
		t.Errorf("selected = %d, want Update", m.menu.sel)
	}
	if !strings.Contains(screen(m), "alive") {
		t.Errorf("screen lacks the word alive:\n%s", screen(m))
	}
}

func TestSplashDegraded_ResultDoesNotMoveACaretTheUserPlaced(t *testing.T) {
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26}, runeKey('4'))
	m, _ = updateAs(t, m, healthResultMsg{reason: deploy.HealthNotResponding})
	if m.menu.sel != 3 {
		t.Errorf("selected = %d, want 3", m.menu.sel)
	}
}

func TestSplashDegraded_UpFromInstallReachesTheRow(t *testing.T) {
	m := onInstall(t, degradedWith(t, deploy.HealthNotResponding))
	m, _ = updateAs(t, m, key(tea.KeyUp))
	s := screen(m)
	assertCaretOn(t, s, "degraded")
	for _, item := range MainMenu {
		if strings.Contains(s, style.SymbolSelected+" "+item.Label) {
			t.Errorf("caret still on menu item %s:\n%s", item.Label, s)
		}
	}
}

func TestSplashDegraded_UpAgainWrapsToExit(t *testing.T) {
	m, _ := updateAs(t, onDegradedRow(t), key(tea.KeyUp))
	last := MainMenu[len(MainMenu)-1].Label
	if last != "Exit" {
		t.Fatalf("last menu item = %q, want Exit", last)
	}
	assertCaretOn(t, screen(m), "Exit")
}

func TestSplashDegraded_DownFromTheRowGoesToInstall(t *testing.T) {
	m, _ := updateAs(t, onDegradedRow(t), key(tea.KeyDown))
	assertCaretOn(t, screen(m), MainMenu[0].Label)
}

func TestSplashDegraded_DigitFromTheRowMovesToAMenuRow(t *testing.T) {
	for i, item := range MainMenu {
		t.Run(item.Label, func(t *testing.T) {
			m, cmd := updateAs(t, onDegradedRow(t), runeKey(rune('1'+i)))
			assertCaretOn(t, screen(m), item.Label)
			if m.Chosen != "" || cmd != nil {
				t.Errorf("a digit chose %q / issued a command", m.Chosen)
			}
		})
	}
}

func TestSplashDegraded_EnterOnTheRowOpensTheDrawer(t *testing.T) {
	m, cmd := updateAs(t, onDegradedRow(t), key(tea.KeyEnter))
	if !strings.Contains(screen(m), "not responding") {
		t.Errorf("drawer not open:\n%s", screen(m))
	}
	if m.Chosen != "" || m.quitting || (cmd != nil && isQuit(cmd)) {
		t.Errorf("Enter on the row chose %q / quit", m.Chosen)
	}
}

func TestSplashDegraded_RightOpensTheDrawer(t *testing.T) {
	m, _ := updateAs(t, onDegradedRow(t), key(tea.KeyRight))
	if !strings.Contains(screen(m), "not responding") {
		t.Errorf("drawer not open:\n%s", screen(m))
	}
}

func TestSplashDegraded_ClosedRowIsFaintRightChevronOpenIsDown(t *testing.T) {
	closed := screen(onDegradedRow(t))
	l, _ := lineWith(closed, "degraded")
	if !strings.HasSuffix(strings.TrimRight(l, " "), "›") || strings.Contains(l, "⌄") {
		t.Errorf("closed row = %q, want to end with ›", l)
	}
	open := screen(openDrawer(t, onDegradedRow(t)))
	l, _ = lineWith(open, "degraded")
	if !strings.HasSuffix(strings.TrimRight(l, " "), "⌄") || strings.Contains(l, "›") {
		t.Errorf("open row = %q, want to end with ⌄", l)
	}
}

func TestSplashDegraded_DrawerClosesAndLandsOnRepair(t *testing.T) {
	for name, k := range map[string]tea.KeyPressMsg{
		"left":  key(tea.KeyLeft),
		"enter": key(tea.KeyEnter),
		"esc":   key(tea.KeyEscape),
	} {
		t.Run(name, func(t *testing.T) {
			m := openDrawer(t, onDegradedRow(t))
			m, cmd := updateAs(t, m, k)
			s := screen(m)
			if strings.Contains(s, "not responding") {
				t.Errorf("drawer still open:\n%s", s)
			}
			assertCaretOn(t, s, "Repair")
			if m.Chosen != "" || (cmd != nil && isQuit(cmd)) {
				t.Errorf("closing the drawer chose %q / quit", m.Chosen)
			}
		})
	}
}

func TestSplashDegraded_EscOnTheClosedRowDoesNothing(t *testing.T) {
	m := onDegradedRow(t)
	before := screen(m)
	m, cmd := updateAs(t, m, key(tea.KeyEscape))
	if screen(m) != before {
		t.Errorf("Esc changed the screen:\n%s\n--- was ---\n%s", screen(m), before)
	}
	if cmd != nil || m.Chosen != "" || m.quitting {
		t.Error("Esc on the closed row acted")
	}
}

func TestSplashDegraded_EnterOnTheRowNeverChoosesOrQuits(t *testing.T) {
	// Enter opens the drawer, Enter again closes it onto Repair; neither
	// press chooses a menu item or quits.
	m := onDegradedRow(t)
	for i := 0; i < 2; i++ {
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, key(tea.KeyEnter))
		if m.Chosen != "" || m.quitting || (cmd != nil && isQuit(cmd)) {
			t.Fatalf("press %d chose %q / quit", i+1, m.Chosen)
		}
	}
	assertCaretOn(t, screen(m), "Repair")
}

func TestSplashDegraded_AliveHasNoRowUpWrapsToExit(t *testing.T) {
	m := onInstall(t, degradedWith(t, deploy.HealthResponding))
	m, _ = updateAs(t, m, key(tea.KeyUp))
	assertCaretOn(t, screen(m), "Exit")
}

func TestSplashDegraded_DormantHasNoRowUpWrapsToExit(t *testing.T) {
	m, _ := updateAs(t, settled(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26})
	m, _ = updateAs(t, m, key(tea.KeyUp))
	assertCaretOn(t, screen(m), "Exit")
}

func TestSplashDegraded_EveryReasonRendersItsExactText(t *testing.T) {
	for _, tc := range allReasons {
		t.Run(tc.name, func(t *testing.T) {
			m := openDrawer(t, func() SplashModel {
				m := degradedWith(t, tc.reason)
				m = onInstall(t, m)
				m, _ = updateAs(t, m, key(tea.KeyUp))
				return m
			}())
			s := screen(m)
			if !strings.Contains(s, tc.text) {
				t.Errorf("open drawer lacks %q:\n%s", tc.text, s)
			}
			if strings.Contains(strings.ToLower(s), "unknown") {
				t.Errorf("screen shows 'unknown':\n%s", s)
			}
		})
	}
}

func TestSplashDegraded_ClosedDrawerHidesTheReason(t *testing.T) {
	s := screen(onDegradedRow(t))
	if strings.Contains(s, "not responding") {
		t.Errorf("closed row shows the reason:\n%s", s)
	}
}

func TestSplashDegraded_CountedShowsServicesRow(t *testing.T) {
	m := degradedSplash(t, healthResultMsg{reason: deploy.HealthNotResponding, running: 2, expected: 3, counted: true})
	m = openDrawer(t, func() SplashModel {
		m = onInstall(t, m)
		m, _ = updateAs(t, m, key(tea.KeyUp))
		return m
	}())
	s := screen(m)
	if !strings.Contains(s, "not responding") || !strings.Contains(s, "2 of 3 services running") {
		t.Errorf("drawer lacks reason or count:\n%s", s)
	}
	if lineIndex(s, "2 of 3 services running") <= lineIndex(s, "not responding") {
		t.Errorf("services row should be beneath the reason row:\n%s", s)
	}
}

func TestSplashDegraded_UncountedHasNoServicesRow(t *testing.T) {
	m := degradedSplash(t, healthResultMsg{reason: deploy.HealthNotResponding, running: 0, expected: 0, counted: false})
	m = openDrawer(t, func() SplashModel {
		m = onInstall(t, m)
		m, _ = updateAs(t, m, key(tea.KeyUp))
		return m
	}())
	s := screen(m)
	if strings.Contains(s, "services") {
		t.Errorf("uncounted drawer mentions services:\n%s", s)
	}
}

func TestSplashDegraded_DrawerRowsAreCentredOnTheAxis(t *testing.T) {
	m := degradedSplash(t, healthResultMsg{reason: deploy.HealthCertificateRejected, running: 2, expected: 3, counted: true})
	m = openDrawer(t, func() SplashModel {
		m = onInstall(t, m)
		m, _ = updateAs(t, m, key(tea.KeyUp))
		return m
	}())
	s := screen(m)
	for _, row := range []string{"certificate rejected", "2 of 3 services running"} {
		assertCentredWith(t, s, row, "O R B I T")
		assertCentredWith(t, s, row, "degraded")
	}
}

func TestSplashDegraded_OpenDrawerKeepsTheMenuOnScreen(t *testing.T) {
	m := degradedSplash(t, healthResultMsg{reason: deploy.HealthNotResponding, running: 2, expected: 3, counted: true})
	m = openDrawer(t, func() SplashModel {
		m = onInstall(t, m)
		m, _ = updateAs(t, m, key(tea.KeyUp))
		return m
	}())
	s := screen(m)
	for _, item := range MainMenu {
		if !strings.Contains(s, item.Label) {
			t.Errorf("menu item %s is off screen:\n%s", item.Label, s)
		}
	}
	if lineIndex(s, "Exit") <= lineIndex(s, "Install") {
		t.Errorf("Exit should be below Install:\n%s", s)
	}
	if got := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); got > 26 {
		t.Errorf("screen is %d rows, want at most 26", got)
	}
}

// probe command

func probeModel(t *testing.T, profiles string) (SplashModel, string) {
	t.Helper()
	dir := t.TempDir()
	env := "APP_URL=http://localhost\nCOMPOSE_PROFILES=" + profiles + "\n"
	if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	m := seedDeployment(NewSplashModel())
	m.targetDir = dir
	return m, dir
}

func TestSplashProbe_ReportsReasonAndServiceCount(t *testing.T) {
	m, dir := probeModel(t, "full")
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthServerError }
	m.serviceStates = func(got string) ([]deploy.ServiceState, error) {
		if got != dir {
			t.Errorf("asked about %q, want %q", got, dir)
		}
		return []deploy.ServiceState{
			{Service: "orbit-db", State: "running"},
			{Service: "orbit-app", State: "running"},
			{Service: "orbit-clamav", State: "running"},
			{Service: "orbit-tika", State: "exited"},
		}, nil
	}
	cmd := m.probeHealthCmd()
	if cmd == nil {
		t.Fatal("probeHealthCmd returned nil")
	}
	want := healthResultMsg{reason: deploy.HealthServerError, running: 3, expected: 5, counted: true}
	if got := cmd(); got != want {
		t.Errorf("message = %+v, want %+v", got, want)
	}
}

func TestSplashProbe_ExpectedServicesFollowTheProfiles(t *testing.T) {
	for profiles, want := range map[string]int{"": 3, "processing": 4, "ai": 4, "full": 5} {
		t.Run("profiles="+profiles, func(t *testing.T) {
			m, _ := probeModel(t, profiles)
			m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthNotResponding }
			m.serviceStates = func(string) ([]deploy.ServiceState, error) { return nil, nil }
			msg, ok := m.probeHealthCmd()().(healthResultMsg)
			if !ok {
				t.Fatal("not a healthResultMsg")
			}
			if msg.expected != want || msg.running != 0 || !msg.counted {
				t.Errorf("got %+v, want expected %d, running 0, counted", msg, want)
			}
		})
	}
}

func TestSplashProbe_ServiceStatesErrorMeansNotCounted(t *testing.T) {
	m, _ := probeModel(t, "full")
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthNotResponding }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) { return nil, errors.New("docker unreachable") }
	want := healthResultMsg{reason: deploy.HealthNotResponding}
	if got := m.probeHealthCmd()(); got != want {
		t.Errorf("message = %+v, want %+v", got, want)
	}
}

func TestSplashProbe_NilProbeMeansNoCommand(t *testing.T) {
	m, _ := probeModel(t, "full")
	m.healthProbe = nil
	if cmd := m.probeHealthCmd(); cmd != nil {
		t.Error("probeHealthCmd should be nil when healthProbe is nil")
	}
}

func TestSplashProbe_PassesTheAppURLToTheProbe(t *testing.T) {
	m, _ := probeModel(t, "")
	var got string
	m.healthProbe = func(_ context.Context, u string) deploy.HealthReason { got = u; return deploy.HealthResponding }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) { return nil, nil }
	m.probeHealthCmd()()
	if got != m.appURL {
		t.Errorf("probe asked about %q, want %q", got, m.appURL)
	}
}
