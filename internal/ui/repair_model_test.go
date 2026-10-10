package ui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// Plain model tests for the Repair flow's error, cancel and edge paths:
// messages go straight to Update, and the assertions read the screen as
// a person would see it (ANSI stripped) and the outcome AppModel reads.

func sizedRepair(t *testing.T) RepairModel {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if cmd != nil {
		t.Fatal("a resize must not start a second tick chain")
	}
	return updated.(RepairModel)
}

func repairUpdate(t *testing.T, m RepairModel, msg tea.Msg) (RepairModel, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(msg)
	return updated.(RepairModel), cmd
}

// repairLines feeds stdout lines from the repair run, as the stream
// would deliver them.
func repairLines(t *testing.T, m RepairModel, lines ...string) RepairModel {
	t.Helper()
	for _, line := range lines {
		m, _ = repairUpdate(t, m, repairStreamMsg{msg: engine.RawLineMsg{Text: line}})
	}
	return m
}

func repairDone(t *testing.T, m RepairModel, done engine.DoneMsg) (RepairModel, tea.Cmd) {
	t.Helper()
	return repairUpdate(t, m, repairStreamMsg{msg: done})
}

func repairScreen(m RepairModel) string { return stripANSI(m.View().Content) }

// recordingStdin stands in for the rotation session's stdin pipe.
type recordingStdin struct {
	strings.Builder
	closed bool
}

func (r *recordingStdin) Close() error { r.closed = true; return nil }

// sleepingStream is a real engine run that would go on for a minute
// unless killed, so a test can prove the flow killed it.
func sleepingStream(t *testing.T) *engine.Stream {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	s, err := engine.Start(cmd)
	if err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(s.Kill)
	return s
}

// awaitEnd waits for a stream's DoneMsg and fails if it never comes.
func awaitEnd(t *testing.T, s *engine.Stream) engine.DoneMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-s.C:
			if !ok {
				t.Fatal("stream closed without a DoneMsg")
			}
			if d, isDone := msg.(engine.DoneMsg); isDone {
				return d
			}
		case <-deadline:
			t.Fatal("the stream was not killed: it was still running after 5s")
		}
	}
}

func TestRepairModel_RotationThatCannotStartShowsTheReason(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairRotating
	m, cmd := repairUpdate(t, m, repairRotateReadyMsg{err: errors.New("fetch repair.sh: connection refused")})
	if cmd != nil {
		t.Fatal("a failed start must not keep pumping a stream that does not exist")
	}
	if m.state != repairError {
		t.Fatalf("state = %v, want repairError", m.state)
	}
	s := repairScreen(m)
	for _, want := range []string{"Diagnosis couldn't run", "fetch repair.sh: connection refused", "▸ Menu", "Exit"} {
		if !strings.Contains(s, want) {
			t.Errorf("error screen lacks %q:\n%s", want, s)
		}
	}

	// The error screen's Menu row goes back to the main menu.
	m, _ = repairUpdate(t, m, key(tea.KeyEnter))
	if !m.Done || !m.WantsMenu {
		t.Fatalf("Menu on the error screen: Done=%v WantsMenu=%v, want both", m.Done, m.WantsMenu)
	}
}

func TestRepairModel_ErrorScreenExitQuits(t *testing.T) {
	m := sizedRepair(t)
	m, _ = repairUpdate(t, m, repairReadyMsg{err: errors.New("target directory: no such file")})
	m, _ = repairUpdate(t, m, key(tea.KeyDown))
	_, cmd := repairUpdate(t, m, key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("Exit on the error screen did not quit")
	}
}

func TestRepairModel_DiagnosisCrashIsAnErrorNotAVerdict(t *testing.T) {
	m := sizedRepair(t)
	m = repairLines(t, m, "finding class=secret-missing target=session-secret severity=warn")
	m, cmd := repairDone(t, m, engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1")})
	if cmd != nil {
		t.Fatal("an unexpected exit must not start another run")
	}
	s := repairScreen(m)
	if !strings.Contains(s, "Diagnosis couldn't run") || !strings.Contains(s, "exit status 1") {
		t.Fatalf("exit 1 should be reported as a failed run with its error:\n%s", s)
	}
	if strings.Contains(s, "Diagnosis clear") || strings.Contains(s, "Problems found") {
		t.Fatalf("a crashed run must not render a diagnosis verdict:\n%s", s)
	}
}

func TestRepairModel_UsageErrorFromCheckModeIsAnError(t *testing.T) {
	// --plan falling back to --check is the one retry; a --check that is
	// itself rejected as a usage error has nothing further to fall back to.
	m := sizedRepair(t)
	m.mode = deploy.RepairCheck
	m, cmd := repairDone(t, m, engine.DoneMsg{ExitCode: 2, Err: errors.New("exit status 2")})
	if cmd != nil {
		t.Fatal("a --check usage error must not retry")
	}
	if m.state != repairError || !strings.Contains(repairScreen(m), "exit status 2") {
		t.Fatalf("state = %v, screen:\n%s", m.state, repairScreen(m))
	}
}

func TestRepairModel_EscWhileReadingKillsTheRunAndReturnsToMenu(t *testing.T) {
	m := sizedRepair(t)
	if !strings.Contains(repairScreen(m), "reading the deployment — nothing will be changed") {
		t.Fatalf("preparing screen:\n%s", repairScreen(m))
	}
	s := sleepingStream(t)
	m, _ = repairUpdate(t, m, repairReadyMsg{stream: s})

	m, _ = repairUpdate(t, m, key(tea.KeyEsc))
	if !m.Done || !m.WantsMenu {
		t.Fatalf("Esc while reading: Done=%v WantsMenu=%v, want both", m.Done, m.WantsMenu)
	}
	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Fatalf("the abandoned run exited cleanly (%+v); it should have been killed", d)
	}
}

