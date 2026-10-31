package ui

// #316: the running check answers within five seconds or not at all, so
// a flow may be open before it does. The app hands the late answer to the
// flow, which turns into the refusal where nothing has started yet and
// ignores it everywhere else. Written from the design note (issue note
// 46624, section 3, "When the result arrives") before the code.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// lateDir is the folder the directly-driven flows act in.
const lateDir = "/opt/orbit"

// lateFolderMsg is the answer that the container was started from
// another folder, as checked for dir.
func lateFolderMsg(dir string) runningResultMsg {
	return runningResultMsg{dir: dir, mismatch: folderMismatch(otherProduction)}
}

// silentApp is the app on its splash whose check has answered that
// nothing is wrong, so flows open as they always did. prepared counts
// the diagnoses Repair starts.
func silentApp(t *testing.T) (m AppModel, dir string, prepared *atomic.Int32) {
	t.Helper()
	dir = installedFolder(t)
	prepared = new(atomic.Int32)
	counting := func(m AppModel) AppModel {
		m.flowSeams.prepareRepair = func(context.Context, string, deploy.RepairMode) (*engine.Stream, error) {
			prepared.Add(1)
			return sleepingStream(t), nil
		}
		return m
	}
	m = started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil), counting))
	if s := screen(m); !strings.Contains(s, "O R B I T") || caretLabel(s) != "Update" {
		t.Fatalf("setup: not on the menu with the caret on Update:\n%s", s)
	}
	return m, dir, prepared
}

func lateUpdateModel(t *testing.T) UpdateModel {
	t.Helper()
	d := &deploy.Deployment{TargetDir: lateDir, AppURL: "https://mail.example.com", Image: recordedImage}
	m, _ := updateAs(t, NewUpdateModel(d, lateDir, "v0.4.4"), tea.WindowSizeMsg{Width: 80, Height: 26})
	return m
}

func lateRemoveModel() RemoveModel {
	m := newTestRemoveModel(standDownOK)
	m.targetDir = lateDir
	return m
}

// unchanged checks that the model still shows what it showed and has not
// left its state.
func unchanged(t *testing.T, what string, before string, now tea.Model) {
	t.Helper()
	if s := screen(now); s != before {
		t.Errorf("%s changed the screen.\nbefore:\n%s\nafter:\n%s", what, before, s)
	}
	if strings.Contains(screen(now), "This Orbit was started from") {
		t.Errorf("%s turned the flow into the refusal:\n%s", what, screen(now))
	}
}

// Update on its confirmation, when the answer arrives, becomes the
// refusal; Menu then leaves it.
func TestOtherFolderLate_UpdateConfirmBecomesTheRefusal(t *testing.T) {
	m, dir, _ := silentApp(t)
	m = openFlow(t, m, "Update")
	if s := screen(m); !strings.Contains(s, "Update Orbit") {
		t.Fatalf("setup: Update is not on its confirmation:\n%s", s)
	}

	m, _ = updateAs(t, m, lateFolderMsg(dir))

	if m.state != appStateUpdate {
		t.Fatalf("state = %v, want it to stay on the update flow's refusal", m.state)
	}
	assertRefusal(t, m, otherProduction, refuseUpdate, "Pull the latest Orbit")

	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Errorf("Menu on the refusal left the app in state %v, want the menu:\n%s", m.state, screen(m))
	}
}

// Once the run has started the answer is ignored: the installer's own
// refusal is the net.
func TestOtherFolderLate_UpdateRunningIgnoresIt(t *testing.T) {
	m, _ := updateAs(t, lateUpdateModel(t), key(tea.KeyEnter)) // Update Orbit
	if m.state != updateStateRunning {
		t.Fatalf("setup: state = %v, want the run started", m.state)
	}
	before := screen(m)

	m, _ = updateAs(t, m, lateFolderMsg(lateDir))

	if m.state != updateStateRunning {
		t.Errorf("state = %v after the answer, want the run left running", m.state)
	}
	unchanged(t, "the late answer", before, m)
}

