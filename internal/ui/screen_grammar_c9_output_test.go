package ui

// Pins #231 (screen-grammar leftovers, #200 C9): the plain-ink style,
// the wrap measure and the thin progress bar move into shared helpers
// and nothing visible changes. The screens below were rendered by the
// code as it stood BEFORE the change, escapes and all, and stored under
// testdata/c9; they must keep rendering byte for byte. A difference
// here is a defect in the change, never a reason to refresh a file.
//
// Later, deliberate changes have moved goldens, and only those: #231
// itself moved the console bar (console_bar_*_none, _phase11, _rollback);
// #238 added the unreadable-settings screens (remove_unreadable_*); #253
// moved repair_diagnosis (the manual steps wrap); #248 dropped the "Ready
// to install" screen, so install_confirm_* were deleted and
// install_profile_* added (the profile screen now names the folder). #252
// moved prompt_secret_* (the mask) and #269 moved prompt_*_40 (hints and
// notes wrap). #270 blanked the one sky cell beside text in
// console_bar_50_*, enginerun_failed_*, install_profile_50,
// install_stale_volume and remove_done_*. #271 moved remove_done_50/80/120
// and remove_notfound_50 (Remove screens stay inside the screen edge).
// #247 made the profile screen a table: install_profile_50/80/120 moved.
// #249 added the notice's scroll track and down arrow: notice_* moved.
// remove_done_50 moved again when the done screen's box took only the
// rows the menu leaves it.
// Any other difference is a regression to look at, never a file to
// re-record.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const (
	c9LongDir = "/srv/containers/mail stack/orbit production/with several/long directories/under the data volume/orbit"
	c9LongErr = "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. " +
		"Is the docker daemon running? permission denied while trying to connect to the docker API " +
		"and then a second sentence so that even the widest terminal has to wrap it onto a further row"
)

// c9Widths: 50 is narrower than both limits (width-4 binds), 80 sits
// between proseWidth and errorWidth, 120 is wider than both (the limit
// binds).
var c9Widths = []int{50, 80, 120}

func c9Size[T tea.Model](t *testing.T, m T, w int) T {
	t.Helper()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: 30})
	return m
}

