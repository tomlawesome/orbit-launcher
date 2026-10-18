package ui

import (
	"context"
	"net/url"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
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

// deploymentIdentity names a deployment the one way every screen does
// (the splash, the Remove and Update confirm screens, Update's title):
// the bare host of its APP_URL, since scheme and path are launcher noise
// at a glance; "deployment detected" when its env file has no APP_URL;
// "no deployment details found" when there is no deployment at all.
func deploymentIdentity(d *deploy.Deployment) string {
	switch {
	case d == nil:
		return "no deployment details found"
	case d.AppURL == "":
		return "deployment detected"
	}
	if u, err := url.Parse(d.AppURL); err == nil && u.Host != "" {
		return u.Host
	}
	return d.AppURL
}

// identityLine is the confirm screens' one line of deployment identity:
// deploymentIdentity, and the install date only when Docker has said
// what it is.
func identityLine(d *deploy.Deployment, installedAt time.Time) string {
	identity := deploymentIdentity(d)
	if d != nil && !installedAt.IsZero() {
		identity += style.Separator + "installed " + installedAt.Format("2006-01-02")
	}
	return identity
}
