package ui

import (
	"context"
	"fmt"
	"os"
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
	installStateNotice
	installStateRunning
	installStateStaleVolume
)

// InstallModel is the Install flow: profile choice, the development
// notice, then the mission console — the engine's event stream rendered natively
// inside the TUI (see internal/ui/enginerun.go and design/mockups-v5.html
// section 02). Standard and Documents are wired; Agent and Full are
// visible but honestly say they aren't available yet rather than
// silently doing the wrong thing.
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
	volume  menu // installVolumeRows

	// memory is the machine's total memory as read when the flow
	// began; memoryKnown is false when it could not be read, and then no
	// row is ever "not enough" (#260).
	memory      uint64
	memoryKnown bool

	// requested is the profile the run asks for, set when the person
	// chooses a row and carried by the run and its retry.
	requested string

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

	// noVolumeCheck is set when the pre-flight is disabled
	// (AppModel.WithoutVolumeCheck): Enter then never waits for it.
	noVolumeCheck bool

	// volumesAnswered is set when the pre-flight's answer has arrived.
	// Until then Enter on the profile screen waits (awaiting holds the
	// row it chose) and the screen shows a working line. The check's own
	// 5 s limit ends the wait: a timed-out check reports no volumes.
	volumesAnswered bool
	awaiting        string

	// frame is the working line's spinner frame, advanced on the shared tick.
	frame int

	// notice is the development notice (#175) between the profile
	// screen and the run; rebuilt fresh each time Standard is chosen.
	notice noticeModel

	// noticeDuration is the notice's reading countdown; zero means
	// noticeDefaultDuration. Copied from AppModel.flowNoticeDuration so
	// tests can shorten it.
	noticeDuration time.Duration

	run engineRun

	// collected is the guided configuration this flow has collected,
	// handed to every run it starts (collectedConfig). It lives as long
	// as the flow, so Install again asks nothing; a new flow asks again.
	collected *collectedConfig

	// startedAt is when the engine run began: the success screen reads
	// the claim link from the app's logs since then (#259).
	startedAt time.Time

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
	// listFolder reads the folder for the stopped screen's listing;
	// nil is os.ReadDir. folderLimit is how long the screen waits for
	// it; zero is folderReadLimit.
	listFolder  func(dir string) ([]os.DirEntry, error)
	folderLimit time.Duration
}

// withSend gives the run its way back into the event loop.
func (r engineRun) withSend(send func(tea.Msg)) engineRun {
	r.send = send
	return r
}

// withProfile makes the run request a deployment profile.
func (r engineRun) withProfile(profile string) engineRun {
	r.profile = profile
	return r
}

func (r engineRun) withSeams(s engineRunSeams) engineRun {
	r.engineRunSeams = s
	return r
}

// NewInstallModel constructs the Install flow for targetDir.
func NewInstallModel(targetDir, version string) InstallModel {
	memory, known := deploy.HostMemory()
	return InstallModel{
		targetDir:   targetDir,
		version:     version,
		profile:     newMenu(installProfiles...),
		volume:      newMenu(installVolumeRows...),
		memory:      memory,
		memoryKnown: known,
		collected:   &collectedConfig{},
	}
}

// The profile screen's rows (#247). Standard and Documents (Orbit's
// "processing" profile, #260) start a run; Agent and Full, which have
// no profile name (they need a local model Orbit doesn't use yet), lead
// to the honest dead end. The menu holds the names; the table draws the
// rest, and the status column is worked out from the machine's memory
// (installProfile.status).
const (
	installStandard  = "Standard"
	installDocuments = "Documents"
)

// installProfile is a row. profile is the name the engine is asked for
// (deploy.ProfileEnv); empty means the row is not yet available.
type installProfile struct{ name, adds, profile string }

var installProfileTable = []installProfile{
	{installStandard, "mail, documents, calendar", deploy.ProfileStandard},
	{installDocuments, "adds text extraction from attachments", deploy.ProfileDocuments},
	{"Agent", "adds a local model for search & suggestions", ""},
	{"Full", "documents extraction and the local model", ""},
}

// The status column's words.
const (
	installAvailable = "available"
	installNotYet    = "not yet"
	installNotEnough = "not enough"
)

// shortOfMemory is whether the machine is known to have less memory than
// the row needs. Standard is never short (the launcher doesn't block the
// baseline, as Orbit's installer doesn't), and a machine whose memory
// could not be read is never short.
func (m InstallModel) shortOfMemory(p installProfile) bool {
	if p.profile == "" || p.profile == deploy.ProfileStandard {
		return false
	}
	return m.memoryKnown && m.memory < deploy.ProfileNeed(p.profile)
}

