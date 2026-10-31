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
// rows the menu leaves it. #257 moved enginerun_failed_* (the stopped
// screen says what happened to the folder, no phase heading, no next:)
// and added enginerun_failed_recovery_* and enginerun_failed_abort_*,
// built from full engine streams.
// #220 added a golden for every screen that had none (the splash and its
// states, the profile choice's other screens, the stopped screens'
// other endings, the configuration screens, Update's dead ends, Remove's
// stand-down, and Repair's working, stop and result screens, and the
// success screen's four states), so a moved blank line or row anywhere
// fails here. Those were recorded from the code as it stood, and no
// earlier golden moved.
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

		// #257: the stopped screen after an unfinished rollback that left
		// a recovery command, and after an abort with a long block.
		out["enginerun_failed_recovery_"+tag] = c9StoppedRun(t, w,
			hostDone, rollbackRunning,
			engine.RawLineMsg{Text: "recovery-command: " + engineCommand},
			parsedEvent(t, retryEvent),
			exit1(retryStderr(engineCommand)...))
		out["enginerun_failed_abort_"+tag] = c9StoppedRun(t, w,
			append([]any{hostDone, rollbackRunning, appAborted}, exit1(abortLines(12)...))...)
	}

	// install: the stale-volume screen lists volume names in plain ink.
	sv := c9Size(t, NewInstallModel("/opt/orbit", "v1"), 80)
	sv.state = installStateStaleVolume
	sv.staleVolumes = []deploy.DatabaseVolume{{Name: "orbit_pgdata", Project: "orbit"}, {Name: "mail_pgdata"}}
	out["install_stale_volume"] = sv.View().Content

	// #318: the refusal Install opens on when the machine's Orbit was
	// started from another folder; the title's lead is Install's own.
	of := c9Size(t, NewInstallModel("/opt/orbit", "v1"), 80)
	of.state = installStateOtherFolder
	of.otherFolder = "/srv/orbit-deployments/production"
	out["install_other_folder"] = of.View().Content

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

	c9OtherScreens(t, out)
	return out
}

// c9StoppedRun is an Install's engine run in a fixed fake folder, fed
// msgs as its stream delivers them, drawn at w x 30 on the screen it
// stops on. The clock is fixed so the screen is the same every pass.
func c9StoppedRun(t *testing.T, w int, msgs ...any) string {
	t.Helper()
	return c9StoppedRunAs(t, deploy.ActionInstall, engineRunSeams{}, w, msgs...)
}

// c9StoppedRunAs is c9StoppedRun for either action, with seams (the
// clock is always the fixed one).
func c9StoppedRunAs(t *testing.T, action deploy.Action, seams engineRunSeams, w int, msgs ...any) string {
	t.Helper()
	seams.now = func() time.Time { return time.Time{} }
	title := "Install — Standard"
	if action == deploy.ActionUpdate {
		title = "Update"
	}
	run := newEngineRun(action, rollbackFolder, title, "v1").withSeams(seams)
	run, _ = run.start(w, 30)
	run.state = runStreaming
	for _, msg := range msgs {
		run, _ = run.update(engineStreamMsg{msg: msg})
	}
	if run.state != runFailed {
		t.Fatalf("the run did not stop (state %v)", run.state)
	}
	return run.view(w, 30)
}

// c9Dir is a fake directory entry for the stopped screen's folder listing.
type c9Dir struct {
	name string
	dir  bool
}

func (e c9Dir) Name() string               { return e.name }
func (e c9Dir) IsDir() bool                { return e.dir }
func (e c9Dir) Type() fs.FileMode          { return 0 }
func (e c9Dir) Info() (fs.FileInfo, error) { return nil, errors.ErrUnsupported }

// c9OtherScreens renders every screen the cases above do not (#220),
// each at the widths the families above use.
func c9OtherScreens(t *testing.T, out map[string]string) {
	t.Helper()
	for _, w := range c9Widths {
		c9OtherScreensAt(t, out, w)
		c9SuccessScreens(t, out, w)
	}
	c9RemoveScreens(t, out)
}

