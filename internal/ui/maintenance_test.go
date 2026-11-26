package ui

// API these tests expect, #242 (internal/ui/maintenance.go, splash.go,
// enginerun.go seams, app.go). The tests pick the plainest signatures the
// build notes leave open; the builder matches them.
//
// Menu (splash.go):
//
//	MainMenu = Install, Update, Configure, Repair, Maintenance, Remove (Gap), Exit
//	const menuMaintenance = 4 (menuInstall 0, menuUpdate 1, menuConfigure 2,
//	menuRepair 3 stay; menuExit moves to 6). A splash health answer of
//	deploy.HealthMaintenance preselects menuMaintenance unless
//	userNavigated; every other answer preselects what it did before.
//
// Pure helpers (maintenance.go):
//
//	const defaultMaintenanceNotice = "Orbit is closed for maintenance and will be back soon."
//	func parseMaintenanceEnd(input string, now time.Time, loc *time.Location) (until time.Time, ok bool)
//	    "" -> (zero Time, true). A length (h/m/d units, "1h30m", between 1m and
//	    7d inclusive, a fixed duration added to now) or HH:MM, 24-hour, on a
//	    one- or two-digit hour and two-digit minute, in loc: today if strictly
//	    after now, else tomorrow's calendar date (not +24h). Surrounding
//	    spaces are ignored. Anything else -> (zero, false).
//	func maintenanceZoneLine(now time.Time, zoneName string) string
//	    now is already in the server zone. With a name:
//	    "Server time zone: Europe/London (BST, UTC+1) · now 14:05"; without:
//	    "Server time zone: UTC+1 · now 14:05". Offsets read UTC+0, UTC+1, UTC+5:30.
//	func maintenanceBackBy(until time.Time, zoneName string) string
//	    until is already in the server zone. "Back by Wednesday 29 October, 22:30 Europe/London (server time)"
//	    (day without a leading zero); with no name the offset stands in:
//	    "... 22:30 UTC+0 (server time)".
//	func serverZoneName() string   // the IANA name from TZ (a leading ":" dropped), else /etc/localtime's target, else ""
//
// The flow (maintenance.go):
//
//	func NewMaintenanceModel(d *deploy.Deployment, targetDir, version string) MaintenanceModel
//	    d == nil is the no-deployment screen; a deployment whose folder lacks
//	    scripts/start-maintenance.sh or end-maintenance.sh
//	    (deploy.HasMaintenanceScripts false) is "Maintenance needs a newer Orbit".
//	fields tests set (unexported): detectErr error; now func() time.Time;
//	    zone *time.Location; zoneName string;
//	    probe func(ctx context.Context, healthURL string) deploy.HealthReason
//	        (called from Init's command with deploy.LocalHealthURL(d); nil probe = unknown);
//	    send func(tea.Msg) (the sink, as Update and Repair);
//	    and, through the embedded engineRunSeams (as RepairModel's prepareRepair):
//	    prepareMaintenance func(ctx context.Context, targetDir string, start bool, notice, until string) (*engine.Stream, error)
//	        nil = deploy.MaintenanceCommand + engine.Start. notice is never empty
//	        for start (the default text stands in); until is "" or RFC 3339 in UTC.
//	func (m MaintenanceModel) Init() tea.Cmd, Update, View, Outcome() flowOutcome
//	AppModel: "Maintenance" opens it (appStateMaintenance) with the app's seams, sender and folder.
//
// Decided where the spec is open: Try again re-runs the same request without
// asking again; the failed-reason sentence about the database shows for
// database-unavailable and for a failure with no reason.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

func london(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

// ---- the end-time parser ----

func TestParseMaintenanceEnd_EmptyMeansNoEnd(t *testing.T) {
	loc := london(t)
	for _, in := range []string{"", "   "} {
		got, ok := parseMaintenanceEnd(in, at(loc, 2025, 7, 1, 14, 5), loc)
		if !ok || !got.IsZero() {
			t.Errorf("%q: got %v, %v; want the zero time, accepted", in, got, ok)
		}
	}
}

func TestParseMaintenanceEnd_Lengths(t *testing.T) {
	loc := london(t)
	now := at(loc, 2025, 7, 1, 14, 5).Add(30 * time.Second)
	cases := map[string]time.Duration{
		"1m":     time.Minute,
		"45m":    45 * time.Minute,
		"2h":     2 * time.Hour,
		"1h30m":  90 * time.Minute,
		"90m":    90 * time.Minute,
		"1d":     24 * time.Hour,
		"7d":     7 * 24 * time.Hour,
		"168h":   7 * 24 * time.Hour,
		"10080m": 7 * 24 * time.Hour,
		" 45m ":  45 * time.Minute,
	}
	for in, d := range cases {
		got, ok := parseMaintenanceEnd(in, now, loc)
		if !ok || !got.Equal(now.Add(d)) {
			t.Errorf("%q: got %v, %v; want now+%v = %v", in, got, ok, d, now.Add(d))
		}
	}
}

func TestParseMaintenanceEnd_ClockTimes(t *testing.T) {
	loc := london(t)
	now := at(loc, 2025, 7, 1, 14, 5)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"22:30", at(loc, 2025, 7, 1, 22, 30)},
		{"23:59", at(loc, 2025, 7, 1, 23, 59)},
		{"14:06", at(loc, 2025, 7, 1, 14, 6)},
		{"14:05", at(loc, 2025, 7, 2, 14, 5)}, // not after now: tomorrow
		{"09:00", at(loc, 2025, 7, 2, 9, 0)},
		{"00:00", at(loc, 2025, 7, 2, 0, 0)},
		{"7:05", at(loc, 2025, 7, 2, 7, 5)},
		{" 22:30 ", at(loc, 2025, 7, 1, 22, 30)},
	}
	for _, c := range cases {
		got, ok := parseMaintenanceEnd(c.in, now, loc)
		if !ok || !got.Equal(c.want) {
			t.Errorf("%q: got %v, %v; want %v", c.in, got, ok, c.want)
		}
	}
	// Same minute with seconds already past it: still tomorrow.
	if got, ok := parseMaintenanceEnd("14:05", now.Add(30*time.Second), loc); !ok || !got.Equal(at(loc, 2025, 7, 2, 14, 5)) {
		t.Errorf("14:05 at 14:05:30: got %v, %v; want tomorrow", got, ok)
	}
}

