package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// engineRun drives one engine run for the Install and Update flows:
// the mission console over the piped plain-mode event stream first,
// and — on the engine's configuration-required refusal — guided
// configuration, then the outcome.
//
// Configuration stays the engine's domain (the #51 principle,
// orbit-launcher#73): the launcher never invents a field name, a
// validation rule, or a file format. With a contract-era engine
// (orbit#297) the collection happens in-console over the machine
// prompt protocol — configure.sh names each field, validates each
// answer with its own validators, and writes its own files, with the
// launcher relaying answers over stdin and adopting the produced
// configuration into the target (see configcollect.go). A legacy
// engine that doesn't speak the protocol fails fast with no prompt
// line, and the flow falls back to the same tea.ExecProcess terminal
// handoff as before — capability detected by behaviour, never by
// version sniffing. The piped engine run itself cannot prompt by
// construction (deploy.BuildEngineCommand detaches it from the
// controlling terminal, engaging the engine's documented
// non-interactive contract), and success and failure are keyed off
// events plus exit codes, never scraped prose.
type engineRunState int

const (
	runPreparing engineRunState = iota
	runStreaming
	runConfigPrompt
	runConfigCollect
	runHandoffRunning
	runFailed
	runSucceeded
)

// Seam types: the overridable dependencies an engine run needs, so
// flow tests never touch the network or a real terminal.
type (
	prepareEngineFunc  func(ctx context.Context, targetDir, action string) (*engine.Stream, func() error, error)
	prepareInstallFunc func(ctx context.Context, targetDir string) (*exec.Cmd, func() error, error)
	runHandoffFunc     func(cmd *exec.Cmd) tea.Cmd
	detectFunc         func(dir string) (*deploy.Deployment, error)
	nowFunc            func() time.Time
)

// flowOutcome is what a flow's engine run reports upward to AppModel.
type flowOutcome struct {
	Done      bool
	Succeeded bool
	WantsMenu bool
	URL       string
	Elapsed   time.Duration
}

func outcomeOf(r engineRun) flowOutcome {
	return flowOutcome{
		Done:      r.Done,
		Succeeded: r.Succeeded,
		WantsMenu: r.WantsMenu,
		URL:       r.URL,
		Elapsed:   r.Elapsed,
	}
}

// engineReadyMsg carries the started engine stream (or the error that
// prevented it — fetch failure, unwritable temp dir).
type engineReadyMsg struct {
	stream  *engine.Stream
	cleanup func() error
	err     error
}

// engineStreamMsg wraps one message from the engine stream's channel.
type engineStreamMsg struct{ msg any }

type engineRun struct {
	action    string // "install" or "update" — the engine flag
	targetDir string
	title     string
	version   string

	state   engineRunState
	console ConsoleModel

	// width/height are remembered so an in-flow restart (the retry
	// after in-console configuration) rebuilds the console at size.
	width, height int

	stream  *engine.Stream
	cleanup func() error

	// cfg is the in-console configuration session, live while state is
	// runConfigCollect.
	cfg configCollect

	// Outcome evidence, gathered from events and the exit code.
	configRefused bool
	lastFailed    *engine.Event
	stderrTail    []string
	runErr        error

	menuSel int

	// Done/Succeeded/WantsMenu are read by the owning flow model and
	// AppModel. URL and Elapsed feed the success screen.
	Done      bool
	Succeeded bool
	WantsMenu bool
	URL       string
	Elapsed   time.Duration

	// Test seams; nil gets the real implementations.
	prepareEngine  prepareEngineFunc
	prepareInstall prepareInstallFunc
	runHandoff     runHandoffFunc
	detect         detectFunc
	now            nowFunc
	prepareConfig  prepareConfigFunc
	startConfig    startConfigFunc
	adoptConfig    adoptConfigFunc
}

func newEngineRun(action, targetDir, title, version string) engineRun {
	return engineRun{
		action:    action,
		targetDir: targetDir,
		title:     title,
		version:   version,
	}
}

// start begins the run: the console starts its clock now, and the
// engine is fetched and launched in the background.
func (r engineRun) start(width, height int) (engineRun, tea.Cmd) {
	r.state = runPreparing
	r.width, r.height = width, height
	r.console = newConsole(r.title, r.version, r.now)
	r.console = r.console.setSize(width, height)
	prepare := r.prepareEngine
	if prepare == nil {
		prepare = defaultPrepareEngine
	}
	targetDir, action := r.targetDir, r.action
	return r, func() tea.Msg {
		stream, cleanup, err := prepare(context.Background(), targetDir, action)
		return engineReadyMsg{stream: stream, cleanup: cleanup, err: err}
	}
}

