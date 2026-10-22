package ui

// The shell-command wrapper's cut (cutUnit) falls only between whole
// escapes, as wrapHard's does: a folder shown escaped in a command box
// never reads \x at the end of one row and 1b at the start of the next.

import (
	"strings"
	"testing"
)

func TestCutUnit_NeverCutsInsideAnEscape(t *testing.T) {
	escapes := map[string]string{"\x1b": `\x1b`, "\r": `\r`, "\u202e": `\u202e`, `\`: `\\`}
	for _, lead := range []string{"", "--project-directory "} {
		for raw, shown := range escapes {
			for k := 20; k <= 60; k++ {
				content := "/opt/" + strings.Repeat("a", k) + shown + strings.Repeat("z", 80)
				unit := lead + visibleText("'/opt/"+strings.Repeat("a", k)+raw+strings.Repeat("z", 80)+"'")
				for _, limit := range []int{42, 50, 64} {
					rows, last := cutUnit(unit, limit)
					var frags []string
					for _, r := range append(rows, last) {
						r = strings.TrimSuffix(r, `\`)
						r = strings.TrimPrefix(strings.TrimLeft(r, " "), lead)
						frags = append(frags, strings.TrimSuffix(strings.TrimPrefix(r, "'"), "'"))
					}
					if got := strings.Join(frags, ""); got != content {
						t.Fatalf("%q after %d at %d: reads back %q, want %q", shown, k, limit, got, content)
					}
					start := len("/opt/") + k
					at := 0
					for i, f := range frags[:len(frags)-1] {
						at += len(f)
						if at > start && at < start+len(shown) {
							t.Errorf("%q after %d at %d: row %d ends inside the escape (%q, next %q)", shown, k, limit, i, f, frags[i+1])
						}
					}
				}
			}
		}
	}
}
