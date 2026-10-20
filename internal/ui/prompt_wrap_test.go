package ui

// Pins #269 (prompt hints and notes wrap to the screen width). On the
// guided-configuration and Repair prompt screens a field's hint and note
// lines were printed at full length; on a narrow terminal a line ran past
// the right edge and the terminal wrapped it, pushing the rest of the
// screen down a row.
//
// What the specification requires, and these tests check:
//   - no line of the prompt screen is wider than the screen, at 40 and
//     80 columns, for every field promptWords knows, for a secret and a
//     text prompt, with and without an APP_URL origin;
//   - wrapping loses nothing: the hint and note words are all still there,
//     in order (a word wider than the screen may be cut across lines, so
//     for those widths the check is on the characters, spaces aside);
//   - the Authentik/Keycloak example URLs are kept whole on a line where
//     they fit (a deliberate choice, not a mid-URL break);
//   - an unsized screen (width 0) is not wrapped at all, as today.
//
// The reference text for "nothing lost" is the same screen rendered at
// width 0, which prints every line whole.

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// Every field promptWords knows (configcollect.go), listed by name so a
// field added later without being listed here does not silently drop out
// of the check: add it to this list.
var promptWrapFields = []string{
	"APP_URL",
	"OIDC_ISSUER",
	"OIDC_CLIENT_ID",
	"OIDC_CLIENT_SECRET",
	"action-word",
	"checkpoint-passphrase",
	"checkpoint-passphrase-confirm",
	"safe-batch",
}

var (
	promptWrapKinds   = []string{"secret", "text"}
	promptWrapOrigins = []string{"", "https://orbit.example.com"}
	promptWrapWidths  = []int{40, 80}
)

// promptWrapScreen renders the prompt session for one field at a width
// and returns the screen with escapes stripped, split into lines.
func promptWrapScreen(field, kind, origin string, width int) []string {
	p := engine.Prompt{Field: field, Kind: kind, Attempt: 1, Required: true}
	s := promptSession{prompt: &p, input: inputLine{runes: []rune("abc")}}
	var b strings.Builder
	s.view(&b, origin, width, 0)
	return strings.Split(ansi.Strip(b.String()), "\n")
}

// promptWrapProse is the screen's lines without the input row (the row
// that starts with the "▸" marker), which is not hint or note text.
func promptWrapProse(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "▸") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func promptWrapEachCase(t *testing.T, fn func(t *testing.T, field, kind, origin string)) {
	t.Helper()
	for _, field := range promptWrapFields {
		for _, kind := range promptWrapKinds {
			for _, origin := range promptWrapOrigins {
				name := fmt.Sprintf("%s/%s/origin=%t", field, kind, origin != "")
				t.Run(name, func(t *testing.T) { fn(t, field, kind, origin) })
			}
		}
	}
}

func TestPromptWrap_NoLineIsWiderThanTheScreen(t *testing.T) {
	promptWrapEachCase(t, func(t *testing.T, field, kind, origin string) {
		for _, width := range promptWrapWidths {
			for i, line := range promptWrapScreen(field, kind, origin, width) {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("width %d: line %d is %d columns wide: %q", width, i, w, line)
				}
			}
		}
	})
}

func TestPromptWrap_NoWordIsLost(t *testing.T) {
	promptWrapEachCase(t, func(t *testing.T, field, kind, origin string) {
		ref := promptWrapProse(promptWrapScreen(field, kind, origin, 0))
		refWords := strings.Fields(strings.Join(ref, " "))
		widest := 0
		for _, w := range refWords {
			widest = max(widest, lipgloss.Width(w))
		}
		for _, width := range promptWrapWidths {
			got := promptWrapProse(promptWrapScreen(field, kind, origin, width))
			gotWords := strings.Fields(strings.Join(got, " "))
			if widest <= width {
				// Nothing has to be cut inside a word: the words come out
				// the same, in the same order.
				if strings.Join(gotWords, " ") != strings.Join(refWords, " ") {
					t.Errorf("width %d: words changed.\nwant: %q\n got: %q", width, refWords, gotWords)
				}
				continue
			}
			// A word wider than the screen (a long URL at 40 columns) has
			// to be cut somewhere to honour "never wider than the screen";
			// then the characters must all survive, in order.
			if strings.Join(gotWords, "") != strings.Join(refWords, "") {
				t.Errorf("width %d: characters lost or reordered.\nwant: %q\n got: %q", width, refWords, gotWords)
			}
		}
	})
}

// The Authentik/Keycloak example URLs (and any other URL in a note) are
// not broken across lines where they fit. At 80 columns every URL in the
// OIDC_ISSUER and OIDC_CLIENT_ID notes fits, so each must appear whole on
// one line. At 40 columns a URL of 40 characters or fewer must still be
// whole on one line; a URL wider than 40 cannot be whole without breaking
// "never wider than the screen", so for those the only assertion is the
// width and no-loss checks in the tests above.
func TestPromptWrap_ExampleURLsStayWhole(t *testing.T) {
	for _, field := range []string{"OIDC_ISSUER", "OIDC_CLIENT_ID"} {
		for _, origin := range promptWrapOrigins {
			var urls []string
			for _, l := range promptWrapProse(promptWrapScreen(field, "text", origin, 0)) {
				for _, w := range strings.Fields(l) {
					if strings.Contains(w, "://") && !strings.HasSuffix(w, "://") {
						urls = append(urls, strings.TrimRight(w, ",;)("))
					}
				}
			}
			if field == "OIDC_ISSUER" && len(urls) < 2 {
				t.Fatalf("%s: expected the Authentik and Keycloak example URLs in the notes, found %q", field, urls)
			}
			for _, width := range promptWrapWidths {
				lines := promptWrapProse(promptWrapScreen(field, "text", origin, width))
				for _, u := range urls {
					if lipgloss.Width(u) > width {
						continue // cannot be whole and fit; see the comment above
					}
					whole := false
					for _, l := range lines {
						if strings.Contains(l, u) {
							whole = true
							break
						}
					}
					if !whole {
						t.Errorf("%s width %d origin %q: %q is broken across lines:\n%s", field, width, origin, u, strings.Join(lines, "\n"))
					}
				}
			}
		}
	}
}

// An unsized screen (width 0, before the first window-size message) keeps
// today's form: nothing is wrapped, so every note is on one line, whole.
// (testdata/c9 pins the exact bytes for two of the fields.)
func TestPromptWrap_UnsizedScreenIsNotWrapped(t *testing.T) {
	promptWrapEachCase(t, func(t *testing.T, field, kind, origin string) {
		label, hint, notes := promptWords(field, origin)
		lines := promptWrapScreen(field, kind, origin, 0)
		has := func(want string) bool {
			for _, l := range lines {
				if strings.TrimSpace(l) == want {
					return true
				}
			}
			return false
		}
		if !has(label) {
			t.Errorf("no line is exactly the label %q:\n%s", label, strings.Join(lines, "\n"))
		}
		if hint != "" && kind != "secret" && !has(hint) {
			t.Errorf("no line is exactly the hint %q:\n%s", hint, strings.Join(lines, "\n"))
		}
		for _, n := range notes {
			if !has(n) {
				t.Errorf("no line is exactly the note %q:\n%s", n, strings.Join(lines, "\n"))
			}
		}
	})
}
