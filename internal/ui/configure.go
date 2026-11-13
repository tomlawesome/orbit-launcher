package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type configureState int

const (
	// configureStateNotFound: nothing here, or a .env-orbit that could not
	// be read (detectErr says which).
	configureStateNotFound configureState = iota
	// configureStatePrepared: .env-orbit but no Compose file; Install is the way in.
	configureStatePrepared
	// configureStateUnsupported: a profile the launcher cannot change.
	configureStateUnsupported
	configureStateProfile
	configureStateConfirm
	// configureStateUnavailable: Agent or Full, Install's own dead end.
	configureStateUnavailable
	configureStateRunning
	// configureStateUnchanged: the run succeeded but the profile did not
	// change (an Orbit release that predates profile changes on update).
	configureStateUnchanged
)

// ConfigureModel is the Configure flow (#286): change an installed
// deployment's profile. It draws the same profile table as Install, asks
// for confirmation, then runs the engine as an update carrying the
// requested profile (deploy.WithProfile) in the mission console, and
// finally checks .env-orbit to see that the profile really changed.
type ConfigureModel struct {
	width, height int
	star          starfield.Model
	targetDir     string
	version       string
	deployment    *deploy.Deployment
	detectErr     error // deploy.Detect's error for targetDir; see RemoveModel
	state         configureState

	profileChoice // the table; current is the deployment's own profile

	// note is the one line under the table after Enter on the current
	// row; a move clears it.
	note string

	// requested is the profile the confirm screen is about and the run
	// asks for; kept is the profile the deployment still has after a
	// successful run that did not change it.
	requested string
	kept      string

	confirm menu // configureConfirmRows, the Change row first

	// left is Esc, Cancel or Menu on a screen that started nothing: back
	// to the main menu (#210).
	left bool

	// installedAt and lookupInstalledAt: see RemoveModel.
	installedAt       time.Time
	lookupInstalledAt func(context.Context, *deploy.Deployment) time.Time

	run   engineRun
	seams engineRunSeams

	// send is the run's way back into the event loop, for the engine
	// stream reader; set by AppModel from the program's sender.
	send func(tea.Msg)
}

// NewConfigureModel constructs the Configure flow for a detected
// deployment. A nil deployment means nothing is here; a deployment
// without a Compose file is only prepared; a profile the launcher cannot
// change (deploy.CurrentProfile) is a dead end, each with its own words.
func NewConfigureModel(d *deploy.Deployment, targetDir, version string) ConfigureModel {
	memory, known := deploy.HostMemory()
	m := ConfigureModel{deployment: d, targetDir: targetDir, version: version}
	switch current, ok := deploy.CurrentProfile(d); {
	case d == nil:
		m.state = configureStateNotFound
	case !deploy.Installed(targetDir):
		m.state = configureStatePrepared
	case !ok:
		m.state = configureStateUnsupported
	default:
		m.state = configureStateProfile
		// The table never shows the scanner row, and its memory need counts
		// the deployment's own scanner setting: Configure runs as an update,
		// which never sends the scanner choice (#340).
		m.profileChoice = newCurrentProfileChoice(memory, known, current, d.ScanMode != deploy.ScanModeDisabled)
	}
	return m
}

func configureConfirmRows(to string) []string { return []string{"Change to " + to, "Cancel"} }

// Outcome surfaces the flow's result to AppModel: back to the menu when
// the person left before anything ran, otherwise the engine run's. The
// unchanged-profile screen is not a success, so it reports nothing.
func (m ConfigureModel) Outcome() flowOutcome {
	if m.left {
		return backToMenu
	}
	if m.state == configureStateUnchanged {
		return flowOutcome{}
	}
	return outcomeOf(m.run)
}

// Init implements tea.Model: on the profile screen it starts the
// read-only install-date lookup, as UpdateModel.Init does.
func (m ConfigureModel) Init() tea.Cmd {
	if m.state != configureStateProfile {
		return nil
	}
	return installedAtCmd(m.lookupInstalledAt, m.deployment)
}

