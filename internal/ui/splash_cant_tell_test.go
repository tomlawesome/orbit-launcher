package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// #341 (amended 2026-11-08, owner answer 2): when either half of the
// check is missing and no fault was seen, the splash says "can't tell".
// A seen fault (a container fault, or an HTTP failure) is still degraded.

const (
	cantTellWord   = "can't tell"
	cantTellDocker = "Docker couldn't be read"
	cantTellAddr   = "health address couldn't be worked out"
)

var (
	// Docker unreadable, the app answered.
	msgDockerUnread = healthResultMsg{reason: deploy.HealthResponding, asked: true}
	// The app was not asked, every container running.
	msgAddrUnworked = healthResultMsg{reason: deploy.HealthUnchecked, asked: true, counted: true, running: 3, expected: 3}
	// Both halves missing.
	msgBothMissing = healthResultMsg{reason: deploy.HealthUnchecked, asked: true}
)

func cantTellSplash(t *testing.T, msg healthResultMsg) SplashModel {
	t.Helper()
	m := degradedSplash(t, msg)
	if m.state != stateCantTell {
		t.Fatalf("state = %v, want can't tell for %+v", m.state, msg)
	}
	return m
}

// Case 6b: which messages are can't tell.
func TestSplashCantTell_State(t *testing.T) {
	fault1 := []deploy.ServiceFault{fault("orbit-tika", "stopped")}
	for name, c := range map[string]struct {
		msg   healthResultMsg
		state deployState
	}{
		"ready, docker unreadable":                {msgDockerUnread, stateCantTell},
		"maintenance, docker unreadable":          {healthResultMsg{reason: deploy.HealthMaintenance, asked: true}, stateCantTell},
		"could not check, all running":            {msgAddrUnworked, stateCantTell},
		"could not check, docker unreadable":      {msgBothMissing, stateCantTell},
		"could not check, no reader":              {healthResultMsg{reason: deploy.HealthUnchecked}, stateCantTell},
		"ready, no reader (harness)":              {healthResultMsg{reason: deploy.HealthResponding}, stateAlive},
		"maintenance, no reader (harness)":        {healthResultMsg{reason: deploy.HealthMaintenance}, stateMaintenance},
		"ready, all running":                      {healthResultMsg{reason: deploy.HealthResponding, asked: true, counted: true, running: 3, expected: 3}, stateAlive},
		"maintenance, all running":                {healthResultMsg{reason: deploy.HealthMaintenance, asked: true, counted: true, running: 3, expected: 3}, stateMaintenance},
		"could not check + fault is degraded":     {healthResultMsg{reason: deploy.HealthUnchecked, asked: true, counted: true, faults: fault1, running: 4, expected: 5}, stateDegraded},
		"server error, docker unreadable":         {healthResultMsg{reason: deploy.HealthServerError, asked: true}, stateDegraded},
		"unexpected answer, docker unreadable":    {healthResultMsg{reason: deploy.HealthUnexpectedAnswer, asked: true}, stateDegraded},
		"address not found, docker unreadable":    {healthResultMsg{reason: deploy.HealthAddressNotFound, asked: true}, stateDegraded},
		"certificate rejected, docker unreadable": {healthResultMsg{reason: deploy.HealthCertificateRejected, asked: true}, stateDegraded},
		"not responding, docker unreadable":       {healthResultMsg{reason: deploy.HealthNotResponding, asked: true}, stateDegraded},
	} {
		t.Run(name, func(t *testing.T) {
			if got := degradedSplash(t, c.msg).state; got != c.state {
				t.Errorf("state = %v, want %v", got, c.state)
			}
		})
	}
}

// Case 6b: a failure over unreadable Docker is degraded with Repair
// preselected and the HTTP reason in the drawer, no count row.
func TestSplashCantTell_EveryHTTPFailureOverUnreadableDockerIsDegraded(t *testing.T) {
	for _, tc := range allReasons {
		if tc.reason == deploy.HealthUnchecked {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			msg := healthResultMsg{reason: tc.reason, asked: true}
			if m := degradedSplash(t, msg); m.state != stateDegraded || m.menu.sel != menuRepair {
				t.Errorf("state = %v, selected = %d, want degraded with Repair", m.state, m.menu.sel)
			}
			s := openDegradedDrawer(t, msg)
			if !strings.Contains(s, tc.text) || strings.Contains(s, "services") {
				t.Errorf("want the reason %q and no count row:\n%s", tc.text, s)
			}
		})
	}
}

// Case 6b: could not check beside a fault is degraded, Repair preselected
// (the fault is not only starting), drawer rows in order.
func TestSplashCantTell_CouldNotCheckBesideAFaultIsDegraded(t *testing.T) {
	msg := healthResultMsg{
		reason: deploy.HealthUnchecked, asked: true, counted: true, running: 4, expected: 5,
		faults: []deploy.ServiceFault{fault("orbit-tika", "stopped")},
	}
	m := degradedSplash(t, msg)
	if m.state != stateDegraded || m.menu.sel != menuRepair {
		t.Errorf("state = %v, selected = %d, want degraded with Repair", m.state, m.menu.sel)
	}
	assertInOrder(t, openDegradedDrawer(t, msg), "could not check", "orbit-tika stopped", "4 of 5 services running")
}

