package ui

import (
	"strings"
	"testing"
	"time"
)

// #336, pending state: uses only symbols that exist on the base, so it
// runs there. Before the lookup answers the screen is today's, byte for
// byte (the c9 golden stays).

func TestUpdateConfirm_PendingNamesNoVersionAndNoRelease(t *testing.T) {
	d := testDeployment()
	d.Version = "v0.3.5"
	m, _ := newTestUpdateModel(d, engineRunSeams{})
	m = func() UpdateModel {
		u, _ := m.Update(installedAtMsg{at: time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)})
		return u.(UpdateModel)
	}()

	got := screen(m)
	for _, banned := range []string{"v0.3.5", "current release", "Version not recorded", "Update Orbit from", "checking which Orbit release"} {
		if strings.Contains(got, banned) {
			t.Errorf("pending screen holds %q:\n%s", banned, got)
		}
	}
	if !strings.Contains(got, "Pull the latest Orbit and update this deployment") {
		t.Errorf("pending screen lost today's title:\n%s", got)
	}
	if !strings.Contains(got, "ghcr.io/tomlawesome/orbit") {
		t.Errorf("pending screen lost the image row:\n%s", got)
	}
}
