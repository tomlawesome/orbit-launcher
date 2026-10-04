package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// fakeClock is a seams.now stand-in a test moves by hand.
type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// openNotice builds an install model at width×height on a fake clock and
// takes it to the notice: Standard, then Install now.
func openNotice(t *testing.T, width, height int, seams engineRunSeams) (InstallModel, *fakeClock, tea.Cmd) {
	t.Helper()
	clock := newFakeClock()
	seams.now = clock.now
	m, _ := newTestInstallModel(seams)
	m = update(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m = update(t, m, key(tea.KeyEnter)) // Standard -> confirm
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if m.state != installStateNotice {
		t.Fatalf("state = %v, want installStateNotice", m.state)
	}
	return m, clock, cmd
}

func update(t *testing.T, m InstallModel, msg tea.Msg) InstallModel {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(InstallModel)
}

func typeText(t *testing.T, m InstallModel, s string) InstallModel {
	t.Helper()
	return update(t, m, tea.KeyPressMsg{Text: s})
}

// awakeNotice is an 80×26 notice with both gates passed.
func awakeNotice(t *testing.T, seams engineRunSeams) InstallModel {
	t.Helper()
	m, clock, _ := openNotice(t, 80, 26, seams)
	clock.advance(noticeDefaultDuration)
	return update(t, m, key(tea.KeyEnd))
}

func screen(m InstallModel) string { return stripANSI(m.View().Content) }

// passNotice takes a teatest journey through the development notice.
// The journey's AppModel sets flowNoticeDuration to a millisecond, so
// the countdown is over by the time the title has been seen.
func passNotice(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte(noticeTitle))
	}, teatest.WithDuration(10*time.Second))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnd})
	tm.Send(tea.KeyPressMsg{Text: noticePhrase})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestNotice_InstallNowOpensTheNoticeAndStartsNoEngine(t *testing.T) {
	t.Parallel()
	engineCalled := false
	m, _, cmd := openNotice(t, 80, 26, engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineCalled = true
			return nil, nil, nil
		},
	})
	if cmd == nil {
		t.Fatal("entering the notice must arm its countdown tick")
	}
	msg, ok := cmd().(noticeTickMsg)
	if !ok {
		t.Fatalf("command yielded %T, want noticeTickMsg", msg)
	}
	if !msg.start.Equal(m.notice.start) {
		t.Errorf("tick start = %v, want the notice's %v", msg.start, m.notice.start)
	}
	if engineCalled {
		t.Error("Install now must open the notice, not start the engine")
	}
}

func TestNotice_EscAndGoBackReturnToConfirmAndReentryIsFresh(t *testing.T) {
	t.Parallel()
	m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
	first := m.notice.start

	m = update(t, m, key(tea.KeyEsc))
	if m.state != installStateConfirm || m.confirmSel != 0 {
		t.Fatalf("Esc: state = %v, confirmSel = %d; want confirm with Install now selected", m.state, m.confirmSel)
	}

	clock.advance(30 * time.Second)
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if m.state != installStateNotice {
		t.Fatalf("state = %v, want installStateNotice", m.state)
	}
	if !m.notice.start.Equal(clock.now()) || m.notice.start.Equal(first) {
		t.Errorf("re-entry start = %v, want a fresh start at %v", m.notice.start, clock.now())
	}
	if !strings.Contains(screen(m), "1:10") {
		t.Error("re-entry must restart the countdown at 1:10")
	}
	if tick, ok := cmd().(noticeTickMsg); !ok || !tick.start.Equal(m.notice.start) {
		t.Errorf("re-entry tick = %#v, want one carrying the new start", tick)
	}
	if _, cmd := m.Update(noticeTickMsg{start: first}); cmd != nil {
		t.Error("a tick from the earlier visit must be dropped, not re-armed")
	}
	if _, cmd := m.Update(noticeTickMsg{start: m.notice.start}); cmd == nil {
		t.Error("the current visit's tick must re-arm while the countdown runs")
	}

	m = update(t, m, key(tea.KeyEnter)) // the caret starts on Go back
	if m.state != installStateConfirm {
		t.Errorf("Enter on Go back: state = %v, want installStateConfirm", m.state)
	}
}

