package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func loadPolicy(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read policy: %w", err)
	}
	var policy Policy
	if err := json.Unmarshal(data, &policy); err != nil {
		return Policy{}, fmt.Errorf("parse policy: %w", err)
	}
	return policy, nil
}

func changedFiles(base, head string) ([]string, error) {
	if base == "" || head == "" {
		return nil, fmt.Errorf("ORBIT_LAUNCHER_BASE_SHA and ORBIT_LAUNCHER_HEAD_SHA are required")
	}
	out, err := exec.Command("git", "diff", "--name-only", base+"..."+head).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}

func main() {
	prBody, hasPRBody := os.LookupEnv("ORBIT_LAUNCHER_PR_BODY")
	if hasPRBody {
		if err := ValidateObservabilityDeclaration(prBody); err != nil {
			fmt.Fprintln(os.Stderr, "Observability governance:", err)
			os.Exit(1)
		}
		fmt.Println("Observability governance: accepted exactly one proportional declaration.")
	}

	policy, err := loadPolicy(".github/planning-governance.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Planning governance:", err)
		os.Exit(1)
	}

	files, err := changedFiles(os.Getenv("ORBIT_LAUNCHER_BASE_SHA"), os.Getenv("ORBIT_LAUNCHER_HEAD_SHA"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Planning governance:", err)
		os.Exit(1)
	}

	var protectedChanges []string
	for _, f := range files {
		if IsProtectedPlanningPath(f, policy) {
			protectedChanges = append(protectedChanges, f)
		}
	}

	if len(protectedChanges) == 0 {
		fmt.Println("Planning governance: no protected planning files changed.")
		return
	}

	attestation := MatchedPlanningAttestation(prBody, policy)
	if attestation == "" {
		fmt.Fprintln(os.Stderr, "Planning governance: protected planning files changed:")
		for _, f := range protectedChanges {
			fmt.Fprintln(os.Stderr, "-", f)
		}
		fmt.Fprintln(os.Stderr, "Add exactly one of these PR-body attestation lines:")
		for _, accepted := range policy.AcceptedAttestations {
			fmt.Fprintln(os.Stderr, "-", accepted)
		}
		os.Exit(1)
	}

	fmt.Printf("Planning governance: accepted %q for %d protected file(s).\n", attestation, len(protectedChanges))
}
