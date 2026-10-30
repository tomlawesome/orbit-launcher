package ui

// Pins #314's screens (design note 2026-10-30): before the OIDC_ISSUER
// answer goes to the configure step, the launcher checks it against the
// provider's discovery document through the checkIssuer seam, and the
// result comes back as an issuerCheckedMsg. Written before the code.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const (
	issuerPrompt   = "prompt field=OIDC_ISSUER kind=url required=true attempt=1"
	issuerAnswer   = "https://id.example.test/realms/orbit"
	checkingLine   = "checking the address with your provider"
	usePublished   = "Use the published address"
	correctAnswer  = "Correct the answer"
	keepAsTyped    = "Keep the answer as typed"
	slashWith      = "the provider publishes this issuer with a trailing slash"
	slashWithout   = "the provider publishes this issuer without the trailing slash"
	differentIssue = "the provider names a different issuer"
)

// issuerSeam records each check it is asked for and answers with
// result(call number from 1, answer).
type issuerSeam struct {
	mu      sync.Mutex
	answers []string
	ctxs    []context.Context
	result  func(call int, answer string) deploy.IssuerCheck
}

func (s *issuerSeam) check(ctx context.Context, answer string) deploy.IssuerCheck {
	s.mu.Lock()
	s.answers = append(s.answers, answer)
	s.ctxs = append(s.ctxs, ctx)
	n := len(s.answers)
	s.mu.Unlock()
	return s.result(n, answer)
}

func (s *issuerSeam) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.answers...)
}

func answering(v deploy.IssuerCheck) *issuerSeam {
	return &issuerSeam{result: func(int, string) deploy.IssuerCheck { return v }}
}

// issuerRun is a configuration session showing the OIDC_ISSUER prompt.
func issuerRun(t *testing.T, check func(context.Context, string) deploy.IssuerCheck) (engineRun, *recordingStdin) {
	t.Helper()
	stdin := &recordingStdin{}
	r := collectingRun(t, engineRunSeams{checkIssuer: check})
	r.cfg.stream = &engine.Stream{C: make(chan any)}
	r.cfg.stdin = stdin
	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: issuerPrompt}})
	return r, stdin
}

// issuerCheckWithin runs cmd the way the program would, expanding
// batches and sequences, and returns the issuerCheckedMsg it produces
// within wait. Other messages (a spinner's tick) are ignored.
func issuerCheckWithin(cmd tea.Cmd, wait time.Duration) (issuerCheckedMsg, bool) {
	found := make(chan issuerCheckedMsg, 8)
	cmdType := reflect.TypeOf(tea.Cmd(nil))
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if m, ok := msg.(issuerCheckedMsg); ok {
				found <- m
				return
			}
			v := reflect.ValueOf(msg)
			if v.IsValid() && v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
				for i := 0; i < v.Len(); i++ {
					sub, _ := v.Index(i).Interface().(tea.Cmd)
					run(sub)
				}
			}
		}()
	}
	run(cmd)
	select {
	case m := <-found:
		return m, true
	case <-time.After(wait):
		return issuerCheckedMsg{}, false
	}
}

func requireIssuerCheck(t *testing.T, cmd tea.Cmd) issuerCheckedMsg {
	t.Helper()
	m, ok := issuerCheckWithin(cmd, 3*time.Second)
	if !ok {
		t.Fatal("Enter on a well-shaped issuer started no check (no issuerCheckedMsg)")
	}
	return m
}

func requireNoIssuerCheck(t *testing.T, cmd tea.Cmd, what string) {
	t.Helper()
	if m, ok := issuerCheckWithin(cmd, 300*time.Millisecond); ok {
		t.Fatalf("%s started a check: %+v", what, m)
	}
}

// submitIssuer types answer into the prompt and presses Enter, returning
// the run and the command Enter gave.
func submitIssuer(t *testing.T, r engineRun, answer string) (engineRun, tea.Cmd) {
	t.Helper()
	r, _ = r.update(paste(answer))
	return r.update(key(tea.KeyEnter))
}

