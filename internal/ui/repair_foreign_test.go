package ui

// #265: Repair stops, with a screen of its own, when .env-orbit or a file
// under .orbit-secrets belongs to another account. The contract (issue
// #265, note 41313):
//
//   - RepairModel.foreignOwned func(dir string) (deploy.Ownership, error);
//     nil checks nothing (the c9 harness).
//   - Every run start (Init, and "diagnose again") calls it first, inside
//     the command that would start the engine. When Files is non-empty the
//     command returns repairForeignMsg{own deploy.Ownership} instead of
//     starting the engine, and the model shows the stop screen. A check
//     that errors does not stop Repair.
//   - Stop screen: "✗ Repair can't check these files"; one row per file,
//     "<path> — owned by <owner>"; two muted rows, "their contents can't
//     be checked and nothing can restore them" and "give them back to this
//     account, then diagnose again"; the command
//     "sudo chown -R <Account>: <dir>/.env-orbit <dir>/.orbit-secrets",
//     naming only the top-level items that hold a foreign file; menu
//     "Copy command", "Menu", "Exit". Copy command copies the command
//     (OSC 52, the clipboard io.Writer seam) and then reads "Copy command
//     · copied". Paths are escaped for the screen as elsewhere.
//   - The trailing colon in "<Account>:" and no group name: it sets the
//     account's own login group.
//   - The app sets foreignOwned = deploy.ForeignOwned where it opens Repair.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// ---- the start: when the seam is asked, and what it stops ----

// rfProbe stands in for both seams and records the order they were asked
// in; the engine start fails at once so nothing real is spawned.
type rfProbe struct {
	calls []string
	dirs  []string
	own   deploy.Ownership
	err   error
}

func (p *rfProbe) wire(m *RepairModel) {
	m.foreignOwned = func(dir string) (deploy.Ownership, error) {
		p.calls = append(p.calls, "check")
		p.dirs = append(p.dirs, dir)
		return p.own, p.err
	}
	m.prepareRepair = func(_ context.Context, _ string, _ deploy.RepairMode) (*engine.Stream, error) {
		p.calls = append(p.calls, "engine")
		return nil, errors.New("stop here")
	}
}

func (p *rfProbe) reset() { p.calls, p.dirs = nil, nil }

func rfCallsAre(t *testing.T, p *rfProbe, want ...string) {
	t.Helper()
	if strings.Join(p.calls, ",") != strings.Join(want, ",") {
		t.Errorf("seams were asked %v, want %v", p.calls, want)
	}
}

var (
	rfEnv     = deploy.ForeignFile{Path: ".env-orbit", Owner: "root"}
	rfSecretA = deploy.ForeignFile{Path: ".orbit-secrets/db_password", Owner: "www-data"}
	rfSecretB = deploy.ForeignFile{Path: ".orbit-secrets/sub/api_key", Owner: "root"}
)

func rfOwn(files ...deploy.ForeignFile) deploy.Ownership {
	return deploy.Ownership{Account: "alice", Files: files}
}

func TestRepairForeign_NewRepairModelChecksNothing(t *testing.T) {
	if NewRepairModel(t.TempDir(), "v0.6.0").foreignOwned != nil {
		t.Error("NewRepairModel set a foreignOwned seam; nil means check nothing, and the app wires the real one")
	}
}

// A nil seam is today's behaviour: Init starts the engine.
func TestRepairForeign_NilSeamStartsTheEngine(t *testing.T) {
	m := sizedRepair(t)
	p := &rfProbe{}
	p.wire(&m)
	m.foreignOwned = nil
	msg := m.Init()()
	if _, ok := msg.(repairReadyMsg); !ok {
		t.Fatalf("Init produced %#v, want a repairReadyMsg", msg)
	}
	rfCallsAre(t, p, "engine")
}

// The check is part of the command that starts the engine, not run while
// Init builds it, so the screen is not held up by it.
func TestRepairForeign_CheckRunsInsideTheStartCommand(t *testing.T) {
	m := sizedRepair(t)
	p := &rfProbe{}
	p.wire(&m)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command")
	}
	if len(p.calls) != 0 {
		t.Fatalf("Init asked %v before the command ran; the check belongs inside the command", p.calls)
	}
	cmd()
	rfCallsAre(t, p, "check", "engine")
}

