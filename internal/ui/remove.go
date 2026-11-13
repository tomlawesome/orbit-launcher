package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type removeState int

const (
	removeStateConfirm removeState = iota
	removeStateStandingDown
	removeStateDone
	removeStateFailed
	removeStateCancelled
	removeStateNotFound
	// removeStateOtherFolder is the dead end for a deployment whose app
	// container was started from another folder (#316).
	removeStateOtherFolder
)

// standDownResultMsg carries the outcome of the async StandDown call back
// into the bubbletea event loop.
type standDownResultMsg struct{ err error }

// RemoveModel is the Remove flow: confirm, stand down (automated,
// reversible), then an exact copy-pasteable, irreversible removal
// command that this program never runs itself — see
// design/mockups.html sections 09-11 and
// internal/deploy/removal_property_test.go for the enforced half of that
// promise. With no deployment detected it stops at the start: there is
// nothing to stand down, and a removal command only ever names a
// directory the launcher actually found (#205).
type RemoveModel struct {
	width, height int
	star          starfield.Model
	deployment    *deploy.Deployment
	state         removeState
	confirm       menu // removeConfirmRows
	after         menu // removeDoneRows or removeFailedRows, once the stand-down answers
	standDownErr  error
	copied        bool

	// own is what deploy.OwnFiles read from the folder as the done screen
	// was entered, after the stand-down: the box, Copy command and the
	// lines under the box are all drawn from it, so they cannot disagree
	// (#285). The zero Own (no .orbit-files) is the whole-folder command.
	own deploy.Own
	// listed is whether the folder had Orbit's own file list when the
	// flow opened: the confirm screen then promises a removal command for
	// the root and home folders too (#285). Read once, not on every frame.
	listed bool

	// frame is the working line's spinner frame, advanced on every tick.
	frame int

	// left is going back to the main menu: Esc, Cancel or Menu on the
	// flow's first screen, with nothing stood down (#210), or Esc or Menu
	// on the removed and failed screens after the stand-down (#225).
	left bool

	// targetDir is where AppModel looked; the no-deployment screen names
	// it. With a deployment, every command uses deployment.TargetDir.
	targetDir string

	// detectErr is deploy.Detect's error for targetDir: a .env-orbit that
	// exists but could not be read. The not-found state then says so
	// instead of "No Orbit deployment found" (#238).
	detectErr error

	// refusal is removeStateOtherFolder's screen: the folder the app
	// container was started from and its rows (#316, #334).
	refusal folderRefusal
	// carriedOn is the person's "Carry on from this folder" (#334): while
	// it is set, a running answer whose folder is still gone, the splash's
	// or Remove's own, is no mismatch. It lives as long as this flow.
	carriedOn bool

	// standDown is overridable in tests so they don't need a real Docker
	// daemon — production code always leaves this nil and gets
	// deploy.StandDown.
	standDown func(context.Context, string) error

	// installedAt is when Docker created the deployment's database
	// volume, zero until the lookup Init starts answers (or if it
	// cannot). lookupInstalledAt is the same kind of seam as standDown:
	// nil in production, which means deploy.InstalledAt.
	installedAt       time.Time
	lookupInstalledAt func(context.Context, *deploy.Deployment) time.Time

	// clipboard is where the OSC 52 copy sequence is written: the same kind
	// of seam as standDown, nil in production, which means os.Stdout. Tests
	// set it so they neither write to the real terminal nor go unchecked.
	clipboard io.Writer

	// runningRead reads the app container for Remove's own running check:
	// nil means no check and no wait. The app copies its reader here, as
	// for Install.
	runningRead func(context.Context, *deploy.Deployment) (deploy.RunningApp, error)
	// runningAnswered is set once that check has answered; awaiting is an
	// Enter on "Stand down Orbit" held until it does.
	runningAnswered bool
	awaiting        bool

	// signalQuit is a SignalQuitMsg that arrived during the stand-down:
	// the launcher quits as soon as the stand-down's result is in.
	signalQuit bool
}

