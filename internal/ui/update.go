package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/release"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type updateState int

const (
	updateStateNotFound updateState = iota
	updateStateConfirm
	updateStateRunning
	// updateStateOtherFolder is the dead end for a deployment whose app
	// container was started from another folder (#316).
	updateStateOtherFolder
	// updateStateNewer is the dead end for an installed Orbit newer than
	// the current release: install.sh does not downgrade (#336).
	updateStateNewer
	// updateStateCurrent is the dead end for an installed Orbit that is
	// already the current release: Update is not a refresh, Repair is
	// (#336, owner 17b).
	updateStateCurrent
)

// UpdateModel is the Update flow: confirm, then the same mission
// console the Install flow uses (see internal/ui/enginerun.go), with
// the engine run flagged --update. install.sh is idempotent against an
// existing deployment: it pulls the latest image, refreshes deployment
// assets, and runs its own configuration migration, preserving the
// existing .env-orbit values.
//
// The confirm screen shows the plan, then the run applies exactly that
// plan (#336): it names the installed and the target Orbit once the
// release lookup answers, and when the target came from the latest
// release the run is pinned to it (ORBIT_CHANNEL=<version>), so
// install.sh verifies and installs the release shown or refuses. Enter
// before the lookup answers waits for it, as Remove's waits for its
// running check; an unknown answer starts nothing on its own.
type UpdateModel struct {
	width, height int
	star          starfield.Model
	targetDir     string
	version       string
	deployment    *deploy.Deployment
	detectErr     error // deploy.Detect's error for targetDir; see RemoveModel
	state         updateState
	confirm       menu // updateConfirmRows

	// refusal is updateStateOtherFolder's screen: the folder the app
	// container was started from and its rows (#316, #334).
	refusal folderRefusal
	// carriedOn is the person's "Carry on from this folder" (#334): while
	// it is set, a running answer whose folder is still gone is no
	// mismatch. It lives as long as this flow.
	carriedOn bool

	// left is Esc, Cancel or Menu on the flow's first screen: back to the
	// main menu with nothing started (#210).
	left bool

	// installedAt and lookupInstalledAt: see RemoveModel.
	installedAt       time.Time
	lookupInstalledAt func(context.Context, *deploy.Deployment) time.Time

	// The release the update would install (#336). lookupTarget is the
	// seam (nil: no lookup and no network, as the splash's update check);
	// targetKnown is set once it has answered, whichever way, with
	// targetErr its failure. awaiting is a held Enter waiting for that
	// answer; frame is its working line's spinner frame.
	target       release.Target
	targetErr    error
	targetKnown  bool
	awaiting     bool
	frame        int
	lookupTarget func(context.Context) (release.Target, error)

	run engineRun

	seams engineRunSeams

	// send is the run's way back into the event loop, for the engine
	// stream reader; set by AppModel from the program's sender.
	send func(tea.Msg)
}

// NewUpdateModel constructs the Update flow for a detected deployment. A
// nil deployment means Update was chosen with nothing installed here —
// the model honestly says so rather than pretending there's something to
// update.
func NewUpdateModel(d *deploy.Deployment, targetDir, version string) UpdateModel {
	state := updateStateConfirm
	if d == nil {
		state = updateStateNotFound
	}
	return UpdateModel{deployment: d, targetDir: targetDir, version: version, state: state, confirm: newMenu(updateConfirmRows...)}
}

var updateConfirmRows = []string{"Update Orbit", "Cancel"}

// deadEndRows are a dead end's one row, Update's and Remove's not-found
// screens: back to the main menu (#228).
var deadEndRows = []string{"Menu"}

// updateOtherFolderBody is the refusal's first body line.
const updateOtherFolderBody = "Update here would act on that folder's Orbit, and Orbit's installer refuses it."

// refuseOtherFolder turns the flow into the screen that names the folder
// the container was started from (#316): a dead end, or with that folder
// gone the offer to carry on from this one (#334). A held Enter is
// dropped: it agreed to the confirmation the refusal replaced, so a
// carry-on needs a fresh one.
func (m UpdateModel) refuseOtherFolder(mm deploy.Mismatch) UpdateModel {
	m.state, m.refusal, m.carriedOn, m.awaiting = updateStateOtherFolder, newFolderRefusal(mm), false, false
	return m
}

