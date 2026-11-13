package ui

// Pins #256 (Repair starts a deployment whose services are not running),
// written from .agents/designs/2026-11-13-repair-start-services.md alone.
// Case numbers are the "Test specification" numbers in that note.

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

const (
	ssPlanLine = "plan action=start-services resolves=services-not-running mutation=service-start backup=not-required target=container rollback=not-required expect=services-healthy"
	ssPlanDone = "plan result=ready actions=1 manual=0"

	ssManualPlan  = "plan action=manual resolves=services-not-running mutation=none backup=not-required"
	ssManualGuide = "manual step: services are not running, and repair starts them only when the deployment is complete on disk and only Docker is missing; resolve secret-missing first, then re-run repair (resolves=services-not-running)"
	ssManualDone  = "plan result=manual-required actions=0 manual=1"

	ssExecDone   = "execute action=start-services resolves=services-not-running result=done"
	ssExecFailed = "execute action=start-services resolves=services-not-running result=failed"
	ssFinding    = "finding class=services-not-running target=container severity=fail"

	ssPlanText = "start Orbit's services — a service isn't running"
	ssDockerEN = "Docker said: Error response from daemon: no space left on device"
)

func ssSized(t *testing.T, w, h int) RepairModel {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func ssPlan(t *testing.T, w, h int, stdout []string, tail []string, exit int) RepairModel {
	t.Helper()
	m := ssSized(t, w, h)
	m = repairLines(t, m, stdout...)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: exit, StderrTail: tail})
	return m
}

func ssExecuted(t *testing.T, w, h int, stdout []string, tail []string, exit int) RepairModel {
	t.Helper()
	m := ssSized(t, w, h)
	m.state = repairExecuting
	m = repairLines(t, m, stdout...)
	var err error
	if exit != 0 {
		err = errors.New("exit status 4")
	}
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: exit, Err: err, StderrTail: tail})
	return m
}

func ssFailedRun(t *testing.T, w, h int, tail []string) RepairModel {
	t.Helper()
	return ssExecuted(t, w, h, []string{
		ssExecFailed,
		"execution result=failed done=0 failed=1",
		ssFinding,
		"diagnosis result=failed checked=20 skipped=0",
	}, tail, 4)
}

func ssFlat(s string) string { return strings.Join(skyWords(s), " ") }

func ssNoRaw(t *testing.T, s string) {
	t.Helper()
	for _, raw := range []string{"start-services", "services-not-running", "service-start", "services-healthy"} {
		if strings.Contains(s, raw) {
			t.Errorf("the screen shows %q:\n%s", raw, s)
		}
	}
}

func ssRowWith(s, sub string) (int, string) {
	for i, r := range strings.Split(s, "\n") {
		if strings.Contains(r, sub) {
			return i, r
		}
	}
	return -1, ""
}

func ssAssertWidth(t *testing.T, s string, w int) {
	t.Helper()
	for i, r := range strings.Split(s, "\n") {
		if got := lipgloss.Width(r); got > w {
			t.Errorf("row %d is %d cells wide on a %d-column screen: %q", i, got, w, r)
		}
	}
}

// Case 1.
func TestRepairStartServices_SafePlanIsProposedAndRunnable(t *testing.T) {
	m := ssPlan(t, 80, 26, []string{ssPlanLine, ssPlanDone}, nil, 3)
	s := screen(m)
	if !strings.Contains(s, "Repairs proposed") {
		t.Fatalf("title:\n%s", s)
	}
	if !strings.Contains(ssFlat(s), ssPlanText) {
		t.Errorf("no plan row %q:\n%s", ssPlanText, s)
	}
	if !strings.Contains(ssFlat(s), "a safe plan is ready — pick a repair below to run it") {
		t.Errorf("summary missing:\n%s", s)
	}
	ssNoRaw(t, s)
	run, menu, exit := strings.Index(s, "Run the safe repairs"), strings.Index(s, "Menu"), strings.Index(s, "Exit")
	if run < 0 || menu < run || exit < menu {
		t.Errorf("menu should hold Run the safe repairs, Menu, Exit in order:\n%s", s)
	}
}