// NewRemoveModel constructs the Remove flow for a detected deployment. A
// nil deployment means nothing was found, and the flow says so and
// offers only the way back.
func NewRemoveModel(d *deploy.Deployment) RemoveModel {
	m := RemoveModel{deployment: d, confirm: newMenu(removeConfirmRows...)}
	if d == nil {
		m.state = removeStateNotFound
	} else if own, err := deploy.OwnFiles(d.TargetDir); err == nil {
		m.listed = own.FileByFile()
	}
	return m
}

// removeOtherFolderBody is the refusal's first body line.
const removeOtherFolderBody = "Remove here would stop that folder's Orbit."

// refuseOtherFolder turns the flow into the screen that names the folder
// the container was started from (#316): a dead end, or with that folder
// gone the offer to carry on from this one (#334).
func (m RemoveModel) refuseOtherFolder(mm deploy.Mismatch) RemoveModel {
	m.state, m.refusal, m.carriedOn = removeStateOtherFolder, newFolderRefusal(mm), false
	m.awaiting = false
	return m
}

// carryOn is the row (#334): the flow goes on as from the container's own
// folder, to the confirmation with Init's install-date lookup and its own
// running check (#329), which a held Enter waits for as ever. It writes,
// relabels and stops nothing itself.
func (m RemoveModel) carryOn() (tea.Model, tea.Cmd) {
	m.state, m.carriedOn = removeStateConfirm, true
	m.runningAnswered, m.awaiting = false, false
	m.confirm = newMenu(removeConfirmRows...)
	return m, m.Init()
}

// removeRunningMsg is Remove's own running check answering for dir:
// otherFolder names the folder the app container was started from when
// that is not dir, and is empty for every other answer, a check that could
// not run or ran out of time included. gone and buildDiffers are the
// answer's Mismatch.FolderGone and BuildDiffers (#334).
type removeRunningMsg struct {
	dir, otherFolder   string
	gone, buildDiffers bool
}

// mismatch is the answer as the refusal names it.
func (r removeRunningMsg) mismatch() deploy.Mismatch {
	return deploy.Mismatch{Kind: deploy.MismatchFolder, Folder: r.otherFolder, FolderGone: r.gone, BuildDiffers: r.buildDiffers}
}

// removeChecking is the working line under the confirm while Enter waits
// for the running check.
const removeChecking = "checking where Orbit was started from"

// checkRunningCmd is Remove's own running check, nil with no reader or no
// deployment.
//
// The confirmation does not trust the splash's answer alone: that check
// may still be out, or may have given up without a word, and the
// stand-down runs `compose down` by project name, so it would stop an
// Orbit started from another folder (#329). Remove asks again when it
// opens, and Enter waits for this answer, never the splash's. A check that
// cannot run or runs out of time answers "", and the stand-down proceeds
// as every silent check lets its flow proceed.
func (m RemoveModel) checkRunningCmd() tea.Cmd {
	read, d, dir := m.runningRead, m.deployment, m.targetDir
	if read == nil || d == nil {
		return nil
	}
	return func() tea.Msg {
		msg := removeRunningMsg{dir: dir}
		if mm := runningMismatch(read, d, dir); mm.Kind == deploy.MismatchFolder {
			msg.otherFolder, msg.gone, msg.buildDiffers = mm.Folder, mm.FolderGone, mm.BuildDiffers
		}
		return msg
	}
}

const removeCopy = "Copy command"

var (
	removeConfirmRows = []string{"Stand down Orbit", "Cancel"}
	removeDoneRows    = []string{removeCopy, "Menu", "Exit"}
	removeFailedRows  = []string{"Menu", "Exit"}
)

