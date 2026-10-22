package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// #255: Repair's first screen shows each expected service's state above
// the findings. The seam these tests require of the builder: a field on
// RepairModel named serviceStates, of type
// func(dir string) ([]deploy.ServiceState, error), where
// deploy.ServiceState is struct{ Service, State, Health string } (one
// `docker compose ps -a --format json` record per container). The
// profiles come from COMPOSE_PROFILES in the folder's .env-orbit.

type svcRow struct{ glyph, name, state string }

// servicesModel is a Repair model at the diagnosis screen, w x h, in a
// folder whose .env-orbit sets profiles, with the seam returning have
// and its answer delivered.
func servicesModel(t *testing.T, profiles string, w, h int, have []deploy.ServiceState, err error) RepairModel {
	t.Helper()
	return servicesRead(t, servicesModelUnread(t, profiles, w, h, have, err))
}

// servicesRead delivers the services read the diagnosis hands back
// (#277: it runs as a command, never on the screen's thread).
func servicesRead(t *testing.T, m RepairModel) RepairModel {
	t.Helper()
	return drive(t, m, m.readServices())
}

// servicesModelUnread is servicesModel before the read has run.
func servicesModelUnread(t *testing.T, profiles string, w, h int, have []deploy.ServiceState, err error) RepairModel {
	t.Helper()
	dir := t.TempDir()
	env := "APP_URL=http://localhost\nCOMPOSE_PROFILES=" + profiles + "\n"
	if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewRepairModel(dir, "v0.6.0")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = updated.(RepairModel)
	m.serviceStates = func(got string) ([]deploy.ServiceState, error) {
		if got != dir {
			t.Errorf("seam asked about %q, want the deployment folder %q", got, dir)
		}
		return have, err
	}
	m.state = repairDiagnosis
	m.diagnosis = &engine.Diagnosis{Result: engine.ResultAttention, Checked: 12, Skipped: 1}
	m.findings = []engine.Finding{
		{Class: "secret-missing", Target: "compose-file", Severity: "fail"},
		{Class: "secret-stale", Target: "env-file", Severity: "warn"},
	}
	// A plan, as scene h of the v8 round has: the collapse rule is "the
	// list and the plan will not both fit", so a fixture without a plan
	// never collapses.
	m.planActions = []engine.PlanAction{
		{Action: "fix-permissions", Resolves: "secret-missing", Mutation: "reversible", Backup: "not-required"},
		{Action: "manual", Resolves: "secret-stale", Mutation: "none", Backup: "required"},
	}
	m.planSummary = &engine.PlanSummary{Result: "ready", Actions: 2}
	m.exitCode = engine.ExitPlanAvailable
	return m
}

func up(names ...string) []deploy.ServiceState {
	var out []deploy.ServiceState
	for _, n := range names {
		out = append(out, deploy.ServiceState{Service: n, State: "running", Health: "healthy"})
	}
	return out
}

// svcRows reads the services block off the screen: the rows that begin
// (after the sky's centring) with a service glyph. Every row's state
// word must sit at the same offset from its glyph.
func svcRows(t *testing.T, m RepairModel) []svcRow {
	t.Helper()
	var rows []svcRow
	for _, line := range strings.Split(screen(m), "\n") {
		r := []rune(line)
		for i, c := range r {
			if (c == '✓' || c == '!' || c == '✗') && i+1 < len(r) && r[i+1] == ' ' && strings.Contains(string(r[i:]), "orbit-") {
				rest := r[i+2:]
				if len(rest) < 14 {
					t.Fatalf("row too short for a 14-cell name column: %q", line)
				}
				state := ""
				if f := strings.Fields(string(rest[14:])); len(f) > 0 {
					state = f[0]
				}
				rows = append(rows, svcRow{string(c), strings.TrimSpace(string(rest[:14])), state})
				break
			}
		}
	}
	return rows
}

func assertRows(t *testing.T, m RepairModel, want []svcRow) {
	t.Helper()
	got := svcRows(t, m)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("services rows = %v, want %v; screen:\n%s", got, want, screen(m))
	}
	s := screen(m)
	label, first, find := strings.Index(s, "services"), strings.Index(s, want[0].name), strings.Index(s, "secret")
	if label < 0 || label > first {
		t.Errorf("no `services` label row above the first service:\n%s", s)
	}
	if find >= 0 && first > find {
		t.Errorf("services block is not above the findings:\n%s", s)
	}
	// Name column: every row's state word starts 16 cells after the
	// block's left edge (glyph, space, 14-cell name), so columns line up.
	col := -1
	for _, line := range strings.Split(s, "\n") {
		r := []rune(line)
		for i, c := range r {
			if (c == '✓' || c == '!' || c == '✗') && i+1 < len(r) && r[i+1] == ' ' && strings.Contains(string(r[i:]), "orbit-") {
				word := strings.TrimSpace(string(r[i+16:]))
				at := i + 16
				if word == "" {
					t.Errorf("no state word in %q", line)
				}
				if col >= 0 && i != col {
					t.Errorf("glyph columns differ (%d vs %d):\n%s", i, col, s)
				}
				col = i
				_ = at
				break
			}
		}
	}
	// Left-aligned as a unit: the label starts at the glyph column.
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "services" {
			if idx := len([]rune(line)) - len([]rune(strings.TrimLeft(line, " "))); col >= 0 && idx != col {
				t.Errorf("label at column %d, rows at %d:\n%s", idx, col, s)
			}
		}
	}
}

