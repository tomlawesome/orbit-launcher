package ui

import (
	"strings"
	"testing"
	"time"
)

// The mission console's rarer renderings: every state's glyph and words,
// a box overflowing with entries, over-wide lines and narrow terminals.

func consoleRows(c ConsoleModel, width, height int) []string {
	var rows []string
	for _, l := range c.contentLines(width, height) {
		rows = append(rows, stripANSI(l))
	}
	return rows
}

func TestConsole_EachStateHasItsGlyphAndWords(t *testing.T) {
	c := testConsole()
	c = c.observeEvent(consoleEvent("pull", "image", "skipped", "image-present", "skip"))
	c = c.observeEvent(consoleEvent("database", "database", "blocked", "volume-ownership", "stop"))
	c = c.observeEvent(consoleEvent("database", "migrations", "waiting", "migration", "wait"))
	c = c.observeEvent(consoleEvent("application", "application", "running", "application-health", "start"))
	c = c.observeRaw("legacy prose line")
	rows := strings.Join(consoleRows(c, 80, 26), "\n")
	for _, want := range []string{
		"· image skipped",
		"✗ database blocked — volume-ownership",
		// Not the newest entry, so it waits quietly instead of spinning.
		"· migrations waiting",
		"· application running",
		"legacy prose line",
	} {
		if !strings.Contains(rows, want) {
			t.Errorf("console lacks %q:\n%s", want, rows)
		}
	}
}

func TestConsole_OnlyTheNewestActiveEntrySpins(t *testing.T) {
	c := testConsole()
	c = c.observeEvent(consoleEvent("database", "database", "starting", "database-health", "start"))
	rows := strings.Join(consoleRows(c, 80, 26), "\n")
	if !strings.Contains(rows, string(spinnerFrames[0])+" database starting") {
		t.Fatalf("the newest active entry should carry the spinner:\n%s", rows)
	}
	c = c.advance()
	if rows := strings.Join(consoleRows(c, 80, 26), "\n"); !strings.Contains(rows, string(spinnerFrames[1])+" database starting") {
		t.Fatalf("the spinner did not move on a tick:\n%s", rows)
	}
}

func TestConsole_BoxShowsTheNewestEntriesWhenFull(t *testing.T) {
	c := testConsole()
	for i := 0; i < 40; i++ {
		c = c.observeRaw("line " + string(rune('A'+i%26)) + strings.Repeat("x", i/26))
	}
	rows := consoleRows(c, 80, 20) // interior: 20-1-8 = 11 rows
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "line A\n") || strings.Contains(joined, "│ line A ") {
		t.Fatalf("the oldest entry should have scrolled out:\n%s", joined)
	}
	if !strings.Contains(joined, "line Nx") {
		t.Fatalf("the newest entry is missing:\n%s", joined)
	}
	// title, blank, box top, 11 entry rows, box bottom, blank, bar, stage.
	if len(rows) != 18 {
		t.Fatalf("content is %d rows, want 18 on a 20-row terminal", len(rows))
	}
}

func TestConsole_OverWideLineIsCutToTheBox(t *testing.T) {
	c := testConsole()
	c = c.observeRaw(strings.Repeat("y", 200))
	for _, row := range c.contentLines(80, 26) {
		if w := visibleWidth(row); w > 76 {
			t.Fatalf("a row is %d cells wide, past the 76-cell box:\n%s", w, stripANSI(row))
		}
	}
}

func TestConsole_NarrowTerminals(t *testing.T) {
	c := testConsole()
	c = c.observeRaw("hello")
	// Under 24 columns the box takes the whole width rather than
	// shrinking below usefulness.
	// Every row, the title row included, fits the terminal at any
	// width — down to the clock's own width and below it (#187).
	for width := 2; width <= 40; width++ {
		for _, row := range consoleRows(c, width, 26) {
			if w := len([]rune(row)); w > width {
				t.Fatalf("a row is %d cells on a %d-column terminal: %q", w, width, row)
			}
		}
	}
	rows := consoleRows(c, 18, 26)
	if !strings.HasPrefix(rows[2], "╭") || !strings.HasSuffix(rows[2], "╮") || len([]rune(rows[2])) != 18 {
		t.Fatalf("box top should span the terminal: %q", rows[2])
	}
	// A terminal too small for any box draws no content at all.
	if got := c.contentLines(1, 26); got != nil {
		t.Fatalf("a 1-column terminal drew %q", got)
	}
}

func TestConsole_TitleRowDegradesInAFixedOrder(t *testing.T) {
	c := testConsole()
	cases := []struct {
		width int
		want  string
	}{
		{35, "ORBIT · Install — Standard 0:00"}, // box 31: everything fits
		{31, "ORBIT · Install — Stan 0:00"},     // box 27: the title text is cut
		{18, "ORBIT · Insta 0:00"},              // box 18: still cut
		{17, "ORBIT · Inst 0:00"},               // four cells of title is the floor
		{16, "ORBIT       0:00"},                // under four: title and separator go
		{10, "ORBIT 0:00"},                      // the mark's last width
		{9, "     0:00"},                        // the mark goes; clock right-aligned
		{4, "0:00"},                             // the clock alone
		{3, "0:0"},                              // the clock is cut last of all
		{2, "0:"},
	}
	for _, tc := range cases {
		if got := consoleRows(c, tc.width, 26)[0]; got != tc.want {
			t.Errorf("title row at width %d = %q, want %q", tc.width, got, tc.want)
		}
	}
}

func TestConsole_ShortTerminalStillShowsThreeEntryRows(t *testing.T) {
	c := testConsole()
	for _, s := range []string{"one", "two", "three", "four"} {
		c = c.observeRaw(s)
	}
	rows := strings.Join(consoleRows(c, 80, 6), "\n")
	for _, want := range []string{"two", "three", "four"} {
		if !strings.Contains(rows, want) {
			t.Errorf("short terminal lost %q:\n%s", want, rows)
		}
	}
	if strings.Contains(rows, "one") {
		t.Errorf("only three entry rows fit:\n%s", rows)
	}
}

func TestConsole_RollbackColoursTheBarAndNamesTheStage(t *testing.T) {
	c := testConsole()
	c = c.observeEvent(consoleEvent("database", "database", "completed", "database-health", "start"))
	c = c.observeEvent(consoleEvent("rollback", "installer", "running", "rollback", "rollback"))
	rows := consoleRows(c, 80, 26)
	if got := rows[len(rows)-1]; !strings.HasPrefix(got, c.stageWord()) {
		t.Fatalf("stage row = %q, want it to lead with %q", got, c.stageWord())
	}
}

func TestFormatClockAndAchieved_ClampNegativeDurations(t *testing.T) {
	// A clock that steps backwards must never show a minus sign.
	if got := formatClock(-5 * time.Second); got != "0:00" {
		t.Errorf("formatClock(-5s) = %q, want 0:00", got)
	}
	if got := formatAchieved(-5 * time.Second); got != "0s" {
		t.Errorf("formatAchieved(-5s) = %q, want 0s", got)
	}
}