func TestRepairModel_EscWhileExecutingDoesNotAbandonTheRepair(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	if !strings.Contains(repairScreen(m), "running the safe repairs") {
		t.Fatalf("executing screen:\n%s", repairScreen(m))
	}
	s := sleepingStream(t)
	m, _ = repairUpdate(t, m, repairReadyMsg{stream: s})

	for _, k := range []tea.KeyPressMsg{key(tea.KeyEsc), key(tea.KeyEnter), runeKey('q')} {
		m, _ = repairUpdate(t, m, k)
	}
	if m.Done || m.state != repairExecuting {
		t.Fatalf("keys during a mutation changed the flow: Done=%v state=%v", m.Done, m.state)
	}
	select {
	case msg := <-s.C:
		t.Fatalf("the running repair was interrupted: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRepairModel_CtrlCKillsTheRunAndQuits(t *testing.T) {
	m := sizedRepair(t)
	s := sleepingStream(t)
	m, _ = repairUpdate(t, m, repairReadyMsg{stream: s})
	_, cmd := repairUpdate(t, m, ctrlC())
	if !isQuit(cmd) {
		t.Fatal("Ctrl+C did not quit")
	}
	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Fatalf("Ctrl+C left the run going (%+v)", d)
	}
}

// Ctrl-C before the run exists must not leave the fetch-and-start going
// unobserved: it cancels the preparation and quits only once the
// preparation has reported back.
func TestRepairModel_CtrlCWhilePreparingCancelsAndQuitsOnceTheRunReportsBack(t *testing.T) {
	m := sizedRepair(t)
	var captured context.Context
	m.prepare = func(ctx context.Context, _ string, _ deploy.RepairMode) (*engine.Stream, error) {
		captured = ctx
		return nil, errors.New("not now")
	}
	ready := m.Init()()

	m, cmd := repairUpdate(t, m, ctrlC())
	if cmd != nil {
		t.Fatal("Ctrl+C while preparing quit before the preparation reported back")
	}
	if !errors.Is(captured.Err(), context.Canceled) {
		t.Fatalf("Ctrl+C did not cancel the preparation: ctx err = %v", captured.Err())
	}
	if _, cmd = repairUpdate(t, m, ready); !isQuit(cmd) {
		t.Fatal("the flow did not quit once the cancelled preparation reported back")
	}
}

// A diagnosis is read-only: one that started as Ctrl-C landed is
// killed and the flow quits.
func TestRepairModel_CtrlCWhileReadingKillsTheDiagnosisThatStartedAnyway(t *testing.T) {
	m := sizedRepair(t)
	s := sleepingStream(t)

	m, cmd := repairUpdate(t, m, ctrlC())
	if cmd != nil {
		t.Fatal("Ctrl+C before the run existed quit before it reported back")
	}
	if _, cmd = repairUpdate(t, m, repairReadyMsg{stream: s}); !isQuit(cmd) {
		t.Fatal("the flow did not quit once the diagnosis arrived")
	}
	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Fatalf("a diagnosis that started after Ctrl+C was left going (%+v)", d)
	}
}

// Ctrl-C before a mutation exists cancels its fetch-and-start and quits
// once the preparation reports back with nothing started.
func TestRepairModel_CtrlCBeforeAMutationStartsCancelsAndQuits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state repairState
		// openInput hands the rotation an input pipe but no run.
		openInput bool
	}{
		{"safe repairs", repairExecuting, false},
		{"credential rotation", repairRotating, false},
		{"credential rotation with its input open", repairRotating, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedRepair(t)
			m.state = tc.state
			var captured context.Context
			var stdin io.WriteCloser
			input := &recordingStdin{}
			if tc.openInput {
				stdin = input
			}
			m.prepare = func(ctx context.Context, _ string, _ deploy.RepairMode) (*engine.Stream, error) {
				captured = ctx
				return nil, context.Canceled
			}
			m.prepareRotate = func(ctx context.Context, _ string) (*engine.Stream, io.WriteCloser, error) {
				captured = ctx
				return nil, stdin, context.Canceled
			}
			var ready tea.Msg
			if tc.state == repairRotating {
				ready = m.startRotate()()
			} else {
				ready = m.startRun(deploy.RepairExecuteSafe)()
			}

			m, cmd := repairUpdate(t, m, ctrlC())
			if cmd != nil {
				t.Fatal("Ctrl+C before the run existed quit before it reported back")
			}
			if !errors.Is(captured.Err(), context.Canceled) {
				t.Fatalf("Ctrl+C did not cancel the preparation: ctx err = %v", captured.Err())
			}
			if _, cmd = repairUpdate(t, m, ready); !isQuit(cmd) {
				t.Fatal("the flow did not quit once the cancelled preparation reported back")
			}
			if tc.openInput && !input.closed {
				t.Fatal("the rotation's input was left open")
			}
		})
	}
}