func TestRepairServices_AllRunning(t *testing.T) {
	m := servicesModel(t, "standard", 80, 30, up("orbit-app", "orbit-db", "orbit-clamav"), nil)
	assertRows(t, m, []svcRow{
		{"✓", "orbit-db", "running"}, {"✓", "orbit-app", "running"}, {"✓", "orbit-clamav", "running"},
	})
	if n := strings.Count(screen(m), "✓ orbit-"); n != 3 {
		t.Errorf("%d running rows, want 3", n)
	}
}

func TestRepairServices_FaultedAndMissing(t *testing.T) {
	have := []deploy.ServiceState{
		{Service: "orbit-db", State: "running", Health: "healthy"},
		{Service: "orbit-app", State: "running", Health: "unhealthy"},
		{Service: "orbit-clamav", State: "running", Health: "starting"},
		{Service: "orbit-tika", State: "restarting"},
	}
	m := servicesModel(t, "full", 80, 30, have, nil)
	assertRows(t, m, []svcRow{
		{"✓", "orbit-db", "running"}, {"!", "orbit-app", "unhealthy"}, {"!", "orbit-clamav", "starting"},
		{"!", "orbit-tika", "restarting"}, {"✗", "orbit-ollama", "missing"},
	})
}

func TestRepairServices_StoppedStates(t *testing.T) {
	for _, st := range []string{"exited", "created", "paused", "dead"} {
		t.Run(st, func(t *testing.T) {
			have := up("orbit-db", "orbit-clamav")
			have = append(have, deploy.ServiceState{Service: "orbit-app", State: st})
			m := servicesModel(t, "standard", 80, 30, have, nil)
			assertRows(t, m, []svcRow{
				{"✓", "orbit-db", "running"}, {"!", "orbit-app", "stopped"}, {"✓", "orbit-clamav", "running"},
			})
		})
	}
}

func TestRepairServices_NoneCreated(t *testing.T) {
	m := servicesModel(t, "standard", 80, 30, nil, nil)
	assertRows(t, m, []svcRow{
		{"✗", "orbit-db", "missing"}, {"✗", "orbit-app", "missing"}, {"✗", "orbit-clamav", "missing"},
	})
}

func TestRepairServices_AIProfileOnly(t *testing.T) {
	m := servicesModel(t, "ai", 80, 30, up("orbit-db", "orbit-app", "orbit-clamav", "orbit-ollama"), nil)
	assertRows(t, m, []svcRow{
		{"✓", "orbit-db", "running"}, {"✓", "orbit-app", "running"}, {"✓", "orbit-clamav", "running"},
		{"✓", "orbit-ollama", "running"},
	})
	if strings.Contains(screen(m), "orbit-tika") {
		t.Errorf("tika listed without the processing or full profile:\n%s", screen(m))
	}
}

func TestRepairServices_ProcessingProfileHasTikaNotOllama(t *testing.T) {
	m := servicesModel(t, "processing", 80, 30, up("orbit-db", "orbit-app", "orbit-clamav", "orbit-tika"), nil)
	assertRows(t, m, []svcRow{
		{"✓", "orbit-db", "running"}, {"✓", "orbit-app", "running"}, {"✓", "orbit-clamav", "running"},
		{"✓", "orbit-tika", "running"},
	})
	if strings.Contains(screen(m), "orbit-ollama") {
		t.Errorf("ollama listed without the ai or full profile:\n%s", screen(m))
	}
}

func TestRepairServices_CouldNotCheck(t *testing.T) {
	m := servicesModel(t, "standard", 80, 30, nil, fmt.Errorf("docker: not running"))
	s := plain(m)
	if !strings.Contains(s, "services · could not check") {
		t.Errorf("no `services · could not check` row:\n%s", screen(m))
	}
	if rows := svcRows(t, m); len(rows) != 0 {
		t.Errorf("per-service rows shown after the check failed: %v", rows)
	}
	if strings.Count(screen(m), "services") != 1 {
		t.Errorf("the block is not exactly one row:\n%s", screen(m))
	}
}