// defaultPrepareEngine fetches install.sh and starts it as the
// detached, piped, plain-mode engine run.
func defaultPrepareEngine(ctx context.Context, targetDir, action string) (*engine.Stream, func() error, error) {
	script, err := deploy.FetchInstallScript(ctx)
	if err != nil {
		return nil, nil, err
	}
	cmd, cleanup, err := deploy.BuildEngineCommand(script, targetDir, action)
	if err != nil {
		return nil, nil, err
	}
	stream, err := engine.Start(cmd)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return stream, cleanup, nil
}

// pumpEngine delivers the next stream message into the event loop.
func pumpEngine(s *engine.Stream) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-s.C
		if !ok {
			return nil
		}
		return engineStreamMsg{msg: msg}
	}
}

// update handles every message while an engine run owns the screen.
func (r engineRun) update(msg tea.Msg) (engineRun, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = msg.Width, msg.Height
		r.console = r.console.setSize(msg.Width, msg.Height)
		return r, nil

	case tickMsg:
		r.console = r.console.advance()
		return r, tick()

	case engineReadyMsg:
		if msg.err != nil {
			r.runErr = msg.err
			r.state = runFailed
			return r, nil
		}
		r.stream = msg.stream
		r.cleanup = msg.cleanup
		r.state = runStreaming
		return r, pumpEngine(r.stream)

	case engineStreamMsg:
		return r.handleStream(msg.msg)

	case configPlanMsg, configStepMsg, configStreamMsg, configAdoptedMsg:
		return r.handleConfigMsg(msg)

	case installPreparedMsg:
		if msg.err != nil {
			r.runErr = msg.err
			r.state = runFailed
			return r, nil
		}
		r.cleanup = msg.cleanup
		handoff := r.runHandoff
		if handoff == nil {
			handoff = defaultRunHandoff
		}
		return r, handoff(msg.cmd)

	case installFinishedMsg:
		if r.cleanup != nil {
			r.cleanup()
			r.cleanup = nil
		}
		if msg.err != nil {
			r.runErr = msg.err
			r.state = runFailed
			return r, nil
		}
		return r.succeed()

	case tea.KeyMsg:
		return r.handleKey(msg)
	}
	return r, nil
}

func (r engineRun) handleStream(msg any) (engineRun, tea.Cmd) {
	switch m := msg.(type) {
	case engine.EventMsg:
		r.console = r.console.observeEvent(m.Event)
		if m.Event.NeedsConfiguration() {
			r.configRefused = true
		}
		if m.Event.IsTerminalFailure() {
			failed := m.Event
			r.lastFailed = &failed
		}
		return r, pumpEngine(r.stream)

	case engine.RawLineMsg:
		r.console = r.console.observeRaw(m.Text)
		return r, pumpEngine(r.stream)

	case engine.DoneMsg:
		if r.cleanup != nil {
			r.cleanup()
			r.cleanup = nil
		}
		r.stderrTail = m.StderrTail
		if m.Err == nil {
			return r.succeed()
		}
		r.runErr = m.Err
		if r.configRefused {
			// The engine's documented configuration-required refusal:
			// the target was rolled back, and the fix is the guided
			// configuration in a real terminal.
			r.state = runConfigPrompt
			r.menuSel = 0
			return r, nil
		}
		r.state = runFailed
		r.menuSel = 0
		return r, nil
	}
	return r, nil
}

// succeed resolves the deployment's URL and finishes the run.
func (r engineRun) succeed() (engineRun, tea.Cmd) {
	detect := r.detect
	if detect == nil {
		detect = deploy.Detect
	}
	if d, err := detect(r.targetDir); err == nil && d != nil {
		r.URL = d.AppURL
	}
	r.Elapsed = r.console.elapsed()
	r.state = runSucceeded
	r.Done = true
	r.Succeeded = true
	return r, nil
}

// beginHandoff hands the real terminal to the interactive installer —
// the config collection stretch. Deliberately flagless: install.sh's
// own menus pick the right action for whatever state the target is in,
// on any engine generation.
func (r engineRun) beginHandoff() (engineRun, tea.Cmd) {
	r.state = runHandoffRunning
	prepare := r.prepareInstall
	if prepare == nil {
		prepare = defaultPrepareInstall
	}
	return r, prepareInstallCmd(prepare, r.targetDir)
}