// checkedWith submits answer, runs the check and delivers its result.
func checkedWith(t *testing.T, r engineRun, answer string) engineRun {
	t.Helper()
	r, cmd := submitIssuer(t, r, answer)
	msg := requireIssuerCheck(t, cmd)
	r, _ = r.update(msg)
	return r
}

// labelAt is where label first appears on the screen, -1 if nowhere.
func labelAt(s, label string) int { return strings.Index(s, label) }

// Fault: the answer sent before (or without) the check, or a match not
// sent on.
func TestIssuerCheck_MatchSendsTheAnswerOn(t *testing.T) {
	seam := answering(deploy.IssuerCheck{Verdict: deploy.IssuerMatches})
	r, stdin := issuerRun(t, seam.check)
	r, cmd := submitIssuer(t, r, issuerAnswer)
	if stdin.String() != "" {
		t.Fatalf("the answer reached the engine before the check: %q", stdin.String())
	}
	msg := requireIssuerCheck(t, cmd)
	if msg.answer != issuerAnswer || msg.result.Verdict != deploy.IssuerMatches {
		t.Fatalf("issuerCheckedMsg = %+v, want the answer and the seam's match", msg)
	}
	if got := seam.calls(); len(got) != 1 || got[0] != issuerAnswer {
		t.Fatalf("checked %q, want once %q", got, issuerAnswer)
	}
	if seam.ctxs[0] == nil || seam.ctxs[0].Err() != nil {
		t.Fatal("the check was given no live context")
	}
	r, _ = r.update(msg)
	if stdin.String() != issuerAnswer+"\n" {
		t.Fatalf("engine received %q, want the matched answer", stdin.String())
	}
}

// Fault: a badly shaped answer held up by a check (it goes to the
// engine, which rejects it, as today). Paired with MatchSendsTheAnswerOn.
func TestIssuerCheck_BadlyShapedAnswerSkipsTheCheck(t *testing.T) {
	for _, answer := range []string{"http://id.example.test/realms/orbit", "https://id.example.test/realms/orbit?x=1"} {
		t.Run(answer, func(t *testing.T) {
			seam := answering(deploy.IssuerCheck{Verdict: deploy.IssuerMatches})
			r, stdin := issuerRun(t, seam.check)
			r, cmd := submitIssuer(t, r, answer)
			if stdin.String() != answer+"\n" {
				t.Fatalf("engine received %q, want the answer sent at once", stdin.String())
			}
			requireNoIssuerCheck(t, cmd, "a badly shaped answer")
			if n := len(seam.calls()); n != 0 {
				t.Fatalf("a badly shaped answer was checked %d times", n)
			}
			if strings.Contains(runScreen(r), checkingLine) {
				t.Fatalf("the checking line shows for an unchecked answer:\n%s", runScreen(r))
			}
		})
	}
}

// Fault: no checking screen, or typing, a paste or Enter acting while
// the check runs.
func TestIssuerCheck_WhileCheckingTheScreenSaysSoAndKeysWait(t *testing.T) {
	seam := answering(deploy.IssuerCheck{Verdict: deploy.IssuerMatches})
	r, stdin := issuerRun(t, seam.check)
	r, cmd := submitIssuer(t, r, issuerAnswer)
	s := runScreen(r)
	if !strings.Contains(s, checkingLine) || !strings.Contains(s, issuerAnswer) {
		t.Fatalf("the checking screen should show the answer and %q:\n%s", checkingLine, s)
	}
	if strings.Contains(s, "▏") {
		t.Fatalf("the input row still has a cursor while checking:\n%s", s)
	}
	msg := requireIssuerCheck(t, cmd)

	for _, k := range []tea.Msg{runeKey('x'), key(tea.KeyBackspace), key(tea.KeyEnter), paste("/extra")} {
		var c tea.Cmd
		r, c = r.update(k)
		requireNoIssuerCheck(t, c, "a key while checking")
	}
	if stdin.String() != "" || len(seam.calls()) != 1 {
		t.Fatalf("keys acted while checking: engine got %q, %d checks", stdin.String(), len(seam.calls()))
	}
	if !strings.Contains(runScreen(r), checkingLine) {
		t.Fatalf("the checking screen went away on a key:\n%s", runScreen(r))
	}
	r, _ = r.update(msg)
	if stdin.String() != issuerAnswer+"\n" {
		t.Fatalf("engine received %q, want the answer as it was when checked", stdin.String())
	}
}

