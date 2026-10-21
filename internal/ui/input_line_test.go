package ui

// Pins #211 (typing surface, #199 WI-7; UI-7). The development notice's
// phrase box, the configuration prompt and the rotation prompt all edit
// one line of text with the same keys: append, space, backspace, and
// Enter to submit. The decisions give those shared keys one home, a
// small `inputLine` value, while Esc stays with each caller. The Ctrl
// guard in the notice goes: bubbletea leaves Key.Text empty under
// modifier keys, so a modifier key already types nothing.
//
// Two kinds of test live here. Behaviour tests drive all three surfaces
// through one table, so they hold before and after the merge. Source
// scans (go/parser, no new names referenced at compile time) pin the
// shape: one type, one backspace handler, no Ctrl guard.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// ---------------------------------------------------------------------------
// The three typing surfaces, behind one rig.
// ---------------------------------------------------------------------------

// typingRig is one typing surface seen from outside: keys go in, the
// text on the screen's input row and what reached the engine come out.
type typingRig struct {
	press func(tea.KeyPressMsg)
	// typed is the answer as the screen shows it, caret excluded.
	typed func() string
	// sent is what the engine's stdin received (always "" for the
	// notice, which has no engine behind it).
	sent func() string
}

// inputRowText reads the answer off a rendered screen: the text between
// the row's marker (the box border or the "▸ " caret) and the "▏"
// cursor. A screen with no cursor row has nothing typed.
func inputRowText(screen string) string {
	for _, line := range strings.Split(screen, "\n") {
		end := strings.Index(line, "▏")
		if end < 0 {
			continue
		}
		before := line[:end]
		if i := strings.Index(before, "│ "); i >= 0 {
			return before[i+len("│ "):]
		}
		if i := strings.LastIndex(before, "▸ "); i >= 0 {
			return before[i+len("▸ "):]
		}
		return before
	}
	return ""
}

// noticeRig is the phrase box of the development notice, awake (both
// gates passed) so letters are accepted.
func noticeRig(t *testing.T) typingRig {
	m := awakeNotice(t, engineRunSeams{})
	return typingRig{
		press: func(k tea.KeyPressMsg) { m, _ = updateAs(t, m, k) },
		typed: func() string { return inputRowText(screen(m)) },
		sent:  func() string { return "" },
	}
}

// rotationRig is Repair's rotation prompt, waiting for a plain-text
// answer.
func rotationRig(t *testing.T) (typingRig, *recordingStdin) {
	m := sizedRepair(t)
	m.state = repairRotating
	stdin := &recordingStdin{}
	m, _ = updateAs(t, m, repairRotateReadyMsg{stream: &engine.Stream{C: make(chan any)}, stdin: stdin})
	m = repairLines(t, m, "prompt field=action-word kind=typed-word required=true attempt=1")
	return typingRig{
		press: func(k tea.KeyPressMsg) { m, _ = updateAs(t, m, k) },
		typed: func() string { return inputRowText(screen(m)) },
		sent:  stdin.String,
	}, stdin
}

// configRig is the in-console configuration prompt, waiting for a
// plain-text answer.
func configRig(t *testing.T) (rig typingRig, stdin *recordingStdin, stream *engine.Stream) {
	r := collectingRun(t, engineRunSeams{})
	stdin = &recordingStdin{}
	stream = sleepingStream(t)
	r.cfg.stream = stream
	r.cfg.stdin = stdin
	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt field=OIDC_CLIENT_ID kind=text required=true attempt=1"}})
	return typingRig{
		press: func(k tea.KeyPressMsg) { r, _ = r.update(k) },
		typed: func() string { return inputRowText(runScreen(r)) },
		sent:  stdin.String,
	}, stdin, stream
}

// surfaces runs body once per typing surface.
func surfaces(t *testing.T, body func(t *testing.T, rig typingRig)) {
	t.Helper()
	t.Run("notice", func(t *testing.T) { body(t, noticeRig(t)) })
	t.Run("rotation", func(t *testing.T) {
		rig, _ := rotationRig(t)
		body(t, rig)
	})
	t.Run("configuration", func(t *testing.T) {
		rig, _, _ := configRig(t)
		body(t, rig)
	})
}

// ---------------------------------------------------------------------------
// Behaviour: one set of keys, one result, on every surface.
// ---------------------------------------------------------------------------

