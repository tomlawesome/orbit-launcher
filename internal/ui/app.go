package ui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

type appState int

const (
	appStateSplash appState = iota
	appStateRemove
	appStateRepair
	appStateInstall
	appStateUpdate
	appStateSuccess
)

// AppModel is the root model: it starts at the splash screen and, once a
// choice is made, hands control to that flow. Install (Standard profile
// only), Update, Remove and Repair are all wired to real flows. Install and Update conclude on the shared
// success screen, whose Menu action returns to a freshly detected
// splash — the launcher is a loop now, not a one-way corridor.
type AppModel struct {
	width, height int
	state         appState
	splash        SplashModel
	remove        RemoveModel
	repair        RepairModel
	install       InstallModel
	// collected is the guided configuration this launcher session has
	// collected (collectedConfig): it outlives one Install flow, so
	// Install from the menu re-seeds without asking again. Removed when
	// a Remove stand-down completes and on exit (FinishDeferredCleanup),
	// never kept between sessions.
	collected *collectedConfig
	update    UpdateModel
	success   SuccessModel

	// Configuration remembered so a refreshed splash (after a flow
	// returns to the menu) is built exactly like the first one.
	version       string
	updateCheck   func(context.Context) (string, bool, error)
	healthProbe   func(ctx context.Context, appURL string) deploy.HealthReason
	detectOnStart bool

	// targetDir is where an existing deployment, if any, would be found.
	// Overridable in tests; production code leaves it empty and gets the
	// working directory. resolvedTargetDir is the one place it is
	// resolved: every flow is handed that directory and guesses nothing.
	targetDir string

	// flowSeams lets tests fake the engine/handoff dependencies of
	// flows this model constructs; zero in production (real deps).
	flowSeams engineRunSeams

	// flowCheckVolumes fakes Install's stale-database-volume pre-flight
	// so tests need no Docker daemon; nil in production (real check).
	flowCheckVolumes func(context.Context, string) []deploy.DatabaseVolume

	// flowNoVolumeCheck is set by WithoutVolumeCheck: Install's Enter
	// then never waits for the pre-flight.
	flowNoVolumeCheck bool

	// flowInstalledAt fakes Remove's and Update's install-date lookup
	// the same way; nil in production (asks Docker).
	flowInstalledAt func(context.Context, *deploy.Deployment) time.Time

	// flowReadClaim fakes the success screen's claim-link read after an
	// Install (#259) so tests need no Docker daemon; nil in production
	// (deploy.ReadClaimLink).
	flowReadClaim func(ctx context.Context, dir, appURL string, since time.Time) (string, error)

	// servicesReader reads the deployment's containers for Repair's
	// services block and the splash's degraded count (#255). Until
	// WithServicesReader sets it (servicesReaderSet), the real `docker
	// compose ps` is used; once set, nil means no reading anywhere (#277).
	servicesReader    func(dir string) ([]deploy.ServiceState, error)
	servicesReaderSet bool

	// runningReader reads the deployment's app container for the
	// splash's running check (#316). Nil, the default, checks nothing, so
	// no constructor asks Docker; only the real entry point opts in
	// (WithRunningReader), and WithoutRunningCheck switches it off again.
	runningReader func(context.Context, *deploy.Deployment) (deploy.RunningApp, error)

	// flowNoticeDuration shortens Install's development-notice countdown
	// (#175) in tests; zero in production (the 20 s default).
	flowNoticeDuration time.Duration

	// altScreen puts the program in the terminal's alternate screen
	// buffer. It was a tea.NewProgram option until the view started
	// carrying it, so the root model now holds the caller's choice: the
	// real entry point asks for it, an in-memory teatest run does not.
	altScreen bool

	// exitNote is a Ctrl-C stop's last word, kept for main to print
	// once the screen has closed (ExitNote).
	exitNote string
	// signalStop is the signal that quit an Update past its commit
	// (SignalStop).
	signalStop os.Signal

	// flowSend is how a flow's engine stream reader gets its output
	// back into the event loop (#159). cmd/orbit-launcher supplies the
	// running program's Send; a test either supplies its own or gets a
	// run that reports it cannot read the engine, never one that waits
	// in silence.
	flowSend func(tea.Msg)
}

