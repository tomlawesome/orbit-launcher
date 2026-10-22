package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

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
	// APP_URL is read from the deployment's own env file, so it is treated
	// as text, not parsed: the host is what lies between the scheme's "//"
	// and the first "/", "?" or "#" after the last "@", so nothing before
	// an "@" (an account part, whatever characters it holds) ever reaches
	// the screen. url.Parse is not used: it reads "admin:1234#x@host" as
	// host "admin:1234" and a fragment, which is RFC 3986's reading and
	// shows the account part. A value this broken may name the wrong
	// host, as may an "@" in a path; neither shows an account part. Shown
	// escaped, so a CR or ESC in it cannot forge or restyle the confirm
	// screen.
	_, host := splitAccount(d.AppURL)
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	return visibleText(host)
}

// splitAccount reads an APP_URL as text the way deploymentIdentity does:
// scheme is everything up to and including "://" (or a leading "//"),
// and rest is what follows it with everything up to the last "@" cut
// away, so an account part, whatever characters it holds, is never in
// either.
func splitAccount(appURL string) (scheme, rest string) {
	rest = appURL
	if i := strings.Index(rest, "://"); i >= 0 {
		scheme, rest = rest[:i+3], rest[i+3:]
	} else if strings.HasPrefix(rest, "//") {
		scheme, rest = "//", rest[2:]
	}
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		rest = rest[i+1:]
	}
	return scheme, rest
}

// withoutAccount is appURL with its user name and password cut out by
// splitAccount's reading, scheme and path kept: the success screen's
// address (#277). Not escaped; the caller escapes for the screen.
func withoutAccount(appURL string) string {
	scheme, rest := splitAccount(appURL)
	return scheme + rest
}

// identityLine is the confirm screens' one line of deployment identity:
// deploymentIdentity, and the install date only when Docker has said
// what it is. It is always one row of at most limit cells (0 means no
// limit): a host too wide for the row is cut on grapheme clusters and
// ends in "…", and the date stays whole, since the host is only that
// wide when its settings file is corrupt (#271).
func identityLine(d *deploy.Deployment, installedAt time.Time, limit int) string {
	identity := deploymentIdentity(d)
	suffix := ""
	if d != nil && !installedAt.IsZero() {
		suffix = style.Separator + "installed " + installedAt.Format("2006-01-02")
	}
	if limit > 0 && lipgloss.Width(identity+suffix) > limit {
		identity = xansi.Truncate(identity, max(limit-lipgloss.Width(suffix)-1, 0), "") + "…"
	}
	return identity + suffix
}