// Outcome surfaces the flow result to AppModel: leaving, on the first
// screen or after the stand-down, returns to the menu; the Exit row and
// Ctrl-C quit instead (Ctrl-C and Esc do nothing while the stand-down runs).
func (m RemoveModel) Outcome() flowOutcome {
	if m.left {
		return backToMenu
	}
	return flowOutcome{}
}

func (m RemoveModel) standDownFunc() func(context.Context, string) error {
	if m.standDown != nil {
		return m.standDown
	}
	return deploy.StandDown
}

// Init implements tea.Model. It starts the read-only install-date
// lookup, and the running check when there is a reader; the confirm
// screen shows the date only once Docker answers.
func (m RemoveModel) Init() tea.Cmd {
	if m.state == removeStateOtherFolder {
		return nil // the refusal shows no date
	}
	lookup := installedAtCmd(m.lookupInstalledAt, m.deployment)
	check := m.checkRunningCmd()
	if check == nil {
		return lookup
	}
	return tea.Batch(lookup, check)
}

// Update implements tea.Model.
func (m RemoveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Build the sky, arm nothing: the one app-wide tick chain
		// SplashModel.Init started is still running and reaches this model
		// the moment it owns the screen. See AppModel.refreshSplash.
		m.width, m.height = msg.Width, msg.Height
		m.star = newSky(msg.Width, msg.Height)
		return m, nil

	case tickMsg:
		m.star = m.star.Advance()
		m.frame = nextFrame(m.frame)
		return m, tick()

	case installedAtMsg:
		m.installedAt = msg.at
		return m, nil

	case standDownResultMsg:
		if msg.err != nil {
			m.standDownErr = msg.err
			m.state = removeStateFailed
			m.after = newMenu(removeFailedRows...)
		} else {
			m.state = removeStateDone
			m.after = newMenu(removeDoneRows...)
			m.own, _ = deploy.OwnFiles(m.deployment.TargetDir) // its only error is no folder, which removalWords reports
		}
		if m.signalQuit {
			return m.handleKey(ctrlCPress)
		}
		return m, nil

	case runningResultMsg:
		// The running check answered after the flow opened. Only the
		// confirmation turns into the refusal; a stand-down in progress
		// is never interrupted (#227) and the screens after it show a
		// result. After carrying on, a folder still gone is what was
		// carried past; one that is back refuses (#334). An answer for
		// another folder is not this flow's.
		if m.state == removeStateConfirm && msg.dir == m.targetDir && msg.mismatch.Kind == deploy.MismatchFolder &&
			!(m.carriedOn && msg.mismatch.FolderGone && !msg.mismatch.BuildDiffers) {
			m = m.refuseOtherFolder(msg.mismatch)
		}
		return m, nil

	case removeRunningMsg:
		// Remove's own check answered. A folder refuses on the confirm,
		// waiting or not; a clean answer releases a held Enter.
		if msg.dir != m.targetDir {
			return m, nil
		}
		m.runningAnswered = true
		if m.state != removeStateConfirm {
			return m, nil
		}
		if msg.otherFolder != "" && !(m.carriedOn && msg.gone && !msg.buildDiffers) {
			return m.refuseOtherFolder(msg.mismatch()), nil
		}
		if m.awaiting {
			m.awaiting = false
			return m.startStandDown()
		}
		return m, nil

	case SignalQuitMsg:
		// The stand-down is not interrupted (see handleKey), but nobody
		// may be left to see its result, so the quit waits for it.
		if m.state == removeStateStandingDown {
			m.signalQuit = true
			return m, nil
		}
		return m.handleKey(ctrlCPress)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m RemoveModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// While the stand-down runs nothing leaves the screen: Ctrl-C would
	// quit and leave `docker compose down` running unseen, so it and
	// Esc do nothing, as in Repair (#227). The result brings keys back.
	if m.state == removeStateStandingDown {
		return m, nil
	}
	if isCtrlC(msg) {
		m.state = removeStateCancelled
		return m, tea.Quit
	}

	switch m.state {
	case removeStateNotFound:
		// The dead end: its one row and Esc both go back to the menu
		// (#205, 4a; #228).
		if _, chosen, back := newMenu(deadEndRows...).onScreen(msg); back || chosen == "Menu" {
			m.left = true
		}
		return m, nil
	case removeStateOtherFolder:
		var carry, leave bool
		m.refusal, carry, leave, _ = m.refusal.key(msg)
		if carry {
			return m.carryOn()
		}
		m.left = leave
		return m, nil
	case removeStateConfirm:
		return m.handleConfirmKey(msg)
	case removeStateDone, removeStateFailed:
		return m.handleDoneKey(msg)
	}
	return m, nil
}

