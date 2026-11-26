package engine

import "testing"

// API these tests expect (internal/engine/maintenance.go), #242:
//
//	type MaintenanceOutcome struct {
//		Action    string // "start" | "end"
//		Result    string // started | already-active | ended | not-active | failed; unknown carried verbatim
//		Reason    string // database-unavailable | notice-invalid | until-invalid | until-in-past; "" when absent; unknown carried verbatim
//		Until     string // RFC 3339 text from until=, "" when until=none or absent (start lines only)
//		Cancelled int    // cancelled=<n> (end lines only); malformed or negative reads as 0
//		Reported  bool   // true when read from an outcome line; false for the synthesised failure below
//	}
//	func ParseMaintenanceOutcome(line string) (MaintenanceOutcome, bool)
//	    lead word "maintenance"; action= and result= are required and non-empty;
//	    unknown keys are ignored; any bare word (prose) makes it not-a-line.
//	func ResolveMaintenance(stdoutLines []string, exitCode int) MaintenanceOutcome
//	    the first parsable outcome line wins; no line at all is
//	    {Result: "failed", Reason: "no-outcome", Reported: false} whatever the exit code;
//	    a success result (started, already-active, ended, not-active) only counts
//	    with exit code 0: with any other exit code the result is "failed"
//	    (the line's Reason is kept; Reported stays true).
//	const MaintenanceExitFailed = 1; MaintenanceExitUsage = 2
//	    (exit 5 is the existing ExitNotInstalled; 130 interrupted is not asserted)
//	const ReasonNoOutcome = "no-outcome"

func TestMaintenanceExitCodes(t *testing.T) {
	if MaintenanceExitFailed != 1 || MaintenanceExitUsage != 2 {
		t.Fatalf("exit codes = %d, %d; the contract says 1 failed, 2 usage", MaintenanceExitFailed, MaintenanceExitUsage)
	}
	if ExitNotInstalled != 5 {
		t.Fatalf("ExitNotInstalled = %d, want 5 (not an Orbit installation or Docker unavailable)", ExitNotInstalled)
	}
}

func TestParseMaintenanceOutcome_EveryLineShape(t *testing.T) {
	cases := []struct {
		line string
		want MaintenanceOutcome
	}{
		{"maintenance action=start result=started until=2025-10-29T22:30:00Z",
			MaintenanceOutcome{Action: "start", Result: "started", Until: "2025-10-29T22:30:00Z", Reported: true}},
		{"maintenance action=start result=started until=none",
			MaintenanceOutcome{Action: "start", Result: "started", Reported: true}},
		{"maintenance action=start result=already-active until=2025-10-29T22:30:00Z",
			MaintenanceOutcome{Action: "start", Result: "already-active", Until: "2025-10-29T22:30:00Z", Reported: true}},
		{"maintenance action=start result=already-active until=none",
			MaintenanceOutcome{Action: "start", Result: "already-active", Reported: true}},
		{"maintenance action=start result=failed reason=database-unavailable until=none",
			MaintenanceOutcome{Action: "start", Result: "failed", Reason: "database-unavailable", Reported: true}},
		{"maintenance action=start result=failed reason=notice-invalid until=none",
			MaintenanceOutcome{Action: "start", Result: "failed", Reason: "notice-invalid", Reported: true}},
		{"maintenance action=start result=failed reason=until-invalid until=none",
			MaintenanceOutcome{Action: "start", Result: "failed", Reason: "until-invalid", Reported: true}},
		{"maintenance action=start result=failed reason=until-in-past until=none",
			MaintenanceOutcome{Action: "start", Result: "failed", Reason: "until-in-past", Reported: true}},
		{"maintenance action=start result=failed until=none",
			MaintenanceOutcome{Action: "start", Result: "failed", Reported: true}},
		{"maintenance action=end result=ended cancelled=0",
			MaintenanceOutcome{Action: "end", Result: "ended", Reported: true}},
		{"maintenance action=end result=ended cancelled=3",
			MaintenanceOutcome{Action: "end", Result: "ended", Cancelled: 3, Reported: true}},
		{"maintenance action=end result=not-active cancelled=0",
			MaintenanceOutcome{Action: "end", Result: "not-active", Reported: true}},
		{"maintenance action=end result=failed reason=database-unavailable cancelled=0",
			MaintenanceOutcome{Action: "end", Result: "failed", Reason: "database-unavailable", Reported: true}},
	}
	for _, c := range cases {
		got, ok := ParseMaintenanceOutcome(c.line)
		if !ok {
			t.Errorf("%q: not parsed", c.line)
			continue
		}
		if got != c.want {
			t.Errorf("%q:\n got %+v\nwant %+v", c.line, got, c.want)
		}
	}
}

