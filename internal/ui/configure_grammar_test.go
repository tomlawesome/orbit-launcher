package ui

// Builders for the Configure flow's screens in the menu-grammar table
// (#286): each is a ConfigureModel sitting on one screen, sized 80x26.

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// configureAt is a Configure flow over a folder holding a deployment
// (profiles as COMPOSE_PROFILES, "-" for none; installed adds the Compose
// file), sized and with 16 GB of memory.
func configureAt(t *testing.T, profiles string, installed bool) ConfigureModel {
	t.Helper()
	docs260Memory(t, 16*docs260GB, true)
	dir := cfg286Folder(t, shortTempDir(t), profiles, installed)
	d, err := deploy.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewConfigureModel(d, dir, "v9.9.9")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	return m
}

func configureNotFound(t *testing.T) tea.Model {
	t.Helper()
	m := NewConfigureModel(nil, shortTempDir(t), "v9.9.9")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	if m.state != configureStateNotFound {
		t.Fatalf("Configure is not on its not-found screen (state %v)", m.state)
	}
	return m
}

func configurePrepared(t *testing.T) tea.Model { return configureAt(t, "-", false) }

func configureUnsupported(t *testing.T) tea.Model { return configureAt(t, "full", true) }

func configureProfile(t *testing.T) tea.Model { return configureAt(t, "-", true) }

func configureConfirm(t *testing.T) tea.Model {
	t.Helper()
	m, _ := updateAs(t, configureAt(t, "-", true), key(tea.KeyDown), key(tea.KeyEnter)) // Documents
	if m.state != configureStateConfirm {
		t.Fatalf("Configure is not on its confirm screen (state %v)", m.state)
	}
	return m
}
