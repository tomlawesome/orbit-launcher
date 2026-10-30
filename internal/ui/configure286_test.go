package ui

// Blind tests for #286 (Configure: change an installed deployment's
// profile from the launcher), written from the issue's design decision
// and build notes, the owner's answers 5a and 6a, and the settled list
// before the code exists. Every expected string is the spec's own.
//
// Names the build notes give and these tests rely on: menuConfigure,
// menuRepair, appStateConfigure (not asserted by value), and the
// existing seams AppModel.flowInstalledAt, flowSeams and flowReadClaim.
// Everything else goes through the screen the person sees, or through
// what the engine run was started with (a recording install.sh).
//
// The fixtures: a deployment is a folder with .env-orbit and
// docker-compose.yml; a folder with .env-orbit alone is prepared, not
// installed; its install date is 2026-10-12 (the identity line's own
// form); memory is 16 GB unless a test says otherwise.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const (
	cfg286Identity = "mail.example.com · installed 2026-10-12"
	cfg286Header   = "Change this deployment's profile"
	cfg286AlsoLine = "This also updates Orbit to its newest release."

	cfg286StandardNow  = "Orbit already runs the Standard profile here — choose another to change it."
	cfg286DocumentsNow = "Orbit already runs the Documents profile here — choose another to change it."

	cfg286Unchanged = "Orbit updated but kept the %s profile: this Orbit release predates profile changes from the launcher. Try again after Orbit's next release."
)

var cfg286Date = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

// cfg286Folder is a folder holding a deployment. profiles is the
// COMPOSE_PROFILES value; "-" leaves the line out. installed adds the
// compose file, which is what makes the deployment installed rather
// than prepared.
func cfg286Folder(t *testing.T, dir, profiles string, installed bool) string {
	t.Helper()
	body := "APP_URL=https://mail.example.com\n"
	if profiles != "-" {
		body += "COMPOSE_PROFILES=" + profiles + "\n"
	}
	writeEnv(t, dir, body)
	if installed {
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("name: orbit\nservices: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// cfg286App is the app, sized, sitting on its main menu, over dir. The
// install date lookup answers cfg286Date; Docker is a fake that knows
// nothing; the host has 16 GB.
func cfg286App(t *testing.T, dir string) (AppModel, *sink) {
	t.Helper()
	docs260Memory(t, 16*docs260GB, true)
	fakeDockerOnPath(t)
	queue := newSink()
	m := NewAppModelNoAnimation().WithSender(queue.Send)
	m.flowInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return cfg286Date }
	m.targetDir = dir
	m = m.WithDeploymentStatus(nil)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	return m, queue
}

// cfg286Open chooses Configure on the main menu and lets the flow start.
func cfg286Open(t *testing.T, m AppModel) AppModel {
	t.Helper()
	m.splash.menu.sel = menuConfigure
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	return drive(t, m, cmd)
}

func cfg286Resize(t *testing.T, m AppModel, w, h int) AppModel {
	t.Helper()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// cfg286RowIndex is the screen row whose whole text (caret and sky
// taken off) is label, or -1.
func cfg286RowIndex(s, label string) int {
	for i, line := range strings.Split(s, "\n") {
		if screenRow(line) == screenRow(label) {
			return i
		}
	}
	return -1
}

// cfg286CaretLine is the one screen line that carries the caret.
func cfg286CaretLine(t *testing.T, s string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "▸") {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d lines carry the caret, want exactly one:\n%s", len(found), s)
	}
	return found[0]
}

// cfg286MoveTo presses Down until the caret line reads label.
func cfg286MoveTo(t *testing.T, m AppModel, label string) AppModel {
	t.Helper()
	for i := 0; i < 8; i++ {
		if screenRow(cfg286CaretLine(t, screen(m))) == label {
			return m
		}
		m, _ = updateAs(t, m, key(tea.KeyDown))
	}
	t.Fatalf("the caret never reached %q:\n%s", label, screen(m))
	return m
}

// cfg286TableStatus reads the wide profile table: the status column
// of each profile, and the name the caret is on.
func cfg286TableStatus(t *testing.T, s string) (status map[string]string, caret string) {
	t.Helper()
	status = map[string]string{}
	for _, row := range profileTableRows {
		for _, line := range strings.Split(s, "\n") {
			i := strings.Index(line, row.adds)
			if i < 0 || !strings.Contains(line, row.name+" ") {
				continue
			}
			status[row.name] = strings.Trim(line[i+len(row.adds):], " ·✦●⟡")
			if strings.Contains(line, "▸") {
				caret = row.name
			}
		}
		if _, ok := status[row.name]; !ok {
			t.Fatalf("no table row for %s:\n%s", row.name, s)
		}
	}
	return status, caret
}

// cfg286TableMoveTo moves the caret to the named profile.
func cfg286TableMoveTo(t *testing.T, m AppModel, name string) AppModel {
	t.Helper()
	for i := 0; i < 6; i++ {
		if _, caret := cfg286TableStatus(t, screen(m)); caret == name {
			return m
		}
		m, _ = updateAs(t, m, key(tea.KeyDown))
	}
	t.Fatalf("the caret never reached %s:\n%s", name, screen(m))
	return m
}

func cfg286OnTable(t *testing.T, m AppModel) {
	t.Helper()
	if s := screen(m); !strings.Contains(s, "Choose a deployment profile") {
		t.Fatalf("not on the profile screen:\n%s", s)
	}
}

func cfg286OnConfirm(t *testing.T, m AppModel) {
	t.Helper()
	if s := screen(m); !strings.Contains(s, cfg286Header) {
		t.Fatalf("not on the confirm screen:\n%s", s)
	}
}

// cfg286ToConfirm opens Configure and chooses the named profile.
func cfg286ToConfirm(t *testing.T, m AppModel, name string) AppModel {
	t.Helper()
	m = cfg286Open(t, m)
	cfg286OnTable(t, m)
	m = cfg286TableMoveTo(t, m, name)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	cfg286OnConfirm(t, m)
	return m
}

// cfg286Start presses the confirm screen's Change row and drives the
// run it starts until it stops.
func cfg286Start(t *testing.T, m AppModel, queue *sink, to string) AppModel {
	t.Helper()
	m = cfg286MoveTo(t, m, "Change to "+to)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	return drive(t, m, cmd, queue)
}

// cfg286DeadEnd checks a dead end: the words, and Menu as the one row.
func cfg286DeadEnd(t *testing.T, s string, want ...string) {
	t.Helper()
	one := flat(s)
	for _, w := range want {
		if !strings.Contains(one, flat(w)) {
			t.Errorf("the dead end does not say %q:\n%s", w, s)
		}
	}
	if !strings.Contains(s, "▸ Menu") || strings.Count(s, "▸") != 1 {
		t.Errorf("a dead end offers Menu and nothing else:\n%s", s)
	}
	if strings.Contains(s, "Choose a deployment profile") {
		t.Errorf("a dead end must not show the profile table:\n%s", s)
	}
}

// --- The menu -------------------------------------------------------------

// Fault: Configure missing, or in the wrong place. The top block reads
// Install, Update, Configure, Repair, Remove, Exit; rows never hide, so
// a folder with nothing in it shows it too; the menu shows labels only.
func TestConfigure286_MenuRowsAreInTheSpecifiedOrderAndNeverHide(t *testing.T) {
	order := []string{"Install", "Update", "Configure", "Repair", "Remove", "Exit"}
	for name, dir := range map[string]string{
		"installed":    cfg286Folder(t, shortTempDir(t), "-", true),
		"nothing here": shortTempDir(t),
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := cfg286App(t, dir)
			s := screen(m)
			prev := -1
			for _, label := range order {
				i := cfg286RowIndex(s, label)
				if i < 0 {
					t.Fatalf("no %q row on the main menu:\n%s", label, s)
				}
				if i <= prev {
					t.Errorf("%q is out of order (row %d, previous row %d):\n%s", label, i, prev, s)
				}
				prev = i
			}
			if strings.Contains(flat(s), "Change which optional services") {
				t.Errorf("the menu shows labels only, not descriptions:\n%s", s)
			}
		})
	}
}