func TestOtherFolderLate_RemoveConfirmBecomesTheRefusal(t *testing.T) {
	m, dir, _ := silentApp(t)
	m = openFlow(t, m, "Remove")
	if s := screen(m); !strings.Contains(s, "Stand down Orbit") {
		t.Fatalf("setup: Remove is not on its confirmation:\n%s", s)
	}

	m, _ = updateAs(t, m, lateFolderMsg(dir))

	if m.state != appStateRemove {
		t.Fatalf("state = %v, want it to stay on the remove flow's refusal", m.state)
	}
	assertRefusal(t, m, otherProduction, refuseRemove, "Stand down Orbit")

	m, cmd := updateAs(t, m, key(tea.KeyEsc))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Errorf("Esc on the refusal left the app in state %v, want the menu:\n%s", m.state, screen(m))
	}
}

// A stand-down in progress is never interrupted (#227).
func TestOtherFolderLate_RemoveStandingDownIgnoresIt(t *testing.T) {
	m, run := updateAs(t, lateRemoveModel(), key(tea.KeyEnter)) // Stand down Orbit is preselected
	if m.state != removeStateStandingDown || run == nil {
		t.Fatalf("setup: the stand-down did not start (state %v)", m.state)
	}
	before := screen(m)

	m, cmd := updateAs(t, m, lateFolderMsg(lateDir))

	if m.state != removeStateStandingDown {
		t.Errorf("state = %v after the answer, want the stand-down left running", m.state)
	}
	if cmd != nil {
		t.Errorf("the answer produced a command during the stand-down")
	}
	unchanged(t, "the late answer", before, m)
	if o := m.Outcome(); o.Done || o.WantsMenu {
		t.Errorf("the answer ended the flow: %+v", o)
	}
}

// Repair while preparing or on the diagnosis screen is read-only by
// contract: the answer stops it as Esc would, and shows the refusal.
func TestOtherFolderLate_RepairDiagnosisIsStoppedAndBecomesTheRefusal(t *testing.T) {
	t.Run("preparing, the run started", func(t *testing.T) {
		m := sizedRepair(t)
		s := sleepingStream(t)
		m, _ = updateAs(t, m, repairReadyMsg{stream: s})

		m, _ = updateAs(t, m, lateFolderMsg(m.targetDir))

		if d := awaitEnd(t, s); d.ExitCode == 0 {
			t.Errorf("the diagnosis ended cleanly (%+v); the answer should have killed it", d)
		}
		assertRefusal(t, m, otherProduction, refuseRepair, "reading the deployment")
	})
	t.Run("preparing, the start not yet reported", func(t *testing.T) {
		m := sizedRepair(t)

		m, _ = updateAs(t, m, lateFolderMsg(m.targetDir))

		if m.ctx.Err() == nil {
			t.Error("the start was not cancelled, so the process it launches would outlive the screen")
		}
		assertRefusal(t, m, otherProduction, refuseRepair, "reading the deployment")
	})
	t.Run("on the diagnosis screen", func(t *testing.T) {
		m := repairLines(t, sizedRepair(t), "diagnosis result=healthy checked=15 skipped=0")
		m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
		if m.state != repairDiagnosis || !strings.Contains(screen(m), "Diagnosis clear") {
			t.Fatalf("setup: not on the diagnosis screen (state %v):\n%s", m.state, screen(m))
		}

		m, _ = updateAs(t, m, lateFolderMsg(m.targetDir))

		assertRefusal(t, m, otherProduction, refuseRepair, "Diagnosis clear")
	})
	t.Run("the refusal is left with Menu", func(t *testing.T) {
		m := sizedRepair(t)
		m, _ = updateAs(t, m, lateFolderMsg(m.targetDir))

		m, _ = updateAs(t, m, key(tea.KeyEnter))

		if o := m.Outcome(); !o.Done || !o.WantsMenu {
			t.Errorf("Menu on the refusal: outcome %+v, want Done and WantsMenu", o)
		}
	})
}

