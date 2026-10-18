package ui

// Pins #211 (prompt session, #199 WI-7; UI-10). In-console configuration
// and the rotation flow in Repair both speak the engine's machine-prompt
// protocol (prompt, prompt-reject, prompt-accept, prompt-abort) and draw
// the same prompt screen. The decisions give them one `promptSession`
// (state, apply-line, view), and the hard-coded "of 3" becomes
// engine.PromptMaxAttempts.

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// ---------------------------------------------------------------------------
// Rendering: the same engine lines draw the same prompt in both flows.
// ---------------------------------------------------------------------------

// promptDriver feeds engine lines and keys to one flow and reads its
// screen.
type promptDriver struct {
	line   func(string)
	press  func(tea.KeyPressMsg)
	screen func() string
}

func rotationDriver(t *testing.T) promptDriver {
	m := sizedRepair(t)
	m.state = repairRotating
	return promptDriver{
		line:   func(l string) { m = repairLines(t, m, l) },
		press:  func(k tea.KeyPressMsg) { m, _ = updateAs(t, m, k) },
		screen: func() string { return screen(m) },
	}
}

func configDriver(t *testing.T) promptDriver {
	r := collectingRun(t, engineRunSeams{})
	r.cfg.stream = &engine.Stream{C: make(chan any)}
	r.cfg.stdin = &recordingStdin{}
	return promptDriver{
		line:   func(l string) { r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: l}}) },
		press:  func(k tea.KeyPressMsg) { r, _ = r.update(k) },
		screen: func() string { return runScreen(r) },
	}
}

// skyGlyphs are the starfield's marks; they drift into any screen's
// blank space and differ between flows, so they are not part of the
// prompt.
var skyGlyphs = map[string]bool{".": true, "·": true, "✦": true, "●": true, "⟡": true, "*": true, "+": true}

// promptBody is the prompt as a person reads it: from the line holding
// start (the field's label, or the waiting words) to the end of the
// screen, with the sky and the surrounding blank space removed. The
// flows' own headers sit above it and differ by design.
func promptBody(t *testing.T, screen, start string) string {
	t.Helper()
	var kept []string
	found := false
	for _, line := range strings.Split(screen, "\n") {
		var words []string
		for _, w := range strings.Fields(line) {
			if !skyGlyphs[w] {
				words = append(words, w)
			}
		}
		joined := strings.Join(words, " ")
		if strings.Contains(joined, start) {
			found = true
		}
		if found && joined != "" {
			kept = append(kept, joined)
		}
	}
	if !found {
		t.Fatalf("no line holds %q:\n%s", start, screen)
	}
	return strings.Join(kept, "\n")
}

// TestPromptSession_BothFlowsDrawTheSamePrompt is the shared rendering
// test the done criterion asks for: the same engine lines (and the same
// typing) give the same prompt in configuration and in rotation, at
// every stage of a field's life. Every stage also pins a fact about the
// prompt itself, so identical emptiness cannot pass.
func TestPromptSession_BothFlowsDrawTheSamePrompt(t *testing.T) {
	type step struct {
		name  string
		lines []string
		keys  []tea.KeyPressMsg
		start string   // where the prompt body begins on screen
		want  []string // facts the body must state
		never []string // words that must not appear anywhere
	}
	typed := func(s string) []tea.KeyPressMsg {
		var ks []tea.KeyPressMsg
		for _, r := range s {
			ks = append(ks, runeKey(r))
		}
		return ks
	}
	steps := []step{
		{
			name:  "waiting for the first prompt",
			start: "talking to the engine",
			want:  []string{"talking to the engine"},
		},
		{
			name:  "a first prompt with a hint",
			lines: []string{"prompt field=APP_URL kind=url required=true attempt=1"},
			start: "Public Orbit origin",
			want:  []string{"Public Orbit origin", "the https:// address Orbit will live at", "▸ ▏"},
		},
		{
			name:  "typed text shows with the cursor",
			lines: []string{"prompt field=OIDC_CLIENT_ID kind=text required=true attempt=1"},
			keys:  typed("abc"),
			start: "OIDC client ID",
			want:  []string{"OIDC client ID", "▸ abc▏"},
		},
		{
			name: "a rejection names its reason and the attempt",
			lines: []string{
				"prompt field=APP_URL kind=url required=true attempt=1",
				"prompt-reject field=APP_URL reason=not-https",
				"prompt field=APP_URL kind=url required=true attempt=2",
			},
			start: "Public Orbit origin",
			want:  []string{"must start with https://", "attempt 2 of 3"},
		},
		{
			name: "the last attempt",
			lines: []string{
				"prompt-reject field=APP_URL reason=empty",
				"prompt field=APP_URL kind=url required=true attempt=3",
			},
			start: "Public Orbit origin",
			want:  []string{"can't be empty", "attempt 3 of 3"},
		},
		{
			name: "an accept clears the reason",
			lines: []string{
				"prompt-reject field=checkpoint-passphrase reason=too-large",
				"prompt-accept field=checkpoint-passphrase",
				"prompt field=checkpoint-passphrase-confirm kind=secret required=true attempt=1",
			},
			start: "Confirm the passphrase",
			want:  []string{"Confirm the passphrase"},
			never: []string{"too large"},
		},
		{
			name:  "a secret is never echoed",
			lines: []string{"prompt field=checkpoint-passphrase kind=secret required=true attempt=1"},
			keys:  typed("hunter2"),
			start: "Checkpoint passphrase",
			want:  []string{"Checkpoint passphrase", "input hidden"},
			never: []string{"hunter2", "hunter"},
		},
		{
			name: "an abort clears the prompt",
			lines: []string{
				"prompt field=APP_URL kind=url required=true attempt=3",
				"prompt-abort field=APP_URL",
			},
			start: "talking to the engine",
			want:  []string{"talking to the engine"},
			never: []string{"Public Orbit origin"},
		},
	}

	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			bodies := map[string]string{}
			for name, mk := range map[string]func(*testing.T) promptDriver{
				"configuration": configDriver,
				"rotation":      rotationDriver,
			} {
				d := mk(t)
				for _, l := range s.lines {
					d.line(l)
				}
				for _, k := range s.keys {
					d.press(k)
				}
				screen := d.screen()
				for _, w := range s.never {
					if strings.Contains(screen, w) {
						t.Errorf("%s: the screen shows %q:\n%s", name, w, screen)
					}
				}
				body := promptBody(t, screen, s.start)
				for _, w := range s.want {
					if !strings.Contains(body, w) {
						t.Errorf("%s: the prompt does not state %q:\n%s", name, w, body)
					}
				}
				bodies[name] = body
			}
			if bodies["configuration"] != bodies["rotation"] {
				t.Errorf("the two flows draw different prompts\nconfiguration:\n%s\n\nrotation:\n%s", bodies["configuration"], bodies["rotation"])
			}
		})
	}
}

