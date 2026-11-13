package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// profileChoice is the profile table both Install (#247, #260) and
// Configure (#286) draw: its menu, the machine's memory as read when the
// flow began, and, for Configure, the profile the deployment already runs.
// The flow models embed it.
type profileChoice struct {
	profile menu // installProfiles, or installProfileRows without the scanner row

	// scannerOn is whether the virus scanner runs (#340): it counts toward
	// each profile's memory need. Install starts it on and the scanner row
	// flips it; Configure takes the deployment's own setting.
	scannerOn bool

	// scannerRow is whether the table draws the scanner toggle as its
	// fifth row: Install does; Configure never does, because it runs as an
	// update and an update never sends the scanner choice (#340, #286).
	scannerRow bool

	// memory is the machine's total memory as read when the flow
	// began; memoryKnown is false when it could not be read, and then no
	// row is ever "not enough" (#260).
	memory      uint64
	memoryKnown bool

	// current is the deploy profile name of the deployment's own row
	// ("" for Install, which has none): that row reads "current", is never
	// "not enough", and Configure starts its caret there.
	current string
}

// newProfileChoice is Install's table, with the scanner row, the scanner
// on at the start.
func newProfileChoice(memory uint64, known bool) profileChoice {
	return profileChoice{profile: newMenu(installProfiles...), memory: memory, memoryKnown: known, scannerOn: true, scannerRow: true}
}

// newCurrentProfileChoice is Configure's table: no scanner row, the
// memory need counted with the deployment's own scanner setting, and the
// caret on current.
func newCurrentProfileChoice(memory uint64, known bool, current string, scannerOn bool) profileChoice {
	return profileChoice{profile: newMenu(installProfileRows...), memory: memory, memoryKnown: known, current: current, scannerOn: scannerOn}.toCurrent()
}

// isCurrent is whether p is the deployment's own row.
func (m profileChoice) isCurrent(p installProfile) bool {
	return p.profile != "" && p.profile == m.current
}

// toCurrent puts the caret back on the deployment's own row.
func (m profileChoice) toCurrent() profileChoice {
	for i, p := range installProfileTable {
		if m.isCurrent(p) {
			m.profile.sel = i
		}
	}
	return m
}

// tableWidth is the width every line of the table block is padded to.
func tableWidth(width int) int {
	if width < installTableMin {
		return measure(width, proseWidth)
	}
	return installTableW
}

// writeTableGap writes the blank row between the table and a line under
// it, padded too, so the sky keeps off it.
func (m profileChoice) writeTableGap(b *strings.Builder, width int) {
	fmt.Fprintln(b, strings.Repeat(" ", tableWidth(width)))
}

// writeUnderTable writes one line of text under the table, padded to the
// table's width so the sky centres it with the table rather than on its
// own (the memory reason, Configure's notes).
func (m profileChoice) writeUnderTable(b *strings.Builder, text string, width int) {
	w := tableWidth(width)
	for _, row := range wrapWordsIn(text, w-2, textArea(width)-2) {
		fmt.Fprintln(b, "  "+style.Tagline.Render(row)+strings.Repeat(" ", max(0, w-2-len([]rune(row)))))
	}
}

// rowNamed is the table row called name; the zero row if none is.
func (m profileChoice) rowNamed(name string) installProfile {
	for _, p := range installProfileTable {
		if p.name == name {
			return p
		}
	}
	return installProfile{}
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
	// installScanner is Install's fifth row (#340): a toggle, not a profile.
	installScanner     = "Scanner"
	installScannerAdds = "recommended: scans documents for viruses"
	installScannerOn   = "on"
	installScannerOff  = "off"
)

// scannerStatus is the scanner row's status word.
func (m profileChoice) scannerStatus() string {
	if m.scannerOn {
		return installScannerOn
	}
	return installScannerOff
}

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
	installCurrent   = "current" // Configure: the deployment's own row
)

// shortOfMemory is whether the machine is known to have less memory than
// the row needs. Standard is never short (the launcher doesn't block the
// baseline, as Orbit's installer doesn't), and a machine whose memory
// could not be read is never short.
func (m profileChoice) shortOfMemory(p installProfile) bool {
	if p.profile == "" || p.profile == deploy.ProfileStandard || m.isCurrent(p) {
		return false
	}
	return m.memoryKnown && m.memory < deploy.ProfileNeed(p.profile, m.scannerOn)
}

