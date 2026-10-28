package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// #302: a deployment Orbit reports as closed to users for maintenance is
// shown as "maintenance" in yellow, with one faint line saying why. It is
// not degraded: no status row, no drawer, and no service-state check.

// maintenanceYellow is the status colour for a deployment closed to users.
var maintenanceYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#facc15"))

const maintenanceNote = "closed to users"

func maintenanceSplash(t *testing.T) SplashModel {
	t.Helper()
	return degradedWith(t, deploy.HealthMaintenance)
}

// rawRow is the row of the view (colour escapes kept) holding sub.
func rawRow(t *testing.T, m SplashModel, sub string) string {
	t.Helper()
	for _, l := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no row with %q:\n%s", sub, screen(m))
	return ""
}

func TestSplashMaintenance_StatusWordIsMaintenance(t *testing.T) {
	s := screen(maintenanceSplash(t))
	if !strings.Contains(s, "maintenance") {
		t.Errorf("screen lacks the word maintenance:\n%s", s)
	}
	for _, other := range []string{"alive", "degraded"} {
		if strings.Contains(s, other) {
			t.Errorf("screen shows %q for a deployment closed for maintenance:\n%s", other, s)
		}
	}
}

func TestSplashMaintenance_StatusWordIsYellow(t *testing.T) {
	m := maintenanceSplash(t)
	row := rawRow(t, m, "maintenance")
	want := maintenanceYellow.Render("maintenance")
	if want == "maintenance" {
		t.Fatal("the yellow style applies no colour at all")
	}
	if !strings.Contains(row, want) {
		t.Errorf("the word is not drawn in yellow #facc15: want %q in %q", want, row)
	}
	for name, other := range map[string]string{
		"green": style.SuccessText.Render("maintenance"),
		"amber": style.DegradedText.Render("maintenance"),
	} {
		if strings.Contains(row, other) {
			t.Errorf("the word is drawn in %s", name)
		}
	}
}

func TestSplashMaintenance_MarkIsYellow(t *testing.T) {
	m := maintenanceSplash(t)
	view := m.View().Content
	want := maintenanceYellow.Render(style.SymbolMark)
	if !strings.Contains(view, want) {
		t.Errorf("the mark is not drawn in yellow #facc15 (state colours outrank brand):\n%q", view)
	}
}

func TestSplashMaintenance_FaintLineSaysClosedToUsers(t *testing.T) {
	m := maintenanceSplash(t)
	if string(deploy.HealthMaintenance) != maintenanceNote {
		t.Fatalf("HealthMaintenance = %q, want %q", string(deploy.HealthMaintenance), maintenanceNote)
	}
	row := rawRow(t, m, maintenanceNote)
	if want := style.MutedText.Render(maintenanceNote); !strings.Contains(row, want) {
		t.Errorf("the line is not faint (muted): want %q in %q", want, row)
	}
	if n := strings.Count(screen(m), maintenanceNote); n != 1 {
		t.Errorf("%q appears %d times, want once:\n%s", maintenanceNote, n, screen(m))
	}
}

func TestSplashMaintenance_FaintLineIsDirectlyBeneathTheWord(t *testing.T) {
	s := screen(maintenanceSplash(t))
	word, note := lineIndex(s, "maintenance"), lineIndex(s, maintenanceNote)
	if word < 0 || note != word+1 {
		t.Errorf("word on row %d, note on row %d, want the note on the very next row:\n%s", word, note, s)
	}
	assertCentredWith(t, s, maintenanceNote, "maintenance")
	assertCentredWith(t, s, maintenanceNote, "O R B I T")
}

func TestSplashMaintenance_NoStatusRowSoUpFromInstallWrapsToExit(t *testing.T) {
	m := onInstall(t, maintenanceSplash(t))
	m, _ = updateAs(t, m, key(tea.KeyUp))
	assertCaretOn(t, screen(m), "Exit")
}