func TestRepairForeign_ForeignFilesStopBeforeTheEngine(t *testing.T) {
	m := sizedRepair(t)
	p := &rfProbe{own: rfOwn(rfEnv, rfSecretA)}
	p.wire(&m)
	msg := m.Init()()
	got, ok := msg.(repairForeignMsg)
	if !ok {
		t.Fatalf("Init produced %#v, want a repairForeignMsg", msg)
	}
	if !reflect.DeepEqual(got.own, p.own) {
		t.Errorf("repairForeignMsg carries %+v, want the seam's answer %+v", got.own, p.own)
	}
	rfCallsAre(t, p, "check")
	if len(p.dirs) != 1 || p.dirs[0] != m.targetDir {
		t.Errorf("seam asked about %v, want the deployment folder %q", p.dirs, m.targetDir)
	}

	m, _ = updateAs(t, m, msg)
	if s := plain(m); !strings.Contains(s, "✗ Repair can't check these files") {
		t.Errorf("no stop screen after the message:\n%s", screen(m))
	}
	rfCallsAre(t, p, "check") // still no engine
}

func TestRepairForeign_NoFilesStartsTheEngine(t *testing.T) {
	for name, own := range map[string]deploy.Ownership{
		"nil files":   {Account: "alice"},
		"empty files": {Account: "alice", Files: []deploy.ForeignFile{}},
	} {
		t.Run(name, func(t *testing.T) {
			m := sizedRepair(t)
			p := &rfProbe{own: own}
			p.wire(&m)
			msg := m.Init()()
			if _, ok := msg.(repairReadyMsg); !ok {
				t.Fatalf("Init produced %#v, want a repairReadyMsg", msg)
			}
			rfCallsAre(t, p, "check", "engine")
		})
	}
}

func TestRepairForeign_ACheckThatErrorsDoesNotStopRepair(t *testing.T) {
	cases := map[string]deploy.Ownership{
		"no answer":      {},
		"a partial list": rfOwn(rfEnv),
	}
	for name, own := range cases {
		t.Run(name, func(t *testing.T) {
			m := sizedRepair(t)
			p := &rfProbe{own: own, err: errors.New("lstat: permission denied")}
			p.wire(&m)
			msg := m.Init()()
			if _, ok := msg.(repairReadyMsg); !ok {
				t.Fatalf("Init produced %#v, want a repairReadyMsg: a failed check must not stop Repair", msg)
			}
			rfCallsAre(t, p, "check", "engine")
			m, _ = updateAs(t, m, msg)
			if strings.Contains(plain(m), "can't check these files") {
				t.Errorf("the stop screen shows after a failed check:\n%s", screen(m))
			}
		})
	}
}

// rfAfterPicture is a model on the after-picture, where "Diagnose again"
// is the first row, as in TestRepairModel_DiagnoseAgainStartsAFreshPlanRun.
func rfAfterPicture(t *testing.T, p *rfProbe) RepairModel {
	t.Helper()
	m := sizedRepair(t)
	p.wire(&m)
	m.mode = deploy.RepairCheck
	m.state = repairExecuting
	m = repairLines(t, m, "execution result=complete done=1 failed=0", "diagnosis result=healthy checked=15 skipped=0")
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 0})
	return m
}

func TestRepairForeign_DiagnoseAgainChecksAgain(t *testing.T) {
	p := &rfProbe{}
	m := rfAfterPicture(t, p)
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // "Diagnose again" is first
	if cmd == nil {
		t.Fatal("Diagnose again started nothing")
	}
	if len(p.calls) != 0 {
		t.Fatalf("the check ran before the command did: %v", p.calls)
	}
	cmd()
	rfCallsAre(t, p, "check", "engine")
	if len(p.dirs) != 1 || p.dirs[0] != m.targetDir {
		t.Errorf("seam asked about %v, want %q", p.dirs, m.targetDir)
	}
}