func (m RemoveModel) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.confirm, chosen, back = m.confirm.onScreen(msg)
	switch {
	case back, chosen == "Cancel":
		m.left, m.awaiting = true, false
	case chosen == "":
		m.awaiting = false // a caret move drops a held Enter
	case chosen == "Stand down Orbit":
		if m.runningRead != nil && !m.runningAnswered {
			m.awaiting = true
			return m, nil
		}
		return m.startStandDown()
	}
	return m, nil
}

// startStandDown starts the stand-down, from Enter or from the running
// check's clean answer to a held Enter.
func (m RemoveModel) startStandDown() (tea.Model, tea.Cmd) {
	m.state = removeStateStandingDown
	targetDir := m.deployment.TargetDir
	standDown := m.standDownFunc()
	return m, func() tea.Msg {
		return standDownResultMsg{err: standDown(context.Background(), targetDir)}
	}
}

// handleDoneKey drives the two screens after the stand-down. Esc and
// the Menu row go back to the main menu, as after Repair (#225, #228);
// the Exit row quits.
func (m RemoveModel) handleDoneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.after, chosen, back = m.after.onScreen(msg)
	switch {
	case back, chosen == "Menu":
		m.left = true
		return m, nil
	case chosen == removeCopy:
		words, err := m.removalWords()
		if err != nil {
			return m, nil
		}
		m.copied = true
		return m, copyToClipboard(m.clipboardWriter(), strings.Join(words, " "))
	case chosen == "Exit":
		return m, tea.Quit
	}
	return m, nil
}

// clipboardWriter is where the copy sequence goes: os.Stdout unless a test
// supplied a writer.
func (m RemoveModel) clipboardWriter() io.Writer {
	if m.clipboard != nil {
		return m.clipboard
	}
	return os.Stdout
}

// copyToClipboard writes an OSC 52 escape sequence, which modern
// terminals (including over SSH) interpret as a clipboard-set request.
// Terminals that don't support it simply ignore the sequence. Written
// directly to stdout rather than via tea.Printf, which is documented as a
// no-op under the alt screen — the mode this program always runs in.
func copyToClipboard(w io.Writer, text string) tea.Cmd {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return func() tea.Msg {
		fmt.Fprintf(w, "\x1b]52;c;%s\x07", encoded)
		return nil
	}
}

// View implements tea.Model.
func (m RemoveModel) View() tea.View { return tea.NewView(m.view()) }

// view renders the screen's content.
func (m RemoveModel) view() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case removeStateNotFound:
		return m.viewNotFound()
	case removeStateConfirm:
		return m.viewConfirm()
	case removeStateStandingDown:
		return m.viewStandingDown()
	case removeStateDone:
		return m.viewDone()
	case removeStateFailed:
		return m.viewFailed()
	case removeStateOtherFolder:
		return m.refusal.view(m.star, m.width, m.height, removeOtherFolderBody)
	default:
		return ""
	}
}

// The flow screens speak the same starchart grammar as every other
// screen (design/DECISIONS.md): centred block, ⟡ mark and bold title,
// muted prose, individually-centred stacked menu, no keybind hints.

