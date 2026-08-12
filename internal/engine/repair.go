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