// Fault: the menu constants not moved with the new row (Repair would
// open Configure's flow, or the reverse).
func TestConfigure286_MenuConstantsFollowTheRowOrder(t *testing.T) {
	if menuConfigure != menuUpdate+1 {
		t.Errorf("menuConfigure = %d, want the row after Update (%d)", menuConfigure, menuUpdate+1)
	}
	if menuRepair != menuConfigure+1 {
		t.Errorf("menuRepair = %d, want the row after Configure (%d)", menuRepair, menuConfigure+1)
	}
}

// Fault: adding the row moved the preselection. An installed
// deployment still starts on Update.
func TestConfigure286_PreselectionIsStillUpdateForAnInstalledDeployment(t *testing.T) {
	m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
	if m.splash.menu.sel != menuUpdate {
		t.Errorf("selected = %d, want Update (%d)", m.splash.menu.sel, menuUpdate)
	}
}

// Fault: the Repair row still opening the wrong flow once the rows
// shift: Repair is one row below Configure and still reaches Repair.
func TestConfigure286_RepairStillOpensRepairAndConfigureDoesNot(t *testing.T) {
	dir := cfg286Folder(t, shortTempDir(t), "-", true)
	m, _ := cfg286App(t, dir)
	m = cfg286Open(t, m)
	if m.state != appStateConfigure {
		t.Errorf("Configure opened state %v, want appStateConfigure", m.state)
	}
	m2, _ := cfg286App(t, dir)
	m2.splash.menu.sel = menuRepair
	m2, cmd := updateAs(t, m2, key(tea.KeyEnter))
	m2 = drive(t, m2, cmd)
	if m2.state == appStateConfigure {
		t.Errorf("Repair opened the Configure flow:\n%s", screen(m2))
	}
}