// A file handed to another account since the first run stops the next.
func TestRepairForeign_DiagnoseAgainStopsOnNewlyForeignFiles(t *testing.T) {
	p := &rfProbe{own: rfOwn(rfSecretA)}
	m := rfAfterPicture(t, p)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Diagnose again started nothing")
	}
	msg := cmd()
	if _, ok := msg.(repairForeignMsg); !ok {
		t.Fatalf("Diagnose again produced %#v, want a repairForeignMsg", msg)
	}
	rfCallsAre(t, p, "check")
	m, _ = updateAs(t, m, msg)
	for _, want := range []string{"✗ Repair can't check these files", ".orbit-secrets/db_password — owned by www-data"} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("screen lacks %q:\n%s", want, screen(m))
		}
	}
}

// The user fixes the owner and diagnoses again from the after-picture:
// the answer is asked afresh every time, never remembered.
func TestRepairForeign_EachRunStartAsksAgain(t *testing.T) {
	p := &rfProbe{own: rfOwn(rfEnv)}
	m := rfAfterPicture(t, p)
	_, cmd := updateAs(t, m, key(tea.KeyEnter))
	if _, ok := cmd().(repairForeignMsg); !ok {
		t.Fatal("first run start did not stop")
	}
	p.reset()
	p.own = deploy.Ownership{Account: "alice"} // handed back
	m2 := rfAfterPicture(t, p)
	_, cmd = updateAs(t, m2, key(tea.KeyEnter))
	if _, ok := cmd().(repairReadyMsg); !ok {
		t.Fatal("second run start did not go ahead after the files were handed back")
	}
	rfCallsAre(t, p, "check", "engine")
}

// ---- the stop screen ----

const rfDir = "/srv/orbit"

// rfStop is the stop screen for own, for a folder, 200 columns wide so
// nothing under test wraps, with the clipboard going to the buffer.
func rfStop(t *testing.T, dir string, own deploy.Ownership) (RepairModel, *bytes.Buffer) {
	t.Helper()
	m := NewRepairModel(dir, "v0.6.0")
	buf := &bytes.Buffer{}
	m.clipboard = buf
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 200, Height: 40}, repairForeignMsg{own: own})
	return m, buf
}

// rfLineIndex is the index of the first screen line containing text, or
// -1. Rows are found by Contains, not screenRow: the starfield glyph "."
// would be trimmed off the front of ".env-orbit".
func rfLineIndex(s, text string) int {
	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(line, text) {
			return i
		}
	}
	return -1
}

// rfCommandOn is the chown command as the screen shows it: from "sudo"
// to the end of the sentence, the sky and any box side trimmed off.
func rfCommandOn(t *testing.T, s string) string {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "sudo chown"); i >= 0 {
			return strings.TrimRight(line[i:], " │.·✦●⟡")
		}
	}
	t.Fatalf("no `sudo chown` command on the screen:\n%s", s)
	return ""
}

func rfWantCommand(account, dir string, env, secrets bool) string {
	cmd := "sudo chown -R " + rfQuote(account+":")
	if env {
		cmd += " " + rfQuote(dir+"/.env-orbit")
	}
	if secrets {
		cmd += " " + rfQuote(dir+"/.orbit-secrets")
	}
	return cmd
}

