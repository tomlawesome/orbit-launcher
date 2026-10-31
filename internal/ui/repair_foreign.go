package ui

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// Repair stops before the engine starts when .env-orbit or a file under
// .orbit-secrets belongs to another account (#265): the engine can read
// neither its contents nor restore it, so a diagnosis would only report
// what the launcher can say plainly here, with the command that gives
// the files back.

// repairForeignMsg is a run start stopped by files another account
// owns; own is the check's answer.
type repairForeignMsg struct{ own deploy.Ownership }

// foreignMenu is the stop screen's rows: Diagnose again is what the
// screen tells the person to do once the files are back (#277).
var foreignMenu = []string{removeCopy, "Diagnose again", "Menu", "Exit"}

// startDiagnosis is startRun for a run start (Init, "Diagnose again"):
// the folder's name is checked first, by RepairCommand's own rule, so
// nothing in a control-named folder is read and it gets the name refusal
// (#277 W3). The foreignOwned check runs next, inside the same command,
// so the screen is never held up by it and each start asks afresh.
// Foreign files stop the start; a check that fails does not, as the
// engine's own diagnosis still reads what it can. A nil seam checks no
// ownership (the c9 harness).
func (m RepairModel) startDiagnosis(mode deploy.RepairMode) tea.Cmd {
	run := m.startRun(mode)
	check := m.foreignOwned
	targetDir, flow := m.targetDir, m.flow
	return func() tea.Msg {
		if err := deploy.CheckFolderName(targetDir); err != nil {
			return repairReadyMsg{err: err, flow: flow}
		}
		if check != nil {
			if own, err := check(targetDir); err == nil && len(own.Files) > 0 {
				return repairForeignMsg{own: own}
			}
		}
		return run()
	}
}

// handleForeignKey is the stop screen's menu. Copy command stays on the
// screen and copies again on every Enter.
func (m RepairModel) handleForeignKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	return m.handleMenu(msg, foreignMenu, func(label string) (tea.Model, tea.Cmd) {
		switch label {
		case removeCopy:
			m.copied = true
			words := deploy.ChownCommandWords(m.targetDir, m.foreign)
			return m, copyToClipboard(m.clipboardWriter(), strings.Join(words, " "))
		case "Diagnose again":
			return m.diagnoseAgain()
		case "Menu":
			m.Done = true
			m.WantsMenu = true
			return m, nil
		default:
			return m, tea.Quit
		}
	})
}

// clipboardWriter is where the copy sequence goes: os.Stdout unless a test
// supplied a writer.
func (m RepairModel) clipboardWriter() io.Writer {
	if m.clipboard != nil {
		return m.clipboard
	}
	return os.Stdout
}

// viewForeign is the stop screen: each foreign file and its owner, why
// Repair stops, and the command that gives them back.
func (m RepairModel) viewForeign() string {
	var b strings.Builder
	writeInlineHeader(&b, style.ErrorText.Render(style.SymbolFailure), "Repair can't check these files")
	head := b.String()
	b.Reset()

	// Paths and owners are nobody's choice of length or characters:
	// escaped for the terminal, each row wrapped only where the screen
	// is narrower than it. Every row is centred on its own, so all are
	// padded to one width: their left edges then align, as planRows does.
	edge := m.width - 4
	var files [][]string
	blockWidth := 0
	for _, f := range m.foreign.Files {
		rows := wrapHard(f.Path+" — owned by "+f.Owner, edge, edge, folderRows)
		for _, row := range rows {
			blockWidth = max(blockWidth, lipgloss.Width(row))
		}
		files = append(files, rows)
	}
	for _, rows := range files {
		for i, row := range rows {
			rows[i] = style.Ink.Render(row + strings.Repeat(" ", blockWidth-lipgloss.Width(row)))
		}
	}

	var tail strings.Builder
	// errorWidth, not proseWidth: the first row is a little over the
	// prose measure and reads better whole wherever it fits.
	wide := measure(m.width, errorWidth)
	fmt.Fprintln(&tail)
	writeWrappedIn(&tail, style.MutedText, "their contents can't be checked and nothing can restore them", wide, textArea(m.width))
	writeWrappedIn(&tail, style.MutedText, "give them back to this account, then diagnose again", wide, textArea(m.width))
	fmt.Fprintln(&tail)

	// The command in a box, as Remove's: the words drawn as they are
	// (an odd folder's word is already in printable $'...' form), so the
	// box and Copy command's line are the same bytes (#277 W2).
	words := deploy.ChownCommandWords(m.targetDir, m.foreign)
	lines := wrapShellCommand(words, m.width-8)
	boxW := 0
	for _, l := range lines {
		boxW = max(boxW, lipgloss.Width(l))
	}
	for _, l := range drawBox(lipgloss.NewStyle().Foreground(style.Border), boxW+4, lines) {
		fmt.Fprintln(&tail, l)
	}
	fmt.Fprintln(&tail)

	// The copy row says so once it has copied; the menu's own label,
	// which Enter answers with, stays the same.
	rows := foreignMenu
	if m.copied {
		rows = append([]string{removeCopy + style.Separator + "copied"}, foreignMenu[1:]...)
	}
	m.menu.over(rows).write(&tail)

	// The file rows take whatever the rest leaves, so a long list never
	// pushes the command and menu off the bottom.
	room := math.MaxInt
	if m.height > 0 {
		room = m.height - 1 - strings.Count(head, "\n") - strings.Count(tail.String(), "\n")
	}
	b.WriteString(head)
	m.writeSteps(&b, files, room)
	b.WriteString(tail.String())
	return skyBlock(m.star, m.width, m.height, b.String())
}