// c9OtherScreensAt is c9OtherScreens at one width.
func c9OtherScreensAt(t *testing.T, out map[string]string, w int) {
	t.Helper()
	deployment := &deploy.Deployment{TargetDir: c9LongDir, AppURL: "https://mail.example.com", Image: "registry.example.com/orbit:1.2.3"}
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	fixed := func() time.Time { return base }
	tag := fmt.Sprintf("%d", w)

	// splash: the settled menu in each state the identity block has,
	// and the degraded drawer, and the update notice.
	splash := func(name string, mutate func(*SplashModel)) {
		m := NewSplashModelNoAnimation()
		m.version = "v1"
		mutate(&m)
		m = c9Size(t, m, w)
		out["splash_"+name+"_"+tag] = m.View().Content
	}
	known := func(m *SplashModel) {
		m.fqdn, m.appURL, m.state, m.menu.sel = "mail.example.com", "https://mail.example.com", stateUnknown, menuUpdate
		m.orbitVersion = "v1.2.0"
	}
	splash("dormant", func(*SplashModel) {})
	splash("unknown", known)
	splash("alive", func(m *SplashModel) { known(m); m.state = stateAlive })
	splash("degraded", func(m *SplashModel) {
		known(m)
		m.state, m.menu.sel = stateDegraded, menuRepair
		m.health = healthResultMsg{reason: deploy.HealthNotResponding}
	})
	splash("degraded_drawer", func(m *SplashModel) {
		known(m)
		m.state, m.onStatusRow, m.drawerOpen = stateDegraded, true, true
		m.health = healthResultMsg{reason: deploy.HealthNotResponding, running: 2, expected: 4, counted: true}
	})
	splash("unreadable", func(m *SplashModel) { m.state = stateUnreadable })
	splash("update_notice", func(m *SplashModel) { m.updateNotice = "v9.9.9" })

	// install: the profile choice's dead end, the profile screen
	// while the old-volume check is still answering, and the notice.
	un := c9Size(t, NewInstallModel("/opt/orbit", "v1"), w)
	un.state = installStateUnavailableProfile
	out["install_unavailable_profile_"+tag] = un.View().Content
	ck := c9Size(t, NewInstallModel("/opt/orbit", "v1"), w)
	ck.awaiting = installStandard
	out["install_profile_checking_"+tag] = ck.View().Content
	nt := c9Size(t, NewInstallModel("/opt/orbit", "v1"), w)
	nt.seams.now = fixed
	nt.state = installStateNotice
	nt.notice = newNotice(base, w, 30)
	out["install_notice_"+tag] = nt.View().Content

	// update: its two dead ends, as Remove's.
	uf := c9Size(t, NewUpdateModel(nil, c9LongDir, "v1"), w)
	out["update_notfound_"+tag] = uf.View().Content
	uu := c9Size(t, NewUpdateModel(nil, c9LongDir, "v1"), w)
	uu.detectErr = &fs.PathError{Op: "open", Path: c9LongDir + "/" + deploy.EnvFile, Err: syscall.EACCES}
	out["update_unreadable_"+tag] = uu.View().Content

	// remove: the working screen between confirm and done.
	sd := c9Size(t, NewRemoveModel(deployment), w)
	sd.state = removeStateStandingDown
	out["remove_standingdown_"+tag] = sd.View().Content

	// the engine run's other screens: stopping, the terminal
	// handoff, the sign-in choice and the configuration session.
	run := func() engineRun {
		r := newEngineRun("install", c9LongDir, "Install — Standard", "v1")
		r.console = newConsole("Install — Standard", "v1", fixed).setSize(w, 30)
		return r
	}
	r := run()
	r.state = runStopping
	out["enginerun_stopping_"+tag] = r.view(w, 30)
	r = run()
	r.state = runHandoffRunning
	out["enginerun_handoff_"+tag] = r.view(w, 30)
	r = run()
	r.state = runConfigSignInMode
	out["enginerun_signin_local_"+tag] = r.view(w, 30)
	r.cfg.signIn = newMenu(configSignInLabels()...)
	r.cfg.signIn.sel = len(configSignInLabels()) - 1
	out["enginerun_signin_last_"+tag] = r.view(w, 30)
	r = run()
	r.state = runConfigCollect
	out["enginerun_config_preparing_"+tag] = r.view(w, 30)
	p := engine.Prompt{Field: "APP_URL", Kind: "url", Attempt: 2, Required: true}
	r.cfg.origin = "https://orbit.example.com"
	r.cfg.session = promptSession{prompt: &p, reason: "that is not a web address", input: inputLine{runes: []rune("orbit.example.com")}}
	out["enginerun_config_prompt_"+tag] = r.view(w, 30)

	// the stopped screen's other endings (the cases above hold the
	// refused-before-writing, recovery and abort ones).
	install := func(msgs ...any) string { return c9StoppedRunAs(t, deploy.ActionInstall, engineRunSeams{}, w, msgs...) }
	update := func(msgs ...any) string { return c9StoppedRunAs(t, deploy.ActionUpdate, engineRunSeams{}, w, msgs...) }
	listing := engineRunSeams{listFolder: func(string) ([]os.DirEntry, error) {
		return []os.DirEntry{c9Dir{"notes.txt", false}, c9Dir{"old-site", true}, c9Dir{"a.txt", false}, c9Dir{"b.txt", false}, c9Dir{deploy.EnvFile, false}}, nil
	}}
	out["enginerun_stopped_folder_"+tag] = c9StoppedRunAs(t, deploy.ActionInstall, listing, w, hostRefused, exit1(notEmptyStderr...))
	out["enginerun_stopped_undone_"+tag] = install(hostDone, composeRunning, rollbackRunning, rollbackDone, composeFailed,
		exit1("Orbit installer: Orbit services could not be created or started."))
	out["enginerun_stopped_cannot_"+tag] = install(hostAborted, exit1("Orbit installer: orbit-install-backups is not a folder."))
	out["enginerun_stopped_notready_"+tag] = install(hostDone, appRepair, exit1("Orbit installer: The web service did not become ready in time."))
	out["enginerun_stopped_died_"+tag] = install(hostDone, composeRunning, exit1("Killed"))
	out["enginerun_stopped_saidnothing_"+tag] = install(engine.RawLineMsg{Text: "Pulling ghcr.io/tomlawesome/orbit:latest"}, exit1("Orbit installer: something went wrong."))
	out["enginerun_stopped_update_retry_"+tag] = update(hostDone, rollbackRunning, rollbackDone, imageRetry, exit1("Orbit installer: Could not pull the image."))
	out["enginerun_stopped_update_repair_"+tag] = update(hostDone, appRepair, exit1("Orbit installer: The web service did not become ready in time."))
	out["enginerun_stopped_update_abort_"+tag] = update(hostAborted, exit1("Orbit installer: Update requires a recognized existing Orbit deployment."))
	out["enginerun_stopped_update_nothing_"+tag] = update(exit1("Orbit installer: something went wrong."))
	cs := newEngineRun("install", c9LongDir, "Install — Standard", "v1")
	cs.console = newConsole("Install — Standard", "v1", fixed).setSize(w, 30)
	cs, _ = cs.update(engineReadyMsg{err: errors.New("fetch install.sh: connection refused")})
	out["enginerun_stopped_nostart_"+tag] = cs.view(w, 30)
	hr := run()
	hr.state, hr.handoffRefused = runFailed, true
	hr.runErr = errors.New("in-console configuration required — terminal handoff refused")
	out["enginerun_stopped_handoff_refused_"+tag] = hr.view(w, 30)
	he := run()
	he.state, he.handoffErr = runFailed, errors.New(c9LongErr)
	he.lastFailed = &engine.Event{Phase: "assets", Component: "fetch", State: "failed", Action: "abort"}
	he.sawEvent = true
	out["enginerun_stopped_handoff_error_"+tag] = he.view(w, 30)

	// repair: the working screens, the dead ends and the stop.
	rp := func() RepairModel { return c9Size(t, NewRepairModel("/opt/orbit", "v1"), w) }
	m := rp()
	out["repair_preparing_"+tag] = m.View().Content
	m.state = repairExecuting
	out["repair_executing_"+tag] = m.View().Content
	m = rp()
	m.state = repairUnavailable
	out["repair_unavailable_"+tag] = m.View().Content
	m = c9Size(t, NewRepairModel(c9LongDir, "v1"), w)
	m.state = repairNotFound
	out["repair_notfound_"+tag] = m.View().Content
	m = c9Size(t, NewRepairModel(c9LongDir, "v1"), w)
	m.state = repairForeign
	m.foreign = deploy.Ownership{Account: "orbit", Files: []deploy.ForeignFile{
		{Path: deploy.EnvFile, Owner: "root"},
		{Path: deploy.SecretsDir + "/db_password", Owner: "root"},
		{Path: deploy.SecretsDir + "/a/rather/long/nested/path/to/a/secret-file-that-is-long", Owner: "someone-else"},
	}}
	out["repair_foreign_"+tag] = m.View().Content
	m.copied = true
	out["repair_foreign_copied_"+tag] = m.View().Content
	m = rp()
	m.state, m.stoppedIn, m.runErr = repairError, repairExecuting, errors.New(c9LongErr)
	out["repair_error_executing_"+tag] = m.View().Content
	m.stoppedIn = repairRotating
	out["repair_error_rotating_"+tag] = m.View().Content

	// the credential rotation: asking, and backing out.
	m = rp()
	m.state = repairRotating
	rot := engine.Prompt{Field: "CHECKPOINT_PASSPHRASE", Kind: "secret", Attempt: 1, Required: true}
	m.rot = promptSession{prompt: &rot, input: inputLine{runes: []rune("hunter2")}}
	out["repair_rotating_"+tag] = m.View().Content
	m.rot = promptSession{}
	m.stream = &engine.Stream{}
	out["repair_rotating_stopping_"+tag] = m.View().Content

	// repair: the plan and the results at every width, and the
	// diagnosis in its other endings.
	m = rp()
	m.state = repairDiagnosis
	m.diagnosis = &engine.Diagnosis{Result: engine.ResultAttention, Checked: 12, Skipped: 1}
	m.findings = []engine.Finding{
		{Class: "secret-missing", Target: "compose-file", Severity: "fail"},
		{Class: "secret-stale", Target: "env-file", Severity: "warn"},
	}
	m.planActions = []engine.PlanAction{
		{Action: "fix-permissions", Resolves: "secret-missing", Mutation: "reversible", Backup: "not-required"},
		{Action: "manual", Resolves: "secret-stale", Mutation: "none", Backup: "required"},
	}
	m.manualNotes = []string{"manual step: Restore the secret from your own backup and keep it somewhere only you can read it. (resolves=secret-stale)"}
	m.planSummary = &engine.PlanSummary{Result: engine.ResultManualRequired, Actions: 2}
	m.exitCode = engine.ExitPlanAvailable
	out["repair_diagnosis_manual_"+tag] = m.View().Content
	m.serviceStates = func(string) ([]deploy.ServiceState, error) { return nil, nil }
	m.services = &serviceReading{loaded: true, expected: []string{"web", "db", "worker"}, states: []deploy.ServiceState{
		{Service: "web", State: deploy.ContainerRunning},
		{Service: "db", State: deploy.ContainerRunning, Health: deploy.HealthUnhealthy},
	}}
	out["repair_diagnosis_services_"+tag] = m.View().Content

	m = rp()
	m.state = repairDiagnosis
	m.diagnosis = &engine.Diagnosis{Result: engine.ResultHealthy, Checked: 12}
	m.exitCode = engine.ExitHealthy
	out["repair_diagnosis_clear_"+tag] = m.View().Content
	m = rp()
	m.state = repairDiagnosis
	m.findings = []engine.Finding{{Class: "secret-missing", Target: "compose-file", Severity: "fail"}}
	out["repair_diagnosis_problems_"+tag] = m.View().Content

	c9ExecutedScreens(t, out, w, rp)
	m = rp()
	m.state = repairExecuted
	m.execSummary = &engine.ExecutionSummary{Result: engine.ResultComplete}
	m.dangerous = &engine.Dangerous{Result: engine.ResultRefused, Reason: "non-interactive"}
	out["repair_executed_dangerous_refused_"+tag] = m.View().Content
}