// assertKeptRunning runs the flow's next command, the reader on an
// adopted run, and fails if it quits or the run says anything through
// pushed within 100 ms: a kept run is never signalled. The reader is
// left waiting on the run, which the test's cleanup kills.
func assertKeptRunning(t *testing.T, cmd tea.Cmd, pushed *sink) {
	t.Helper()
	if cmd == nil {
		t.Fatal("nothing is watching the run that started")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("the run that started as Ctrl+C landed was quit or interrupted: %#v", msg)
	}
	select {
	case msg := <-pushed.msgs:
		t.Fatalf("the run that started as Ctrl+C landed was quit or interrupted: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

// A mutation the engine started just as Ctrl-C landed is kept, not
// killed: a signal milliseconds after start is exactly "stopping
// halfway". The launcher stays on screen and watches it; a rotation's
// input is closed at once so the engine backs out at its first prompt.
func TestRepairModel_CtrlCWhileARepairIsStartingKeepsTheRunThatStartedAnyway(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  repairState
		ready  func(s *engine.Stream, stdin *recordingStdin) tea.Msg
		screen string
	}{
		{"safe repairs", repairExecuting, func(s *engine.Stream, _ *recordingStdin) tea.Msg {
			return repairReadyMsg{stream: s}
		}, "can't be stopped from here"},
		{"credential rotation", repairRotating, func(s *engine.Stream, stdin *recordingStdin) tea.Msg {
			return repairRotateReadyMsg{stream: s, stdin: stdin}
		}, "stopping — waiting for the engine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedRepair(t)
			pushed := newSink()
			m.send = pushed.Send
			m.state = tc.state
			s, stdin := sleepingStream(t), &recordingStdin{}

			m, cmd := repairUpdate(t, m, ctrlC())
			if cmd != nil {
				t.Fatal("Ctrl+C before the run existed quit before it reported back")
			}
			if !errors.Is(m.ctx.Err(), context.Canceled) {
				t.Fatalf("Ctrl+C did not cancel the preparation: ctx err = %v", m.ctx.Err())
			}

			m, cmd = repairUpdate(t, m, tc.ready(s, stdin))
			if m.stream != s {
				t.Fatal("the flow did not take over the run that started")
			}
			if m.state != tc.state || m.Done {
				t.Fatalf("the run's arrival changed the flow: state=%v Done=%v", m.state, m.Done)
			}
			if tc.state == repairRotating && !stdin.closed {
				t.Fatal("the rotation's input was left open")
			}
			if sc := repairScreen(m); !strings.Contains(sc, tc.screen) {
				t.Fatalf("screen lacks %q:\n%s", tc.screen, sc)
			}
			assertKeptRunning(t, cmd, pushed)
		})
	}
}

// The kept run gets a fresh context: a later "Diagnose again" must not
// fetch on the one Ctrl-C cancelled.
func TestRepairModel_DiagnoseAgainWorksAfterARunWasKeptPastCtrlC(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	m, _ = repairUpdate(t, m, ctrlC())
	m, _ = repairUpdate(t, m, repairReadyMsg{stream: sleepingStream(t)})
	m = repairLines(t, m, "execution result=complete done=1 failed=0", "diagnosis result=healthy checked=15 skipped=0")
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
	if m.state != repairExecuted {
		t.Fatalf("state = %v, want repairExecuted", m.state)
	}

	var captured context.Context
	m.prepare = func(ctx context.Context, _ string, _ deploy.RepairMode) (*engine.Stream, error) {
		captured = ctx
		return nil, errors.New("stop here")
	}
	_, cmd := repairUpdate(t, m, key(tea.KeyEnter)) // "Diagnose again" is first
	if cmd == nil {
		t.Fatal("Diagnose again started nothing")
	}
	cmd()
	if err := captured.Err(); err != nil {
		t.Fatalf("Diagnose again fetched on a cancelled context: %v", err)
	}
}

// Every screen without a mutation in flight keeps Ctrl-C as
// kill-and-quit.
func TestRepairModel_CtrlCElsewhereKillsAnyRunAndQuits(t *testing.T) {
	for _, state := range []repairState{repairDiagnosis, repairUnavailable, repairError, repairExecuted} {
		m := sizedRepair(t)
		m.state = state
		if _, cmd := repairUpdate(t, m, ctrlC()); !isQuit(cmd) {
			t.Fatalf("state %v: Ctrl+C did not quit", state)
		}
	}
	m := sizedRepair(t)
	m.state = repairDiagnosis
	s := sleepingStream(t)
	m.stream = s
	if _, cmd := repairUpdate(t, m, ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl+C with a run did not quit")
	}
	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Fatalf("Ctrl+C left the run going (%+v)", d)
	}
}

func TestRepairModel_FailedExecutionWithoutSummaryIsAnError(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1")})
	if m.state != repairError {
		t.Fatalf("state = %v, want repairError", m.state)
	}
	if !strings.Contains(repairScreen(m), "exit status 1") {
		t.Fatalf("error screen:\n%s", repairScreen(m))
	}
}

func TestRepairModel_RefusedDangerousBatchSaysNothingWasRotated(t *testing.T) {
	cases := []struct {
		name, reason, extra string
	}{
		{"declined", "declined", ""},
		{"no terminal", "non-interactive", "(no terminal was available to approve it)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedRepair(t)
			m.state = repairRotating
			m = repairLines(t, m, "dangerous result=refused done=0 failed=0 reason="+tc.reason)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: engine.ExitDangerousRefused, Err: errors.New("exit status 6")})
			if m.state != repairExecuted {
				t.Fatalf("exit 6 is an outcome, not an error: state = %v", m.state)
			}
			s := repairScreen(m)
			if !strings.Contains(s, "credentials left as they were — nothing was rotated") {
				t.Fatalf("refusal not stated:\n%s", s)
			}
			if tc.extra != "" && !strings.Contains(s, tc.extra) {
				t.Fatalf("refusal reason %q not stated:\n%s", tc.extra, s)
			}
			if tc.extra == "" && strings.Contains(s, "no terminal") {
				t.Fatalf("a declined rotation must not blame the terminal:\n%s", s)
			}
			if strings.Contains(s, "Repairs applied") || strings.Contains(s, "failed") {
				t.Fatalf("a refused batch neither applied nor failed anything:\n%s", s)
			}
		})
	}
}

func TestRepairModel_ExecutionOutcomeTitles(t *testing.T) {
	cases := map[string]string{
		"complete":     "Repairs applied",
		"declined":     "Nothing was changed",
		"unactionable": "Nothing safe to run",
		"empty":        "Nothing to repair",
		"failed":       "Some repairs failed",
		"interrupted":  "Repair run ended", // unknown result: honest fallback
	}
	for result, title := range cases {
		t.Run(result, func(t *testing.T) {
			m := sizedRepair(t)
			m.state = repairExecuting
			m = repairLines(t, m, "execution result="+result+" done=0 failed=0")
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
			s := repairScreen(m)
			if !strings.Contains(s, title) {
				t.Fatalf("execution result=%s should be titled %q:\n%s", result, title, s)
			}
			if strings.Contains(s, "done ·") {
				t.Fatalf("a zero tally is not worth a line:\n%s", s)
			}
		})
	}
}

