package engine

import "strings"

// Repair diagnosis contract — orbit scripts/repair.sh --check (issue
// orbit#261, first slice). One `finding` line per finding, then exactly
// one terminal `diagnosis` line. Enums only: stdout never carries a
// path, a configured value, or a secret.

// repair.sh's exit codes (orbit docs/engine-events.md, "Exit codes").
// They are repair's own and collide with install.sh's: never route one
// script's exit code through the other's table.
const (
	ExitHealthy   = 0 // --check healthy; --plan empty; --execute succeeded
	ExitUsage     = 2 // usage error, including a repair.sh too old for the mode
	ExitAttention = 3 // --check: warnings only
	// ExitPlanAvailable is --plan's reading of the same code: a plan
	// was proposed.
	ExitPlanAvailable = ExitAttention
	ExitFailed        = 4 // a fail finding, unplannable failures, or a failed batch
	ExitNotInstalled  = 5 // not an Orbit installation, in every mode
	// ExitDangerousRefused is "refused, unmutated": the dangerous
	// batch's gate was never passed, or the whole execution was refused
	// on an unsupported deployment version. Nothing was attempted; it
	// is an outcome, not a failure.
	ExitDangerousRefused = 6
)

// repair.sh's result vocabularies (orbit docs/engine-events.md,
// "result vocabularies"). Each line form has its own closed set; a word
// shared between sets is one constant here, never a promise that the
// sets are interchangeable.
const (
	ResultHealthy        = "healthy"         // diagnosis
	ResultAttention      = "attention"       // diagnosis
	ResultFailed         = "failed"          // diagnosis, execute, execution, dangerous
	ResultEmpty          = "empty"           // plan, execution, dangerous
	ResultReady          = "ready"           // plan
	ResultManualRequired = "manual-required" // plan
	ResultDone           = "done"            // execute
	ResultSkipped        = "skipped"         // execute
	ResultComplete       = "complete"        // execution, dangerous
	ResultUnactionable   = "unactionable"    // execution
	ResultDeclined       = "declined"        // execution
	ResultRefused        = "refused"         // execution, dangerous
)

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
	return Diagnosis{
		Result:  fields["result"],
		Checked: nonNegative(fields["checked"]),
		Skipped: nonNegative(fields["skipped"]),
	}, true
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
	return PlanSummary{
		Result:  fields["result"],
		Actions: nonNegative(fields["actions"]),
		Manual:  nonNegative(fields["manual"]),
	}, true
}

// repairFields reads a "<lead> key=value ..." line through
// keyValueFields, tolerating unknown keys and rejecting prose (any bare
// word after the lead).
func repairFields(line, lead string) (map[string]string, bool) {
	tokens := strings.Fields(line)
	if len(tokens) < 2 || tokens[0] != lead {
		return nil, false
	}
	return keyValueFields(tokens[1:])
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
	Result string // empty | complete | unactionable | declined | failed | refused
	Done   int
	Failed int
	// Reason is set only for result=refused, currently
	// deployment-version-unsupported; empty when the line carries none.
	Reason string
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
	return ExecutionSummary{
		Result: fields["result"],
		Done:   nonNegative(fields["done"]),
		Failed: nonNegative(fields["failed"]),
		Reason: fields["reason"],
	}, true
}

// Dangerous is the `dangerous result=…` terminal line printed once by
// repair.sh --execute --dangerous (orbit#533), after the safe batch's
// own execution summary. result=refused means the approval gate was
// never passed — nothing was attempted, and exit code 6 says so: an
// outcome, not a failure.
type Dangerous struct {
	Result string // empty | complete | refused | failed
	Done   int
	Failed int
	Reason string // none | non-interactive | refused-by-operator | checkpoint-failed | step-failed
}

// ParseDangerous parses the `dangerous …` terminal line. ok is false
// for anything else, including a line missing the contract's result or
// reason.
func ParseDangerous(line string) (d Dangerous, ok bool) {
	fields, ok := repairFields(line, "dangerous")
	if !ok || fields["result"] == "" || fields["reason"] == "" {
		return Dangerous{}, false
	}
	return Dangerous{
		Result: fields["result"],
		Done:   nonNegative(fields["done"]),
		Failed: nonNegative(fields["failed"]),
		Reason: fields["reason"],
	}, true
}
