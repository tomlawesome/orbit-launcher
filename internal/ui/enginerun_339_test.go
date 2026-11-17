package ui

// Pins #339 (the launcher's note about retired SMTP lines), written from
// the issue alone. install.sh may print, in its completion block after a
// successful update, a line starting "Outgoing mail:". The launcher shows
// ONE fixed line of its own for it, never the engine's prose.
//
// The goldens under testdata/outgoing_mail were written by hand, not
// generated: each holds the text rows from the address down to the row
// above "Get into Orbit" (sky glyphs left out), with the note wrapped by
// the same words-in-measure rule as the installer's service notes.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const (
	mailNote       = "Outgoing mail: the old SMTP mail lines were removed; Orbit keeps mail settings itself now."
	mailEngineLine = "Outgoing mail: SMTP credentials were migrated to Orbit's mail settings. They were removed from .env-orbit and deleted from the secrets folder."
	mailHostile    = "Outgoing mail: SMTP_PASSWORD=hunter2 was moved to /srv/secrets/smtp_password"
	mailURL        = "https://mail.example.com"
)

func rawLine(s string) engine.RawLineMsg { return engine.RawLineMsg{Text: s} }

// updateRunApp runs Update on the app with the given engine messages and
// returns the app, sized w by 30, once the stream has been consumed.
func updateRunApp(t *testing.T, w int, msgs ...any) AppModel {
	t.Helper()
	dir := shortTempDir(t)
	writeEnv(t, dir, "APP_URL="+mailURL+"\n")
	m, queue := wiringModel(t, dir, mailURL, &claimRecorder{})
	m.flowSeams.prepareEngine = fakeEngine(nil, msgs...)
	m = openUpdate(t, m, nil)
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Update Orbit
	m = drive(t, m, cmd, queue)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: 30})
	return m
}

func completedUpdate(lines ...string) []any {
	msgs := []any{
		ev("host", "host", "completed", "host-tools", "check"),
		ev("complete", "installer", "completed", "deployment-ready", "complete"),
	}
	for _, l := range lines {
		msgs = append(msgs, rawLine(l))
	}
	return append(msgs, engine.DoneMsg{ExitCode: 0})
}

var gapRun = regexp.MustCompile(`\s{2,}`)

// textBlock is the screen's text rows from the address down to the row
// above "Get into Orbit", each cut to its own words: the sky's glyphs are
// beside the text, so a row is its letter-bearing runs, glyph tokens
// trimmed off both ends.
func textBlock(t *testing.T, s string) string {
	t.Helper()
	rows := strings.Split(s, "\n")
	from, to := -1, -1
	for i, r := range rows {
		if from < 0 && strings.Contains(r, mailURL) {
			from = i
		}
		if strings.Contains(r, "Get into Orbit") {
			to = i
		}
	}
	if from < 0 || to < 0 || to < from {
		t.Fatalf("the screen has no address row above Get into Orbit:\n%s", s)
	}
	hasWord := regexp.MustCompile(`[A-Za-z0-9]`)
	var out []string
	for _, r := range rows[from:to] {
		var keep []string
		for _, piece := range gapRun.Split(strings.TrimSpace(r), -1) {
			toks := strings.Fields(piece)
			for len(toks) > 0 && !hasWord.MatchString(toks[0]) {
				toks = toks[1:]
			}
			for len(toks) > 0 && !hasWord.MatchString(toks[len(toks)-1]) {
				toks = toks[:len(toks)-1]
			}
			if len(toks) > 0 {
				keep = append(keep, strings.Join(toks, " "))
			}
		}
		out = append(out, strings.Join(keep, " | "))
	}
	return strings.Join(out, "\n") + "\n"
}