// Fault: a slash difference not offered, offered with the wrong
// sentence, or Use sending the typed answer instead of the published one.
func TestIssuerCheck_SlashMismatchOffersThePublishedValue(t *testing.T) {
	cases := []struct{ name, answer, published, sentence string }{
		{"missing slash", issuerAnswer, issuerAnswer + "/", slashWith + ":"},
		{"extra slash", issuerAnswer + "/", issuerAnswer, slashWithout + ":"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seam := answering(deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Published: c.published})
			r, stdin := issuerRun(t, seam.check)
			r = checkedWith(t, r, c.answer)
			s := runScreen(r)
			for _, want := range []string{c.sentence, c.published, usePublished, correctAnswer} {
				if !strings.Contains(s, want) {
					t.Fatalf("the slash screen lacks %q:\n%s", want, s)
				}
			}
			if labelAt(s, usePublished) > labelAt(s, correctAnswer) {
				t.Fatalf("%q should come before %q:\n%s", usePublished, correctAnswer, s)
			}
			if stdin.String() != "" {
				t.Fatalf("engine received %q before a choice", stdin.String())
			}
			r, cmd := r.update(key(tea.KeyEnter))
			if stdin.String() != c.published+"\n" {
				t.Fatalf("Use sent %q, want the published %q", stdin.String(), c.published+"\n")
			}
			requireNoIssuerCheck(t, cmd, "Use the published address")
		})
	}
}

// Fault: Correct losing the answer, keeping the colon (it introduced the
// value), or the corrected answer sent without a fresh check.
func TestIssuerCheck_SlashMismatchCorrectReopensTheAnswer(t *testing.T) {
	seam := &issuerSeam{result: func(call int, _ string) deploy.IssuerCheck {
		if call == 1 {
			return deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Published: issuerAnswer + "/"}
		}
		return deploy.IssuerCheck{Verdict: deploy.IssuerMatches}
	}}
	r, stdin := issuerRun(t, seam.check)
	r = checkedWith(t, r, issuerAnswer)
	r, _ = r.update(key(tea.KeyDown))
	r, _ = r.update(key(tea.KeyEnter))
	s := runScreen(r)
	if got := inputRowText(s); got != issuerAnswer {
		t.Fatalf("Correct reopened the answer as %q, want %q kept:\n%s", got, issuerAnswer, s)
	}
	if !strings.Contains(s, slashWith) || strings.Contains(s, slashWith+":") {
		t.Fatalf("the reason row should read %q without a colon:\n%s", slashWith, s)
	}
	if strings.Contains(s, usePublished) {
		t.Fatalf("the menu is still drawn after Correct:\n%s", s)
	}
	if stdin.String() != "" {
		t.Fatalf("Correct sent %q", stdin.String())
	}
	r, cmd := r.update(key(tea.KeyEnter))
	if stdin.String() != "" {
		t.Fatalf("Enter after Correct sent %q before checking again", stdin.String())
	}
	msg := requireIssuerCheck(t, cmd)
	if got := seam.calls(); len(got) != 2 || got[1] != issuerAnswer {
		t.Fatalf("checks %q, want the answer checked again", got)
	}
	r, _ = r.update(msg)
	if stdin.String() != issuerAnswer+"\n" {
		t.Fatalf("engine received %q after the re-check matched", stdin.String())
	}
}