// Update implements tea.Model.
func (m ConfigureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// See InstallModel.Update: neither message is consumed here, a resize
	// arms no tick chain, and the engine run re-arms the one chain while
	// it owns the screen.
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
		m.star = newSky(resized.Width, resized.Height)
	}
	if _, ok := msg.(tickMsg); ok {
		m.star = m.star.Advance()
		if m.state != configureStateRunning {
			return m, tick()
		}
	}

	// The lookup may answer after the run has started; it is this
	// model's message, never the engine run's.
	if at, ok := msg.(installedAtMsg); ok {
		m.installedAt = at.at
		return m, nil
	}

	if m.state == configureStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m.checkChanged(), cmd
	}

	// Outside a run a signal is the Ctrl-C key: nothing is working.
	if _, ok := msg.(SignalQuitMsg); ok {
		return m.handleKey(ctrlCPress)
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	return m, nil
}

// checkChanged looks at a run that just succeeded: .env-orbit is read
// again, and if the deployment still has the profile it had, the run is
// not shown as a success (an Orbit release that ignores the request on
// update would otherwise look like it worked).
func (m ConfigureModel) checkChanged() ConfigureModel {
	if m.state != configureStateRunning || !m.run.Done || !m.run.Succeeded || m.run.signalQuit {
		return m
	}
	detect := m.seams.detect
	if detect == nil {
		detect = deploy.Detect
	}
	d, err := detect(m.targetDir)
	if err != nil || d == nil {
		return m
	}
	if now, ok := deploy.CurrentProfile(d); ok && now != m.requested {
		m.kept = now
		m.state = configureStateUnchanged
	}
	return m
}

func (m ConfigureModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(msg) {
		return m, tea.Quit
	}
	switch m.state {
	case configureStateNotFound, configureStatePrepared, configureStateUnsupported, configureStateUnchanged:
		// The dead end: its one row and Esc both go back to the menu,
		// and no other key does anything (#228).
		if _, chosen, back := newMenu(deadEndRows...).onScreen(msg); back || chosen == "Menu" {
			m.left = true
		}
		return m, nil
	case configureStateUnavailable:
		if _, chosen, back := newMenu(installUnavailableRows...).onScreen(msg); back || chosen == "Back" {
			m.state = configureStateProfile
		}
		return m, nil
	case configureStateProfile:
		return m.handleProfileKey(msg)
	case configureStateConfirm:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

func (m ConfigureModel) handleProfileKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.profile, chosen, back = m.profile.onScreen(msg)
	m.note = ""
	switch {
	case back:
		m.left = true
	case chosen == "":
		// a move, or a key the menu does not use
	default:
		p := m.rowNamed(chosen)
		switch {
		case m.isCurrent(p):
			m.note = fmt.Sprintf("Orbit already runs the %s profile here — choose another to change it.", p.name)
		case p.profile == "":
			// Install's dead end, with its Back row: back is this table.
			m.state = configureStateUnavailable
		case m.shortOfMemory(p):
			// "not enough": the caret rests here but Enter does nothing,
			// and the line under the table says why.
		default:
			m.requested = p.profile
			m.state = configureStateConfirm
			m.confirm = newMenu(configureConfirmRows(p.name)...)
		}
	}
	return m, nil
}

func (m ConfigureModel) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.confirm, chosen, back = m.confirm.onScreen(msg)
	switch {
	case back, chosen == "Cancel":
		// One step back (#210): the profile screen, caret on the current row.
		m.state = configureStateProfile
		m.requested = ""
		m.note = ""
		m.profileChoice = m.toCurrent()
	case chosen != "":
		m.state = configureStateRunning
		title := "Configure — " + m.requestedRow()
		m.run = newEngineRun(deploy.ActionUpdate, m.targetDir, title, m.version).withProfile(m.requested).withSeams(m.seams).withSend(m.send)
		var cmd tea.Cmd
		m.run, cmd = m.run.start(m.width, m.height)
		return m, cmd
	}
	return m, nil
}

// rowOf is the table row whose profile is the given one.
func rowOf(profile string) installProfile {
	for _, p := range installProfileTable {
		if p.profile != "" && p.profile == profile {
			return p
		}
	}
	return installProfile{}
}

// requestedRow is the name of the row the run asks for.
func (m ConfigureModel) requestedRow() string { return rowOf(m.requested).name }

// View implements tea.Model.
func (m ConfigureModel) View() tea.View { return tea.NewView(m.view()) }

