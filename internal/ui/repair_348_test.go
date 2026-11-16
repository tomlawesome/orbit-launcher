package ui

// Pins #348 (Repair words for recreate-services and the folder and scanner
// classes), written from the decision on the issue alone.

import (
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const (
	w348Recreate = "recreate Orbit's containers from this folder"
	w348Moved    = "containers were created from a folder that no longer exists"
	w348Elsewhr  = "containers were created from another folder that still exists"
	w348Scanner  = "scanner setting and compose files disagree"
)

func w348Plan(t *testing.T, line string) string {
	t.Helper()
	m := ssPlan(t, 120, 30, []string{line, "plan result=ready actions=1 manual=0"}, nil, 3)
	return screen(m)
}

func TestRepair348_RecreateServicesOffersTheSafeBatch(t *testing.T) {
	s := w348Plan(t, "plan action=recreate-services resolves=folder-moved mutation=service-start backup=not-required")
	if !strings.Contains(s, "Run the safe repairs") {
		t.Errorf("a plan whose only action is recreate-services lacks the Run the safe repairs row:\n%s", s)
	}
}

func TestRepair348_RecreateServicesShowsItsWords(t *testing.T) {
	s := w348Plan(t, "plan action=recreate-services resolves=folder-moved mutation=service-start backup=not-required")
	if !strings.Contains(ssFlat(s), w348Recreate) {
		t.Errorf("no %q on the plan screen:\n%s", w348Recreate, s)
	}
	if strings.Contains(s, "recreate-services") {
		t.Errorf("the raw action token is on screen:\n%s", s)
	}
	if got := actionWords("recreate-services"); got != w348Recreate {
		t.Errorf("actionWords = %q, want %q", got, w348Recreate)
	}
}

func TestRepair348_ClassWordsOnPlanScreen(t *testing.T) {
	for _, c := range []struct{ class, want string }{
		{"folder-moved", w348Moved},
		{"folder-elsewhere", w348Elsewhr},
		{"scanner-configuration-mismatch", w348Scanner},
	} {
		s := w348Plan(t, "plan action=recreate-services resolves="+c.class+" mutation=service-start backup=not-required")
		if !strings.Contains(ssFlat(s), c.want) {
			t.Errorf("%s: plan screen lacks %q:\n%s", c.class, c.want, s)
		}
		if strings.Contains(s, c.class) {
			t.Errorf("%s: raw class token on the plan screen:\n%s", c.class, s)
		}
	}
}

func TestRepair348_ClassWordsInFindingRows(t *testing.T) {
	for _, c := range []struct{ class, sev, want string }{
		{"folder-moved", "fail", w348Moved},
		{"folder-elsewhere", "fail", w348Elsewhr},
		{"scanner-configuration-mismatch", "fail", w348Scanner},
		{"scanner-configuration-mismatch", "info", w348Scanner},
	} {
		got := ansi.Strip(findingLine(engine.Finding{Class: c.class, Target: "container", Severity: c.sev}))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s/%s: finding row %q lacks %q", c.class, c.sev, got, c.want)
		}
		if strings.Contains(got, c.class) {
			t.Errorf("%s/%s: finding row shows the raw token: %q", c.class, c.sev, got)
		}
		if got := classWords(c.class); got != c.want {
			t.Errorf("classWords(%s) = %q, want %q", c.class, got, c.want)
		}
	}
}

func TestRepair348_AfterPictureShowsWordsNotTokens(t *testing.T) {
	s := screen(ssFailedRun348(t))
	for _, want := range []string{w348Recreate, w348Moved, w348Scanner} {
		if !strings.Contains(ssFlat(s), want) {
			t.Errorf("after-picture lacks %q:\n%s", want, s)
		}
	}
	for _, raw := range []string{"recreate-services", "folder-moved", "scanner-configuration-mismatch"} {
		if strings.Contains(s, raw) {
			t.Errorf("after-picture shows %q:\n%s", raw, s)
		}
	}
}

func ssFailedRun348(t *testing.T) RepairModel {
	t.Helper()
	return ssExecuted(t, 120, 30, []string{
		"execute action=recreate-services resolves=folder-moved result=failed",
		"execution result=failed done=0 failed=1",
		"finding class=folder-moved target=container severity=fail",
		"finding class=scanner-configuration-mismatch target=configuration severity=info",
		"diagnosis result=failed checked=20 skipped=0",
	}, nil, 4)
}
