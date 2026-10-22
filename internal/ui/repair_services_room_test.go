package ui

// Pins #277 A3 and A5: the Repair diagnosis screen's services block is
// budgeted by the rows it draws.
//
//   - A3: whether the running services fold into one "✓ N running" row
//     depends on the rows the block takes: if the screen with the full list
//     is H rows tall, then at a terminal height of H + 1 (the last row is
//     left to the sky) the full list shows, unfolded.
//   - A5: on a terminal too short to give the block a row, the
//     "services · checking" row is not drawn, and the screen's summary and
//     menu stay on screen, as they do for the loaded block.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

var foldedRow = regexp.MustCompile(`✓ \d+ running`)

// contentRows counts the rows from the first to the last one that carries
// words (the sky's glyphs are not letters): the rows the screen takes.
func contentRows(s string) int {
	first, last := -1, -1
	for i, row := range strings.Split(s, "\n") {
		if strings.IndexFunc(row, unicode.IsLetter) >= 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return 0
	}
	return last - first + 1
}

func TestRepairServicesRoom_TheFullListShowsOneRowOverItsOwnHeight(t *testing.T) {
	fixtures := []struct {
		name    string
		have    []deploy.ServiceState
		wantRow int // rows the full list takes
	}{
		{"one faulted", append(up("orbit-db", "orbit-app", "orbit-clamav", "orbit-ollama"), deploy.ServiceState{Service: "orbit-tika", State: "restarting"}), 5},
		{"two missing", up("orbit-db", "orbit-app", "orbit-clamav"), 5},
	}
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			// A terminal with room to spare shows the whole list; its content
			// is the screen's height, H.
			tall := servicesModel(t, "full", 80, 60, fx.have, nil)
			if n := len(svcRows(t, tall)); n != fx.wantRow || foldedRow.MatchString(plain(tall)) {
				t.Fatalf("setup: the tall screen shows %d service rows (want the full %d, unfolded):\n%s", n, fx.wantRow, screen(tall))
			}
			h := contentRows(screen(tall))
			if h == 0 {
				t.Fatalf("setup: no content on the tall screen:\n%s", screen(tall))
			}

			m := servicesModel(t, "full", 80, h+1, fx.have, nil)
			s := screen(m)
			if foldedRow.MatchString(plain(m)) {
				t.Errorf("at height %d (the screen is %d rows tall) the running services are folded:\n%s", h+1, h, s)
			}
			if n := len(svcRows(t, m)); n != fx.wantRow {
				t.Errorf("at height %d (the screen is %d rows tall) %d service rows show, want the full %d:\n%s", h+1, h, n, fx.wantRow, s)
			}
			if !strings.Contains(s, "Exit") {
				t.Errorf("the menu is cut off at height %d:\n%s", h+1, s)
			}
		})
	}
}

// ---- A5 ----

// roomMarkers are the summary and menu words a diagnosis screen carries.
var roomMarkers = []string{"checked", "Menu", "Exit"}

func hasServicesRow(s string) bool {
	for _, row := range strings.Split(s, "\n") {
		if strings.Contains(row, "services") {
			return true
		}
	}
	return false
}

// checkingMatchesLoaded fails if, at this height, the checking screen
// draws a services row the loaded block would not, or loses a piece of the
// summary or menu the loaded screen keeps.
func checkingMatchesLoaded(t *testing.T, loaded, checking RepairModel, h int) {
	t.Helper()
	ls, cs := screen(loaded), screen(checking)
	if rows := len(strings.Split(strings.TrimRight(cs, "\n"), "\n")); rows > h {
		t.Errorf("height %d: the checking screen is %d rows:\n%s", h, rows, cs)
	}
	if !hasServicesRow(ls) {
		if _, drawn := checkingRow(cs); drawn {
			t.Errorf("height %d: no room for the services block, yet the checking row is drawn:\n%s", h, cs)
		}
	}
	for _, w := range roomMarkers {
		if strings.Contains(ls, w) && !strings.Contains(cs, w) {
			t.Errorf("height %d: the loaded screen shows %q but the checking one lost it:\n%s", h, w, cs)
		}
	}
	if hasServicesRow(ls) {
		if _, ok := checkingRow(cs); !ok {
			t.Errorf("height %d: the block has a row, yet no checking row is drawn:\n%s", h, cs)
		}
	}
}

func TestRepairServicesRoom_CheckingRespectsTheRowBudget(t *testing.T) {
	have := append(up("orbit-db", "orbit-app", "orbit-clamav", "orbit-ollama"), deploy.ServiceState{Service: "orbit-tika", State: "restarting"})
	for _, w := range c9Widths {
		for h := 1; h <= 40; h++ {
			loaded := servicesModel(t, "full", w, h, have, nil)
			checking := servicesModelUnread(t, "full", w, h, have, nil)
			_ = checking.readServices() // started, not answered
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				checkingMatchesLoaded(t, loaded, checking, h)
			})
		}
	}
}

// The same through the real flow: a healthy diagnosis lands, the read has
// started and not answered.
func TestRepairServicesRoom_CheckingRespectsTheRowBudgetInTheFlow(t *testing.T) {
	flow := func(t *testing.T, h int) (RepairModel, tea.Cmd) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile), []byte("APP_URL=http://localhost\nCOMPOSE_PROFILES=full\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := NewRepairModel(dir, "v0.6.0")
		m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: h})
		m.serviceStates = func(string) ([]deploy.ServiceState, error) {
			return append(up("orbit-db", "orbit-app", "orbit-clamav", "orbit-ollama"), deploy.ServiceState{Service: "orbit-tika", State: "restarting"}), nil
		}
		m = repairLines(t, m, "diagnosis result=healthy checked=15 skipped=0")
		return repairDone(t, m, engine.DoneMsg{ExitCode: engine.ExitHealthy})
	}
	for h := 1; h <= 40; h++ {
		checking, cmd := flow(t, h)
		loaded, cmd2 := flow(t, h)
		loaded = drive(t, loaded, cmd2)
		_ = cmd
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			checkingMatchesLoaded(t, loaded, checking, h)
		})
	}
}