// c9RemoveScreens is Remove's screens that need one width only: the root
// folder it will not build a removal for, and a stand-down that timed out.
func c9RemoveScreens(t *testing.T, out map[string]string) {
	t.Helper()
	// remove, once, at 80: the root folder it will not build a removal for,
	// and a stand-down that timed out.
	root := &deploy.Deployment{TargetDir: "/", AppURL: "https://mail.example.com", Image: "registry.example.com/orbit:1.2.3"}
	rc := c9Size(t, NewRemoveModel(root), 80)
	rc.installedAt = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	out["remove_confirm_root"] = rc.View().Content
	rc.state = removeStateDone
	rc.after = newMenu(removeDoneRows...)
	out["remove_done_root"] = rc.View().Content
	rt := c9Size(t, NewRemoveModel(&deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com", Image: "registry.example.com/orbit:1.2.3"}), 80)
	rt.state = removeStateFailed
	rt.after = newMenu(removeFailedRows...)
	rt.standDownErr = &deploy.StandDownTimeoutError{Limit: 5 * time.Minute, Words: []string{"docker", "compose", "--project-directory", "/opt/orbit", "down"}, Err: errors.New("signal: killed")}
	out["remove_failed_timeout"] = rt.View().Content
}

// c9ExecutedScreens is what a Repair execution ended as, at width w;
// rp makes the sized model.
func c9ExecutedScreens(t *testing.T, out map[string]string, w int, rp func() RepairModel) {
	t.Helper()
	tag := fmt.Sprintf("%d", w)
	for name, sum := range map[string]*engine.ExecutionSummary{
		"complete": {Result: engine.ResultComplete, Done: 2},
		"declined": {Result: engine.ResultDeclined},
		"refused":  {Result: engine.ResultRefused, Reason: "deployment-version-unsupported"},
		"empty":    {Result: engine.ResultEmpty},
	} {
		m := rp()
		m.state = repairExecuted
		m.execSummary = sum
		if name == "complete" {
			m.executeResults = []engine.ExecuteResult{{Action: "fix-permissions", Resolves: "secret-missing", Result: engine.ResultDone}}
			m.diagnosis = &engine.Diagnosis{Result: engine.ResultHealthy, Checked: 12}
		}
		out["repair_executed_"+name+"_"+tag] = m.View().Content
	}
}

// c9SuccessScreens is the success screen at width w: before the claim
// link has been read (the blank slot under Get into Orbit), with a claim
// link, after the read found none, and an address with an account part
// (hidden on screen). The sky is the shared fixed seed and the screen is
// not animated.
func c9SuccessScreens(t *testing.T, out map[string]string, w int) {
	t.Helper()
	tag := fmt.Sprintf("%d", w)
	build := func(appURL string, read bool) SuccessModel {
		m := NewSuccessModel(appURL, 3*time.Minute+42*time.Second, "v1")
		m.noAnimation = true
		if read {
			m.readClaim = func() (string, error) { return "", nil }
		}
		return c9Size(t, m, w)
	}
	const address = "https://mail.example.com"
	m := build(address, true)
	out["success_claim_pending_"+tag] = m.View().Content
	m, _ = updateAs(t, m, claimLinkMsg{link: address + "/#claim=Zm9yLXRlc3RzLW9ubHk"})
	out["success_claim_link_"+tag] = m.View().Content
	m = build(address, true)
	m, _ = updateAs(t, m, claimLinkMsg{})
	out["success_claim_none_"+tag] = m.View().Content
	m = build("https://admin:not-a-password@mail.example.com", false)
	out["success_account_hidden_"+tag] = m.View().Content
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
			t.Errorf("%s no longer renders as it did before #231 (screens must not move): %s", name, firstRowDiff(string(golden), got))
		}
	}
	for name := range screens {
		if !want[name] {
			t.Errorf("screen %s has no golden file", name)
		}
	}
}

// firstRowDiff names the first row where got differs from want, with
// that row from each side quoted, so a moved row is found at a glance
// rather than by reading two whole screens (#277).
func firstRowDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var wr, gr string
		if i < len(w) {
			wr = w[i]
		}
		if i < len(g) {
			gr = g[i]
		}
		if i >= len(w) || i >= len(g) || wr != gr {
			return fmt.Sprintf("row %d of %d (want) / %d (got) differs:\nwant %q\ngot  %q", i+1, len(w), len(g), wr, gr)
		}
	}
	return "the screens differ only in a way no row shows"
}
