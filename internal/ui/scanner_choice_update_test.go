package ui

// Blind tests for #340, Update's side (spec items 7 and 12): Update
// passes nothing to the engine, and its confirm screen names an off
// scanner as a read-only fact.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

const scanner340UpdateRow = "virus scanner off by choice"

func scanner340UpdateDeployment(scanMode string) *deploy.Deployment {
	d := testDeployment()
	d.ScanMode = scanMode
	return d
}

// Spec 7: an Update run saw no ORBIT_INSTALL_SCANNER at all, not even
// an empty one, whatever the record says.
func TestScanner340_UpdatePassesNoScannerVariable(t *testing.T) {
	for _, c := range []struct{ name, mode string }{
		{"scanner disabled in the record", deploy.ScanModeDisabled},
		{"key absent from the record", ""},
		{"scanner required in the record", "required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, out := scanner340Recorder(t)
			m := NewUpdateModel(scanner340UpdateDeployment(c.mode), t.TempDir(), "v9.9.9")
			s := newSink()
			m.send = s.Send
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
			m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Update Orbit
			if m.state != updateStateRunning || cmd == nil {
				t.Fatalf("Enter did not start the update (state %v):\n%s", m.state, screen(m))
			}
			drive(t, m, cmd, s)
			got := docs260Lines(t, out, "scanner")
			if len(got) != 1 || got[0] != "<unset>" {
				t.Errorf("the installer saw %s = %q, want one run with the variable unset", scanner340Env, got)
			}
		})
	}
}

// Spec 12: the confirm screen says so, faintly under the image row, only
// when the record says the scanner is off.
func TestScanner340_UpdateConfirmShowsAnOffScanner(t *testing.T) {
	m, _ := newTestUpdateModel(scanner340UpdateDeployment(deploy.ScanModeDisabled), engineRunSeams{})
	s := screen(m)
	rows := strings.Split(s, "\n")
	image := -1
	for i, r := range rows {
		if strings.Contains(r, "ghcr.io/tomlawesome") {
			image = i
		}
	}
	if image < 0 || image+1 >= len(rows) {
		t.Fatalf("setup: no image row on the confirm screen:\n%s", s)
	}
	if !strings.Contains(rows[image+1], scanner340UpdateRow) {
		t.Errorf("the row after the image row is %q, want %q:\n%s", rows[image+1], scanner340UpdateRow, s)
	}
	if !strings.Contains(m.View().Content, style.Tagline.Render(scanner340UpdateRow)) {
		t.Errorf("the row is not drawn in the faint style")
	}
}

func TestScanner340_UpdateConfirmShowsNothingWhenTheScannerIsOn(t *testing.T) {
	plain, _ := newTestUpdateModel(scanner340UpdateDeployment(""), engineRunSeams{})
	want := screen(plain)
	for _, mode := range []string{"", "required"} {
		m, _ := newTestUpdateModel(scanner340UpdateDeployment(mode), engineRunSeams{})
		got := screen(m)
		if strings.Contains(got, "virus scanner") {
			t.Errorf("ScanMode %q: the confirm screen speaks of the scanner:\n%s", mode, got)
		}
		if got != want {
			t.Errorf("ScanMode %q: the confirm screen differs from the one without the key:\n%s", mode, got)
		}
	}
}