// Fault: a different issuer offered as a choice, sent on, or not shown.
func TestIssuerCheck_OtherMismatchAsksAgain(t *testing.T) {
	other := "https://login.example.test/realms/other"
	seam := answering(deploy.IssuerCheck{Verdict: deploy.IssuerDiffers, Published: other})
	r, stdin := issuerRun(t, seam.check)
	r = checkedWith(t, r, issuerAnswer)
	s := runScreen(r)
	if !strings.Contains(s, differentIssue) || !strings.Contains(s, other) {
		t.Fatalf("the screen should say %q and show %q:\n%s", differentIssue, other, s)
	}
	if got := inputRowText(s); got != issuerAnswer {
		t.Fatalf("the prompt reopened with %q, want %q kept:\n%s", got, issuerAnswer, s)
	}
	for _, row := range []string{usePublished, correctAnswer, keepAsTyped} {
		if strings.Contains(s, row) {
			t.Fatalf("a different issuer offers %q; it should only ask again:\n%s", row, s)
		}
	}
	if stdin.String() != "" {
		t.Fatalf("a different issuer sent %q", stdin.String())
	}
	_, cmd := r.update(key(tea.KeyEnter))
	requireIssuerCheck(t, cmd)
	if stdin.String() != "" {
		t.Fatalf("Enter again sent %q without a check", stdin.String())
	}
}

// Fault: a check that could not run blocking the answer, keeping it
// without asking, or the reason not shown. Both rows are exercised.
func TestIssuerCheck_UncheckedOffersKeepOrCorrect(t *testing.T) {
	unchecked := deploy.IssuerCheck{Verdict: deploy.IssuerUnchecked, Reason: "the address could not be reached"}
	setup := func(t *testing.T) (engineRun, *recordingStdin) {
		r, stdin := issuerRun(t, answering(unchecked).check)
		r = checkedWith(t, r, issuerAnswer)
		s := runScreen(r)
		if !strings.Contains(s, "could not check this issuer: the address could not be reached") {
			t.Fatalf("the reason is not shown:\n%s", s)
		}
		if a, b := labelAt(s, correctAnswer), labelAt(s, keepAsTyped); a < 0 || b < 0 || a > b {
			t.Fatalf("rows should be %q then %q:\n%s", correctAnswer, keepAsTyped, s)
		}
		if stdin.String() != "" {
			t.Fatalf("engine received %q before a choice", stdin.String())
		}
		return r, stdin
	}
	t.Run("keep", func(t *testing.T) {
		r, stdin := setup(t)
		r, _ = r.update(key(tea.KeyDown))
		r, _ = r.update(key(tea.KeyEnter))
		if stdin.String() != issuerAnswer+"\n" {
			t.Fatalf("Keep sent %q, want the typed answer", stdin.String())
		}
	})
	t.Run("correct", func(t *testing.T) {
		r, stdin := setup(t)
		r, _ = r.update(key(tea.KeyEnter))
		if got := inputRowText(runScreen(r)); got != issuerAnswer || stdin.String() != "" {
			t.Fatalf("Correct: input %q, engine %q; want the answer kept and nothing sent", got, stdin.String())
		}
	})
}

// fakeIssuerClient is a client for a local TLS provider publishing
// issuer, dialling it whatever host a URL names.
func fakeIssuerClient(t *testing.T, issuer string) *http.Client {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": issuer})
	}))
	t.Cleanup(srv.Close)
	tr := srv.Client().Transport.(*http.Transport).Clone()
	addr := srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	tr.TLSClientConfig.ServerName = "example.com" // the test certificate's name
	return &http.Client{Transport: tr}
}