// rfQuote is POSIX single-quote escaping as Python's shlex.quote does it,
// bare when every character is shell-safe: the command is pasted into a
// shell, so a folder with a space or quote must arrive as one argument,
// the same as Remove's copied command.
func rfQuote(s string) string {
	if s == "" {
		return "''"
	}
	if regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestRepairForeign_HeadingRowsAndMutedRows(t *testing.T) {
	own := rfOwn(rfEnv, rfSecretA, rfSecretB)
	m, _ := rfStop(t, rfDir, own)
	s := screen(m)

	title := rfLineIndex(s, "✗ Repair can't check these files")
	if title < 0 {
		t.Fatalf("no heading `✗ Repair can't check these files`:\n%s", s)
	}
	last := -1
	for _, f := range own.Files {
		row := f.Path + " — owned by " + f.Owner
		i := rfLineIndex(s, row)
		if i < 0 {
			t.Errorf("no row %q:\n%s", row, s)
			continue
		}
		if i <= title || i <= last {
			t.Errorf("row %q (line %d) is out of order: heading at %d, previous file at %d", row, i, title, last)
		}
		last = i
	}
	if n := strings.Count(s, " — owned by "); n != len(own.Files) {
		t.Errorf("%d `owned by` rows, want one per file (%d):\n%s", n, len(own.Files), s)
	}

	// The muted rows may wrap, so they are read with the wrapping undone.
	p := plain(m)
	muted1 := "their contents can't be checked and nothing can restore them"
	muted2 := "give them back to this account, then diagnose again"
	i1, i2 := strings.Index(p, muted1), strings.Index(p, muted2)
	if i1 < 0 || i2 < 0 {
		t.Fatalf("muted rows missing (first at %d, second at %d):\n%s", i1, i2, s)
	}
	lastFile := strings.Index(p, rfSecretB.Path+" — owned by "+rfSecretB.Owner)
	if i1 < lastFile || i2 < i1 {
		t.Errorf("order is not file rows, then the first muted row, then the second:\n%s", s)
	}
	cmd := strings.Index(p, "sudo chown")
	if cmd < i2 {
		t.Errorf("the command is not after the muted rows:\n%s", s)
	}
	if menu := strings.Index(p, "Copy command"); menu < cmd {
		t.Errorf("the menu is not after the command:\n%s", s)
	}
}

func TestRepairForeign_CommandNamesOnlyTheItemsHoldingForeignFiles(t *testing.T) {
	cases := []struct {
		name           string
		files          []deploy.ForeignFile
		env, secretsIn bool
	}{
		{"only .env-orbit", []deploy.ForeignFile{rfEnv}, true, false},
		{"only one secret", []deploy.ForeignFile{rfSecretA}, false, true},
		{"several secrets", []deploy.ForeignFile{rfSecretA, rfSecretB}, false, true},
		{"both", []deploy.ForeignFile{rfEnv, rfSecretA}, true, true},
		{"both, several secrets", []deploy.ForeignFile{rfEnv, rfSecretA, rfSecretB}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := rfStop(t, rfDir, rfOwn(tc.files...))
			want := rfWantCommand("alice", rfDir, tc.env, tc.secretsIn)
			if got := rfCommandOn(t, screen(m)); got != want {
				t.Errorf("command = %q, want %q", got, want)
			}
			p := plain(m)
			if !tc.env && strings.Contains(p, rfDir+"/.env-orbit") {
				t.Errorf("the command names .env-orbit though it is not foreign:\n%s", screen(m))
			}
			if !tc.secretsIn && strings.Contains(p, rfDir+"/.orbit-secrets") {
				t.Errorf("the command names .orbit-secrets though nothing under it is foreign:\n%s", screen(m))
			}
			if n := strings.Count(p, rfDir+"/.orbit-secrets"); tc.secretsIn && n != 1 {
				t.Errorf("the command names .orbit-secrets %d times, want once", n)
			}
		})
	}
}

// "<Account>:" with the colon and no group name sets the account's own
// login group.
func TestRepairForeign_CommandUsesTheAccountColonForm(t *testing.T) {
	for _, account := range []string{"alice", "mail-admin"} {
		t.Run(account, func(t *testing.T) {
			own := rfOwn(rfEnv, rfSecretA)
			own.Account = account
			m, _ := rfStop(t, rfDir, own)
			got := rfCommandOn(t, screen(m))
			if want := rfWantCommand(account, rfDir, true, true); got != want {
				t.Errorf("command = %q, want %q", got, want)
			}
			for _, bad := range []string{" " + account + " ", account + ":" + account, ":" + account, account + "/"} {
				if strings.Contains(got, bad) {
					t.Errorf("command %q contains %q: want `<Account>:` followed by a space, no group", got, bad)
				}
			}
			if !strings.Contains(got, "-R "+account+": "+rfDir+"/") {
				t.Errorf("command %q lacks `-R %s: <dir>/`", got, account)
			}
		})
	}
}

