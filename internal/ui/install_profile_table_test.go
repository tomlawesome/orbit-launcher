package ui

// Pins #247: the deployment profile choice is a table.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

type profileRow struct{ name, adds, avail string }

var profileTableRows = []profileRow{
	{"Standard", "mail, documents, calendar", "available"},
	{"Documents", "adds text extraction from attachments", "not yet"},
	{"Agent", "adds a local model for search & suggestions", "not yet"},
	{"Full", "documents extraction and the local model", "not yet"},
}

func profileScreenRows(t *testing.T, w int) [][]rune {
	t.Helper()
	m := c9Size(t, NewInstallModel("/opt/orbit", "v1"), w)
	var rows [][]rune
	for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		rows = append(rows, []rune(l))
	}
	return rows
}

func runeIndex(row []rune, s string) int {
	i := strings.Index(string(row), s)
	if i < 0 {
		return -1
	}
	return len([]rune(string(row)[:i]))
}

func hasAt(row []rune, col int, s string) bool {
	r := []rune(s)
	return col >= 0 && col+len(r) <= len(row) && string(row[col:col+len(r)]) == s
}

func rowOfName(t *testing.T, rows [][]rune, name string) int {
	t.Helper()
	for i, r := range rows {
		if strings.Contains(string(r), name+" ") && !strings.Contains(string(r), "Orbit installs") {
			return i
		}
	}
	t.Fatalf("no row names %q", name)
	return -1
}

func TestProfileTableWide(t *testing.T) {
	for _, w := range []int{80, 120} {
		rows := profileScreenRows(t, w)
		nameCol := (w-67)/2 + 2
		prev := -1
		for i, p := range profileTableRows {
			ri := rowOfName(t, rows, p.name)
			if ri <= prev {
				t.Errorf("w=%d: %s is not below the previous profile", w, p.name)
			}
			prev = ri
			row := rows[ri]
			if !hasAt(row, nameCol, p.name) {
				t.Errorf("w=%d: %s name not at column %d: %q", w, p.name, nameCol, string(row))
			}
			if !hasAt(row, nameCol+11, p.adds) {
				t.Errorf("w=%d: %s adds not at column %d: %q", w, p.name, nameCol+11, string(row))
			}
			if !hasAt(row, nameCol+56, p.avail) {
				t.Errorf("w=%d: %s availability not at column %d: %q", w, p.name, nameCol+56, string(row))
			}
			caret := runeIndex(row, "▸")
			if i == 0 && caret != nameCol-2 {
				t.Errorf("w=%d: caret at %d, want %d: %q", w, caret, nameCol-2, string(row))
			}
			if i != 0 && caret >= 0 {
				t.Errorf("w=%d: caret on %s row", w, p.name)
			}
		}
		for _, r := range rows {
			if strings.Contains(string(r), "only Standard is available") {
				t.Errorf("w=%d: footer still present", w)
			}
		}
	}
}

func TestProfileTableNoAIWord(t *testing.T) {
	re := regexp.MustCompile(`\bAI\b`)
	for _, w := range []int{50, 80, 120} {
		for _, r := range profileScreenRows(t, w) {
			if re.MatchString(string(r)) {
				t.Errorf("w=%d: word AI on the screen: %q", w, string(r))
			}
		}
	}
}

func TestProfileTableNarrow(t *testing.T) {
	rows := profileScreenRows(t, 50)
	for _, r := range rows {
		if len(r) > 50 {
			t.Errorf("row wider than 50: %q", string(r))
		}
		if strings.Contains(string(r), "only Standard is available") {
			t.Errorf("footer still present")
		}
	}
	prev := -1
	for i, p := range profileTableRows {
		head := p.name + " · " + p.avail
		ri := -1
		for j, r := range rows {
			if strings.Contains(string(r), head) {
				ri = j
			}
		}
		if ri < 0 {
			t.Fatalf("no row %q", head)
		}
		if ri <= prev {
			t.Errorf("%s out of order", p.name)
		}
		nameCol := runeIndex(rows[ri], p.name)
		if c := runeIndex(rows[ri], "▸"); (i == 0) != (c >= 0) || (i == 0 && c != nameCol-2) {
			t.Errorf("%s: caret at %d, name at %d", p.name, c, nameCol)
		}
		// the adds text follows on the next rows (it may wrap, never cut),
		// starting at the name's column, up to a blank row.
		var parts []string
		j := ri + 1
		for ; j < len(rows) && strings.TrimSpace(string(rows[j])) != ""; j++ {
			parts = append(parts, strings.TrimSpace(string(rows[j])))
		}
		if j == ri+1 || !hasAt(rows[ri+1], nameCol, strings.Fields(p.adds)[0]) {
			t.Errorf("%s: adds text not on the next row at column %d: %q", p.name, nameCol, string(rows[ri+1]))
		}
		if got := strings.Join(parts, " "); got != p.adds {
			t.Errorf("%s: adds = %q, want %q", p.name, got, p.adds)
		}
		prev = j
	}
}

func TestProfileUnavailableNamesNoAIAndFull(t *testing.T) {
	m := c9Size(t, NewInstallModel("/opt/orbit", "v1"), 80)
	m.state = installStateUnavailableProfile
	out := ansi.Strip(m.View().Content)
	if strings.Contains(out, "AI and Full") {
		t.Errorf("unavailable screen still says \"AI and Full\":\n%s", out)
	}
	if !strings.Contains(out, "isn't available yet") {
		t.Errorf("unavailable screen not shown:\n%s", out)
	}
}
