package engine

import "testing"

func TestParseFinding(t *testing.T) {
	f, ok := ParseFinding("finding class=secret-missing target=session-secret severity=warn")
	if !ok {
		t.Fatal("expected a finding line to parse")
	}
	if f.Class != "secret-missing" || f.Target != "session-secret" || f.Severity != "warn" {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

func TestParseFinding_UnknownClassesCarriedVerbatim(t *testing.T) {
	f, ok := ParseFinding("finding class=database-unreachable target=database severity=fail")
	if !ok {
		t.Fatal("expected a next-slice class to parse — unknown enums are renderable, not rejected")
	}
	if f.Class != "database-unreachable" {
		t.Fatalf("unexpected finding: %+v", f)
	}
}

func TestParseFinding_ToleratesUnknownTrailingFields(t *testing.T) {
	if _, ok := ParseFinding("finding class=secret-missing target=session-secret severity=warn note=extra"); !ok {
		t.Fatal("expected unknown trailing key=value fields to be tolerated")
	}
}

func TestParseFinding_RejectsProseAndIncomplete(t *testing.T) {
	for _, line := range []string{
		"",
		"finding something odd here",
		"finding class=x target=y", // missing severity
		"findings class=x target=y severity=warn",
		"diagnosis result=healthy checked=13 skipped=0",
	} {
		if _, ok := ParseFinding(line); ok {
			t.Errorf("expected %q to be rejected", line)
		}
	}
}

func TestParseDiagnosis(t *testing.T) {
	d, ok := ParseDiagnosis("diagnosis result=attention checked=12 skipped=1")
	if !ok {
		t.Fatal("expected a diagnosis line to parse")
	}
	if d.Result != "attention" || d.Checked != 12 || d.Skipped != 1 {
		t.Fatalf("unexpected diagnosis: %+v", d)
	}
}

func TestParseDiagnosis_MalformedCountsDefaultToZero(t *testing.T) {
	d, ok := ParseDiagnosis("diagnosis result=healthy checked=many skipped=-3")
	if !ok {
		t.Fatal("expected the line to parse")
	}
	if d.Checked != 0 || d.Skipped != 0 {
		t.Fatalf("unexpected diagnosis: %+v", d)
	}
}

func TestParseDiagnosis_RejectsProse(t *testing.T) {
	for _, line := range []string{
		"",
		"diagnosis complete",
		"finding class=x target=y severity=warn",
	} {
		if _, ok := ParseDiagnosis(line); ok {
			t.Errorf("expected %q to be rejected", line)
		}
	}
}

func TestParsePlanAction(t *testing.T) {
	p, ok := ParsePlanAction("plan action=rotate-database-credential resolves=database-credential-mismatch mutation=credential-rotation backup=required")
	if !ok {
		t.Fatal("expected a plan action line to parse")
	}
	if p.Action != "rotate-database-credential" || p.Resolves != "database-credential-mismatch" ||
		p.Mutation != "credential-rotation" || p.Backup != "required" {
		t.Fatalf("unexpected action: %+v", p)
	}
}

func TestParsePlanAction_RejectsSummaryAndProse(t *testing.T) {
	for _, line := range []string{
		"plan result=ready actions=4 manual=2",
		"plan something informal",
		"plan action=manual resolves=x mutation=none", // missing backup
		"manual step: do the thing (resolves=x)",
	} {
		if _, ok := ParsePlanAction(line); ok {
			t.Errorf("expected %q to be rejected", line)
		}
	}
}

func TestParsePlanSummary(t *testing.T) {
	s, ok := ParsePlanSummary("plan result=ready actions=4 manual=2")
	if !ok {
		t.Fatal("expected the summary line to parse")
	}
	if s.Result != "ready" || s.Actions != 4 || s.Manual != 2 {
		t.Fatalf("unexpected summary: %+v", s)
	}
}

func TestParsePlanSummary_RejectsActionLines(t *testing.T) {
	if _, ok := ParsePlanSummary("plan action=manual resolves=x mutation=none backup=not-required"); ok {
		t.Error("expected an action line to be rejected by the summary parser")
	}
}

func TestParsePlan_UnknownEnumsCarriedVerbatim(t *testing.T) {
	p, ok := ParsePlanAction("plan action=defragment-rings resolves=ring-dust mutation=cosmetic backup=not-required")
	if !ok {
		t.Fatal("expected unknown enum values to parse — renderable, not rejected")
	}
	if p.Action != "defragment-rings" || p.Mutation != "cosmetic" {
		t.Fatalf("unexpected action: %+v", p)
	}
}
