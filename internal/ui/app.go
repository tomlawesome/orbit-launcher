package ui

import (
	"context"
	"net/url"
	"os"

	tea "github.com/charmbracelet/bubbletea"

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
// only), Update, Remove and Repair (a deliberate non-mutating stub) are
// all wired to real flows. Install and Update conclude on the shared
// success screen, whose Menu action returns to a freshly detected
// splash — the launcher is a loop now, not a one-way corridor.
type AppModel struct {
	width, height int
	state         appState
	splash        SplashModel
	remove        RemoveModel
	repair        RepairModel
	install       InstallModel
	update        UpdateModel
	success       SuccessModel

	// Configuration remembered so a refreshed splash (after a flow
	// returns to the menu) is built exactly like the first one.
	version       string
	updateCheck   func(context.Context) (string, bool, error)
	healthProbe   func(ctx context.Context, appURL string) bool
	detectOnStart bool

	// targetDir is where an existing deployment, if any, would be found.
	// Overridable in tests; production code leaves it empty and gets the
	// working directory.
	targetDir string

	// flowSeams lets tests fake the engine/handoff dependencies of
	// flows this model constructs; zero in production (real deps).
	flowSeams engineRunSeams

	// flowCheckVolumes fakes Install's stale-database-volume pre-flight
	// so tests need no Docker daemon; nil in production (real check).
	flowCheckVolumes func(context.Context, string) []deploy.DatabaseVolume
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

// WithVersion sets the version string rendered bottom-right on the
// splash, the mission console and the success screen, e.g. "v0.1.0".
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
func (m AppModel) WithDeploymentStatus(probe func(ctx context.Context, appURL string) bool) AppModel {
	m.healthProbe = probe
	m.detectOnStart = true
	m.splash = m.applyDeployment(m.splash)
	return m
}

// WithoutVolumeCheck disables Install's stale-database-volume pre-flight
// (issue #105). Its answer comes from the local Docker daemon, so it is
// the one part of the Install flow whose behaviour depends on the machine
// underneath it — which is exactly what a hermetic test suite cannot
// have. See ORBIT_LAUNCHER_NO_VOLUME_CHECK in cmd/orbit-launcher.
func (m AppModel) WithoutVolumeCheck() AppModel {
	m.flowCheckVolumes = func(context.Context, string) []deploy.DatabaseVolume { return nil }
	return m
}

// applyDeployment populates s's identity block from a fresh detection.
func (m AppModel) applyDeployment(s SplashModel) SplashModel {
	d, err := deploy.Detect(m.resolvedTargetDir())
	if err != nil || d == nil {
		return s
	}
	s.fqdn = displayHost(d.AppURL)
	s.appURL = d.AppURL
	s.orbitVersion = d.Version
	s.state = stateUnknown
	s.selected = menuUpdate // a deployment's most likely next act
	s.healthProbe = m.healthProbe
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
	return m, tea.Batch(cmds...)
}

// displayHost reduces an APP_URL to the bare FQDN the identity block
// shows — scheme and path are launcher noise at a glance.
func displayHost(appURL string) string {
	if u, err := url.Parse(appURL); err == nil && u.Host != "" {
		return u.Host
	}
	if appURL != "" {
		return appURL
	}
	return "deployment detected"
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

	switch m.state {
	case appStateSplash:
		return m.updateSplash(msg)
	case appStateRemove:
		updated, cmd := m.remove.Update(msg)
		m.remove = updated.(RemoveModel)
		return m, cmd
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
		if m.success.Chosen == "menu" {
			return m.refreshSplash()
		}
		return m, cmd
	}
	return m, nil
}

// watchOutcome moves the app forward when a flow's engine run
// concludes: the shared success screen, or back to the menu.
func (m AppModel) watchOutcome(o flowOutcome, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if !o.Done {
		return m, cmd
	}
	if o.Succeeded {
		m.success = NewSuccessModel(o.URL, o.Elapsed, m.version)
		m.success.noAnimation = m.splash.noAnimation
		m.state = appStateSuccess
		return m, func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} }
	}
	if o.WantsMenu {
		return m.refreshSplash()
	}
	return m, cmd
}

func (m AppModel) updateSplash(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.splash.Update(msg)
	m.splash = updated.(SplashModel)

	if !m.splash.quitting || m.splash.Chosen == "" {
		return m, cmd
	}

	sizeCmd := func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} }

	switch m.splash.Chosen {
	case "Remove":
		// deploy.Detect's error return only ever reflects a real I/O
		// failure reading an existing .env-orbit, never "not installed"
		// (that's a nil Deployment, nil error) — the Remove confirm
		// screen already renders sensibly for a nil Deployment, so
		// there's nothing actionable to do with an error here beyond
		// proceeding with what we have.
		deployment, _ := deploy.Detect(m.resolvedTargetDir())
		m.remove = NewRemoveModel(deployment)
		m.state = appStateRemove
		return m, sizeCmd
	case "Repair":
		m.repair = NewRepairModel(m.resolvedTargetDir(), m.version)
		m.repair.prepare = m.flowSeams.prepareRepair
		m.state = appStateRepair
		return m, tea.Batch(sizeCmd, m.repair.Init())
	case "Install":
		m.install = NewInstallModel(m.resolvedTargetDir(), m.version)
		m.install.seams = m.flowSeams
		m.install.checkVolumes = m.flowCheckVolumes
		m.state = appStateInstall
		// Install's Init runs the stale-database-volume pre-flight
		// (issue #105) — like Repair's, it is read-only, so it starts
		// with the flow rather than waiting for a confirmation.
		return m, tea.Batch(sizeCmd, m.install.Init())
	case "Update":
		deployment, _ := deploy.Detect(m.resolvedTargetDir())
		m.update = NewUpdateModel(deployment, m.resolvedTargetDir(), m.version)
		m.update.seams = m.flowSeams
		m.state = appStateUpdate
		return m, sizeCmd
	default:
		return m, tea.Quit
	}
}

// View implements tea.Model.
func (m AppModel) View() string {
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
	return ""
}
