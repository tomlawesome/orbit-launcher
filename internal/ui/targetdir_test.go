package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// #205 (#199 WI-1: EN-1, UI-2, EN-12). AppModel resolves the target
// directory once and every flow uses what it was handed; a removal
// command only ever names a directory the launcher actually found; the
// box and the clipboard are built from the same words.

// removalLine is deploy.RemovalCommand for a directory the test's story
// says was found; an error there is a test failure.
func removalLine(t *testing.T, dir string) string {
	t.Helper()
	cmd, err := deploy.RemovalCommand(dir)
	if err != nil {
		t.Fatalf("RemovalCommand(%q): %v", dir, err)
	}
	return cmd
}

// removalWords is deploy.RemovalCommandWords, likewise.
func removalWords(t *testing.T, dir string) []string {
	t.Helper()
	words, err := deploy.RemovalCommandWords(dir)
	if err != nil {
		t.Fatalf("RemovalCommandWords(%q): %v", dir, err)
	}
	return words
}

// shortTempDir is a fresh directory whose path is short enough that the
// removal command's words fit the box whole, so a test reading the box
// back never trips over a word too wide for the screen.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "orb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// fakeDockerOnPath puts a docker that records every call and succeeds
// first on PATH, and returns the file the calls are recorded in. A
// stand-down is a docker call, so an empty record proves none started.
func fakeDockerOnPath(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(bin, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + record + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func dockerCalls(t *testing.T, record string) string {
	t.Helper()
	got, err := os.ReadFile(record)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// oneLine reads a screen as running text: any run of whitespace,
// including a wrap onto the next row, is one space.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// settle runs cmd and everything it leads to the way the program would,
// expanding batches and draining what an engine run pushes into the
// sink. The sky's ticks are dropped: they only move stars, and a frozen
// test has no use for an endless chain.
func settle(t *testing.T, m AppModel, cmd tea.Cmd, pushed *sink) AppModel {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; ; i++ {
		if i > 1000 {
			t.Fatal("command chain did not settle")
		}
		var msg tea.Msg
		switch {
		case len(queue) > 0:
			next := queue[0]
			queue = queue[1:]
			if next == nil {
				continue
			}
			msg = next()
		case pushed != nil:
			select {
			case msg = <-pushed.msgs:
			case <-time.After(2 * time.Second):
				return m
			}
		default:
			return m
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if _, ok := msg.(tickMsg); ok || msg == nil {
			continue
		}
		var more tea.Cmd
		m, more = appUpdate(t, m, msg)
		// Nothing follows the stream ending, so waiting for more would
		// only spend the timeout above.
		if _, ended := msg.(engineStreamEndedMsg); ended && more == nil && len(queue) == 0 {
			return m
		}
		queue = append(queue, more)
	}
}

func appScreen(m AppModel) string { return stripANSI(m.View().Content) }

// openRemove takes the menu to Remove and lets the flow start.
func openRemove(t *testing.T, m AppModel) AppModel {
	t.Helper()
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = appUpdate(t, m, key(tea.KeyUp), key(tea.KeyUp)) // Install wraps to Exit, then Remove
	m, cmd := appUpdate(t, m, key(tea.KeyEnter))
	return settle(t, m, cmd, nil)
}

// openUpdate takes the menu to Update and lets the flow start.
func openUpdate(t *testing.T, m AppModel, pushed *sink) AppModel {
	t.Helper()
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, _ = appUpdate(t, m, key(tea.KeyDown)) // Install, then Update
	m, cmd := appUpdate(t, m, key(tea.KeyEnter))
	return settle(t, m, cmd, pushed)
}

// boxRows returns the rows drawn inside the screen's box, border,
// padding and continuation backslash removed.
func boxRows(screen string) []string {
	var rows []string
	for _, row := range strings.Split(screen, "\n") {
		row = strings.TrimSpace(row)
		if !strings.HasPrefix(row, "│") {
			continue
		}
		inner := strings.TrimSpace(strings.Trim(row, "│"))
		inner = strings.TrimSpace(strings.TrimSuffix(inner, "\\"))
		if inner != "" {
			rows = append(rows, inner)
		}
	}
	return rows
}

// pathPattern finds an absolute path written in prose: a slash at the
// start of a word, up to the next space, quote or border.
var pathPattern = regexp.MustCompile(`(?:^|\s)(/[^\s'"│]*)`)

// prosePaths returns every absolute path the screen names outside its
// box, trailing punctuation dropped.
func prosePaths(screen string) []string {
	var paths []string
	for _, row := range strings.Split(screen, "\n") {
		if strings.HasPrefix(strings.TrimSpace(row), "│") {
			continue
		}
		for _, m := range pathPattern.FindAllStringSubmatch(row, -1) {
			if p := strings.TrimRight(m[1], ".,;:—)"); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// assertNamesOnly fails if the screen's prose names any directory other
// than dir (or a file inside it).
func assertNamesOnly(t *testing.T, what, screen, dir string) {
	t.Helper()
	for _, p := range prosePaths(screen) {
		if p != dir && !strings.HasPrefix(p, dir+"/") {
			t.Errorf("%s names %q, but the directory found is %q:\n%s", what, p, dir, screen)
		}
	}
	for _, placeholder := range []string{"/opt/orbit", "the deployment directory"} {
		if dir != "/opt/orbit" && strings.Contains(screen, placeholder) {
			t.Errorf("%s shows the placeholder %q instead of %q:\n%s", what, placeholder, dir, screen)
		}
	}
}

// assertNoRemovalCommand fails if the screen offers anything that removes
// or stands down a deployment.
func assertNoRemovalCommand(t *testing.T, what, screen string) {
	t.Helper()
	for _, banned := range []string{"rm -rf", "down -v", "--project-directory", "Copy command", "Stand down Orbit", "standing down", "stood down", "/opt/orbit", "the deployment directory"} {
		if strings.Contains(screen, banned) {
			t.Errorf("%s shows %q:\n%s", what, banned, screen)
		}
	}
}

// --- 1. No deployment: Remove stops at the start -------------------------

// Owner decision on #205 (answer 5a): with no deployment found, Remove
// stops at its first screen with "No Orbit deployment found in <dir>"
// and offers Back only. Nothing is stood down and no removal command is
// ever shown, because there is no directory the launcher found.
func TestAppModel_RemoveWithNoDeploymentStopsAtTheStart(t *testing.T) {
	setups := map[string]func(t *testing.T) (AppModel, string){
		"directory handed to the app": func(t *testing.T) (AppModel, string) {
			dir := shortTempDir(t)
			m := NewAppModelNoAnimation().WithoutVolumeCheck()
			m.targetDir = dir
			return m, dir
		},
		"working directory": func(t *testing.T) (AppModel, string) {
			t.Chdir(shortTempDir(t))
			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			return NewAppModelNoAnimation().WithoutVolumeCheck(), wd
		},
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			record := fakeDockerOnPath(t)
			m, dir := setup(t)
			m = openRemove(t, m)

			s := appScreen(m)
			if want := "No Orbit deployment found in " + dir; !strings.Contains(oneLine(s), want) {
				t.Fatalf("first Remove screen should say %q:\n%s", want, s)
			}
			assertNoRemovalCommand(t, "the no-deployment screen", s)
			if strings.Contains(s, "Cancel") {
				t.Errorf("the no-deployment screen offers Cancel; want Back only:\n%s", s)
			}
			if !strings.Contains(s, "▸ Back") || strings.Count(s, "▸") != 1 {
				t.Errorf("the no-deployment screen should offer Back and nothing else:\n%s", s)
			}

			// Whatever is pressed, nothing is stood down and no command
			// appears; choosing Back returns to the menu.
			for _, c := range []struct {
				keys []tea.Msg
				back bool // the keys choose Back
			}{
				{[]tea.Msg{key(tea.KeyEnter)}, true},
				{[]tea.Msg{key(tea.KeyDown), key(tea.KeyEnter)}, true},
				{[]tea.Msg{key(tea.KeyUp), key(tea.KeyEnter)}, true},
				{[]tea.Msg{key(tea.KeyEsc)}, false},
			} {
				after := m
				for _, k := range c.keys {
					var cmd tea.Cmd
					after, cmd = appUpdate(t, after, k)
					after = settle(t, after, cmd, nil)
					assertNoRemovalCommand(t, "after a key on the no-deployment screen", appScreen(after))
				}
				if c.back {
					if s := appScreen(after); !strings.Contains(s, "Install") || !strings.Contains(s, "Remove") {
						t.Errorf("keys %v choose Back, which should return to the menu:\n%s", c.keys, s)
					}
				}
			}
			if calls := dockerCalls(t, record); calls != "" {
				t.Errorf("Remove with no deployment called docker:\n%s", calls)
			}
		})
	}
}

// --- 2. A deployment: prose, box and stand-down name the directory found --

func TestAppModel_RemoveNamesTheDirectoryItFound(t *testing.T) {
	setups := map[string]func(t *testing.T, dir string) (AppModel, string){
		"directory handed to the app": func(t *testing.T, dir string) (AppModel, string) {
			t.Chdir(shortTempDir(t)) // the working directory holds nothing
			m := NewAppModelNoAnimation().WithoutVolumeCheck()
			m.targetDir = dir
			return m, dir
		},
		"working directory": func(t *testing.T, dir string) (AppModel, string) {
			t.Chdir(dir)
			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			return NewAppModelNoAnimation().WithoutVolumeCheck(), wd
		},
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			record := fakeDockerOnPath(t)
			dir := shortTempDir(t)
			writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
			m, dir := setup(t, dir)
			m = openRemove(t, m)

			s := appScreen(m)
			if !strings.Contains(s, "Stand down Orbit") {
				t.Fatalf("Remove with a deployment should open on its confirm screen:\n%s", s)
			}
			assertNamesOnly(t, "the confirm screen", s, dir)

			m, cmd := appUpdate(t, m, key(tea.KeyEnter)) // Stand down Orbit
			m = settle(t, m, cmd, nil)
			s = appScreen(m)
			if !strings.Contains(s, "stood down") {
				t.Fatalf("stand-down did not finish:\n%s\ndocker calls:\n%s", s, dockerCalls(t, record))
			}
			if calls := dockerCalls(t, record); !strings.Contains(calls, "--project-directory "+dir+" ") {
				t.Errorf("stand-down was not run against %q; docker calls:\n%s", dir, calls)
			}
			if got, want := joinedCommand(s), removalLine(t, dir); got != want {
				t.Errorf("box reads\n  %q\nwant the command for the directory found\n  %q\nscreen:\n%s", got, want, s)
			}
			assertNamesOnly(t, "the stood-down screen", s, dir)
		})
	}
}

// --- 3. Box and clipboard are the same words -------------------------------

// removeDone drives a RemoveModel for a deployment in dir through a
// successful stand-down at the given width, with the clipboard captured.
func removeDone(t *testing.T, dir string, width int) (RemoveModel, *bytes.Buffer, *string) {
	t.Helper()
	d := &deploy.Deployment{TargetDir: dir, AppURL: "https://mail.example.com"}
	var stoodDown string
	var clip bytes.Buffer
	m := NewRemoveModel(d)
	m.standDown = func(_ context.Context, got string) error { stoodDown = got; return nil }
	m.lookupInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }
	m.clipboard = &clip
	m, _ = removeUpdate(t, m, tea.WindowSizeMsg{Width: width, Height: 30})
	if s := stripANSI(m.View().Content); !strings.Contains(s, "Stand down Orbit") {
		t.Fatalf("confirm screen:\n%s", s)
	}
	assertNamesOnly(t, "the confirm screen", stripANSI(m.View().Content), dir)
	m, cmd := removeUpdate(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("confirm did not start the stand-down")
	}
	m, _ = removeUpdate(t, m, cmd())
	if s := stripANSI(m.View().Content); !strings.Contains(s, "stood down") {
		t.Fatalf("stood-down screen:\n%s", s)
	}
	return m, &clip, &stoodDown
}

// The removal box is the command's words laid out on rows, each row a
// run of whole words (an option may share a row with its value), and the
// clipboard holds the same words joined. Neither can name a different
// directory from the other, or from the deployment that was stood down.
func TestRemoveModel_BoxAndClipboardAreTheSameWords(t *testing.T) {
	for _, dir := range []string{
		"/srv/containers/mail-orbit",
		"/srv/My Drive/containers/mail orbit production",
	} {
		for _, width := range []int{80, 60} {
			m, clip, stoodDown := removeDone(t, dir, width)
			s := stripANSI(m.View().Content)
			words := removalWords(t, dir)
			want := strings.Join(words, " ")
			if want != removalLine(t, dir) {
				t.Fatalf("RemovalCommand(%q) is not its words joined", dir)
			}

			i := 0
			for _, row := range boxRows(s) {
				matched := false
				for j := i + 1; j <= len(words); j++ {
					if strings.Join(words[i:j], " ") == row {
						i, matched = j, true
						break
					}
				}
				if !matched {
					t.Errorf("dir %q at %d columns: box row %q is not the next whole words of %q\n%s", dir, width, row, words, s)
					break
				}
			}
			if i != len(words) {
				t.Errorf("dir %q at %d columns: box holds %d of %d words\n%s", dir, width, i, len(words), s)
			}

			_, copyCmd := removeUpdate(t, m, key(tea.KeyEnter)) // Copy command is preselected
			if copyCmd == nil {
				t.Fatal("Copy command wrote nothing")
			}
			copyCmd()
			const prefix, suffix = "\x1b]52;c;", "\x07"
			payload := strings.TrimSuffix(strings.TrimPrefix(clip.String(), prefix), suffix)
			copied, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				t.Fatalf("clipboard payload %q: %v", clip.String(), err)
			}
			if string(copied) != want {
				t.Errorf("dir %q: copied\n  %q\nbox words\n  %q", dir, copied, want)
			}
			if *stoodDown != dir {
				t.Errorf("stood down %q, but the command names %q", *stoodDown, dir)
			}
			if !strings.Contains(dir, " ") {
				assertNamesOnly(t, "the stood-down screen", s, dir)
			}
		}
	}
}

// --- 4. No placeholder directory in the UI ---------------------------------

// The UI never invents a directory: no "/opt/orbit" default and no "the
// deployment directory" stand-in anywhere in a string the UI's code
// holds. Every directory shown comes from AppModel's resolution.
func TestUISource_HoldsNoPlaceholderDirectory(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				value = lit.Value
			}
			if strings.Contains(value, "opt/orbit") || strings.Contains(strings.ToLower(value), "the deployment directory") {
				t.Errorf("%s: placeholder directory in %s", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// --- 5. Update uses the directory AppModel resolved -------------------------

// AppModel is the only resolver: Update acts on exactly the directory
// the app resolved, whether that came from the app's own target or the
// working directory, and its screens name no other.
func TestAppModel_UpdateActsOnTheDirectoryTheAppResolved(t *testing.T) {
	setups := map[string]func(t *testing.T, dir string) (AppModel, string){
		"directory handed to the app": func(t *testing.T, dir string) (AppModel, string) {
			t.Chdir(shortTempDir(t)) // the working directory holds nothing
			m := NewAppModelNoAnimation()
			m.targetDir = dir
			return m, dir
		},
		"working directory": func(t *testing.T, dir string) (AppModel, string) {
			t.Chdir(dir)
			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			return NewAppModelNoAnimation(), wd
		},
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			dir := shortTempDir(t)
			writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
			m, dir := setup(t, dir)

			var engineDir string
			s := newSink()
			m = m.WithSender(s.Send).WithoutVolumeCheck()
			m.flowSeams = engineRunSeams{
				prepareEngine: func(ctx context.Context, targetDir, action string) (*engine.Stream, func() error, error) {
					engineDir = targetDir
					return fakeEngine(nil, successStream()...)(ctx, targetDir, action)
				},
				detect: func(string) (*deploy.Deployment, error) {
					return &deploy.Deployment{TargetDir: dir, AppURL: "https://mail.example.test"}, nil
				},
			}
			m = openUpdate(t, m, nil)
			screen := appScreen(m)
			if !strings.Contains(screen, "Pull the latest Orbit and update this deployment") {
				t.Fatalf("Update should open on its confirm screen:\n%s", screen)
			}
			assertNamesOnly(t, "Update's confirm screen", screen, dir)

			m, cmd := appUpdate(t, m, key(tea.KeyEnter)) // Update Orbit
			m = settle(t, m, cmd, s)
			if engineDir != dir {
				t.Errorf("Update ran the engine in %q, want the directory the app resolved, %q", engineDir, dir)
			}
			assertNamesOnly(t, "Update's last screen", appScreen(m), dir)
		})
	}
}

// With nothing found, Update's screen names no directory but the one the
// app resolved.
func TestAppModel_UpdateWithNoDeploymentNamesOnlyTheResolvedDirectory(t *testing.T) {
	dir := shortTempDir(t)
	t.Chdir(dir)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	m := openUpdate(t, NewAppModelNoAnimation().WithoutVolumeCheck(), nil)
	screen := appScreen(m)
	if !strings.Contains(screen, "No Orbit deployment found") {
		t.Fatalf("Update with nothing installed should say so:\n%s", screen)
	}
	assertNamesOnly(t, "Update's not-found screen", screen, wd)
}
