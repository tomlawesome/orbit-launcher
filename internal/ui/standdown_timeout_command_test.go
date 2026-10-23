package ui

// Pins audit v0.4.3-c G2 (#293): when the stand-down hits its limit, the
// one command the failed screen offers (it has no Copy row) is the
// command deploy.StandDownCommandWords quotes, byte for byte, and typed
// into bash it names the person's real folder. Before the fix every
// backslash in the shown command was doubled, so a folder holding a
// backslash, a control character, a bidi mark or a no-break space came
// out as a folder that does not exist.

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const timeoutRetrySentence = "Try again manually later with:"

// timeoutCommandFolders are the folders G2 names: the odd ones, then the
// plain one whose row must not change. Invisible and bidi characters are
// written as escapes, never raw.
var timeoutCommandFolders = []struct {
	name, folder string
}{
	{"backslash", `/opt/back\slash`},
	{"escape", "/srv/odd\x1bname"},
	{"bidi", "/srv/a\u202eb"},
	{"nbsp", "/srv/a\u00a0b"},
	{"apostrophe-newline", "/srv/o'brien\nx"},
	{"plain", "/opt/orbit"},
}

// timedOutFailedScreen drives Remove to the failed screen of a stand-down
// that hit its 5-minute limit on folder, at width columns, and returns the
// screen with styling stripped.
func timedOutFailedScreen(t *testing.T, folder string, width int) string {
	t.Helper()
	m := NewRemoveModel(&deploy.Deployment{TargetDir: folder, AppURL: "https://mail.example.com"})
	m.standDown = func(context.Context, string) error {
		return &deploy.StandDownTimeoutError{Limit: 5 * time.Minute, Words: deploy.StandDownCommandWords(folder)}
	}
	m.clipboard = io.Discard
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
	m, run := updateAs(t, m, key(tea.KeyEnter)) // Stand down Orbit is preselected
	if run == nil {
		t.Fatalf("confirming started nothing:\n%s", screen(m))
	}
	m, _ = updateAs(t, m, run())
	s := screen(m)
	flat := proseOnly(s)
	if !strings.Contains(flat, "Stand-down stopped") || !strings.Contains(flat, "The stack hasn't come down after 5 minutes.") {
		t.Fatalf("not the timed-out failed screen (state %v):\n%s", m.state, s)
	}
	return s
}

// trimStarfield is row without the starfield glyphs (the ones proseOnly
// drops) that can land in the gaps either side of a row's text, and
// without the spaces around it; the text between is kept byte for byte.
func trimStarfield(row string) string {
	isStar := func(w string) bool { return strings.Trim(w, ".·✦●") == "" }
	fields := strings.Fields(row)
	for len(fields) > 0 && isStar(fields[0]) {
		row = strings.TrimLeft(row, " ")
		row = strings.TrimPrefix(row, fields[0])
		fields = fields[1:]
	}
	for len(fields) > 0 && isStar(fields[len(fields)-1]) {
		row = strings.TrimRight(row, " ")
		row = strings.TrimSuffix(row, fields[len(fields)-1])
		fields = fields[:len(fields)-1]
	}
	return strings.Trim(row, " ")
}