func (m RemoveModel) viewNotFound() string {
	if m.detectErr != nil {
		return viewUnreadableDeployment(m.star, m.width, m.height, m.targetDir, m.detectErr)
	}
	return viewNoDeployment(m.star, m.width, m.height, m.targetDir,
		"Remove acts only on a deployment the launcher has found.",
		"Start it in the directory that holds "+deploy.EnvFile+".")
}

func (m RemoveModel) viewConfirm() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "This stops Orbit and removes its containers")

	fmt.Fprintln(&b, style.Ink.Render(identityLine(m.deployment, m.installedAt, measure(m.width, proseWidth))))
	fmt.Fprintln(&b)
	// Each hand-broken line wraps on its own, so it stays one row where
	// it fits and breaks at spaces where it does not.
	prose := []string{
		"Your mail, documents, and configuration are not deleted",
		"by this step — they stay on disk, and the next screen",
		"shows exactly how to remove them if you choose to.",
	}
	// The root or home folder without Orbit's own file list gets no
	// removal of its files, so nothing is promised for it.
	if !m.listed && m.refusedFolder() != "" {
		prose = []string{prose[0], "by this step — they stay on disk."}
	}
	for _, line := range prose {
		writeWrappedIn(&b, style.MutedText, line, measure(m.width, proseWidth), textArea(m.width))
	}
	fmt.Fprintln(&b)
	m.confirm.write(&b)
	if m.awaiting {
		fmt.Fprintln(&b)
		b.WriteString(workingLine(m.frame, removeChecking, ""))
	}
	return skyBlock(m.star, m.width, m.height, b.String())
}

// removeCannotStopLine is the stand-down's second working line. It keeps
// Repair's "can't be stopped" (#227) but not Repair's reason: a
// stand-down deletes nothing and is undone by starting Orbit again, so
// stopping it halfway is not unsafe.
const removeCannotStopLine = "Please wait — this can't be cancelled. Nothing is being deleted."

func (m RemoveModel) viewStandingDown() string {
	return skyBlock(m.star, m.width, m.height, workingLine(m.frame, "standing down Orbit", removeCannotStopLine))
}