// Fault: a hostile provider's value reaching the terminal. The real
// checker runs against a provider publishing it; the raw view (escape
// codes and all) never holds it.
func TestIssuerCheck_HostileValueIsNeverDrawn(t *testing.T) {
	long := issuerAnswer + "/" + strings.Repeat("evil", 600) // over 2,048 bytes
	for name, c := range map[string]struct{ published, marker string }{
		"terminal title escape": {issuerAnswer + "\x1b]0;pwned\x07", "pwned"},
		"over 2,048 bytes":      {long, "evilevil"},
	} {
		t.Run(name, func(t *testing.T) {
			client := fakeIssuerClient(t, c.published)
			r, stdin := issuerRun(t, func(ctx context.Context, answer string) deploy.IssuerCheck {
				return deploy.CheckOidcIssuer(ctx, client, answer)
			})
			r = checkedWith(t, r, issuerAnswer)
			raw := r.view(80, 26)
			if strings.Contains(raw, c.marker) || strings.Contains(raw, "\x1b]0;") {
				t.Fatalf("the provider's value was drawn: %q", raw)
			}
			if !strings.Contains(runScreen(r), differentIssue) {
				t.Fatalf("the screen should still say %q:\n%s", differentIssue, runScreen(r))
			}
			if stdin.String() != "" {
				t.Fatalf("engine received %q", stdin.String())
			}
		})
	}
	// A slash difference whose value is withheld offers nothing: the
	// sentence without its colon, the answer reopened.
	t.Run("withheld slash value", func(t *testing.T) {
		r, stdin := issuerRun(t, answering(deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Withheld: true}).check)
		r = checkedWith(t, r, issuerAnswer)
		s := runScreen(r)
		if !strings.Contains(s, slashWith) || strings.Contains(s, slashWith+":") || strings.Contains(s, usePublished) {
			t.Fatalf("a withheld slash value should re-ask with %q and offer nothing:\n%s", slashWith, s)
		}
		if got := inputRowText(s); got != issuerAnswer || stdin.String() != "" {
			t.Fatalf("input %q, engine %q; want the answer reopened and nothing sent", got, stdin.String())
		}
	})
}

// squeezed is s with every space and line break removed, so a value the
// screen wrapped across rows is still found.
func squeezed(s string) string { return strings.Join(strings.Fields(s), "") }

// Fault: a published issuer too long for three rows of the text area at
// 80x26 drawn (whole, cut short or wrapped), or offered as "Use the
// published address". The owner decided this on 2026-10-30: it is
// withheld and the person is asked again, never shown a part of it.
// Paired: a published value that fits is drawn and offered.
func TestIssuerCheck_PublishedValueTooLongForThreeRowsIsWithheld(t *testing.T) {
	// A well-shaped printable-ASCII https issuer of 400 bytes whose
	// every 6-byte piece is different, so any part of it is recognisable.
	base := "https://login.example.test/realms/"
	var b strings.Builder
	b.WriteString(base)
	for i := 0; b.Len() < 400; i++ {
		fmt.Fprintf(&b, "p%03d-", i)
	}
	tooLong := b.String()[:400]
	if len(tooLong) != 400 || !deploy.WellShapedIssuer(tooLong) || !deploy.EchoableIssuer(tooLong) {
		t.Fatalf("setup: %d bytes, well-shaped %v, echoable %v", len(tooLong), deploy.WellShapedIssuer(tooLong), deploy.EchoableIssuer(tooLong))
	}

	t.Run("400 bytes is withheld", func(t *testing.T) {
		r, stdin := issuerRun(t, answering(deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Published: tooLong}).check)
		r = checkedWith(t, r, issuerAnswer)
		s := runScreen(r)
		for name, view := range map[string]string{"screen": squeezed(s), "raw view": squeezed(r.view(80, 26))} {
			for _, at := range []int{0, 180, 360} {
				if piece := tooLong[at : at+40]; strings.Contains(view, piece) {
					t.Fatalf("the %s draws part of the published value (bytes %d-%d):\n%s", name, at, at+40, s)
				}
			}
		}
		if strings.Contains(s, usePublished) {
			t.Fatalf("a value too long to show is offered as %q:\n%s", usePublished, s)
		}
		if !strings.Contains(s, slashWith) || strings.Contains(s, slashWith+":") {
			t.Fatalf("the reason row should read %q without a colon:\n%s", slashWith, s)
		}
		if got := inputRowText(s); got != issuerAnswer {
			t.Fatalf("the prompt reopened with %q, want %q kept:\n%s", got, issuerAnswer, s)
		}
		r, _ = r.update(key(tea.KeyEnter))
		if stdin.String() != "" {
			t.Fatalf("Enter sent %q; nothing was offered to send", stdin.String())
		}
	})
	t.Run("60 bytes is drawn and offered", func(t *testing.T) {
		fits := tooLong[:60]
		r, stdin := issuerRun(t, answering(deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Published: fits}).check)
		r = checkedWith(t, r, issuerAnswer)
		s := runScreen(r)
		for _, want := range []string{slashWith + ":", fits, usePublished, correctAnswer} {
			if !strings.Contains(s, want) {
				t.Fatalf("a value that fits should be drawn and offered; the screen lacks %q:\n%s", want, s)
			}
		}
		r, _ = r.update(key(tea.KeyEnter))
		if stdin.String() != fits+"\n" {
			t.Fatalf("Use sent %q, want the published %q", stdin.String(), fits+"\n")
		}
	})
}

