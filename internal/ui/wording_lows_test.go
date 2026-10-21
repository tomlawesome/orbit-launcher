package ui

// The owner's Remove wording, 2026-10-21 (#236): the stand-down timeout
// message and the stand-down working line. Written from the decision
// alone.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const standDownCommandLine = "docker compose --project-directory /opt/orbit --env-file /opt/orbit/.env-orbit down"

func timedOutStandDown(limit time.Duration) *deploy.StandDownTimeoutError {
	return &deploy.StandDownTimeoutError{Limit: limit, Words: deploy.StandDownCommandWords("/opt/orbit")}
}

// failedWith runs Remove's stand-down to a result of err on a screen
// width wide and returns the failed screen.
func failedWith(t *testing.T, err error, width int) string {
	t.Helper()
	m, run := removeStandingDown(t, func(context.Context, string) error { return err })
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
	m, _ = updateAs(t, m, run())
	if m.state != removeStateFailed {
		t.Fatalf("state = %v, want removeStateFailed:\n%s", m.state, screen(m))
	}
	return screen(m)
}

func TestWordingLows_TimeoutScreenSaysTheStackHasntComeDownAndTheLimit(t *testing.T) {
	cases := []struct {
		limit time.Duration
		after string
	}{
		{time.Minute, "after 1 minute"},
		{90 * time.Second, "after 90 seconds"},
		{5 * time.Minute, "after 5 minutes"},
	}
	for _, c := range cases {
		t.Run(c.after, func(t *testing.T) {
			s := failedWith(t, timedOutStandDown(c.limit), 80)
			want := "The stack hasn't come down " + c.after + ". Try again manually later with:"
			if !strings.Contains(proseOnly(s), want) {
				t.Errorf("failed screen should say %q:\n%s", want, s)
			}
			if !strings.Contains(s, "Stand-down stopped") {
				t.Errorf("failed screen should keep its title:\n%s", s)
			}
		})
	}
}

func TestWordingLows_TimeoutScreenShowsTheCommandAfterTheSentence(t *testing.T) {
	s := failedWith(t, timedOutStandDown(5*time.Minute), 80)
	// At 80 columns the command may wrap the way every copyable command
	// here does (a trailing backslash continues the row), so the order is
	// checked on its start; width 120 pins it whole on one row.
	flat := proseOnly(s)
	sentence := strings.Index(flat, "Try again manually later with:")
	command := strings.Index(flat, "docker compose --project-directory /opt/orbit")
	if sentence < 0 || command < 0 || command < sentence+len("Try again manually later with:") {
		t.Errorf("the command should come after the sentence (sentence at %d, command at %d):\n%s", sentence, command, s)
	}
}

func TestWordingLows_TimeoutCommandIsOnARowOfItsOwnAtWidth120(t *testing.T) {
	s := failedWith(t, timedOutStandDown(5*time.Minute), 120)
	var rows []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "docker compose") {
			rows = append(rows, l)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("the command should be on exactly one row, found %d:\n%s", len(rows), s)
	}
	if !strings.Contains(rows[0], standDownCommandLine) {
		t.Errorf("the row is not the whole command: %q\n%s", rows[0], s)
	}
	if strings.Contains(rows[0], "Try again") || strings.Contains(rows[0], "stack hasn't") {
		t.Errorf("the command shares its row with the sentence: %q", rows[0])
	}
}

func TestWordingLows_TimeoutScreenDropsTheOldWords(t *testing.T) {
	s := failedWith(t, timedOutStandDown(5*time.Minute), 120)
	flat := proseOnly(s)
	for _, bad := range []string{"5m0s", "did not finish within", "finishes the job", "signal: killed"} {
		if strings.Contains(flat, bad) {
			t.Errorf("failed screen should not say %q:\n%s", bad, s)
		}
	}
}

// The timeout error may arrive wrapped.
func TestWordingLows_ATimeoutErrorWrappedByACallerStillGetsTheNewScreen(t *testing.T) {
	wrapped := errors.Join(errors.New("context"), timedOutStandDown(5*time.Minute))
	s := failedWith(t, wrapped, 120)
	if !strings.Contains(proseOnly(s), "The stack hasn't come down after 5 minutes.") {
		t.Errorf("failed screen:\n%s", s)
	}
}

// Guard: a failure that is not a timeout still shows Docker's words.
func TestWordingLows_AnotherFailureStillShowsDockersWords(t *testing.T) {
	s := failedWith(t, errors.New("docker compose down: exit status 1: no such service: orbit"), 120)
	flat := proseOnly(s)
	if !strings.Contains(flat, "no such service: orbit") {
		t.Errorf("failed screen should carry Docker's words:\n%s", s)
	}
	if strings.Contains(flat, "The stack hasn't come down") || strings.Contains(flat, "Try again manually later") {
		t.Errorf("a failure that is not a timeout got the timeout message:\n%s", s)
	}
}

// 3. The working line.
func TestWordingLows_WorkingLineIsPleaseWaitNothingIsBeingDeleted(t *testing.T) {
	m, _ := removeStandingDown(t, standDownOK)
	s := screen(m)
	if !strings.Contains(proseOnly(s), "standing down Orbit") {
		t.Fatalf("not the working screen:\n%s", s)
	}
	if !strings.Contains(proseOnly(s), "Please wait — this can't be cancelled. Nothing is being deleted.") {
		t.Errorf("working line should read exactly the owner's sentence:\n%s", s)
	}
	for _, bad := range []string{"can't be stopped from here", "nothing is deleted, and it ends on its own", "ends on its own"} {
		if strings.Contains(proseOnly(s), bad) {
			t.Errorf("working screen should not say %q:\n%s", bad, s)
		}
	}
}