// carryOn is the row (#334): the flow goes on as from the container's own
// folder, to the confirmation and its install-date lookup. It writes,
// relabels and asks Docker nothing itself.
func (m UpdateModel) carryOn() (tea.Model, tea.Cmd) {
	m.state, m.carriedOn = updateStateConfirm, true
	return m, m.Init()
}

// Outcome surfaces the flow's result to AppModel: back to the menu when
// the person left on the first screen, otherwise the engine run's.
func (m UpdateModel) Outcome() flowOutcome {
	if m.left {
		return backToMenu
	}
	return outcomeOf(m.run)
}

// Init implements tea.Model. On the confirm screen it starts the
// read-only install-date lookup, as RemoveModel.Init does, and the
// release lookup when there is one.
func (m UpdateModel) Init() tea.Cmd {
	if m.state != updateStateConfirm {
		return nil
	}
	return tea.Batch(installedAtCmd(m.lookupInstalledAt, m.deployment), m.targetCmd())
}

// targetMsg is the release lookup's answer.
type targetMsg struct {
	target release.Target
	err    error
}

// targetCmd is the release lookup, nil with no seam or once answered.
// The lookup bounds its own call (release.ResolveTarget).
func (m UpdateModel) targetCmd() tea.Cmd {
	lookup := m.lookupTarget
	if lookup == nil || m.targetKnown {
		return nil
	}
	return func() tea.Msg {
		target, err := lookup(context.Background())
		return targetMsg{target: target, err: err}
	}
}

// updateChecking is the working line under the confirm while Enter waits
// for the release lookup.
const updateChecking = "checking which Orbit release is current"

// answerTarget takes the release lookup's answer. Only the confirm
// screen takes it: once the run has started its pin is fixed, and a flow
// that was left starts nothing. Installed newer than the target, or the
// target itself, is a dead end, even for a held Enter; a held Enter starts the run on a known answer and is dropped
// on an unknown one, so the next Enter runs unpinned.
func (m UpdateModel) answerTarget(t targetMsg) (tea.Model, tea.Cmd) {
	if m.state != updateStateConfirm || m.left {
		return m, nil
	}
	m.target, m.targetErr, m.targetKnown = t.target, t.err, true
	held := m.awaiting
	m.awaiting = false
	target := m.targetVersion()
	switch {
	case target != "" && target == m.installedVersion():
		m.state = updateStateCurrent
		return m, nil
	case target != "" && release.NewerThan(m.installedVersion(), target):
		m.state = updateStateNewer
		return m, nil
	}
	if held && target != "" {
		return m.startRun()
	}
	return m, nil
}

// targetVersion is the release the lookup named, "" while it has not
// answered or could not say.
func (m UpdateModel) targetVersion() string {
	if !m.targetKnown || m.targetErr != nil {
		return ""
	}
	return m.target.Version
}

// installedVersion is the deployment's recorded Orbit release, "" when
// none is recorded. Only the confirm screen and its newer dead end ask,
// and NewUpdateModel opens those only with a deployment.
func (m UpdateModel) installedVersion() string { return m.deployment.Version }

// Update implements tea.Model.
func (m UpdateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// See InstallModel.Update: neither message is consumed here, a resize
	// arms no tick chain, and the engine run re-arms the one chain while
	// it owns the screen.
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
		m.star = newSky(resized.Width, resized.Height)
	}
	if _, ok := msg.(tickMsg); ok {
		m.star = m.star.Advance()
		m.frame = nextFrame(m.frame)
		if m.state != updateStateRunning {
			return m, tick()
		}
	}

	// The lookup may answer after the run has started; it is this
	// model's message, never the engine run's.
	if at, ok := msg.(installedAtMsg); ok {
		m.installedAt = at.at
		return m, nil
	}
	if t, ok := msg.(targetMsg); ok {
		return m.answerTarget(t)
	}

	// The running check may answer after the flow has opened. Before the
	// run starts, another folder's container turns the confirmation into
	// the refusal; once it has, the installer's own refusal is the net.
	// After carrying on, a folder still gone is what was carried past; one
	// that is back refuses (#334). An answer for another folder is not
	// this flow's.
	if r, ok := msg.(runningResultMsg); ok {
		if m.state == updateStateConfirm && r.dir == m.targetDir && r.mismatch.Kind == deploy.MismatchFolder &&
			!(m.carriedOn && r.mismatch.FolderGone && !r.mismatch.BuildDiffers) {
			m = m.refuseOtherFolder(r.mismatch)
		}
		return m, nil
	}

	if m.state == updateStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
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

