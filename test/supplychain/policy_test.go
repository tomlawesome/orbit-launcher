// Package supplychain is the CI gate over .github/supply-chain-policy.json.
//
// The rules themselves live in the policy package, next to the generator that
// writes the file and shared with the `supplychainpolicy` command, so a person
// running that command by hand and CI running this test cannot reach different
// conclusions. Each rule is proved to fire by unit tests in that package
// against constructed inputs; this test is the one that runs against the real
// repository.
package supplychain

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/supplychainpolicy/policy"
)

const root = "../.."

const regenerateAdvice = "Regenerate the policy: go run ./tools/supplychainpolicy -write"

// policyAdvice is what to do about a policy that does not match the pins.
// Usually that is to regenerate it. But when two workflows pin one action at
// different commits no policy can match both, and the generator keeps only
// the last pin, so regenerating would hide the other: then the workflows
// must agree first, and the advice names each disagreeing pair.
func policyAdvice(pins []policy.Pin) string {
	first := map[string]policy.Pin{}
	var conflicts []string
	for _, p := range pins {
		if !policy.IsSHA(p.Ref) {
			continue
		}
		f, seen := first[p.Action]
		switch {
		case !seen:
			first[p.Action] = p
		case f.Ref != p.Ref:
			conflicts = append(conflicts, fmt.Sprintf("%s is pinned to %s at %s:%d but to %s at %s:%d",
				p.Action, f.Ref, f.File, f.Line, p.Ref, p.File, p.Line))
		}
	}
	if len(conflicts) == 0 {
		return regenerateAdvice
	}
	return "Two workflows pin one action at different commits, which no policy can match:\n  " +
		strings.Join(conflicts, "\n  ") +
		"\nMake the workflows pin each action to one commit, then run this check again."
}

func TestPolicyMatchesTheWorkflows(t *testing.T) {
	problems, err := policy.Verify(root)
	if err != nil {
		t.Fatalf("cannot check the supply-chain policy: %v", err)
	}
	if len(problems) == 0 {
		return
	}
	pins, err := policy.CollectPins(root)
	if err != nil {
		t.Fatalf("collecting pins: %v", err)
	}
	advice := policyAdvice(pins)
	for _, p := range problems {
		t.Errorf("%s\n%s", p, advice)
	}
}

// Two workflows pinning one action at different commits cannot be fixed by
// regenerating: the generator keeps only the last pin, which hides the other
// (#195 X-Q5). The advice must name both pins and not say to regenerate.
func TestPolicyAdviceForConflictingPinsIsNotRegenerate(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	pins := []policy.Pin{
		{Action: "actions/checkout", Ref: a, File: "ci.yml", Line: 12},
		{Action: "actions/setup-go", Ref: b, File: "ci.yml", Line: 16},
		{Action: "actions/checkout", Ref: b, File: "release.yml", Line: 9},
	}
	advice := policyAdvice(pins)
	if strings.Contains(strings.ToLower(advice), "regenerat") || strings.Contains(advice, "-write") {
		t.Errorf("advice for conflicting pins says to regenerate, which would hide one of them:\n%s", advice)
	}
	for _, want := range []string{"actions/checkout", "ci.yml:12", "release.yml:9", a, b} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice does not name %q:\n%s", want, advice)
		}
	}
	// Without a conflict the advice stays: regenerate.
	if got := policyAdvice(pins[:2]); got != regenerateAdvice {
		t.Errorf("advice without a conflict is %q, want %q", got, regenerateAdvice)
	}
}

// TestGeneratedFieldsAreUpToDate is the "generated file is current" gate: the
// mechanical fields must be exactly what the generator would derive today, in
// the same order. TestPolicyMatchesTheWorkflows catches a policy that
// contradicts a workflow; this catches one that has merely drifted in shape.
func TestGeneratedFieldsAreUpToDate(t *testing.T) {
	pins, err := policy.CollectPins(root)
	if err != nil {
		t.Fatalf("collecting pins: %v", err)
	}
	pol, err := policy.Load(root)
	if err != nil {
		t.Fatalf("loading the policy: %v", err)
	}

	// Excepted actions are deliberately not recorded; see WithoutExceptions.
	want := policy.WithoutExceptions(policy.DerivedActions(pins), pol.Exceptions)
	if len(pol.Actions) != len(want) {
		t.Fatalf("the policy records %d actions, the workflows pin %d.\n%s",
			len(pol.Actions), len(want), policyAdvice(pins))
	}
	for i, w := range want {
		got := pol.Actions[i]
		if got.Name != w.Name || got.Commit != w.Commit || got.Version != w.Version {
			t.Errorf("policy entry %d records %s %s %s; the workflows say %s %s %s.\n%s",
				i, got.Name, got.Version, got.Commit, w.Name, w.Version, w.Commit, policyAdvice(pins))
		}
	}
}
