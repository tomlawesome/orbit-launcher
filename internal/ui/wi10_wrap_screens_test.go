package ui

// Pins #214 (boxes, wrapping, widths; #199 WI-10, UI-4): unbounded text
// -- a target directory, a Docker error -- is wrapped at word boundaries
// on every screen that prints it, so no row is wider than the terminal.
// Remove already did this; Install's confirm screen, the engine-run
// failure screen (Install and Update) and Repair's error screen did not.
// All at 80 columns.

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// wi10Escape matches any CSI sequence (parameters, intermediates, any
// final byte) and any OSC, so a row's width is read on its text alone.
var wi10Escape = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")

func wi10Plain(s string) string { return wi10Escape.ReplaceAllString(s, "") }

const (
	wi10Cols = 80

	// A Docker error as the daemon words it: many short words, longer
	// than the 80-column screen on one line.
	wi10DockerError = "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. " +
		"Is the docker daemon running? permission denied while trying to connect to the docker API"

	// A target directory with spaces in it (one shell word when quoted,
	// several for the wrapper): 100 cells, past any 80-column row.
	wi10LongDir = "/srv/containers/mail stack/orbit production/with several/long directories/under the data volume/orbit"
)

// wi10AssertWrapped fails unless the screen fits cols columns, holds
// every long word of text whole and in order, and does not carry the
// text on one row (so the check cannot pass on a text that never needed
// wrapping).
func wi10AssertWrapped(t *testing.T, screen, text string) {
	t.Helper()
	plain := wi10Plain(screen)
	for i, row := range strings.Split(plain, "\n") {
		if w := lipgloss.Width(row); w > wi10Cols {
			t.Errorf("row %d is %d cells on a %d-column screen: %q", i, w, wi10Cols, strings.TrimSpace(row))
		}
		if strings.Contains(row, text) {
			t.Errorf("row %d carries the whole %d-cell text unwrapped: %q", i, lipgloss.Width(text), strings.TrimSpace(row))
		}
	}
	at := 0
	for _, w := range strings.Fields(text) {
		if len(w) < 6 {
			continue
		}
		i := strings.Index(plain[at:], w)
		if i < 0 {
			t.Errorf("word %q is missing, cut or out of order in:\n%s", w, plain)
			return
		}
		at += i + len(w)
	}
}

// Wrapping must not eat the text: a short directory stays whole on one
// row, and so does an unbroken path that fits the terminal (a word is
// never cut at the screen's edge).
func TestInstallConfirm_ShortAndUnbrokenPathsStayWhole(t *testing.T) {
	for _, dir := range []string{"/opt/orbit", "/srv/containers/mail/orbit-production/with/a/longer/unbroken/path"} {
		m := NewInstallModel(dir, "v1")
		updated, _ := m.Update(tea.WindowSizeMsg{Width: wi10Cols, Height: 40})
		updated, _ = updated.Update(key(tea.KeyEnter)) // Standard -> confirm
		screen := wi10Plain(updated.View().Content)
		if !strings.Contains(screen, dir) {
			t.Errorf("confirm screen lost %q:\n%s", dir, screen)
		}
		for i, row := range strings.Split(screen, "\n") {
			if w := lipgloss.Width(row); w > wi10Cols {
				t.Errorf("dir %q: row %d is %d cells: %q", dir, i, w, strings.TrimSpace(row))
			}
		}
	}
}

func TestInstallConfirm_LongTargetDirectoryWrapsAt80Columns(t *testing.T) {
	m := NewInstallModel(wi10LongDir, "v1")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: wi10Cols, Height: 40})
	updated, _ = updated.Update(key(tea.KeyEnter)) // Standard -> confirm
	wi10AssertWrapped(t, updated.View().Content, wi10LongDir)
}

// The engine-run failure screen is Install's and Update's alike: the
// reason (a failed start) and the stderr tail (a run that died) both
// carry Docker's own words.
func TestEngineRunFailure_DockerErrorsWrapAt80Columns(t *testing.T) {
	for _, action := range []string{"install", "update"} {
		t.Run(action+"/reason", func(t *testing.T) {
			r := startedRun(t, action, engineRunSeams{})
			r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New(wi10DockerError)})
			if r.state != runFailed {
				t.Fatalf("state = %v, want runFailed", r.state)
			}
			wi10AssertWrapped(t, r.view(wi10Cols, 40), wi10DockerError)
		})
		t.Run(action+"/stderr tail", func(t *testing.T) {
			r := startedRun(t, action, engineRunSeams{})
			r.state = runStreaming
			r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{
				ExitCode: 1, Err: errors.New("exit status 1"), StderrTail: []string{wi10DockerError},
			}})
			if r.state != runFailed {
				t.Fatalf("state = %v, want runFailed", r.state)
			}
			wi10AssertWrapped(t, r.view(wi10Cols, 40), wi10DockerError)
		})
	}
}

func TestRepairError_DockerErrorsWrapAt80Columns(t *testing.T) {
	size := func(m RepairModel) RepairModel {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: wi10Cols, Height: 40})
		return updated.(RepairModel)
	}
	t.Run("diagnosis could not start", func(t *testing.T) {
		m := size(sizedRepair(t))
		m, _ = repairUpdate(t, m, repairReadyMsg{err: errors.New(wi10DockerError)})
		if m.state != repairError {
			t.Fatalf("state = %v, want repairError", m.state)
		}
		wi10AssertWrapped(t, m.View().Content, wi10DockerError)
	})
	t.Run("rotation could not start", func(t *testing.T) {
		m := size(sizedRepair(t))
		m.state = repairRotating
		m, _ = repairUpdate(t, m, repairRotateReadyMsg{err: errors.New(wi10DockerError)})
		if m.state != repairError {
			t.Fatalf("state = %v, want repairError", m.state)
		}
		wi10AssertWrapped(t, m.View().Content, wi10DockerError)
	})
	t.Run("run ended with an error", func(t *testing.T) {
		m := size(sizedRepair(t))
		m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 1, Err: errors.New(wi10DockerError)})
		if m.state != repairError {
			t.Fatalf("state = %v, want repairError", m.state)
		}
		wi10AssertWrapped(t, m.View().Content, wi10DockerError)
	})
}

// Remove wrapped Docker errors before this change: it proves the
// assertion above can pass, and that the change keeps it so.
func TestRemoveFailure_DockerErrorWrapsAt80Columns(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error { return errors.New(wi10DockerError) })
	updated, cmd := m.Update(key(tea.KeyEnter))
	updated, _ = updated.Update(cmd())
	if got := updated.(RemoveModel).state; got != removeStateFailed {
		t.Fatalf("state = %v, want removeStateFailed", got)
	}
	wi10AssertWrapped(t, updated.View().Content, wi10DockerError)
}
