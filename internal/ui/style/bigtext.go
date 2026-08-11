package style

import "strings"

// The big wordmark is a half-block pixel font: each letter is designed on
// a 5×6 pixel grid and rendered two pixel rows per terminal row using
// ▀ ▄ █, the standard TUI big-text technique — a genuinely larger ORBIT
// without depending on the terminal's font size
// (design/mockups-v5.html section 01).
var pixFont = map[rune][]string{
	'O': {
		".###.",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		".###.",
	},
	'R': {
		"####.",
		"#...#",
		"####.",
		"#.#..",
		"#..#.",
		"#...#",
	},
	'B': {
		"####.",
		"#...#",
		"####.",
		"#...#",
		"#...#",
		"####.",
	},
	'I': {
		"###",
		".#.",
		".#.",
		".#.",
		".#.",
		"###",
	},
	'T': {
		"#####",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
		"..#..",
	},
}

// BigText renders word in the half-block pixel font as three equal-width
// unstyled rows. Letters without a glyph in the font are skipped — the
// font deliberately covers only what the wordmark needs.
func BigText(word string) []string {
	rows := [3]strings.Builder{}
	for _, ch := range word {
		glyph, ok := pixFont[ch]
		if !ok {
			continue
		}
		for r := 0; r < 3; r++ {
			for c := 0; c < len(glyph[0]); c++ {
				topOn := glyph[r*2][c] == '#'
				botOn := glyph[r*2+1][c] == '#'
				switch {
				case topOn && botOn:
					rows[r].WriteRune('█')
				case topOn:
					rows[r].WriteRune('▀')
				case botOn:
					rows[r].WriteRune('▄')
				default:
					rows[r].WriteRune(' ')
				}
			}
			rows[r].WriteRune(' ') // one-cell letter gap
		}
	}
	return []string{
		strings.TrimRight(rows[0].String(), " "),
		strings.TrimRight(rows[1].String(), " "),
		strings.TrimRight(rows[2].String(), " "),
	}
}