// The shared keys must edit the line identically wherever it is typed.
// Written so that it holds before and after the merge.
func TestInputLine_SharedKeysEditTheSameOnEverySurface(t *testing.T) {
	surfaces(t, func(t *testing.T, rig typingRig) {
		for _, k := range []tea.KeyPressMsg{runeKey('a'), runeKey('b'), key(tea.KeySpace), runeKey('c')} {
			rig.press(k)
		}
		if got := rig.typed(); got != "ab c" {
			t.Fatalf("after a, b, space, c the line reads %q, want %q", got, "ab c")
		}

		rig.press(key(tea.KeyBackspace))
		rig.press(key(tea.KeyBackspace))
		if got := rig.typed(); got != "ab" {
			t.Fatalf("two backspaces left %q, want %q", got, "ab")
		}

		// Pasted or composed text arrives as one Text of several runes.
		rig.press(tea.KeyPressMsg{Text: "xyz"})
		if got := rig.typed(); got != "abxyz" {
			t.Fatalf("a multi-rune Text left %q, want %q", got, "abxyz")
		}

		// Backspace removes one character, not one byte.
		rig.press(tea.KeyPressMsg{Code: 'é', Text: "é"})
		if got := rig.typed(); got != "abxyzé" {
			t.Fatalf("a non-ASCII rune left %q, want %q", got, "abxyzé")
		}
		rig.press(key(tea.KeyBackspace))
		if got := rig.typed(); got != "abxyz" {
			t.Fatalf("backspace over a non-ASCII rune left %q, want %q", got, "abxyz")
		}
	})
}

// Backspace on an empty line is harmless on every surface. The notice's
// box is empty only before the first letter, so the line is first
// filled and then emptied there.
func TestInputLine_BackspaceOnAnEmptyLineIsHarmless(t *testing.T) {
	surfaces(t, func(t *testing.T, rig typingRig) {
		rig.press(runeKey('a'))
		rig.press(key(tea.KeyBackspace))
		rig.press(key(tea.KeyBackspace))
		rig.press(key(tea.KeyBackspace))
		if got := rig.typed(); got != "" {
			t.Fatalf("backspace past the start left %q", got)
		}
		rig.press(runeKey('z'))
		if got := rig.typed(); got != "z" {
			t.Fatalf("typing after over-deleting left %q, want %q", got, "z")
		}
	})
}

