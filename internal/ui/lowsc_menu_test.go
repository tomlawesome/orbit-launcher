package ui

// v0.4.3-c audit Lows (issue #277), written from the specification alone:
//
//   - H. On the success screen the menu does not jump while the claim
//     link read is pending and after it finds no link.
//   - I. On a stopped Install screen that reads the folder, the menu does
//     not jump between "reading the folder" and an answer with nothing to
//     list.

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// H (#277): the claim read is pending, then the answer is "no link"
// (an empty answer, and an error, which the screen treats as no link):
// the first menu row, and the rest, stay on the same screen rows.
func TestLowsC_H_SuccessMenuDoesNotJumpWhenTheClaimReadFindsNoLink(t *testing.T) {
	answers := map[string]func() (string, error){
		"empty answer": func() (string, error) { return "", nil },
		"read error":   func() (string, error) { return "", errors.New("docker not running") },
	}
	for name, read := range answers {
		for _, size := range []struct{ w, h int }{{80, 26}, {120, 40}} {
			t.Run(name+" "+strconv.Itoa(size.w)+"x"+strconv.Itoa(size.h), func(t *testing.T) {
				m := NewSuccessModel(claimApp, 3*time.Minute, "v9.9.9")
				m.noAnimation = true
				m.readClaim = read
				m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
				before := screen(m)

				// The reader's own command, as the program would run it.
				cmd := m.claimCmd()
				if cmd == nil {
					t.Fatal("no claim read was started")
				}
				m, _ = updateAs(t, m, cmd())
				after := screen(m)

				for _, label := range []string{"Get into Orbit", "Terminal", "Menu"} {
					was, now := rowIndex(before, label), rowIndex(after, label)
					if was < 0 || now < 0 {
						t.Fatalf("%q is missing (row %d before, %d after)\n--- before ---\n%s\n--- after ---\n%s", label, was, now, before, after)
					}
					if now != was {
						t.Errorf("%q moved from row %d to row %d when the read found no link\n--- before ---\n%s\n--- after ---\n%s",
							label, was, now, before, after)
					}
				}
			})
		}
	}
}

// I (#277): the stopped Install screen's menu rows are on the same
// screen rows while "reading the folder" shows and after the read finds
// nothing to list, whatever the height (the screen is centred, so a row
// that goes can move the rest at some heights and not at others).
func TestLowsC_I_StoppedInstallMenuDoesNotJumpWhenTheFolderHasNothingToList(t *testing.T) {
	for height := 24; height <= 40; height++ {
		t.Run(strconv.Itoa(height), func(t *testing.T) {
			dir := folderWith(t)
			seam := gbStAnswers()
			m, _, held := gbStInstallStopped(t, dir, gbStRefusedEngine(), seam, true)
			if held == nil {
				t.Fatalf("the folder was not read by a command:\n%s", screen(m))
			}
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: height})
			first := screen(m)
			if !strings.Contains(first, gbStReadingRow) {
				t.Fatalf("no %q row in the first frame:\n%s", gbStReadingRow, first)
			}
			firstRows := gbStMenuRows(first)
			if len(firstRows) == 0 {
				t.Fatalf("no menu rows in the first frame:\n%s", first)
			}

			m, cmd := updateAs(t, m, held)
			m = drive(t, m, cmd)
			after := screen(m)

			if strings.Contains(after, gbStReadingRow) {
				t.Errorf("%q is still shown after the answer:\n%s", gbStReadingRow, after)
			}
			if got := gbStMenuRows(after); !slices.Equal(got, firstRows) {
				t.Errorf("the menu rows moved: lines %v while reading, %v after the answer\n--- before ---\n%s\n--- after ---\n%s",
					firstRows, got, first, after)
			}
		})
	}
}