func (m UpdateModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(msg) {
		return m, tea.Quit
	}
	switch m.state {
	case updateStateNotFound, updateStateNewer, updateStateCurrent:
		// The dead end: its one row and Esc both go back to the menu,
		// and no other key does anything (#228).
		if _, chosen, back := newMenu(deadEndRows...).onScreen(msg); back || chosen == "Menu" {
			m.left = true
		}
		return m, nil
	case updateStateOtherFolder:
		var carry, leave bool
		m.refusal, carry, leave = m.refusal.key(msg)
		if carry {
			return m.carryOn()
		}
		m.left = leave
		return m, nil
	case updateStateConfirm:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

func (m UpdateModel) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.confirm, chosen, back = m.confirm.onScreen(msg)
	switch {
	case back, chosen == "Cancel":
		m.left, m.awaiting = true, false
	case chosen == "":
		m.awaiting = false // a caret move drops a held Enter, as on Remove
	case chosen == "Update Orbit":
		if !m.targetKnown && m.lookupTarget != nil {
			m.awaiting = true
			return m, nil
		}
		return m.startRun()
	}
	return m, nil
}

// startRun starts the update, from Enter or from the release lookup's
// known answer to a held Enter, pinned to the target when the lookup
// says the run must be.
func (m UpdateModel) startRun() (tea.Model, tea.Cmd) {
	m.state, m.awaiting = updateStateRunning, false
	// The title keeps the plan named while it is applied: the release
	// when the lookup named one, "Update" when it could not. Only an
	// address names the deployment; without one, "deployment detected"
	// adds nothing.
	target := m.targetVersion()
	title := "Update"
	if target != "" {
		title = "Update to " + shownVersion(target)
	}
	if m.deployment != nil && m.deployment.AppURL != "" {
		title += " — " + deploymentIdentity(m.deployment)
	}
	m.run = newEngineRun(deploy.ActionUpdate, m.targetDir, title, m.version).withSeams(m.seams).withSend(m.send)
	if target != "" && m.target.Pin {
		m.run = m.run.withChannel(target)
	}
	var cmd tea.Cmd
	m.run, cmd = m.run.start(m.width, m.height)
	return m, cmd
}

// View implements tea.Model.
func (m UpdateModel) View() tea.View { return tea.NewView(m.view()) }

// view renders the screen's content.
func (m UpdateModel) view() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case updateStateNotFound:
		return m.viewNotFound()
	case updateStateConfirm:
		return m.viewConfirm()
	case updateStateRunning:
		return m.run.view(m.width, m.height)
	case updateStateOtherFolder:
		return m.refusal.view(m.star, m.width, m.height, updateOtherFolderBody)
	case updateStateNewer:
		return m.viewNewer()
	case updateStateCurrent:
		return m.viewCurrent()
	}
	return ""
}

// The flow screens speak the same starchart grammar as every other
// screen (design/DECISIONS.md): centred block, ⟡ mark and bold title,
// muted prose, individually-centred stacked menu, no keybind hints.

func (m UpdateModel) viewNotFound() string {
	if m.detectErr != nil {
		return viewUnreadableDeployment(m.star, m.width, m.height, m.targetDir, m.detectErr)
	}
	return viewNoDeployment(m.star, m.width, m.height, m.targetDir,
		"There's nothing to update in this directory —",
		"Install is the way to get into Orbit first.")
}

