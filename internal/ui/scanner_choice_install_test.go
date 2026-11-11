package ui

// Blind tests for #340 (the virus-scanner choice on Install), written
// from the design note's test specification alone: items 1 to 5, 8, 10,
// 11 and 16 to 18. What a person sees is read from the stripped screen,
// and what the engine is given from what the fake installer recorded.

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const (
	scanner340Adds   = "recommended: scans documents for viruses"
	scanner340Off    = "Off: uploaded and mailed documents are not virus-scanned. To turn it on later, run scripts/configure.sh --set-scanner on, then Update."
	scanner340Env    = "ORBIT_INSTALL_SCANNER"
	scanner340Rowsat = 4 // Down presses from Standard to the Scanner row
)

// scanner340Model is a fresh Install flow at width w on the profile screen.
func scanner340Model(t *testing.T, w int) InstallModel {
	t.Helper()
	return c9Size(t, NewInstallModel("/opt/orbit", "v1"), w)
}

func scanner340Rows(m tea.Model) [][]rune {
	var rows [][]rune
	for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		rows = append(rows, []rune(l))
	}
	return rows
}

// scanner340Press presses code n times.
func scanner340Press(t *testing.T, m InstallModel, code rune, n int) InstallModel {
	t.Helper()
	for i := 0; i < n; i++ {
		m, _ = updateAs(t, m, key(code))
	}
	return m
}

// scanner340Toggle moves to the Scanner row from Standard and presses Enter.
func scanner340Toggle(t *testing.T, m InstallModel) InstallModel {
	t.Helper()
	m = scanner340Press(t, m, tea.KeyDown, scanner340Rowsat)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	return m
}

// scanner340Status is the status column of the named wide-table row.
func scanner340Status(t *testing.T, m tea.Model, w int, name string) string {
	t.Helper()
	rows := scanner340Rows(m)
	r := rows[rowOfName(t, rows, name)]
	nameCol := (w-67)/2 + 2
	if nameCol+55 > len(r) {
		t.Fatalf("w=%d: %s row too short for a status column: %q", w, name, string(r))
	}
	return strings.TrimSpace(string(r[nameCol+55:]))
}

func scanner340RowIndex(rows [][]rune, text string) int {
	for i, r := range rows {
		if strings.Contains(string(r), text) {
			return i
		}
	}
	return -1
}

func scanner340Blank(r []rune) bool { return strings.TrimSpace(string(r)) == "" }

// Spec 1: the default is on, in a row under the four profiles with one
// blank row between; the four profiles are as they were.
func TestScanner340_DefaultRowIsOnUnderTheProfiles(t *testing.T) {
	for _, w := range []int{80, 120} {
		m := scanner340Model(t, w)
		rows := scanner340Rows(m)
		nameCol := (w-67)/2 + 2
		full := rowOfName(t, rows, "Full")
		sc := rowOfName(t, rows, "Scanner")
		if sc != full+2 || !scanner340Blank(rows[full+1]) {
			t.Errorf("w=%d: Scanner is on row %d, Full on %d: want one blank row between", w, sc, full)
		}
		if !hasAt(rows[sc], nameCol, "Scanner") || !hasAt(rows[sc], nameCol+11, scanner340Adds) {
			t.Errorf("w=%d: the Scanner row is wrong: %q", w, string(rows[sc]))
		}
		if got := scanner340Status(t, m, w, "Scanner"); got != "on" {
			t.Errorf("w=%d: Scanner reads %q, want on", w, got)
		}
		if strings.Contains(flat(screen(m)), "not virus-scanned") {
			t.Errorf("w=%d: the consequence line is on the screen with the scanner on:\n%s", w, screen(m))
		}
		for _, p := range profileTableRows {
			r := rows[rowOfName(t, rows, p.name)]
			if !hasAt(r, nameCol+11, p.adds) || !hasAt(r, nameCol+55, p.avail) {
				t.Errorf("w=%d: %s changed: %q", w, p.name, string(r))
			}
		}
	}
}

// Spec 2: Enter on the row flips it and stays; the caret does not move.
func TestScanner340_EnterTogglesAndStays(t *testing.T) {
	m := scanner340Press(t, scanner340Model(t, 80), tea.KeyDown, scanner340Rowsat)
	idle := NewInstallModel("/opt/orbit", "v1").awaiting
	for i, want := range []string{"off", "on"} {
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, key(tea.KeyEnter))
		if m.state != installStateProfile {
			t.Fatalf("Enter %d left the profile screen (state %v):\n%s", i+1, m.state, screen(m))
		}
		if m.awaiting != idle {
			t.Errorf("Enter %d set awaiting to %q", i+1, m.awaiting)
		}
		if cmd != nil {
			if msg := cmd(); msg != nil {
				if _, tick := msg.(tickMsg); !tick {
					t.Errorf("Enter %d started something: %T", i+1, msg)
				}
			}
		}
		if got := scanner340Status(t, m, 80, "Scanner"); got != want {
			t.Errorf("after Enter %d the Scanner reads %q, want %q", i+1, got, want)
		}
		rows := scanner340Rows(m)
		if c := runeIndex(rows[rowOfName(t, rows, "Scanner")], "▸"); c < 0 {
			t.Errorf("after Enter %d the caret left the Scanner row:\n%s", i+1, screen(m))
		}
		if strings.Contains(screen(m), "Development") || strings.Contains(screen(m), "notice") {
			t.Errorf("after Enter %d a notice is on the screen:\n%s", i+1, screen(m))
		}
	}
}

