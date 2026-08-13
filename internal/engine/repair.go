package engine

import (
	"strconv"
	"strings"
)

// Repair diagnosis contract — orbit scripts/repair.sh --check (issue
// orbit#261, first slice). One `finding` line per finding, then exactly
// one terminal `diagnosis` line. Enums only: stdout never carries a
// path, a configured value, or a secret. Exit codes: 0 healthy,
// 3 attention, 4 failed, 2 usage, 5 not-an-orbit-installation.

// Finding is one diagnosis finding.
type Finding struct {
	Class    string // reason class, e.g. secret-missing — unknown carried verbatim
	Target   string // target class, e.g. compose-file
	Severity string // info | warn | fail
}

// Diagnosis is the terminal summary line.
type Diagnosis struct {
	Result  string // healthy | attention | failed
	Checked int
	Skipped int
}

// ParseFinding parses one stdout line as a repair finding. ok is false
// for anything that isn't one.
func ParseFinding(line string) (f Finding, ok bool) {
	fields, ok := repairFields(line, "finding")
	if !ok {
		return Finding{}, false
	}
	f = Finding{Class: fields["class"], Target: fields["target"], Severity: fields["severity"]}
	if f.Class == "" || f.Target == "" || f.Severity == "" {
		return Finding{}, false
	}
	return f, true
}

// ParseDiagnosis parses the terminal diagnosis summary line. ok is
// false for anything that isn't one.
func ParseDiagnosis(line string) (d Diagnosis, ok bool) {
	fields, ok := repairFields(line, "diagnosis")
	if !ok {
		return Diagnosis{}, false
	}
	if fields["result"] == "" {
		return Diagnosis{}, false
	}
	checked, err := strconv.Atoi(fields["checked"])
	if err != nil || checked < 0 {
		checked = 0
	}
	skipped, err := strconv.Atoi(fields["skipped"])
	if err != nil || skipped < 0 {
		skipped = 0
	}
	return Diagnosis{Result: fields["result"], Checked: checked, Skipped: skipped}, true
}

// PlanAction is one proposed, classified repair action from
// `repair.sh --plan` (orbit#261 slice 3) — a proposal only; nothing
// executes until orbit's executor slice exists.
type PlanAction struct {
	Action   string // action class, e.g. fix-permissions — unknown carried verbatim
	Resolves string // the reason class this action addresses
	Mutation string // none | reversible | credential-rotation | service-restart
	Backup   string // required | not-required
}

// PlanSummary is the plan's terminal line.
type PlanSummary struct {
	Result  string // empty | ready | manual-required
	Actions int
	Manual  int
}

// ParsePlanAction parses one `plan action=…` line. ok is false for
// anything else, including the plan summary line.
func ParsePlanAction(line string) (p PlanAction, ok bool) {
	fields, ok := repairFields(line, "plan")
	if !ok || fields["action"] == "" {
		return PlanAction{}, false
	}
	p = PlanAction{
		Action:   fields["action"],
		Resolves: fields["resolves"],
		Mutation: fields["mutation"],
		Backup:   fields["backup"],
	}
	if p.Resolves == "" || p.Mutation == "" || p.Backup == "" {
		return PlanAction{}, false
	}
	return p, true
}

// ParsePlanSummary parses the `plan result=…` terminal line. ok is
// false for anything else, including plan action lines.
func ParsePlanSummary(line string) (s PlanSummary, ok bool) {
	fields, ok := repairFields(line, "plan")
	if !ok || fields["result"] == "" || fields["action"] != "" {
		return PlanSummary{}, false
	}
	actions, err := strconv.Atoi(fields["actions"])
	if err != nil || actions < 0 {
		actions = 0
	}
	manual, err := strconv.Atoi(fields["manual"])
	if err != nil || manual < 0 {
		manual = 0
	}
	return PlanSummary{Result: fields["result"], Actions: actions, Manual: manual}, true
}

// repairFields tokenizes a "<lead> key=value ..." line, tolerating
// unknown keys and rejecting prose (any bare word after the lead).
func repairFields(line, lead string) (map[string]string, bool) {
	tokens := strings.Fields(line)
	if len(tokens) < 2 || tokens[0] != lead {
		return nil, false
	}
	fields := map[string]string{}
	for _, token := range tokens[1:] {
		key, value, found := strings.Cut(token, "=")
		if !found || key == "" {
			return nil, false
		}
		fields[key] = value
	}
	return fields, true
}

// ExecuteResult is one `execute action=…` line from repair.sh
// --execute (orbit#261 slice 4): what happened to one planned action.
type ExecuteResult struct {
	Action   string // action class — unknown carried verbatim
	Resolves string // the reason class it addressed
	Result   string // done | failed | skipped
}

// ExecutionSummary is the `execution result=…` terminal line.
type ExecutionSummary struct {
	Result string // empty | complete | unactionable | declined | failed
	Done   int
	Failed int
}

// ParseExecuteResult parses one `execute …` line. ok is false for
// anything else.
func ParseExecuteResult(line string) (e ExecuteResult, ok bool) {
	fields, ok := repairFields(line, "execute")
	if !ok {
		return ExecuteResult{}, false
	}
	e = ExecuteResult{Action: fields["action"], Resolves: fields["resolves"], Result: fields["result"]}
	if e.Action == "" || e.Resolves == "" || e.Result == "" {
		return ExecuteResult{}, false
	}
	return e, true
}

// ParseExecutionSummary parses the `execution …` terminal line. ok is
// false for anything else.
func ParseExecutionSummary(line string) (s ExecutionSummary, ok bool) {
	fields, ok := repairFields(line, "execution")
	if !ok || fields["result"] == "" {
		return ExecutionSummary{}, false
	}
	done, err := strconv.Atoi(fields["done"])
	if err != nil || done < 0 {
		done = 0
	}
	failed, err := strconv.Atoi(fields["failed"])
	if err != nil || failed < 0 {
		failed = 0
	}
	return ExecutionSummary{Result: fields["result"], Done: done, Failed: failed}, true
}
