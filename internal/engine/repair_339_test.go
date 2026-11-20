package engine

import "testing"

// #339: the parser accepts the retire-smtp-lines action as it does any
// other, and still rejects a plan line missing a required field.
func TestRepair339_ParsePlanActionRetireSMTPLines(t *testing.T) {
	p, ok := ParsePlanAction("plan action=retire-smtp-lines resolves=smtp-lines-retirable mutation=reversible backup=required target=env-file")
	if !ok {
		t.Fatal("ParsePlanAction rejected a retire-smtp-lines plan line")
	}
	if p.Action != "retire-smtp-lines" || p.Resolves != "smtp-lines-retirable" || p.Mutation != "reversible" || p.Backup != "required" {
		t.Errorf("parsed %+v", p)
	}
	if _, ok := ParsePlanAction("plan action=retire-smtp-lines resolves=smtp-lines-retirable backup=required target=env-file"); ok {
		t.Error("a plan line with no mutation= was accepted")
	}
}

func TestRepair339_ParseExecuteResultRetireSMTPLines(t *testing.T) {
	e, ok := ParseExecuteResult("execute action=retire-smtp-lines resolves=smtp-lines-retirable result=applied target=env-file")
	if !ok {
		t.Fatal("ParseExecuteResult rejected a retire-smtp-lines execute line")
	}
	if e.Action != "retire-smtp-lines" || e.Resolves != "smtp-lines-retirable" || e.Result != "applied" {
		t.Errorf("parsed %+v", e)
	}
}