// NewAppModel constructs the root application model, starting at the
// splash screen.
func NewAppModel() AppModel {
	return AppModel{splash: NewSplashModel(), state: appStateSplash}
}

// NewAppModelNoAnimation constructs the root application model with the
// splash screen's starfield frozen — see NewSplashModelNoAnimation.
func NewAppModelNoAnimation() AppModel {
	return AppModel{splash: NewSplashModelNoAnimation(), state: appStateSplash}
}

// WithUpdateCheck opts the splash screen into a non-blocking self-update
// check against GitHub Releases, using fn (in production,
// internal/release.CheckForUpdate). No constructor enables this by
// default — see SplashModel.checkForUpdate — so only a caller that
// explicitly wants it (cmd/orbit-launcher's real entry point) has to
// ask.
func (m AppModel) WithUpdateCheck(fn func(context.Context) (version string, hasUpdate bool, err error)) AppModel {
	m.updateCheck = fn
	m.splash.checkForUpdate = fn
	return m
}

// WithVersion sets the launcher version printed, as given, in the centred
// foot of the splash and the mission console, e.g. "v0.1.0". An empty
// version renders no foot; release.DisplayVersion decides the shape.
func (m AppModel) WithVersion(v string) AppModel {
	m.version = v
	m.splash.version = v
	return m
}

// WithDeploymentStatus detects an existing deployment and populates the
// splash's identity block. Detection is deliberately synchronous — one
// small file read — so the caret's preselection is settled before the
// first frame, with no race against the user's first keypress. probe,
// when non-nil, resolves alive-vs-degraded asynchronously from the
// splash's Init (see SplashModel.probeHealthCmd); nil leaves the state
// at "deployment exists, health unknown", which renders as the FQDN
// with no status word.
func (m AppModel) WithDeploymentStatus(probe func(ctx context.Context, appURL string) deploy.HealthReason) AppModel {
	m.healthProbe = probe
	m.detectOnStart = true
	m.splash = m.applyDeployment(m.splash)
	return m
}

// WithServicesReader supplies how the services are read, for Repair's
// services block and the splash's count, in place of the real `docker
// compose ps` (#277): tests pass a fake, so no app-level test depends on
// the machine's Docker. Nil reads nothing: the splash does not count and
// Repair shows no services block, whichever order this and
// WithDeploymentStatus are given in.
func (m AppModel) WithServicesReader(read func(dir string) ([]deploy.ServiceState, error)) AppModel {
	m.servicesReader, m.servicesReaderSet = read, true
	if m.splash.targetDir != "" { // a deployment was applied already
		m.splash.serviceStates = read
	}
	return m
}

// readServices is the services reader the app hands its screens: the
// one WithServicesReader set, nil included, else the real one.
func (m AppModel) readServices() func(dir string) ([]deploy.ServiceState, error) {
	if m.servicesReaderSet {
		return m.servicesReader
	}
	return realServiceStates
}

// WithSender gives the flows a way to push messages into the event
// loop from outside it, which the engine stream reader needs (#159).
// Without it a run cannot read the engine and says so plainly rather
// than waiting for output that can never arrive.
func (m AppModel) WithSender(send func(tea.Msg)) AppModel {
	m.flowSend = send
	return m
}

// WithoutVolumeCheck disables Install's stale-database-volume pre-flight
// (issue #105). Its answer comes from the local Docker daemon, so it is
// the one part of the Install flow whose behaviour depends on the machine
// underneath it — which is exactly what a hermetic test suite cannot
// have. See ORBIT_LAUNCHER_NO_VOLUME_CHECK in cmd/orbit-launcher.
// It also leaves the install date off Remove's and Update's confirm
// screens: that date is read from the same daemon's volumes.
func (m AppModel) WithoutVolumeCheck() AppModel {
	m.flowCheckVolumes = func(context.Context, string) []deploy.DatabaseVolume { return nil }
	m.flowNoVolumeCheck = true
	m.flowInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }
	return m
}

// WithRunningReader turns the running check on (#316): the splash asks
// read which app container the deployment runs and says so when it
// contradicts the record, and Update, Repair and Remove refuse a
// container started from another folder. In production read is
// deploy.RunningAppOf; tests pass a fake, so none depends on the
// machine's Docker. Whichever order this and WithDeploymentStatus are
// given in, the check is made for an installed deployment only.
func (m AppModel) WithRunningReader(read func(context.Context, *deploy.Deployment) (deploy.RunningApp, error)) AppModel {
	m.runningReader = read
	if m.splash.deployment != nil { // an installed deployment was applied already
		m.splash.runningRead = read
	}
	return m
}