func TestRepairModel_ExecutionWithoutSummaryButCleanExitEndsOnAfterPicture(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
	if m.state != repairExecuted || !strings.Contains(repairScreen(m), "Repair run ended") {
		t.Fatalf("state = %v, screen:\n%s", m.state, repairScreen(m))
	}
}

func TestRepairModel_PartialRepairListsEachStepAndWhatStillStands(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	m = repairLines(t, m,
		"execute action=fix-permissions resolves=managed-file-permissions result=done",
		"execute action=restart-services resolves=application-unhealthy result=failed",
		"execute action=restore-transaction resolves=staging-evidence-present result=skipped",
		"execution result=failed done=1 failed=1",
		"finding class=secret-missing target=session-secret severity=info",
		"finding class=application-unhealthy target=application severity=fail",
		"diagnosis result=failed checked=15 skipped=0",
	)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})
	s := repairScreen(m)
	for _, want := range []string{
		"Some repairs failed",
		"✓ restore safe permissions — permissions aren't restricted to the owner",
		"✗ restart Orbit's services — reports unhealthy",
		"restore the interrupted install transaction — an interrupted install left staging behind",
		"1 done · 1 failed",
		"still standing after repairs:",
		"application — reports unhealthy",
		"session-secret secret — absent or empty",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("after-picture lacks %q:\n%s", want, s)
		}
	}
	// What still fails is listed before what is merely informational.
	if strings.Index(s, "application — reports unhealthy") > strings.Index(s, "session-secret secret") {
		t.Errorf("findings not ordered fail before info:\n%s", s)
	}
	if strings.Contains(s, "diagnosis clear after repairs") {
		t.Errorf("an unhealthy re-diagnosis must not read as clear:\n%s", s)
	}
}

func TestRepairModel_DiagnoseAgainStartsAFreshPlanRun(t *testing.T) {
	m := sizedRepair(t)
	var modes []deploy.RepairMode
	m.prepare = func(_ context.Context, _ string, mode deploy.RepairMode) (*engine.Stream, error) {
		modes = append(modes, mode)
		return nil, errors.New("stop here")
	}
	m.mode = deploy.RepairCheck
	m.planActions = []engine.PlanAction{{Action: "fix-permissions", Resolves: "managed-file-permissions", Mutation: "reversible", Backup: "not-required"}}
	m.state = repairExecuting
	m = repairLines(t, m, "execution result=complete done=1 failed=0", "diagnosis result=healthy checked=15 skipped=0")
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})

	m, cmd := repairUpdate(t, m, key(tea.KeyEnter)) // "Diagnose again" is first
	if m.state != repairPreparing {
		t.Fatalf("state = %v, want repairPreparing", m.state)
	}
	if m.planActions != nil || m.diagnosis != nil || m.execSummary != nil {
		t.Fatal("the old run's evidence must not carry into the fresh diagnosis")
	}
	if !strings.Contains(repairScreen(m), "reading the deployment") {
		t.Fatalf("screen:\n%s", repairScreen(m))
	}
	if cmd == nil {
		t.Fatal("Diagnose again started nothing")
	}
	cmd()
	if len(modes) != 1 || modes[0] != deploy.RepairPlan {
		t.Fatalf("Diagnose again ran modes %v, want one --plan run", modes)
	}
}

func TestRepairModel_AfterPictureMenuAndExitRows(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})

	toMenu, _ := repairUpdate(t, m, key(tea.KeyDown))
	toMenu, _ = repairUpdate(t, toMenu, key(tea.KeyEnter))
	if !toMenu.Done || !toMenu.WantsMenu {
		t.Fatalf("Menu row: Done=%v WantsMenu=%v", toMenu.Done, toMenu.WantsMenu)
	}

	// Up from the first row wraps to the last, Exit.
	wrapped, _ := repairUpdate(t, m, key(tea.KeyUp))
	if !strings.Contains(repairScreen(wrapped), "▸ Exit") {
		t.Fatalf("Up from the top did not wrap to Exit:\n%s", repairScreen(wrapped))
	}
	_, cmd := repairUpdate(t, wrapped, key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("Exit row did not quit")
	}

	// Esc anywhere on a menu screen goes back to the main menu; an
	// unbound key does nothing at all.
	ignored, cmd := repairUpdate(t, m, runeKey('x'))
	if cmd != nil || ignored.Done || ignored.menuSel != 0 {
		t.Fatalf("an unbound key changed the screen: cmd=%v Done=%v sel=%d", cmd != nil, ignored.Done, ignored.menuSel)
	}
	esc, _ := repairUpdate(t, m, key(tea.KeyEsc))
	if !esc.Done || !esc.WantsMenu {
		t.Fatal("Esc did not return to the main menu")
	}
}