// Fault: a result for an answer no longer being checked acted on.
// Paired: the current answer's result is.
func TestIssuerCheck_StaleResultIsDropped(t *testing.T) {
	changed := issuerAnswer + "x"
	seam := &issuerSeam{result: func(call int, _ string) deploy.IssuerCheck {
		if call == 1 {
			return deploy.IssuerCheck{Verdict: deploy.IssuerUnchecked, Reason: "the address could not be reached"}
		}
		return deploy.IssuerCheck{Verdict: deploy.IssuerMatches}
	}}
	r, stdin := issuerRun(t, seam.check)
	r = checkedWith(t, r, issuerAnswer)
	r, _ = r.update(key(tea.KeyEnter)) // Correct the answer
	r, _ = r.update(runeKey('x'))
	r, cmd := r.update(key(tea.KeyEnter))
	if !strings.Contains(runScreen(r), checkingLine) {
		t.Fatalf("the corrected answer is not being checked:\n%s", runScreen(r))
	}
	r, c := r.update(issuerCheckedMsg{answer: issuerAnswer, result: deploy.IssuerCheck{Verdict: deploy.IssuerMatches}})
	if stdin.String() != "" || c != nil {
		t.Fatalf("a stale result acted: engine %q, command %v", stdin.String(), c != nil)
	}
	if !strings.Contains(runScreen(r), checkingLine) {
		t.Fatalf("a stale result ended the check:\n%s", runScreen(r))
	}
	msg := requireIssuerCheck(t, cmd)
	if msg.answer != changed {
		t.Fatalf("the check was for %q, want %q", msg.answer, changed)
	}
	r, _ = r.update(msg)
	if stdin.String() != changed+"\n" {
		t.Fatalf("engine received %q, want the corrected answer", stdin.String())
	}

	// A result while nothing is being checked is dropped too.
	r2, stdin2 := issuerRun(t, seam.check)
	r2, _ = r2.update(paste(issuerAnswer))
	r2, c = r2.update(issuerCheckedMsg{answer: issuerAnswer, result: deploy.IssuerCheck{Verdict: deploy.IssuerMatches}})
	if stdin2.String() != "" || c != nil || inputRowText(runScreen(r2)) != issuerAnswer {
		t.Fatalf("a result with no check running acted: engine %q:\n%s", stdin2.String(), runScreen(r2))
	}
}