func TestNotice_TickChainEndsWithTheCountdown(t *testing.T) {
	m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
	clock.advance(noticeDefaultDuration)
	if _, cmd := m.Update(noticeTickMsg{start: m.notice.start}); cmd != nil {
		t.Error("the tick must stop once the countdown is over")
	}
}

func TestNotice_WrapsTheApprovedCopy(t *testing.T) {
	doc := buildNoticeDoc(noticeMeasure)
	prose, gaps := 0, 0
	for i, r := range doc {
		if r.blank() {
			gaps++
			continue
		}
		prose++
		if r.width() > noticeMeasure {
			t.Errorf("row %d is %d cells, over the %d measure: %q", i, r.width(), noticeMeasure, r.plain())
		}
	}
	if len(doc) != 39 || prose != 31 || gaps != 8 {
		t.Errorf("doc = %d rows (%d prose, %d gaps), want 39 (31, 8) as the mockup renders", len(doc), prose, gaps)
	}

	// The emphasised sentence breaks across two rows; both carry it.
	var spanRows []int
	var emText strings.Builder
	for i, r := range doc {
		for _, s := range r.segs {
			if s.em && strings.Contains(noticeEmphasis[0], strings.TrimSpace(s.text)) && strings.TrimSpace(s.text) != "" {
				spanRows = append(spanRows, i)
				emText.WriteString(s.text + " ")
				break
			}
		}
		if len(spanRows) == 2 {
			break
		}
	}
	if len(spanRows) != 2 || spanRows[1] != spanRows[0]+1 {
		t.Fatalf("emphasised sentence rows = %v, want two consecutive rows", spanRows)
	}
	if got := strings.Join(strings.Fields(emText.String()), " "); got != noticeEmphasis[0] {
		t.Errorf("emphasised text = %q, want exactly %q", got, noticeEmphasis[0])
	}
	if strings.Contains(emText.String(), ".") {
		t.Error("the full stop after the emphasised sentence must not be emphasised")
	}

	m, _, _ := openNotice(t, 80, 26, engineRunSeams{})
	for i, line := range strings.Split(m.notice.view(m.now(), m.noticeDur()), "\n") {
		if w := visibleWidth(line); w != noticeMeasure+4 {
			t.Errorf("block line %d is %d cells, want %d: %q", i, w, noticeMeasure+4, stripANSI(line))
		}
	}
}

func TestNotice_At80x26BothGatesMustPass(t *testing.T) {
	m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
	view := screen(m)
	for _, want := range []string{noticeTitle, "Project state: alpha", "1:10", "Go back", "To install, type  " + noticePhrase} {
		if !strings.Contains(view, want) {
			t.Errorf("first view lacks %q", want)
		}
	}
	if lines := strings.Split(m.View().Content, "\n"); len(lines) != 26 {
		t.Errorf("view is %d rows, want 26", len(lines))
	}
	l := m.notice.layout()
	if len(l.rows) != 12 {
		t.Errorf("prose viewport = %d rows, want 12", len(l.rows))
	}
	if l.fadeBot != len(l.rows)-1 || l.fadeTop != -1 {
		t.Errorf("fades = top %d, bottom %d; want only the last row faded", l.fadeTop, l.fadeBot)
	}
	if m.notice.endSeen {
		t.Fatal("the end is not on screen yet")
	}

	m = update(t, m, key(tea.KeyEnd))
	if !m.notice.endSeen {
		t.Fatal("End must show the end and open the scroll gate")
	}

	clock.advance(69 * time.Second)
	if !strings.Contains(screen(m), "0:01") {
		t.Error("at 69 s the countdown must read 0:01")
	}
	m = update(t, m, runeKey('I'))
	if m.notice.focus != noticeFocusBack || len(m.notice.typed) != 0 {
		t.Fatal("letters must be ignored while the countdown runs")
	}

	clock.advance(time.Second)
	block := strings.Split(m.notice.view(m.now(), m.noticeDur()), "\n")
	countdown := block[noticeHeaderRows+len(m.notice.layout().rows)]
	if strings.TrimSpace(stripANSI(countdown)) != "" {
		t.Errorf("at 70 s the countdown row must be blank, got %q", stripANSI(countdown))
	}
	m = update(t, m, runeKey('I'))
	if m.notice.focus != noticeFocusField || string(m.notice.typed) != "I" {
		t.Errorf("once awake a letter must focus the box and type: focus %v, typed %q", m.notice.focus, string(m.notice.typed))
	}
}

