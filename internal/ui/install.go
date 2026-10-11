package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type installState int

const (
	installStateProfile installState = iota
	installStateUnavailableProfile
	installStateConfirm
	installStateNotice
	installStateRunning
	installStateStaleVolume
)

// InstallModel is the Install flow: profile choice, confirmation, then
// the mission console — the engine's event stream rendered natively
// inside the TUI (see internal/ui/enginerun.go and design/mockups-v5.html
// section 02). Only the Standard profile is wired; AI/Full are visible
// but honestly say they aren't available yet rather than silently doing
// the wrong thing.
//
// orbit-launcher still never invents Orbit configuration itself
// (issue #51): the engine run cannot prompt by construction, and when
// the engine refuses with its configuration-required signal, the flow
// runs configure.sh's own guided setup — the single source of truth
// for what fields it needs and what answers are valid — in-console
// over the machine prompt protocol when the engine speaks it
// (orbit#297), or via the terminal handoff when it doesn't.
type InstallModel struct {
	width, height int
	star          starfield.Model
	state         installState
	targetDir     string
	version       string

	profile menu // installProfiles
	confirm menu // installConfirmRows
	volume  menu // installVolumeRows

	// left is Esc or Cancel on the flow's first screen: back to the main
	// menu with nothing started (#210).
	left bool

	// staleVolumes carries the pre-flight's finding (issue #105): Orbit
	// database volumes this machine already has and this target cannot
	// prove it owns.
	staleVolumes []deploy.DatabaseVolume

	// send is the run's way back into the event loop, for the engine
	// stream reader; set by AppModel from the program's sender.
	send func(tea.Msg)

	// checkVolumes is overridable in tests so they need no Docker
	// daemon — production code leaves it nil and gets the real check.
	checkVolumes func(context.Context, string) []deploy.DatabaseVolume

	// notice is the development notice (#175) between the confirm
	// screen and the run; rebuilt fresh on every Install now.
	notice noticeModel

	// noticeDuration is the notice's reading countdown; zero means
	// noticeDefaultDuration. Copied from AppModel.flowNoticeDuration so
	// tests can shorten it.
	noticeDuration time.Duration

	run engineRun

	// Test seams, copied into the engine run at start; nil means real.
	seams engineRunSeams
}

// engineRunSeams bundles the overridable dependencies tests inject; nil
// fields get the real implementations. It is the one list of seams:
// engineRun and RepairModel embed it, Install and Update hold it and
// hand it to their run, and AppModel passes it down whole.
type engineRunSeams struct {
	prepareEngine  prepareEngineFunc
	prepareInstall prepareInstallFunc
	runHandoff     runHandoffFunc
	detect         detectFunc
	now            nowFunc
	prepareConfig  prepareConfigFunc
	startConfig    startConfigFunc
	adoptConfig    adoptConfigFunc
	recheckConfig  recheckConfigFunc
	prepareRepair  prepareRepairFunc
	prepareRotate  prepareRotateFunc
}

// withSend gives the run its way back into the event loop.
func (r engineRun) withSend(send func(tea.Msg)) engineRun {
	r.send = send
	return r
}

func (r engineRun) withSeams(s engineRunSeams) engineRun {
	r.engineRunSeams = s
	return r
}

// NewInstallModel constructs the Install flow for targetDir.
func NewInstallModel(targetDir, version string) InstallModel {
	return InstallModel{
		targetDir: targetDir,
		version:   version,
		profile:   newMenu(installProfiles...),
		confirm:   newMenu(installConfirmRows...),
		volume:    newMenu(installVolumeRows...),
	}
}

// The profile screen's rows. Only Standard is wired; AI and Full lead to
// the honest dead end.
const installStandard = "Standard" + style.Separator + "mail, documents, calendar"

var installProfiles = []string{
	installStandard,
	"AI" + style.Separator + "adds a local model for search & suggestions",
	"Full" + style.Separator + "AI features plus every optional service",
}

var (
	installConfirmRows = []string{"Install now", "Cancel"}
	installVolumeRows  = []string{"Continue anyway", "Cancel"}

	// installUnavailableRows: the dead end's one row, back to the
	// profile choice that led in.
	installUnavailableRows = []string{"Back"}
)

// Outcome surfaces the flow's result to AppModel: back to the menu when
// the person left on the first screen, otherwise the engine run's.
func (m InstallModel) Outcome() flowOutcome {
	if m.left {
		return backToMenu
	}
	return outcomeOf(m.run)
}

// staleVolumesMsg carries the pre-flight's finding back into the event
// loop. An empty slice is the overwhelmingly common case and means the
// flow proceeds exactly as it always has.
type staleVolumesMsg struct{ volumes []deploy.DatabaseVolume }