// Fault: Esc ignored while checking, the check left running after
// leaving, or its late result acting on a session that is gone.
func TestIssuerCheck_EscWhileCheckingLeavesTheSession(t *testing.T) {
	entered := make(chan struct{})
	returned := make(chan error, 1)
	check := func(ctx context.Context, answer string) deploy.IssuerCheck {
		close(entered)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
		returned <- ctx.Err()
		return deploy.IssuerCheck{Verdict: deploy.IssuerMatches}
	}
	stdin := &recordingStdin{}
	r := collectingRun(t, engineRunSeams{checkIssuer: check})
	r.cfg.stream = sleepingStream(t)
	r.cfg.stdin = stdin
	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: issuerPrompt}})
	r, cmd := submitIssuer(t, r, issuerAnswer)
	go issuerCheckWithin(cmd, 6*time.Second)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the check never started")
	}
	r, _ = r.update(key(tea.KeyEsc))
	if o := outcomeOf(r); !leftForMenu(o) {
		t.Fatalf("outcome = %+v after Esc while checking, want back to the main menu", o)
	}
	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("leaving did not cancel the check's context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the check kept running after the session was left")
	}
	state := r.state
	r, c := r.update(issuerCheckedMsg{answer: issuerAnswer, result: deploy.IssuerCheck{Verdict: deploy.IssuerMatches}})
	if c != nil || r.state != state || stdin.String() != "" || !leftForMenu(outcomeOf(r)) {
		t.Fatalf("a late result acted after leaving: state %v, engine %q, outcome %+v", r.state, stdin.String(), outcomeOf(r))
	}
}

// Fault: a surface taller than 26 rows or wider than 80 cells, or one
// that fits only by losing its rows.
func TestIssuerCheck_ScreensFitEightyByTwentySix(t *testing.T) {
	long := "https://id.example.test/realms/" + strings.Repeat("orbit-", 20)
	type surface struct {
		name  string
		build func(t *testing.T) engineRun
		shows []string
	}
	at := func(v deploy.IssuerCheck) func(t *testing.T) engineRun {
		return func(t *testing.T) engineRun {
			r, _ := issuerRun(t, answering(v).check)
			return checkedWith(t, r, long)
		}
	}
	surfaces := []surface{
		{"typing", func(t *testing.T) engineRun {
			r, _ := issuerRun(t, answering(deploy.IssuerCheck{}).check)
			r, _ = r.update(paste(long))
			return r
		}, []string{"OIDC issuer URL"}},
		{"checking", func(t *testing.T) engineRun {
			r, _ := issuerRun(t, answering(deploy.IssuerCheck{}).check)
			r, _ = submitIssuer(t, r, long)
			return r
		}, []string{checkingLine}},
		{"slash offered", at(deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Published: long + "/"}),
			[]string{slashWith + ":", usePublished, correctAnswer}},
		{"slash corrected", func(t *testing.T) engineRun {
			r := at(deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Published: long + "/"})(t)
			r, _ = r.update(key(tea.KeyDown))
			r, _ = r.update(key(tea.KeyEnter))
			return r
		}, []string{slashWith}},
		{"slash withheld", at(deploy.IssuerCheck{Verdict: deploy.IssuerSlashDiffers, Withheld: true}), []string{slashWith}},
		{"different issuer", at(deploy.IssuerCheck{Verdict: deploy.IssuerDiffers, Published: "https://login.example.test/realms/" + strings.Repeat("other-", 20)}),
			[]string{differentIssue}},
	}
	for _, reason := range []string{
		"the address could not be reached", "address not found", "certificate rejected", "no answer within 10s",
		"redirected to a non-https address", "the document is larger than 1 MiB", "the answer is not a JSON document",
		"the document names no issuer",
	} {
		surfaces = append(surfaces, surface{"cannot check: " + reason,
			at(deploy.IssuerCheck{Verdict: deploy.IssuerUnchecked, Reason: deploy.IssuerCheckReason(reason)}),
			[]string{"could not check this issuer: " + reason, correctAnswer, keepAsTyped}})
	}
	for _, sf := range surfaces {
		t.Run(sf.name, func(t *testing.T) {
			s := runScreen(sf.build(t))
			lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
			if len(lines) > 26 {
				t.Errorf("%d rows, over 26:\n%s", len(lines), s)
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > 80 {
					t.Errorf("row %d is %d cells, over 80: %q", i, w, l)
				}
			}
			for _, want := range sf.shows {
				if !strings.Contains(s, want) {
					t.Errorf("the screen lost %q:\n%s", want, s)
				}
			}
		})
	}
}
