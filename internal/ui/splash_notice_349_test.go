package ui

// #349 (layout decision, 2026-11-16): the splash's update notice is two
// rows and carries no command; the command lives on Update's confirm
// screen. Written from the decision note alone.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const noticeSecondRow349 = "re-run Orbit's installer to get it · the command is under Update"

// withNotice349 hands m the splash's update notice for v9.9.9.
func withNotice349(t *testing.T, m AppModel) AppModel {
	t.Helper()
	m, _ = updateAs(t, m, updateAvailableMsg{version: "v9.9.9"})
	return m
}

// noticeStates349 builds, at 80x26, every splash state the splash tests
// build, each with the update notice set.
func noticeStates349(t *testing.T) map[string]AppModel {
	t.Helper()
	states := map[string]AppModel{}
	// Nothing installed here.
	states["nothing installed"] = withNotice349(t, started(t, folderApp(t, shortTempDir(t), 80, 26, deploy.HealthResponding)))
	// Installed, each health answer.
	dir := installedFolder(t, "ORBIT_CONFIG_APPLIED_VERSION=v1.2.0")
	states["alive"] = withNotice349(t, started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding)))
	for _, r := range allReasons {
		m := withNotice349(t, started(t, folderApp(t, dir, 80, 26, r.reason)))
		states["degraded "+r.name] = m
		d, _ := updateAs(t, m, key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyUp))
		d, _ = updateAs(t, d, key(tea.KeyRight))
		states["degraded "+r.name+" drawer"] = d
	}
	// Running-check mismatches.
	for name, mm := range map[string]deploy.Mismatch{
		"another folder": folderMismatch(otherProduction),
		"another build":  buildMismatch("v0.3.0", "v1.2.0"),
	} {
		states["alive, "+name] = answer(t, withNotice349(t, started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))), dir, mm)
		states["degraded, "+name] = answer(t, withNotice349(t, started(t, folderApp(t, dir, 80, 26, deploy.HealthNotResponding))), dir, mm)
	}
	// The #341 worst case: drawer open, five faults (full profile) or
	// three (default), the build-mismatch rows.
	for name, profile := range map[string][]string{"default profile": nil, "full profile": {"COMPOSE_PROFILES=full"}} {
		d := installedFolder(t, append([]string{"ORBIT_CONFIG_APPLIED_VERSION=v1.2.0"}, profile...)...)
		m := folderApp(t, d, 80, 26, deploy.HealthNotResponding, func(m AppModel) AppModel {
			return m.WithServicesReader(func(string) ([]deploy.ServiceState, error) { return nil, nil })
		})
		m = withNotice349(t, started(t, m))
		m = answer(t, m, d, buildMismatch("v0.3.0", "v1.2.0"))
		m, _ = updateAs(t, m, key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyUp))
		m, _ = updateAs(t, m, key(tea.KeyRight))
		states["worst case, "+name] = m
		m2 := answer(t, m, d, folderMismatch(otherProduction))
		states["worst case with folder row, "+name] = m2
	}
	return states
}

func TestSplashNotice349_NoRowWiderThan80InAnyState(t *testing.T) {
	for name, m := range noticeStates349(t) {
		t.Run(name, func(t *testing.T) {
			s := screen(m)
			if !strings.Contains(s, "update available: v9.9.9") {
				t.Fatalf("setup: no notice on the screen:\n%s", s)
			}
			for _, r := range rowsOf(s) {
				if w := lipgloss.Width(r); w > 80 {
					t.Errorf("a row is %d cells, want at most 80: %q", w, r)
				}
			}
		})
	}
}

func TestSplashNotice349_NoCommandAndPointsToUpdate(t *testing.T) {
	for name, m := range noticeStates349(t) {
		t.Run(name, func(t *testing.T) {
			s := screen(m)
			if strings.Contains(s, "curl") {
				t.Errorf("the splash shows the command:\n%s", s)
			}
			if !strings.Contains(s, "under Update") {
				t.Errorf("the splash does not point to Update:\n%s", s)
			}
		})
	}
}

func TestSplashNotice349_SecondRowReadsExactly(t *testing.T) {
	for name, m := range noticeStates349(t) {
		t.Run(name, func(t *testing.T) {
			s := screen(m)
			i := lineIndex(s, "update available: v9.9.9")
			rows := rowsOf(s)
			if i < 0 || i+1 >= len(rows) {
				t.Fatalf("no notice rows:\n%s", s)
			}
			if got := strings.TrimSpace(rows[i+1]); got != noticeSecondRow349 {
				t.Errorf("the notice's second row is %q, want %q", got, noticeSecondRow349)
			}
		})
	}
}

// The #286/#341 gap-yield case still holds with the two-row notice: the
// gap before Remove is the only row that gives way, and the block fills
// the 25-row body exactly on the full profile.
func TestSplashNotice349_GapYieldStillHolds(t *testing.T) {
	states := noticeStates349(t)
	for _, tc := range []struct {
		name  string
		block int
		gap   bool
	}{
		{"worst case, default profile", 24, true},
		{"worst case, full profile", 25, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := screen(states[tc.name])
			if n := len(rowsOf(s)); n > 26 {
				t.Errorf("the screen is %d rows, want at most 26", n)
			}
			top := lineIndex(s, "O R B I T") - 2
			exit := lineIndex(s, "Exit")
			if block := exit - top + 1; block != tc.block {
				t.Errorf("the centre block is %d rows, want %d:\n%s", block, tc.block, s)
			}
			if exit >= 25 {
				t.Errorf("Exit is on row %d, want it inside the 25-row body:\n%s", exit, s)
			}
			if gap := lineIndex(s, "Remove")-lineIndex(s, "Repair") == 2; gap != tc.gap {
				t.Errorf("a blank row before Remove is %v, want %v:\n%s", gap, tc.gap, s)
			}
		})
	}
}