// Case 6b: asked is true whenever the command had a reader, false when
// it had none; counted implies asked.
func TestSplashProbe_AskedFollowsTheReader(t *testing.T) {
	for name, c := range map[string]struct {
		reader  func(string) ([]deploy.ServiceState, error)
		asked   bool
		counted bool
	}{
		"no reader":    {nil, false, false},
		"reader error": {func(string) ([]deploy.ServiceState, error) { return nil, errors.New("docker unreachable") }, true, false},
		"reader ok":    {func(string) ([]deploy.ServiceState, error) { return allRunning(3), nil }, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := probeModel(t, "")
			m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding }
			m.serviceStates = c.reader
			msg, ok := m.probeHealthCmd()().(healthResultMsg)
			if !ok {
				t.Fatal("not a healthResultMsg")
			}
			if msg.asked != c.asked || msg.counted != c.counted {
				t.Errorf("asked = %v, counted = %v, want %v and %v", msg.asked, msg.counted, c.asked, c.counted)
			}
			if msg.counted && !msg.asked {
				t.Error("counted without asked")
			}
		})
	}
}

// Case 15: the identity block's lines.
func TestSplashCantTell_IdentityBlockLines(t *testing.T) {
	for name, c := range map[string]struct {
		msg  healthResultMsg
		want []string
		not  []string
	}{
		"docker unreadable":  {msgDockerUnread, []string{"mail.example.com", cantTellWord, cantTellDocker}, []string{cantTellAddr}},
		"address unworked":   {msgAddrUnworked, []string{"mail.example.com", cantTellWord, cantTellAddr}, []string{cantTellDocker}},
		"both, docker first": {msgBothMissing, []string{"mail.example.com", cantTellWord, cantTellDocker, cantTellAddr}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			s := screen(cantTellSplash(t, c.msg))
			assertInOrder(t, s, c.want...)
			// Each line sits directly beneath the previous one.
			for i := 1; i < len(c.want); i++ {
				if lineIndex(s, c.want[i]) != lineIndex(s, c.want[i-1])+1 {
					t.Errorf("%q is not directly beneath %q:\n%s", c.want[i], c.want[i-1], s)
				}
			}
			for _, absent := range append(c.not, "closed to users", "›", "⌄", "could not check", "services running", "alive", "degraded", "maintenance") {
				if strings.Contains(s, absent) {
					t.Errorf("screen shows %q:\n%s", absent, s)
				}
			}
		})
	}
}

// Case 15: the texts are exact and pass the wording rule's contraction scan.
func TestSplashCantTell_TextsAreExactAndContracted(t *testing.T) {
	for _, text := range []string{"can't tell", "Docker couldn't be read", "health address couldn't be worked out"} {
		if hits := spelledOutForms(text); len(hits) > 0 {
			t.Errorf("%q spells out %v", text, hits)
		}
	}
	if cantTellWord != "can't tell" || cantTellDocker != "Docker couldn't be read" || cantTellAddr != "health address couldn't be worked out" {
		t.Error("test constants drifted from the spec's texts")
	}
}

// Case 15: gold mark, as dormant; faint word, as dormant's.
func TestSplashCantTell_MarkIsGoldAndWordIsFaint(t *testing.T) {
	m := cantTellSplash(t, msgBothMissing)
	dormant, _ := updateAs(t, settled(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26})
	gold := style.MarkStyle.Render(style.SymbolMark)
	if gold == style.SuccessText.Render(style.SymbolMark) || gold == style.DegradedText.Render(style.SymbolMark) {
		t.Fatal("setup: the gold mark renders like a state colour")
	}
	if row := rawRow(t, dormant, style.SymbolMark); !strings.Contains(row, gold) {
		t.Fatalf("setup: the dormant mark is not style.MarkStyle: %q", row)
	}
	row := rawRow(t, m, style.SymbolMark)
	if !strings.Contains(row, gold) {
		t.Errorf("the mark is not gold (style.MarkStyle): want %q in %q", gold, row)
	}
	for name, other := range map[string]string{
		"alive":       style.SuccessText.Render(style.SymbolMark),
		"degraded":    style.DegradedText.Render(style.SymbolMark),
		"maintenance": style.MaintenanceText.Render(style.SymbolMark),
	} {
		if strings.Contains(row, other) {
			t.Errorf("the mark wears the %s colour: %q", name, row)
		}
	}
	faint, muted := style.Tagline.Render(cantTellWord), style.MutedText.Render(cantTellWord)
	if faint == muted {
		t.Fatal("setup: Tagline and MutedText render alike")
	}
	for _, text := range []string{cantTellWord, cantTellDocker, cantTellAddr} {
		r := rawRow(t, m, text)
		if !strings.Contains(r, style.Tagline.Render(text)) {
			t.Errorf("%q is not faint (style.Tagline): %q", text, r)
		}
	}
}

