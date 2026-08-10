package main

import "testing"

func TestValidateObservabilityDeclaration(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "missing declaration",
			body:    "Just a plain PR body.",
			wantErr: true,
		},
		{
			name: "duplicate declaration",
			body: "Observability-Impact: none — no runtime behaviour\n" +
				"Observability-Impact: none — said twice",
			wantErr: true,
		},
		{
			name:    "none with a specific reason",
			body:    "Observability-Impact: none — documentation only, no runtime behaviour",
			wantErr: false,
		},
		{
			name:    "none with an unexplained placeholder reason",
			body:    "Observability-Impact: none — tbd",
			wantErr: true,
		},
		{
			name:    "changed with no evidence fields",
			body:    "Observability-Impact: changed",
			wantErr: true,
		},
		{
			name: "changed with all four fields",
			body: "Observability-Impact: changed\n" +
				"Operational event/state: new install-progress log line\n" +
				"Failure/recovery: bounded retry then explicit failure state\n" +
				"Privacy/redaction: no secrets in the new log line\n" +
				"Operator-documentation impact: README quickstart updated",
			wantErr: false,
		},
		{
			name: "changed with a placeholder field value",
			body: "Observability-Impact: changed\n" +
				"Operational event/state: n/a\n" +
				"Failure/recovery: bounded retry then explicit failure state\n" +
				"Privacy/redaction: no secrets in the new log line\n" +
				"Operator-documentation impact: README quickstart updated",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateObservabilityDeclaration(tt.body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateObservabilityDeclaration() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func testPolicy() Policy {
	return Policy{
		AcceptedAttestations: []string{"Planning-Model: Human"},
		ProtectedFiles:       []string{"docs/implementation-plan.md"},
		ProtectedPrefixes:    []string{".github/workflows/"},
	}
}

func TestIsProtectedPlanningPath(t *testing.T) {
	policy := testPolicy()

	tests := []struct {
		path string
		want bool
	}{
		{"docs/implementation-plan.md", true},
		{".github/workflows/ci.yml", true},
		{"internal/ui/placeholder.go", false},
		{"docs\\implementation-plan.md", true}, // Windows-style separator normalized
	}

	for _, tt := range tests {
		if got := IsProtectedPlanningPath(tt.path, policy); got != tt.want {
			t.Errorf("IsProtectedPlanningPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchedPlanningAttestation(t *testing.T) {
	policy := testPolicy()

	if got := MatchedPlanningAttestation("no attestation here", policy); got != "" {
		t.Errorf("expected no match, got %q", got)
	}

	body := "Some PR body.\n\nPlanning-Model: Human\n"
	if got := MatchedPlanningAttestation(body, policy); got != "Planning-Model: Human" {
		t.Errorf("expected match, got %q", got)
	}
}