// The zone is the server's, not now's: the same instant held in UTC reads
// the clock time in loc.
func TestParseMaintenanceEnd_ClockTimeIsInTheGivenZoneNotNowsZone(t *testing.T) {
	loc := london(t)
	now := time.Date(2025, 7, 1, 12, 5, 0, 0, time.UTC) // 13:05 in London (BST)
	got, ok := parseMaintenanceEnd("14:00", now, loc)
	if want := time.Date(2025, 7, 1, 13, 0, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("got %v, %v; want %v (14:00 London, today)", got, ok, want)
	}
	got, ok = parseMaintenanceEnd("13:00", now, loc)
	if want := time.Date(2025, 7, 2, 12, 0, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("got %v, %v; want %v (13:00 London already past, so tomorrow)", got, ok, want)
	}
}

// The last Sunday of October 2025 is the 26th: London leaves summer time at
// 02:00 BST (01:00 UTC), so that day has 25 hours.
func TestParseMaintenanceEnd_AcrossTheAutumnClockChange(t *testing.T) {
	loc := london(t)

	// Tomorrow's 22:30 is after the change: 22:30 GMT = 22:30 UTC, not
	// 21:30 UTC (+24h of BST would give that).
	now := at(loc, 2025, 10, 25, 23, 0)
	got, ok := parseMaintenanceEnd("22:30", now, loc)
	if want := time.Date(2025, 10, 26, 22, 30, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("22:30 rolled across the change: got %v, %v; want %v", got, ok, want)
	}

	// Today's 03:00 is after the change: 03:00 GMT = 03:00 UTC, an hour
	// later than "now + 2h30m" of wall time would say.
	now = at(loc, 2025, 10, 26, 0, 30)
	got, ok = parseMaintenanceEnd("03:00", now, loc)
	if want := time.Date(2025, 10, 26, 3, 0, 0, 0, time.UTC); !ok || !got.Equal(want) {
		t.Errorf("03:00 across the change: got %v, %v; want %v", got, ok, want)
	}

	// A length is a fixed duration, whatever the wall clock does.
	got, ok = parseMaintenanceEnd("3h", now, loc)
	if !ok || !got.Equal(now.Add(3*time.Hour)) {
		t.Errorf("3h across the change: got %v, %v; want now+3h", got, ok)
	}
}

func TestParseMaintenanceEnd_Refusals(t *testing.T) {
	loc := london(t)
	now := at(loc, 2025, 7, 1, 14, 5)
	for _, in := range []string{
		"abc", "0m", "0h", "8d", "169h", "10081m", "7d1m",
		"24:00", "25:00", "12:60", "7:5", "22.30", "22:3", "123:00", ":30", "22:",
		"-5m", "45", "1.5h", "m", "h", "45 m", "1h 30m", "two hours", "22:30pm", "2025-07-01T22:30:00Z",
	} {
		if got, ok := parseMaintenanceEnd(in, now, loc); ok {
			t.Errorf("%q accepted as %v; it is neither a length nor a time", in, got)
		}
	}
}

// ---- the zone and date lines ----

func TestMaintenanceZoneLine(t *testing.T) {
	loc := london(t)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		now  time.Time
		zone string
		want string
	}{
		{"summer with a name", at(loc, 2025, 7, 1, 14, 5), "Europe/London", "Server time zone: Europe/London (BST, UTC+1) · now 14:05"},
		{"winter with a name", at(loc, 2025, 12, 1, 9, 7), "Europe/London", "Server time zone: Europe/London (GMT, UTC+0) · now 09:07"},
		{"a half-hour zone", at(kolkata, 2025, 7, 1, 14, 5), "Asia/Kolkata", "Server time zone: Asia/Kolkata (IST, UTC+5:30) · now 14:05"},
		{"summer without a name", at(loc, 2025, 7, 1, 14, 5), "", "Server time zone: UTC+1 · now 14:05"},
		{"winter without a name", at(loc, 2025, 12, 1, 9, 7), "", "Server time zone: UTC+0 · now 09:07"},
		{"a half-hour zone without a name", at(kolkata, 2025, 7, 1, 14, 5), "", "Server time zone: UTC+5:30 · now 14:05"},
	}
	for _, c := range cases {
		if got := maintenanceZoneLine(c.now, c.zone); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestMaintenanceBackBy(t *testing.T) {
	loc := london(t)
	cases := []struct {
		until time.Time
		zone  string
		want  string
	}{
		{at(loc, 2025, 10, 29, 22, 30), "Europe/London", "Back by Wednesday 29 October, 22:30 Europe/London (server time)"},
		{at(loc, 2025, 10, 6, 9, 5), "Europe/London", "Back by Monday 6 October, 09:05 Europe/London (server time)"},
		{at(loc, 2025, 10, 29, 22, 30), "", "Back by Wednesday 29 October, 22:30 UTC+0 (server time)"},
		{at(loc, 2025, 7, 1, 22, 30), "", "Back by Tuesday 1 July, 22:30 UTC+1 (server time)"},
	}
	for _, c := range cases {
		if got := maintenanceBackBy(c.until, c.zone); got != c.want {
			t.Errorf("\n got %q\nwant %q", got, c.want)
		}
	}
}

func TestServerZoneName_ReadsTZ(t *testing.T) {
	t.Setenv("TZ", "Europe/London")
	if got := serverZoneName(); got != "Europe/London" {
		t.Errorf("TZ=Europe/London: got %q", got)
	}
	t.Setenv("TZ", ":Europe/London")
	if got := serverZoneName(); got != "Europe/London" {
		t.Errorf("TZ=:Europe/London: got %q, want the leading colon dropped", got)
	}
}

// ---- the main menu and the splash ----

func TestMainMenu_MaintenanceSitsBetweenRepairAndRemove(t *testing.T) {
	var labels []string
	for _, item := range MainMenu {
		labels = append(labels, item.Label)
	}
	want := []string{"Install", "Update", "Configure", "Repair", "Maintenance", "Remove", "Exit"}
	if !slices.Equal(labels, want) {
		t.Fatalf("MainMenu = %q, want %q", labels, want)
	}
	for i, item := range MainMenu {
		if wantGap := item.Label == "Remove"; item.Gap != wantGap {
			t.Errorf("row %d %s: Gap = %v, want %v (Remove keeps its gap; no other row has one)", i, item.Label, item.Gap, wantGap)
		}
	}
	if menuInstall != 0 || menuUpdate != 1 || menuConfigure != 2 || menuRepair != 3 || menuMaintenance != 4 {
		t.Errorf("indices: install %d update %d configure %d repair %d maintenance %d; want 0 1 2 3 4",
			menuInstall, menuUpdate, menuConfigure, menuRepair, menuMaintenance)
	}
	if menuExit != len(MainMenu)-1 || MainMenu[menuExit].Label != "Exit" {
		t.Errorf("menuExit = %d, want the last row", menuExit)
	}
}

func TestSplashMaintenance242_ClosedAnswerPreselectsTheMaintenanceRow(t *testing.T) {
	m := degradedWith(t, deploy.HealthMaintenance)
	if m.menu.sel != menuMaintenance {
		t.Errorf("selected = %d, want Maintenance (%d)", m.menu.sel, menuMaintenance)
	}
	if m.userNavigated {
		t.Error("userNavigated set by a probe result")
	}
	assertCaretOn(t, screen(m), "Maintenance")
}

func TestSplashMaintenance242_ACaretTheUserPlacedStays(t *testing.T) {
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26}, runeKey('1'))
	m, _ = updateAs(t, m, healthResultMsg{reason: deploy.HealthMaintenance})
	if m.menu.sel != menuInstall || !m.userNavigated {
		t.Errorf("selected = %d, userNavigated = %v; the probe must not move a caret the person placed", m.menu.sel, m.userNavigated)
	}
}

