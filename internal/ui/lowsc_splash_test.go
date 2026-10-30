package ui

import (
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// #277 owner answer 4a (2026-10-25): when the health check could not run at
// all (reason deploy.HealthUnchecked, e.g. the address is not http/https),
// the splash caret stays on the row it would be on for a healthy
// deployment; Repair is not preselected. A check that ran and failed still
// preselects Repair.

func TestLowsC_K_UncheckedHealthKeepsTheHealthyCaretRow(t *testing.T) {
	healthy := degradedWith(t, deploy.HealthResponding)
	m := degradedWith(t, deploy.HealthUnchecked)
	if m.menu.sel == menuRepair {
		t.Errorf("selected = Repair, want Repair not preselected when the check could not run")
	}
	if m.menu.sel != healthy.menu.sel {
		t.Errorf("selected = %d, want %d (the row for a healthy deployment)", m.menu.sel, healthy.menu.sel)
	}
}

// Control: a check that ran and failed still preselects Repair.
func TestLowsC_K_FailedHealthStillPreselectsRepair(t *testing.T) {
	for _, r := range []deploy.HealthReason{
		deploy.HealthNotResponding,
		deploy.HealthAddressNotFound,
		deploy.HealthServerError,
		deploy.HealthCertificateRejected,
	} {
		m := degradedWith(t, r)
		if m.menu.sel != menuRepair {
			t.Errorf("reason %q: selected = %d, want Repair (%d)", r, m.menu.sel, menuRepair)
		}
	}
}
