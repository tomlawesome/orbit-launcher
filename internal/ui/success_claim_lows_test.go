package ui

// The success screen's #277 Lows, written from the findings alone:
//
//   - the menu rows hold still when the claim link arrives late;
//   - "Get into Orbit" never puts the one-time code on the opener's
//     command line: it hands the opener a private redirect file instead
//     (the pattern Jupyter uses for its token URL), removed on exit;
//   - the success screen hides an address's user name and password as
//     the confirm screens do.

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// --- 1. The menu holds still ------------------------------------------------

// A success screen whose claim read is still out, then the same screen
// once the link arrives: every menu row that was there before is on the
// same screen row afterwards, so the caret never jumps.
func TestSuccessLows_MenuRowsHoldStillWhenTheClaimLinkArrives(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 26}, {120, 40}} {
		t.Run(strconv.Itoa(size.w)+"x"+strconv.Itoa(size.h), func(t *testing.T) {
			m := NewSuccessModel(claimApp, 3*time.Minute, "v9.9.9")
			m.noAnimation = true
			m.readClaim = func() (string, error) { return claimLink, nil }
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
			before := screen(m)

			m, _ = updateAs(t, m, claimLinkMsg{link: claimLink})
			after := screen(m)
			if rowIndex(after, "Copy claim link") < 0 {
				t.Fatalf("the claim link did not reach the screen:\n%s", after)
			}
			for _, label := range []string{"Get into Orbit", "Terminal", "Menu"} {
				was, now := rowIndex(before, label), rowIndex(after, label)
				if was < 0 {
					t.Fatalf("%q is not on the screen before the link:\n%s", label, before)
				}
				if now != was {
					t.Errorf("%q moved from row %d to row %d when the claim link arrived\n--- before ---\n%s\n--- after ---\n%s",
						label, was, now, before, after)
				}
			}
			if c := claimCaret(after); c != "Get into Orbit" {
				t.Errorf("caret on %q after the link arrived, want Get into Orbit", c)
			}
		})
	}
}

// The success screen hides the user name and password in APP_URL as the
// confirm screens do, before and after a claim link arrives; the host is
// still shown.
func TestSuccessLows_ScreenHidesTheAccountPart(t *testing.T) {
	cases := []struct {
		appURL, host string
		hidden       []string
	}{
		{"https://user:secret@mail.example.com", "mail.example.com", []string{"user", "secret"}},
		{"https://user:secret@mail.example.com:8443/x", "mail.example.com:8443", []string{"user", "secret"}},
		{"https://admin:hunter#2@mail.example.com", "mail.example.com", []string{"admin", "hunter"}},
	}
	for _, c := range cases {
		for _, size := range []struct{ w, h int }{{50, 26}, {80, 26}, {120, 40}} {
			m := NewSuccessModel(c.appURL, 3*time.Minute, "v9.9.9")
			m.noAnimation = true
			m.readClaim = func() (string, error) { return claimLink, nil }
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
			screens := map[string]string{"before the link": screen(m)}
			m, _ = updateAs(t, m, claimLinkMsg{link: claimLink})
			screens["after the link"] = screen(m)
			for when, s := range screens {
				if !strings.Contains(s, c.host) {
					t.Errorf("%s at %dx%d, %s: host %q is not shown:\n%s", c.appURL, size.w, size.h, when, c.host, s)
				}
				for _, h := range c.hidden {
					if strings.Contains(s, h) {
						t.Errorf("%s at %dx%d, %s: screen shows %q:\n%s", c.appURL, size.w, size.h, when, h, s)
					}
				}
			}
		}
	}
}