// Spec 3: the consequence line, only while off, under the table.
func TestScanner340_ConsequenceLineWhileOff(t *testing.T) {
	for _, w := range []int{50, 80, 120} {
		m := scanner340Toggle(t, scanner340Model(t, w))
		if got := flat(screen(m)); !strings.Contains(got, scanner340Off) {
			t.Errorf("w=%d: the consequence line is missing with the scanner off:\n%s", w, screen(m))
		}
		rows := scanner340Rows(m)
		line := scanner340RowIndex(rows, "Off: uploaded")
		if line < 0 {
			t.Fatalf("w=%d: no row starts the consequence line:\n%s", w, screen(m))
		}
		if full := scanner340RowIndex(rows, "documents extraction and the local model"); full >= 0 && line <= full {
			t.Errorf("w=%d: the consequence line is not under the table", w)
		}
		for _, r := range rows {
			if len(r) > w {
				t.Errorf("w=%d: row wider than the screen: %q", w, string(r))
			}
		}
	}
	m := scanner340Model(t, 80)
	m = scanner340Toggle(t, m)
	m, _ = updateAs(t, m, key(tea.KeyEnter)) // back on
	if strings.Contains(flat(screen(m)), "not virus-scanned") {
		t.Errorf("the consequence line stays after turning the scanner on:\n%s", screen(m))
	}
}

// Spec 4: the two-row form at 50 columns.
func TestScanner340_NarrowLayout(t *testing.T) {
	for _, state := range []string{"on", "off"} {
		m := scanner340Model(t, 50)
		if state == "off" {
			m = scanner340Toggle(t, m)
		}
		rows := scanner340Rows(m)
		head := scanner340RowIndex(rows, "Scanner · "+state)
		if head < 0 {
			t.Fatalf("50 columns, %s: no head %q:\n%s", state, "Scanner · "+state, screen(m))
		}
		if head == 0 || !scanner340Blank(rows[head-1]) {
			t.Errorf("50 columns, %s: no blank row before the Scanner entry:\n%s", state, screen(m))
		}
		var parts []string
		// The entry is the block's last: stop once the adds text is read,
		// so the sky row below it is never taken for more of it.
		for j := head + 1; j < len(rows) && !scanner340Blank(rows[j]) && len(strings.Join(parts, " ")) < len(scanner340Adds); j++ {
			parts = append(parts, strings.TrimSpace(string(rows[j])))
		}
		if got := strings.Join(parts, " "); got != scanner340Adds {
			t.Errorf("50 columns, %s: the adds text = %q, want %q", state, got, scanner340Adds)
		}
	}
}

// scanner340Recorder points the launcher's install.sh override at a
// script that records ORBIT_INSTALL_SCANNER (or <unset>) and its
// arguments per run, then fails in a way Install can retry.
func scanner340Recorder(t *testing.T) (target, out string) {
	t.Helper()
	target, out = docs260Recorder(t)
	t.Setenv(scanner340Env, "")
	if err := os.Unsetenv(scanner340Env); err != nil {
		t.Fatal(err)
	}
	installScriptAt(t, "printf '%s\\n' \"${"+scanner340Env+"-<unset>}\" >> \"$ORBIT_T260_OUT/scanner\"\n"+
		"printf '%s\\n' \"$*\" >> \"$ORBIT_T260_OUT/argv\"\n"+
		"echo 'phase=host component=host state=failed reason=docker-host action=retry elapsed=0s'\n"+
		"exit 1\n")
	return target, out
}

func scanner340NoChoiceInArgv(t *testing.T, out string) {
	t.Helper()
	argv := docs260Lines(t, out, "argv")
	if len(argv) == 0 {
		t.Fatal("the installer was never started")
	}
	for _, a := range argv {
		if strings.Contains(strings.ToLower(a), "scanner") {
			t.Errorf("the installer's arguments %q name the scanner; it travels in the environment only", a)
		}
		for _, f := range strings.Fields(a) {
			if f == "on" || f == "off" {
				t.Errorf("the installer's arguments %q carry %q; the choice travels in the environment only", a, f)
			}
		}
	}
}