// A modifier key types nothing. bubbletea leaves Key.Text empty for
// Ctrl and Alt combinations (its own documentation says so), so the
// notice's `!Mod.Contains(ModCtrl)` guard is redundant and goes; the
// other two surfaces never had one. Each case first proves a plain
// letter types, so a surface that ignores everything cannot pass.
func TestInputLine_ModifierKeysTypeNothing(t *testing.T) {
	surfaces(t, func(t *testing.T, rig typingRig) {
		rig.press(runeKey('a'))
		if got := rig.typed(); got != "a" {
			t.Fatalf("a plain letter left %q, want %q", got, "a")
		}
		for _, mk := range []struct {
			name string
			k    tea.KeyPressMsg
		}{
			{"ctrl+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}},
			{"alt+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt}},
			{"ctrl+alt+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModAlt}},
			// Ctrl+A now jumps to the start of the line (#258), and the
			// rig reads only the text before the cursor; Ctrl+Q means
			// nothing in a field.
			{"ctrl+q", tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}},
		} {
			rig.press(mk.k)
			if got := rig.typed(); got != "a" {
				t.Errorf("%s changed the line to %q, want it untouched (%q)", mk.name, got, "a")
			}
		}
	})
}

// Ctrl+Space is a modifier key too. A surface that types a space for
// the bare Space code regardless of modifiers would put a space in the
// answer here.
func TestInputLine_CtrlSpaceTypesNothing(t *testing.T) {
	surfaces(t, func(t *testing.T, rig typingRig) {
		rig.press(runeKey('a'))
		rig.press(key(tea.KeySpace))
		if got := rig.typed(); got != "a " {
			t.Fatalf("plain Space left %q, want %q", got, "a ")
		}
		rig.press(tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl})
		if got := rig.typed(); got != "a " {
			t.Errorf("ctrl+space changed the line to %q, want it untouched (%q)", got, "a ")
		}
	})
}

// The guard against modifier keys must not become a guard against
// capitals: Shift+letter carries its text and must type.
func TestInputLine_ShiftedLettersStillType(t *testing.T) {
	surfaces(t, func(t *testing.T, rig typingRig) {
		rig.press(runeKey('a'))
		rig.press(tea.KeyPressMsg{Code: 'b', Text: "B", Mod: tea.ModShift})
		if got := rig.typed(); got != "aB" {
			t.Fatalf("shift+b left %q, want %q", got, "aB")
		}
	})
}

// Enter submits the line to the engine, with its newline, on the two
// machine-prompt surfaces. (The notice checks its phrase instead; its
// Enter is pinned in notice_test.go.)
func TestInputLine_EnterSendsTheLineToTheEngine(t *testing.T) {
	t.Run("rotation", func(t *testing.T) {
		rig, _ := rotationRig(t)
		for _, k := range []tea.KeyPressMsg{runeKey('o'), runeKey('k'), key(tea.KeySpace), runeKey('1')} {
			rig.press(k)
		}
		rig.press(key(tea.KeyEnter))
		if got := rig.sent(); got != "ok 1\n" {
			t.Fatalf("engine received %q, want %q", got, "ok 1\n")
		}
	})
	t.Run("configuration", func(t *testing.T) {
		rig, _, _ := configRig(t)
		for _, k := range []tea.KeyPressMsg{runeKey('o'), runeKey('k'), key(tea.KeySpace), runeKey('1')} {
			rig.press(k)
		}
		rig.press(key(tea.KeyEnter))
		if got := rig.sent(); got != "ok 1\n" {
			t.Fatalf("engine received %q, want %q", got, "ok 1\n")
		}
	})
}

// A modifier key must not submit or send anything either: Ctrl+Enter
// is not Enter.
func TestInputLine_NothingReachesTheEngineUntilEnter(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) typingRig{
		"rotation":      func(t *testing.T) typingRig { r, _ := rotationRig(t); return r },
		"configuration": func(t *testing.T) typingRig { r, _, _ := configRig(t); return r },
	} {
		t.Run(name, func(t *testing.T) {
			rig := mk(t)
			rig.press(runeKey('a'))
			rig.press(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
			if got := rig.sent(); got != "" {
				t.Fatalf("the engine received %q before Enter", got)
			}
			rig.press(key(tea.KeyEnter))
			if got := rig.sent(); got != "a\n" {
				t.Fatalf("engine received %q, want %q (the Ctrl key must not be part of it)", got, "a\n")
			}
		})
	}
}

// Esc stays with each caller: the shared line must not swallow it, and
// typed text does not change what it does. Passed already; it guards
// the merge. Configuration kills the stream and goes to the menu,
// rotation closes stdin and waits for the engine, the notice goes back
// to the profile screen.
func TestInputLine_EscKeepsEachCallersMeaning(t *testing.T) {
	t.Run("notice goes back to the profile screen", func(t *testing.T) {
		m := awakeNotice(t, engineRunSeams{})
		m = typeText(t, m, "I've")
		m, _ = updateAs(t, m, key(tea.KeyEsc))
		if m.state != installStateProfile {
			t.Fatalf("state = %v, want installStateProfile", m.state)
		}
	})

	t.Run("rotation closes stdin and waits", func(t *testing.T) {
		m := sizedRepair(t)
		m.state = repairRotating
		stdin := &recordingStdin{}
		m, _ = updateAs(t, m, repairRotateReadyMsg{stream: &engine.Stream{C: make(chan any)}, stdin: stdin})
		m = repairLines(t, m, "prompt field=action-word kind=typed-word required=true attempt=1")
		m, _ = updateAs(t, m, runeKey('a'))
		m, cmd := updateAs(t, m, key(tea.KeyEsc))
		if !stdin.closed {
			t.Fatal("Esc did not close the session's stdin")
		}
		if isQuit(cmd) || m.state != repairRotating {
			t.Fatalf("Esc left state %v (quit=%v), want repairRotating until the engine exits", m.state, isQuit(cmd))
		}
		if stdin.String() != "" {
			t.Fatalf("Esc sent the half-typed answer %q to the engine", stdin.String())
		}
	})

	t.Run("configuration kills the stream and goes to the menu", func(t *testing.T) {
		r := collectingRun(t, engineRunSeams{})
		s := sleepingStream(t)
		stdin := &recordingStdin{}
		r.cfg.stream = s
		r.cfg.stdin = stdin
		r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt field=OIDC_CLIENT_ID kind=text required=true attempt=1"}})
		r, _ = r.update(runeKey('a'))
		r, _ = r.update(key(tea.KeyEsc))
		if o := outcomeOf(r); !leftForMenu(o) {
			t.Fatalf("outcome = %+v, want back to the main menu without success (the session was cancelled)", o)
		}
		awaitEnd(t, s)
		if stdin.String() != "" {
			t.Fatalf("Esc sent the half-typed answer %q to the engine", stdin.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Source scans: the shape the decisions ask for.
// ---------------------------------------------------------------------------

// parseUISources parses every non-test Go file under internal/ui.
func parseUISources(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, path := range uiSourceFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = f
	}
	return fset, out
}

// typeDecls lists where a package-level type of this name is declared.
func typeDecls(files map[string]*ast.File, name string) []string {
	var at []string
	for path, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				if ts, ok := s.(*ast.TypeSpec); ok && ts.Name.Name == name {
					at = append(at, path)
				}
			}
		}
	}
	return at
}

// TestInputLine_DeclaredExactlyOnce pins the done criterion "one
// inputLine": the typing surface is a named value type in one place.
func TestInputLine_DeclaredExactlyOnce(t *testing.T) {
	_, files := parseUISources(t)
	if at := typeDecls(files, "inputLine"); len(at) != 1 {
		t.Fatalf("type inputLine is declared %d times (%v), want exactly once", len(at), at)
	}
}

// selectorUses lists the files that mention pkg.name as a selector.
func selectorUses(files map[string]*ast.File, pkg, name string) []string {
	var at []string
	for path, f := range files {
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != name {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg {
				found = true
			}
			return true
		})
		if found {
			at = append(at, path)
		}
	}
	return at
}

// TestInputLine_BackspaceIsHandledInOnePlace pins "one inputLine" by
// its footprint: the backspace key, which all three typing surfaces
// handle today in three separate switches, is named in exactly one
// non-test file. (At least one, so a rewrite that stopped handling
// backspace cannot pass.)
func TestInputLine_BackspaceIsHandledInOnePlace(t *testing.T) {
	_, files := parseUISources(t)
	if at := selectorUses(files, "tea", "KeyBackspace"); len(at) != 1 {
		t.Fatalf("tea.KeyBackspace is named in %d files (%v), want exactly one: the input line", len(at), at)
	}
}

// ctrlGuards finds every `!x.Mod.Contains(tea.ModCtrl)` in a file: the
// guard that kept Ctrl-held keys out of the notice's box.
func ctrlGuards(fset *token.FileSet, f *ast.File) []string {
	var at []string
	ast.Inspect(f, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpr)
		if !ok || u.Op != token.NOT {
			return true
		}
		call, ok := u.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fn.Sel.Name != "Contains" {
			return true
		}
		for _, a := range call.Args {
			if sel, ok := a.(*ast.SelectorExpr); ok && sel.Sel.Name == "ModCtrl" {
				at = append(at, fset.Position(u.Pos()).String())
			}
		}
		return true
	})
	return at
}

