// Package linefile reads the hand-kept line files the tools check
// against, .github/coverage-floors.txt and .github/licence-review.txt
// (#199 SU-17): one record per line, blank lines and `#` comments
// skipped. What a record means stays with each tool.
package linefile

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Line is one record: its 1-based line number in the file, its text
// trimmed of surrounding space, and that text's whitespace-separated
// fields.
type Line struct {
	No     int
	Text   string
	Fields []string
}

// Read returns every line of r that is neither blank nor a comment (its
// first non-space character is `#`), in file order.
func Read(r io.Reader) ([]Line, error) {
	var out []Line
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	no := 0
	for sc.Scan() {
		no++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		out = append(out, Line{No: no, Text: text, Fields: strings.Fields(text)})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: %w", no+1, err)
	}
	return out, nil
}