func (m RemoveModel) viewDone() string {
	var b strings.Builder
	writeInlineHeader(&b, style.SuccessText.Render(style.SymbolMark), "Orbit has been stood down")

	targetDir := m.deployment.TargetDir
	prose := measure(m.width, proseWidth)
	writeWrappedIn(&b, style.MutedText, "Containers and networks are stopped. Your files and data", prose, textArea(m.width))
	// The path is the one word here whose length and characters nobody
	// chose, so it is escaped for the terminal and cut across rows.
	for _, row := range wrapHard("volumes are still on disk at "+targetDir+" —", prose, m.width-4, folderRows) {
		fmt.Fprintln(&b, style.MutedText.Render(row))
	}
	writeWrappedIn(&b, style.MutedText, "nothing has been deleted.", prose, textArea(m.width))
	fmt.Fprintln(&b)
	// With Orbit's own file list the command names those files (#285).
	// Without it, the root or home folder gets only the data-volume
	// half: "sudo rm -rf" there would delete far more than Orbit.
	refused := m.refusedFolder()
	ownFiles := m.own.FileByFile()
	switch {
	case ownFiles:
		writeWrappedIn(&b, style.Tagline, "to remove Orbit from this machine, copy and run", prose, textArea(m.width))
	case refused != "":
		writeWrappedIn(&b, style.Tagline, "to remove Orbit's data volumes from this machine, copy and run", prose, textArea(m.width))
	default:
		writeWrappedIn(&b, style.Tagline, "to fully remove Orbit from this machine, copy and run", prose, textArea(m.width))
	}

	// The command is over 100 cells for a short path, and the box adds
	// four; a terminal cuts whatever is wider than it, and the cut end is
	// the half that deletes everything. Wrapped, it reads whole at 80
	// columns, and a word wider than the box is cut across rows (#271).
	// The box's outside edge is the console's, width-4: four cells for
	// its frame, so the text gets width-8. Both are laid out from the
	// same words, drawn as they are: a word holding a character the
	// terminal would have to escape is already quoted in $'...' form
	// with those escapes, so every word is printable, and the box and
	// Copy command's one-line form are the same bytes (#277 W2).
	words, _ := m.removalWords()
	lines := wrapShellCommand(words, m.width-8)

	// The rows after the box are laid out first, so the box knows how
	// many rows they need.
	var tail strings.Builder
	// errorWidth, not proseWidth: the box's caption reads at the box's measure.
	wide := measure(m.width, errorWidth)
	switch {
	case ownFiles:
		writeWrappedIn(&tail, style.DegradedText, "this deletes all mail, documents and configuration — it cannot be undone", wide, textArea(m.width))
		for _, row := range wrapHard(m.ownFilesLine(), prose, m.width-4, leftoverRows) {
			fmt.Fprintln(&tail, style.Tagline.Render(row))
		}
	case refused != "":
		writeWrappedIn(&tail, style.DegradedText, "this deletes all mail and documents — it cannot be undone", wide, textArea(m.width))
		writeWrappedIn(&tail, style.Tagline, "The folder itself is not removed: it is "+refused+", not a folder of Orbit's own. Orbit's files there stay for you to remove.", prose, textArea(m.width))
	default:
		writeWrappedIn(&tail, style.DegradedText, "this deletes all mail, documents and configuration, and everything else in the folder, backups included — it cannot be undone", wide, textArea(m.width))
	}
	// A .orbit-files that was there but refused whole: the command above
	// is the one for a folder without it, and the screen says why.
	if m.own.ManifestIgnored != nil {
		writeWrappedIn(&tail, style.Tagline, removeListUnread, prose, textArea(m.width))
	}
	fmt.Fprintln(&tail)
	// The copy row says so once it has copied; the menu's own label,
	// which Enter answers with, stays the same.
	done := m.after
	if m.copied {
		done = done.over(append([]string{removeCopy + style.Separator + "copied"}, removeDoneRows[1:]...))
	}
	done.write(&tail)

	// The box gets the rows the screen has left once every other row,
	// the menu included, has its place: a long folder at a narrow width
	// must never push Copy command, Menu and Exit off the screen. When
	// the box is short, its end is kept, not its start: the end is the
	// half that deletes the files (`sudo rm -rf …` and its rmdir tail),
	// the part that must read. One "…" row stands first for the rows dropped,
	// as the stopped screen's installer block keeps its newest lines
	// (fitTail); Copy command still carries the whole command (#277 L2).
	room := max(m.height-strings.Count(b.String(), "\n")-strings.Count(tail.String(), "\n")-2, 1)
	if len(lines) > room {
		lines = append([]string{"  …"}, lines[len(lines)-(room-1):]...)
	}
	boxW := 0
	for _, l := range lines {
		boxW = max(boxW, lipgloss.Width(l))
	}
	for _, l := range drawBox(lipgloss.NewStyle().Foreground(style.Border), boxW+4, lines) {
		fmt.Fprintln(&b, l)
	}
	b.WriteString(tail.String())
	return skyBlock(m.star, m.width, m.height, b.String())
}

// removalWords is the command the removed screen offers: Orbit's own
// files by name when the folder's .orbit-files was read (#285), else the
// whole removal, or only its data-volume half for a folder that is not
// Orbit's own (refusedFolder). Compose removes only this project's
// volumes, so that half is safe anywhere, and without it the leftover
// database volume would block every later install.
func (m RemoveModel) removalWords() ([]string, error) {
	words, err := deploy.RemovalCommandWords(m.deployment.TargetDir, m.own)
	if errors.Is(err, deploy.ErrRootFolder) || errors.Is(err, deploy.ErrHomeFolder) {
		return deploy.VolumeRemovalCommandWords(m.deployment.TargetDir)
	}
	return words, err
}