// status is the row's status column: "available" for Standard, the
// memory a profile adds over Standard ("+1 GB"), "not enough" when the
// machine is short of what the profile needs in all, and "not yet" for
// a row with no profile behind it.
func (m profileChoice) status(p installProfile) string {
	switch {
	case m.isCurrent(p):
		return installCurrent
	case p.profile == "":
		return installNotYet
	case p.profile == deploy.ProfileStandard:
		return installAvailable
	case m.shortOfMemory(p):
		return installNotEnough
	}
	extra := deploy.ProfileNeed(p.profile, m.scannerOn) - deploy.ProfileNeed(deploy.ProfileStandard, m.scannerOn)
	return fmt.Sprintf("+%d GB", extra>>30)
}

// canChoose is whether Enter on the row does anything: the baseline
// always, a profile row unless the machine is short of memory.
func (m profileChoice) canChoose(p installProfile) bool {
	return p.profile != "" && !m.shortOfMemory(p)
}

// memoryReason is the one line under the table when a row is "not
// enough"; empty when no row is. It names the first such row.
func (m profileChoice) memoryReason() string {
	for _, p := range installProfileTable {
		if m.shortOfMemory(p) {
			return fmt.Sprintf("%s needs %d GB of memory in all and this machine has %d GB.",
				p.name, deploy.ProfileNeed(p.profile, m.scannerOn)>>30, m.memory>>30)
		}
	}
	return ""
}

// installProfileRows is the profile rows' names; installProfiles adds
// Install's scanner row.
var installProfileRows = func() []string {
	names := make([]string, len(installProfileTable))
	for i, p := range installProfileTable {
		names[i] = p.name
	}
	return names
}()

var installProfiles = append(append([]string{}, installProfileRows...), installScanner)

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

// writeProfileTable draws the profile choice as a table at 68 columns and
// over, and as two rows per profile below. Every row is padded to the
// block's width so the sky's per-line centring keeps the block one unit.
func (m profileChoice) writeProfileTable(b *strings.Builder, width int) {
	pad := func(styled string, used, w int) string {
		return styled + strings.Repeat(" ", max(0, w-used))
	}
	caret := func(sel bool) string {
		if sel {
			return style.MenuCaret.Render(style.SymbolSelected) + " "
		}
		return "  "
	}
	scannerIdx := len(installProfileTable)
	rowStyle := func(i int, p installProfile) lipgloss.Style {
		switch {
		case i == m.profile.sel:
			return style.Ink
		case m.scannerRow && i == scannerIdx, m.canChoose(p):
			return style.MutedText
		}
		return style.Tagline
	}
	if width >= installTableMin {
		for i, p := range installProfileTable {
			st := rowStyle(i, p)
			status := m.status(p)
			line := caret(i == m.profile.sel) +
				pad(st.Render(p.name), len(p.name), installNameCol) + " " +
				pad(st.Render(p.adds), len([]rune(p.adds)), installAddsCol) + " " +
				pad(st.Render(status), len(status), installAvailCol)
			fmt.Fprintln(b, line)
		}
		if !m.scannerRow {
			return
		}
		st := rowStyle(scannerIdx, installProfile{})
		status := m.scannerStatus()
		fmt.Fprintln(b, pad("", 0, installTableW))
		fmt.Fprintln(b, caret(m.profile.sel == scannerIdx)+
			pad(st.Render(installScanner), len(installScanner), installNameCol)+" "+
			pad(st.Render(installScannerAdds), len([]rune(installScannerAdds)), installAddsCol)+" "+
			pad(st.Render(status), len(status), installAvailCol))
		return
	}
	w := measure(width, proseWidth)
	entries := append([]installProfile{}, installProfileTable...)
	if m.scannerRow {
		entries = append(entries, installProfile{name: installScanner, adds: installScannerAdds})
	}
	for i, p := range entries {
		if i > 0 {
			fmt.Fprintln(b, pad("", 0, w))
		}
		st := rowStyle(i, p)
		status := m.status(p)
		if m.scannerRow && i == scannerIdx {
			status = m.scannerStatus()
		}
		head := p.name + style.Separator + status
		fmt.Fprintln(b, pad(caret(i == m.profile.sel)+st.Render(head), 2+len([]rune(head)), w))
		for _, r := range wrapWordsIn(p.adds, w-2, textArea(width)-2) {
			fmt.Fprintln(b, pad("  "+st.Render(r), 2+len([]rune(r)), w))
		}
	}
}