func TestRepairModel_RotationPromptEditingAndRejection(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairRotating
	if !strings.Contains(repairScreen(m), "talking to the engine…") {
		t.Fatalf("before the first prompt:\n%s", repairScreen(m))
	}

	// Keys before any prompt has arrived go nowhere.
	m, _ = repairUpdate(t, m, runeKey('r'))
	if len(m.rotInput) != 0 {
		t.Fatalf("typing before a prompt was captured: %q", string(m.rotInput))
	}

	stdin := &recordingStdin{}
	m, _ = repairUpdate(t, m, repairRotateReadyMsg{stream: &engine.Stream{C: make(chan any)}, stdin: stdin})
	m = repairLines(t, m, "prompt field=action-word kind=typed-word required=true attempt=1")
	for _, k := range []tea.KeyPressMsg{runeKey('r'), runeKey('o'), runeKey('x'), key(tea.KeyBackspace), key(tea.KeySpace), key(tea.KeyBackspace), runeKey('t')} {
		m, _ = repairUpdate(t, m, k)
	}
	if !strings.Contains(repairScreen(m), "rot▏") {
		t.Fatalf("the typed word should read rot after the edits:\n%s", repairScreen(m))
	}
	m, _ = repairUpdate(t, m, key(tea.KeyEnter))
	if stdin.String() != "rot\n" {
		t.Fatalf("stdin got %q, want the edited word and a newline", stdin.String())
	}

	// The engine rejects it and asks again: the reason is shown in words,
	// and the attempt count says how many tries are left.
	m = repairLines(t, m,
		"prompt-reject field=action-word reason=empty",
		"prompt field=action-word kind=typed-word required=true attempt=2",
	)
	s := repairScreen(m)
	if !strings.Contains(s, "cannot be empty") || !strings.Contains(s, "attempt 2 of 3") {
		t.Fatalf("rejection or attempt not shown:\n%s", s)
	}

	// Backspace on an empty answer is harmless.
	m, _ = repairUpdate(t, m, key(tea.KeyBackspace))
	if len(m.rotInput) != 0 {
		t.Fatal("backspace on empty input produced input")
	}

	// An abort clears the prompt; the screen waits on the engine again.
	m = repairLines(t, m, "prompt-abort field=action-word")
	if !strings.Contains(repairScreen(m), "talking to the engine…") {
		t.Fatalf("after abort:\n%s", repairScreen(m))
	}

	// Esc closes stdin — the engine's documented abort — and waits for
	// the engine to exit rather than leaving the session behind.
	m, _ = repairUpdate(t, m, key(tea.KeyEsc))
	if !stdin.closed {
		t.Fatal("Esc did not close the session's stdin")
	}
	if m.state != repairRotating {
		t.Fatalf("state = %v, want repairRotating until the engine exits", m.state)
	}

	// A second Esc with input already closed does nothing.
	m, cmd := repairUpdate(t, m, key(tea.KeyEsc))
	if cmd != nil || m.state != repairRotating {
		t.Fatalf("a second Esc changed the flow: cmd=%v state=%v", cmd != nil, m.state)
	}
}

func TestRepairModel_RotationReasonClearsOnAccept(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairRotating
	m = repairLines(t, m,
		"prompt-reject field=checkpoint-passphrase reason=too-large",
		"prompt-accept field=checkpoint-passphrase",
		"prompt field=checkpoint-passphrase-confirm kind=secret required=true attempt=1",
	)
	s := repairScreen(m)
	if strings.Contains(s, "too large") {
		t.Fatalf("a rejection survived the accept that followed it:\n%s", s)
	}
	if !strings.Contains(s, "Confirm the passphrase") {
		t.Fatalf("next prompt not shown:\n%s", s)
	}
}

func TestRepairModel_StreamMessagesAfterAbandonAreIgnored(t *testing.T) {
	m := sizedRepair(t)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
	before := repairScreen(m)
	m, cmd := repairUpdate(t, m, repairStreamMsg{msg: engine.RawLineMsg{Text: "finding class=secret-missing target=session-secret severity=fail"}})
	if cmd != nil || repairScreen(m) != before {
		t.Fatal("a late line from a finished run changed the screen or kept pumping")
	}
}

func TestRepairModel_EventsAndUnknownMessagesDoNotDisturbTheRun(t *testing.T) {
	m := sizedRepair(t)
	m.stream = &engine.Stream{C: make(chan any)}
	// The shared reader keeps reading whatever the handler returns
	// (#206), so what an event line must not do is end or fail the run.
	m, cmd := repairUpdate(t, m, repairStreamMsg{msg: engine.EventMsg{Event: engine.Event{Phase: "host"}}})
	if cmd != nil || m.state != repairPreparing || m.runErr != nil {
		t.Fatal("an event line disturbed the run")
	}
	m, cmd = repairUpdate(t, m, repairStreamMsg{msg: 42})
	if cmd != nil {
		t.Fatal("an unknown stream message should be dropped, not pumped on")
	}
	m, cmd = repairUpdate(t, m, struct{}{})
	if cmd != nil || m.state != repairPreparing {
		t.Fatal("an unrelated message changed the flow")
	}
}

func TestRepairModel_UnsizedViewIsBlank(t *testing.T) {
	if got := NewRepairModel("/opt/orbit", "v").View().Content; got != "" {
		t.Fatalf("before the first resize the view should be blank, got %q", got)
	}
}

func TestRepairModel_TickAdvancesAndRearms(t *testing.T) {
	m := sizedRepair(t)
	if _, cmd := repairUpdate(t, m, tickMsg{}); cmd == nil {
		t.Fatal("a tick must re-arm the one tick chain")
	}
}

func TestRepairModel_PlanSummaryWordsUnderThePlan(t *testing.T) {
	const restart = "plan action=restart-services resolves=stale-container mutation=reversible backup=not-required"
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"no summary", []string{restart}, "nothing has run yet — pick a repair below to run it"},
		{"manual alongside a runnable step", []string{restart, "plan result=manual-required actions=0 manual=1"}, "some steps need your hands — the rest can run from the menu below"},
		{"unknown result", []string{restart, "plan result=blocked actions=0 manual=0"}, "blocked — nothing has run yet"},
		{"manual only", []string{
			"plan action=manual resolves=volume-retained-without-credentials mutation=none backup=not-required",
			"plan result=manual-required actions=0 manual=1",
		}, "some steps need your hands — nothing has run yet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedRepair(t)
			m = repairLines(t, m, tc.lines...)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4})
			s := repairScreen(m)
			if !strings.Contains(s, tc.want) {
				t.Fatalf("plan summary should read %q:\n%s", tc.want, s)
			}
			if strings.Contains(s, "execution arrives") {
				t.Fatalf("the plan says execution is unavailable while the menu offers it:\n%s", s)
			}
		})
	}
}

func TestRepairModel_UnsummarisedPlanNeedsAttention(t *testing.T) {
	m := sizedRepair(t)
	m = repairLines(t, m,
		"plan action=rerun-configuration resolves=configuration-incomplete mutation=none backup=not-required",
		"plan result=ready actions=1 manual=0",
	)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4})
	s := repairScreen(m)
	if !strings.Contains(s, "Needs your attention") || !strings.Contains(s, "re-run guided configuration — required fields aren't ready") {
		t.Fatalf("screen:\n%s", s)
	}
}