// Init implements tea.Model: the stale-database-volume pre-flight starts
// immediately. It is read-only — one `docker volume ls` — so there is
// nothing to confirm first, and it runs off the first frame's path so
// the profile screen is never waiting on Docker to draw.
func (m InstallModel) Init() tea.Cmd {
	check := m.checkVolumes
	if check == nil {
		check = deploy.UnownedDatabaseVolumes
	}
	targetDir := m.targetDir
	return func() tea.Msg {
		return staleVolumesMsg{volumes: check(context.Background(), targetDir)}
	}
}

// Update implements tea.Model.
func (m InstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// The sky is this model's own, but neither message is consumed here:
	// while an engine run owns the screen the run's console needs both
	// too, and it re-arms the tick chain itself. A resize builds the sky
	// and arms nothing — the one app-wide chain SplashModel.Init started
	// is still running, and starting a second here would double the
	// sky's speed for the rest of the process (see AppModel.refreshSplash).
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
		m.star = newSky(resized.Width, resized.Height)
		if m.state == installStateNotice {
			// A resize can reveal the end, which opens the scroll gate.
			m.notice = m.notice.resize(resized.Width, resized.Height)
		}
	}
	if _, ok := msg.(tickMsg); ok {
		m.star = m.star.Advance()
		if m.state != installStateRunning {
			return m, tick()
		}
	}

	// The notice's countdown redraws on its own one-second chain. A
	// tick from an earlier visit (Back, then Install now again)
	// carries that visit's start and is dropped, so two chains never
	// run; the chain ends when the countdown does.
	if t, ok := msg.(noticeTickMsg); ok {
		if m.state != installStateNotice || !t.start.Equal(m.notice.start) || m.notice.timerDone(m.now(), m.noticeDur()) {
			return m, nil
		}
		return m, noticeTick(m.notice.start)
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok && m.state == installStateNotice {
		switch wheel.Button {
		case tea.MouseWheelUp:
			m.notice = m.notice.scrollBy(-noticeWheelRows)
		case tea.MouseWheelDown:
			m.notice = m.notice.scrollBy(noticeWheelRows)
		}
		return m, nil
	}

	if found, ok := msg.(staleVolumesMsg); ok {
		// Only the profile screen is interrupted. The check is async, so
		// a quick hand can already be past it — and stopping someone
		// mid-confirm to report a pre-flight finding would be worse than
		// letting the engine give them the same news.
		if len(found.volumes) > 0 && m.state == installStateProfile {
			m.staleVolumes = found.volumes
			m.state = installStateStaleVolume
			m.volume = newMenu(installVolumeRows...)
		}
		return m, nil
	}

	if m.state == installStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m, cmd
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	return m, nil
}

func (m InstallModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(msg) {
		return m, tea.Quit
	}
	switch m.state {
	case installStateStaleVolume:
		return m.handleStaleVolumeKey(msg)
	case installStateProfile:
		return m.handleProfileKey(msg)
	case installStateUnavailableProfile:
		// The only way out of the honest dead end is back to the
		// choice that led in.
		if _, chosen, back := newMenu(installUnavailableRows...).onScreen(msg); back || chosen == "Back" {
			m.state = installStateProfile
		}
		return m, nil
	case installStateConfirm:
		return m.handleConfirmKey(msg)
	case installStateNotice:
		return m.handleNoticeKey(msg)
	}
	return m, nil
}

// handleStaleVolumeKey keeps the pre-flight advisory. Continue anyway is
// always offered and always works: this screen reports what the engine
// is about to refuse, and a detection mistake here must never be able to
// stand between someone and an install.
//
// It takes the first screen's place when it fires, so Esc and Cancel are
// the same act here as on the profile screen: back to the main menu.
func (m InstallModel) handleStaleVolumeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.volume, chosen, back = m.volume.onScreen(msg)
	switch {
	case chosen == "Continue anyway":
		m.state = installStateProfile
	case back, chosen == "Cancel":
		m.left = true
	}
	return m, nil
}

func (m InstallModel) handleProfileKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.profile, chosen, back = m.profile.onScreen(msg)
	switch {
	case back:
		m.left = true
	case chosen == "": // a move, or a key the menu does not use
	case chosen == installStandard:
		m.state = installStateConfirm
		m.confirm = newMenu(installConfirmRows...)
	default:
		m.state = installStateUnavailableProfile
	}
	return m, nil
}

func (m InstallModel) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.confirm, chosen, back = m.confirm.onScreen(msg)
	switch {
	case back, chosen == "Cancel":
		m.state = installStateProfile
	case chosen == "Install now":
		// Install now opens the development notice (#175), fresh every
		// time: going back and coming again restarts the countdown and
		// closes the scroll gate.
		m.state = installStateNotice
		m.notice = newNotice(m.now(), m.width, m.height)
		return m, noticeTick(m.notice.start)
	}
	return m, nil
}

// handleNoticeKey wires the notice: Esc or Back returns to the
// confirm screen, a correct phrase starts the run, nothing else leaves.
func (m InstallModel) handleNoticeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var out noticeOutcome
	m.notice, out = m.notice.handleKey(msg, m.now, m.noticeDur())
	switch out {
	case noticeBack:
		m.state = installStateConfirm
		return m, nil
	case noticeAccept:
		m.state = installStateRunning
		return m.startRun()
	}
	return m, nil
}

