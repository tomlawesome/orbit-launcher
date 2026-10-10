package ui

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// #206 (#199 WI-2, UI-5): one goroutine reader in internal/ui pushes
// every engine message plus an end-of-stream message, and no flow pulls
// one message at a time. The #159 fix reached the install/update run
// only; Repair and in-console configuration kept a re-armed one-shot
// chain that goes quiet when the stream closes before its DoneMsg.
//
// These tests drive each flow the way the program does — commands run
// concurrently, pushed messages arrive through the sender — so they
// hold whatever shape the single reader takes.

// stoppedStream replays msgs and then closes without a DoneMsg: an
// engine that stopped without reporting how its run finished.
func stoppedStream(msgs ...any) *engine.Stream {
	ch := make(chan any, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	return &engine.Stream{C: ch}
}

// finishedStream replays msgs, the last of which is the DoneMsg, and
// closes behind it the way a real engine stream does.
func finishedStream(msgs ...any) *engine.Stream { return stoppedStream(msgs...) }

// flowLoop is a miniature event loop: every command runs on its own
// goroutine and its result, like anything the flow pushes through its
// sender, lands in one queue that update drains on the test goroutine.
// A command that blocks (an old-style pull on an open stream) blocks
// only itself, as it would under bubbletea.
type flowLoop struct {
	t      *testing.T
	queue  *sink
	update func(tea.Msg) tea.Cmd
}

func newFlowLoop(t *testing.T, queue *sink, update func(tea.Msg) tea.Cmd) *flowLoop {
	return &flowLoop{t: t, queue: queue, update: update}
}

func (l *flowLoop) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				l.run(c)
			}
			return
		}
		if msg != nil {
			l.queue.Send(msg)
		}
	}()
}

// settle runs cmd and everything it leads to until the queue has been
// quiet for a moment (or a few seconds have passed in all), so late
// messages have had their chance to land before the test looks.
func (l *flowLoop) settle(cmd tea.Cmd) {
	l.t.Helper()
	l.run(cmd)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-l.queue.msgs:
			l.run(l.update(msg))
		case <-time.After(300 * time.Millisecond):
			return
		case <-deadline:
			return
		}
	}
}

// stoppedWithoutReporting is the #159 wording for this exact condition,
// already shown by the install/update run; one reader, one reason.
const stoppedWithoutReporting = "stopped without reporting"

// --- Repair -------------------------------------------------------------

// repairViaApp opens Repair from the main menu of an AppModel wired the
// way main.go wires it — a sender into the event loop — with prepare as
// the repair seam. It returns once the diagnosis has been asked for.
func repairViaApp(t *testing.T, prepare prepareRepairFunc) (*AppModel, *flowLoop, tea.Cmd) {
	t.Helper()
	queue := newSink()
	m := NewAppModelNoAnimation().WithSender(queue.Send).WithoutVolumeCheck()
	m.targetDir = t.TempDir()
	m.flowSeams = engineRunSeams{prepareRepair: prepare}
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 30}, key(tea.KeyDown), key(tea.KeyDown)) // Install, Update, Repair
	m, cmd := appUpdate(t, m, key(tea.KeyEnter))
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 30}) // a flow draws nothing until sized
	if s := stripANSI(m.View().Content); !strings.Contains(s, "reading the deployment") {
		t.Fatalf("Repair did not open:\n%s", s)
	}
	app := &m
	loop := newFlowLoop(t, queue, func(msg tea.Msg) tea.Cmd {
		var next tea.Cmd
		*app, next = appUpdate(t, *app, msg)
		return next
	})
	return app, loop, cmd
}

