package ui

// Two more #277 Lows for the success screen's "Get into Orbit", written
// from the findings alone:
//
//   - the redirect page the opener receives escapes the claim link, so a
//     link holding HTML-special characters cannot break out of the markup;
//   - when there is no claim link, an address's user name and password
//     never reach the opener.

import (
	"html"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// openerSees is everything the opener is handed for arg: the argument
// itself, plus the file's contents when the argument names a file (a path
// or a file:// URL), read through the path the opener got.
func openerSees(t *testing.T, arg string) (argument, page string) {
	t.Helper()
	path := strings.TrimPrefix(arg, "file://")
	if strings.HasPrefix(path, "/") {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("the opener was handed %q but the file cannot be read: %v", arg, err)
		}
		return arg, string(body)
	}
	return arg, ""
}

// openAddress is a success screen on appURL with the given claim link
// (empty for none) after "Get into Orbit" was chosen; it returns every
// argument the opener received. TMPDIR is a fresh directory.
func openAddress(t *testing.T, appURL, link string) []string {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	var opened []string
	m := NewSuccessModel(appURL, 3*time.Minute, "v9.9.9")
	m.noAnimation = true
	m.readClaim = func() (string, error) { return link, nil }
	m.openURL = func(url string) error { opened = append(opened, url); return nil }
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m, _ = updateAs(t, m, claimLinkMsg{link: link})
	updateAs(t, m, key(tea.KeyEnter)) // Get into Orbit is the default row
	return opened
}

// --- 1. The redirect page escapes the link -----------------------------------

// A claim link holding ", ', <, > and & goes into the redirect page only
// as HTML character references: the raw text is nowhere in the markup,
// and unescaping the page gives the link back.
func TestSuccessEscape_RedirectPageEscapesTheLink(t *testing.T) {
	const link = `https://orbit.example/claim?code=a&b="x'<y>`
	opened := openAddress(t, "https://orbit.example", link)
	if len(opened) != 1 {
		t.Fatalf("opener called %d times, want once: %q", len(opened), opened)
	}
	arg, page := openerSees(t, opened[0])
	if page == "" {
		t.Fatalf("the opener was handed %q, which is not a redirect file", arg)
	}
	if strings.Contains(page, `"x'<y>`) {
		t.Errorf("the redirect page holds the raw link text:\n%s", page)
	}
	if strings.Contains(page, "<y>") {
		t.Errorf("the redirect page lets the link open a tag:\n%s", page)
	}
	for what, forms := range map[string][]string{
		"&": {"&amp;", "&#38;", "&#x26;"},
		"<": {"&lt;", "&#60;", "&#x3c;", "&#x3C;"},
		">": {"&gt;", "&#62;", "&#x3e;", "&#x3E;"},
		`"`: {"&quot;", "&#34;", "&#x22;"},
		"'": {"&#39;", "&#x27;", "&apos;"},
	} {
		found := false
		for _, f := range forms {
			if strings.Contains(page, f) {
				found = true
			}
		}
		if !found {
			t.Errorf("the redirect page has no character reference for %s (tried %q):\n%s", what, forms, page)
		}
	}
	if !strings.Contains(html.UnescapeString(page), link) {
		t.Errorf("unescaping the redirect page does not give the link back %q:\n%s", link, page)
	}
}

// --- 2. An address's account part never reaches the opener -------------------

// With no claim link and an address that has a user name and password,
// the opener's argument, which every local account can read, holds
// neither. The address is still opened: through a private page (0600),
// which may hold the account part since only this account can read it.
func TestSuccessEscape_NoClaimLinkAddressAccountPartNeverReachesTheOpener(t *testing.T) {
	opened := openAddress(t, "https://user:secret@orbit.example/", "")
	if len(opened) != 1 {
		t.Fatalf("the opener was called %d times, want once", len(opened))
	}
	arg, page := openerSees(t, opened[0])
	for _, bad := range []string{"secret", "user:"} {
		if strings.Contains(arg, bad) {
			t.Errorf("the opener got %q: it holds %q", arg, bad)
		}
	}
	if page != "" {
		info, err := os.Stat(strings.TrimPrefix(arg, "file://"))
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("the page the opener got has mode %o, want 600", mode)
		}
		if !strings.Contains(html.UnescapeString(page), "orbit.example") {
			t.Errorf("the page does not lead to the address:\n%s", page)
		}
	}
}

// The guard: an address with no account part is still opened, by plain
// URL or by page, and what the opener gets leads to it.
func TestSuccessEscape_NoClaimLinkPlainAddressIsStillOpened(t *testing.T) {
	opened := openAddress(t, "https://orbit.example/", "")
	if len(opened) != 1 {
		t.Fatalf("opener called %d times, want once: %q", len(opened), opened)
	}
	arg, page := openerSees(t, opened[0])
	if !strings.Contains(arg, "https://orbit.example/") && !strings.Contains(html.UnescapeString(page), "https://orbit.example/") {
		t.Errorf("the opener got %q (page %q), which does not lead to https://orbit.example/", arg, page)
	}
}