// refusedFolder names the deployment folder when it is the root or the
// home folder ("the root folder", "your home folder"), which is never
// itself removed; empty otherwise.
func (m RemoveModel) refusedFolder() string {
	_, err := deploy.RemovalCommandWords(m.deployment.TargetDir, deploy.Own{})
	switch {
	case errors.Is(err, deploy.ErrRootFolder):
		return "the root folder"
	case errors.Is(err, deploy.ErrHomeFolder):
		return "your home folder"
	}
	return ""
}

// removeListUnread is the line for a .orbit-files refused whole.
const removeListUnread = "Orbit's own file list here could not be read."

// leftoverRows is the most rows the line under the box takes, so many
// odd leftover names never push the menu off the screen.
const leftoverRows = 3

// ownFilesLine is the line under the file-by-file command's warning:
// what happens to the folder itself (owner, 2026-10-22, 20a and 21a).
// Up to three leftovers are named, then counted. When the folder could
// not be listed in full, what else is there is not known, and the line
// says only what rmdir does.
func (m RemoveModel) ownFilesLine() string {
	switch m.refusedFolder() {
	case "the root folder":
		return "The root folder itself is never removed."
	case "your home folder":
		return "Your home folder itself is never removed."
	}
	if m.own.ListingFailed != nil {
		return "The folder stays if anything else is still in it."
	}
	left := m.own.Leftovers
	if len(left) == 0 {
		return "The folder is removed too: nothing else is in it."
	}
	named := strings.Join(left[:min(len(left), 3)], ", ")
	if len(left) > 3 {
		named += fmt.Sprintf(" and %d more", len(left)-3)
	}
	return "Left in place for you: " + named + " — the folder stays while they are in it."
}

func (m RemoveModel) viewFailed() string {
	var b strings.Builder
	writeInlineHeader(&b, style.ErrorText.Render(style.SymbolFailure), "Stand-down stopped")
	var timedOut *deploy.StandDownTimeoutError
	if errors.As(m.standDownErr, &timedOut) {
		// Docker's words would say only that it was killed: the screen
		// gives the limit and the command to run by hand instead. The
		// words are drawn as they are, like the removed screen's box:
		// shellQuote already wrote any odd name in printable $'...' form,
		// so a second escaping pass would double its backslashes and show
		// a command that names another folder (#277 W2).
		writeWrappedIn(&b, style.Tagline, "The stack hasn't come down after "+limitWords(timedOut.Limit)+". Try again manually later with:", measure(m.width, errorWidth), textArea(m.width))
		for _, row := range wrapShellCommand(timedOut.Words, m.width-8) {
			fmt.Fprintln(&b, style.MutedText.Render(row))
		}
		fmt.Fprintln(&b)
	} else if m.standDownErr != nil {
		// Docker's words, wrapped so the reason is never cut at the
		// screen's edge. Compose adds a progress line per container
		// (about 36 for Orbit), which would push Menu and Exit off a
		// small terminal: the first line, which holds the reason, stays,
		// and only the last six after it, as on the engine failure
		// screen.
		lines := strings.Split(strings.TrimRight(m.standDownErr.Error(), "\n"), "\n")
		shown := append([]string{lines[0]}, tailLines(lines[1:], 6)...)
		writeWrappedIn(&b, style.Tagline, visibleLines(strings.Join(shown, "\n")), measure(m.width, errorWidth), textArea(m.width))
		fmt.Fprintln(&b)
	}
	m.after.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// limitWords names a time limit in whole minutes where it is one, else in
// whole seconds: "1 minute", "5 minutes", "90 seconds".
func limitWords(d time.Duration) string {
	unit, n := "second", int(d.Round(time.Second)/time.Second)
	if d >= time.Minute && d%time.Minute == 0 {
		unit, n = "minute", int(d/time.Minute)
	}
	if n != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s", n, unit)
}