func TestNotice_TallTerminalFitsWithoutScrolling(t *testing.T) {
	m, _, _ := openNotice(t, 120, 53, engineRunSeams{})
	if !m.notice.endSeen {
		t.Error("when everything fits the scroll gate is open on entry")
	}
	if l := m.notice.layout(); len(l.rows) != 39 || l.fadeTop != -1 || l.fadeBot != -1 {
		t.Errorf("layout = %d rows, fades %d/%d; want all 39 unfaded", len(l.rows), l.fadeTop, l.fadeBot)
	}
}

func TestNotice_ThreeHiddenRowsNeedThreeDowns(t *testing.T) {
	m, _, _ := openNotice(t, 120, 50, engineRunSeams{})
	if m.notice.endSeen {
		t.Fatal("three rows hang below at 120×50")
	}
	for range 3 {
		m = update(t, m, key(tea.KeyDown))
	}
	if !m.notice.endSeen {
		t.Error("three ↓ must show the end")
	}
}

func TestNotice_GapIsNeverTheLastRowWhileMoreFollows(t *testing.T) {
	for height := 15; height <= 60; height++ {
		n := newNotice(time.Time{}, 80, height)
		for off := 0; off <= n.maxOffset(); off++ {
			n.offset = off
			l := n.layout()
			if !l.endOnScreen && l.rows[len(l.rows)-1].blank() {
				t.Errorf("height %d offset %d: last visible row is a gap with rows remaining", height, off)
			}
		}
	}
}

func TestNotice_ScrollGateIsSticky(t *testing.T) {
	m, clock, _ := openNotice(t, 80, 26, engineRunSeams{})
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 53})
	if !m.notice.endSeen {
		t.Fatal("a resize that reveals the end must open the scroll gate")
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m = update(t, m, key(tea.KeyHome))
	if !m.notice.endSeen {
		t.Fatal("scrolling back up must not close the gate")
	}
	clock.advance(noticeDefaultDuration)
	if !m.notice.awake(m.now(), m.noticeDur()) {
		t.Error("the box must be awake with the gate open and the countdown over")
	}
}

func TestNotice_ScrollKeysAndWheel(t *testing.T) {
	m, _, _ := openNotice(t, 80, 26, engineRunSeams{})
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("the notice turns mouse reporting on so the wheel scrolls")
	}
	m = update(t, m, key(tea.KeyPgDown))
	if m.notice.offset != 11 {
		t.Errorf("PgDn: offset = %d, want viewport-1 = 11", m.notice.offset)
	}
	m = update(t, m, key(tea.KeyPgUp))
	m = update(t, m, key(tea.KeySpace))
	if m.notice.offset != 11 || len(m.notice.typed) != 0 {
		t.Errorf("Space while asleep: offset = %d, typed %q; want a page down", m.notice.offset, string(m.notice.typed))
	}
	m = update(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.notice.offset != 8 {
		t.Errorf("wheel up: offset = %d, want 8", m.notice.offset)
	}
	m = update(t, m, key(tea.KeyHome))
	if m.notice.offset != 0 {
		t.Errorf("Home: offset = %d, want 0", m.notice.offset)
	}
	m = update(t, m, key(tea.KeyEsc))
	if m.View().MouseMode != tea.MouseModeNone {
		t.Error("mouse reporting is for the notice only")
	}
}

func TestNotice_TypingFocusesTheBox(t *testing.T) {
	m := awakeNotice(t, engineRunSeams{})
	m = typeText(t, m, "I've re")
	if m.notice.focus != noticeFocusField || !strings.Contains(screen(m), "I've re▏") {
		t.Fatalf("typing must focus the box and show the text with the cursor:\n%s", screen(m))
	}
	m = update(t, m, key(tea.KeyBackspace))
	if string(m.notice.typed) != "I've r" {
		t.Errorf("Backspace: typed = %q, want %q", string(m.notice.typed), "I've r")
	}
	m = update(t, m, key(tea.KeyDown))
	if m.notice.focus != noticeFocusBack || !strings.Contains(screen(m), "▸ Go back") {
		t.Error("↓ from the box must move the caret to Go back")
	}
	m = update(t, m, key(tea.KeyUp))
	if m.notice.focus != noticeFocusField || string(m.notice.typed) != "I've r" {
		t.Error("↑ from Go back must return to the box, keeping the text")
	}
}