// WithoutRunningCheck turns the running check off: no question, no rows
// and no refusals. Its answer comes from the local Docker daemon, which a
// hermetic test suite does not control. See ORBIT_LAUNCHER_NO_RUNNING_CHECK
// in cmd/orbit-launcher.
func (m AppModel) WithoutRunningCheck() AppModel {
	return m.WithRunningReader(nil)
}

// WithAltScreen renders in the terminal's alternate screen buffer, so the
// launcher owns the whole window and hands the user's scrollback back
// untouched on exit. Only cmd/orbit-launcher asks for it — see
// AppModel.View.
func (m AppModel) WithAltScreen() AppModel {
	m.altScreen = true
	return m
}

// applyDeployment populates s's identity block from a fresh detection.
func (m AppModel) applyDeployment(s SplashModel) SplashModel {
	s.folder = filepath.Clean(m.resolvedTargetDir())
	d, err := deploy.Detect(m.resolvedTargetDir())
	if err != nil {
		// The file is there but unreadable: no identity and nothing to
		// probe, and Update preselected so the first Enter lands on the
		// screen that explains rather than an Install that would be
		// refused (#238).
		s.state = stateUnreadable
		s.menu.sel = menuUpdate
		return s
	}
	if d == nil {
		return s
	}
	s.fqdn = deploymentIdentity(d)
	s.appURL = d.AppURL
	s.orbitVersion = d.Version
	s.state = stateUnknown
	s.menu.sel = menuUpdate // a deployment's most likely next act
	s.targetDir = m.resolvedTargetDir()
	s.serviceStates = m.readServices()
	if !deploy.Installed(s.targetDir) {
		// Only the pre-provisioned configuration, as a rolled-back
		// install leaves it: Install is what the engine will accept,
		// and there is nothing to probe. An address nothing of ours
		// serves can only answer "not responding" (painting the folder
		// degraded and moving the caret to a Repair the engine refuses)
		// or be answered by a stranger. The state stays unknown.
		s.menu.sel = menuInstall
		return s
	}
	s.healthProbe = m.healthProbe
	s.deployment = d
	s.runningRead = m.runningReader
	return s
}

// refreshSplash rebuilds the splash for a return-to-menu: same
// configuration, fresh detection (an install that just succeeded should
// greet its own deployment). The returned cmd starts the splash's
// background lookups but deliberately not another tick chain — the
// running one carries on.
func (m AppModel) refreshSplash() (AppModel, tea.Cmd) {
	s := NewSplashModel()
	if m.splash.noAnimation {
		s = NewSplashModelNoAnimation()
	}
	// The arrival plays once per process launch — a return to the menu
	// goes straight to the lit room.
	s.introDone = true
	s.version = m.version
	s.checkForUpdate = m.updateCheck
	if m.detectOnStart {
		s = m.applyDeployment(s)
	}
	m.splash = s
	m.state = appStateSplash

	var cmds []tea.Cmd
	cmds = append(cmds, func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} })
	if s.checkForUpdate != nil {
		cmds = append(cmds, s.checkForUpdateCmd())
	}
	if s.healthProbe != nil && s.appURL != "" {
		cmds = append(cmds, s.probeHealthCmd())
	}
	if cmd := s.checkRunningCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m AppModel) resolvedTargetDir() string {
	if m.targetDir != "" {
		return m.targetDir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// Init implements tea.Model.
func (m AppModel) Init() tea.Cmd { return m.splash.Init() }

// Update implements tea.Model.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
	}
	// The splash and success screens have no working step, so a signal is
	// the Ctrl-C key there; every flow tells the two apart itself.
	if _, ok := msg.(SignalQuitMsg); ok && (m.state == appStateSplash || m.state == appStateSuccess) {
		msg = ctrlCPress
	}

	// The running check may answer after a flow has opened: the splash
	// keeps the answer, and the flow decides whether it still matters.
	// Install and the success screen are not concerned (#316).
	if result, ok := msg.(runningResultMsg); ok && m.state != appStateSplash {
		updated, _ := m.splash.Update(result)
		m.splash = updated.(SplashModel)
	}

	switch m.state {
	case appStateSplash:
		return m.updateSplash(msg)
	case appStateRemove:
		updated, cmd := m.remove.Update(msg)
		m.remove = updated.(RemoveModel)
		// A completed stand-down retires the deployment the held answers
		// were collected for: drop them, so a later Install into a wiped
		// folder asks again instead of reusing configuration the person
		// was told is gone.
		if m.remove.state == removeStateDone && m.collected != nil {
			m.collected.release()
			m.collected = nil
		}
		return m.watchOutcome(m.remove.Outcome(), cmd)
	case appStateRepair:
		updated, cmd := m.repair.Update(msg)
		m.repair = updated.(RepairModel)
		return m.watchOutcome(m.repair.Outcome(), cmd)
	case appStateInstall:
		updated, cmd := m.install.Update(msg)
		m.install = updated.(InstallModel)
		return m.watchOutcome(m.install.Outcome(), cmd)
	case appStateUpdate:
		updated, cmd := m.update.Update(msg)
		m.update = updated.(UpdateModel)
		return m.watchOutcome(m.update.Outcome(), cmd)
	case appStateSuccess:
		updated, cmd := m.success.Update(msg)
		m.success = updated.(SuccessModel)
		return m.watchOutcome(m.success.Outcome(), cmd)
	}
	return m, nil
}

