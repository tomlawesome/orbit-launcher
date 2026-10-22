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
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// claimCode is the one-time code inside claimLink and wiringLink.
const claimCode = "Zm9yLXRlc3Qtb25seS1ub3QtYS1yZWFsLWNvZGU"

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

// --- 2. The opener never sees the code --------------------------------------

// redirectFile is the file an opener argument names: a path, or a
// file:// URL. It fails the test unless the argument is free of the
// one-time code and names an absolute path.
func redirectFile(t *testing.T, arg string) string {
	t.Helper()
	if strings.Contains(arg, claimCode) || strings.Contains(arg, "#claim=") {
		t.Fatalf("the opener was handed the one-time code: %q", arg)
	}
	path := strings.TrimPrefix(arg, "file://")
	if !filepath.IsAbs(path) {
		t.Fatalf("opener argument %q is not a file path or file:// URL", arg)
	}
	return path
}

// assertPrivateRedirect checks path is a 0600 file in a 0700 directory
// under tmp that holds link.
func assertPrivateRedirect(t *testing.T, path, tmp, link string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the redirect file is not there while the launcher runs: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("redirect file mode = %o, want 600", perm)
	}
	dir := filepath.Dir(path)
	if dir == filepath.Clean(tmp) {
		t.Errorf("the redirect file sits directly in TMPDIR, want a private directory inside it")
	}
	if dinfo, err := os.Stat(dir); err != nil {
		t.Errorf("redirect directory: %v", err)
	} else if perm := dinfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("redirect directory mode = %o, want 700", perm)
	}
	if rel, err := filepath.Rel(tmp, path); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("redirect file %q is not under TMPDIR %q", path, tmp)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the redirect file: %v", err)
	}
	if !strings.Contains(string(body), link) {
		t.Errorf("the redirect file does not hold the claim link %q:\n%s", link, body)
	}
}

// assertTmpEmpty fails unless tmp holds nothing at all.
func assertTmpEmpty(t *testing.T, tmp string) {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("TMPDIR still holds %q after the launcher quit", names)
	}
}

// Get into Orbit with a claim link: the opener's argument lacks the
// code, names a 0600 file in a 0700 directory in TMPDIR that holds the
// link, and quitting from the success screen removes it all.
func TestSuccessLows_GetIntoOrbitHandsTheOpenerARedirectFile(t *testing.T) {
	for name, quitKeys := range map[string][]tea.Msg{
		"Terminal": {key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)},
		"Ctrl+C":   {ctrlC()},
	} {
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			var opened []string
			m := claimModel(t, claimApp, claimLink, 80, &bytes.Buffer{})
			m.openURL = func(url string) error { opened = append(opened, url); return nil }

			m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Get into Orbit
			if cmd != nil || m.Chosen != "" {
				t.Fatalf("Get into Orbit left the screen (cmd = %v, Chosen = %q)", cmd != nil, m.Chosen)
			}
			if len(opened) != 1 {
				t.Fatalf("opener called %d times, want once: %q", len(opened), opened)
			}
			path := redirectFile(t, opened[0])
			assertPrivateRedirect(t, path, tmp, claimLink)

			_, cmd = updateAs(t, m, quitKeys...)
			if !isQuit(cmd) {
				t.Fatalf("%s did not quit", name)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the redirect file is still there after quitting (stat err = %v)", err)
			}
			assertTmpEmpty(t, tmp)
		})
	}
}

// The real opener is handed exactly what the seam received, so a
// launcher that only lets http and https through still opens the file.
func TestSuccessLows_RealOpenerAcceptsTheRedirectFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	var opened []string
	m := claimModel(t, claimApp, claimLink, 80, &bytes.Buffer{})
	m.openURL = func(url string) error { opened = append(opened, url); return nil }
	updateAs(t, m, key(tea.KeyEnter))
	if len(opened) != 1 {
		t.Fatalf("opener called %d times, want once: %q", len(opened), opened)
	}
	redirectFile(t, opened[0])

	record := fakeOpener(t, "xdg-open")
	if err := defaultOpenURL(opened[0]); err != nil {
		t.Fatalf("defaultOpenURL(%q): %v", opened[0], err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := os.ReadFile(record)
		if err == nil && string(got) == opened[0] {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("xdg-open was handed %q, want %q", got, opened[0])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Through the whole launcher: after Install, Get into Orbit writes the
// redirect file; going back to the menu keeps it (the launcher still
// runs); quitting from the menu removes it and leaves TMPDIR empty.
func TestSuccessLows_RedirectFileOutlivesTheScreenButNotTheLauncher(t *testing.T) {
	tmp := t.TempDir()
	rec := &claimRecorder{link: wiringLink}
	m, queue := wiringModel(t.TempDir(), wiringApp, rec)
	m, _ = installToEnd(t, m, queue)
	if m.state != appStateSuccess {
		t.Fatalf("Install did not reach the success screen (state %v):\n%s", m.state, screen(m))
	}
	// TMPDIR is set only now, so the install's own files cannot count.
	t.Setenv("TMPDIR", tmp)
	var opened []string
	m.success.openURL = func(url string) error { opened = append(opened, url); return nil }

	m, _ = updateAs(t, m, key(tea.KeyEnter)) // Get into Orbit
	if len(opened) != 1 {
		t.Fatalf("opener called %d times, want once: %q", len(opened), opened)
	}
	path := redirectFile(t, opened[0])
	assertPrivateRedirect(t, path, tmp, wiringLink)

	m, cmd := updateAs(t, m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)) // Menu
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Fatalf("Menu did not go back to the menu (state %v):\n%s", m.state, screen(m))
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the redirect file went with the success screen; the launcher still runs: %v", err)
	}

	_, cmd = updateAs(t, m, ctrlC())
	if !isQuit(cmd) {
		t.Fatal("Ctrl+C on the menu did not quit")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the redirect file is still there after the launcher quit (stat err = %v)", err)
	}
	assertTmpEmpty(t, tmp)
}

// --- 3. No account part on the success screen -------------------------------

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