// Once Repair is changing the deployment (or has), the person reads the
// result that is on screen: nothing replaces it, nothing is interrupted.
func TestOtherFolderLate_RepairExecutingIgnoresIt(t *testing.T) {
	cases := []struct {
		name  string
		model func(t *testing.T) RepairModel
		alive bool // a stream is running that must be left alone
	}{
		{"executing", func(t *testing.T) RepairModel { return stoppedMutation(t, repairExecuting) }, true},
		{"rotating", func(t *testing.T) RepairModel {
			m := sizedRepair(t)
			m.state = repairRotating
			m, _ = updateAs(t, m, repairRotateReadyMsg{stream: sleepingStream(t), stdin: &recordingStdin{}})
			return m
		}, true},
		{"executed", func(t *testing.T) RepairModel {
			m := sizedRepair(t)
			m.state = repairExecuting
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
			if m.state != repairExecuted {
				t.Fatalf("setup: state = %v, want repairExecuted", m.state)
			}
			return m
		}, false},
		{"stopped", func(t *testing.T) RepairModel {
			m := stoppedMutation(t, repairExecuting)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4})
			if m.state != repairError {
				t.Fatalf("setup: state = %v, want repairError", m.state)
			}
			return m
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := c.model(t)
			before, state := screen(m), m.state

			m, _ = updateAs(t, m, lateFolderMsg(m.targetDir))

			if m.state != state {
				t.Errorf("state = %v after the answer, want %v", m.state, state)
			}
			unchanged(t, "the late answer", before, m)
			if o := m.Outcome(); o.Done || o.WantsMenu {
				t.Errorf("the answer ended the flow: %+v", o)
			}
			if c.alive {
				select {
				case msg := <-m.stream.C:
					t.Errorf("the running step was interrupted: %#v", msg)
				case <-time.After(100 * time.Millisecond):
				}
			}
		})
	}
}