// c9Screens renders every screen the three helpers touch, keyed by the
// golden file's name. Raw output: the ink and bar colours live in the
// escapes.
func c9Screens(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	deployment := &deploy.Deployment{TargetDir: c9LongDir, AppURL: "https://mail.example.com", Image: "registry.example.com/orbit:1.2.3"}
	installedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	for _, w := range c9Widths {
		tag := fmt.Sprintf("%d", w)

		// install: the profile screen names the directory at proseWidth.
		in := c9Size(t, NewInstallModel(c9LongDir, "v1"), w)
		out["install_profile_"+tag] = in.View().Content

		// remove: not found (header wraps the directory), done (prose and
		// the removal command), failed (error at errorWidth).
		nf := c9Size(t, NewRemoveModel(nil), w)
		nf.targetDir = c9LongDir
		out["remove_notfound_"+tag] = nf.View().Content

		// the same dead end for a .env-orbit that is there but unreadable
		// (#238), on a fixed fake path so the golden is stable.
		ur := c9Size(t, NewRemoveModel(nil), w)
		ur.targetDir = c9LongDir
		ur.detectErr = &fs.PathError{Op: "open", Path: c9LongDir + "/" + deploy.EnvFile, Err: syscall.EACCES}
		out["remove_unreadable_"+tag] = ur.View().Content

		rm := c9Size(t, NewRemoveModel(deployment), w)
		rm.installedAt = installedAt
		rm.state = removeStateDone
		rm.after = newMenu(removeDoneRows...) // as the stand-down's answer sets it
		out["remove_done_"+tag] = rm.View().Content
		rm.state = removeStateFailed
		rm.after = newMenu(removeFailedRows...)
		rm.standDownErr = errors.New(c9LongErr)
		out["remove_failed_"+tag] = rm.View().Content

		// repair: the error screen (errorWidth).
		rp := c9Size(t, NewRepairModel(c9LongDir, "v1"), w)
		rp.state = repairError
		rp.runErr = errors.New(c9LongErr)
		out["repair_error_"+tag] = rp.View().Content

		// the engine-run failure screen (Install and Update share it).
		run := newEngineRun("install", c9LongDir, "Install — Standard", "v1")
		run.console = newConsole("Install — Standard", "v1", func() time.Time { return time.Time{} }).setSize(w, 30)
		run.state = runFailed
		run.lastFailed = &engine.Event{Phase: "assets", Component: "fetch", State: "failed", Reason: c9LongErr, Action: "retry"}
		out["enginerun_failed_"+tag] = run.view(w, 30)
		run.lastFailed = nil
		run.runErr = errors.New(c9LongErr)
		out["enginerun_failed_err_"+tag] = run.view(w, 30)
	}

	// install: the stale-volume screen lists volume names in plain ink.
	sv := c9Size(t, NewInstallModel("/opt/orbit", "v1"), 80)
	sv.state = installStateStaleVolume
	sv.staleVolumes = []deploy.DatabaseVolume{{Name: "orbit_pgdata", Project: "orbit"}, {Name: "mail_pgdata"}}
	out["install_stale_volume"] = sv.View().Content

	// remove and update confirm: the identity line in plain ink.
	rc := c9Size(t, NewRemoveModel(deployment), 80)
	rc.installedAt = installedAt
	out["remove_confirm"] = rc.View().Content
	up := c9Size(t, NewUpdateModel(deployment, deployment.TargetDir, "v1"), 80)
	up.installedAt = installedAt
	up.state = updateStateConfirm
	out["update_confirm"] = up.View().Content

	// the prompt session: label and input row in plain ink, a typed
	// answer, a secret, and the not-yet-sized (width 0) form.
	for name, p := range map[string]engine.Prompt{
		"text":   {Field: "APP_URL", Kind: "url", Attempt: 1, Required: true},
		"secret": {Field: "OIDC_CLIENT_SECRET", Kind: "secret", Attempt: 1, Required: true},
	} {
		for _, w := range []int{0, 40, 80} {
			p := p
			s := promptSession{prompt: &p, input: inputLine{runes: []rune("https://orbit.example.com/with/a/long/enough/path")}}
			var b strings.Builder
			s.view(&b, "https://orbit.example.com", w, 0)
			out[fmt.Sprintf("prompt_%s_%d", name, w)] = b.String()
		}
	}
	var waiting strings.Builder
	promptSession{}.view(&waiting, "", 80, 0)
	out["prompt_waiting"] = waiting.String()

	// repair: results, plan lines and findings are plain ink.
	rd := c9Size(t, NewRepairModel("/opt/orbit", "v1"), 80)
	rd.state = repairExecuted
	rd.execSummary = &engine.ExecutionSummary{Result: engine.ResultComplete, Done: 2, Failed: 1}
	rd.executeResults = []engine.ExecuteResult{
		{Action: "fix-permissions", Resolves: "secret-missing", Result: engine.ResultDone},
		{Action: "restart-service", Resolves: "container-unhealthy", Result: engine.ResultFailed},
		{Action: "rotate-credentials", Resolves: "secret-stale", Result: engine.ResultSkipped},
	}
	out["repair_executed"] = rd.View().Content
	dg := c9Size(t, NewRepairModel("/opt/orbit", "v1"), 80)
	dg.state = repairDiagnosis
	dg.diagnosis = &engine.Diagnosis{Result: engine.ResultAttention, Checked: 12, Skipped: 1}
	dg.findings = []engine.Finding{
		{Class: "secret-missing", Target: "compose-file", Severity: "fail"},
		{Class: "secret-stale", Target: "env-file", Severity: "warn"},
		{Class: "secret-missing", Target: "data-volume", Severity: "info"},
	}
	dg.planActions = []engine.PlanAction{
		{Action: "fix-permissions", Resolves: "secret-missing", Mutation: "reversible", Backup: "not-required"},
		{Action: "manual", Resolves: "secret-stale", Mutation: "none", Backup: "required"},
	}
	dg.planSummary = &engine.PlanSummary{Result: "ready", Actions: 2}
	dg.exitCode = engine.ExitPlanAvailable
	out["repair_diagnosis"] = dg.View().Content

	// notice: the phrase text, and the countdown bar at the start, a
	// third of the way, nearly done and after the gate (no bar).
	start := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	const gate = 10 * time.Second
	for _, size := range [][2]int{{80, 26}, {50, 26}} {
		n := newNotice(start, size[0], size[1])
		for _, at := range []time.Duration{0, 3 * time.Second, 9500 * time.Millisecond, 10 * time.Second, time.Minute} {
			out[fmt.Sprintf("notice_%dx%d_%dms", size[0], size[1], at.Milliseconds())] = n.view(start.Add(at), gate)
		}
	}

	// console: the stage bar at every phase reached, and in rollback.
	// Both widths: box capped at 76, and narrower.
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	for _, w := range []int{80, 50} {
		c := newConsole("Install — Standard", "v9.9.9", func() time.Time { return base }).setSize(w, 26)
		out[fmt.Sprintf("console_bar_%d_none", w)] = c.view(w, 26)
		for i, p := range consolePhases {
			c = c.observeEvent(engine.Event{Phase: p.token, Component: "x", State: "completed", Reason: "r", Action: "a"})
			out[fmt.Sprintf("console_bar_%d_phase%02d", w, i)] = c.view(w, 26)
		}
		rb := c.observeEvent(engine.Event{Phase: "rollback", Component: "x", State: "started", Reason: "r", Action: "a"})
		out[fmt.Sprintf("console_bar_%d_rollback", w)] = rb.view(w, 26)
	}
	return out
}

func TestScreenGrammarC9_ScreensRenderByteForByteAsBefore(t *testing.T) {
	screens := c9Screens(t)
	again := c9Screens(t)
	for name, got := range screens {
		if again[name] != got {
			t.Fatalf("%s renders differently on a second pass; the guard would be noise", name)
		}
	}
	files, err := filepath.Glob(filepath.Join("testdata", "c9", "*.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no golden files under testdata/c9")
	}
	want := map[string]bool{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".golden")
		want[name] = true
		golden, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := screens[name]
		if !ok {
			t.Errorf("golden %s has no screen rendering it any more", name)
			continue
		}
		if got != string(golden) {
			t.Errorf("%s no longer renders as it did before #231 (screens must not move):\nwant %q\ngot  %q", name, string(golden), got)
		}
	}
	for name := range screens {
		if !want[name] {
			t.Errorf("screen %s has no golden file", name)
		}
	}
}