// --- Dead ends --------------------------------------------------------------

// Fault: a folder with nothing in it showing a profile table, or the
// wrong words. Paired with the installed folder, which does show it.
func TestConfigure286_NothingHereIsADeadEnd(t *testing.T) {
	dir := shortTempDir(t)
	m, _ := cfg286App(t, dir)
	m = cfg286Open(t, m)
	cfg286DeadEnd(t, screen(m),
		"No Orbit deployment found in "+dir,
		"There's nothing to configure here yet — Install chooses the profile as it goes in.")

	m2, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
	m2 = cfg286Open(t, m2)
	cfg286OnTable(t, m2)
	if strings.Contains(flat(screen(m2)), "nothing to configure") {
		t.Errorf("an installed deployment got the nothing-here dead end:\n%s", screen(m2))
	}
}

// Fault: a prepared-but-not-installed folder (no compose file) being
// offered a profile change. Paired with the same folder once the
// compose file is there.
func TestConfigure286_PreparedButNotInstalledIsADeadEnd(t *testing.T) {
	dir := cfg286Folder(t, shortTempDir(t), "-", false)
	m, _ := cfg286App(t, dir)
	m = cfg286Open(t, m)
	cfg286DeadEnd(t, screen(m), "Orbit isn't installed here yet, only prepared — Install is the way in.")

	cfg286Folder(t, dir, "-", true)
	m2, _ := cfg286App(t, dir)
	m2 = cfg286Open(t, m2)
	cfg286OnTable(t, m2)
}

// Fault: ai, full, a mix or an unknown value being treated as Standard
// or Documents. Paired with the values that are supported.
func TestConfigure286_UnsupportedProfilesAreADeadEndAndSupportedOnesAreNot(t *testing.T) {
	for _, profiles := range []string{"ai", "full", "processing,ai", "bogus"} {
		t.Run("unsupported "+profiles, func(t *testing.T) {
			m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), profiles, true))
			m = cfg286Open(t, m)
			cfg286DeadEnd(t, screen(m),
				"This deployment's profile can't be changed from the launcher yet",
				"It uses a local model, or a mix the launcher doesn't recognise. Orbit's own installer at a terminal can change it.")
		})
	}
	for _, profiles := range []string{"", "processing", `"processing"`} {
		t.Run("supported "+profiles, func(t *testing.T) {
			m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), profiles, true))
			m = cfg286Open(t, m)
			cfg286OnTable(t, m)
		})
	}
}

// Fault: an unreadable .env-orbit reported as missing, or Configure
// having its own wording for it. It is the existing screen.
func TestConfigure286_UnreadableEnvIsTheExistingUnreadableScreen(t *testing.T) {
	for name, dir := range map[string]string{
		"mode 0000": unreadableEnvDir(t),
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := cfg286App(t, dir)
			m = cfg286Open(t, m)
			assertUnreadableScreen(t, screen(m), dir)
		})
	}
}

// The same with a .env-orbit that is a directory: unreadable for every
// user, root included, so this one never skips.
func TestConfigure286_EnvThatIsADirectoryIsTheExistingUnreadableScreen(t *testing.T) {
	dir := directoryEnvDir(t)
	m, _ := cfg286App(t, dir)
	m = cfg286Open(t, m)
	assertCouldntReadScreen(t, screen(m), dir)
}

// Fault: a dead end that traps the person, or a Menu row that quits.
// Menu and Esc both go to the live main menu from every dead end.
func TestConfigure286_EveryDeadEndLeavesForTheMenuByMenuRowAndByEsc(t *testing.T) {
	folders := map[string]func(t *testing.T) string{
		"nothing here": func(t *testing.T) string { return shortTempDir(t) },
		"prepared":     func(t *testing.T) string { return cfg286Folder(t, shortTempDir(t), "-", false) },
		"unsupported":  func(t *testing.T) string { return cfg286Folder(t, shortTempDir(t), "full", true) },
		"directory":    directoryEnvDir,
	}
	for name, mk := range folders {
		for _, how := range []string{"Menu row", "Esc"} {
			t.Run(name+"/"+how, func(t *testing.T) {
				m, _ := cfg286App(t, mk(t))
				m = cfg286Open(t, m)
				if m.state != appStateConfigure {
					t.Fatalf("not in the Configure flow (state %v):\n%s", m.state, screen(m))
				}
				k := key(tea.KeyEnter)
				if how == "Esc" {
					k = key(tea.KeyEsc)
				}
				m, cmd := updateAs(t, m, k)
				m = drive(t, m, cmd)
				if m.state != appStateSplash {
					t.Fatalf("%s left for state %v, want the main menu:\n%s", how, m.state, screen(m))
				}
			})
		}
	}
}

// --- The profile choice -------------------------------------------------------