// timeoutCommandRows is the rows after the row that finishes the "Try
// again manually later with:" sentence, up to the next blank row, each
// with the starfield and spaces either side trimmed.
func timeoutCommandRows(t *testing.T, s string) []string {
	t.Helper()
	rows := strings.Split(s, "\n")
	start := -1
	for i := range rows {
		if strings.HasSuffix(proseOnly(strings.Join(rows[:i+1], "\n")), timeoutRetrySentence) {
			start = i + 1
			break
		}
	}
	if start < 0 || start >= len(rows) {
		t.Fatalf("no row follows %q:\n%s", timeoutRetrySentence, s)
	}
	var out []string
	for _, r := range rows[start:] {
		r = trimStarfield(r)
		if r == "" {
			break
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		t.Fatalf("the row after %q is blank:\n%s", timeoutRetrySentence, s)
	}
	return out
}

// bashWords is what bash makes of the shown command's words after
// "docker compose": printf prints each word NUL-terminated, nothing else
// in the text is run.
func bashWords(t *testing.T, shown string) []string {
	t.Helper()
	rest, ok := strings.CutPrefix(shown, "docker compose ")
	if !ok {
		t.Fatalf("shown command %q does not start with \"docker compose \"", shown)
		return nil
	}
	cmd := exec.Command("bash", "-c", `printf '%s\0' `+rest)
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash could not read the shown command %q: %v", shown, err)
	}
	words := strings.Split(string(out), "\x00")
	if len(words) > 0 && words[len(words)-1] == "" {
		words = words[:len(words)-1]
	}
	return words
}

func wantFolderWords(folder string) []string {
	return []string{"--project-directory", folder, "--env-file", folder + "/" + deploy.EnvFile, "down"}
}

// checkShownCommand is checks 1 and 2: the shown command is the quoted
// words joined by spaces, and bash reads it back as the real folder.
func checkShownCommand(t *testing.T, folder, shown, s string) {
	t.Helper()
	if want := strings.Join(deploy.StandDownCommandWords(folder), " "); shown != want {
		t.Errorf("shown command = %q, want %q:\n%s", shown, want, s)
	}
	got := bashWords(t, shown)
	want := wantFolderWords(folder)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
		t.Errorf("bash reads the shown command %q as %q, want %q", shown, got, want)
	}
}

// 1 and 2 at 200 columns, where the command is one row.
func TestStandDownTimeout_ShownCommandIsTheQuotedWordsAndNamesTheRealFolder(t *testing.T) {
	if deploy.EnvFile != ".env-orbit" {
		t.Fatalf("deploy.EnvFile = %q, want .env-orbit", deploy.EnvFile)
	}
	for _, c := range timeoutCommandFolders {
		t.Run(c.name, func(t *testing.T) {
			s := timedOutFailedScreen(t, c.folder, 200)
			shown := timeoutCommandRows(t, s)[0]
			checkShownCommand(t, c.folder, shown, s)
		})
	}
}

// 3. Nothing raw reaches the screen: every rune is printable or a plain
// space (rows are split on newlines).
func TestStandDownTimeout_ScreenHoldsNoRawControlOrInvisibleCharacter(t *testing.T) {
	for _, c := range timeoutCommandFolders {
		t.Run(c.name, func(t *testing.T) {
			s := timedOutFailedScreen(t, c.folder, 200)
			for i, row := range strings.Split(s, "\n") {
				for _, r := range row {
					if r != ' ' && !unicode.IsPrint(r) {
						t.Errorf("row %d holds %U, not printable:\n%q", i, r, row)
					}
				}
			}
		})
	}
}

// 4. The plain folder's row is unchanged.
func TestStandDownTimeout_PlainFolderRowIsUnchanged(t *testing.T) {
	s := timedOutFailedScreen(t, "/opt/orbit", 200)
	const want = "docker compose --project-directory /opt/orbit --env-file /opt/orbit/.env-orbit down"
	if got := timeoutCommandRows(t, s)[0]; got != want {
		t.Errorf("command row = %q, want %q:\n%s", got, want, s)
	}
}

// Narrow case: at 50 columns the command may wrap; read back the way
// remove_narrow_test.go does, then apply checks 1 and 2.
func TestStandDownTimeout_NarrowShownCommandReadsBackAsTheQuotedWords(t *testing.T) {
	for _, c := range timeoutCommandFolders {
		t.Run(c.name, func(t *testing.T) {
			s := timedOutFailedScreen(t, c.folder, 50)
			shown := strings.TrimSpace(narrowReadBack(timeoutCommandRows(t, s)))
			checkShownCommand(t, c.folder, shown, s)
		})
	}
}