// A. The fixed line shows, once, under the address.
func TestEngineRun339_UpdateSuccessShowsTheFixedMailLine(t *testing.T) {
	m := updateRunApp(t, 120, completedUpdate(mailEngineLine)...)
	if m.state != appStateSuccess {
		t.Fatalf("state = %v, want the success screen:\n%s", m.state, screen(m))
	}
	s := screen(m)
	if !strings.Contains(flat(s), mailNote) {
		t.Errorf("the success screen lacks %q:\n%s", mailNote, s)
	}
	if strings.Contains(s, "migrated") || strings.Contains(s, ".env-orbit") {
		t.Errorf("the engine's own prose is on the screen:\n%s", s)
	}
	if u, n := strings.Index(s, mailURL), strings.Index(s, "Outgoing mail:"); u < 0 || n < u {
		t.Errorf("the note is not below the address:\n%s", s)
	}
}

func TestEngineRun339_TwoEngineLinesShowTheNoteOnce(t *testing.T) {
	m := updateRunApp(t, 120, completedUpdate(mailEngineLine, "Outgoing mail: and a second line")...)
	if n := strings.Count(flat(screen(m)), "Outgoing mail:"); n != 1 {
		t.Errorf("the note shows %d times, want once:\n%s", n, screen(m))
	}
}

func TestEngineRun339_AHostileEngineLineIsNeverRendered(t *testing.T) {
	m := updateRunApp(t, 80, completedUpdate(mailHostile)...)
	if m.state != appStateSuccess {
		t.Fatalf("state = %v, want the success screen:\n%s", m.state, screen(m))
	}
	raw := m.View().Content
	for _, bad := range []string{"hunter2", "SMTP_PASSWORD", "smtp_password", "/srv/secrets"} {
		if strings.Contains(raw, bad) {
			t.Errorf("the view contains %q from the engine's line:\n%s", bad, screen(m))
		}
	}
	if !strings.Contains(flat(screen(m)), mailNote) {
		t.Errorf("the fixed line is missing:\n%s", screen(m))
	}
}

// B. No such line: no note.
func TestEngineRun339_NoMailLineNoNote(t *testing.T) {
	m := updateRunApp(t, 80, completedUpdate()...)
	if s := screen(m); strings.Contains(s, "Outgoing mail") {
		t.Errorf("a note with no engine line:\n%s", s)
	}
	m = updateRunApp(t, 80, completedUpdate("Orbit is ready.")...)
	if s := screen(m); strings.Contains(s, "Outgoing mail") {
		t.Errorf("a note after an unrelated completion line:\n%s", s)
	}
}

// C. A failed run never shows it, even when the line came first.
func TestEngineRun339_AFailedRunNeverShowsTheNote(t *testing.T) {
	m := updateRunApp(t, 80,
		ev("host", "host", "completed", "host-tools", "check"),
		ev("complete", "installer", "completed", "deployment-ready", "complete"),
		rawLine(mailEngineLine),
		ev("application", "application", "failed", "failure", "repair"),
		engine.DoneMsg{Err: os.ErrInvalid, ExitCode: 1},
	)
	if m.state == appStateSuccess {
		t.Fatalf("a failed run reached the success screen:\n%s", screen(m))
	}
	if s := screen(m); strings.Contains(s, "Outgoing mail") {
		t.Errorf("the stopped screen shows the note:\n%s", s)
	}
}

// F. Goldens at 50, 80, 120; the address stays on screen.
func TestEngineRun339_SuccessScreenWithTheNoteMatchesItsGolden(t *testing.T) {
	for _, w := range []int{50, 80, 120} {
		m := updateRunApp(t, w, completedUpdate(mailEngineLine)...)
		if m.state != appStateSuccess {
			t.Fatalf("width %d: state = %v:\n%s", w, m.state, screen(m))
		}
		s := screen(m)
		if !strings.Contains(s, mailURL) {
			t.Errorf("width %d: the address is off the screen:\n%s", w, s)
		}
		want, err := os.ReadFile(filepath.Join("testdata", "outgoing_mail", "update_success_"+strconv.Itoa(w)+".golden"))
		if err != nil {
			t.Fatal(err)
		}
		if got := textBlock(t, s); got != string(want) {
			t.Errorf("width %d: the text block differs from its golden: %s\nscreen:\n%s", w, firstRowDiff(string(want), got), s)
		}
	}
}