// TestFindingLine_WordsForEveryContractEnum pins the words each target
// and reason class renders as: these are what a person reads, and an
// enum that slipped back to its raw form would be noticed only by them.
func TestFindingLine_WordsForEveryContractEnum(t *testing.T) {
	targets := map[string]string{
		"directory":          "target directory",
		"env-file":           ".env-orbit",
		"compose-file":       "docker-compose.yml",
		"compose":            "compose configuration",
		"configuration":      "configuration",
		"secrets-directory":  "secrets directory",
		"postgres-password":  "postgres-password secret",
		"document-kek":       "document-kek secret",
		"oidc-client-secret": "oidc-client-secret secret",
		"staging":            "installer staging",
		"container":          "containers",
		"database-volume":    "database volume",
		"database":           "database",
		"application":        "application",
		"future-target":      "future-target",
	}
	for target, want := range targets {
		got := stripANSI(findingLine(engine.Finding{Class: "x", Target: target, Severity: "info"}))
		if !strings.Contains(got, " "+want+" — x") {
			t.Errorf("target %q renders %q, want %q", target, got, want)
		}
	}

	classes := map[string]string{
		"not-orbit-directory":                 "no recognizable Orbit installation",
		"managed-file-missing":                "missing",
		"managed-file-symlink":                "is a symlink, refusing to trust it",
		"managed-file-permissions":            "permissions aren't restricted to the owner",
		"secrets-directory-invalid":           "missing, symlinked or permissions too open",
		"secret-missing":                      "absent or empty",
		"secret-permissions":                  "wrong type or permissions",
		"configuration-incomplete":            "required fields aren't ready",
		"configuration-invalid":               "unreadable or structurally broken",
		"staging-evidence-present":            "an interrupted install left staging behind",
		"compose-interpolation-failed":        "compose files don't resolve",
		"docker-unavailable":                  "docker couldn't be reached for this check",
		"container-foreign-owner":             "a container in this project isn't Orbit's",
		"volume-retained-without-credentials": "database data kept but its credentials are gone",
		"unrelated-resource-present":          "an unrelated Orbit-like volume exists",
		"database-unreachable":                "can't be reached",
		"database-credential-mismatch":        "rejects the stored credentials",
		"stale-container":                     "running an older image than configured",
		"application-unhealthy":               "reports unhealthy",
		"unsupported-schema":                  "schema newer than this engine supports",
		"migration-failed":                    "a migration failed",
		"image-identity-mismatch":             "image identity doesn't match the record",
		"future-class":                        "future-class",
	}
	for class, want := range classes {
		got := stripANSI(findingLine(engine.Finding{Class: class, Target: "database", Severity: "warn"}))
		if !strings.HasSuffix(got, "database — "+want) {
			t.Errorf("class %q renders %q, want it to end %q", class, got, want)
		}
	}
}

func TestPlanLine_ActionWords(t *testing.T) {
	actions := map[string]string{
		"fix-permissions":            "restore safe permissions",
		"rerun-configuration":        "re-run guided configuration",
		"restore-transaction":        "restore the interrupted install transaction",
		"rotate-database-credential": "rotate database credentials",
		"regenerate-secret":          "regenerate the secret",
		"restart-services":           "restart Orbit's services",
		"manual":                     "needs your hands",
		"future-action":              "future-action",
	}
	for action, want := range actions {
		got := stripANSI(planLine(engine.PlanAction{Action: action, Resolves: "migration-failed", Backup: "not-required"}))
		if !strings.Contains(got, want+" — a migration failed") {
			t.Errorf("action %q renders %q, want %q", action, got, want)
		}
	}
}

func TestSortedFindings_UnknownSeverityGoesLastAndOrderIsStable(t *testing.T) {
	in := []engine.Finding{
		{Class: "a", Severity: "notice"},
		{Class: "b", Severity: "info"},
		{Class: "c", Severity: "fail"},
		{Class: "d", Severity: "weird"},
		{Class: "e", Severity: "warn"},
	}
	var got []string
	for _, f := range sortedFindings(in) {
		got = append(got, f.Class)
	}
	if strings.Join(got, "") != "cebad" {
		t.Fatalf("order = %v, want fail, warn, info, then unknown in engine order", got)
	}
	if in[0].Class != "a" {
		t.Fatal("sortedFindings reordered its input")
	}
}

// isQuit reports whether cmd is bubbletea's quit command.
func isQuit(cmd tea.Cmd) bool { return cmd != nil && cmd() == tea.Quit() }

func TestRepairModel_CtrlCWhileExecutingDoesNotKillTheRepair(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	s := sleepingStream(t)
	m, _ = repairUpdate(t, m, repairReadyMsg{stream: s})

	m, cmd := repairUpdate(t, m, ctrlC())
	if isQuit(cmd) {
		t.Fatal("Ctrl+C during a mutation quit the launcher")
	}
	if m.Done || m.state != repairExecuting {
		t.Fatalf("Ctrl+C during a mutation changed the flow: Done=%v state=%v", m.Done, m.state)
	}
	select {
	case msg := <-s.C:
		t.Fatalf("the running repair was interrupted: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRepairModel_ExecutingScreenSaysItCannotBeStopped(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	if s := repairScreen(m); !strings.Contains(s, "can't be stopped from here") {
		t.Fatalf("executing screen does not say it cannot be stopped:\n%s", s)
	}
}

func TestRepairModel_EscWhileRotatingClosesInputAndWaitsForTheEngine(t *testing.T) {
	for _, tc := range []struct {
		name string
		k    tea.KeyPressMsg
	}{
		{"esc", key(tea.KeyEsc)},
		{"ctrl+c", ctrlC()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedRepair(t)
			m.state = repairRotating
			s := sleepingStream(t)
			stdin := &recordingStdin{}
			m, _ = repairUpdate(t, m, repairRotateReadyMsg{stream: s, stdin: stdin})
			m = repairLines(t, m, "prompt field=action-word kind=typed-word required=true attempt=1")

			m, cmd := repairUpdate(t, m, tc.k)
			if !stdin.closed {
				t.Fatal("the key did not close the session's input")
			}
			if isQuit(cmd) {
				t.Fatal("the key quit the launcher while the engine was still running")
			}
			if m.state != repairRotating {
				t.Fatalf("state = %v, want repairRotating until the engine exits", m.state)
			}
			if sc := repairScreen(m); !strings.Contains(sc, "stopping — waiting for the engine") {
				t.Fatalf("screen does not say it is waiting for the engine:\n%s", sc)
			}
			select {
			case msg := <-s.C:
				t.Fatalf("the rotation was killed instead of left to back out: %#v", msg)
			case <-time.After(100 * time.Millisecond):
			}

			// The engine backs out at its prompt and exits 6: the run ends
			// on the after-picture like every execution.
			m = repairLines(t, m, "dangerous result=refused done=0 failed=0 reason=refused-by-operator")
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 6})
			if m.state != repairExecuted {
				t.Fatalf("state = %v, want repairExecuted", m.state)
			}
			if sc := repairScreen(m); !strings.Contains(sc, "credentials left as they were — nothing was rotated") {
				t.Fatalf("after-picture does not say nothing was rotated:\n%s", sc)
			}
		})
	}
}

