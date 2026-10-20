package ui

// Specified by issue #250 (flat notice countdown): the notice's reading
// countdown is a fixed 20 seconds, shown as 0:20, whatever the length of
// the notice. Typing the acknowledgement still works only once the
// countdown has run out AND the whole notice has been on screen. Each
// "does not yet" check below is paired with the "now it does" case in
// the same test, so a notice that never accepts typing cannot pass.

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// noticeWidths gives notices of clearly different length: the copy wraps
// to a different number of rows at each width.
var noticeWidths = []int{40, 80, 200}

func TestNoticeCountdown_DefaultIsTwentySeconds(t *testing.T) {
	t.Parallel()
	if noticeDefaultDuration != 20*time.Second {
		t.Errorf("noticeDefaultDuration = %v, want 20s", noticeDefaultDuration)
	}
	m, _, _ := openNotice(t, 80, 26, engineRunSeams{})
	if got := m.noticeDur(); got != 20*time.Second {
		t.Errorf("a notice with no override counts down %v, want 20s", got)
	}
}

func TestNoticeCountdown_StartsAtTwentyAndTicksDown(t *testing.T) {
	t.Parallel()
	m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
	for _, tc := range []struct {
		after time.Duration
		want  string
	}{
		{0, "0:20"},
		{5 * time.Second, "0:15"},
		{19 * time.Second, "0:01"},
	} {
		clock.t = m.notice.start.Add(tc.after)
		view := screen(m)
		if !strings.Contains(view, tc.want) {
			t.Errorf("after %v the countdown must read %s:\n%s", tc.after, tc.want, view)
		}
		if strings.Contains(view, "1:10") {
			t.Errorf("after %v the old 1:10 start is still on screen", tc.after)
		}
	}
}

func TestNoticeCountdown_IsTheSameWhateverTheNoticeLength(t *testing.T) {
	t.Parallel()
	minRows, maxRows := 1<<30, 0
	for _, w := range noticeWidths {
		rows := len(buildNoticeDoc(noticeMeasureFor(w)))
		minRows, maxRows = min(minRows, rows), max(maxRows, rows)
	}
	if minRows == maxRows {
		t.Fatalf("the test widths all give a %d-row notice; pick widths that differ", minRows)
	}

	for _, w := range noticeWidths {
		m, clock, _ := openNotice(t, w, 26, engineRunSeams{})
		if !strings.Contains(screen(m), "0:20") {
			t.Errorf("width %d: the countdown must start at 0:20:\n%s", w, screen(m))
		}
		m, _ = updateAs(t, m, key(tea.KeyEnd))

		clock.t = m.notice.start.Add(19 * time.Second)
		m, _ = updateAs(t, m, runeKey('I'))
		if !m.notice.typed.empty() {
			t.Errorf("width %d: at 19 s typing must still be ignored, typed %q", w, m.notice.typed.String())
		}
		clock.t = m.notice.start.Add(20 * time.Second)
		m, _ = updateAs(t, m, runeKey('I'))
		if m.notice.typed.String() != "I" {
			t.Errorf("width %d: at 20 s typing must work, typed %q", w, m.notice.typed.String())
		}
	}
}

func TestNoticeCountdown_TypingNeedsTheCountdownOverAndTheEndSeen(t *testing.T) {
	t.Parallel()

	t.Run("countdown over but end not yet on screen", func(t *testing.T) {
		m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
		if m.notice.endSeen {
			t.Fatal("precondition: the end of the notice is not on screen at 80x26")
		}
		clock.advance(5 * time.Minute) // far past the countdown
		m, _ = updateAs(t, m, runeKey('I'))
		if !m.notice.typed.empty() {
			t.Fatalf("with the end unseen typing must be ignored, typed %q", m.notice.typed.String())
		}
		m, _ = updateAs(t, m, key(tea.KeyEnd))
		m, _ = updateAs(t, m, runeKey('I'))
		if m.notice.typed.String() != "I" {
			t.Errorf("once the end has been seen typing must work, typed %q", m.notice.typed.String())
		}
	})

	t.Run("end seen but countdown still running", func(t *testing.T) {
		m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
		m, _ = updateAs(t, m, key(tea.KeyEnd))
		clock.advance(19 * time.Second)
		m, _ = updateAs(t, m, runeKey('I'))
		if !m.notice.typed.empty() {
			t.Fatalf("at 19 s typing must be ignored, typed %q", m.notice.typed.String())
		}
		clock.advance(time.Second)
		m, _ = updateAs(t, m, runeKey('I'))
		if m.notice.typed.String() != "I" {
			t.Errorf("at 20 s typing must work, typed %q", m.notice.typed.String())
		}
	})

	t.Run("a notice that fits the screen waits only for the countdown", func(t *testing.T) {
		m, clock, _ := openNotice(t, 120, 55, engineRunSeams{})
		if !m.notice.endSeen {
			t.Fatal("precondition: the whole notice is on screen at 120x55")
		}
		clock.advance(19 * time.Second)
		m, _ = updateAs(t, m, runeKey('I'))
		if !m.notice.typed.empty() {
			t.Fatalf("at 19 s typing must be ignored, typed %q", m.notice.typed.String())
		}
		clock.advance(time.Second)
		m, _ = updateAs(t, m, runeKey('I'))
		if m.notice.typed.String() != "I" {
			t.Errorf("at 20 s typing must work, typed %q", m.notice.typed.String())
		}
	})
}

func TestNoticeCountdown_ThePhraseStartsTheRunOnlyAfterTwentySeconds(t *testing.T) {
	t.Parallel()
	calls := 0
	m, clock, _ := openNotice(t, 80, 26, engineRunSeams{
		prepareEngine: func(ctx context.Context, dir, action string) (*engine.Stream, func() error, error) {
			calls++
			return fakeEngine(nil, successStream()...)(ctx, dir, action)
		},
	})
	m, _ = updateAs(t, m, key(tea.KeyEnd))

	// At 19 s the phrase is not accepted into the box.
	clock.advance(19 * time.Second)
	m, _ = updateAs(t, m, tea.KeyPressMsg{Text: noticePhrase})
	if !m.notice.typed.empty() || m.state != installStateNotice {
		t.Fatalf("at 19 s the phrase must be ignored: typed %q, state %v", m.notice.typed.String(), m.state)
	}

	// At 20 s the same phrase is typed and Enter starts the run.
	clock.advance(time.Second)
	m, _ = updateAs(t, m, tea.KeyPressMsg{Text: noticePhrase})
	if m.notice.typed.String() != noticePhrase {
		t.Fatalf("at 20 s the phrase must be typed, got %q", m.notice.typed.String())
	}
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if m.state != installStateRunning || cmd == nil {
		t.Fatalf("a matching phrase at 20 s must start the run: state = %v", m.state)
	}
	cmd()
	if calls != 1 {
		t.Errorf("engine prepared %d times, want once", calls)
	}
}

func TestNoticeCountdown_TheTickChainEndsAtTwentySeconds(t *testing.T) {
	t.Parallel()
	m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
	clock.advance(19 * time.Second)
	if _, cmd := m.Update(noticeTickMsg{start: m.notice.start}); cmd == nil {
		t.Error("at 19 s the countdown is still running, so the tick must re-arm")
	}
	clock.advance(time.Second)
	if _, cmd := m.Update(noticeTickMsg{start: m.notice.start}); cmd != nil {
		t.Error("at 20 s the countdown is over, so the tick must stop")
	}
}