// An answer that is not the other-folder one changes no flow: a different
// build is for the flows' installer to refuse, and nothing is nothing.
func TestOtherFolderLate_BuildMismatchChangesNoFlow(t *testing.T) {
	answers := map[string]deploy.Mismatch{
		"a different build": buildMismatch("v0.3.0", "v1.2.0"),
		"nothing":           {},
	}
	for name, mismatch := range answers {
		msg := runningResultMsg{dir: lateDir, mismatch: mismatch}
		t.Run(name+" / update", func(t *testing.T) {
			m := lateUpdateModel(t)
			before := screen(m)
			m, _ = updateAs(t, m, msg)
			unchanged(t, name, before, m)
		})
		t.Run(name+" / remove", func(t *testing.T) {
			m := lateRemoveModel()
			before := screen(m)
			m, _ = updateAs(t, m, msg)
			unchanged(t, name, before, m)
		})
		t.Run(name+" / repair", func(t *testing.T) {
			m := sizedRepair(t)
			s := sleepingStream(t)
			m, _ = updateAs(t, m, repairReadyMsg{stream: s})
			before := screen(m)

			m, _ = updateAs(t, m, runningResultMsg{dir: m.targetDir, mismatch: mismatch})

			unchanged(t, name, before, m)
			select {
			case msg := <-s.C:
				t.Errorf("the diagnosis was interrupted: %#v", msg)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

// The answer carries the folder it was checked for. A flow acting in
// another folder is not the one it is about.
func TestOtherFolderLate_ResultForAnotherFolderIsDropped(t *testing.T) {
	const elsewhere = "/opt/elsewhere"

	t.Run("update", func(t *testing.T) {
		m := lateUpdateModel(t)
		before := screen(m)
		m, _ = updateAs(t, m, lateFolderMsg(elsewhere))
		unchanged(t, "an answer for another folder", before, m)
	})
	t.Run("remove", func(t *testing.T) {
		m := lateRemoveModel()
		before := screen(m)
		m, _ = updateAs(t, m, lateFolderMsg(elsewhere))
		unchanged(t, "an answer for another folder", before, m)
	})
	t.Run("repair", func(t *testing.T) {
		m := sizedRepair(t)
		s := sleepingStream(t)
		m, _ = updateAs(t, m, repairReadyMsg{stream: s})
		before := screen(m)

		m, _ = updateAs(t, m, lateFolderMsg(elsewhere))

		unchanged(t, "an answer for another folder", before, m)
		select {
		case msg := <-s.C:
			t.Errorf("the diagnosis was interrupted by an answer for another folder: %#v", msg)
		case <-time.After(100 * time.Millisecond):
		}
	})
}

// With the check off there is no refusal anywhere, whatever the reader
// would have said: the flows open as they did before the check existed.
func TestApp_WithoutRunningCheckOpensFlowsAsToday(t *testing.T) {
	dir := installedFolder(t)
	var prepared atomic.Int32
	counting := func(m AppModel) AppModel {
		m.flowSeams.prepareRepair = func(context.Context, string, deploy.RepairMode) (*engine.Stream, error) {
			prepared.Add(1)
			return sleepingStream(t), nil
		}
		return m
	}
	off := func(m AppModel) AppModel { return m.WithoutRunningCheck() }
	app := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		runningAs(deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction}, nil), counting, off))
	if s := screen(app); strings.Contains(s, otherFolderRow) || caretLabel(s) != "Update" {
		t.Fatalf("setup: the check is not off:\n%s", s)
	}

	for _, c := range []struct{ label, shows string }{
		{"Update", "Update Orbit"},
		{"Remove", "Stand down Orbit"},
	} {
		m := openFlow(t, app, c.label)
		s := screen(m)
		if strings.Contains(s, "This Orbit was started from") || !strings.Contains(s, c.shows) {
			t.Errorf("%s did not open on its confirmation (%q):\n%s", c.label, c.shows, s)
		}
	}

	m := openFlow(t, app, "Repair")
	if n := prepared.Load(); n != 1 {
		t.Errorf("Repair started %d diagnoses, want 1", n)
	}
	if s := screen(m); strings.Contains(s, "This Orbit was started from") {
		t.Errorf("Repair opened on a refusal:\n%s", s)
	}
}

// The app hands the late answer to a Repair that is open, as it does to
// Update and Remove: while the diagnosis is being prepared and on its
// diagnosis screen, Repair becomes the refusal.
func TestOtherFolderLate_RepairForwardedByTheAppBecomesTheRefusal(t *testing.T) {
	openRepair := func(t *testing.T, opts ...appOpt) (AppModel, string) {
		t.Helper()
		dir := installedFolder(t)
		opts = append([]appOpt{runningAs(deploy.RunningApp{}, nil)}, opts...)
		m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, opts...))
		m = moveCaretTo(t, m, "Repair")
		m, _ = updateAs(t, m, key(tea.KeyEnter))
		if m.state != appStateRepair {
			t.Fatalf("setup: state = %v, want the repair flow", m.state)
		}
		return sized(t, m, 80, 26), dir
	}

	t.Run("preparing", func(t *testing.T) {
		// The flow's start command is not run, so the diagnosis is still
		// being prepared when the answer arrives.
		m, dir := openRepair(t)
		if s := screen(m); !strings.Contains(s, "reading the deployment") {
			t.Fatalf("setup: Repair is not preparing:\n%s", s)
		}

		m, _ = updateAs(t, m, lateFolderMsg(dir))

		if m.state != appStateRepair {
			t.Fatalf("state = %v, want it to stay on the repair flow's refusal", m.state)
		}
		assertRefusal(t, m, otherProduction, refuseRepair, "reading the deployment")
	})
	t.Run("diagnosis", func(t *testing.T) {
		queue := newSink()
		m, dir := openRepair(t,
			func(m AppModel) AppModel {
				m.flowSeams.prepareRepair = fakeRepairStream(`echo 'diagnosis result=healthy checked=1 skipped=0'; exit 0`)
				return m.WithSender(queue.Send)
			})
		// Run the flow's own start and take what its stream pushes.
		m = drive(t, m, m.repair.Init())
		m = appDrainStream(t, m, queue)
		if s := screen(m); !strings.Contains(s, "Diagnosis clear") {
			t.Fatalf("setup: Repair is not on its diagnosis:\n%s", s)
		}

		m, _ = updateAs(t, m, lateFolderMsg(dir))

		assertRefusal(t, m, otherProduction, refuseRepair, "Diagnosis clear")
	})
}

// A diagnosis that was being started when the answer stopped it may still
// report back with a run. That run is read-only and nobody is watching it:
// it is killed, as the one that arrives after Ctrl-C is, and the refusal
// stays.
func TestOtherFolderLate_RepairStreamArrivingAfterTheRefusalIsKilled(t *testing.T) {
	m := sizedRepair(t)
	m, _ = updateAs(t, m, lateFolderMsg(m.targetDir))
	assertRefusal(t, m, otherProduction, refuseRepair, "reading the deployment")
	s := sleepingStream(t)

	m, _ = updateAs(t, m, repairReadyMsg{stream: s})

	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Errorf("the diagnosis that arrived after the refusal was left going (%+v)", d)
	}
	assertRefusal(t, m, otherProduction, refuseRepair, "reading the deployment")
}