func TestSplashMaintenance242_OtherAnswersPreselectAsBefore(t *testing.T) {
	if m := degradedWith(t, deploy.HealthResponding); m.menu.sel != menuUpdate {
		t.Errorf("alive: selected = %d, want Update", m.menu.sel)
	}
	if m := degradedWith(t, deploy.HealthNotResponding); m.menu.sel != menuRepair {
		t.Errorf("degraded: selected = %d, want Repair", m.menu.sel)
	}
}

// ---- fixtures ----

// maintDeployment is an installed folder: .env-orbit (published at
// 192.0.2.5:8080), and, when withScripts, both maintenance scripts.
func maintDeployment(t *testing.T, withScripts bool) (string, *deploy.Deployment) {
	t.Helper()
	t.Setenv("ORBIT_BIND_ADDRESS", "")
	t.Setenv("ORBIT_PORT", "")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile), "ORBIT_BIND_ADDRESS=192.0.2.5", "ORBIT_PORT=8080")
	var files []testutil.File
	if withScripts {
		files = []testutil.File{
			{Path: "scripts/start-maintenance.sh", Body: "#!/usr/bin/env bash\n", Mode: 0o700},
			{Path: "scripts/end-maintenance.sh", Body: "#!/usr/bin/env bash\n", Mode: 0o700},
		}
	}
	testutil.StageTree(t, dir, 0o755, files...)
	d, err := deploy.Detect(dir)
	if err != nil || d == nil {
		t.Fatalf("Detect: %v, %v", d, err)
	}
	return dir, d
}

type maintCall struct {
	start         bool
	notice, until string
}

// fakeMaintenance stands in for the deployment's scripts: every call is
// recorded and runs script under bash (real stdout, stderr and exit code).
func fakeMaintenance(calls *[]maintCall, script string) func(context.Context, string, bool, string, string) (*engine.Stream, error) {
	return func(_ context.Context, _ string, start bool, notice, until string) (*engine.Stream, error) {
		*calls = append(*calls, maintCall{start, notice, until})
		return engine.Start(exec.Command("bash", "-c", script))
	}
}

// newMaint is a sized flow on a deployment with scripts, the server clock
// fixed at Tuesday 1 July 2025, 14:05 in London.
func newMaint(t *testing.T, script string) (MaintenanceModel, *[]maintCall, *sink) {
	t.Helper()
	dir, d := maintDeployment(t, true)
	calls := &[]maintCall{}
	s := newSink()
	m := NewMaintenanceModel(d, dir, "v9.9.9")
	loc := london(t)
	m.now = func() time.Time { return at(loc, 2025, 7, 1, 14, 5) }
	m.zone, m.zoneName = loc, "Europe/London"
	m.prepareMaintenance = fakeMaintenance(calls, script)
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	return m, calls, s
}

const (
	startedLine = "echo 'maintenance action=start result=started until=none'"
	endedLine   = "echo 'maintenance action=end result=ended cancelled=0'"
)

var maintLabels = map[string]bool{
	"Close Orbit for maintenance": true, "Reopen Orbit": true, "Cancel": true, "Close Orbit": true,
	"Menu": true, "Exit": true, "Try again": true,
}

func maintRows(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if row := screenRow(line); maintLabels[row] {
			out = append(out, row)
		}
	}
	return out
}

func maintCaret(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "▸") {
			return screenRow(line)
		}
	}
	return ""
}

func typed(t *testing.T, m MaintenanceModel, text string) MaintenanceModel {
	t.Helper()
	for _, r := range text {
		m, _ = updateAs(t, m, runeKey(r))
	}
	return m
}

func has(t *testing.T, m tea.Model, subs ...string) {
	t.Helper()
	s := screen(m)
	flat := flatten(s)
	for _, sub := range subs {
		// Prose wraps at the screen's width, so a sentence is found
		// with the line breaks and padding taken out.
		if !strings.Contains(s, sub) && !strings.Contains(flat, flatten(sub)) {
			t.Errorf("screen lacks %q:\n%s", sub, s)
		}
	}
}

// flatten joins a screen's lines, each trimmed of padding and the sky,
// into one line with single spaces.
func flatten(s string) string {
	sky := func(f string) bool { return len([]rune(f)) == 1 && strings.Contains(".·✦●", f) }
	var words []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		for len(f) > 0 && sky(f[0]) {
			f = f[1:]
		}
		for len(f) > 0 && sky(f[len(f)-1]) {
			f = f[:len(f)-1]
		}
		words = append(words, f...)
	}
	return strings.Join(words, " ")
}

func lacks(t *testing.T, m tea.Model, subs ...string) {
	t.Helper()
	s := screen(m)
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("screen holds %q:\n%s", sub, s)
		}
	}
}

// Screen navigation, by what each screen is titled.
const (
	titleChoose  = "Maintenance"
	titleNotice  = "What should people see?"
	titleWhen    = "When will Orbit be back?"
	titleConfirm = "Close Orbit for maintenance?"
	titleReopen  = "Reopen Orbit?"
)

func toNotice(t *testing.T, m MaintenanceModel) MaintenanceModel {
	t.Helper()
	m, _ = updateAs(t, m, key(tea.KeyEnter)) // Close Orbit for maintenance
	has(t, m, titleNotice)
	return m
}

func toWhen(t *testing.T, m MaintenanceModel, notice string) MaintenanceModel {
	t.Helper()
	m = typed(t, toNotice(t, m), notice)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	has(t, m, titleWhen)
	return m
}

func toConfirm(t *testing.T, m MaintenanceModel, notice, when string) MaintenanceModel {
	t.Helper()
	m = typed(t, toWhen(t, m, notice), when)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	has(t, m, titleConfirm)
	return m
}