// view renders the screen's content.
func (m ConfigureModel) view() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case configureStateNotFound:
		return m.viewNotFound()
	case configureStatePrepared:
		return m.viewPrepared()
	case configureStateUnsupported:
		return m.viewUnsupported()
	case configureStateProfile:
		return m.viewProfile()
	case configureStateConfirm:
		return m.viewConfirm()
	case configureStateUnavailable:
		return viewUnavailableProfile(m.star, m.width, m.height)
	case configureStateRunning:
		return m.run.view(m.width, m.height)
	case configureStateUnchanged:
		return m.viewUnchanged()
	}
	return ""
}

// The flow screens speak the same starchart grammar as every other
// screen (design/DECISIONS.md): centred block, ⟡ mark and bold title,
// muted prose, individually-centred stacked menu, no keybind hints.

func (m ConfigureModel) viewNotFound() string {
	if m.detectErr != nil {
		return viewUnreadableDeployment(m.star, m.width, m.height, m.targetDir, m.detectErr)
	}
	return viewNoDeployment(m.star, m.width, m.height, m.targetDir,
		"There's nothing to configure here yet — Install chooses the profile as it goes in.")
}

func (m ConfigureModel) viewPrepared() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark),
		wrapWordsIn("Orbit isn't installed here yet, only prepared — Install is the way in.", measure(m.width, proseWidth), textArea(m.width))...)
	writeStackedMenu(&b, deadEndRows, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m ConfigureModel) viewUnsupported() string {
	var b strings.Builder
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark),
		wrapWordsIn("This deployment's profile can't be changed from the launcher yet", measure(m.width, proseWidth), textArea(m.width))...)
	writeWrappedIn(&b, style.MutedText, "It uses a local model, or a mix the launcher doesn't recognise. Orbit's own installer at a terminal can change it.",
		measure(m.width, proseWidth), textArea(m.width))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, deadEndRows, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m ConfigureModel) viewUnchanged() string {
	var b strings.Builder
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark), "The profile didn't change")
	writeWrappedIn(&b, style.MutedText,
		fmt.Sprintf("Orbit updated but kept the %s profile: this Orbit release predates profile changes from the launcher. Try again after Orbit's next release.", rowOf(m.kept).name),
		measure(m.width, proseWidth), textArea(m.width))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, deadEndRows, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// identity is the deployment's identity line, as Update's confirm screen draws it.
func (m ConfigureModel) identity() string {
	return style.Ink.Render(identityLine(m.deployment, m.installedAt, measure(m.width, proseWidth)))
}

func (m ConfigureModel) viewProfile() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "Choose a deployment profile")
	fmt.Fprintln(&b, m.identity())
	fmt.Fprintln(&b)
	m.writeProfileTable(&b, m.width)
	for _, line := range []string{m.memoryReason(), m.note} {
		if line == "" {
			continue
		}
		m.writeTableGap(&b, m.width)
		m.writeUnderTable(&b, line, m.width)
	}
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m ConfigureModel) viewConfirm() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "Change this deployment's profile")
	fmt.Fprintln(&b, m.identity())
	fmt.Fprintln(&b, style.Ink.Render(rowOf(m.current).name+" → "+m.requestedRow()))
	fmt.Fprintln(&b)
	effect := "This starts the attachment reader (orbit-tika), using about 1 GB more memory."
	if m.requested == deploy.ProfileStandard {
		effect = "This stops the attachment reader (orbit-tika), freeing about 1 GB. Attachments are no longer read for text; nothing stored is deleted."
	}
	// Each line wraps on its own, as Update's do, at the screen's edge.
	edge := measure(m.width, m.width)
	writeWrappedIn(&b, style.MutedText, effect, edge, textArea(m.width))
	writeWrappedIn(&b, style.MutedText, "It runs as an update inside the mission console, right here: Orbit restarts briefly on the latest Orbit release. Your settings and data are kept.", edge, textArea(m.width))
	fmt.Fprintln(&b)
	// Its own line (owner, 5a): a profile change is an update run, so it
	// also moves Orbit to its newest release. No version: the launcher
	// does not know the newest release before the run.
	writeWrappedIn(&b, style.Ink, "This also updates Orbit to its newest release.", edge, textArea(m.width))
	fmt.Fprintln(&b)
	m.confirm.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}