func (r engineRun) handleKey(msg tea.KeyMsg) (engineRun, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		if r.stream != nil {
			r.stream.Kill()
		}
		r.cfg.close()
		return r, tea.Quit
	}

	switch r.state {
	case runConfigCollect:
		return r.handleConfigKey(msg)

	case runConfigPrompt:
		return r.handleMenuKey(msg, len(configPromptMenu), func(sel int) (engineRun, tea.Cmd) {
			switch sel {
			case 0:
				return r.beginConfigCollect()
			case 1:
				r.Done = true
				r.WantsMenu = true
				return r, nil
			default:
				return r, tea.Quit
			}
		})

	case runFailed:
		return r.handleMenuKey(msg, len(failedMenu), func(sel int) (engineRun, tea.Cmd) {
			switch sel {
			case 0:
				return r.beginHandoff()
			case 1:
				r.Done = true
				r.WantsMenu = true
				return r, nil
			default:
				return r, tea.Quit
			}
		})
	}
	return r, nil
}

func (r engineRun) handleMenuKey(msg tea.KeyMsg, items int, choose func(int) (engineRun, tea.Cmd)) (engineRun, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		r.Done = true
		r.WantsMenu = true
		return r, nil
	case tea.KeyUp:
		r.menuSel = (r.menuSel - 1 + items) % items
		return r, nil
	case tea.KeyDown:
		r.menuSel = (r.menuSel + 1) % items
		return r, nil
	case tea.KeyEnter:
		return choose(r.menuSel)
	}
	return r, nil
}

// Both post-run screens offer the same three exits, in the same order:
// forward into the real interactive installer, back to the menu, or
// out. On the failure screen the forward option matters most for a
// legacy engine (orbit main today), which reports no telemetry — its
// non-interactive refusal is indistinguishable from any other exit 1,
// and the honest remedy is the same guided installer.
var configPromptMenu = []string{"Continue — guided configuration", "Menu", "Exit"}
var failedMenu = []string{"Open the guided installer", "Menu", "Exit"}

// view renders whichever screen the run is on.
func (r engineRun) view(width, height int) string {
	if width == 0 {
		return ""
	}
	switch r.state {
	case runPreparing, runStreaming:
		return r.console.view(width, height)
	case runConfigPrompt:
		return r.viewConfigPrompt(width, height)
	case runConfigCollect:
		return r.viewConfigCollect(width, height)
	case runHandoffRunning:
		return lipgloss.NewStyle().Padding(1, 2).Render(style.Tagline.Render("in the installer — you'll return here when it finishes"))
	case runFailed:
		return r.viewFailed(width, height)
	}
	return ""
}

func (r engineRun) viewConfigPrompt(width, height int) string {
	var b strings.Builder
	fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Orbit needs your configuration"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("The engine stopped before touching anything: some settings"))
	fmt.Fprintln(&b, style.MutedText.Render("only you can provide. Continue to fill them in right here —"))
	fmt.Fprintln(&b, style.MutedText.Render("every answer is checked by Orbit's own setup, which takes"))
	fmt.Fprintln(&b, style.MutedText.Render("the terminal itself only if it has to — then the "+r.action+" resumes."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, configPromptMenu, r.menuSel)
	return centreBlock(width, height, b.String())
}

func (r engineRun) viewFailed(width, height int) string {
	var b strings.Builder
	fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render(flowStoppedTitle(r.action)))
	fmt.Fprintln(&b)
	if r.lastFailed != nil {
		word := r.lastFailed.Phase
		if idx := phaseIndex(word); idx >= 0 {
			word = consolePhases[idx].word
		}
		fmt.Fprintln(&b, style.ErrorText.Render(word)+style.Tagline.Render(" — "+r.lastFailed.Reason+" · next: "+r.lastFailed.Action))
	} else if r.runErr != nil {
		fmt.Fprintln(&b, style.Tagline.Render(r.runErr.Error()))
	}
	for _, line := range tailLines(r.stderrTail, 6) {
		fmt.Fprintln(&b, style.Tagline.Render(line))
	}
	fmt.Fprintln(&b)
	writeStackedMenu(&b, failedMenu, r.menuSel)
	return centreBlock(width, height, b.String())
}

// writeStackedMenu writes menu rows in the centred grammar: each label
// centres on the screen axis (via the per-line centring downstream),
// the selected row's caret riding two cells left of its label.
func writeStackedMenu(b *strings.Builder, items []string, selected int) {
	for i, label := range items {
		fmt.Fprintln(b, menuRow(label, i == selected))
	}
}

func flowStoppedTitle(action string) string {
	if action == "update" {
		return "Update stopped"
	}
	return "Installation stopped"
}

func tailLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// centreBlock places a plain content block on an empty screen using
// the same vertical rhythm as the scene screens.
func centreBlock(width, height int, content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	topOffset := int(0.42 * float64(height-1-len(lines)))
	if topOffset < 0 {
		topOffset = 0
	}
	var b strings.Builder
	for i := 0; i < topOffset; i++ {
		b.WriteString("\n")
	}
	for _, line := range lines {
		pad := (width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