func toReopen(t *testing.T, m MaintenanceModel) MaintenanceModel {
	t.Helper()
	m, _ = updateAs(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // Reopen Orbit
	has(t, m, titleReopen)
	return m
}

// pump runs cmd and every command and pushed message that follows, the way
// the program would, until done holds or ten seconds pass. quit reports a
// tea.Quit seen on the way.
func pump(t *testing.T, m MaintenanceModel, cmd tea.Cmd, s *sink, done func(MaintenanceModel, bool) bool) (MaintenanceModel, bool) {
	t.Helper()
	return pumpFor(t, m, cmd, s, done, 0)
}

// pumpFor is pump that, with window > 0, ignores done and just takes
// everything that arrives for that long: for asserting a state nothing
// more will change.
func pumpFor(t *testing.T, m MaintenanceModel, cmd tea.Cmd, s *sink, done func(MaintenanceModel, bool) bool, window time.Duration) (MaintenanceModel, bool) {
	t.Helper()
	// The one command driver (helpers_test.go) does the running; the
	// watcher only notes a quit passing through.
	var w maintWatch
	if window > 0 {
		w = driveQuiet(t, maintWatch{MaintenanceModel: m}, cmd, window, s)
	} else {
		w = driveUntil(t, maintWatch{MaintenanceModel: m}, cmd, func(tm tea.Model) bool {
			cur := tm.(maintWatch)
			return done(cur.MaintenanceModel, cur.quit)
		}, s)
	}
	return w.MaintenanceModel, w.quit
}

// maintWatch is a MaintenanceModel that remembers a tea.Quit sent to it.
type maintWatch struct {
	MaintenanceModel
	quit bool
}

func (w maintWatch) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.QuitMsg); ok {
		w.quit = true
		return w, nil
	}
	updated, cmd := w.MaintenanceModel.Update(msg)
	w.MaintenanceModel = updated.(MaintenanceModel)
	return w, cmd
}

func untilScreen(sub string) func(MaintenanceModel, bool) bool {
	return func(m MaintenanceModel, _ bool) bool { return strings.Contains(screen(m), sub) }
}

// runTo confirms the current screen's first row and pumps to the screen
// holding want.
func runTo(t *testing.T, m MaintenanceModel, s *sink, want string) MaintenanceModel {
	t.Helper()
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m, _ = pump(t, m, cmd, s, untilScreen(want))
	return m
}

// ---- dead ends ----

func TestMaintenanceModel_NoDeploymentIsADeadEndWithMenuOnly(t *testing.T) {
	m := NewMaintenanceModel(nil, t.TempDir(), "v9.9.9")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	has(t, m, "No Orbit deployment found in",
		"There's nothing to put into maintenance in this directory —",
		"Install is the way to get into Orbit first.")
	if got := maintRows(screen(m)); !slices.Equal(got, []string{"Menu"}) {
		t.Fatalf("rows = %q, want just Menu", got)
	}
	m2, cmd := updateAs(t, m, key(tea.KeyEnter))
	if isQuit(cmd) || !leftForMenu(m2.Outcome()) {
		t.Fatalf("Enter on Menu: quit=%v outcome=%+v, want back to the menu", isQuit(cmd), m2.Outcome())
	}
	m3, _ := updateAs(t, m, key(tea.KeyEsc))
	if !leftForMenu(m3.Outcome()) {
		t.Errorf("Esc on the first screen: outcome=%+v, want back to the menu", m3.Outcome())
	}
}

func TestMaintenanceModel_UnreadableEnvIsTheUnreadableScreen(t *testing.T) {
	m := NewMaintenanceModel(nil, "/opt/orbit", "v9.9.9")
	m.detectErr = fmt.Errorf("open /opt/orbit/.env-orbit: permission denied")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	has(t, m, "Couldn't read .env-orbit in /opt/orbit", "permission denied")
	lacks(t, m, "No Orbit deployment found", "nothing to put into maintenance")
	if got := maintRows(screen(m)); !slices.Equal(got, []string{"Menu"}) {
		t.Fatalf("rows = %q, want just Menu", got)
	}
}

func TestMaintenanceModel_AnOrbitWithoutTheScriptsNeedsAnUpdate(t *testing.T) {
	dir, d := maintDeployment(t, false)
	var calls []maintCall
	m := NewMaintenanceModel(d, dir, "v9.9.9")
	m.prepareMaintenance = fakeMaintenance(&calls, startedLine)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	has(t, m, "Maintenance needs a newer Orbit",
		"This Orbit release doesn't ship its maintenance commands yet. Update brings them. Until then this reopens Orbit from the deployment folder:",
		"bash scripts/end-maintenance.sh")
	if got := maintRows(screen(m)); !slices.Equal(got, []string{"Menu", "Exit"}) {
		t.Fatalf("rows = %q, want Menu, Exit", got)
	}
	// Menu leaves, Exit quits, and nothing ran.
	m2, _ := updateAs(t, m, key(tea.KeyEnter))
	if !leftForMenu(m2.Outcome()) {
		t.Errorf("Menu: outcome=%+v", m2.Outcome())
	}
	_, cmd := updateAs(t, m, key(tea.KeyDown), key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Error("Exit did not quit")
	}
	if len(calls) != 0 {
		t.Errorf("a dead end ran the engine: %v", calls)
	}
}

// ---- M1: choose, and the status line ----

func TestMaintenanceModel_ChooseScreenRowsAndNoStatusWhileUnknown(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	has(t, m, titleChoose, "mail.example.com")
	if got := maintRows(screen(m)); !slices.Equal(got, []string{"Close Orbit for maintenance", "Reopen Orbit", "Cancel"}) {
		t.Fatalf("rows = %q", got)
	}
	lacks(t, m, "Orbit is open to users", "Orbit is closed to users right now")
}

