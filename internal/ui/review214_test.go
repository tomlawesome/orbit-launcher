package ui

// Pins the #214 review (findings 1 and 3): the console's cut measures
// grapheme clusters, the same unit lipgloss.Width counts, and the
// wrapper keeps the line breaks and spacing it was given.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Clusters whose width is not the sum of their runes' widths: a
// variation selector or keycap measures 0 alone, and a ZWJ family is
// one two-cell cluster of five runes.
var review214Clusters = map[string]string{
	"warning sign": "⚠️",
	"red heart":    "❤️",
	"keycap one":   "1️⃣",
	"family":       "👨‍👩‍👧",
	"flag":         "🇬🇧",
	"accented e":   "é",
	"cjk":          "日",
	"plain letter": "a",
}

func TestTruncateStyled_CutsWholeClustersWithinTheWidth(t *testing.T) {
	for name, cluster := range review214Clusters {
		in := strings.Repeat(cluster, 40)
		for w := 1; w <= 30; w++ {
			got := truncateStyled(in, w)
			if gw := lipgloss.Width(got); gw > w {
				t.Errorf("%s at %d cells: cut is %d cells: %q", name, w, gw, got)
			}
			// Whole clusters only: the cut is k copies of the cluster.
			if k := len(got) / len(cluster); got != strings.Repeat(cluster, k) {
				t.Errorf("%s at %d cells: cut ends mid-cluster: %q", name, w, got)
			}
			// And not cut short: at most one cell is left unused.
			if gw := lipgloss.Width(got); lipgloss.Width(in) > w && gw < w-1 {
				t.Errorf("%s at %d cells: cut is only %d cells: %q", name, w, gw, got)
			}
		}
	}
}

// The reviewer's case: an engine line of "⚠️ warn " ran a box row to 87
// cells on an 80-column screen.
func TestConsole_EmojiPresentationLineStaysInsideTheBox(t *testing.T) {
	for name, cluster := range review214Clusters {
		c := testConsole().observeRaw(strings.Repeat(cluster+" warn ", 20))
		box, all := wi10BoxRows(c, 80, 26)
		for _, row := range all {
			if w := lipgloss.Width(row); w > 76 {
				t.Errorf("%s: a row is %d cells, past the 76-cell box: %q", name, w, row)
			}
		}
		for _, row := range box {
			if w := lipgloss.Width(row); w != 76 || !strings.HasSuffix(row, "│") {
				t.Errorf("%s: a box row is %d cells or lost its right border: %q", name, w, row)
			}
		}
	}
}

// #157's line, as the console gets it: the cut stays linear in its
// length. The bound is generous; the quadratic cut took minutes.
func TestTruncateStyled_OverlongProgressLineIsCutQuickly(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 4000; i++ {
		b.WriteString(" waiting for postgres (")
		b.WriteString(strings.Repeat("9", 4))
		b.WriteString(")\r")
	}
	line := b.String()
	if len(line) < 100000 {
		t.Fatalf("test line is only %d bytes", len(line))
	}
	start := time.Now()
	for i := 0; i < 10; i++ {
		if got := truncateStyled(line, 72); lipgloss.Width(got) > 72 {
			t.Fatalf("cut is %d cells", lipgloss.Width(got))
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("ten cuts of a %d-byte line took %v", len(line), d)
	}
}

// A line break in the text is kept, and a line that fits is written as
// given, even when another line of the same text has to wrap.
func TestWrapWords_KeepsLineBreaksAndSpacing(t *testing.T) {
	long := "permission denied while trying to connect to the docker API at unix:///var/run/docker.sock"
	got := wrapWords("Error response from daemon:\n"+long+"\n  indented detail", 40)
	if len(got) < 4 || got[0] != "Error response from daemon:" || got[len(got)-1] != "  indented detail" {
		t.Errorf("wrapWords lost the line breaks or the indent: %q", got)
	}
	for _, l := range got {
		if lipgloss.Width(l) > 40 {
			t.Errorf("row %q is %d cells, past 40", l, lipgloss.Width(l))
		}
	}
	// Runs of spaces inside a line that wraps stay as given.
	got = wrapWords("Checking host —  · next: "+strings.Repeat("retry ", 12), 40)
	if len(got) < 2 || !strings.HasPrefix(got[0], "Checking host —  · next:") {
		t.Errorf("wrapWords collapsed the double space: %q", got)
	}
}

// On a screen: Repair's error keeps Docker's own line break.
func TestRepairError_MultiLineDockerErrorKeepsItsBreak(t *testing.T) {
	m := sizedRepair(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: wi10Cols, Height: 40})
	m = updated.(RepairModel)
	m, _ = repairUpdate(t, m, repairReadyMsg{err: errors.New("Error response from daemon:\n" + wi10DockerError)})
	screen := wi10Plain(m.View().Content)
	found := false
	for _, row := range strings.Split(screen, "\n") {
		if strings.TrimSpace(row) == "Error response from daemon:" {
			found = true
		}
	}
	if !found {
		t.Errorf("the first line of the error is not a row of its own:\n%s", wi10Compact(screen))
	}
	wi10AssertWrapped(t, m.View().Content, wi10DockerError)
}