// TestCtrlGuardScanSeesTheGuard proves the scan below can fire: it
// finds the guard in a snippet shaped like the notice's old one and
// ignores a positive Ctrl check (Ctrl-C handling is legitimate).
func TestCtrlGuardScanSeesTheGuard(t *testing.T) {
	const guarded = `package p
func f(msg tea.KeyPressMsg) {
	if msg.Text != "" && !msg.Mod.Contains(tea.ModCtrl) {
		_ = msg.Text
	}
}`
	const plain = `package p
func f(msg tea.KeyPressMsg) {
	if msg.Mod.Contains(tea.ModCtrl) && msg.Code == 'c' {
		return
	}
}`
	fset := token.NewFileSet()
	g, err := parser.ParseFile(fset, "guarded.go", guarded, 0)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parser.ParseFile(fset, "plain.go", plain, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctrlGuards(fset, g)) != 1 {
		t.Error("the scan missed a `!Mod.Contains(tea.ModCtrl)` guard")
	}
	if len(ctrlGuards(fset, p)) != 0 {
		t.Error("the scan flagged a positive Ctrl check")
	}
}

// TestInputLine_NoCtrlGuardRemains pins "the Ctrl guard is gone": no
// non-test file in internal/ui negates a Ctrl-modifier test around
// typing. The behaviour test above proves a modifier key still types
// nothing without it.
func TestInputLine_NoCtrlGuardRemains(t *testing.T) {
	fset, files := parseUISources(t)
	for path, f := range files {
		for _, at := range ctrlGuards(fset, f) {
			t.Errorf("%s: Ctrl guard `!Mod.Contains(tea.ModCtrl)` remains at %s; Text is empty under modifier keys, so it is redundant", path, at)
		}
	}
}
