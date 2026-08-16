package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type installState int

const (
	installStateProfile installState = iota
	installStateUnavailableProfile
	installStateConfirm
	installStateRunning
	installStateStaleVolume
)

// staleVolumeCheckTimeout bounds the pre-flight's one docker call. It is
// generous for `docker volume ls` and still short enough that an
// unreachable daemon costs a moment, not a wait — the check is advisory,
// so timing out simply means the screen never appears.
const staleVolumeCheckTimeout = 5 * time.Second

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

	profileSel int // 0 = Standard, 1 = AI, 2 = Full
	confirmSel int // 0 = Install now, 1 = Back
	volumeSel  int // 0 = Continue anyway, 1 = Back

	// staleVolumes carries the pre-flight's finding (issue #105): Orbit
	// database volumes this machine already has and this target cannot
	// prove it owns.
	staleVolumes []deploy.DatabaseVolume

	// checkVolumes is overridable in tests so they need no Docker
	// daemon — production code leaves it nil and gets the real check.
	checkVolumes func(context.Context, string) []deploy.DatabaseVolume

	run engineRun

	// Test seams, copied into the engine run at start; nil means real.
	seams engineRunSeams
}

// engineRunSeams bundles the overridable dependencies tests inject.
type engineRunSeams struct {
	prepareEngine  prepareEngineFunc
	prepareInstall prepareInstallFunc
	runHandoff     runHandoffFunc
	detect         detectFunc
	now            nowFunc
	prepareConfig  prepareConfigFunc
	startConfig    startConfigFunc
	adoptConfig    adoptConfigFunc
	prepareRepair  prepareRepairFunc
}

func (r engineRun) withSeams(s engineRunSeams) engineRun {
	r.prepareEngine = s.prepareEngine
	r.prepareInstall = s.prepareInstall
	r.runHandoff = s.runHandoff
	r.detect = s.detect
	r.now = s.now
	r.prepareConfig = s.prepareConfig
	r.startConfig = s.startConfig
	r.adoptConfig = s.adoptConfig
	return r
}

// NewInstallModel constructs the Install flow for targetDir.
func NewInstallModel(targetDir, version string) InstallModel {
	return InstallModel{targetDir: targetDir, version: version}
}

// Done, Succeeded, WantsMenu, SuccessURL and SuccessElapsed surface the
// engine run's outcome to AppModel.
func (m InstallModel) Outcome() flowOutcome { return outcomeOf(m.run) }

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
		ctx, cancel := context.WithTimeout(context.Background(), staleVolumeCheckTimeout)
		defer cancel()
		return staleVolumesMsg{volumes: check(ctx, targetDir)}
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
		m.star = starfield.New(resized.Width, resized.Height, 1)
	}
	if _, ok := msg.(tickMsg); ok {
		m.star = m.star.Advance()
		if m.state != installStateRunning {
			return m, tick()
		}
	}

	if found, ok := msg.(staleVolumesMsg); ok {
		// Only the profile screen is interrupted. The check is async, so
		// a quick hand can already be past it — and stopping someone
		// mid-confirm to report a pre-flight finding would be worse than
		// letting the engine give them the same news.
		if len(found.volumes) > 0 && m.state == installStateProfile {
			m.staleVolumes = found.volumes
			m.state = installStateStaleVolume
			m.volumeSel = 0
		}
		return m, nil
	}

	if m.state == installStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m, cmd
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		return m.handleKey(key)
	}
	return m, nil
}

func (m InstallModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
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
		if msg.Type == tea.KeyEnter || msg.Type == tea.KeyEsc {
			m.state = installStateProfile
		}
		return m, nil
	case installStateConfirm:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

// handleStaleVolumeKey keeps the pre-flight advisory. Continue anyway is
// always offered and always works: this screen reports what the engine
// is about to refuse, and a detection mistake here must never be able to
// stand between someone and an install.
func (m InstallModel) handleStaleVolumeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyUp, tea.KeyDown:
		m.volumeSel = 1 - m.volumeSel
		return m, nil
	case tea.KeyEnter:
		if m.volumeSel == 1 {
			return m, tea.Quit
		}
		m.state = installStateProfile
		return m, nil
	}
	return m, nil
}

func (m InstallModel) handleProfileKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyUp:
		m.profileSel = (m.profileSel - 1 + 3) % 3
		return m, nil
	case tea.KeyDown:
		m.profileSel = (m.profileSel + 1) % 3
		return m, nil
	case tea.KeyEnter:
		if m.profileSel != 0 {
			m.state = installStateUnavailableProfile
			return m, nil
		}
		m.state = installStateConfirm
		m.confirmSel = 0
		return m, nil
	}
	return m, nil
}

func (m InstallModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = installStateProfile
		return m, nil
	case tea.KeyUp, tea.KeyDown:
		m.confirmSel = 1 - m.confirmSel
		return m, nil
	case tea.KeyEnter:
		if m.confirmSel == 1 {
			m.state = installStateProfile
			return m, nil
		}
		m.state = installStateRunning
		m.run = newEngineRun("install", m.targetDir, "Install — Standard", m.version).withSeams(m.seams)
		var cmd tea.Cmd
		m.run, cmd = m.run.start(m.width, m.height)
		return m, cmd
	}
	return m, nil
}

// View implements tea.Model.
func (m InstallModel) View() string {
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
	fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("This machine already has an Orbit database"))
	fmt.Fprintln(&b)

	for _, v := range m.staleVolumes {
		line := v.Name
		if v.Project != "" {
			line += "  ·  from the " + v.Project + " deployment"
		}
		fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(line))
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, style.MutedText.Render("It was left by an earlier install in another directory, and"))
	fmt.Fprintln(&b, style.MutedText.Render("this one has none of the credentials that would prove it owns"))
	fmt.Fprintln(&b, style.MutedText.Render("that database. Orbit will not start over a database it cannot"))
	fmt.Fprintln(&b, style.MutedText.Render("prove belongs to it, so the installer will stop rather than"))
	fmt.Fprintln(&b, style.MutedText.Render("risk the data inside it."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("nothing has been changed, and nothing will be by this screen"))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Continue anyway", "Back"}, m.volumeSel)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m InstallModel) viewProfile() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Choose a deployment profile"))
	fmt.Fprintln(&b)

	profiles := []struct{ name, desc string }{
		{"Standard", "mail, documents, calendar"},
		{"AI", "adds a local model for search & suggestions"},
		{"Full", "AI features plus every optional service"},
	}
	for i, p := range profiles {
		fmt.Fprintln(&b, menuRow(p.name+"  ·  "+p.desc, i == m.profileSel))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("only Standard is available so far"))
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m InstallModel) viewUnavailableProfile() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("This profile isn't available yet"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("AI and Full need local-model configuration that"))
	fmt.Fprintln(&b, style.MutedText.Render("hasn't been built yet."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Back"}, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m InstallModel) viewConfirm() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Ready to install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("Orbit's own installer runs inside the mission console —"))
	fmt.Fprintln(&b, style.MutedText.Render("watch it validate, pull the image, and start the containers"))
	fmt.Fprintln(&b, style.MutedText.Render("right here. If it needs configuration only you can provide,"))
	fmt.Fprintln(&b, style.MutedText.Render("it stops safely and asks first."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render(m.targetDir))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Install now", "Back"}, m.confirmSel)
	return skyBlock(m.star, m.width, m.height, b.String())
}