// Spec 5: the run carries the choice, in the environment only.
func TestScanner340_RunCarriesTheChoiceInTheEnvironmentOnly(t *testing.T) {
	for _, c := range []struct {
		name    string
		off     bool
		upTo    int // Up presses from the Scanner row to the chosen profile
		profile string
		want    string
	}{
		{"Standard, scanner left on", false, 0, "Standard", "on"},
		{"Standard, scanner off", true, 4, "Standard", "off"},
		{"Documents, scanner off", true, 3, "Documents", "off"},
		{"Documents, scanner on", false, 0, "Documents", "on"},
	} {
		t.Run(c.name, func(t *testing.T) {
			docs260Memory(t, 16*docs260GB, true)
			target, out := scanner340Recorder(t)
			m, s := docs260Model(t, target)
			if c.off {
				m = scanner340Toggle(t, m) // the caret ends on the Scanner row
				m = scanner340Press(t, m, tea.KeyUp, c.upTo)
			} else if c.profile == "Documents" {
				m = docs260Down(t, m, 1)
			}
			m, cmd := docs260Accept(t, m, c.profile)
			drive(t, m, cmd, s)
			if got := docs260Lines(t, out, "scanner"); len(got) != 1 || got[0] != c.want {
				t.Errorf("the installer saw %s = %q, want exactly one run with %q", scanner340Env, got, c.want)
			}
			scanner340NoChoiceInArgv(t, out)
		})
	}
}

// Spec 8: the retry after guided configuration carries the choice again.
func TestScanner340_RetryAfterGuidedConfigurationCarriesTheChoiceAgain(t *testing.T) {
	f := newHandoffFixture(t, 1, tailSucceeds)
	t.Setenv(scanner340Env, "")
	if err := os.Unsetenv(scanner340Env); err != nil {
		t.Fatal(err)
	}
	head := strings.Replace(handoffInstallerHead, "out=@OUT@\n",
		"out=@OUT@\nprintf '%s\\n' \"${"+scanner340Env+"-<unset>}\" >> \"$out/scanner\"\n", 1)
	installScriptAt(t, strings.NewReplacer("@OUT@", shQuote(f.out), "@MAX@", "1", "@TREEENV@", deploy.ConfigTreeEnv).Replace(head)+tailSucceeds)

	m, s := f.model(f.seams(true, nil))
	m = scanner340Toggle(t, m)
	m = scanner340Press(t, m, tea.KeyUp, 4) // back on Standard
	m = f.guidedConfiguration(m, s)

	if got := f.runs(); got != "2" {
		t.Fatalf("the installer ran %q times, want 2 (the refusal, then the retry); screen:\n%s", got, screen(m))
	}
	got := strings.Fields(f.read("scanner"))
	if len(got) != 2 || got[0] != "off" || got[1] != "off" {
		t.Errorf("the installer saw %s = %q on its two runs, want off and off", scanner340Env, got)
	}
}

// scanner340Machine puts the machine at gb GiB and returns a fresh
// profile screen at 80 columns.
func scanner340Machine(t *testing.T, gb uint64) InstallModel {
	t.Helper()
	docs260Memory(t, gb*docs260GB, true)
	return scanner340Model(t, 80)
}

// Spec 10: the memory check follows the toggle.
func TestScanner340_MemoryCheckFollowsTheToggle(t *testing.T) {
	m := scanner340Machine(t, 4)
	if got := scanner340Status(t, m, 80, "Documents"); got != "not enough" {
		t.Fatalf("scanner on, 4 GiB: Documents reads %q, want not enough", got)
	}
	if !strings.Contains(flat(screen(m)), docs260Short) {
		t.Errorf("scanner on, 4 GiB: the reason %q is missing:\n%s", docs260Short, screen(m))
	}

	m = scanner340Toggle(t, m)
	for name, want := range map[string]string{"Standard": "available", "Documents": "+1 GB", "Scanner": "off"} {
		if got := scanner340Status(t, m, 80, name); got != want {
			t.Errorf("scanner off, 4 GiB: %s reads %q, want %q", name, got, want)
		}
	}
	if s := flat(screen(m)); strings.Contains(s, "this machine has") || strings.Contains(s, "needs") {
		t.Errorf("scanner off, 4 GiB: a memory reason is still on the screen:\n%s", screen(m))
	}
	// Enter on Documents opens the notice.
	docs, _ := updateAs(t, scanner340Press(t, m, tea.KeyUp, 3), key(tea.KeyEnter))
	if docs.state != installStateNotice {
		t.Errorf("scanner off, 4 GiB: Enter on Documents did not open the notice (state %v):\n%s", docs.state, screen(docs))
	}

	// Back on: not enough again.
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if got := scanner340Status(t, m, 80, "Documents"); got != "not enough" {
		t.Errorf("scanner back on, 4 GiB: Documents reads %q, want not enough", got)
	}

	// 2 GiB with the scanner off: Documents needs 3 GB; Standard is fine.
	m = scanner340Toggle(t, scanner340Machine(t, 2))
	if got := scanner340Status(t, m, 80, "Documents"); got != "not enough" {
		t.Errorf("scanner off, 2 GiB: Documents reads %q, want not enough", got)
	}
	if got := scanner340Status(t, m, 80, "Standard"); got != "available" {
		t.Errorf("scanner off, 2 GiB: Standard reads %q, want available", got)
	}
	if want := "Documents needs 3 GB of memory in all and this machine has 2 GB."; !strings.Contains(flat(screen(m)), want) {
		t.Errorf("scanner off, 2 GiB: %q is missing:\n%s", want, screen(m))
	}
	// Standard is available whatever the machine and the toggle.
	for _, gb := range []uint64{2, 4, 16} {
		for _, off := range []bool{false, true} {
			m := scanner340Machine(t, gb)
			if off {
				m = scanner340Toggle(t, m)
			}
			if got := scanner340Status(t, m, 80, "Standard"); got != "available" {
				t.Errorf("%d GiB, scanner off=%v: Standard reads %q", gb, off, got)
			}
		}
	}
}