// startRun builds and starts the engine run.
func (m InstallModel) startRun() (InstallModel, tea.Cmd) {
	m.run = newEngineRun(deploy.ActionInstall, m.targetDir, "Install — Standard", m.version).withSeams(m.seams).withSend(m.send)
	var cmd tea.Cmd
	m.run, cmd = m.run.start(m.width, m.height)
	return m, cmd
}

// now is the flow's clock: the injected seam, or the real one.
func (m InstallModel) now() time.Time {
	if m.seams.now != nil {
		return m.seams.now()
	}
	return time.Now()
}

func (m InstallModel) noticeDur() time.Duration {
	if m.noticeDuration > 0 {
		return m.noticeDuration
	}
	return noticeDefaultDuration
}

// View implements tea.Model.
//
// Mouse reporting is on for the notice only, so the wheel scrolls it;
// every other screen leaves the terminal's own mouse handling alone.
func (m InstallModel) View() tea.View {
	v := tea.NewView(m.view())
	if m.state == installStateNotice {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// view renders the screen's content.
func (m InstallModel) view() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case installStateStaleVolume:
		return m.viewStaleVolume()
	case installStateProfile:
		return m.viewProfile()
	case installStateUnavailableProfile:
		return m.viewUnavailableProfile()
	case installStateConfirm:
		return m.viewConfirm()
	case installStateNotice:
		return m.viewNotice()
	case installStateRunning:
		return m.run.view(m.width, m.height)
	}
	return ""
}

// The flow screens speak the same starchart grammar as every other
// screen (design/DECISIONS.md): centred block, ⟡ mark and bold title,
// muted prose, individually-centred stacked menu, no keybind hints.

// viewStaleVolume is the pre-flight screen (issue #105). It explains a
// refusal the engine is about to make and names the volume behind it —
// nothing more. It deliberately does not offer to clear anything: the
// dead end being fixed here is being told what is refused and never why,
// and deleting someone's database is a separate, higher-consequence act
// that belongs on its own screen if it is ever added at all.
//
// Every name shown comes verbatim from docker's own output (see
// deploy.UnownedDatabaseVolumes), because a person may well act on a
// volume name they read here.
func (m InstallModel) viewStaleVolume() string {
	var b strings.Builder
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark), "This machine already has an Orbit database")

	for _, v := range m.staleVolumes {
		line := v.Name
		if v.Project != "" {
			// Named as the Compose label it is, not as "from the
			// <project> deployment": since Orbit #999/#1043 every
			// install declares the same project, so that phrasing
			// read as if it identified one install when it never
			// could. The directory sentence below is what actually
			// tells them apart.
			line += style.Separator + "Compose project " + v.Project
		}
		fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(line))
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, style.MutedText.Render("It was left by an earlier install in another directory, and"))
	fmt.Fprintln(&b, style.MutedText.Render("this one has none of the credentials that would prove it owns"))
	fmt.Fprintln(&b, style.MutedText.Render("that database. Orbit won't start over a database it can't"))
	fmt.Fprintln(&b, style.MutedText.Render("prove belongs to it, so the installer will stop rather than"))
	fmt.Fprintln(&b, style.MutedText.Render("risk the data inside it."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("nothing has been changed, and nothing will be by this screen"))
	fmt.Fprintln(&b)
	m.volume.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m InstallModel) viewProfile() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "Choose a deployment profile")
	m.profile.write(&b)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("only Standard is available so far"))
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m InstallModel) viewUnavailableProfile() string {
	var b strings.Builder
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark), "This profile isn't available yet")
	fmt.Fprintln(&b, style.MutedText.Render("AI and Full need local-model configuration that"))
	fmt.Fprintln(&b, style.MutedText.Render("hasn't been built yet."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, installUnavailableRows, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// viewNotice is the development notice (#175, notice.go) over the sky.
func (m InstallModel) viewNotice() string {
	return skyBlock(m.star, m.width, m.height, m.notice.view(m.now(), m.noticeDur()))
}

func (m InstallModel) viewConfirm() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "Ready to install")
	fmt.Fprintln(&b, style.MutedText.Render("Orbit's own installer runs inside the mission console —"))
	fmt.Fprintln(&b, style.MutedText.Render("watch it validate, pull the image, and start the containers"))
	fmt.Fprintln(&b, style.MutedText.Render("right here. If it needs configuration only you can provide,"))
	fmt.Fprintln(&b, style.MutedText.Render("it stops safely and asks first."))
	fmt.Fprintln(&b)
	// The path is the one word here whose length nobody chose.
	writeWrapped(&b, style.Tagline, m.targetDir, min(m.width-4, proseWidth))
	fmt.Fprintln(&b)
	m.confirm.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}