// Fault: the table not marking the deployment's own profile, or not
// starting the caret on it. Both profiles, with the other rows keeping
// Install's statuses.
func TestConfigure286_TableMarksTheCurrentProfileAndStartsTheCaretOnIt(t *testing.T) {
	for _, c := range []struct {
		profiles, current string
		want              map[string]string
	}{
		{"-", "Standard", map[string]string{"Standard": "current", "Documents": "+1 GB", "Agent": "not yet", "Full": "not yet"}},
		{"processing", "Documents", map[string]string{"Standard": "available", "Documents": "current", "Agent": "not yet", "Full": "not yet"}},
	} {
		t.Run(c.current, func(t *testing.T) {
			m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), c.profiles, true))
			m = cfg286Open(t, m)
			cfg286OnTable(t, m)
			status, caret := cfg286TableStatus(t, screen(m))
			for name, want := range c.want {
				if status[name] != want {
					t.Errorf("%s reads %q, want %q:\n%s", name, status[name], want, screen(m))
				}
			}
			if caret != c.current {
				t.Errorf("the caret starts on %q, want the current profile %q:\n%s", caret, c.current, screen(m))
			}
		})
	}
}

// Fault: the screen not saying which deployment it is about. Header,
// then the identity line in the existing form.
func TestConfigure286_ProfileScreenNamesTheDeploymentAndItsDate(t *testing.T) {
	m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
	m = cfg286Open(t, m)
	s := screen(m)
	if !strings.Contains(s, "Choose a deployment profile") || !strings.Contains(s, cfg286Identity) {
		t.Errorf("the profile screen lacks its header or %q:\n%s", cfg286Identity, s)
	}
	if h, id := strings.Index(s, "Choose a deployment profile"), strings.Index(s, cfg286Identity); h > id {
		t.Errorf("the identity line is above the header:\n%s", s)
	}
}

// Fault: Install's memory rule not carried over. With 4 GB Documents
// reads "not enough" and one line gives the reason; Standard stays
// available. Paired with Documents as the current profile, which reads
// "current" however little memory there is.
func TestConfigure286_MemoryRuleIsInstallsRule(t *testing.T) {
	dir := cfg286Folder(t, shortTempDir(t), "-", true)
	m, _ := cfg286App(t, dir)
	docs260Memory(t, 4*docs260GB, true)
	m = cfg286Open(t, m)
	status, _ := cfg286TableStatus(t, screen(m))
	if status["Documents"] != "not enough" || status["Standard"] != "current" {
		t.Errorf("with 4 GB: Standard %q, Documents %q; want current and not enough:\n%s", status["Standard"], status["Documents"], screen(m))
	}
	if n := strings.Count(flat(screen(m)), docs260Short); n != 1 {
		t.Errorf("the memory line is on the screen %d times, want once:\n%s", n, screen(m))
	}

	m2, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "processing", true))
	docs260Memory(t, 4*docs260GB, true)
	m2 = cfg286Open(t, m2)
	status, _ = cfg286TableStatus(t, screen(m2))
	if status["Documents"] != "current" || status["Standard"] != "available" {
		t.Errorf("Documents deployment with 4 GB: Standard %q, Documents %q; want available and current:\n%s", status["Standard"], status["Documents"], screen(m2))
	}
}

// Fault: Enter on a "not enough" row starting something, or moving on.
// Nothing starts and the table stays. Paired with exactly 7 GB, where
// the same Enter moves on to the confirm screen.
func TestConfigure286_EnterOnNotEnoughDoesNothingAndSevenGBIsEnough(t *testing.T) {
	target, out := docs260Recorder(t)
	cfg286Folder(t, target, "-", true)
	m, queue := cfg286App(t, target)
	docs260Memory(t, 4*docs260GB, true)
	m = cfg286Open(t, m)
	m = cfg286TableMoveTo(t, m, "Documents")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = driveQuiet(t, m, cmd, settleQuiet, queue)
	cfg286OnTable(t, m)
	if strings.Contains(screen(m), cfg286Header) {
		t.Errorf("Enter on a not-enough row reached the confirm screen:\n%s", screen(m))
	}
	if got := docs260Lines(t, out, "profile"); len(got) != 0 {
		t.Errorf("an installer ran: %q", got)
	}

	m2, _ := cfg286App(t, target)
	docs260Memory(t, 7*docs260GB, true)
	m2 = cfg286ToConfirm(t, m2, "Documents")
	_ = m2
}