func TestRepairForeign_MenuRowsAndOrder(t *testing.T) {
	m, _ := rfStop(t, rfDir, rfOwn(rfEnv))
	s := screen(m)
	if got := strings.Join(screenLabels(s), "|"); got != "Copy command|Menu|Exit" {
		t.Errorf("menu rows = %q, want Copy command, Menu, Exit:\n%s", got, s)
	}
	if got := caretLabel(s); got != "Copy command" {
		t.Errorf("caret on %q, want Copy command:\n%s", got, s)
	}
	if strings.Contains(s, "Diagnose again") {
		t.Errorf("the stop screen offers Diagnose again; the way back is Menu:\n%s", s)
	}
}

func TestRepairForeign_MenuAndExitRows(t *testing.T) {
	m, _ := rfStop(t, rfDir, rfOwn(rfEnv))

	toMenu, _ := updateAs(t, m, key(tea.KeyDown))
	toMenu, _ = updateAs(t, toMenu, key(tea.KeyEnter))
	if !toMenu.Done || !toMenu.WantsMenu {
		t.Errorf("Menu row: Done=%v WantsMenu=%v, want both", toMenu.Done, toMenu.WantsMenu)
	}

	exit, _ := updateAs(t, m, key(tea.KeyDown), key(tea.KeyDown))
	if got := caretLabel(screen(exit)); got != "Exit" {
		t.Fatalf("two Downs left the caret on %q, want Exit", got)
	}
	if _, cmd := updateAs(t, exit, key(tea.KeyEnter)); !isQuit(cmd) {
		t.Error("the Exit row did not quit")
	}
}

func rfDecodeClip(t *testing.T, got string) string {
	t.Helper()
	const prefix, suffix = "\x1b]52;c;", "\x07"
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("clipboard write = %q, want an OSC 52 sequence", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
	if err != nil {
		t.Fatalf("payload is not standard base64: %v", err)
	}
	return string(decoded)
}

func TestRepairForeign_CopyWritesTheCommandAsOSC52(t *testing.T) {
	cases := []struct {
		name         string
		files        []deploy.ForeignFile
		env, secrets bool
	}{
		{"env only", []deploy.ForeignFile{rfEnv}, true, false},
		{"secrets only", []deploy.ForeignFile{rfSecretA}, false, true},
		{"both", []deploy.ForeignFile{rfEnv, rfSecretA, rfSecretB}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, buf := rfStop(t, rfDir, rfOwn(tc.files...))
			if strings.Contains(screen(m), "· copied") {
				t.Fatalf("the screen says copied before anything was copied:\n%s", screen(m))
			}
			m, copyCmd := updateAs(t, m, key(tea.KeyEnter)) // Copy command is the default row
			if copyCmd == nil {
				t.Fatal("Copy command wrote nothing: expected a command that writes OSC 52")
			}
			if msg := copyCmd(); msg != nil {
				t.Errorf("the copy command returned %#v, want a nil message", msg)
			}
			want := rfWantCommand("alice", rfDir, tc.env, tc.secrets)
			if got := rfDecodeClip(t, buf.String()); got != want {
				t.Errorf("copied %q, want the command %q", got, want)
			}
			// What was copied is what the screen shows.
			if shown := rfCommandOn(t, screen(m)); shown != want {
				t.Errorf("screen shows %q, clipboard has %q", shown, want)
			}
		})
	}
}

func TestRepairForeign_CopyThenReadsCopiedAndStaysOnScreen(t *testing.T) {
	m, buf := rfStop(t, rfDir, rfOwn(rfEnv, rfSecretA))
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	s := screen(m)
	if !strings.Contains(s, "▸ Copy command · copied") {
		t.Errorf("the copy row should say it has copied, still selected:\n%s", s)
	}
	if !strings.Contains(plain(m), "✗ Repair can't check these files") || m.Done {
		t.Errorf("copying left the stop screen (Done=%v):\n%s", m.Done, s)
	}
	if got := strings.Join(screenLabels(s), "|"); got != "Copy command · copied|Menu|Exit" {
		t.Errorf("rows after copying = %q", got)
	}
	// Enter on the copied row copies again rather than doing nothing.
	buf.Reset()
	_, again := updateAs(t, m, key(tea.KeyEnter))
	if again == nil {
		t.Fatal("Enter on the copied row did nothing")
	}
	again()
	if buf.Len() == 0 {
		t.Error("Enter on the copied row wrote nothing the second time")
	}
}