func TestParseMaintenanceOutcome_UnknownFieldsIgnoredAndOrderFree(t *testing.T) {
	got, ok := ParseMaintenanceOutcome("maintenance until=none note=extra result=started action=start future=1")
	if !ok || got.Action != "start" || got.Result != "started" || got.Until != "" {
		t.Fatalf("got %+v, %v; unknown keys must be ignored and key order must not matter", got, ok)
	}
}

func TestParseMaintenanceOutcome_UnknownResultAndReasonCarriedVerbatim(t *testing.T) {
	got, ok := ParseMaintenanceOutcome("maintenance action=start result=postponed reason=moon-phase until=none")
	if !ok || got.Result != "postponed" || got.Reason != "moon-phase" {
		t.Fatalf("got %+v, %v; unknown enums are renderable, not rejected", got, ok)
	}
}

func TestParseMaintenanceOutcome_MalformedCancelledReadsAsZero(t *testing.T) {
	for _, n := range []string{"many", "-4", "", "1.5"} {
		got, ok := ParseMaintenanceOutcome("maintenance action=end result=ended cancelled=" + n)
		if !ok || got.Cancelled != 0 {
			t.Errorf("cancelled=%q: got %+v, %v; want 0", n, got, ok)
		}
	}
}

func TestParseMaintenanceOutcome_RejectsProseAndIncomplete(t *testing.T) {
	for _, line := range []string{
		"",
		"maintenance",
		"maintenance started",
		"maintenance is now active",
		"maintenance action=start",                    // no result
		"maintenance result=started",                  // no action
		"maintenance action= result=started",          // empty action
		"maintenance action=start result=",            // empty result
		"maintenance action=start result=started now", // a bare word after the lead
		"Maintenance mode started.",
		"maintenances action=start result=started until=none",
		"diagnosis result=healthy checked=1 skipped=0",
		"  also not: maintenance action=start result=started until=none",
	} {
		if got, ok := ParseMaintenanceOutcome(line); ok {
			t.Errorf("%q parsed as %+v; it is not an outcome line", line, got)
		}
	}
}

func TestResolveMaintenance_TheLineIsTheOutcome(t *testing.T) {
	got := ResolveMaintenance([]string{"preparing…", "maintenance action=end result=ended cancelled=2", "bye"}, 0)
	if got.Result != "ended" || got.Cancelled != 2 || !got.Reported {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveMaintenance_NoLineIsFailedWhateverTheExitCode(t *testing.T) {
	for _, exit := range []int{0, 1, 2, 130} {
		for _, lines := range [][]string{nil, {}, {"Maintenance mode is now on.", "done"}} {
			got := ResolveMaintenance(lines, exit)
			if got.Result != "failed" || got.Reason != ReasonNoOutcome || got.Reported {
				t.Errorf("exit %d, lines %q: got %+v, want a failed outcome with reason %q and Reported=false: success is never read from prose", exit, lines, got, ReasonNoOutcome)
			}
		}
	}
}

func TestResolveMaintenance_ASuccessNeedsExitZero(t *testing.T) {
	for _, line := range []string{
		"maintenance action=start result=started until=none",
		"maintenance action=start result=already-active until=none",
		"maintenance action=end result=ended cancelled=0",
		"maintenance action=end result=not-active cancelled=0",
	} {
		if got := ResolveMaintenance([]string{line}, 0); got.Result == "failed" || !got.Reported {
			t.Errorf("%q with exit 0: got %+v", line, got)
		}
		for _, exit := range []int{1, 2, 5, 130} {
			if got := ResolveMaintenance([]string{line}, exit); got.Result != "failed" {
				t.Errorf("%q with exit %d: got %+v, want failed", line, exit, got)
			}
		}
	}
}

func TestResolveMaintenance_AFailedLineKeepsItsReason(t *testing.T) {
	got := ResolveMaintenance([]string{"maintenance action=start result=failed reason=until-in-past until=none"}, 1)
	if got.Result != "failed" || got.Reason != "until-in-past" || !got.Reported {
		t.Fatalf("got %+v", got)
	}
}
