package ui

// Pins #267 FR3-2 (from #248): when the deployment's APP_URL cannot be
// read as an address (any control character does that), the Remove and
// Update confirm screens must not fall back to showing the whole value,
// because the value may carry an account part (user:password). Only the
// host is shown, escaped as visibleText escapes it, and nothing after the
// first "/" past the host. A parseable address with an account part must
// not show it either, and an ordinary address reads exactly as before.
// Every absence check is paired with a check that the host IS shown, so
// none can pass because the whole row is missing.

import (
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

var credentialWidths = []int{50, 80, 120}

// credentialCases are addresses that carry an account part. wantHost is
// the host as it must read on screen; hidden are texts that must never
// appear anywhere on a confirm screen.
var credentialCases = []struct {
	name, appURL, wantHost string
	hidden                 []string
}{
	{
		name:     "unparseable (ESC in host)",
		appURL:   "https://user:secret@m.example\x1b[0m/path",
		wantHost: `m.example\x1b[0m`,
		hidden:   []string{"secret", "user:", "/path"},
	},
	{
		name:     "unparseable (CR in host), distinctive tail",
		appURL:   "https://user:secret@m.example\r/tailmarker?token=hunter2",
		wantHost: `m.example\r`,
		hidden:   []string{"secret", "user:", "tailmarker", "hunter2"},
	},
	{
		name:     "unparseable (control rune in the account part)",
		appURL:   "https://us\x1ber:secret@m.example/tailmarker",
		wantHost: "m.example",
		hidden:   []string{"secret", "user:", "tailmarker"},
	},
	{
		name:     "parseable",
		appURL:   "https://user:secret@mail.example.com/x",
		wantHost: "mail.example.com",
		hidden:   []string{"secret", "user:"},
	},
}

// FR3-2 (a). Neither confirm screen shows the account part or the tail of
// the address, with or without the install date, at three widths; the
// host is shown on both.
func TestIdentityCredentials_ConfirmScreensNeverShowTheAccountPart(t *testing.T) {
	dates := map[string]time.Time{
		"no date":   {},
		"with date": time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC),
	}
	for _, c := range credentialCases {
		for dname, at := range dates {
			for _, width := range credentialWidths {
				d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: c.appURL}
				for name, got := range identityConfirmScreens(t, d, at, width) {
					if !strings.Contains(got, c.wantHost) {
						t.Errorf("%s, %s, %s at %d columns: host %s is not shown:\n%q",
							c.name, dname, name, width, c.wantHost, got)
					}
					if r := identityStray(got); r != 0 {
						t.Errorf("%s, %s, %s at %d columns: %U reached the screen:\n%q",
							c.name, dname, name, width, r, got)
					}
					for _, h := range c.hidden {
						if strings.Contains(got, h) {
							t.Errorf("%s, %s, %s at %d columns: screen shows %q:\n%q",
								c.name, dname, name, width, h, got)
						}
					}
				}
			}
		}
	}
}

// FR3-2 (b). An ordinary address still reads as the bare host, at the
// narrow width too.
func TestIdentityCredentials_OrdinaryAppURLIsUnchanged(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}
	for _, width := range credentialWidths {
		for name, got := range identityConfirmScreens(t, d, at, width) {
			if !strings.Contains(got, "mail.example.com · installed 2026-10-20") {
				t.Errorf("%s at %d columns: want the host and date as before:\n%s", name, width, got)
			}
		}
		for name, got := range identityConfirmScreens(t, d, time.Time{}, width) {
			rows := identityRowsWith(got, "mail.example.com")
			if len(rows) != 1 || strings.Contains(rows[0], "installed") || strings.Contains(rows[0], "https://") {
				t.Errorf("%s at %d columns: want the bare host on one row, got %q", name, width, rows)
			}
		}
	}
}

// FR3-2 (c). deploymentIdentity itself drops the account part and the
// tail, keeps the escaped host, and returns nothing a terminal would act on.
func TestIdentityCredentials_DeploymentIdentityDropsTheAccountPart(t *testing.T) {
	for _, c := range credentialCases {
		t.Run(c.name, func(t *testing.T) {
			got := deploymentIdentity(&deploy.Deployment{AppURL: c.appURL})
			if !strings.Contains(got, c.wantHost) {
				t.Errorf("deploymentIdentity = %q, want it to hold the host %s", got, c.wantHost)
			}
			if r := identityStray(got); r != 0 {
				t.Errorf("deploymentIdentity returned %U: %q", r, got)
			}
			for _, h := range c.hidden {
				if strings.Contains(got, h) {
					t.Errorf("deploymentIdentity = %q, must not hold %q", got, h)
				}
			}
		})
	}
}