func TestMaintenanceModel_StatusLineFollowsTheHealthAnswer(t *testing.T) {
	cases := []struct {
		reason  deploy.HealthReason
		want    string
		notWant string
	}{
		{deploy.HealthMaintenance, "Orbit is closed to users right now", "Orbit is open to users"},
		{deploy.HealthResponding, "Orbit is open to users", "closed to users right now"},
		{deploy.HealthUnchecked, "", "Orbit is"},
		{deploy.HealthNotResponding, "", "Orbit is"},
	}
	for _, c := range cases {
		t.Run(string(c.reason), func(t *testing.T) {
			m, _, s := newMaint(t, startedLine)
			var mu sync.Mutex
			var asked []string
			m.probe = func(_ context.Context, url string) deploy.HealthReason {
				mu.Lock()
				defer mu.Unlock()
				asked = append(asked, url)
				return c.reason
			}
			m, _ = pumpFor(t, m, m.Init(), s, nil, 400*time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"http://192.0.2.5:8080/api/health"}; !slices.Equal(asked, want) {
				t.Errorf("probe asked %q, want %q (the deployment's own health address, once)", asked, want)
			}
			if c.want != "" {
				has(t, m, c.want)
			}
			lacks(t, m, c.notWant)
			// A status line is never a gate: both rows stay, in order.
			if got := maintRows(screen(m)); !slices.Equal(got, []string{"Close Orbit for maintenance", "Reopen Orbit", "Cancel"}) {
				t.Errorf("rows = %q", got)
			}
		})
	}
}

func TestMaintenanceModel_NoProbeSeamMeansUnknown(t *testing.T) {
	m, _, s := newMaint(t, startedLine)
	m, _ = pumpFor(t, m, m.Init(), s, nil, 200*time.Millisecond)
	lacks(t, m, "Orbit is open to users", "closed to users right now")
}

func TestMaintenanceModel_EscAndCancelLeaveForTheMenuCtrlCQuits(t *testing.T) {
	m, calls, _ := newMaint(t, startedLine)
	m2, _ := updateAs(t, m, key(tea.KeyEsc))
	if !leftForMenu(m2.Outcome()) {
		t.Errorf("Esc: outcome=%+v, want back to the menu", m2.Outcome())
	}
	m3, _ := updateAs(t, m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)) // Cancel
	if !leftForMenu(m3.Outcome()) {
		t.Errorf("Cancel: outcome=%+v, want back to the menu", m3.Outcome())
	}
	if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
		t.Error("Ctrl-C on the first screen did not quit")
	}
	if len(*calls) != 0 {
		t.Errorf("leaving ran the engine: %v", *calls)
	}
}

// ---- S1 notice ----

func TestMaintenanceModel_NoticeScreenProseAndDefault(t *testing.T) {
	m, calls, _ := newMaint(t, startedLine)
	m = toNotice(t, m)
	has(t, m, "One short notice, shown on the maintenance page. Leave it empty for: Orbit is closed for maintenance and will be back soon.")
	// Empty is accepted and goes on; the confirm screen names the default.
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	has(t, m, titleWhen)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	has(t, m, titleConfirm, "Notice: "+defaultMaintenanceNotice)
	if len(*calls) != 0 {
		t.Errorf("the engine ran before the confirm: %v", *calls)
	}
}

func TestMaintenanceModel_NoticeRefusalsAreInlineAndStayOnTheScreen(t *testing.T) {
	cases := []struct {
		name string
		feed func(*testing.T, MaintenanceModel) MaintenanceModel
	}{
		{"over 500 characters", func(t *testing.T, m MaintenanceModel) MaintenanceModel {
			m, _ = updateAs(t, m, tea.PasteMsg{Content: strings.Repeat("a", 501)})
			return m
		}},
		{"a control character", func(t *testing.T, m MaintenanceModel) MaintenanceModel {
			m, _ = updateAs(t, m, tea.KeyPressMsg{Code: 'x', Text: "bad\x07bell"})
			return m
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _, _ := newMaint(t, startedLine)
			m = c.feed(t, toNotice(t, m))
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			if cmd != nil && isQuit(cmd) {
				t.Fatal("Enter quit")
			}
			has(t, m, titleNotice)
			lacks(t, m, titleWhen)
		})
	}
}

func TestMaintenanceModel_NoticeOf500CharactersIsAccepted(t *testing.T) {
	m, calls, s := newMaint(t, startedLine)
	notice := strings.Repeat("é", 500) // characters, not bytes
	m = toNotice(t, m)
	m, _ = updateAs(t, m, tea.PasteMsg{Content: notice})
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	has(t, m, titleWhen)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	m = runTo(t, m, s, "Orbit is closed for maintenance")
	if len(*calls) != 1 || (*calls)[0].notice != notice {
		t.Errorf("calls = %+v, want the 500-character notice passed whole", *calls)
	}
}

func TestMaintenanceModel_EscStepsBackOneScreenAtATime(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m = toConfirm(t, m, "Back soon", "45m")
	m, _ = updateAs(t, m, key(tea.KeyEsc))
	has(t, m, titleWhen)
	m, _ = updateAs(t, m, key(tea.KeyEsc))
	has(t, m, titleNotice)
	m, _ = updateAs(t, m, key(tea.KeyEsc))
	has(t, m, titleChoose)
	lacks(t, m, titleNotice)
	if m.Outcome().Done {
		t.Fatal("Esc from the notice screen left the flow; it goes to the choice")
	}
	m, _ = updateAs(t, m, key(tea.KeyEsc))
	if !leftForMenu(m.Outcome()) {
		t.Errorf("Esc from the choice: outcome=%+v, want back to the menu", m.Outcome())
	}
}

// ---- S2 when back ----

func TestMaintenanceModel_WhenScreenProseAndZoneLine(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m = toWhen(t, m, "")
	has(t, m, "A length like 45m or 2h, or a time like 22:30. Leave it empty if you can't say.",
		"Server time zone: Europe/London (BST, UTC+1) · now 14:05")
}

func TestMaintenanceModel_WhenScreenZoneLineWithoutAName(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m.zoneName = ""
	m = toWhen(t, m, "")
	has(t, m, "Server time zone: UTC+1 · now 14:05")
	lacks(t, m, "Europe/London")
}

func TestMaintenanceModel_WhenRefusalsAreInlineAndStayOnTheScreen(t *testing.T) {
	for _, in := range []string{"abc", "0m", "8d", "24:00", "7:5", "22.30"} {
		t.Run(in, func(t *testing.T) {
			m, calls, _ := newMaint(t, startedLine)
			m = typed(t, toWhen(t, m, ""), in)
			m, _ = updateAs(t, m, key(tea.KeyEnter))
			has(t, m, titleWhen, "That isn't a length or a time — try 45m, 2h or 22:30")
			lacks(t, m, titleConfirm)
			if len(*calls) != 0 {
				t.Errorf("ran the engine: %v", *calls)
			}
		})
	}
}

func TestMaintenanceModel_WhenAcceptedAnswersGoToTheConfirm(t *testing.T) {
	for _, in := range []string{"", "45m", "2h", "1h30m", "22:30", "09:00"} {
		t.Run("in="+in, func(t *testing.T) {
			m, _, _ := newMaint(t, startedLine)
			toConfirm(t, m, "", in)
		})
	}
}

