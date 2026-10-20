package ui

// Pins #248 FR2-3 and FR3-1: values from the deployment's settings file
// are the file's text, not the launcher's, so every screen that shows one
// must show control and format runes as Go escapes (visibleText) and never
// let one reach the terminal. The Remove and Update confirm screens show
// APP_URL (FR2-3); the success screen shows APP_URL after an Install or
// Update (FR3-1); Update's image line shows ORBIT_IMAGE (FR3-1b); the
// splash foot shows ORBIT_CONFIG_APPLIED_VERSION (FR3-1c). An ordinary
// value shows exactly as before, and the raw value is kept wherever it is
// used rather than shown (the success screen opens the raw address).

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

// identitySuccessScreen draws the success screen for appURL at width.
func identitySuccessScreen(t *testing.T, appURL string, width int) string {
	t.Helper()
	m := NewSuccessModel(appURL, 0, "v")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
	return screen(m)
}

// identityUpdateScreen draws Update's confirm screen for d at width.
func identityUpdateScreen(t *testing.T, d *deploy.Deployment, width int) string {
	t.Helper()
	um, _ := newTestUpdateModel(d, engineRunSeams{})
	um, _ = updateAs(t, um, tea.WindowSizeMsg{Width: width, Height: 40})
	if um.state != updateStateConfirm {
		t.Fatalf("update state = %v, want the confirm screen", um.state)
	}
	return screen(um)
}

// identitySplashFoot draws the splash with a detected deployment whose
// applied version is orbitVersion, and returns the whole screen and its
// last row (the foot).
func identitySplashFoot(t *testing.T, orbitVersion string, width int) (string, string) {
	t.Helper()
	m := seedDeployment(settled(NewSplashModel()))
	m.version = "v0.1.0"
	m.orbitVersion = orbitVersion
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
	got := screen(m)
	rows := strings.Split(strings.TrimRight(got, "\n"), "\n")
	return got, rows[len(rows)-1]
}

// FR3-1 (1). A success-screen address holding ESC and CR is shown as text,
// on one row.
func TestIdentityEscape_SuccessScreenControlRunesInAppURLShowAsEscapes(t *testing.T) {
	const want = `m.example\x1b[2J\rEVIL`
	for _, width := range identityWidths {
		got := identitySuccessScreen(t, "https://m.example\x1b[2J\rEVIL", width)
		if r := identityStray(got); r != 0 {
			t.Errorf("success at %d columns: %U reached the screen:\n%q", width, r, got)
		}
		if rows := identityRowsWith(got, want); len(rows) != 1 {
			t.Errorf("success at %d columns: %d rows hold %s, want exactly 1:\n%q", width, len(rows), want, got)
		}
	}
}

// FR3-1 (2). A format rune is shown as its escape.
func TestIdentityEscape_SuccessScreenFormatRuneInAppURLShowsAsEscape(t *testing.T) {
	const want = `m.example\u202eEVIL`
	for _, width := range identityWidths {
		got := identitySuccessScreen(t, "https://m.example\u202eEVIL", width)
		if r := identityStray(got); r != 0 {
			t.Errorf("success at %d columns: %U reached the screen:\n%q", width, r, got)
		}
		if !strings.Contains(got, want) {
			t.Errorf("success at %d columns: screen lacks the escaped text %s:\n%q", width, want, got)
		}
	}
}

// FR3-1 (3). An ordinary address reads exactly as it did: the address
// itself, whole, on one row (starfield dots may share the row).
func TestIdentityEscape_SuccessScreenOrdinaryAppURLIsUnchanged(t *testing.T) {
	for _, width := range identityWidths {
		got := identitySuccessScreen(t, "https://mail.example.com", width)
		rows := identityRowsWith(got, "mail.example.com")
		if len(rows) != 1 || !strings.Contains(rows[0], " https://mail.example.com ") {
			t.Errorf("success at %d columns: want the address whole on one row, got %q", width, rows)
		}
		if r := identityStray(got); r != 0 {
			t.Errorf("success at %d columns: %U reached the screen:\n%q", width, r, got)
		}
	}
}

// FR3-1 (4). The escape is for the screen only: Get into Orbit opens the
// raw address, and the model still holds it.
func TestIdentityEscape_SuccessScreenOpensTheRawAppURL(t *testing.T) {
	const raw = "https://m.example\x1b[2J\rEVIL\u202e"
	var opened string
	m := NewSuccessModel(raw, 0, "v")
	m.openURL = func(url string) error { opened = url; return nil }
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
	if m.appURL != raw {
		t.Errorf("appURL = %q, want the raw value %q", m.appURL, raw)
	}
	m, _ = updateAs(t, m, key(tea.KeyEnter)) // Get into Orbit is the default row
	if opened != raw {
		t.Errorf("open received %q, want the raw value %q", opened, raw)
	}
}

