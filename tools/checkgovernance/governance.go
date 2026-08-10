// Package main implements orbit-launcher's planning-governance check: a
// required Observability-Impact declaration on every pull request, and a
// required Planning-Model attestation on any pull request touching a
// protected planning path. Ported from orbit's
// scripts/check-planning-governance.mjs, in Go rather than Node so this
// repository's CI doesn't need a second language toolchain.
package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Policy is the shape of .github/planning-governance.json.
type Policy struct {
	PlanningAuthorities  []string `json:"planningAuthorities"`
	AcceptedAttestations []string `json:"acceptedAttestations"`
	ProtectedFiles       []string `json:"protectedFiles"`
	ProtectedPrefixes    []string `json:"protectedPrefixes"`
}

const (
	observabilityDeclarationPrefix = "Observability-Impact:"
	observabilityChangedLine       = "Observability-Impact: changed"
)

var (
	observabilityNoneLine     = regexp.MustCompile(`^Observability-Impact: none — (.+)$`)
	unexplainedReasonPattern  = regexp.MustCompile(`(?i)^(?:<[^>]+>|specific reason|tbd|todo|n/?a|none|not applicable|no impact|no operational impact|(?:documentation|docs|test|tests|formatting) only|no runtime changes?|replace(?: this)?(?: with)? .*)\.?$`)
	observabilityEntryPattern = map[string]*regexp.Regexp{
		"Operational event/state":       regexp.MustCompile(`(?i)^(?:[-*]\s*)?Operational event/state\s*:\s*(.*?)\s*$`),
		"Failure/recovery":              regexp.MustCompile(`(?i)^(?:[-*]\s*)?Failure/recovery\s*:\s*(.*?)\s*$`),
		"Privacy/redaction":             regexp.MustCompile(`(?i)^(?:[-*]\s*)?Privacy/redaction\s*:\s*(.*?)\s*$`),
		"Operator-documentation impact": regexp.MustCompile(`(?i)^(?:[-*]\s*)?Operator-documentation impact\s*:\s*(.*?)\s*$`),
	}
	// Deterministic order for stable error messages; map iteration order
	// isn't, so this doesn't rely on it.
	observabilityEntryOrder = []string{
		"Operational event/state",
		"Failure/recovery",
		"Privacy/redaction",
		"Operator-documentation impact",
	}
)

func isUnexplained(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || unexplainedReasonPattern.MatchString(trimmed)
}

func bodyLines(body string) []string {
	raw := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	lines := make([]string, len(raw))
	for i, line := range raw {
		lines[i] = strings.TrimSpace(line)
	}
	return lines
}

// ValidateObservabilityDeclaration checks that the PR body contains exactly
// one Observability-Impact declaration, and that a "changed" declaration
// carries all four required evidence fields with a real (non-placeholder)
// value.
func ValidateObservabilityDeclaration(body string) error {
	lines := bodyLines(body)

	var declarations []string
	for _, line := range lines {
		if strings.HasPrefix(line, observabilityDeclarationPrefix) {
			declarations = append(declarations, line)
		}
	}

	if len(declarations) == 0 {
		return fmt.Errorf("an Observability-Impact declaration is required")
	}
	if len(declarations) != 1 {
		return fmt.Errorf("exactly one Observability-Impact declaration is required")
	}

	declaration := declarations[0]
	if declaration == observabilityChangedLine {
		var missing []string
		for _, label := range observabilityEntryOrder {
			pattern := observabilityEntryPattern[label]
			var matches []string
			for _, line := range lines {
				if pattern.MatchString(line) {
					matches = append(matches, line)
				}
			}
			if len(matches) != 1 {
				missing = append(missing, label)
				continue
			}
			groups := pattern.FindStringSubmatch(matches[0])
			if len(groups) < 2 || isUnexplained(groups[1]) {
				missing = append(missing, label)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("Observability-Impact: changed requires concise entries for: %s", strings.Join(missing, ", "))
		}
		return nil
	}

	match := observabilityNoneLine.FindStringSubmatch(declaration)
	if match == nil || isUnexplained(match[1]) {
		return fmt.Errorf("the Observability-Impact declaration must be exactly changed or none with a specific reason")
	}
	return nil
}

// IsProtectedPlanningPath reports whether path matches the policy's
// protected-file list or protected-prefix list.
func IsProtectedPlanningPath(path string, policy Policy) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	for _, protected := range policy.ProtectedFiles {
		if normalized == protected {
			return true
		}
	}
	for _, prefix := range policy.ProtectedPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

// MatchedPlanningAttestation returns the accepted attestation line present
// in body, or "" if none is present.
func MatchedPlanningAttestation(body string, policy Policy) string {
	lines := bodyLines(body)
	lineSet := make(map[string]bool, len(lines))
	for _, line := range lines {
		lineSet[line] = true
	}
	for _, accepted := range policy.AcceptedAttestations {
		if lineSet[accepted] {
			return accepted
		}
	}
	return ""
}
