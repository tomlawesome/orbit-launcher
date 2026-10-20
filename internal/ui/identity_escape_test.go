package ui

// Pins #248 FR2-3: the deployment's APP_URL is the settings file's text,
// not the launcher's, so the Remove and Update confirm screens must show
// control and format runes as Go escapes (visibleText) and never let one
// reach the terminal. An ordinary address shows exactly as before.

import (
	"io"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

var identityWidths = []int{80, 120}

// identityConfirmScreens draws both confirm screens for d at width, once
// the install date (zero = not yet known) has arrived.
func identityConfirmScreens(t *testing.T, d *deploy.Deployment, installedAt time.Time, width int) map[string]string {
	t.Helper()
	msgs := []tea.Msg{tea.WindowSizeMsg{Width: width, Height: 40}}
	if !installedAt.IsZero() {
		msgs = append(msgs, installedAtMsg{at: installedAt})
	}

	rm := NewRemoveModel(d)
	rm.clipboard = io.Discard
	rm, _ = updateAs(t, rm, msgs...)
	if rm.state != removeStateConfirm {
		t.Fatalf("remove state = %v, want the confirm screen", rm.state)
	}

	um, _ := newTestUpdateModel(d, engineRunSeams{})
	um, _ = updateAs(t, um, msgs...)
	if um.state != updateStateConfirm {
		t.Fatalf("update state = %v, want the confirm screen", um.state)
	}
	return map[string]string{"Remove": screen(rm), "Update": screen(um)}
}

// identityStray names the first rune on s that is a control or format
// rune other than the LF between rows, or 0 if there is none.
func identityStray(s string) rune {
	for _, r := range s {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return r
		}
	}
	return 0
}

// identityRowsWith are the screen's rows holding needle.
func identityRowsWith(s, needle string) []string {
	var rows []string
	for _, r := range strings.Split(s, "\n") {
		if strings.Contains(r, needle) {
			rows = append(rows, r)
		}
	}
	return rows
}

// a. An address holding CR and ESC is shown as text, on one row.
func TestIdentityEscape_ControlRunesInAppURLShowAsEscapes(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://m.example\x1b[2J\rEVIL"}
	const want = `m.example\x1b[2J\rEVIL`
	for _, width := range identityWidths {
		for name, got := range identityConfirmScreens(t, d, time.Time{}, width) {
			if r := identityStray(got); r != 0 {
				t.Errorf("%s at %d columns: %U reached the screen:\n%q", name, width, r, got)
			}
			if rows := identityRowsWith(got, "m.example"); len(rows) != 1 {
				t.Errorf("%s at %d columns: %d identity rows, want exactly 1:\n%q", name, width, len(rows), got)
			}
			if !strings.Contains(got, want) {
				t.Errorf("%s at %d columns: screen lacks the escaped text %s:\n%q", name, width, want, got)
			}
		}
	}
}

// b. An ordinary address reads exactly as it did.
func TestIdentityEscape_OrdinaryAppURLIsUnchanged(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	for _, width := range identityWidths {
		for name, got := range identityConfirmScreens(t, d, at, width) {
			if !strings.Contains(got, "mail.example.com · installed 2026-10-20") {
				t.Errorf("%s at %d columns: want the host and date as before:\n%s", name, width, got)
			}
		}
		for name, got := range identityConfirmScreens(t, d, time.Time{}, width) {
			rows := identityRowsWith(got, "mail.example.com")
			if len(rows) != 1 || strings.Contains(rows[0], "installed") {
				t.Errorf("%s at %d columns: want the bare host on one row, got %q", name, width, rows)
			}
		}
	}
}

// c. A format rune (right-to-left override) is shown as its escape.
func TestIdentityEscape_FormatRuneInAppURLShowsAsEscape(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://m.example\u202eEVIL"}
	const want = `m.example\u202eEVIL`
	for _, width := range identityWidths {
		for name, got := range identityConfirmScreens(t, d, time.Time{}, width) {
			if r := identityStray(got); r != 0 {
				t.Errorf("%s at %d columns: %U reached the screen:\n%q", name, width, r, got)
			}
			if !strings.Contains(got, want) {
				t.Errorf("%s at %d columns: screen lacks the escaped text %s:\n%q", name, width, want, got)
			}
		}
	}
}

// d. No address, and no deployment, keep their words.
func TestIdentityEscape_EmptyAndMissingKeepTheirWords(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit"}
	for _, width := range identityWidths {
		for name, got := range identityConfirmScreens(t, d, time.Time{}, width) {
			if !strings.Contains(got, "deployment detected") {
				t.Errorf("%s at %d columns: want \"deployment detected\":\n%s", name, width, got)
			}
		}
	}
	if got := deploymentIdentity(nil); got != "no deployment details found" {
		t.Errorf("deploymentIdentity(nil) = %q", got)
	}
	if got := deploymentIdentity(d); got != "deployment detected" {
		t.Errorf("deploymentIdentity(empty AppURL) = %q", got)
	}
	if got := identityLine(nil, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)); got != "no deployment details found" {
		t.Errorf("identityLine(nil) = %q", got)
	}
}

// e. deploymentIdentity itself escapes, for a CR and for an ESC.
func TestIdentityEscape_DeploymentIdentityEscapesControlRunes(t *testing.T) {
	cases := []struct{ name, appURL, want string }{
		{"CR", "https://a.example\rEVIL", `a.example\rEVIL`},
		{"ESC", "https://a.example\x1b[31mEVIL", `a.example\x1b[31mEVIL`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := deploymentIdentity(&deploy.Deployment{AppURL: c.appURL})
			if r := identityStray(got); r != 0 {
				t.Errorf("deploymentIdentity returned %U: %q", r, got)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("deploymentIdentity = %q, want it to hold %s", got, c.want)
			}
		})
	}
}