func TestRepairModel_RefusedSafeRunSaysWhy(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	m = repairLines(t, m, "execution result=refused done=0 failed=0 reason=deployment-version-unsupported")
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 6, Err: errors.New("exit status 6")})
	if m.state != repairExecuted {
		t.Fatalf("exit 6 is an outcome, not an error: state = %v", m.state)
	}
	s := repairScreen(m)
	for _, want := range []string{"Repair refused", "too old to repair here", "then diagnose again"} {
		if !strings.Contains(s, want) {
			t.Errorf("refused run lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "nothing was rotated") {
		t.Fatalf("a safe run that never offered a rotation must not report one refused:\n%s", s)
	}
}

// A refusal for a reason the launcher has no words for still says
// nothing was changed and names the reason as given; with no reason it
// says only that the engine refused. Neither claims a too-old release or
// a rotation nobody asked for.
func TestRepairModel_RefusedRunForAnotherReasonNamesIt(t *testing.T) {
	cases := []struct {
		name, line, want string
	}{
		{"named reason", "execution result=refused done=0 failed=0 reason=lock-held", "nothing was changed — the engine refused to run (lock-held)"},
		{"no reason", "execution result=refused done=0 failed=0", "nothing was changed — the engine refused to run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sizedRepair(t)
			m.state = repairExecuting
			m = repairLines(t, m, tc.line)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 6, Err: errors.New("exit status 6")})
			s := repairScreen(m)
			for _, want := range []string{"Repair refused", tc.want} {
				if !strings.Contains(s, want) {
					t.Errorf("refused run lacks %q:\n%s", want, s)
				}
			}
			if tc.name == "no reason" && strings.Contains(s, "refused to run (") {
				t.Errorf("a refusal with no reason shows an empty or invented one:\n%s", s)
			}
			for _, wrong := range []string{"too old to repair here", "nothing was rotated"} {
				if strings.Contains(s, wrong) {
					t.Errorf("refused run claims %q:\n%s", wrong, s)
				}
			}
		})
	}
}

// A plan with nothing runnable and no summary line says nothing has run,
// and does not point at a menu that offers no repair.
func TestRepairModel_UnsummarisedManualOnlyPlanDoesNotPointAtTheMenu(t *testing.T) {
	m := sizedRepair(t)
	m = repairLines(t, m, "plan action=manual resolves=volume-retained-without-credentials mutation=none backup=not-required")
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4})
	s := repairScreen(m)
	if !strings.Contains(s, "nothing has run yet") {
		t.Fatalf("plan summary should read \"nothing has run yet\":\n%s", s)
	}
	if strings.Contains(s, "pick a repair below") || strings.Contains(s, "execution arrives") {
		t.Fatalf("the plan points at a repair the menu does not offer:\n%s", s)
	}
}

// deploymentRepair places repair.sh in dir/scripts the way install.sh
// does from the image, and points the install.sh source at a server
// that counts requests, so a test can prove nothing was fetched (#190).
func deploymentRepair(t *testing.T, dir, script string) (requests *atomic.Int32) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "repair.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return countingScriptSource(t)
}

// countingScriptSource points the install.sh override at a server that
// counts every request and serves a script for any path.
func countingScriptSource(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte("#!/usr/bin/env bash\necho 'fetched — never run this'\nexit 0\n"))
	}))
	t.Cleanup(server.Close)
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", server.URL+"/scripts/install.sh")
	return &n
}

func TestDefaultPrepareRepair_RunsTheDeploymentsOwnScriptInTheRequestedMode(t *testing.T) {
	dir := t.TempDir()
	const script = "#!/usr/bin/env bash\necho \"diagnosis result=healthy checked=$# skipped=0\"\necho \"args: $*\" >&2\nexit 0\n"
	requests := deploymentRepair(t, dir, script)

	m := NewRepairModel(dir, "v0.6.0") // no seam: the real default
	ready, ok := m.Init()().(repairReadyMsg)
	if !ok || ready.err != nil {
		t.Fatalf("default prepare: %#v", ready)
	}
	done := awaitEnd(t, ready.stream)
	if done.ExitCode != 0 || !strings.Contains(strings.Join(done.StderrTail, "\n"), "args: --plan") {
		t.Fatalf("the deployment's script did not run in --plan mode: %+v", done)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("Repair made %d HTTP requests; it must fetch nothing", n)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "scripts", "repair.sh")); string(got) != script {
		t.Errorf("the deployment's repair.sh was overwritten: %q", got)
	}
}