func (m UpdateModel) viewConfirm() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), m.confirmTitle())

	prose := measure(m.width, proseWidth)
	fmt.Fprintln(&b, style.Ink.Render(identityLine(m.deployment, m.installedAt, prose)))
	if row := m.releaseRow(); row != "" {
		writeWrappedIn(&b, style.MutedText, row, prose, textArea(m.width))
	}
	if image := m.deploymentImage(); image != "" {
		fmt.Fprintln(&b, style.Tagline.Render(endCut(visibleText(image), prose)))
	}
	fmt.Fprintln(&b)
	// Each line wraps on its own, as Remove's do (#271), but at the
	// screen's edge rather than proseWidth: the second line is 60 cells
	// and stays one row from 64 columns up, as it always has (#274).
	edge := measure(m.width, m.width)
	writeWrappedIn(&b, style.MutedText, "The update runs inside the mission console, right here.", edge, textArea(m.width))
	writeWrappedIn(&b, style.MutedText, "Your existing configuration is preserved. Nothing is deleted.", edge, textArea(m.width))
	fmt.Fprintln(&b)
	m.confirm.write(&b)
	if m.awaiting {
		fmt.Fprintln(&b)
		b.WriteString(workingLine(m.frame, updateChecking, ""))
	}
	return skyBlock(m.star, m.width, m.height, b.String())
}

// shownVersion is a version as a screen shows it: escaped, since the
// installed one is read from the deployment's file, and cut to 10 cells
// so every title fits in 48.
func shownVersion(v string) string { return endCut(visibleText(v), 10) }

// confirmTitle names the plan once the release is known: from the
// installed release to the target, or just to the target when none is
// recorded. Until then, and when the target cannot be read, it is the
// title that already says what Update does.
func (m UpdateModel) confirmTitle() string {
	target := m.targetVersion()
	if target == "" {
		return "Pull the latest Orbit and update this deployment"
	}
	if installed := m.installedVersion(); installed != "" {
		return "Update Orbit from " + shownVersion(installed) + " to " + shownVersion(target)
	}
	return "Update Orbit to " + shownVersion(target)
}

// releaseRow says what the title cannot: no recorded version, and a
// release lookup that could not answer. Nothing while the lookup runs.
func (m UpdateModel) releaseRow() string {
	if !m.targetKnown {
		return ""
	}
	installed := "Version not recorded"
	if v := m.installedVersion(); v != "" {
		installed = "On " + shownVersion(v)
	}
	switch {
	case m.targetVersion() == "":
		return installed + style.Separator + "current release couldn't be read"
	case m.installedVersion() == "":
		return installed
	}
	return ""
}

// viewNewer is the dead end for an installed Orbit newer than the
// current release, in the no-deployment grammar: Orbit's installer
// refuses to downgrade, so Update is not offered (#336).
func (m UpdateModel) viewNewer() string {
	return m.viewDeadEnd("This Orbit is newer than the current release",
		shownVersion(m.installedVersion())+" is installed; the current stable release is "+shownVersion(m.targetVersion())+".",
		"Orbit's installer doesn't downgrade.")
}

// viewCurrent is the dead end for an installed Orbit that is already the
// current release, in viewNewer's grammar: there is nothing to update
// to, and Repair is what checks or fixes a deployment (#336, owner 17b).
func (m UpdateModel) viewCurrent() string {
	return m.viewDeadEnd("Orbit "+shownVersion(m.installedVersion())+" is already the current release",
		"There is no newer release to update to.",
		"To check or fix this deployment, use Repair.")
}

// viewDeadEnd is a release dead end: the accent mark and title, its
// muted rows, the faint promise, and the one Menu row.
func (m UpdateModel) viewDeadEnd(title string, rows ...string) string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), title)
	// Every row runs to errorWidth, as the other-folder refusal's do: a
	// fact row is one line at 80 columns and wraps at spaces at 50.
	limit, edge := measure(m.width, errorWidth), textArea(m.width)
	for _, row := range rows {
		writeWrappedIn(&b, style.MutedText, row, limit, edge)
	}
	writeWrappedIn(&b, style.Tagline, otherFolderUntouched, limit, edge)
	fmt.Fprintln(&b)
	writeStackedMenu(&b, deadEndRows, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// deploymentImage is the deployment's image reference without its
// digest pin — the digest is engine bookkeeping, not something a
// person scans a confirmation screen for.
func (m UpdateModel) deploymentImage() string {
	if m.deployment == nil || m.deployment.Image == "" {
		return ""
	}
	image, _, _ := strings.Cut(m.deployment.Image, "@")
	return image
}
