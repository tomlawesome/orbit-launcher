package ui

// Pins #277 L1 (ratified, answer 4a): Repair's diagnosis screen on a
// short terminal. The services block gives way first, in steps:
//
//	full list -> running rows folded ("✓ r running" + each faulted row)
//	          -> one muted row "services · r of n running" -> gone
//
// and only then do the findings and the plan's steps shorten. Header,
// the counts line, the plan summary and every menu row never give way,
// and the screen's last row is left to the sky.
//
// C is the number of rows of everything but the block at full length; n
// is the number of services, f the faulted ones, r the running ones. The
// tests measure C from the screen with the full list (its content is
// C + 1 + n rows) rather than hard-coding it, then check each height's
// form against the rule table:
//
//	h >= C+1+(1+n)                 full list
//	C+1+(2+f) <= h < C+1+(1+n)     folded, when r > 0
//	C+2 <= h (below that)          one row "services · r of n running"
//	h <= C+1                       no block
//
// The fixture's "full" profile expects five services, so n = 5 here (the
// spec's drawn example shows six; the rules are in terms of n).

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// groupBCore is the spec's core height: header, blank, counts, plan line,
// blank, a three-row menu, the sky's row and one "more" row each for
// findings and steps: 13 rows at 80 columns. At 50 columns each "and N
// more — make the terminal taller to see them" row wraps to two rows, so
// the core there is 15.
func groupBCore(w int) int {
	if w < 60 {
		return 15
	}
	return 13
}

func groupBHave() []deploy.ServiceState {
	// r = 4 running, f = 1 faulted, n = 5.
	return append(up("orbit-db", "orbit-app", "orbit-clamav", "orbit-ollama"),
		deploy.ServiceState{Service: "orbit-tika", State: "restarting"})
}

// groupBLastLetterRow is the 0-based index of the last screen row that
// carries words; the sky's glyphs are not letters.
func groupBLastLetterRow(s string) int {
	last := -1
	for i, row := range strings.Split(s, "\n") {
		if strings.IndexFunc(row, func(r rune) bool { return r >= 'A' && r <= 'z' }) >= 0 {
			last = i
		}
	}
	return last
}

var groupBOneRow = regexp.MustCompile(`services · (\d+) of (\d+) running`)