// A diagnosis whose stream closes before its DoneMsg must not leave
// Repair on "reading the deployment" for ever: the operator gets the
// error screen, with the reason and a way out.
func TestRepair_DiagnosisStreamClosingEarlyFailsVisibly(t *testing.T) {
	app, loop, cmd := repairViaApp(t, func(context.Context, string, deploy.RepairMode) (*engine.Stream, error) {
		return stoppedStream(engine.RawLineMsg{Text: "finding class=secret-missing target=session-secret severity=warn"}), nil
	})
	loop.settle(cmd)

	s := stripANSI(app.View().Content)
	if strings.Contains(s, "reading the deployment") {
		t.Fatalf("the diagnosis stopped and Repair is still waiting on it — silence, not a failure:\n%s", s)
	}
	for _, want := range []string{"Diagnosis couldn't run", stoppedWithoutReporting, "Menu", "Exit"} {
		if !strings.Contains(s, want) {
			t.Errorf("the failure screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Diagnosis clear") || strings.Contains(s, "Needs attention") {
		t.Errorf("a run that never finished rendered a verdict:\n%s", s)
	}
}

// The same for a mutation: safe repairs whose stream stops before its
// DoneMsg are not "applied", and the screen says the engine stopped.
func TestRepair_ExecutionStreamClosingEarlyFailsVisibly(t *testing.T) {
	app, loop, cmd := repairViaApp(t, func(_ context.Context, _ string, mode deploy.RepairMode) (*engine.Stream, error) {
		if mode == deploy.RepairExecuteSafe {
			return stoppedStream(engine.RawLineMsg{Text: "execute action=fix-permissions resolves=managed-file-permissions result=done"}), nil
		}
		return engine.Start(exec.Command("bash", "-c", safePlanStream))
	})
	loop.settle(cmd)
	if s := stripANSI(app.View().Content); !strings.Contains(s, "Run the safe repairs") {
		t.Fatalf("the plan screen never arrived:\n%s", s)
	}

	loop.settle(loop.update(key(tea.KeyEnter))) // "Run the safe repairs" is preselected

	s := stripANSI(app.View().Content)
	if strings.Contains(s, "running the safe repairs") {
		t.Fatalf("the repair stopped and the flow is still waiting on it — silence, not a failure:\n%s", s)
	}
	if strings.Contains(s, "Repairs applied") {
		t.Fatalf("a repair run that never reported its outcome reads as applied:\n%s", s)
	}
	for _, want := range []string{stoppedWithoutReporting, "Menu"} {
		if !strings.Contains(s, want) {
			t.Errorf("the failure screen lacks %q:\n%s", want, s)
		}
	}
}

// Late messages from a superseded run are dropped. A --plan the script
// rejects (exit 2) is rerun as --check; the rejected run's stream closes
// behind its DoneMsg, and that ending lands while --check is already
// reading. It belongs to the old run and must not fail the new one.
func TestRepair_TheSupersededRunsStreamEndingDoesNotFailTheNewRun(t *testing.T) {
	app, loop, cmd := repairViaApp(t, func(_ context.Context, _ string, mode deploy.RepairMode) (*engine.Stream, error) {
		if mode == deploy.RepairPlan {
			return finishedStream(engine.DoneMsg{ExitCode: 2, Err: errors.New("exit status 2"), StderrTail: []string{"Usage: repair.sh --check"}}), nil
		}
		return finishedStream(
			engine.RawLineMsg{Text: "diagnosis result=healthy checked=13 skipped=0"},
			engine.DoneMsg{ExitCode: 0},
		), nil
	})
	loop.settle(cmd)

	s := stripANSI(app.View().Content)
	if !strings.Contains(s, "Diagnosis clear") {
		t.Fatalf("the --check rerun's verdict did not stand:\n%s", s)
	}
	for _, unwanted := range []string{"Diagnosis couldn't run", stoppedWithoutReporting} {
		if strings.Contains(s, unwanted) {
			t.Fatalf("the rejected --plan run's stream ending failed the --check run that replaced it (%q):\n%s", unwanted, s)
		}
	}
}

// --- In-console configuration ---------------------------------------------

// configViaInstall takes an Install flow, built with its sender the way
// AppModel builds it, through an engine refusal for configuration and
// into the guided session's sign-in-mode screen, then picks local
// sign-in so --init starts on start's stream.
func configViaInstall(t *testing.T, start startConfigFunc) (*InstallModel, *flowLoop) {
	t.Helper()
	m, queue := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngine(nil, configRefusalStream()...),
		prepareConfig: planned(true, false),
		startConfig:   start,
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("true"), func() error { return nil }, nil
		},
		runHandoff: fakeHandoff(nil),
		detect:     fakeDetect("https://orbit.example.test"),
	})
	m, cmd := startInstallRun(t, m)
	install := &m
	loop := newFlowLoop(t, queue, func(msg tea.Msg) tea.Cmd {
		updated, next := install.Update(msg)
		*install = updated.(InstallModel)
		return next
	})
	loop.settle(cmd)
	if s := stripANSI(install.View().Content); !strings.Contains(s, "Continue — guided configuration") {
		t.Fatalf("the engine's configuration refusal never arrived:\n%s", s)
	}
	loop.settle(loop.update(key(tea.KeyEnter))) // Continue — guided configuration
	if s := stripANSI(install.View().Content); !strings.Contains(s, "How will people sign in?") {
		t.Fatalf("the guided session did not reach the sign-in question:\n%s", s)
	}
	return install, loop
}

