package readiness

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Step is one counted line of a release record.
type Step struct {
	N        int
	Status   string
	Evidence string
}

// Record is a parsed docs/releases/vX.Y.Z.md.
type Record struct {
	Version   string
	GuideBlob string
	Steps     []Step   // valid lines only
	Signers   []string // the value of each Signed-off-by: line
	Problems  []string // lines that do not count, each saying why
}

// GuideSection is the heading of docs/releasing.md whose numbered lines are
// the steps a release record lists.
const GuideSection = "## What you do, in short"

var guideStep = regexp.MustCompile(`^(\d+)\. +(.*)$`)

// ParseGuideSteps lists the numbered steps under "## What you do, in short",
// stopping at the next `## ` heading. A guide without the section, or a
// section with no numbered line, is an error.
func ParseGuideSteps(guide string) ([]string, error) {
	in, found := false, false
	var steps []string
	for _, line := range strings.Split(strings.ReplaceAll(guide, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			if strings.TrimSpace(line) == GuideSection {
				in, found = true, true
			}
			continue
		}
		if in {
			if m := guideStep.FindStringSubmatch(line); m != nil {
				steps = append(steps, strings.TrimSpace(m[2]))
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("docs/releasing.md has no %q section", GuideSection)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("docs/releasing.md's %q section has no numbered steps", GuideSection)
	}
	return steps, nil
}

var (
	recordStep  = regexp.MustCompile(`^step (\d+):\s*(.*)$`)
	placeholder = regexp.MustCompile(`(?i)^(<.*>|todo|tbd|tbc|\.\.\.|…|-+|n/a|none|x+)$`)
)

// errNoEvidence is a done/not-needed line with nothing, or a placeholder,
// after the status.
var errNoEvidence = errors.New("needs evidence")

// ParseRecord parses a release record: the version and guide-blob header
// lines, each `step N: done: <evidence> | not-needed: <reason> |
// after-merge` line, and every Signed-off-by: line. A step line with an
// unknown status, or with empty or placeholder evidence, is a problem and is
// not counted.
func ParseRecord(src string) Record {
	var r Record
	for _, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "version:"):
			r.Version = strings.TrimSpace(strings.TrimPrefix(line, "version:"))
		case strings.HasPrefix(line, "guide-blob:"):
			r.GuideBlob = strings.TrimSpace(strings.TrimPrefix(line, "guide-blob:"))
		case strings.HasPrefix(line, "Signed-off-by:"):
			r.Signers = append(r.Signers, strings.TrimSpace(strings.TrimPrefix(line, "Signed-off-by:")))
		default:
			m := recordStep.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			st, err := parseStatus(n, m[2])
			if err != nil {
				r.Problems = append(r.Problems, err.Error())
				continue
			}
			r.Steps = append(r.Steps, st)
		}
	}
	return r
}

func parseStatus(n int, rest string) (Step, error) {
	status, evidence, _ := strings.Cut(rest, ":")
	status, evidence = strings.TrimSpace(status), strings.TrimSpace(evidence)
	switch status {
	case "after-merge":
		return Step{N: n, Status: status}, nil
	case "done", "not-needed":
		if evidence == "" || placeholder.MatchString(evidence) {
			what := "a link or job id"
			if status == "not-needed" {
				what = "the owner's decision"
			}
			return Step{}, fmt.Errorf("step %d: %s %s (%s), not a placeholder", n, status, errNoEvidence, what)
		}
		return Step{N: n, Status: status, Evidence: evidence}, nil
	default:
		return Step{}, fmt.Errorf("step %d: unknown status %q; use done: <link or job id>, not-needed: <owner decision> or after-merge", n, status)
	}
}