// Short screens: the running rows collapse, faulted ones keep a row.
func TestRepairServices_CollapsesOnShortScreen(t *testing.T) {
	have := up("orbit-db", "orbit-app", "orbit-clamav", "orbit-ollama")
	have = append(have, deploy.ServiceState{Service: "orbit-tika", State: "restarting"})
	short := servicesModel(t, "full", 80, 20, have, nil)
	s := screen(short)
	if !strings.Contains(plain(short), "✓ 4 running") {
		t.Errorf("no collapsed `✓ 4 running` row at 80x20:\n%s", s)
	}
	if strings.Contains(s, "✓ orbit-") {
		t.Errorf("running services still have their own rows at 80x20:\n%s", s)
	}
	assertFaultRowsOnly := svcRows(t, short)
	if len(assertFaultRowsOnly) != 1 || assertFaultRowsOnly[0] != (svcRow{"!", "orbit-tika", "restarting"}) {
		t.Errorf("faulted row lost or extra rows at 80x20: %v\n%s", assertFaultRowsOnly, s)
	}
	if !strings.Contains(s, "Menu") && !strings.Contains(s, "Exit") {
		t.Errorf("menu not on the short screen:\n%s", s)
	}
	if n := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); n > 20 {
		t.Errorf("screen is %d rows, over the 20 available:\n%s", n, s)
	}

	tall := servicesModel(t, "full", 80, 26, have, nil)
	if strings.Contains(plain(tall), "running") && strings.Contains(plain(tall), "4 running") {
		t.Errorf("collapsed at 80x26 where everything fits:\n%s", screen(tall))
	}
	assertRows(t, tall, []svcRow{
		{"✓", "orbit-db", "running"}, {"✓", "orbit-app", "running"}, {"✓", "orbit-clamav", "running"},
		{"!", "orbit-tika", "restarting"}, {"✓", "orbit-ollama", "running"},
	})
}

// A faulted or missing row is never folded into the collapsed count.
func TestRepairServices_CollapseKeepsMissingRows(t *testing.T) {
	m := servicesModel(t, "full", 80, 20, up("orbit-db", "orbit-app", "orbit-clamav"), nil)
	assertRowsPresent := svcRows(t, m)
	want := []svcRow{{"✗", "orbit-tika", "missing"}, {"✗", "orbit-ollama", "missing"}}
	if fmt.Sprint(assertRowsPresent) != fmt.Sprint(want) {
		t.Errorf("rows = %v, want %v; screen:\n%s", assertRowsPresent, want, screen(m))
	}
	if !strings.Contains(plain(m), "✓ 3 running") {
		t.Errorf("no `✓ 3 running`:\n%s", screen(m))
	}
}

// Reading is read-only: rendering twice gives the same screen and the
// seam is not asked to change anything (it has no way to).
func TestRepairServices_RenderingIsRepeatable(t *testing.T) {
	m := servicesModel(t, "standard", 80, 30, up("orbit-db", "orbit-app", "orbit-clamav"), nil)
	if a, b := screen(m), screen(m); a != b {
		t.Errorf("two renders differ:\n%s\n---\n%s", a, b)
	}
}

// Diagnose again reads the services again: the after-picture must not
// keep showing the state read for the first diagnosis.
func TestRepairServices_DiagnoseAgainReadsTheServicesAgain(t *testing.T) {
	have := []deploy.ServiceState{
		{Service: "orbit-db", State: "running", Health: "healthy"},
		{Service: "orbit-app", State: "exited"},
		{Service: "orbit-clamav", State: "running", Health: "healthy"},
	}
	calls := 0
	m := servicesModelUnread(t, "", 80, 30, nil, nil)
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		calls++
		return have, nil
	}
	m = servicesRead(t, m)

	assertRows(t, m, []svcRow{{"✓", "orbit-db", "running"}, {"!", "orbit-app", "stopped"}, {"✓", "orbit-clamav", "running"}})
	if calls != 1 {
		t.Fatalf("first diagnosis read the services %d times, want 1", calls)
	}
	// Rendering again must not read again.
	_ = screen(m)
	_ = screen(m)
	if calls != 1 {
		t.Fatalf("rendering the diagnosis read the services %d times in total, want 1", calls)
	}

	m.state = repairExecuted
	m.menu = menu{}
	m, _ = updateAs(t, m, key(tea.KeyEnter))

	have = up("orbit-db", "orbit-app", "orbit-clamav")
	m = repairLines(t, m, "diagnosis result=healthy checked=15 skipped=0")
	m, read := repairDone(t, m, engine.DoneMsg{ExitCode: engine.ExitHealthy})
	m = drive(t, m, read)

	if calls != 2 {
		t.Errorf("services read %d times after Diagnose again, want 2", calls)
	}
	rows := svcRows(t, m)
	var sawApp bool
	for _, r := range rows {
		if r.state == "stopped" {
			t.Errorf("a stopped row remains after Diagnose again: %v", rows)
		}
		if r.name == "orbit-app" {
			sawApp = true
			if r.glyph != "✓" || r.state != "running" {
				t.Errorf("orbit-app row = %v, want ✓ orbit-app running", r)
			}
		}
	}
	if !sawApp {
		t.Errorf("no orbit-app row; screen:\n%s", screen(m))
	}
	_ = screen(m)
	_ = screen(m)
	if calls != 2 {
		t.Errorf("rendering added reads: %d, want 2", calls)
	}
}