// Case 2.
func TestRepairStartServices_SafePlanWrapsAt50AndIsOneRowAt120(t *testing.T) {
	s := screen(ssPlan(t, 50, 26, []string{ssPlanLine, ssPlanDone}, nil, 3))
	ssAssertWidth(t, s, 50)
	if !strings.Contains(s, "Run the safe repairs") {
		t.Errorf("Run the safe repairs not offered at 50 columns:\n%s", s)
	}
	i, first := ssRowWith(s, "start Orbit's services")
	if i < 0 {
		t.Fatalf("no plan row:\n%s", s)
	}
	rows := strings.Split(s, "\n")
	if strings.Contains(first, "running") {
		t.Errorf("the plan row should wrap at 50 columns, not stay one row: %q", first)
	}
	next := rows[i+1]
	if !strings.Contains(next, "running") {
		t.Errorf("the continuation row lacks the rest of the text: %q", next)
	}
	startCol := lipgloss.Width(first[:strings.Index(first, "start Orbit's")])
	idx := strings.IndexFunc(next, func(r rune) bool { return r >= 'a' && r <= 'z' })
	if idx >= 0 && lipgloss.Width(next[:idx]) < startCol {
		t.Errorf("the continuation is not indented under the text (text at %d): %q", startCol, next)
	}

	s = screen(ssPlan(t, 120, 26, []string{ssPlanLine, ssPlanDone}, nil, 3))
	if _, row := ssRowWith(s, "start Orbit's services"); !strings.Contains(row, "a service isn't running") {
		t.Errorf("at 120 columns the plan row is one row, got %q", row)
	}
}

// Case 3.
func TestRepairStartServices_RunTheSafeRepairsStartsTheSafeBatch(t *testing.T) {
	m := ssPlan(t, 80, 26, []string{ssPlanLine, ssPlanDone}, nil, 3)
	var modes []deploy.RepairMode
	m.prepareRepair = func(_ context.Context, _ string, mode deploy.RepairMode) (*engine.Stream, error) {
		modes = append(modes, mode)
		return nil, errors.New("stop here")
	}
	_, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Run the safe repairs started nothing")
	}
	cmd()
	if len(modes) != 1 || modes[0] != deploy.RepairExecuteSafe {
		t.Fatalf("modes = %v, want one --execute --safe-only run", modes)
	}
}

// Case 4.
func TestRepairStartServices_ManualPlanShowsEnginesGuidance(t *testing.T) {
	m := ssPlan(t, 80, 26, []string{ssManualPlan, ssManualDone}, []string{ssManualGuide}, 4)
	s := screen(m)
	if !strings.Contains(s, "Needs your attention") {
		t.Fatalf("title:\n%s", s)
	}
	flat := ssFlat(s)
	for _, want := range []string{
		"needs your hands — a service isn't running",
		"services are not running, and repair starts them only when the deployment is complete on disk and only Docker is missing; resolve secret-missing first, then re-run repair",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "resolves=") || strings.Contains(s, "manual step:") {
		t.Errorf("protocol text on screen:\n%s", s)
	}
	if strings.Contains(s, "Run the safe repairs") {
		t.Errorf("a manual-only plan offers Run the safe repairs:\n%s", s)
	}

	s = screen(ssPlan(t, 50, 40, []string{ssManualPlan, ssManualDone}, []string{ssManualGuide}, 4))
	ssAssertWidth(t, s, 50)
	if i, row := ssRowWith(s, "needs your hands"); i < 0 || !strings.Contains(row, "isn't running") {
		t.Errorf("at 50 columns the manual row should be one row, got %q:\n%s", row, s)
	}
	if !strings.Contains(ssFlat(s), "resolve secret-missing first, then re-run repair") {
		t.Errorf("guidance lost at 50 columns:\n%s", s)
	}
}