// ---- text the launcher did not write reaches the screen escaped ----

func TestRepairForeign_PathsAndOwnersAreEscapedOnScreen(t *testing.T) {
	evilPath := ".orbit-secrets/evil\x1b[2J\rEVIL"
	evilOwner := "ow\x1b[31mner"
	own := rfOwn(deploy.ForeignFile{Path: evilPath, Owner: evilOwner})
	m, _ := rfStop(t, rfDir, own)

	raw := m.View().Content
	for _, bad := range []string{"\x1b[2J", "\r", "ow\x1b[31mner"} {
		if strings.Contains(raw, bad) {
			t.Errorf("raw screen holds %q: an unescaped control reached the terminal", bad)
		}
	}
	want := `.orbit-secrets/evil\x1b[2J\rEVIL — owned by ow\x1b[31mner`
	if rfLineIndex(screen(m), want) < 0 {
		t.Errorf("no escaped row %q:\n%s", want, screen(m))
	}
	// One file, one row: an LF or CR in the name must not add rows.
	if n := strings.Count(screen(m), " — owned by "); n != 1 {
		t.Errorf("%d `owned by` rows, want 1:\n%s", n, screen(m))
	}
}

func TestRepairForeign_FolderIsEscapedInTheCommandOnScreenButCopiedQuoted(t *testing.T) {
	dir := "/srv/\x1b[2Jorbit"
	m, buf := rfStop(t, dir, rfOwn(rfEnv, rfSecretA))

	raw := m.View().Content
	for _, bad := range []string{"\x1b[2J", "\r"} {
		if strings.Contains(raw, bad) {
			t.Errorf("raw screen holds %q: the folder reached the terminal unescaped", bad)
		}
	}
	shownDir := `/srv/\x1b[2Jorbit`
	if got, want := rfCommandOn(t, screen(m)), rfWantCommand("alice", shownDir, true, true); got != want {
		t.Errorf("command on screen = %q, want %q", got, want)
	}

	_, copyCmd := updateAs(t, m, key(tea.KeyEnter))
	if copyCmd == nil {
		t.Fatal("Copy command wrote nothing")
	}
	copyCmd()
	if got, want := rfDecodeClip(t, buf.String()), rfWantCommand("alice", dir, true, true); got != want {
		t.Errorf("copied %q, want the quoted command %q", got, want)
	}
}

// A folder with a space or a quote is quoted in the command, so the pasted
// line still names each item as one argument.
func TestRepairForeign_CommandQuotesFoldersAShellWouldSplit(t *testing.T) {
	for _, dir := range []string{"/srv/my orbit", "/srv/tom's orbit"} {
		m, buf := rfStop(t, dir, rfOwn(rfEnv, rfSecretA))
		want := rfWantCommand("alice", dir, true, true)
		if !strings.Contains(want, "'") {
			t.Fatalf("test helper did not quote %q", dir)
		}
		_, copyCmd := updateAs(t, m, key(tea.KeyEnter))
		if copyCmd == nil {
			t.Fatal("Copy command wrote nothing")
		}
		copyCmd()
		if got := rfDecodeClip(t, buf.String()); got != want {
			t.Errorf("dir %q: copied %q, want %q", dir, got, want)
		}
	}
}

// ---- the wiring ----

// The app hands Repair the real check where it opens Repair: a scan of
// app.go, because a foreign owner can't be made in an unprivileged test.
func TestRepairForeign_AppWiresDeployForeignOwned(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "app.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	isReal := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ForeignOwned" {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "deploy"
	}
	wired := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "foreignOwned" && i < len(v.Rhs) && isReal(v.Rhs[i]) {
					wired = true
				}
			}
		case *ast.KeyValueExpr:
			if id, ok := v.Key.(*ast.Ident); ok && id.Name == "foreignOwned" && isReal(v.Value) {
				wired = true
			}
		}
		return true
	})
	if !wired {
		t.Error("app.go never sets foreignOwned to deploy.ForeignOwned")
	}
}
