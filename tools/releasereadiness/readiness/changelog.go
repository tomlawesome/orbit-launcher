package readiness

import (
	"fmt"
	"regexp"
	"strings"
)

// The six change types Keep a Changelog 1.1.0 names. An entry counts only
// under one of these.
var changeTypes = map[string]bool{
	"Added": true, "Changed": true, "Deprecated": true,
	"Removed": true, "Fixed": true, "Security": true,
}

var (
	releaseHeading    = regexp.MustCompile(`^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})$`)
	unreleasedHeading = regexp.MustCompile(`^## \[?Unreleased\]?\s*$`)
)

// section is one `## ` heading of a changelog and the lines under it.
type section struct {
	heading string
	lines   []string
}

func splitSections(src string) []section {
	var out []section
	cur := -1
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			out = append(out, section{heading: strings.TrimRight(line, " \t")})
			cur = len(out) - 1
			continue
		}
		if cur >= 0 {
			out[cur].lines = append(out[cur].lines, line)
		}
	}
	return out
}

// entries counts the list items under a Keep a Changelog change-type heading.
func entries(lines []string) int {
	n := 0
	counting := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "### ") {
			counting = changeTypes[strings.TrimSpace(strings.TrimPrefix(t, "### "))]
			continue
		}
		if counting && (strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")) && len(strings.TrimSpace(t[2:])) > 0 {
			n++
		}
	}
	return n
}

// anyListItem reports whether a section holds any list item at all, under
// any heading: an Unreleased section with one still holds entries.
func anyListItem(lines []string) bool {
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if (strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")) && len(strings.TrimSpace(t[2:])) > 0 {
			return true
		}
	}
	return false
}

// CheckChangelog reads a CHANGELOG.md: the version of its newest release
// heading ("" when there is none) and every problem found. The newest
// release is the first `## ` heading that is not Unreleased; it must read
// exactly `## [X.Y.Z] - YYYY-MM-DD` and hold at least one entry under a Keep
// a Changelog heading, and no Unreleased section may hold entries.
func CheckChangelog(src string) (version string, problems []string) {
	sawRelease := false
	for _, s := range splitSections(src) {
		if unreleasedHeading.MatchString(s.heading) {
			if anyListItem(s.lines) {
				problems = append(problems, "Unreleased still holds entries: move them into the release section (or out of this release) on dev")
			}
			continue
		}
		if sawRelease {
			continue // older releases are not checked
		}
		sawRelease = true
		m := releaseHeading.FindStringSubmatch(s.heading)
		if m == nil {
			problems = append(problems, fmt.Sprintf("the newest release heading %q is not of the shape `## [X.Y.Z] - YYYY-MM-DD`", s.heading))
			continue
		}
		version = m[1]
		if entries(s.lines) == 0 {
			problems = append(problems, fmt.Sprintf("the %s section is empty: it needs at least one entry under ### Added, Changed, Deprecated, Removed, Fixed or Security", version))
		}
	}
	if !sawRelease {
		problems = append(problems, "CHANGELOG.md has no release heading: rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`")
	}
	if version != "" {
		for i, p := range problems {
			problems[i] = strings.Replace(p, "into the release section", "into the "+version+" section", 1)
		}
	}
	return version, problems
}
