// The Orbit commit the live job installs from (#193). orbit/pin.json names
// an ai/orbit branch and a commit on it; the job reads both with jq, fetches
// install.sh and what verifies the manifest at the commit, and reads the
// manifest from the branch's latest build. scripts/bump-orbit-pin.sh writes
// the file, and this keeps a hand edit to the same shape.
package supplychain

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestOrbitPinNamesABranchAndACommit(t *testing.T) {
	var pin map[string]any
	if err := json.Unmarshal([]byte(readRepoFile(t, "orbit", "pin.json")), &pin); err != nil {
		t.Fatalf("orbit/pin.json is not a JSON object: %v", err)
	}
	var keys []string
	for k := range pin {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "branch,commit" {
		t.Errorf("orbit/pin.json must hold exactly branch and commit, got %v (#193)", keys)
	}
	if b, ok := pin["branch"].(string); !ok || strings.TrimSpace(b) == "" {
		t.Errorf("orbit/pin.json's branch must be a non-empty string, got %v (#193)", pin["branch"])
	}
	if c, ok := pin["commit"].(string); !ok || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(c) {
		t.Errorf("orbit/pin.json's commit must be a 40-hex commit, got %v (#193)", pin["commit"])
	}
}