// Spec 11: both lines, in order.
func TestScanner340_MemoryReasonThenScannerLine(t *testing.T) {
	m := scanner340Toggle(t, scanner340Machine(t, 4))
	rows := scanner340Rows(m)
	if scanner340RowIndex(rows, "this machine has") >= 0 {
		t.Errorf("4 GiB, scanner off: a memory reason is shown:\n%s", screen(m))
	}
	if scanner340RowIndex(rows, "Off: uploaded") < 0 {
		t.Errorf("4 GiB, scanner off: the scanner line is missing:\n%s", screen(m))
	}

	m = scanner340Toggle(t, scanner340Machine(t, 2))
	rows = scanner340Rows(m)
	reason := scanner340RowIndex(rows, "this machine has 2 GB.")
	line := scanner340RowIndex(rows, "Off: uploaded")
	if reason < 0 || line < 0 {
		t.Fatalf("2 GiB, scanner off: want both lines:\n%s", screen(m))
	}
	if line != reason+2 || !scanner340Blank(rows[reason+1]) {
		t.Errorf("2 GiB, scanner off: the scanner line is on row %d, the reason ends on row %d: want one blank row between:\n%s", line, reason, screen(m))
	}
}

// Spec 16: with the pre-flight not answered, the row toggles at once and
// a later Enter on Standard waits as today.
func TestScanner340_ToggleDoesNotWaitForThePreflight(t *testing.T) {
	m := scanner340Model(t, 80)
	m.checkVolumes = staleVolumeCheck() // asked, not yet answered
	idle := m.awaiting
	if !m.waiting() {
		t.Fatalf("setup: a fresh flow is not waiting for its pre-flight")
	}
	m = scanner340Toggle(t, m)
	if m.awaiting != idle {
		t.Errorf("Enter on the Scanner row set awaiting to %q", m.awaiting)
	}
	if got := scanner340Status(t, m, 80, "Scanner"); got != "off" {
		t.Errorf("the Scanner reads %q, want off at once", got)
	}
	m = scanner340Press(t, m, tea.KeyUp, 4)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.awaiting != installStandard || m.state != installStateProfile {
		t.Errorf("Enter on Standard: awaiting %q, state %v; want it to wait for the pre-flight", m.awaiting, m.state)
	}
}

// Spec 17: Esc and Ctrl-C on the Scanner row act as on any row.
func TestScanner340_EscAndCtrlCOnTheRow(t *testing.T) {
	m := scanner340Press(t, scanner340Model(t, 80), tea.KeyDown, scanner340Rowsat)
	left, _ := updateAs(t, m, key(tea.KeyEsc))
	if !leftForMenu(left.Outcome()) {
		t.Errorf("Esc on the Scanner row: outcome %+v, want back to the menu", left.Outcome())
	}
	_, cmd := updateAs(t, m, ctrlC())
	if cmd == nil {
		t.Fatal("Ctrl-C on the Scanner row did nothing")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Errorf("Ctrl-C on the Scanner row did not quit")
	}
}

// Spec 18: no hints about keys, on or off.
func TestScanner340_NoKeyHints(t *testing.T) {
	for _, off := range []bool{false, true} {
		m := scanner340Model(t, 80)
		if off {
			m = scanner340Toggle(t, m)
		}
		s := screen(m)
		for _, hint := range []string{"Enter", "Space", "←", "→", "toggle", "Toggle", "press", "Press"} {
			if strings.Contains(s, hint) {
				t.Errorf("scanner off=%v: the screen holds the hint %q:\n%s", off, hint, s)
			}
		}
	}
}