// Fault: Enter on the current row doing anything but explain. The line
// names the deployment's own profile; moving the caret clears it.
// Paired with Enter on another row, which does move on.
func TestConfigure286_EnterOnTheCurrentRowExplainsAndMovingTheCaretClearsIt(t *testing.T) {
	for _, c := range []struct{ profiles, current, other, line string }{
		{"-", "Standard", "Documents", cfg286StandardNow},
		{"processing", "Documents", "Standard", cfg286DocumentsNow},
	} {
		t.Run(c.current, func(t *testing.T) {
			target, out := docs260Recorder(t)
			cfg286Folder(t, target, c.profiles, true)
			m, queue := cfg286App(t, target)
			m = cfg286Open(t, m)
			if strings.Contains(flat(screen(m)), "already runs") {
				t.Fatalf("the line is up before Enter:\n%s", screen(m))
			}
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m = driveQuiet(t, m, cmd, settleQuiet, queue)
			cfg286OnTable(t, m)
			if strings.Contains(screen(m), cfg286Header) {
				t.Fatalf("Enter on the current row reached the confirm screen:\n%s", screen(m))
			}
			if !strings.Contains(flat(screen(m)), c.line) {
				t.Errorf("the screen does not say %q:\n%s", c.line, screen(m))
			}
			if got := docs260Lines(t, out, "profile"); len(got) != 0 {
				t.Errorf("an installer ran: %q", got)
			}
			m, _ = updateAs(t, m, key(tea.KeyDown))
			if strings.Contains(flat(screen(m)), "already runs") {
				t.Errorf("moving the caret did not clear the line:\n%s", screen(m))
			}

			m2, _ := cfg286App(t, target)
			cfg286ToConfirm(t, m2, c.other)
		})
	}
}

// Fault: Esc on the profile screen trapping the person or quitting.
func TestConfigure286_EscOnTheProfileScreenGoesToTheMainMenu(t *testing.T) {
	m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
	m = cfg286Open(t, m)
	cfg286OnTable(t, m)
	m, cmd := updateAs(t, m, key(tea.KeyEsc))
	if isQuit(cmd) {
		t.Fatal("Esc on the profile screen quit")
	}
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Errorf("Esc left for state %v, want the main menu:\n%s", m.state, screen(m))
	}
}

// Fault: the changed table overflowing a narrow screen, or growing a
// hint line. At 68 columns and over it is one row per profile; below,
// two rows with the status in the head ("Standard · current").
func TestConfigure286_ProfileScreenFitsAtEveryWidth(t *testing.T) {
	for _, w := range []int{120, 80, 68, 67, 50} {
		m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
		m = cfg286Open(t, m)
		m = cfg286Resize(t, m, w, 30)
		s := screen(m)
		for _, line := range strings.Split(s, "\n") {
			if n := len([]rune(line)); n > w {
				t.Errorf("w=%d: row wider than the screen (%d): %q", w, n, line)
			}
			for _, hint := range []string{"Enter ", "Esc ", "navigate", "to select", "to choose"} {
				if strings.Contains(line, hint) {
					t.Errorf("w=%d: a key hint (%q): %q", w, hint, line)
				}
			}
		}
		want := "current"
		if w < 68 {
			want = "Standard · current"
		}
		if !strings.Contains(s, want) {
			t.Errorf("w=%d: no %q on the screen:\n%s", w, want, s)
		}
	}
}

// Fault: Agent and Full on the Configure table acting differently from
// Install's table (starting a run, reaching the confirm screen, or
// nothing at all). Enter opens Install's "isn't available yet" dead end
// with its Back row; Back (and Esc) returns to the Configure table with
// the caret on the row it left, and no run starts. Paired with
// Documents, which with enough memory still reaches the confirm screen.
func TestConfigure286_AgentAndFullOpenInstallsNotYetDeadEndAndBackReturnsToTheirRow(t *testing.T) {
	for _, row := range []string{"Agent", "Full"} {
		for _, back := range []string{"Back row", "Esc"} {
			t.Run(row+"/"+back, func(t *testing.T) {
				target, out := docs260Recorder(t)
				cfg286Folder(t, target, "-", true)
				m, queue := cfg286App(t, target)
				m = cfg286Open(t, m)
				m = cfg286TableMoveTo(t, m, row)
				m, cmd := updateAs(t, m, key(tea.KeyEnter))
				m = driveQuiet(t, m, cmd, settleQuiet, queue)
				s := screen(m)
				for _, want := range []string{
					"This profile isn't available yet",
					"Agent and Full need a local model, which Orbit doesn't use yet.",
				} {
					if !strings.Contains(flat(s), want) {
						t.Errorf("Enter on %s: the dead end does not say %q:\n%s", row, want, s)
					}
				}
				if !strings.Contains(s, "▸ Back") {
					t.Errorf("Enter on %s: no Back row on the dead end:\n%s", row, s)
				}
				if strings.Contains(s, cfg286Header) || strings.Contains(s, "Choose a deployment profile") {
					t.Errorf("Enter on %s did not leave the table for the dead end:\n%s", row, s)
				}
				k := key(tea.KeyEnter)
				if back == "Esc" {
					k = key(tea.KeyEsc)
				}
				m, cmd = updateAs(t, m, k)
				if isQuit(cmd) {
					t.Fatalf("%s quit", back)
				}
				m = driveQuiet(t, m, cmd, settleQuiet, queue)
				cfg286OnTable(t, m)
				if _, caret := cfg286TableStatus(t, screen(m)); caret != row {
					t.Errorf("after %s the caret is on %q, want the row it left, %q:\n%s", back, caret, row, screen(m))
				}
				if got := docs260Lines(t, out, "profile"); len(got) != 0 {
					t.Errorf("an installer ran: %q", got)
				}
			})
		}
	}

	m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
	m = cfg286ToConfirm(t, m, "Documents")
	if strings.Contains(flat(screen(m)), "This profile isn't available yet") {
		t.Errorf("Documents with enough memory reached the not-yet dead end:\n%s", screen(m))
	}
}