// status is the row's status column: "available" for Standard, the
// memory a profile adds over Standard ("+1 GB"), "not enough" when the
// machine is short of what the profile needs in all, and "not yet" for
// a row with no profile behind it.
func (m InstallModel) status(p installProfile) string {
	switch {
	case p.profile == "":
		return installNotYet
	case p.profile == deploy.ProfileStandard:
		return installAvailable
	case m.shortOfMemory(p):
		return installNotEnough
	}
	extra := deploy.ProfileNeed(p.profile) - deploy.ProfileNeed(deploy.ProfileStandard)
	return fmt.Sprintf("+%d GB", extra>>30)
}

// canChoose is whether Enter on the row does anything: the baseline
// always, a profile row unless the machine is short of memory.
func (m InstallModel) canChoose(p installProfile) bool {
	return p.profile != "" && !m.shortOfMemory(p)
}

// memoryReason is the one line under the table when a row is "not
// enough"; empty when no row is. It names the first such row.
func (m InstallModel) memoryReason() string {
	for _, p := range installProfileTable {
		if m.shortOfMemory(p) {
			return fmt.Sprintf("%s needs %d GB of memory in all and this machine has %d GB.",
				p.name, deploy.ProfileNeed(p.profile)>>30, m.memory>>30)
		}
	}
	return ""
}

var installProfiles = func() []string {
	names := make([]string, len(installProfileTable))
	for i, p := range installProfileTable {
		names[i] = p.name
	}
	return names
}()

// Table geometry at width >= installTableMin: two cells of caret column,
// the name column, a space, the adds column, a space, the availability
// column.
const (
	installNameCol  = 10
	installAddsCol  = 43
	installAvailCol = 10
	installTableW   = 2 + installNameCol + 1 + installAddsCol + 1 + installAvailCol
	installTableMin = installTableW + 1
)

var (
	installVolumeRows = []string{"Continue anyway", "Cancel"}

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
		m.frame = nextFrame(m.frame)
		if m.state != installStateRunning {
			return m, tick()
		}
	}

	// The notice's countdown redraws on its own one-second chain. A
	// tick from an earlier visit (Back, then Standard again)
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
		m.volumesAnswered = true
		chosen := m.awaiting
		m.awaiting = ""
		// Only the profile screen is interrupted. The check is async, so
		// a quick hand can already be past it — and stopping someone
		// mid-notice to report a pre-flight finding would be worse than
		// letting the engine give them the same news.
		if len(found.volumes) > 0 && m.state == installStateProfile {
			m.staleVolumes = found.volumes
			m.state = installStateStaleVolume
			m.volume = newMenu(installVolumeRows...)
			return m, nil
		}
		// None found: a waiting Enter carries on as it would have.
		if chosen != "" && m.state == installStateProfile {
			return m.chooseProfile(chosen)
		}
		return m, nil
	}

	if m.state == installStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		if m.run.Done {
			// The flow is over: no later run will be seeded from the
			// answers, so they go now rather than at exit.
			m.collected.release()
		}
		return m, cmd
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
		m.awaiting = ""
	case chosen == "": // a move, or a key the menu does not use
		// A move drops a waiting Enter: the row it chose is no longer
		// the one under the cursor.
		m.awaiting = ""
	case m.shortOfMemory(m.rowNamed(chosen)):
		// "not enough": the caret rests here but Enter does nothing, and
		// the line under the table says why.
		m.awaiting = ""
	case m.checkVolumes != nil && !m.volumesAnswered && !m.noVolumeCheck:
		// The pre-flight has not answered: wait for it rather than
		// moving on past a finding it is about to report.
		m.awaiting = chosen
	default:
		return m.chooseProfile(chosen)
	}
	return m, nil
}

// rowNamed is the table row called name; the zero row if none is.
func (m InstallModel) rowNamed(name string) installProfile {
	for _, p := range installProfileTable {
		if p.name == name {
			return p
		}
	}
	return installProfile{}
}

// requestedRow is the name of the row whose profile the run asks for,
// for the console's title; Standard when none was chosen.
func (m InstallModel) requestedRow() string {
	for _, p := range installProfileTable {
		if p.profile != "" && p.profile == m.requested {
			return p.name
		}
	}
	return installStandard
}

// chooseProfile acts on the row Enter chose on the profile screen.
func (m InstallModel) chooseProfile(chosen string) (tea.Model, tea.Cmd) {
	if p := m.rowNamed(chosen); m.canChoose(p) {
		// A profile row opens the development notice (#175) directly
		// (#248), fresh every time: going back and coming again restarts
		// the countdown and closes the scroll gate.
		m.requested = p.profile
		m.state = installStateNotice
		m.notice = newNotice(m.now(), m.width, m.height)
		return m, noticeTick(m.notice.start)
	}
	m.state = installStateUnavailableProfile
	return m, nil
}

// handleNoticeKey wires the notice: Esc or Back returns to the
// profile screen, a correct phrase starts the run, nothing else leaves.
func (m InstallModel) handleNoticeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var out noticeOutcome
	m.notice, out = m.notice.handleKey(msg, m.now, m.noticeDur())
	switch out {
	case noticeBack:
		m.state = installStateProfile
		return m, nil
	case noticeAccept:
		m.state = installStateRunning
		return m.startRun()
	}
	return m, nil
}