// Case 5.
func TestRepairStartServices_AppliedAfterPicture(t *testing.T) {
	s := screen(ssExecuted(t, 80, 26, []string{
		ssExecDone, "execution result=complete done=1 failed=0", "diagnosis result=healthy checked=20 skipped=0",
	}, nil, 0))
	for _, want := range []string{"Repairs applied", "✓ start Orbit's services — a service isn't running", "1 done · 0 failed", "diagnosis clear after repairs"} {
		if !strings.Contains(ssFlat(s), ssFlat(want)) {
			t.Errorf("after-picture lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Docker said") {
		t.Errorf("a Docker row on a clean run:\n%s", s)
	}
	ssNoRaw(t, s)
}

// Case 6.
func TestRepairStartServices_FailedShowsDockersWords(t *testing.T) {
	m := ssFailedRun(t, 80, 26, []string{
		"Orbit repair: " + ssDockerEN,
		"Orbit repair: something else went wrong",
		"Orbit repair: and another unrelated line",
	})
	s := screen(m)
	for _, want := range []string{"Some repairs failed", "✗ start Orbit's services — a service isn't running", "0 done · 1 failed", "still standing after repairs:", "containers — a service isn't running"} {
		if !strings.Contains(ssFlat(s), ssFlat(want)) {
			t.Errorf("after-picture lacks %q:\n%s", want, s)
		}
	}
	d := strings.Index(s, ssDockerEN)
	if d < 0 {
		t.Fatalf("Docker's words are not shown:\n%s", s)
	}
	if e, tally := strings.Index(s, "start Orbit's services"), strings.Index(s, "0 done · 1 failed"); !(e < d && d < tally) {
		t.Errorf("the Docker row must sit between the execute rows and the tally:\n%s", s)
	}
	if strings.Contains(s, "something else") || strings.Contains(s, "unrelated line") {
		t.Errorf("unrelated stderr shown:\n%s", s)
	}
	if strings.Contains(s, "Orbit repair:") {
		t.Errorf("the Orbit repair: prefix is on screen:\n%s", s)
	}
	row := rawRowOf(t, m, ssDockerEN)
	if !strings.Contains(row, style.Tagline.Render(ssDockerEN)) {
		t.Errorf("the Docker row is not faint: %q", row)
	}
}

// Case 7.
func TestRepairStartServices_TimeoutShowsBothDockerRowsInOrder(t *testing.T) {
	const timeout = "Docker Compose did not finish within 300 s, so it was stopped."
	s := screen(ssFailedRun(t, 120, 30, []string{"Orbit repair: " + ssDockerEN, "Orbit repair: " + timeout}))
	a, b := strings.Index(s, ssDockerEN), strings.Index(s, timeout)
	if a < 0 || b < 0 || a > b {
		t.Fatalf("both Docker rows, in stderr order, expected:\n%s", s)
	}
	if strings.Contains(s, "Orbit repair:") {
		t.Errorf("prefix on screen:\n%s", s)
	}
}

// Case 8.
func TestRepairStartServices_LongDockerLineIsBounded(t *testing.T) {
	long := "Docker said: " + strings.Repeat("word ", 100)
	long = strings.TrimSpace(long)[:500]
	tail := []string{"Orbit repair: " + long}

	// The rows the screen leaves (height less every other row less one)
	// hold a 500-character line at 50x26, so all of it shows.
	s := screen(ssFailedRun(t, 50, 26, tail))
	ssAssertWidth(t, s, 50)
	if !strings.Contains(ssFlat(s), ssFlat(long)) || strings.Contains(s, "…") {
		t.Errorf("a 500-character line fits at 50x26 and should show whole:\n%s", s)
	}
	// Where the words do not fit, they are kept behind one … row.
	longer := strings.TrimSpace(strings.Repeat("word ", 400))
	s = screen(ssFailedRun(t, 50, 26, []string{"Orbit repair: Docker said: " + longer}))
	ssAssertWidth(t, s, 50)
	if strings.Count(s, "…") != 1 {
		t.Errorf("a line too long for 50x26 should be kept behind one … row:\n%s", s)
	}
	for _, want := range []string{"Diagnose again", "Menu", "Exit"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q pushed off the screen:\n%s", want, s)
		}
	}
	if got := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); got > 26 {
		t.Errorf("screen is %d rows on a 26-row terminal", got)
	}

	s = screen(ssFailedRun(t, 80, 40, tail))
	if !strings.Contains(ssFlat(s), ssFlat(long)) {
		t.Errorf("at 80x40 the whole line should show:\n%s", s)
	}
	if strings.Contains(s, "…") {
		t.Errorf("no … row expected at 80x40:\n%s", s)
	}
}

// Case 9.
func TestRepairStartServices_NoDockerRowWithoutAFailedRow(t *testing.T) {
	s := screen(ssExecuted(t, 80, 26, []string{
		ssExecDone, "execution result=complete done=1 failed=0", "diagnosis result=healthy checked=20 skipped=0",
	}, []string{"Orbit repair: " + ssDockerEN}, 0))
	if strings.Contains(s, "Docker") {
		t.Errorf("a Docker row with no failed execute row:\n%s", s)
	}
}

// Case 10.
func TestRepairStartServices_DockerWordsAreForeignText(t *testing.T) {
	s := screen(ssFailedRun(t, 80, 30, []string{"Orbit repair: Docker said: bad\x1b[2J disk\rfull\u202eend"}))
	raw := ssFailedRun(t, 80, 30, []string{"Orbit repair: Docker said: bad\x1b[2J disk\rfull\u202eend"}).View().Content
	for _, bad := range []string{"\x1b[2J", "\r", "\u202e"} {
		if strings.Contains(s, bad) || strings.Contains(ansi.Strip(raw), bad) {
			t.Errorf("control %q reached the screen:\n%q", bad, s)
		}
	}
	if !strings.Contains(s, "Docker said:") {
		t.Fatalf("the Docker row vanished:\n%s", s)
	}
	for _, kept := range []string{"bad", "disk", "full", "end"} {
		if !strings.Contains(s, kept) {
			t.Errorf("%q was dropped with the controls:\n%s", kept, s)
		}
	}
}