// ExitNote is the warning that a Ctrl-C stop ended the installer before
// it finished undoing its work; empty otherwise.
func (m AppModel) ExitNote() string { return m.exitNote }

// SignalStop is the signal that quit an Update past its commit, nil if
// none did. main turns SIGTERM and SIGHUP into a stderr line and exit 128
// plus the signal number (#304).
func (m AppModel) SignalStop() os.Signal { return m.signalStop }

// watchOutcome moves the app forward when a flow's engine run
// concludes: the shared success screen, or back to the menu.
func (m AppModel) watchOutcome(o flowOutcome, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	// Before the Done check: a stop quits without ever setting Done.
	if o.ExitNote != "" {
		m.exitNote = o.ExitNote
	}
	if o.SignalStop != nil {
		m.signalStop = o.SignalStop
	}
	if !o.Done {
		return m, cmd
	}
	if o.Succeeded {
		m.success = NewSuccessModel(o.URL, o.Elapsed, m.version)
		m.success.noAnimation = m.splash.noAnimation
		if m.state == appStateInstall {
			m.success.readClaim = m.claimReader(o.URL)
			// Only a fresh install ends with optional services still
			// starting; an update fails on them instead.
			m.success.serviceNotes = m.install.run.ServiceNotes
		}
		m.state = appStateSuccess
		sizeCmd := func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} }
		// Not Init: the sky's ticks already run, carried over from the
		// flow, and a second chain would double their pace.
		if read := m.success.claimCmd(); read != nil {
			return m, tea.Batch(sizeCmd, read)
		}
		return m, sizeCmd
	}
	if o.WantsRepair {
		// The stopped screen's Open Repair: Repair on the same folder.
		return m.startRepair()
	}
	if o.WantsMenu {
		return m.refreshSplash()
	}
	return m, cmd
}

// startRepair opens Repair on the launcher's folder, from the splash or
// from a stopped Install or Update that left its services in place. When
// the running check found the app container started from another folder
// it opens on the refusal instead, and no diagnosis starts (#316).
func (m AppModel) startRepair() (tea.Model, tea.Cmd) {
	otherFolder := ""
	if m.splash.mismatch.Kind == deploy.MismatchFolder {
		otherFolder = m.splash.mismatch.Folder
	}
	sizeCmd := func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} }
	m.repair = NewRepairModel(m.resolvedTargetDir(), m.version)
	m.repair.engineRunSeams = m.flowSeams
	m.repair.serviceStates = m.readServices()
	m.repair.foreignOwned = deploy.ForeignOwned
	m.repair.send = m.flowSend
	if otherFolder != "" {
		m.repair = m.repair.refuseOtherFolder(otherFolder)
	}
	m.state = appStateRepair
	return m, tea.Batch(sizeCmd, m.repair.Init())
}