// A configuration step whose stream closes before its DoneMsg must not
// leave the operator on the prompt screen for ever.
func TestConfig_StreamClosingEarlyFailsVisibly(t *testing.T) {
	t.Setenv(requireInConsoleEnv, "")
	install, loop := configViaInstall(t, func(string, deploy.ConfigStep, deploy.AuthMode) (*engine.Stream, io.WriteCloser, error) {
		return stoppedStream(engine.RawLineMsg{Text: "prompt field=APP_URL kind=url required=true attempt=1"}),
			nopWriteCloser{&bytes.Buffer{}}, nil
	})
	loop.settle(loop.update(key(tea.KeyEnter))) // Local accounts: --init starts

	s := stripANSI(install.View().Content)
	if strings.Contains(s, "Public Orbit origin") || strings.Contains(s, "talking to the engine") {
		t.Fatalf("the configuration step stopped and the session is still waiting on it — silence, not a failure:\n%s", s)
	}
	for _, want := range []string{"Installation stopped", stoppedWithoutReporting} {
		if !strings.Contains(s, want) {
			t.Errorf("the failure screen lacks %q:\n%s", want, s)
		}
	}
}

// The configuration session's state guard keeps working: once Esc has
// cancelled the session, its stream's DoneMsg and its ending arrive
// late and belong to nothing. The refusal menu stays put.
func TestConfig_ACancelledSessionsLateStreamEndingIsDropped(t *testing.T) {
	var started *engine.Stream
	install, loop := configViaInstall(t, func(string, deploy.ConfigStep, deploy.AuthMode) (*engine.Stream, io.WriteCloser, error) {
		s, stdin, err := engine.StartInteractive(exec.Command("bash", "-c",
			`echo "prompt field=APP_URL kind=url required=true attempt=1"; sleep 30`))
		if err == nil {
			started = s
			t.Cleanup(s.Kill)
		}
		return s, stdin, err
	})
	loop.settle(loop.update(key(tea.KeyEnter))) // Local accounts: --init starts
	if started == nil {
		t.Fatal("--init never started")
	}
	if s := stripANSI(install.View().Content); !strings.Contains(s, "Public Orbit origin") {
		t.Fatalf("the session's first prompt never arrived:\n%s", s)
	}

	loop.settle(loop.update(key(tea.KeyEsc)))
	started.Kill() // whatever Esc did to the engine, its stream now ends
	loop.settle(nil)

	s := stripANSI(install.View().Content)
	if !strings.Contains(s, "Continue — guided configuration") {
		t.Fatalf("Esc should leave the operator on the refusal menu:\n%s", s)
	}
	for _, unwanted := range []string{"Installation stopped", stoppedWithoutReporting} {
		if strings.Contains(s, unwanted) {
			t.Fatalf("the cancelled session's late stream ending changed the screen (%q):\n%s", unwanted, s)
		}
	}
}

// --- One reader -----------------------------------------------------------

// TestEngineStream_NoFlowKeepsItsOwnPump: the per-flow one-shot pumps
// are gone, and every receive from an engine stream's channel in the
// package lives in one function — the shared reader.
func TestEngineStream_NoFlowKeepsItsOwnPump(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	readers := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			switch fn.Name.Name {
			case "pumpRepair", "pumpConfig":
				t.Errorf("%s: %s still exists; Repair and configuration read through the one engine stream reader", fset.Position(fn.Pos()), fn.Name.Name)
			}
			if fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				var ch ast.Expr
				switch x := n.(type) {
				case *ast.UnaryExpr:
					if x.Op == token.ARROW {
						ch = x.X
					}
				case *ast.RangeStmt:
					ch = x.X
				}
				if sel, ok := ch.(*ast.SelectorExpr); ok && sel.Sel.Name == "C" {
					readers[fn.Name.Name] = true
					t.Logf("%s: %s receives from an engine stream", fset.Position(n.Pos()), fn.Name.Name)
				}
				return true
			})
		}
	}
	if len(readers) != 1 {
		names := make([]string, 0, len(readers))
		for name := range readers {
			names = append(names, name)
		}
		t.Errorf("engine streams are read in %d functions %v, want exactly one shared reader", len(readers), names)
	}
}