// TestPromptSession_AFreshPromptStartsWithAnEmptyAnswer: typed text is
// cleared when the engine asks again, so a rejected answer is not
// silently resubmitted. Both flows, one table.
func TestPromptSession_AFreshPromptStartsWithAnEmptyAnswer(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) promptDriver{
		"configuration": configDriver,
		"rotation":      rotationDriver,
	} {
		t.Run(name, func(t *testing.T) {
			d := mk(t)
			d.line("prompt field=APP_URL kind=url required=true attempt=1")
			d.press(runeKey('x'))
			if got := inputRowText(d.screen()); got != "x" {
				t.Fatalf("typing left %q, want %q", got, "x")
			}
			d.line("prompt-reject field=APP_URL reason=not-https")
			d.line("prompt field=APP_URL kind=url required=true attempt=2")
			if got := inputRowText(d.screen()); got != "" {
				t.Fatalf("a fresh prompt kept the old answer %q", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The attempt limit has one name, in engine.
// ---------------------------------------------------------------------------

var ofThree = regexp.MustCompile(`\bof\s+3\b`)

// stringLiteralsMatching lists the string literals in files that match re.
func stringLiteralsMatching(fset *token.FileSet, files map[string]*ast.File, re *regexp.Regexp) []string {
	var at []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && re.MatchString(s) {
				at = append(at, fset.Position(lit.Pos()).String()+": "+lit.Value)
			}
			return true
		})
	}
	return at
}

// TestPromptSession_AttemptLimitIsEnginesName pins "of 3 becomes
// engine.PromptMaxAttempts": no string literal in internal/ui spells the
// limit, and the UI does reach for the engine's constant (so a view that
// shows no limit at all cannot pass).
func TestPromptSession_AttemptLimitIsEnginesName(t *testing.T) {
	fset, files := parseUISources(t)
	for _, at := range stringLiteralsMatching(fset, files, ofThree) {
		t.Errorf("a string literal hard-codes the attempt limit: %s", at)
	}
	if at := selectorUses(files, "engine", "PromptMaxAttempts"); len(at) == 0 {
		t.Error("no file in internal/ui uses engine.PromptMaxAttempts")
	}
}

// TestAttemptLimitScanSeesTheLiteral proves the scan above can fire.
func TestAttemptLimitScanSeesTheLiteral(t *testing.T) {
	if !ofThree.MatchString("attempt %d of 3") || ofThree.MatchString("attempt %d of %d") {
		t.Fatal("the pattern does not tell a hard-coded limit from a formatted one")
	}
}

// ---------------------------------------------------------------------------
// One promptSession.
// ---------------------------------------------------------------------------

// TestPromptSession_DeclaredExactlyOnce pins the done criterion "one
// promptSession": the machine-prompt state, apply-line and view are a
// named type in one place.
func TestPromptSession_DeclaredExactlyOnce(t *testing.T) {
	_, files := parseUISources(t)
	if at := typeDecls(files, "promptSession"); len(at) != 1 {
		t.Fatalf("type promptSession is declared %d times (%v), want exactly once", len(at), at)
	}
}

// TestPromptSession_PromptLineKindsAreSwitchedOnOnce pins "one
// apply-line": the four engine prompt message types (Prompt,
// PromptReject, PromptAccept, PromptAbort) are each named in exactly
// one non-test file. Today two flows each carry their own switch.
func TestPromptSession_PromptLineKindsAreSwitchedOnOnce(t *testing.T) {
	_, files := parseUISources(t)
	for _, name := range []string{"Prompt", "PromptReject", "PromptAccept", "PromptAbort"} {
		if at := selectorUses(files, "engine", name); len(at) != 1 {
			t.Errorf("engine.%s is named in %d files (%v), want exactly one: the prompt session", name, len(at), at)
		}
	}
}