// Case 11.
func TestRepairStartServices_SkippedRowIsFaint(t *testing.T) {
	m := ssExecuted(t, 80, 26, []string{
		"execute action=start-services resolves=services-not-running result=skipped",
		"execution result=unactionable done=0 failed=0",
	}, []string{"Orbit repair: " + ssDockerEN}, 0)
	s := screen(m)
	if !strings.Contains(s, "Nothing safe to run") {
		t.Errorf("title:\n%s", s)
	}
	if strings.Contains(s, "Docker") {
		t.Errorf("a Docker row on a skipped run:\n%s", s)
	}
	if !strings.Contains(ssFlat(s), ssPlanText) {
		t.Fatalf("skipped row missing:\n%s", s)
	}
	if row := rawRowOf(t, m, "start Orbit's services"); !strings.Contains(row, style.Tagline.Render(ssPlanText)) {
		t.Errorf("skipped row is not faint: %q", row)
	}
}

// Case 12 (the parsers already carry the new values; pinned so they keep to it).
func TestRepairStartServices_ParsersCarryTheNewValues(t *testing.T) {
	p, ok := engine.ParsePlanAction(ssPlanLine)
	if !ok || p.Mutation != "service-start" || p.Backup != "not-required" {
		t.Errorf("ParsePlanAction = %+v, %v", p, ok)
	}
	e, ok := engine.ParseExecuteResult(ssExecFailed)
	if !ok || e.Result != "failed" {
		t.Errorf("ParseExecuteResult = %+v, %v", e, ok)
	}
}

// Case 13.
func TestRepairStartServices_WordTables(t *testing.T) {
	if got := classWords("services-not-running"); got != "a service isn't running" {
		t.Errorf("classWords = %q", got)
	}
	if got := actionWords("start-services"); got != "start Orbit's services" {
		t.Errorf("actionWords = %q", got)
	}
	found := map[string]bool{}
	for _, p := range realPlanPairs {
		if p.class == "services-not-running" {
			found[p.action] = true
		}
	}
	if !found["start-services"] || !found["manual"] {
		t.Errorf("realPlanPairs lacks the services-not-running pairs: %v", found)
	}
	order := ""
	for _, o := range orbitClassOrder {
		if o.class == "services-not-running" {
			order = o.action
		}
	}
	if order != "start-services|manual" {
		t.Errorf("orbitClassOrder pairs services-not-running with %q, want start-services|manual", order)
	}
	got := ansi.Strip(findingLine(engine.Finding{Class: "services-not-running", Target: "container", Severity: "fail"}))
	if !strings.Contains(got, "containers — a service isn't running") {
		t.Errorf("finding row = %q", got)
	}
}

// Case 14.
func TestRepairStartServices_NeighboursAreUnchanged(t *testing.T) {
	if got := classWords("database-unreachable"); got != "can't be reached" {
		t.Errorf("database-unreachable now reads %q", got)
	}

	s := screen(ssPlan(t, 80, 26, []string{
		"plan action=restart-services resolves=stale-container mutation=reversible backup=not-required",
		ssPlanDone,
	}, nil, 3))
	if !strings.Contains(ssFlat(s), "restart Orbit's services — a container is running an older image than configured") ||
		!strings.Contains(s, "Run the safe repairs") {
		t.Errorf("restart-services plan changed:\n%s", s)
	}

	s = screen(ssPlan(t, 80, 26, []string{
		"plan action=rotate-database-credential resolves=database-credential-mismatch mutation=credential-rotation backup=required",
		ssPlanDone,
	}, nil, 3))
	if !strings.Contains(s, "Rotate database credentials") || strings.Contains(s, "Run the safe repairs") {
		t.Errorf("a rotate-only plan must offer only Rotate database credentials:\n%s", s)
	}

	s = screen(ssExecuted(t, 80, 26, []string{
		"execute action=restart-services resolves=application-unhealthy result=failed",
		"execution result=failed done=0 failed=1",
		"finding class=application-unhealthy target=application severity=fail",
		"diagnosis result=failed checked=15 skipped=0",
	}, []string{"Orbit repair: some line"}, 4))
	if strings.Contains(s, "Docker") {
		t.Errorf("a failed restart-services with no Docker line grew a Docker row:\n%s", s)
	}

	m := ssSized(t, 80, 26)
	m.state = repairExecuting
	m = repairLines(t, m, ssExecDone)
	if !strings.Contains(screen(m), "running the safe repairs — every step reversible") {
		t.Errorf("working line changed:\n%s", screen(m))
	}
}