// Case 15: the lines are centred on the axis like the FQDN.
func TestSplashCantTell_LinesAreCentredOnTheAxis(t *testing.T) {
	s := screen(cantTellSplash(t, msgBothMissing))
	for _, text := range []string{cantTellWord, cantTellDocker, cantTellAddr} {
		assertCentredWith(t, s, text, "O R B I T")
		assertCentredWith(t, s, text, "mail.example.com")
	}
}

// Case 16: keys. No status row, so Up from the first menu row wraps to
// Exit and nothing opens.
func TestSplashCantTell_UpStaysOnTheMenuAndRightOpensNothing(t *testing.T) {
	m := onInstall(t, cantTellSplash(t, msgBothMissing))
	before := screen(m)
	m, _ = updateAs(t, m, key(tea.KeyUp))
	if m.onStatusRow {
		t.Error("Up reached a status row")
	}
	assertCaretOn(t, screen(m), "Exit")
	m, _ = updateAs(t, onInstall(t, cantTellSplash(t, msgBothMissing)), key(tea.KeyRight))
	if m.onStatusRow || m.drawerOpen || m.Chosen != "" {
		t.Errorf("Right: onStatusRow = %v, drawerOpen = %v, chosen = %q, want none", m.onStatusRow, m.drawerOpen, m.Chosen)
	}
	if got := screen(m); got != before {
		t.Errorf("Right changed the screen:\n%s\n--- was ---\n%s", got, before)
	}
}

func TestSplashCantTell_EnterChoosesTheMenuRowNotADrawer(t *testing.T) {
	m := cantTellSplash(t, msgBothMissing)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.drawerOpen || m.onStatusRow {
		t.Error("Enter opened a drawer or a status row")
	}
	if m.Chosen != MainMenu[menuUpdate].Label {
		t.Errorf("chosen = %q, want the caret's row %q", m.Chosen, MainMenu[menuUpdate].Label)
	}
}

// Case 16: the result moves no caret.
func TestSplashCantTell_MovesNoCaret(t *testing.T) {
	msgs := map[string]healthResultMsg{"docker": msgDockerUnread, "address": msgAddrUnworked, "both": msgBothMissing}
	for name, msg := range msgs {
		t.Run(name+" leaves Update", func(t *testing.T) {
			if got := cantTellSplash(t, msg).menu.sel; got != menuUpdate {
				t.Errorf("selected = %d, want Update (%d)", got, menuUpdate)
			}
		})
		t.Run(name+" leaves a placed caret", func(t *testing.T) {
			m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26}, runeKey('4'))
			m, _ = updateAs(t, m, msg)
			if m.state != stateCantTell || m.menu.sel != 3 {
				t.Errorf("state = %v, selected = %d, want can't tell with the caret still on 3", m.state, m.menu.sel)
			}
		})
		t.Run(name+" keeps Exit for another folder's containers", func(t *testing.T) {
			m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26})
			m.targetDir = "/opt/orbit"
			folder := runningResultMsg{dir: m.targetDir, mismatch: folderMismatch(otherProduction)}
			for _, order := range [][]tea.Msg{{msg, folder}, {folder, msg}} {
				got, _ := updateAs(t, m, order...)
				if got.state != stateCantTell || got.menu.sel != menuExit {
					t.Errorf("state = %v, selected = %d, want can't tell with the caret on Exit", got.state, got.menu.sel)
				}
			}
		})
	}
}

// Case 17: the 24-row ceiling for can't tell with both lines and the
// folder row. The identity block, FQDN through the last line, takes no
// more rows than the degraded worst case of 9.
func TestSplashCantTell_BothLinesKeepTheMenuOnScreen(t *testing.T) {
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 24})
	m.targetDir = "/opt/orbit"
	m, _ = updateAs(t, m, msgBothMissing)
	if m.state != stateCantTell {
		t.Fatalf("state = %v, want can't tell", m.state)
	}
	s := screen(m)
	for _, item := range MainMenu {
		if !strings.Contains(s, item.Label) {
			t.Errorf("menu item %s is off screen:\n%s", item.Label, s)
		}
	}
	for _, text := range []string{cantTellWord, cantTellDocker, cantTellAddr, "/opt/orbit"} {
		if !strings.Contains(s, text) {
			t.Errorf("screen lacks %q:\n%s", text, s)
		}
	}
	if got := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); got > 26 {
		t.Errorf("screen is %d rows, want at most 26", got)
	}
	if rows := lineIndex(s, "/opt/orbit") - lineIndex(s, "mail.example.com") + 1; rows > 9 {
		t.Errorf("identity block takes %d rows, want at most 9:\n%s", rows, s)
	}
}