// --- The confirm screen -------------------------------------------------------

// Fault: wrong words on the Standard → Documents confirm screen, or the
// rows missing.
func TestConfigure286_ConfirmForDocumentsSaysWhatWillHappen(t *testing.T) {
	m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
	m = cfg286ToConfirm(t, m, "Documents")
	s := screen(m)
	one := flat(s)
	for _, want := range []string{
		cfg286Identity,
		"Standard → Documents",
		"This starts the attachment reader (orbit-tika), using about 1 GB more memory.",
		"It runs as an update inside the mission console, right here: Orbit restarts briefly on the latest Orbit release. Your settings and data are kept.",
	} {
		if !strings.Contains(one, flat(want)) {
			t.Errorf("the confirm screen does not say %q:\n%s", want, s)
		}
	}
	if cfg286RowIndex(s, "Change to Documents") < 0 || cfg286RowIndex(s, "Cancel") < 0 {
		t.Errorf("the confirm screen lacks its Change to Documents / Cancel rows:\n%s", s)
	}
	if strings.Contains(one, "freeing about") {
		t.Errorf("the Documents confirm screen carries the stop wording:\n%s", s)
	}
}

// Fault: wrong words on the Documents → Standard confirm screen, or it
// reusing the start wording. Nothing stored is deleted, said plainly.
func TestConfigure286_ConfirmForStandardSaysWhatWillHappen(t *testing.T) {
	m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), "processing", true))
	m = cfg286ToConfirm(t, m, "Standard")
	s := screen(m)
	one := flat(s)
	for _, want := range []string{
		cfg286Identity,
		"Documents → Standard",
		"This stops the attachment reader (orbit-tika), freeing about 1 GB. Attachments are no longer read for text; nothing stored is deleted.",
	} {
		if !strings.Contains(one, flat(want)) {
			t.Errorf("the confirm screen does not say %q:\n%s", want, s)
		}
	}
	if cfg286RowIndex(s, "Change to Standard") < 0 || cfg286RowIndex(s, "Cancel") < 0 {
		t.Errorf("the confirm screen lacks its Change to Standard / Cancel rows:\n%s", s)
	}
	if strings.Contains(one, "using about 1 GB more memory") {
		t.Errorf("the Standard confirm screen carries the start wording:\n%s", s)
	}
}

var cfg286VersionLike = regexp.MustCompile(`\d+\.\d+\.\d+`)

// Fault (owner, 5a): the person not being told, before they confirm,
// that Configure also moves Orbit to its newest release. The line is
// its own row, above the Change row, in both directions. No version is
// claimed: the launcher does not know the newest release yet.
func TestConfigure286_ConfirmTellsThemItAlsoUpdatesOrbitOnItsOwnLine(t *testing.T) {
	for _, c := range []struct{ profiles, to string }{{"-", "Documents"}, {"processing", "Standard"}} {
		t.Run("to "+c.to, func(t *testing.T) {
			m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), c.profiles, true))
			m = cfg286ToConfirm(t, m, c.to)
			s := screen(m)
			at := cfg286RowIndex(s, cfg286AlsoLine)
			if at < 0 {
				t.Fatalf("%q is not on a row of its own:\n%s", cfg286AlsoLine, s)
			}
			if change := cfg286RowIndex(s, "Change to "+c.to); change < 0 || at >= change {
				t.Errorf("the line (row %d) is not above the Change row (row %d):\n%s", at, change, s)
			}
			if v := cfg286VersionLike.FindString(s); v != "" {
				t.Errorf("the screen shows a version-like %q the launcher cannot know:\n%s", v, s)
			}
		})
	}
}

// Fault: Esc or Cancel leaving the flow, starting the run, or landing
// anywhere but the profile screen with the caret back on the current
// row. Paired with the Change row, which starts the run.
func TestConfigure286_EscAndCancelGoBackOneStepAndStartNothing(t *testing.T) {
	for _, how := range []string{"Esc", "Cancel"} {
		t.Run(how, func(t *testing.T) {
			target, out := docs260Recorder(t)
			cfg286Folder(t, target, "-", true)
			m, queue := cfg286App(t, target)
			m = cfg286ToConfirm(t, m, "Documents")
			var cmd tea.Cmd
			if how == "Esc" {
				m, cmd = updateAs(t, m, key(tea.KeyEsc))
			} else {
				m = cfg286MoveTo(t, m, "Cancel")
				m, cmd = updateAs(t, m, key(tea.KeyEnter))
			}
			if isQuit(cmd) {
				t.Fatalf("%s quit", how)
			}
			m = driveQuiet(t, m, cmd, settleQuiet, queue)
			cfg286OnTable(t, m)
			if _, caret := cfg286TableStatus(t, screen(m)); caret != "Standard" {
				t.Errorf("the caret is on %q after %s, want the current profile, Standard:\n%s", caret, how, screen(m))
			}
			if got := docs260Lines(t, out, "profile"); len(got) != 0 {
				t.Errorf("%s still started an installer: %q", how, got)
			}
		})
	}
}