func TestDefaultPrepareRepair_AbsentScriptIsUnavailable(t *testing.T) {
	requests := countingScriptSource(t)
	m := sizedRepair(t)
	msg := m.Init()()
	m, _ = repairUpdate(t, m, msg)
	if m.state != repairUnavailable || !strings.Contains(repairScreen(m), "Diagnosis needs a newer Orbit") {
		t.Fatalf("state = %v, screen:\n%s", m.state, repairScreen(m))
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("an absent repair.sh made %d HTTP requests; there is no fallback fetch", n)
	}
	if _, err := os.Lstat(filepath.Join(m.targetDir, "scripts")); !os.IsNotExist(err) {
		t.Error("nothing may be written into the deployment when repair.sh is absent")
	}
}

func TestDefaultPrepareRepair_MissingTargetIsUnavailable(t *testing.T) {
	countingScriptSource(t)
	m := NewRepairModel(filepath.Join(t.TempDir(), "gone"), "v0.6.0")
	ready := m.Init()().(repairReadyMsg)
	if !errors.Is(ready.err, deploy.ErrRepairUnavailable) {
		t.Fatalf("err = %v, want ErrRepairUnavailable", ready.err)
	}
}

func TestDefaultPrepareRotate_RunsTheDangerousModeWithMachinePrompts(t *testing.T) {
	dir := t.TempDir()
	requests := deploymentRepair(t, dir, "#!/usr/bin/env bash\necho \"prompt field=action-word kind=typed-word required=true attempt=1\"\nread -r w\necho \"got $w $* $ORBIT_REPAIR_PROMPTS\" >&2\nexit 0\n")
	m := NewRepairModel(dir, "v0.6.0")
	ready := m.startRotate()().(repairRotateReadyMsg)
	if ready.err != nil {
		t.Fatalf("default rotate prepare: %v", ready.err)
	}
	if _, err := io.WriteString(ready.stdin, "rotate\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	ready.stdin.Close()
	done := awaitEnd(t, ready.stream)
	tail := strings.Join(done.StderrTail, "\n")
	if !strings.Contains(tail, "got rotate") || !strings.Contains(tail, "machine") {
		t.Fatalf("the rotation did not run with piped machine prompts: %q", tail)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("Rotate made %d HTTP requests; it must fetch nothing", n)
	}
}

func TestDefaultPrepareRotate_AbsentScriptIsUnavailable(t *testing.T) {
	requests := countingScriptSource(t)
	if r := NewRepairModel(t.TempDir(), "v").startRotate()().(repairRotateReadyMsg); !errors.Is(r.err, deploy.ErrRepairUnavailable) {
		t.Fatalf("err = %v, want ErrRepairUnavailable", r.err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("an absent repair.sh made %d HTTP requests", n)
	}
}

// A Ctrl-C that lands before the run starts stops it: nothing is
// started.
func TestDefaultPrepareRepair_CancelledBeforeTheStartStartsNothing(t *testing.T) {
	dir := t.TempDir()
	deploymentRepair(t, dir, "#!/usr/bin/env bash\nsleep 60\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, err := defaultPrepareRepair(ctx, dir, deploy.RepairPlan)
	if stream != nil {
		t.Cleanup(stream.Kill)
		t.Fatal("a cancelled preparation still started the repair")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// The same early Ctrl-C stops a credential rotation before it starts.
func TestDefaultPrepareRotate_CancelledBeforeTheStartStartsNothing(t *testing.T) {
	dir := t.TempDir()
	deploymentRepair(t, dir, "#!/usr/bin/env bash\nsleep 60\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, stdin, err := defaultPrepareRotate(ctx, dir)
	if stream != nil {
		t.Cleanup(stream.Kill)
		if stdin != nil {
			stdin.Close()
		}
		t.Fatal("a cancelled preparation still started the rotation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A RepairModel built without NewRepairModel has no context of its own;
// its preparation falls back to one that is never cancelled, so the
// diagnosis still runs instead of failing on a missing context.
func TestRepairModel_BuiltWithoutTheConstructorStillRunsTheDiagnosis(t *testing.T) {
	dir := t.TempDir()
	deploymentRepair(t, dir, "#!/usr/bin/env bash\necho \"diagnosis result=healthy checked=0 skipped=0\"\nexit 0\n")
	m := RepairModel{targetDir: dir, mode: deploy.RepairPlan} // no constructor, no seam
	ready, ok := m.Init()().(repairReadyMsg)
	if !ok || ready.err != nil {
		t.Fatalf("a model built without the constructor could not start its diagnosis: %#v", ready)
	}
	if done := awaitEnd(t, ready.stream); done.ExitCode != 0 {
		t.Fatalf("the diagnosis did not run to the end: %+v", done)
	}
}

// #191: a repair.sh on an untrusted path is refused on the failure
// screen, with the path and the reason, and never run.
func TestDefaultPrepareRepair_UntrustedScriptIsRefused(t *testing.T) {
	dir := t.TempDir()
	deploymentRepair(t, dir, "#!/usr/bin/env bash\ntouch ran\n")
	script := filepath.Join(dir, "scripts", "repair.sh")
	if err := os.Chmod(script, 0o666); err != nil {
		t.Fatal(err)
	}
	m := NewRepairModel(dir, "v0.6.0")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = updated.(RepairModel)
	m, _ = repairUpdate(t, m, m.Init()())
	if m.state != repairError {
		t.Fatalf("state = %v, want the failure screen", m.state)
	}
	var untrusted *deploy.UntrustedPathError
	if !errors.As(m.runErr, &untrusted) || untrusted.Path != script {
		t.Fatalf("runErr = %v, want a refusal naming %s", m.runErr, script)
	}
	if s := repairScreen(m); !strings.Contains(s, "Diagnosis couldn't run") || !strings.Contains(s, "writable by everyone") {
		t.Fatalf("screen:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); !os.IsNotExist(err) {
		t.Fatal("the untrusted repair.sh ran")
	}

	rot := NewRepairModel(dir, "v").startRotate()().(repairRotateReadyMsg)
	if !errors.As(rot.err, &untrusted) {
		t.Fatalf("rotate err = %v, want a refusal", rot.err)
	}
}