// ---- S3 confirm ----

func TestMaintenanceModel_ConfirmNamesTheNoticeTheFullEndAndWhatItDoes(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m = toConfirm(t, m, "Back after lunch", "22:30")
	has(t, m, "mail.example.com", "Notice: Back after lunch",
		"Back by Tuesday 1 July, 22:30 Europe/London (server time)",
		"Everyone but administrators sees the maintenance page from now. Administrators can still sign in; the sign-in page stays open.")
	if got := maintRows(screen(m)); !slices.Equal(got, []string{"Close Orbit", "Cancel"}) {
		t.Fatalf("rows = %q, want Close Orbit, Cancel", got)
	}
	lacks(t, m, "No end time stated")
}

func TestMaintenanceModel_ConfirmWithNoEndTimeSaysSo(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m = toConfirm(t, m, "", "")
	has(t, m, "No end time stated", "Notice: "+defaultMaintenanceNotice)
	lacks(t, m, "Back by")
}

func TestMaintenanceModel_ConfirmRollingToTomorrowShowsTomorrowsDate(t *testing.T) {
	m, _, _ := newMaint(t, startedLine)
	m = toConfirm(t, m, "", "09:00")
	has(t, m, "Back by Wednesday 2 July, 09:00 Europe/London (server time)")
}

func TestMaintenanceModel_CancelAtConfirmStartsNothing(t *testing.T) {
	m, calls, _ := newMaint(t, startedLine)
	m = toConfirm(t, m, "x", "45m")
	m, cmd := updateAs(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // Cancel
	if cmd != nil && isQuit(cmd) {
		t.Fatal("Cancel quit")
	}
	if !leftForMenu(m.Outcome()) {
		t.Errorf("outcome=%+v, want back to the menu", m.Outcome())
	}
	if len(*calls) != 0 {
		t.Errorf("Cancel ran the engine: %v", *calls)
	}
}

// ---- E1 reopen ----

func TestMaintenanceModel_ReopenConfirmWordsAndRows(t *testing.T) {
	m, calls, _ := newMaint(t, endedLine)
	m = toReopen(t, m)
	has(t, m, "Ends maintenance now. Users get back in straight away; a scheduled notice already due is cancelled as well.")
	if got := maintRows(screen(m)); !slices.Equal(got, []string{"Reopen Orbit", "Cancel"}) {
		t.Fatalf("rows = %q", got)
	}
	m2, _ := updateAs(t, m, key(tea.KeyEsc))
	has(t, m2, titleChoose)
	lacks(t, m2, titleReopen)
	m3, _ := updateAs(t, m, key(tea.KeyDown), key(tea.KeyEnter))
	if !leftForMenu(m3.Outcome()) {
		t.Errorf("Cancel: outcome=%+v", m3.Outcome())
	}
	if len(*calls) != 0 {
		t.Errorf("ran the engine: %v", *calls)
	}
}

// ---- the run: exact arguments ----

func TestMaintenanceModel_StartPassesNoticeAndUTCUntil(t *testing.T) {
	cases := []struct {
		name, notice, when string
		want               maintCall
	}{
		{"length", "Back soon", "45m", maintCall{true, "Back soon", "2025-07-01T13:50:00Z"}},
		{"clock time, summer time converted to UTC", "Back soon", "22:30", maintCall{true, "Back soon", "2025-07-01T21:30:00Z"}},
		{"clock time rolling to tomorrow", "Back soon", "09:00", maintCall{true, "Back soon", "2025-07-02T08:00:00Z"}},
		{"no end time: no until", "Back soon", "", maintCall{true, "Back soon", ""}},
		{"empty notice: the default text", "", "2h", maintCall{true, defaultMaintenanceNotice, "2025-07-01T15:05:00Z"}},
		{"notice with shell characters kept whole", "it's `$(x)` & \"y\"", "", maintCall{true, "it's `$(x)` & \"y\"", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, calls, s := newMaint(t, startedLine)
			m = toConfirm(t, m, c.notice, c.when)
			runTo(t, m, s, "Orbit is closed for maintenance")
			if len(*calls) != 1 || (*calls)[0] != c.want {
				t.Errorf("calls = %+v, want exactly %+v", *calls, c.want)
			}
		})
	}
}

func TestMaintenanceModel_ReopenRunsTheEndScript(t *testing.T) {
	m, calls, s := newMaint(t, endedLine)
	m = toReopen(t, m)
	runTo(t, m, s, "Orbit is open again")
	if len(*calls) != 1 || (*calls)[0].start {
		t.Errorf("calls = %+v, want one end call", *calls)
	}
}

func TestMaintenanceModel_WhileWorkingTheScreenSaysSoAndOffersNoRows(t *testing.T) {
	m, _, _ := newMaint(t, "sleep 5")
	m = toConfirm(t, m, "x", "")
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	has(t, m, "closing Orbit for maintenance", "a few seconds; Orbit's database must be running")
	if got := maintRows(screen(m)); len(got) != 0 {
		t.Errorf("rows while working = %q, want none", got)
	}
	r, _, _ := newMaint(t, "sleep 5")
	r = toReopen(t, r)
	r, _ = updateAs(t, r, key(tea.KeyEnter))
	has(t, r, "reopening Orbit", "a few seconds; Orbit's database must be running")
}

// ---- outcomes ----

func TestMaintenanceModel_OutcomeScreens(t *testing.T) {
	cases := []struct {
		name     string
		reopen   bool
		script   string
		want     []string
		notWant  []string
		rows     []string
		minGlyph string
	}{
		{"started", false, startedLine,
			[]string{"Orbit is closed for maintenance", "Visitors see the maintenance page; administrators can still sign in. Reopen from this menu or Orbit's Administration screen."},
			[]string{"Couldn't", "already closed"}, []string{"Menu", "Exit"}, "✓"},
		{"started with an end time", false, "echo 'maintenance action=start result=started until=2025-07-01T21:30:00Z'",
			[]string{"Orbit is closed for maintenance"}, []string{"Couldn't"}, []string{"Menu", "Exit"}, "✓"},
		{"already active", false, "echo 'maintenance action=start result=already-active until=none'",
			[]string{"Orbit was already closed for maintenance", "Nothing changed; the notice and end time you entered were not applied."},
			[]string{"Orbit is closed for maintenance", "Couldn't"}, []string{"Menu", "Exit"}, "⟡"},
		{"ended", true, endedLine,
			[]string{"Orbit is open again", "Users are back in."},
			[]string{"cancelled", "Couldn't"}, []string{"Menu", "Exit"}, "✓"},
		{"ended and cancelled two", true, "echo 'maintenance action=end result=ended cancelled=2'",
			[]string{"Orbit is open again", "2 due scheduled notices were cancelled too."}, nil, []string{"Menu", "Exit"}, "✓"},
		{"ended and cancelled one", true, "echo 'maintenance action=end result=ended cancelled=1'",
			[]string{"Orbit is open again", "1 due scheduled notice"}, []string{"1 due scheduled notices"}, []string{"Menu", "Exit"}, "✓"},
		{"not active", true, "echo 'maintenance action=end result=not-active cancelled=0'",
			[]string{"Maintenance was not active", "Orbit was already open; nothing changed."},
			[]string{"Orbit is open again", "Couldn't"}, []string{"Menu", "Exit"}, "⟡"},
		{"unknown fields are ignored", true, "echo 'maintenance action=end result=ended cancelled=0 future=1 note=x'",
			[]string{"Orbit is open again"}, nil, []string{"Menu", "Exit"}, "✓"},
		{"a line among other output", false, "echo 'Starting…'; " + startedLine + "; echo 'bye' >&2",
			[]string{"Orbit is closed for maintenance"}, nil, []string{"Menu", "Exit"}, "✓"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _, s := newMaint(t, c.script)
			if c.reopen {
				m = toReopen(t, m)
			} else {
				m = toConfirm(t, m, "", "")
			}
			m = runTo(t, m, s, c.want[0])
			has(t, m, c.want...)
			lacks(t, m, c.notWant...)
			has(t, m, c.minGlyph)
			if got := maintRows(screen(m)); !slices.Equal(got, c.rows) {
				t.Errorf("rows = %q, want %q", got, c.rows)
			}
			if maintCaret(screen(m)) != "Menu" {
				t.Errorf("the caret is on %q, want Menu", maintCaret(screen(m)))
			}
			if o := m.Outcome(); o.Done {
				t.Errorf("an outcome screen is not yet leaving: %+v", o)
			}
		})
	}
}