// --- The run ----------------------------------------------------------------------

// Fault: the run carrying the wrong profile, none, or the wrong action.
// Standard → Documents sends processing; Documents → Standard sends
// standard; both are an update run (--plain --update), and the profile
// travels in the environment only.
func TestConfigure286_RunIsAnUpdateCarryingTheChosenProfileInItsEnvironment(t *testing.T) {
	for _, c := range []struct{ profiles, to, want string }{
		{"-", "Documents", "processing"},
		{"processing", "Standard", "standard"},
	} {
		t.Run(c.to, func(t *testing.T) {
			target, out := docs260Recorder(t)
			cfg286Folder(t, target, c.profiles, true)
			m, queue := cfg286App(t, target)
			m = cfg286ToConfirm(t, m, c.to)
			cfg286Start(t, m, queue, c.to)
			if got := docs260Lines(t, out, "profile"); len(got) != 1 || got[0] != c.want {
				t.Errorf("the installer saw %s = %q, want exactly one run with %q", docs260Env, got, c.want)
			}
			argv := docs260Lines(t, out, "argv")
			if len(argv) != 1 || !strings.Contains(argv[0], "--plain") || !strings.Contains(argv[0], "--update") {
				t.Errorf("the installer was started with %q, want an update run (--plain --update)", argv)
			}
			docs260NoProfileInArgv(t, out)
		})
	}
}

// cfg286OpenUpdate opens Update on an installed folder, where the menu
// already rests on Update (openUpdate presses Down from Install). No
// sink: nothing is pushed before the run starts.
func cfg286OpenUpdate(t *testing.T, m AppModel) AppModel {
	t.Helper()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	return drive(t, m, cmd)
}

// Fault: Update itself starting to send a profile (the guard the
// Configure change must not break): an Update run sees the variable
// unset.
func TestConfigure286_UpdateStillSendsNoProfile(t *testing.T) {
	target, out := docs260Recorder(t)
	cfg286Folder(t, target, "processing", true)
	m, queue := cfg286App(t, target)
	m = cfg286OpenUpdate(t, m)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	drive(t, m, cmd, queue)
	if got := docs260Lines(t, out, "profile"); len(got) != 1 || got[0] != "<unset>" {
		t.Errorf("an Update run saw %s = %q, want one run with the variable unset", docs260Env, got)
	}
}

// cfg286StoppingScreen starts a run that blocks, waits until the
// installer is running, presses Ctrl-C and returns the screen. flow is
// "update" or "configure".
func cfg286StoppingScreen(t *testing.T, flow string) string {
	t.Helper()
	mark := filepath.Join(t.TempDir(), "started")
	t.Setenv("ORBIT_T286_MARK", mark)
	installScriptAt(t, ": > \"$ORBIT_T286_MARK\"\n"+
		"echo 'phase=host component=host state=completed reason=host-tools action=check elapsed=0s'\n"+
		"exec sleep 5\n")
	m, queue := cfg286App(t, cfg286Folder(t, shortTempDir(t), "-", true))
	started := func(tea.Model) bool { _, err := os.Stat(mark); return err == nil }
	var cmd tea.Cmd
	if flow == "update" {
		m = cfg286OpenUpdate(t, m)
		m, cmd = updateAs(t, m, key(tea.KeyEnter))
	} else {
		m = cfg286ToConfirm(t, m, "Documents")
		m = cfg286MoveTo(t, m, "Change to Documents")
		m, cmd = updateAs(t, m, key(tea.KeyEnter))
	}
	m = driveUntil(t, m, cmd, started, queue)
	if flow == "configure" && !strings.Contains(screen(m), "Configure — Documents") {
		t.Errorf("the mission console is not titled Configure — Documents:\n%s", screen(m))
	}
	m, _ = updateAs(t, m, ctrlC())
	return screen(m)
}

