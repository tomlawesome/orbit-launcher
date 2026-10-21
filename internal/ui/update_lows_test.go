package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// 236-17: when .env-orbit has no APP_URL the Update run screen's title is
// a plain "Update", not "Update — deployment detected". (This reverses
// the no-address row of TestIdentity_UpdateTitleNamesTheDeployment in
// wi10_identity_test.go.) With an address the title keeps it.
func TestUpdate_TitleIsPlainWhenThereIsNoAddress(t *testing.T) {
	title := func(d *deploy.Deployment) string {
		m := NewUpdateModel(d, "/opt/orbit", "v9.9.9")
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
		updated, _ = updated.Update(key(tea.KeyEnter)) // Update Orbit
		if got := updated.(UpdateModel); got.state != updateStateRunning {
			t.Fatalf("state = %v, want updateStateRunning", got.state)
		}
		for _, row := range strings.Split(screen(updated), "\n") {
			if strings.Contains(row, "Update") {
				return strings.TrimSpace(row)
			}
		}
		t.Fatalf("no title row on the run screen:\n%s", screen(updated))
		return ""
	}

	plain := title(&deploy.Deployment{TargetDir: "/opt/orbit"})
	if strings.Contains(plain, "deployment detected") || strings.Contains(plain, "—") {
		t.Errorf("title with no APP_URL = %q, want a plain Update with no address", plain)
	}
	if f := strings.Fields(plain); len(f) < 3 || f[0] != "ORBIT" || f[2] != "Update" || (len(f) > 3 && f[3] == "—") {
		t.Errorf("title with no APP_URL = %q, want \"ORBIT · Update\" and nothing after it but the clock", plain)
	}
	if with := title(&deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}); !strings.Contains(with, "Update — mail.example.com") {
		t.Errorf("title with an APP_URL = %q, want it to keep the address", with)
	}
}
