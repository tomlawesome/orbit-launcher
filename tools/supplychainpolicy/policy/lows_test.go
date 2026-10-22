package policy

import (
	"strings"
	"testing"
)

const otherCheckoutSHA = "4444444444444444444444444444444444444444"

func problemText(problems []Problem) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString(p.String())
		b.WriteString("\n")
	}
	return b.String()
}

// X-Q5 (#195): two workflows pinning one action at different commits is a
// fault in the workflows, not a stale policy. No policy can match both, so
// "regenerate the policy" cannot clear it and, because the last pin wins when
// the policy is derived, regenerating would hide one of the two pins. The
// report must name both places and must not send the maintainer to
// regenerate.
func TestVerifyPinsReportsTwoWorkflowsPinningOneActionAtDifferentCommits(t *testing.T) {
	for _, c := range []struct {
		name   string
		policy func(Policy) Policy
	}{
		{"policy records the first pin", func(p Policy) Policy { return p }},
		{"policy records the second pin", func(p Policy) Policy { p.Actions[0].Commit = otherCheckoutSHA; return p }},
	} {
		t.Run(c.name, func(t *testing.T) {
			pins := append(goodPins(),
				Pin{Action: "actions/checkout", Full: "actions/checkout", Ref: otherCheckoutSHA, Comment: "v7.0.1", File: "release.yml", Line: 22})

			problems := VerifyPins(pins, c.policy(goodPolicy()))

			text := problemText(problems)
			var conflict string
			for _, p := range problems {
				s := p.String()
				if strings.Contains(s, "ci.yml") && strings.Contains(s, "release.yml") {
					conflict = s
				}
			}
			if conflict == "" {
				t.Fatalf("no problem names both ci.yml and release.yml, the two places pinning actions/checkout at different commits:\n%s", text)
			}
			if !strings.Contains(conflict, "actions/checkout") {
				t.Errorf("the conflict %q does not name the action", conflict)
			}
			if !strings.Contains(conflict, checkoutSHA[:7]) || !strings.Contains(conflict, otherCheckoutSHA[:7]) {
				t.Errorf("the conflict %q does not show both commits", conflict)
			}
			for _, p := range problems {
				s := strings.ToLower(p.String())
				if strings.Contains(s, "actions/checkout") && strings.Contains(s, "regenerat") {
					t.Errorf("a problem about actions/checkout tells the maintainer to regenerate, which cannot fix a conflict between workflows: %q", p.String())
				}
			}
		})
	}
}

// Same commit in two workflows is the ordinary case and stays quiet; so does
// the same action at two subpaths of one repository and commit.
func TestVerifyPinsAcceptsOneActionPinnedTwiceAtTheSameCommit(t *testing.T) {
	pins := append(goodPins(),
		Pin{Action: "actions/checkout", Full: "actions/checkout", Ref: checkoutSHA, Comment: "v7.0.1", File: "release.yml", Line: 22})
	if problems := VerifyPins(pins, goodPolicy()); len(problems) != 0 {
		t.Fatalf("the same commit in two workflows reported: %s", problemText(problems))
	}
}
