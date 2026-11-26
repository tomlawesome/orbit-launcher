package engine

// Maintenance outcome lines (#242), from Orbit's start-maintenance.sh and
// end-maintenance.sh:
//
//	maintenance action=start result=started|already-active|failed [reason=<r>] until=<RFC3339>|none
//	maintenance action=end   result=ended|not-active|failed [reason=<r>] cancelled=<n>

const (
	MaintenanceExitFailed = 1 // the action failed
	MaintenanceExitUsage  = 2 // bad arguments
	// ReasonNoOutcome marks a run that printed no outcome line.
	ReasonNoOutcome = "no-outcome"
)

// MaintenanceOutcome is one parsed outcome line, or the failure
// synthesised when there was none (Reported false).
type MaintenanceOutcome struct {
	Action    string
	Result    string // unknown carried verbatim
	Reason    string
	Until     string // "" when until=none or absent
	Cancelled int
	Reported  bool
}

// ParseMaintenanceOutcome parses one `maintenance …` line.
func ParseMaintenanceOutcome(line string) (MaintenanceOutcome, bool) {
	f, ok := repairFields(line, "maintenance")
	if !ok || f["action"] == "" || f["result"] == "" {
		return MaintenanceOutcome{}, false
	}
	o := MaintenanceOutcome{
		Action:    f["action"],
		Result:    f["result"],
		Reason:    f["reason"],
		Cancelled: nonNegative(f["cancelled"]),
		Reported:  true,
	}
	if u := f["until"]; u != "none" {
		o.Until = u
	}
	return o, true
}

// ResolveMaintenance reads the outcome from stdout and the exit code:
// the first parsable line wins; none is a failure whatever the exit
// code; a success result needs exit code 0.
func ResolveMaintenance(stdoutLines []string, exitCode int) MaintenanceOutcome {
	for _, l := range stdoutLines {
		if o, ok := ParseMaintenanceOutcome(l); ok {
			switch o.Result {
			case "started", "already-active", "ended", "not-active":
				if exitCode != 0 {
					o.Result = "failed"
				}
			}
			return o
		}
	}
	return MaintenanceOutcome{Result: "failed", Reason: ReasonNoOutcome}
}