func TestSplashMaintenance_NoChevronAndNoDrawer(t *testing.T) {
	var m SplashModel
	for _, k := range []tea.KeyPressMsg{key(tea.KeyRight), key(tea.KeyEnter)} {
		m, _ = updateAs(t, onInstall(t, maintenanceSplash(t)), key(tea.KeyUp), k)
		s := screen(m)
		if strings.Contains(s, "›") || strings.Contains(s, "⌄") {
			t.Errorf("a status row chevron is drawn:\n%s", s)
		}
		if n := strings.Count(s, maintenanceNote); n != 1 {
			t.Errorf("%q appears %d times, want once (no drawer):\n%s", maintenanceNote, n, s)
		}
	}
	if l, _ := lineWith(screen(m), "maintenance"); strings.Contains(l, "›") || strings.Contains(l, "⌄") {
		t.Errorf("the word row has a chevron: %q", l)
	}
}

func TestSplashMaintenance_PreselectsUpdateLikeAlive(t *testing.T) {
	m := maintenanceSplash(t)
	if m.menu.sel != menuUpdate {
		t.Errorf("selected = %d, want Update", m.menu.sel)
	}
	if m.userNavigated {
		t.Error("userNavigated set by a probe result")
	}
	alive := degradedWith(t, deploy.HealthResponding)
	if m.menu.sel != alive.menu.sel {
		t.Errorf("selected = %d, alive selects %d", m.menu.sel, alive.menu.sel)
	}
	assertCaretOn(t, screen(m), "Update")
}

func TestSplashMaintenance_ResultDoesNotMoveACaretTheUserPlaced(t *testing.T) {
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26}, runeKey('4'))
	m, _ = updateAs(t, m, healthResultMsg{reason: deploy.HealthMaintenance})
	if m.menu.sel != 3 {
		t.Errorf("selected = %d, want 3", m.menu.sel)
	}
	if !m.userNavigated {
		t.Error("userNavigated was cleared by the result")
	}
}

func TestSplashMaintenance_ServiceStatesAreNotChecked(t *testing.T) {
	m, _ := probeModel(t, "full")
	var probes, serviceCalls int
	m.healthProbe = func(context.Context, string) deploy.HealthReason {
		probes++
		return deploy.HealthMaintenance
	}
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		serviceCalls++
		return nil, nil
	}
	msg, ok := m.probeHealthCmd()().(healthResultMsg)
	if !ok {
		t.Fatal("not a healthResultMsg")
	}
	if probes != 1 {
		t.Errorf("health probe ran %d times, want 1", probes)
	}
	if serviceCalls != 0 {
		t.Errorf("service states asked %d times, want 0 for maintenance", serviceCalls)
	}
	if msg.reason != deploy.HealthMaintenance || msg.counted {
		t.Errorf("message = %+v, want maintenance and not counted", msg)
	}
}

func TestSplashMaintenance_ServiceStatesAreStillCheckedWhenDegraded(t *testing.T) {
	// Control: the seam counts calls, so zero above means something.
	m, _ := probeModel(t, "full")
	var serviceCalls int
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthServerError }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		serviceCalls++
		return nil, nil
	}
	m.probeHealthCmd()()
	if serviceCalls != 1 {
		t.Errorf("service states asked %d times for a degraded result, want 1", serviceCalls)
	}
}

func TestSplashMaintenance_AliveControlStillSaysAliveInGreen(t *testing.T) {
	m := degradedWith(t, deploy.HealthResponding)
	s := screen(m)
	if !strings.Contains(s, "alive") || strings.Contains(s, "maintenance") || strings.Contains(s, maintenanceNote) {
		t.Errorf("alive screen wrong:\n%s", s)
	}
	if row := rawRow(t, m, "alive"); !strings.Contains(row, style.SuccessText.Render("alive")) {
		t.Errorf("alive is not green: %q", row)
	}
	if strings.Contains(m.View().Content, maintenanceYellow.Render(style.SymbolMark)) {
		t.Error("an alive deployment draws a yellow mark")
	}
}

func TestSplashMaintenance_DegradedControlIsUnchanged(t *testing.T) {
	m := degradedWith(t, deploy.HealthNotResponding)
	s := screen(m)
	if !strings.Contains(s, "degraded") || strings.Contains(s, "maintenance") || strings.Contains(s, maintenanceNote) {
		t.Errorf("degraded screen wrong:\n%s", s)
	}
	if row := rawRow(t, m, "degraded"); !strings.Contains(row, style.DegradedText.Render("degraded")) {
		t.Errorf("degraded is not amber: %q", row)
	}
}
