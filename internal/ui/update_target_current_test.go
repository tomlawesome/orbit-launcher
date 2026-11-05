package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/release"
)

// #336 case D, owner answer 17b: installed equals the current release, so
// Update is not offered (Repair is the refresh). The screen is a dead end
// in case E's grammar. Written to fail until the code stops using the
// case A shape for D.

var currentTarget = release.Target{Version: "v0.3.6", Pin: true}

func TestUpdateConfirm_CaseD_InstalledIsCurrentIsADeadEnd(t *testing.T) {
	called := false
	m := answeredModel(t, versionedDeployment("v0.3.6"), 80, refusingSeams(&called), currentTarget, nil)
	s := screen(m)
	for _, want := range []string{
		"Orbit v0.3.6 is already the current release",
		"There is no newer release to update to.",
		"To check or fix this deployment, use Repair.",
		"nothing has been changed, and nothing will be by this screen",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if got := screenLabels(s); !slices.Equal(got, []string{"Menu"}) {
		t.Errorf("rows = %q, want just Menu", got)
	}
	if strings.Contains(s, "Update Orbit") {
		t.Errorf("Update Orbit is offered or named:\n%s", s)
	}
}

func TestUpdateConfirm_CaseD_EnterAndEscGoToTheMenuAndNeverStartTheEngine(t *testing.T) {
	called := false
	m := answeredModel(t, versionedDeployment("v0.3.6"), 80, refusingSeams(&called), currentTarget, nil)
	for name, k := range map[string]tea.KeyPressMsg{"Enter": key(tea.KeyEnter), "Esc": key(tea.KeyEsc)} {
		u, cmd := m.Update(k)
		if cmd != nil {
			cmd()
		}
		got := u.(UpdateModel)
		if got.state == updateStateRunning {
			t.Errorf("%s: the run started", name)
		}
		if o := got.Outcome(); !leftForMenu(o) {
			t.Errorf("%s: outcome = %+v, want back to the menu", name, o)
		}
	}
	if called {
		t.Error("the engine started from the dead end")
	}
}

func TestUpdateConfirm_CaseD_EnterWhilePendingThenCurrentAnswerStartsNothing(t *testing.T) {
	called := false
	m := pendingTargetModel(t, &called)
	m.deployment.Version = "v0.3.6"
	u, _ := m.Update(key(tea.KeyEnter))
	u, cmd := u.Update(targetMsg{target: currentTarget})
	if cmd != nil {
		cmd()
	}
	got := u.(UpdateModel)
	if got.state == updateStateRunning || called {
		t.Fatalf("state = %v, started = %v; want no run", got.state, called)
	}
	s := screen(got)
	if !strings.Contains(s, "Orbit v0.3.6 is already the current release") {
		t.Errorf("the dead end is not showing:\n%s", s)
	}
	if got := screenLabels(s); !slices.Equal(got, []string{"Menu"}) {
		t.Errorf("rows = %q, want just Menu", got)
	}
}