// startRun builds and starts the engine run.
func (m InstallModel) startRun() (InstallModel, tea.Cmd) {
	m.run = newEngineRun(deploy.ActionInstall, m.targetDir, "Install — "+m.requestedRow(), m.version).withProfile(m.requested).withSeams(m.seams).withSend(m.send)
	m.run.collected = m.collected
	var cmd tea.Cmd
	m.run, cmd = m.run.start(m.width, m.height)
	// The console's own start, so the clock is read once (a later
	// restart after in-console configuration keeps this first one).
	m.startedAt = m.run.console.startedAt
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
// volume name they read here; only a rune that isn't printable is shown
// as its escape (#272).
func (m InstallModel) viewStaleVolume() string {
	var b strings.Builder
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark), "This machine already has an Orbit database")

	for _, v := range m.staleVolumes {
		line := visibleText(v.Name)
		if v.Project != "" {
			// Named as the Compose label it is, not as "from the
			// <project> deployment": since Orbit #999/#1043 every
			// install declares the same project, so that phrasing
			// read as if it identified one install when it never
			// could. The directory sentence below is what actually
			// tells them apart.
			line += style.Separator + "Compose project " + visibleText(v.Project)
		}
		fmt.Fprintln(&b, style.Ink.Render(line))
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
	// The folder is the one word here whose length and characters nobody
	// chose: it is escaped so a CR or LF in it cannot forge a row, and
	// cut across rows rather than off the screen's edge (#248).
	for _, row := range wrapHard("Orbit installs into "+m.targetDir, measure(m.width, proseWidth), m.width-4, folderRows) {
		fmt.Fprintln(&b, style.Ink.Render(row))
	}
	fmt.Fprintln(&b)
	m.writeProfileTable(&b)
	if reason := m.memoryReason(); reason != "" {
		fmt.Fprintln(&b)
		// Padded to the table's width, so the sky centres it with the
		// table rather than on its own.
		w := installTableW
		if m.width < installTableMin {
			w = measure(m.width, proseWidth)
		}
		for _, row := range wrapWordsIn(reason, w-2, textArea(m.width)-2) {
			fmt.Fprintln(&b, "  "+style.Tagline.Render(row)+strings.Repeat(" ", max(0, w-2-len([]rune(row)))))
		}
	}
	if m.awaiting != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, workingLine(m.frame, "Checking for an old Orbit database…", ""))
	}
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m InstallModel) viewUnavailableProfile() string {
	var b strings.Builder
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark), "This profile isn't available yet")
	fmt.Fprintln(&b, style.MutedText.Render("Agent and Full need a local model, which Orbit"))
	fmt.Fprintln(&b, style.MutedText.Render("doesn't use yet."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, installUnavailableRows, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// viewNotice is the development notice (#175, notice.go) over the sky.
func (m InstallModel) viewNotice() string {
	return skyBlock(m.star, m.width, m.height, m.notice.view(m.now(), m.noticeDur()))
}

// writeProfileTable draws the profile choice as a table at 68 columns and
// over, and as two rows per profile below. Every row is padded to the
// block's width so the sky's per-line centring keeps the block one unit.
func (m InstallModel) writeProfileTable(b *strings.Builder) {
	pad := func(styled string, used, w int) string {
		return styled + strings.Repeat(" ", max(0, w-used))
	}
	caret := func(sel bool) string {
		if sel {
			return style.MenuCaret.Render(style.SymbolSelected) + " "
		}
		return "  "
	}
	rowStyle := func(i int, p installProfile) lipgloss.Style {
		switch {
		case i == m.profile.sel:
			return style.Ink
		case m.canChoose(p):
			return style.MutedText
		}
		return style.Tagline
	}
	if m.width >= installTableMin {
		for i, p := range installProfileTable {
			st := rowStyle(i, p)
			status := m.status(p)
			line := caret(i == m.profile.sel) +
				pad(st.Render(p.name), len(p.name), installNameCol) + " " +
				pad(st.Render(p.adds), len([]rune(p.adds)), installAddsCol) + " " +
				pad(st.Render(status), len(status), installAvailCol)
			fmt.Fprintln(b, line)
		}
		return
	}
	w := measure(m.width, proseWidth)
	for i, p := range installProfileTable {
		if i > 0 {
			fmt.Fprintln(b, pad("", 0, w))
		}
		st := rowStyle(i, p)
		head := p.name + style.Separator + m.status(p)
		fmt.Fprintln(b, pad(caret(i == m.profile.sel)+st.Render(head), 2+len([]rune(head)), w))
		for _, r := range wrapWordsIn(p.adds, w-2, textArea(m.width)-2) {
			fmt.Fprintln(b, pad("  "+st.Render(r), 2+len([]rune(r)), w))
		}
	}
}
