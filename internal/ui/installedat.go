package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// installedAtMsg carries the deployment's install date back from Docker;
// the zero time means Docker could not say, and the date is left out.
type installedAtMsg struct{ at time.Time }

// installedAtCmd looks up d's install date off the event loop. lookup is
// a test seam; nil means deploy.InstalledAt, which bounds its own Docker
// call. With no deployment there is nothing to look up.
func installedAtCmd(lookup func(context.Context, *deploy.Deployment) time.Time, d *deploy.Deployment) tea.Cmd {
	if d == nil {
		return nil
	}
	if lookup == nil {
		lookup = deploy.InstalledAt
	}
	return func() tea.Msg {
		return installedAtMsg{at: lookup(context.Background(), d)}
	}
}

// identityLine is the confirm screens' one line of deployment identity:
// the host, and the install date only when Docker has said what it is.
func identityLine(d *deploy.Deployment, installedAt time.Time) string {
	if d == nil || d.AppURL == "" {
		return "no deployment details found"
	}
	identity := displayHost(d.AppURL)
	if !installedAt.IsZero() {
		identity += " · installed " + installedAt.Format("2006-01-02")
	}
	return identity
}