// claimReader is the success screen's claim-link read after a finished
// Install (#259): the app's logs since the install started, in the
// folder it installed into. Only Install offers it; after an Update
// Orbit has long been claimed. No address means nothing to compare a
// link against, so nothing is read.
func (m AppModel) claimReader(appURL string) func() (string, error) {
	if appURL == "" {
		return nil
	}
	read := m.flowReadClaim
	if read == nil {
		read = deploy.ReadClaimLink
	}
	dir, since := m.install.targetDir, m.install.startedAt
	return func() (string, error) { return read(context.Background(), dir, appURL, since) }
}

func (m AppModel) updateSplash(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.splash.Update(msg)
	m.splash = updated.(SplashModel)

	if !m.splash.quitting || m.splash.Chosen == "" {
		return m, cmd
	}

	sizeCmd := func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} }

	// A running check that has answered "started from another folder"
	// turns Update, Repair and Remove into a refusal naming that folder.
	// One that has not answered yet is handed to the open flow later.
	otherFolder := ""
	if m.splash.mismatch.Kind == deploy.MismatchFolder {
		otherFolder = m.splash.mismatch.Folder
	}

	switch m.splash.Chosen {
	case "Remove":
		// deploy.Detect's error return only ever reflects a real I/O
		// failure reading an existing .env-orbit, never "not installed"
		// (that's a nil Deployment, nil error). Either way nothing usable
		// was found, so Remove stops at the start; it names where it
		// looked, or says the file could not be read (#238).
		dir := m.resolvedTargetDir()
		deployment, err := deploy.Detect(dir)
		m.remove = NewRemoveModel(deployment)
		m.remove.targetDir = dir
		m.remove.detectErr = err
		m.remove.lookupInstalledAt = m.flowInstalledAt
		if otherFolder != "" && deployment != nil {
			m.remove = m.remove.refuseOtherFolder(otherFolder)
		}
		m.state = appStateRemove
		// Init looks up the install date — read-only, so like Install's
		// pre-flight it starts with the flow.
		return m, tea.Batch(sizeCmd, m.remove.Init())
	case "Repair":
		return m.startRepair()
	case "Install":
		m.install = NewInstallModel(m.resolvedTargetDir(), m.version)
		if m.collected == nil {
			m.collected = &collectedConfig{}
		}
		m.install.collected = m.collected
		m.install.seams = m.flowSeams
		m.install.checkVolumes = m.flowCheckVolumes
		if m.install.checkVolumes == nil {
			m.install.checkVolumes = deploy.UnownedDatabaseVolumes
		}
		m.install.noVolumeCheck = m.flowNoVolumeCheck
		m.install.noticeDuration = m.flowNoticeDuration
		m.install.send = m.flowSend
		m.state = appStateInstall
		// Install's Init runs the stale-database-volume pre-flight
		// (issue #105) — like Repair's, it is read-only, so it starts
		// with the flow rather than waiting for a confirmation.
		return m, tea.Batch(sizeCmd, m.install.Init())
	case "Update":
		dir := m.resolvedTargetDir()
		deployment, err := deploy.Detect(dir)
		m.update = NewUpdateModel(deployment, dir, m.version)
		m.update.detectErr = err
		m.update.seams = m.flowSeams
		m.update.send = m.flowSend
		m.update.lookupInstalledAt = m.flowInstalledAt
		if otherFolder != "" && deployment != nil {
			m.update = m.update.refuseOtherFolder(otherFolder)
		}
		m.state = appStateUpdate
		return m, tea.Batch(sizeCmd, m.update.Init())
	default:
		return m, tea.Quit
	}
}

// View implements tea.Model. The alternate screen is a property of the
// view rather than a program option, so this is where the caller's choice
// (WithAltScreen) is applied — every flow below draws the same way
// whether or not the launcher owns the whole window.
func (m AppModel) View() tea.View {
	v := m.screen()
	v.AltScreen = m.altScreen
	return v
}

// screen is the view of whichever flow currently owns the screen.
func (m AppModel) screen() tea.View {
	switch m.state {
	case appStateSplash:
		return m.splash.View()
	case appStateRemove:
		return m.remove.View()
	case appStateRepair:
		return m.repair.View()
	case appStateInstall:
		return m.install.View()
	case appStateUpdate:
		return m.update.View()
	case appStateSuccess:
		return m.success.View()
	}
	return tea.NewView("")
}