// FR3-1b (a). An image holding ESC and CR shows as text, on one row.
func TestIdentityEscape_UpdateImageLineControlRunesShowAsEscapes(t *testing.T) {
	const want = `ghcr.io/tomlawesome/orbit:v0.3.0\x1b[2J\rEVIL`
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com",
		Image: "ghcr.io/tomlawesome/orbit:v0.3.0\x1b[2J\rEVIL@sha256:abc"}
	for _, width := range identityWidths {
		got := identityUpdateScreen(t, d, width)
		if r := identityStray(got); r != 0 {
			t.Errorf("Update at %d columns: %U reached the screen:\n%q", width, r, got)
		}
		if rows := identityRowsWith(got, want); len(rows) != 1 {
			t.Errorf("Update at %d columns: %d rows hold %s, want exactly 1:\n%q", width, len(rows), want, got)
		}
	}
}

// FR3-1b (b). A format rune in the image shows as its escape.
func TestIdentityEscape_UpdateImageLineFormatRuneShowsAsEscape(t *testing.T) {
	const want = `ghcr.io/tomlawesome/orbit:v0.3.0\u202eEVIL`
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com",
		Image: "ghcr.io/tomlawesome/orbit:v0.3.0\u202eEVIL@sha256:abc"}
	for _, width := range identityWidths {
		got := identityUpdateScreen(t, d, width)
		if r := identityStray(got); r != 0 {
			t.Errorf("Update at %d columns: %U reached the screen:\n%q", width, r, got)
		}
		if !strings.Contains(got, want) {
			t.Errorf("Update at %d columns: screen lacks the escaped text %s:\n%q", width, want, got)
		}
	}
}

// FR3-1b (c). An ordinary image shows with its digest cut, as before.
func TestIdentityEscape_UpdateImageLineOrdinaryImageIsUnchanged(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com",
		Image: "ghcr.io/tomlawesome/orbit:v0.3.0@sha256:0123456789abcdef"}
	for _, width := range identityWidths {
		got := identityUpdateScreen(t, d, width)
		rows := identityRowsWith(got, "ghcr.io/tomlawesome/orbit:v0.3.0")
		if len(rows) != 1 {
			t.Fatalf("Update at %d columns: %d rows hold the image, want 1:\n%s", width, len(rows), got)
		}
		if strings.Contains(got, "sha256") || strings.Contains(got, "@") {
			t.Errorf("Update at %d columns: the digest must stay cut:\n%s", width, got)
		}
	}
}

// FR3-1b. The digest is cut from the raw value first, so a control rune
// inside the digest is gone with it and is not shown as an escape.
func TestIdentityEscape_UpdateImageLineDigestIsCutBeforeEscaping(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com",
		Image: "ghcr.io/tomlawesome/orbit:v0.3.0@sha256:ab\x1b[2Jcd"}
	for _, width := range identityWidths {
		got := identityUpdateScreen(t, d, width)
		if r := identityStray(got); r != 0 {
			t.Errorf("Update at %d columns: %U reached the screen:\n%q", width, r, got)
		}
		if strings.Contains(got, "sha256") || strings.Contains(got, `\x1b`) {
			t.Errorf("Update at %d columns: the cut digest leaked onto the screen:\n%q", width, got)
		}
		if !strings.Contains(got, "ghcr.io/tomlawesome/orbit:v0.3.0") {
			t.Errorf("Update at %d columns: screen lacks the image:\n%q", width, got)
		}
	}
}

// FR3-1c (a). An applied version holding ESC and CR shows as text, on the
// foot row.
func TestIdentityEscape_SplashFootControlRunesInVersionShowAsEscapes(t *testing.T) {
	const want = `orbit 0.3.0\x1b[2J\rEVIL`
	for _, width := range identityWidths {
		got, foot := identitySplashFoot(t, "0.3.0\x1b[2J\rEVIL", width)
		if r := identityStray(got); r != 0 {
			t.Errorf("splash at %d columns: %U reached the screen:\n%q", width, r, got)
		}
		if !strings.Contains(foot, want) {
			t.Errorf("splash at %d columns: foot %q lacks the escaped text %s", width, foot, want)
		}
	}
}

// FR3-1c (b). A format rune in the version shows as its escape.
func TestIdentityEscape_SplashFootFormatRuneInVersionShowsAsEscape(t *testing.T) {
	const want = `orbit 0.3.0\u202eEVIL`
	for _, width := range identityWidths {
		got, foot := identitySplashFoot(t, "0.3.0\u202eEVIL", width)
		if r := identityStray(got); r != 0 {
			t.Errorf("splash at %d columns: %U reached the screen:\n%q", width, r, got)
		}
		if !strings.Contains(foot, want) {
			t.Errorf("splash at %d columns: foot %q lacks the escaped text %s", width, foot, want)
		}
	}
}

// FR3-1c (c). An ordinary version reads exactly as it did.
func TestIdentityEscape_SplashFootOrdinaryVersionIsUnchanged(t *testing.T) {
	for _, width := range identityWidths {
		_, foot := identitySplashFoot(t, "0.3.0", width)
		if !strings.Contains(foot, "orbit-launcher v0.1.0 · orbit 0.3.0") {
			t.Errorf("splash at %d columns: foot = %q, want both versions as before", width, foot)
		}
	}
}
