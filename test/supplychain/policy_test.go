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
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/supplychainpolicy/policy"
)

const root = "../.."

func TestPolicyMatchesTheWorkflows(t *testing.T) {
	problems, err := policy.Verify(root)
	if err != nil {
		t.Fatalf("cannot check the supply-chain policy: %v", err)
	}
	for _, p := range problems {
		t.Errorf("%s\nRegenerate the policy: go run ./tools/supplychainpolicy -write", p)
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

	want := policy.DerivedActions(pins)
	if len(pol.Actions) != len(want) {
		t.Fatalf("the policy records %d actions, the workflows pin %d.\n"+
			"Regenerate the policy: go run ./tools/supplychainpolicy -write",
			len(pol.Actions), len(want))
	}
	for i, w := range want {
		got := pol.Actions[i]
		if got.Name != w.Name || got.Commit != w.Commit || got.Version != w.Version {
			t.Errorf("policy entry %d records %s %s %s; the workflows say %s %s %s.\n"+
				"Regenerate the policy: go run ./tools/supplychainpolicy -write",
				i, got.Name, got.Version, got.Commit, w.Name, w.Version, w.Commit)
		}
	}
}