func TestNotice_EnterChecksThePhrase(t *testing.T) {
	calls := 0
	m := awakeNotice(t, engineRunSeams{
		prepareEngine: func(ctx context.Context, dir, action string) (*engine.Stream, func() error, error) {
			calls++
			return fakeEngine(nil, successStream()...)(ctx, dir, action)
		},
	})

	// An empty, focused box: Enter does nothing.
	m = typeText(t, m, "x")
	m = update(t, m, key(tea.KeyBackspace))
	m = update(t, m, key(tea.KeyEnter))
	if m.state != installStateNotice || m.notice.refused {
		t.Fatal("Enter on an empty box must change nothing")
	}

	m = typeText(t, m, "I read this and I understand")
	m = update(t, m, key(tea.KeyEnter))
	if !strings.Contains(screen(m), noticeRefusal) || string(m.notice.typed) != "I read this and I understand" {
		t.Fatal("a wrong phrase must be refused and kept for fixing")
	}
	m = update(t, m, key(tea.KeyBackspace))
	if strings.Contains(screen(m), noticeRefusal) {
		t.Error("the next key must clear the refusal")
	}

	m = update(t, m, key(tea.KeyEsc))
	m = update(t, m, key(tea.KeyEnter)) // fresh notice
	m.seams.now = func() time.Time { return m.notice.start.Add(noticeDefaultDuration) }
	m = update(t, m, key(tea.KeyEnd))
	m = typeText(t, m, "I have read this and I understand")
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if m.state != installStateRunning || cmd == nil {
		t.Fatalf("a matching phrase must start the run: state = %v", m.state)
	}
	cmd()
	if calls != 1 {
		t.Errorf("engine prepared %d times, want once", calls)
	}
}

func TestPhraseMatches(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"I've read this and I understand", true},
		{"ive read this and i understand", true},
		{"I'VE READ THIS AND I UNDERSTAND", true},
		{"I have read this and I understand", true},
		{"I’ve read this and I understand", true},
		{"  I've  read this and I understand.  ", true},
		{"I read this and I understand", false},
		{"I've read it", false},
		{"I've read this and I understand it", false},
		{"I had read this and I understand", false},
		{"", false},
	} {
		if got := phraseMatches(tc.in); got != tc.want {
			t.Errorf("phraseMatches(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNotice_ExtremeSizesDoNotPanic(t *testing.T) {
	for _, size := range [][2]int{{40, 10}, {200, 60}, {10, 3}} {
		m, clock, _ := openNotice(t, size[0], size[1], engineRunSeams{})
		_ = m.View()
		clock.advance(noticeDefaultDuration)
		for _, k := range []tea.KeyPressMsg{key(tea.KeyEnd), key(tea.KeyPgUp), key(tea.KeyDown)} {
			m = update(t, m, k)
		}
		m = typeText(t, m, strings.Repeat("long text ", 10))
		_ = m.View()
	}
}

func TestNotice_TeaTest_PassesTheNoticeAt80x26(t *testing.T) {
	m := NewInstallModel("/opt/orbit", "v9.9.9")
	m.checkVolumes = noStaleVolumes
	m.noticeDuration = time.Millisecond
	m.seams = engineRunSeams{prepareEngine: fakeEngineStreaming(t, ev("host", "host", "completed", "host-tools", "check"))}
	sender := &deferredSender{}
	m.send = sender.Send

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 26))
	sender.attach(tm.Send)
	wait := func(want string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
			return bytes.Contains(out, []byte(want))
		}, teatest.WithDuration(5*time.Second))
	}

	wait("Choose a deployment profile")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	wait("Ready to install")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	passNotice(t, tm)
	wait("Install — Standard")

	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}