func TestMaintenanceModel_OutcomeRowsLeaveOrQuit(t *testing.T) {
	m, _, s := newMaint(t, startedLine)
	m = runTo(t, toConfirm(t, m, "", ""), s, "Orbit is closed for maintenance")
	m2, _ := updateAs(t, m, key(tea.KeyEnter)) // Menu
	if !leftForMenu(m2.Outcome()) {
		t.Errorf("Menu: outcome=%+v", m2.Outcome())
	}
	_, cmd := updateAs(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // Exit
	if !isQuit(cmd) {
		t.Error("Exit did not quit")
	}
	m3, _ := updateAs(t, m, key(tea.KeyEsc))
	if !leftForMenu(m3.Outcome()) {
		t.Errorf("Esc: outcome=%+v, want back to the menu", m3.Outcome())
	}
}

// ---- failures ----

const dbHint = "Orbit's database must be running — Repair shows whether it is."

func TestMaintenanceModel_FailedStartShowsStderrAndTryAgainFirst(t *testing.T) {
	script := "echo 'database is down' >&2; echo 'maintenance action=start result=failed reason=database-unavailable until=none'; exit 1"
	m, calls, s := newMaint(t, script)
	m = toConfirm(t, m, "Back soon", "45m")
	m = runTo(t, m, s, "Couldn't close Orbit")
	has(t, m, "✗", "database is down", dbHint)
	lacks(t, m, "Orbit is closed for maintenance", "reason=", "database-unavailable")
	if got := maintRows(screen(m)); !slices.Equal(got, []string{"Try again", "Menu", "Exit"}) {
		t.Fatalf("rows = %q, want Try again, Menu, Exit", got)
	}
	// Try again runs the same request again.
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	has(t, m, "closing Orbit for maintenance")
	pump(t, m, cmd, s, untilScreen("Couldn't close Orbit"))
	want := maintCall{true, "Back soon", "2025-07-01T13:50:00Z"}
	if len(*calls) != 2 || (*calls)[0] != want || (*calls)[1] != want {
		t.Errorf("calls = %+v, want the same request twice", *calls)
	}
}

func TestMaintenanceModel_FailedReopen(t *testing.T) {
	script := "echo 'maintenance action=end result=failed reason=database-unavailable cancelled=0'; exit 1"
	m, calls, s := newMaint(t, script)
	m = runTo(t, toReopen(t, m), s, "Couldn't reopen Orbit")
	has(t, m, "✗", dbHint)
	lacks(t, m, "Couldn't close Orbit")
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Try again
	pump(t, m, cmd, s, untilScreen("Couldn't reopen Orbit"))
	if len(*calls) != 2 || (*calls)[0].start || (*calls)[1].start {
		t.Errorf("calls = %+v, want two end calls", *calls)
	}
}

func TestMaintenanceModel_EveryFailureReasonIsAFailedScreenNeverARawCode(t *testing.T) {
	for _, reason := range []string{"database-unavailable", "notice-invalid", "until-invalid", "until-in-past", "something-new"} {
		t.Run(reason, func(t *testing.T) {
			m, _, s := newMaint(t, "echo 'maintenance action=start result=failed reason="+reason+" until=none'; exit 1")
			m = runTo(t, toConfirm(t, m, "", ""), s, "Couldn't close Orbit")
			lacks(t, m, "reason=", reason, "Orbit is closed for maintenance")
			if got := maintRows(screen(m)); !slices.Equal(got, []string{"Try again", "Menu", "Exit"}) {
				t.Errorf("rows = %q", got)
			}
		})
	}
}

func TestMaintenanceModel_FailureWithNoReasonStillSaysWhatToCheck(t *testing.T) {
	m, _, s := newMaint(t, "echo 'maintenance action=start result=failed until=none'; exit 1")
	m = runTo(t, toConfirm(t, m, "", ""), s, "Couldn't close Orbit")
	has(t, m, dbHint)
}

// Success is read from the outcome line and the exit code, never from prose.
func TestMaintenanceModel_ExitZeroWithoutALineIsAFailure(t *testing.T) {
	for _, script := range []string{
		"exit 0",
		"echo 'Maintenance mode started successfully.'",
		"echo 'maintenance is active'; echo 'all done' >&2",
	} {
		t.Run(script, func(t *testing.T) {
			m, _, s := newMaint(t, script)
			m = runTo(t, toConfirm(t, m, "", ""), s, "Couldn't close Orbit")
			has(t, m, "Orbit didn't report an outcome")
			lacks(t, m, "Orbit is closed for maintenance", "already closed")
			r, _, rs := newMaint(t, script)
			r = runTo(t, toReopen(t, r), rs, "Couldn't reopen Orbit")
			has(t, r, "Orbit didn't report an outcome")
			lacks(t, r, "Orbit is open again", "Maintenance was not active")
		})
	}
}

func TestMaintenanceModel_ASuccessLineWithAFailingExitIsAFailure(t *testing.T) {
	m, _, s := newMaint(t, startedLine+"; exit 1")
	m = runTo(t, toConfirm(t, m, "", ""), s, "Couldn't close Orbit")
	lacks(t, m, "Orbit is closed for maintenance")
}

func TestMaintenanceModel_ExitFiveIsTheNoDeploymentScreen(t *testing.T) {
	m, _, s := newMaint(t, "echo 'not an Orbit installation' >&2; exit 5")
	m = runTo(t, toConfirm(t, m, "", ""), s, "nothing to put into maintenance")
	has(t, m, "No Orbit deployment found in", "Install is the way to get into Orbit first.")
	lacks(t, m, "Couldn't close Orbit", "Try again")
	r, _, rs := newMaint(t, "exit 5")
	r = runTo(t, toReopen(t, r), rs, "nothing to put into maintenance")
	lacks(t, r, "Couldn't reopen Orbit")
}

func TestMaintenanceModel_UsageExitIsAFailureNotASuccess(t *testing.T) {
	m, _, s := newMaint(t, "echo 'usage: start-maintenance.sh' >&2; exit 2")
	m = runTo(t, toConfirm(t, m, "", ""), s, "Couldn't close Orbit")
	has(t, m, "usage: start-maintenance.sh")
}

// A refusal to start at all (no process) is the same failed screen.
func TestMaintenanceModel_AStartErrorIsAFailedScreen(t *testing.T) {
	m, _, s := newMaint(t, startedLine)
	m.prepareMaintenance = func(context.Context, string, bool, string, string) (*engine.Stream, error) {
		return nil, fmt.Errorf("the stand-in could not start")
	}
	m = runTo(t, toConfirm(t, m, "", ""), s, "Couldn't close Orbit")
	has(t, m, "the stand-in could not start")
}

// #191: the default start goes through deploy.MaintenanceCommand, so a
// script someone else could write is refused and never run.
func TestMaintenanceModel_AnUntrustedScriptIsRefusedAndNeverRuns(t *testing.T) {
	dir, d := maintDeployment(t, true)
	marker := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "scripts", "start-maintenance.sh")
	body := "#!/usr/bin/env bash\ntouch " + marker + "\necho 'maintenance action=start result=started until=none'\n"
	if err := os.WriteFile(script, []byte(body), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o666); err != nil {
		t.Fatal(err)
	}
	s := newSink()
	m := NewMaintenanceModel(d, dir, "v9.9.9")
	m.now = func() time.Time { return at(london(t), 2025, 7, 1, 14, 5) }
	m.zone = london(t)
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	m = runTo(t, toConfirm(t, m, "", ""), s, "Couldn't close Orbit")
	has(t, m, "writable by everyone")
	lacks(t, m, "Orbit is closed for maintenance")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the untrusted start-maintenance.sh ran")
	}
}

