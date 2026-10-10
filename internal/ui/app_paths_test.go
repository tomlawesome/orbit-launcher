package ui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// AppModel's options and routing edges, driven by plain Update calls.

func appUpdate(t *testing.T, m AppModel, msgs ...tea.Msg) (AppModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var updated tea.Model
		updated, cmd = m.Update(msg)
		m = updated.(AppModel)
	}
	return m, cmd
}

// appDrive runs cmd and every command its messages produce, the way
// the program would, expanding batches; it stops when nothing is left.
func appDrive(t *testing.T, m AppModel, cmd tea.Cmd) AppModel {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; len(queue) > 0; i++ {
		if i > 1000 {
			t.Fatal("command chain did not settle")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if msg == nil {
			continue
		}
		var more tea.Cmd
		m, more = appUpdate(t, m, msg)
		queue = append(queue, more)
	}
	return m
}

// appDrainStream feeds what an engine stream reader pushes through
// queue back into the model, as the program would, until the reader
// reports the stream's end.
func appDrainStream(t *testing.T, m AppModel, queue *sink) AppModel {
	t.Helper()
	for {
		select {
		case msg := <-queue.msgs:
			m = appDrive(t, m, func() tea.Msg { return msg })
			if _, ended := msg.(repairStreamEndedMsg); ended {
				return m
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the engine stream never reported its end")
		}
	}
}

// appMessages runs cmd and every command in its batches, without
// feeding the results back, and returns every message produced.
func appMessages(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var msgs []tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

func writeEnv(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAppModel_AltScreenOnlyWhenAsked(t *testing.T) {
	if NewAppModel().View().AltScreen {
		t.Fatal("the alternate screen must be opt-in")
	}
	if !NewAppModel().WithAltScreen().View().AltScreen {
		t.Fatal("WithAltScreen did not take the whole window")
	}
}

func TestAppModel_NoAnimationStartsNoTickChain(t *testing.T) {
	if cmd := NewAppModelNoAnimation().Init(); cmd != nil {
		if _, isTick := cmd().(tickMsg); isTick {
			t.Fatal("a frozen starfield must not start the tick chain")
		}
	}
}

func TestAppModel_ExitOnTheMenuQuits(t *testing.T) {
	m := NewAppModelNoAnimation()
	m.targetDir = t.TempDir()
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = appUpdate(t, m, key(tea.KeyUp)) // Install wraps to Exit
	_, cmd := appUpdate(t, m, key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("Exit on the main menu did not quit")
	}
}

func TestAppModel_WithoutVolumeCheckNeverInterruptsInstall(t *testing.T) {
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = t.TempDir()
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, cmd := appUpdate(t, m, key(tea.KeyEnter)) // Install
	if m.state != appStateInstall {
		t.Fatalf("state = %v, want the install flow", m.state)
	}
	// Run the flow's start commands (size, pre-flight) as the program would.
	m = appDrive(t, m, cmd)
	if m.install.state != installStateProfile || !strings.Contains(stripANSI(m.View().Content), "Choose a deployment profile") {
		t.Fatalf("the install flow was interrupted: state = %v", m.install.state)
	}
}

// Remove and Update both confirm against the deployment's identity, so
// both start the install-date lookup the moment they take the screen.
func TestAppModel_RemoveAndUpdateStartTheInstallDateLookup(t *testing.T) {
	want := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	flows := map[string][]tea.Msg{
		"Remove": {key(tea.KeyUp), key(tea.KeyUp)}, // Install wraps to Exit, then Remove
		"Update": {key(tea.KeyDown)},
	}
	for name, keys := range flows {
		for _, hermetic := range []bool{false, true} {
			dir := t.TempDir()
			writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
			m := NewAppModelNoAnimation()
			m.flowInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return want }
			if hermetic {
				m = m.WithoutVolumeCheck()
			}
			m.targetDir = dir
			m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			m, _ = appUpdate(t, m, keys...)
			_, cmd := appUpdate(t, m, key(tea.KeyEnter))

			var got *installedAtMsg
			for _, msg := range appMessages(t, cmd) {
				if at, ok := msg.(installedAtMsg); ok {
					got = &at
				}
			}
			switch {
			case got == nil:
				t.Errorf("%s (hermetic %v): no install-date lookup started", name, hermetic)
			case hermetic && !got.at.IsZero():
				t.Errorf("%s: WithoutVolumeCheck still asked Docker, got %v", name, got.at)
			case !hermetic && !got.at.Equal(want):
				t.Errorf("%s: install date = %v, want %v", name, got.at, want)
			}
		}
	}
}

func TestAppModel_WithSenderIsTheFlowsWayIntoTheEventLoop(t *testing.T) {
	got := make(chan tea.Msg, 1)
	m := NewAppModelNoAnimation().WithSender(func(msg tea.Msg) { got <- msg }).WithoutVolumeCheck()
	m.targetDir = t.TempDir()
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24}, key(tea.KeyEnter))
	if m.install.send == nil {
		t.Fatal("the install flow was given no sender")
	}
	m.install.send(engineStreamEndedMsg{})
	select {
	case msg := <-got:
		if _, ok := msg.(engineStreamEndedMsg); !ok {
			t.Fatalf("sender delivered %#v", msg)
		}
	default:
		t.Fatal("the install flow's sender is not the one the app was given")
	}
}

func TestAppModel_DeploymentWithoutAProperURLStillShowsSomething(t *testing.T) {
	cases := map[string]string{
		"APP_URL=orbit.lan\n": "orbit.lan",           // no scheme: shown as written
		"ORBIT_IMAGE=x\n":     "deployment detected", // no URL at all
	}
	for env, want := range cases {
		dir := t.TempDir()
		writeEnv(t, dir, env)
		m := NewAppModelNoAnimation()
		m.targetDir = dir
		m = m.WithDeploymentStatus(nil)
		if m.splash.fqdn != want {
			t.Errorf("%q: identity = %q, want %q", env, m.splash.fqdn, want)
		}
	}
}

func TestAppModel_NoTargetDirDetectsInTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "APP_URL=https://cwd.example.test\n")
	t.Chdir(dir)
	m := NewAppModelNoAnimation().WithDeploymentStatus(nil)
	if m.splash.fqdn != "cwd.example.test" {
		t.Fatalf("identity = %q, want the working directory's deployment", m.splash.fqdn)
	}
}

