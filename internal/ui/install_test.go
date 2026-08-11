package ui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func newTestInstallModel(writeConfig func(string, deploy.Config) error, install func(context.Context, string, func(string)) error) InstallModel {
	m := NewInstallModel("/opt/orbit")
	m.writeConfig = writeConfig
	m.install = install
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(InstallModel)
}

// fillConfigFields types each value into its field and leaves focus on
// the last field — so a caller's subsequent Enter exercises the
// last-field "continue" path, not a focus-advance.
func fillConfigFields(t *testing.T, m InstallModel) InstallModel {
	t.Helper()
	values := []string{"https://mail.example.com", "https://auth.example.com/o/orbit/", "orbit-client", "s3cr3t"}
	for i, v := range values {
		for _, r := range v {
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			m = updated.(InstallModel)
		}
		if i < len(values)-1 {
			updated, _ := m.Update(key(tea.KeyTab))
			m = updated.(InstallModel)
		}
	}
	return m
}

func TestInstallModel_SelectingStandardMovesToConfig(t *testing.T) {
	m := newTestInstallModel(nil, nil)
	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if m.state != installStateConfig {
		t.Errorf("state = %v, want installStateConfig", m.state)
	}
}

func TestInstallModel_SelectingAIOrFullShowsUnavailableNotFakeProgress(t *testing.T) {
	for _, sel := range []int{1, 2} { // AI, Full
		m := newTestInstallModel(nil, nil)
		m.profileSel = sel
		updated, _ := m.Update(key(tea.KeyEnter))
		m = updated.(InstallModel)
		if m.state != installStateUnavailableProfile {
			t.Errorf("profile %d: state = %v, want installStateUnavailableProfile", sel, m.state)
		}
	}
}

func TestInstallModel_CannotProceedFromConfigUntilAllFieldsFilled(t *testing.T) {
	m := newTestInstallModel(nil, nil)
	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> config
	m = updated.(InstallModel)

	// Tab through all fields without typing anything, then try to
	// continue from the last field.
	for i := 0; i < fieldCount; i++ {
		updated, _ = m.Update(key(tea.KeyEnter))
		m = updated.(InstallModel)
	}
	if m.state != installStateConfig {
		t.Errorf("state = %v, want to still be on installStateConfig with empty fields", m.state)
	}
}

func TestInstallModel_CompleteConfigReachesReview(t *testing.T) {
	m := newTestInstallModel(nil, nil)
	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> config
	m = updated.(InstallModel)
	m = fillConfigFields(t, m)

	updated, _ = m.Update(key(tea.KeyEnter)) // continue from the last field
	m = updated.(InstallModel)

	if m.state != installStateReview {
		t.Fatalf("state = %v, want installStateReview", m.state)
	}
	cfg := m.config()
	if cfg.AppURL != "https://mail.example.com" {
		t.Errorf("AppURL = %q", cfg.AppURL)
	}
	if cfg.OIDCClientSecret != "s3cr3t" {
		t.Errorf("OIDCClientSecret = %q", cfg.OIDCClientSecret)
	}
}

func TestInstallModel_ReviewNeverShowsTheSecretInPlainText(t *testing.T) {
	m := newTestInstallModel(nil, nil)
	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	m = fillConfigFields(t, m)
	updated, _ = m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)

	if containsSubstring(m.viewReview(), "s3cr3t") {
		t.Error("the review screen must never render the OIDC client secret in plain text")
	}
}

func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestInstallModel_ReviewToInstallWritesConfigThenInstalls(t *testing.T) {
	var writtenCfg deploy.Config
	var writeCalled, installCalled bool
	var installTargetDir string

	writeConfig := func(dir string, cfg deploy.Config) error {
		writeCalled = true
		writtenCfg = cfg
		return nil
	}
	install := func(_ context.Context, dir string, onLine func(string)) error {
		installCalled = true
		installTargetDir = dir
		onLine("resolving image")
		onLine("starting services")
		return nil
	}

	m := newTestInstallModel(writeConfig, install)
	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	m = fillConfigFields(t, m)
	updated, _ = m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)

	updated, cmd := m.Update(key(tea.KeyEnter)) // Install now
	m = updated.(InstallModel)
	if m.state != installStateProgress {
		t.Fatalf("state = %v, want installStateProgress", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a command to start waiting for install events")
	}

	// Drain events until Done, feeding each back through Update exactly
	// as the real bubbletea loop would.
	for i := 0; i < 10; i++ {
		msg := cmd()
		updated, cmd = m.Update(msg)
		m = updated.(InstallModel)
		if m.state != installStateProgress {
			break
		}
	}

	if !writeCalled {
		t.Error("expected writeConfig to be called")
	}
	if !installCalled {
		t.Error("expected install to be called")
	}
	if installTargetDir != "/opt/orbit" {
		t.Errorf("install called with targetDir = %q, want /opt/orbit", installTargetDir)
	}
	if writtenCfg.AppURL != "https://mail.example.com" {
		t.Errorf("writeConfig called with AppURL = %q", writtenCfg.AppURL)
	}
	if m.state != installStateDone {
		t.Errorf("state = %v, want installStateDone", m.state)
	}
	if len(m.lines) == 0 {
		t.Error("expected streamed lines to have been recorded")
	}
}

func TestInstallModel_WriteConfigFailureReachesFailedWithoutCallingInstall(t *testing.T) {
	installCalled := false
	writeConfig := func(string, deploy.Config) error { return errors.New("disk full") }
	install := func(context.Context, string, func(string)) error {
		installCalled = true
		return nil
	}

	m := newTestInstallModel(writeConfig, install)
	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	m = fillConfigFields(t, m)
	updated, _ = m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)

	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(InstallModel)

	if installCalled {
		t.Error("install must not run when writeConfig fails")
	}
	if m.state != installStateFailed {
		t.Errorf("state = %v, want installStateFailed", m.state)
	}
	if m.installErr == nil {
		t.Error("expected installErr to be set")
	}
}

func TestInstallModel_InstallFailureReachesFailedState(t *testing.T) {
	writeConfig := func(string, deploy.Config) error { return nil }
	install := func(context.Context, string, func(string)) error {
		return errors.New("docker compose up failed")
	}

	m := newTestInstallModel(writeConfig, install)
	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	m = fillConfigFields(t, m)
	updated, _ = m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)

	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(InstallModel)

	if m.state != installStateFailed {
		t.Errorf("state = %v, want installStateFailed", m.state)
	}
}
