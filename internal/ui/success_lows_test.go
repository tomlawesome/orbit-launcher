package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// 236-c8 (SP2-2): "Get into Orbit" hands the address to the platform's
// opener, and APP_URL can come from a file the operator did not write.
// Only http and https addresses are ever passed on.
func TestSuccess_GetIntoOrbitOpensOnlyHTTPAndHTTPS(t *testing.T) {
	cases := map[string]bool{
		"https://mail.example.com":       true,
		"http://mail.example.com:8080/x": true,
		"file:///etc/passwd":             false,
		"javascript:alert(1)":            false,
		"ftp://192.0.2.10/":              false,
		"data:text/html,hello":           false,
		"ssh://192.0.2.10/":              false,
		"--help":                         false,
		"-oProxyCommand=true":            false,
	}
	for raw, wantOpened := range cases {
		opened := false
		m := NewSuccessModel(raw, 0, "v")
		m.openURL = func(string) error { opened = true; return nil }
		m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26}, key(tea.KeyEnter))
		if opened != wantOpened {
			t.Errorf("APP_URL %q: opener called = %v, want %v", raw, opened, wantOpened)
		}
	}
}