func TestAppModel_ReturnToMenuReprobesAndRechecksAndStaysFrozen(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
	probes, checks := 0, 0
	m := NewAppModelNoAnimation().
		WithUpdateCheck(func(context.Context) (string, bool, error) { checks++; return "v9.9.10", true, nil })
	m.targetDir = dir
	m = m.WithDeploymentStatus(func(context.Context, string) bool { probes++; return true })
	m.flowSeams = engineRunSeams{prepareRepair: fakeRepairStream(`echo 'diagnosis result=healthy checked=1 skipped=0'; exit 0`)}
	queue := newSink()
	m = m.WithSender(queue.Send)
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Repair, then its Menu row.
	m, _ = appUpdate(t, m, key(tea.KeyDown)) // Update is preselected; Repair is next
	m, cmd := appUpdate(t, m, key(tea.KeyEnter))
	m = appDrive(t, m, cmd)
	m = appDrainStream(t, m, queue)
	if !strings.Contains(stripANSI(m.View().Content), "Diagnosis clear") {
		t.Fatalf("repair did not finish:\n%s", stripANSI(m.View().Content))
	}
	m, cmd = appUpdate(t, m, key(tea.KeyEnter))
	if m.state != appStateSplash {
		t.Fatalf("state = %v, want the menu", m.state)
	}
	if !m.splash.noAnimation {
		t.Fatal("a frozen launcher came back to an animated menu")
	}
	m = appDrive(t, m, cmd)
	if probes != 1 || checks != 1 {
		t.Fatalf("return to menu ran %d probes and %d update checks, want one of each", probes, checks)
	}
	s := stripANSI(m.View().Content)
	if !strings.Contains(s, "mail.example.test") || !strings.Contains(s, "v9.9.10") {
		t.Fatalf("the refreshed menu should greet the deployment and the update:\n%s", s)
	}
}

func TestAppModel_UnknownStateDrawsNothingAndIgnoresInput(t *testing.T) {
	m := NewAppModel()
	m.state = appState(99)
	m, cmd := appUpdate(t, m, key(tea.KeyEnter))
	if cmd != nil || m.View().Content != "" {
		t.Fatal("an unknown state acted or drew")
	}
}

// pingModel quits as soon as it receives a ping, reporting it.
type pingModel struct{ got chan<- string }

type pingMsg string

func (p pingModel) Init() tea.Cmd { return nil }
func (p pingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s, ok := msg.(pingMsg); ok {
		p.got <- string(s)
		return p, tea.Quit
	}
	return p, nil
}
func (p pingModel) View() tea.View { return tea.NewView("") }

func TestProgramSender_DeliversOnlyOnceAttached(t *testing.T) {
	s := NewProgramSender()
	s.Send(pingMsg("too early")) // nothing attached: dropped, not a panic

	got := make(chan string, 2)
	p := tea.NewProgram(pingModel{got: got}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	s.Attach(p)
	s.Send(pingMsg("hello"))

	select {
	case msg := <-got:
		if msg != "hello" {
			t.Fatalf("program received %q, want hello (the early send must have been dropped)", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the attached program never received the message")
	}
	if err := <-done; err != nil {
		t.Fatalf("program: %v", err)
	}
}
