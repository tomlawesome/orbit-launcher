package ui

// Pins #357: plain words for the Repair codes Orbit's repair.sh added at
// 0d522cec (ai/orbit#1804, #1817), written from the issue alone.
// The "no class or action falls back to raw text" walk over orbitClassOrder
// is new here: no earlier test covers every class of orbitClassOrder.

import (
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

var words357Class = []struct{ class, target, want string }{
	{"restore-unfinished", "restore", "a restore didn't finish"},
	{"service-restarting", "container", "a service keeps restarting"},
	{"database-volume-missing", "database", "the database volume is missing"},
	{"smtp-lines-retirable", "env-file", "old SMTP mail lines are left over; Orbit keeps mail settings itself now"},
}

const words357Action = "remove the old SMTP mail lines (Orbit keeps a backup)"

func TestRepair357_ClassWords(t *testing.T) {
	for _, c := range words357Class {
		if got := classWords(c.class); got != c.want {
			t.Errorf("classWords(%q) = %q, want %q", c.class, got, c.want)
		}
		got := ansi.Strip(findingLine(engine.Finding{Class: c.class, Target: c.target, Severity: "warn"}))
		if !strings.Contains(got, c.want) || strings.Contains(got, c.class) {
			t.Errorf("finding %s/%s reads %q, want words %q and no raw class", c.class, c.target, got, c.want)
		}
	}
}

func TestRepair357_ActionWords(t *testing.T) {
	if got := actionWords("retire-smtp-lines"); got != words357Action {
		t.Errorf("actionWords(retire-smtp-lines) = %q, want %q", got, words357Action)
	}
}

func TestRepair357_TargetWordsRestore(t *testing.T) {
	if got := targetWords("restore"); got != "restore" {
		t.Errorf("targetWords(restore) = %q, want %q", got, "restore")
	}
}

func TestRepair357_RetireSMTPPlanLine(t *testing.T) {
	p := realPlanPair{"smtp-lines-retirable", "retire-smtp-lines"}
	got := ansi.Strip(planLine(p.planAction()))
	if !strings.Contains(got, words357Action) || strings.Contains(got, "retire-smtp-lines") || strings.Contains(got, "smtp-lines-retirable") {
		t.Errorf("plan line reads %q, want %q and no raw codes", got, words357Action)
	}
}

// Every class and action in Orbit's class order has a case: none falls back
// to its raw text. A raw fallback returns the input unchanged.
func TestRepair357_NoOrbitClassOrActionFallsBackToRawText(t *testing.T) {
	for _, o := range orbitClassOrder {
		if got := classWords(o.class); got == o.class {
			t.Errorf("classWords(%q) falls back to the raw class", o.class)
		}
		for _, a := range strings.Split(o.action, "|") {
			if a == "manual" {
				continue
			}
			if got := actionWords(a); got == a {
				t.Errorf("actionWords(%q) falls back to the raw action (class %s)", a, o.class)
			}
		}
	}
}