// ---- while working ----

// One database write is under way: Ctrl-C and Esc do nothing, and a
// SIGHUP/SIGTERM waits for the step to end and then quits.
func TestMaintenanceModel_WhileWorkingCtrlCAndEscAreIgnoredAndASignalWaits(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "finished")
	script := fmt.Sprintf("sleep 1; touch %q; %s", marker, startedLine)
	m, _, s := newMaint(t, script)
	m = toConfirm(t, m, "", "")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	has(t, m, "closing Orbit for maintenance")

	for name, msg := range map[string]tea.Msg{"Ctrl-C": ctrlC(), "Esc": key(tea.KeyEsc)} {
		var c tea.Cmd
		m, c = updateAs(t, m, msg)
		if isQuit(c) {
			t.Fatalf("%s quit while the step was running", name)
		}
		if m.Outcome().Done {
			t.Fatalf("%s left the flow while the step was running: %+v", name, m.Outcome())
		}
		has(t, m, "closing Orbit for maintenance")
	}
	m, c := updateAs(t, m, SignalQuitMsg{})
	if isQuit(c) {
		t.Fatal("a SIGTERM quit in the middle of the step; it waits for it")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("setup: the step had already finished")
	}

	_, quit := pump(t, m, cmd, s, func(_ MaintenanceModel, quit bool) bool { return quit })
	if !quit {
		t.Fatal("never quit after the step ended")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the launcher quit before the step finished: %v", err)
	}
}

func TestMaintenanceModel_WhileWorkingCtrlCAndEscNeverKillTheStep(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "finished")
	script := fmt.Sprintf("sleep 1; touch %q; %s", marker, endedLine)
	m, _, s := newMaint(t, script)
	m = toReopen(t, m)
	m, cmd := updateAs(t, m, key(tea.KeyEnter), ctrlC(), key(tea.KeyEsc), ctrlC())
	m, _ = pump(t, m, cmd, s, untilScreen("Orbit is open again"))
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the step was cut short: %v", err)
	}
	has(t, m, "Orbit is open again")
}

// ---- the app ----

func TestAppModel_MaintenanceRowOpensTheFlowAndEscComesBack(t *testing.T) {
	dir, _ := maintDeployment(t, true)
	m := NewAppModel().WithoutVolumeCheck()
	m.targetDir = dir
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 30))
	skipArrival(tm)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte("Maintenance")) },
		teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: '5', Text: "5"}) // the fifth row: Maintenance
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Close Orbit for maintenance"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte("O R B I T")) },
		teatest.WithDuration(5*time.Second))
	tm.Send(ctrlC())
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

// ---- screen grammar ----

// The design law: no key hints anywhere, whichever screen.
func TestMaintenanceModel_NoScreenCarriesKeyHints(t *testing.T) {
	m, _, s := newMaint(t, startedLine)
	var seen []string
	grab := func(m MaintenanceModel) { seen = append(seen, screen(m)) }
	grab(m)
	n := toNotice(t, m)
	grab(n)
	w := toWhen(t, m, "x")
	grab(w)
	grab(toConfirm(t, m, "x", "45m"))
	grab(toReopen(t, m))
	grab(runTo(t, toConfirm(t, m, "", ""), s, "Orbit is closed for maintenance"))
	for _, scr := range seen {
		for _, hint := range []string{"Esc", "esc ", "Enter", "enter ", "press ", "Ctrl", "↑", "↓", "q to"} {
			if strings.Contains(scr, hint) {
				t.Errorf("a key hint %q on:\n%s", hint, scr)
			}
		}
	}
}
