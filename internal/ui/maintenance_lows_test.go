package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

func TestParseMaintenanceEnd_OverflowAndRepeatedUnitsAreRefused(t *testing.T) {
	loc := london(t)
	now := at(loc, 2025, 7, 1, 14, 5)
	// 99999d x 4 wraps a Duration sum back into range if units may repeat.
	for _, in := range []string{
		strings.Repeat("99999d", 400), "99999d99999d99999d99999d99999d99999d", "1h1h", "30m1h", "1m1d", "1d1d",
		"99999d", "8d", "169h", "10081m", "99999h99999m",
	} {
		if got, ok := parseMaintenanceEnd(in, now, loc); ok {
			t.Errorf("%q accepted as %v", in, got)
		}
	}
	if _, ok := parseMaintenanceEnd("1d2h3m", now, loc); !ok {
		t.Error("1d2h3m refused")
	}
}

func TestMaintenanceModel_KeepsOnlyTheLast40OutputLines(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m.state, m.start = maintWorking, true
	for i := 0; i < 500; i++ {
		m, _ = updateAs(t, m, maintStreamMsg{msg: engine.RawLineMsg{Text: fmt.Sprintf("line %d", i)}})
	}
	if len(m.lines) != 40 || m.lines[39] != "line 499" || m.lines[0] != "line 460" {
		t.Fatalf("kept %d lines, first %q", len(m.lines), m.lines[0])
	}
}

func TestMaintenanceModel_SuccessLineWithFailingExitIsNotTheDatabaseHint(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m.state, m.start = maintWorking, true
	m, _ = updateAs(t, m, maintStreamMsg{msg: engine.RawLineMsg{Text: "maintenance action=start result=started until=none"}})
	m, _ = updateAs(t, m, maintStreamMsg{msg: engine.DoneMsg{ExitCode: 1}})
	has(t, m, "Couldn't close Orbit")
	lacks(t, m, "database must be running")
}

func TestMaintenanceModel_ActionsAreOfferedPerScript(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end bool
		rows       []string
	}{
		{"only end", false, true, []string{"Reopen Orbit", "Cancel"}},
		{"only start", true, false, []string{"Close Orbit for maintenance", "Cancel"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, d := maintDeploymentWith(t, tc.start, tc.end)
			m := NewMaintenanceModel(d, dir, "v9.9.9")
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
			if got := maintRows(screen(m)); !slices.Equal(got, tc.rows) {
				t.Fatalf("rows = %q, want %q", got, tc.rows)
			}
			lacks(t, m, "needs a newer Orbit")
		})
	}
}

func TestMaintenanceModel_NewerScreenOnlyPrintsACommandForAScriptThatExists(t *testing.T) {
	dir, d := maintDeployment(t, false)
	m := NewMaintenanceModel(d, dir, "v9.9.9")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	lacks(t, m, "bash scripts/")
}