func TestGroupB_L1_ServicesBlockGivesWayInStepsOnAShortTerminal(t *testing.T) {
	const n, r, f = 5, 4, 1
	for _, w := range []int{50, 80} {
		tall := servicesModel(t, "full", w, 80, groupBHave(), nil)
		if got := len(svcRows(t, tall)); got != n {
			t.Fatalf("setup: width %d: the tall screen shows %d service rows, want the full %d:\n%s", w, got, n, screen(tall))
		}
		hFull := contentRows(screen(tall)) // C + 1 + n
		c := hFull - (1 + n)
		wantLabels := screenLabels(screen(tall))
		sweepTop := c + 2 + n
		for h := groupBCore(w); h <= sweepTop; h++ {
			h := h
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				m := servicesModel(t, "full", w, h, groupBHave(), nil)
				s := screen(m)
				p := plain(m)
				rows := svcRows(t, m)
				oneRow := groupBOneRow.FindStringSubmatch(s)

				switch {
				case h >= c+1+(1+n):
					if len(rows) != n || foldedRow.MatchString(p) || oneRow != nil {
						t.Errorf("want the full list (%d rows), got rows=%d folded=%v oneRow=%v:\n%s", n, len(rows), foldedRow.MatchString(p), oneRow != nil, s)
					}
				case h >= c+1+(2+f):
					if !strings.Contains(p, fmt.Sprintf("✓ %d running", r)) {
						t.Errorf("want the folded form `✓ %d running`:\n%s", r, s)
					}
					if len(rows) != f || rows[0] != (svcRow{"!", "orbit-tika", "restarting"}) {
						t.Errorf("want only the faulted row, got %v:\n%s", rows, s)
					}
					if oneRow != nil {
						t.Errorf("the one-row form shows where the folded form fits:\n%s", s)
					}
				case h >= c+2:
					if oneRow == nil || oneRow[1] != fmt.Sprint(r) || oneRow[2] != fmt.Sprint(n) {
						t.Errorf("want one row `services · %d of %d running`:\n%s", r, n, s)
					}
					if len(rows) != 0 || foldedRow.MatchString(p) {
						t.Errorf("per-service or folded rows show beside the one-row form (rows=%v):\n%s", rows, s)
					}
					if got := strings.Count(s, "services"); got != 1 {
						t.Errorf("the block is %d rows with the word services, want exactly 1:\n%s", got, s)
					}
				default: // h <= c+1: no block
					if strings.Contains(s, "services") || len(rows) != 0 || foldedRow.MatchString(p) {
						t.Errorf("no room for the block, yet it is drawn:\n%s", s)
					}
				}

				// At every height from the core up: the counts line, every
				// menu row, the screen within h rows, the last row the sky's.
				if !strings.Contains(s, "12 checked") || !strings.Contains(s, "1 skipped") {
					t.Errorf("the counts line is missing:\n%s", s)
				}
				if !strings.Contains(s, "Run the safe repairs") {
					t.Errorf("the first menu row is missing:\n%s", s)
				}
				if got := screenLabels(s); fmt.Sprint(got) != fmt.Sprint(wantLabels) {
					t.Errorf("menu rows = %v, want %v:\n%s", got, wantLabels, s)
				}
				if nrows := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); nrows > h {
					t.Errorf("the screen is %d rows, over the %d available:\n%s", nrows, h, s)
				}
				if last := groupBLastLetterRow(s); last > h-2 {
					t.Errorf("words on row %d (0-based) of %d: the last row belongs to the sky:\n%s", last, h, s)
				}
			})
		}
	}
}

// With nothing running (r = 0) and the list not fitting, the one-row
// form says so: `services · 0 of n running`, never the folded form.
func TestGroupB_L1_NothingRunningUsesTheOneRowForm(t *testing.T) {
	const n = 5
	for _, w := range []int{50, 80} {
		tall := servicesModel(t, "full", w, 80, nil, nil)
		hFull := contentRows(screen(tall))
		c := hFull - (1 + n)
		for h := c + 2; h <= hFull; h++ {
			h := h
			if h < groupBCore(w) {
				continue
			}
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				m := servicesModel(t, "full", w, h, nil, nil)
				s := screen(m)
				if !strings.Contains(s, "services · 0 of 5 running") {
					t.Errorf("want `services · 0 of 5 running`:\n%s", s)
				}
				if rows := svcRows(t, m); len(rows) != 0 {
					t.Errorf("per-service rows show beside the one-row form: %v\n%s", rows, s)
				}
			})
		}
	}
}

// Findings and steps shorten only after the block is gone: while the
// block still has a row (h >= C+2), every finding is on screen and no
// "and N more" row is drawn.
func TestGroupB_L1_FindingsAndStepsShortenOnlyAfterTheBlockIsGone(t *testing.T) {
	const n = 5
	for _, w := range []int{50, 80} {
		tall := servicesModel(t, "full", w, 80, groupBHave(), nil)
		c := contentRows(screen(tall)) - (1 + n)
		for h := c + 2; h <= c+2+n; h++ {
			h := h
			if h < groupBCore(w) {
				continue
			}
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				m := servicesModel(t, "full", w, h, groupBHave(), nil)
				s := screen(m)
				if strings.Contains(s, "and ") && strings.Contains(s, "more — ") {
					t.Errorf("a findings or steps row was shortened while the block still has a row:\n%s", s)
				}
				for _, want := range []string{"absent or empty", "secret-stale"} {
					if !strings.Contains(s, want) {
						t.Errorf("a finding (%q) was dropped while the block still has a row:\n%s", want, s)
					}
				}
			})
		}
	}
}