// Fault: Ctrl-C mid-run behaving differently from Update (killing the
// installer without letting it undo, or reading as a failure). Paired
// with Update itself: both say the installer is undoing what it started
// and neither calls it a failure.
func TestConfigure286_CtrlCMidRunIsWhatUpdateDoes(t *testing.T) {
	for _, flow := range []string{"update", "configure"} {
		t.Run(flow, func(t *testing.T) {
			s := cfg286StoppingScreen(t, flow)
			if !strings.Contains(flat(s), "the installer is undoing what it started") {
				t.Errorf("Ctrl-C did not start the same clean stop as Update:\n%s", s)
			}
			for _, bad := range []string{"Update stopped", "Configure stopped", "Install stopped"} {
				if strings.Contains(s, bad) {
					t.Errorf("a stop the person asked for reads as a failure (%q):\n%s", bad, s)
				}
			}
		})
	}
}

// cfg286FinishScript is an install.sh that ends well; rewrite, when
// set, first rewrites .env-orbit the way a release that honours the
// request would, with profiles as COMPOSE_PROFILES.
func cfg286FinishScript(t *testing.T, dir string, rewrite bool, profiles string) {
	t.Helper()
	t.Setenv("ORBIT_T286_DIR", dir)
	body := ""
	if rewrite {
		body += "printf 'APP_URL=https://mail.example.com\\nCOMPOSE_PROFILES=" + profiles + "\\n' > \"$ORBIT_T286_DIR/.env-orbit\"\n"
	}
	body += "echo 'phase=host component=host state=completed reason=host-tools action=check elapsed=0s'\n" +
		"echo 'phase=application component=application state=healthy reason=application-health action=health elapsed=0s'\n" +
		"echo 'phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=0s'\n" +
		"exit 0\n"
	installScriptAt(t, body)
}

// Fault: a successful change ending anywhere but the shared success
// screen, or reading a claim link (Update's success has none), or
// being reported as "didn't change" when it did. Both directions.
func TestConfigure286_SuccessIsTheSharedScreenWithoutAClaimLink(t *testing.T) {
	for _, c := range []struct{ profiles, to, after string }{
		{"-", "Documents", "processing"},
		{"processing", "Standard", ""},
	} {
		t.Run(c.to, func(t *testing.T) {
			dir := cfg286Folder(t, shortTempDir(t), c.profiles, true)
			cfg286FinishScript(t, dir, true, c.after)
			rec := &claimRecorder{link: wiringLink}
			m, queue := cfg286App(t, dir)
			m.flowReadClaim = rec.read
			m = cfg286ToConfirm(t, m, c.to)
			m = cfg286Start(t, m, queue, c.to)
			if m.state != appStateSuccess {
				t.Fatalf("the change did not reach the success screen (state %v):\n%s", m.state, screen(m))
			}
			if len(rec.calls) != 0 || m.success.readClaim != nil {
				t.Errorf("a profile change read the claim link (%d calls)", len(rec.calls))
			}
			if s := screen(m); strings.Contains(s, "Copy claim link") || strings.Contains(s, "The profile didn't change") {
				t.Errorf("wrong success screen:\n%s", s)
			}
		})
	}
}

// Fault: shipping the launcher before Orbit honours the request must be
// safe: when the profile is unchanged after a successful run the person
// is told so, honestly, with Menu; it is not shown as a success. Paired
// with the changed case above.
func TestConfigure286_ProfileThatDidNotChangeIsSaidSo(t *testing.T) {
	for _, c := range []struct{ profiles, to, kept string }{
		{"-", "Documents", "Standard"},
		{"processing", "Standard", "Documents"},
	} {
		t.Run("to "+c.to, func(t *testing.T) {
			dir := cfg286Folder(t, shortTempDir(t), c.profiles, true)
			cfg286FinishScript(t, dir, false, "")
			m, queue := cfg286App(t, dir)
			m = cfg286ToConfirm(t, m, c.to)
			m = cfg286Start(t, m, queue, c.to)
			s := screen(m)
			if m.state == appStateSuccess {
				t.Errorf("an unchanged profile reached the success screen:\n%s", s)
			}
			one := flat(s)
			for _, want := range []string{"The profile didn't change", strings.Replace(cfg286Unchanged, "%s", c.kept, 1)} {
				if !strings.Contains(one, want) {
					t.Errorf("the screen does not say %q:\n%s", want, s)
				}
			}
			if !strings.Contains(s, "▸ Menu") {
				t.Errorf("no Menu row on the unchanged screen:\n%s", s)
			}
		})
	}
}

// Fault: a failed run being shown the unchanged-profile screen (a
// failure has its own screen) or as success.
func TestConfigure286_FailedRunShowsTheEnginesFailureNotTheUnchangedScreen(t *testing.T) {
	target, _ := docs260Recorder(t)
	cfg286Folder(t, target, "-", true)
	m, queue := cfg286App(t, target)
	m = cfg286ToConfirm(t, m, "Documents")
	m = cfg286Start(t, m, queue, "Documents")
	s := screen(m)
	if m.state == appStateSuccess || strings.Contains(s, "The profile didn't change") {
		t.Errorf("a failed run reads as success or unchanged:\n%s", s)
	}
	if !strings.Contains(s, "stopped") {
		t.Errorf("a failed run shows no failure screen:\n%s", s)
	}
}
