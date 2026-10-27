package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// In-console guided configuration, driven by orbit's machine prompt
// protocol (docs/engine-events.md "Machine prompts (v0)", orbit#297).
// On the engine's configuration refusal the launcher opens the configuration
// tree the engine handed over (ORBIT_LAUNCHER_CONFIG_TREE, ai/orbit#1225;
// see deploy.BuildInstallCommand), seeds it from the target, and runs
// configure.sh's own guided flow with machine prompts — every answer
// validated by the engine's validators, never the launcher's guesses,
// and no value (least of all the secret) ever appearing in a protocol
// line. Nothing is downloaded (#190): an engine that handed over no
// tree, or one that doesn't speak the protocol (it fails fast with no
// prompt line), gets the same terminal handoff as before — capability
// detected by behaviour, never by version sniffing.

// configPlan is the prepared session: the configuration tree plus what
// configure.sh --check says still needs collecting. cleanup ends the
// session (clearing what it collected); the tree itself is the engine
// run's, released with the run's files.
type configPlan struct {
	configTree string
	cleanup    func()
	needInit   bool
	needSecret bool
	unfixable  []string
}

// configPlanMsg carries the prepared session or the reason there is
// none (which routes to the handoff fallback).
type configPlanMsg struct {
	plan configPlan
	err  error
}

// configStepMsg carries one started configure step.
type configStepMsg struct {
	stream *engine.Stream
	stdin  io.WriteCloser
	err    error
}

// configStreamMsg wraps one message from the configure stream.
type configStreamMsg struct{ msg any }

// configStreamEndedMsg reports a configure stream closing; stream says
// which step's, since each step has its own.
type configStreamEndedMsg struct {
	stream *engine.Stream
	reason error
}

// configStream is the configure steps' wrapping for the engine stream
// reader.
var configStream = streamWrap{
	msg: func(msg any) tea.Msg { return configStreamMsg{msg: msg} },
	ended: func(s *engine.Stream, reason error) tea.Msg {
		return configStreamEndedMsg{stream: s, reason: reason}
	},
}

// configAdoptedMsg reports the collected configuration having been kept
// for the next run's configuration tree (an update's: having landed in
// the target), or why it could not be.
type configAdoptedMsg struct{ err error }

// configRecheckMsg carries configure.sh --check's fresh answer, asked
// again once --init has landed (see recheckAfterInit).
type configRecheckMsg struct {
	check deploy.ConfigCheck
	err   error
}

// Seams so flow tests drive the whole session with fakes.
type (
	prepareConfigFunc func(ctx context.Context, targetDir, configTree string) configPlanMsg
	startConfigFunc   func(configTree string, step deploy.ConfigStep, mode deploy.AuthMode) (*engine.Stream, io.WriteCloser, error)
	adoptConfigFunc   func(configTree, targetDir string) error
	recheckConfigFunc func(ctx context.Context, configTree string) (deploy.ConfigCheck, error)
)

// configCollect is the live session state.
type configCollect struct {
	plan   configPlan
	step   deploy.ConfigStep
	stream *engine.Stream
	stdin  io.WriteCloser

	// session is the step's machine-prompt exchange; its sawPrompt is
	// the protocol capability evidence, per step.
	session promptSession
	// origin is the accepted APP_URL answer, kept so a later field's
	// guidance can name this deployment's real callback URL. Only ever
	// set from a non-secret field.
	origin string

	// signIn is the highlight on the sign-in-mode screen (issue #154),
	// over configSignInLabels; Local accounts, first, is the default.
	signIn menu
	// authMode is the answer, once chosen, threaded into BuildConfigureCommand
	// for the --init step it gates. Empty until the person chooses.
	authMode deploy.AuthMode

	// late is shared with the prepare and start commands still running.
	late *configLate

	// ctx is the session's lifetime: the configure.sh --check runs it
	// starts are cancelled by close, so leaving stops them.
	ctx    context.Context
	cancel context.CancelFunc
}

// configLate settles who releases what a prepare or start command
// produced. Leaving the session can happen while the command runs, and
// in the app its result then reaches the main menu, not this run, so a
// result made after leaving is released by the command itself, and one
// handed over but not yet taken is released by the leaving.
//
// The preparation also writes into the run's configuration tree (the
// import), so the tree must not be removed under it: a removal racing
// the copy can leave an empty folder in TMPDIR that nothing removes
// (#277). Leaving cancels the preparation (close cancels its context,
// which the import checks before every file), and while it runs the
// run's release of its files waits here; the preparation runs it once
// it has returned, so nothing is written into the tree after it is
// removed.
type configLate struct {
	mu      sync.Mutex
	stopped bool
	held    func()

	preparing bool
	// ended is closed once the running preparation has ended and the
	// release that waited for it, if any, has finished.
	ended     chan struct{}
	runFiles  func() // the run's files' release, waiting for it
	releasing bool   // end is running runFiles
}

// begin is a preparation starting. False means the session was left
// before it started: there is nothing to prepare for.
func (l *configLate) begin() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return false
	}
	l.preparing = true
	l.ended = make(chan struct{})
	return true
}

// end is the preparation finished with the tree: a release of the run's
// files that waited for it runs now, and only once it has finished is
// anyone waiting on the preparation let go, so nothing quits in the
// middle of the removal.
func (l *configLate) end() {
	l.mu.Lock()
	release := l.runFiles
	l.runFiles, l.preparing, l.releasing = nil, false, release != nil
	l.mu.Unlock()
	if release != nil {
		release()
	}
	close(l.ended)
	deferredReleases.drop(l)
}

// releaseRunFilesAfter keeps release for the running preparation to run
// once it ends, reporting whether it did; false means none is running
// and the caller releases now.
func (l *configLate) releaseRunFilesAfter(release func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.preparing {
		return false
	}
	l.runFiles = release
	deferredReleases.add(l)
	return true
}

// awaitPreparation returns once a preparation that has started has
// ended and the release that waited for it has finished, waiting at
// most limit for the preparation itself. Past the limit, a release
// still waiting runs here instead, and is finished before this returns:
// the launcher is quitting and nothing else would run it. A release the
// preparation has already begun is waited for to its end; a preparation
// with no release waiting is not waited for past the limit.
func (l *configLate) awaitPreparation(limit time.Duration) {
	l.mu.Lock()
	ended := l.ended
	l.mu.Unlock()
	if ended == nil {
		return
	}
	select {
	case <-ended:
		return
	case <-time.After(limit):
	}
	l.mu.Lock()
	release, releasing := l.runFiles, l.releasing
	l.runFiles = nil
	l.mu.Unlock()
	switch {
	case release != nil:
		release()
		deferredReleases.drop(l)
	case releasing:
		<-ended
	}
}

// deferredReleases are the sessions whose preparation still holds a
// release of the run's files. Whatever screen ends the launcher, the
// process must not exit before they are done (FinishDeferredCleanup):
// a session left with Esc during the import is no longer on any screen
// when the menu quits.
var deferredReleases releaseSet

type releaseSet struct {
	mu   sync.Mutex
	held map[*configLate]struct{}
}

func (s *releaseSet) add(l *configLate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = map[*configLate]struct{}{}
	}
	s.held[l] = struct{}{}
}

func (s *releaseSet) drop(l *configLate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, l)
}

func (s *releaseSet) list() []*configLate {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ls []*configLate
	for l := range s.held {
		ls = append(ls, l)
	}
	return ls
}

// deferredCleanupWait bounds how long the launcher's exit waits for a
// configuration preparation still writing into a run's configuration
// tree. It is a backstop only: leaving the session cancels the
// preparation's context, its import stops within one file and its
// configure.sh --check is stopped, so the wait is normally that one
// file.
const deferredCleanupWait = 2 * time.Second

// FinishDeferredCleanup is for the launcher's exit, once the program has
// ended: it waits, at most deferredCleanupWait in all, for every removal
// of a run's files that is waiting on a configuration preparation, and
// past that runs the ones still waiting itself; it returns once all have
// finished (the pattern of a graceful shutdown draining in-flight work
// before the process ends).
func FinishDeferredCleanup() {
	deadline := time.Now().Add(deferredCleanupWait)
	for _, l := range deferredReleases.list() {
		l.awaitPreparation(time.Until(deadline))
	}
	// The session's guided answers go with it (collectedConfig).
	releaseCollectedTrees()
}

// hand is a command's result arriving; release frees it. It reports
// whether the session is still there to take it.
func (l *configLate) hand(release func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		release()
		return false
	}
	l.held = release
	return true
}

// take is the session receiving a handed result: it owns it from now
// on. False means the result was already released.
func (l *configLate) take() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		return false
	}
	l.held = nil
	return true
}

// stop ends the session for every command result still to come.
func (l *configLate) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	if l.held != nil {
		l.held()
		l.held = nil
	}
}

// close releases the session's process and what it collected, and
// whatever a command still running for it goes on to produce.
func (c *configCollect) close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.late != nil {
		c.late.stop()
	}
	if c.stream != nil {
		c.stream.Kill()
		c.stream = nil
	}
	if c.stdin != nil {
		c.stdin.Close()
		c.stdin = nil
	}
	if c.plan.cleanup != nil {
		c.plan.cleanup()
		c.plan.cleanup = nil
	}
}

// defaultPrepareConfig opens the configuration tree the engine handed over
// and plans the session there. No tree (an install.sh without
// ai/orbit#1225) is an error, which routes to the terminal handoff.
func defaultPrepareConfig(ctx context.Context, targetDir, configTree string) configPlanMsg {
	cleanup, err := deploy.OpenConfigTree(configTree)
	if err != nil {
		return configPlanMsg{err: err}
	}
	if err := deploy.ImportTargetConfigContext(ctx, configTree, targetDir); err != nil {
		cleanup()
		return configPlanMsg{err: err}
	}
	check, err := deploy.RunConfigCheck(ctx, configTree)
	if err != nil {
		cleanup()
		return configPlanMsg{err: err}
	}
	return configPlanMsg{plan: configPlan{
		configTree: configTree,
		cleanup:    cleanup,
		needInit:   check.NeedsInit(),
		needSecret: check.NeedsSecret(),
		unfixable:  check.Unfixable(),
	}}
}

func defaultStartConfig(configTree string, step deploy.ConfigStep, mode deploy.AuthMode) (*engine.Stream, io.WriteCloser, error) {
	return engine.StartInteractive(deploy.BuildConfigureCommand(configTree, step, mode))
}

// beginConfigCollect starts the in-console path: prepare the session in
// the background while the screen shows the working state.
func (r engineRun) beginConfigCollect() (engineRun, tea.Cmd) {
	r.state = runConfigCollect
	late := &configLate{}
	ctx, cancel := context.WithCancel(context.Background())
	r.cfg = configCollect{late: late, ctx: ctx, cancel: cancel}
	prepare := r.prepareConfig
	if prepare == nil {
		prepare = defaultPrepareConfig
	}
	targetDir, configTree := r.targetDir, r.configTree
	return r, func() tea.Msg {
		if !late.begin() {
			return nil
		}
		msg := prepare(ctx, targetDir, configTree)
		handed := late.hand(func() { releaseConfigMsg(msg) })
		late.end()
		if !handed {
			return nil
		}
		return msg
	}
}

// releaseConfigMsg frees what a prepare or start command produced.
func releaseConfigMsg(msg tea.Msg) {
	switch msg := msg.(type) {
	case configStepMsg:
		if msg.stream != nil {
			msg.stream.Kill()
		}
		if msg.stdin != nil {
			msg.stdin.Close()
		}
	case configPlanMsg:
		if msg.plan.cleanup != nil {
			msg.plan.cleanup()
		}
	}
}

// The four ways in-console collection can be abandoned, in fixed
// phrases. Deliberately not built from the underlying error: it carries
// local paths, and configure.sh's output carries answers. Neither
// belongs in a log CI keeps as an artifact (see logDiag).
const (
	fallbackNoTree       = "the engine handed over no configuration tree"
	fallbackUnfixable    = "the configuration needs fields this console can't ask for"
	fallbackStepFailed   = "the configuration step would not start"
	fallbackLegacyEngine = "the engine never spoke the machine prompt protocol"
)

// fallbackToHandoff records why in-console collection was abandoned,
// then routes on: normally into the terminal handoff, or — under
// ORBIT_LAUNCHER_REQUIRE_IN_CONSOLE_CONFIG — to a hard stop, which
// beginHandoff decides.
func (r engineRun) fallbackToHandoff(reason string) (engineRun, tea.Cmd) {
	logDiag("in-console configuration unavailable: " + reason)
	return r.beginHandoff()
}

// handleConfigMsg advances the session. Any failure that isn't the
// person cancelling falls back to the terminal handoff — the engine's
// own interactive flow remains the path that always works.
func (r engineRun) handleConfigMsg(msg tea.Msg) (engineRun, tea.Cmd) {
	if r.state != runConfigCollect {
		// A cancelled session's process can still deliver its queued
		// messages; they belong to nothing now. A process or plan that
		// was still being started when the session was left is stopped
		// or cleaned up here, unless leaving already did.
		switch msg.(type) {
		case configStepMsg, configPlanMsg:
			if r.cfg.late == nil || r.cfg.late.take() {
				releaseConfigMsg(msg)
			}
		}
		return r, nil
	}
	switch msg.(type) {
	case configStepMsg, configPlanMsg:
		if r.cfg.late != nil && !r.cfg.late.take() {
			// Released already: it belongs to a session left before.
			return r, nil
		}
	}
	switch msg := msg.(type) {
	case configPlanMsg:
		var untrusted *deploy.UntrustedPathError
		if errors.As(msg.err, &untrusted) {
			// The tree is on a path the launcher does not trust (#191):
			// a refusal, naming the path and the reason, never a quiet
			// fallback. The failure screen still offers the guided
			// installer as the person's own choice.
			r.cfg.close()
			r.releaseRunFiles()
			r.lastFailed = nil
			r.stderrTail = nil
			r.runErr = msg.err
			r.state = runFailed
			r.menu = menu{}
			return r, nil
		}
		if msg.err != nil || len(msg.plan.unfixable) > 0 {
			// Can't collect here (no tree was handed over, or fields
			// beyond the protocol's vocabulary are missing) — the
			// guided installer in the real terminal can.
			reason := fallbackNoTree
			if msg.err == nil {
				// How many, not which: the names come from the tree's
				// own --check output, which is never logged. The count
				// is enough to tell this apart from a missing tree.
				reason = fmt.Sprintf("%s (%d)", fallbackUnfixable, len(msg.plan.unfixable))
			}
			// Stored first so close runs the plan's cleanup (#266).
			r.cfg.plan = msg.plan
			r.cfg.close()
			return r.fallbackToHandoff(reason)
		}
		r.cfg.plan = msg.plan
		if !msg.plan.needInit && !msg.plan.needSecret {
			// Nothing left to collect (a prior session already
			// produced a complete configuration): adopt and retry.
			return r.adoptAndRetry()
		}
		if msg.plan.needInit {
			// Issue #154: before the first --init, ask how people will
			// sign in. Local accounts is the default; choosing either
			// row starts --init with that mode (handleConfigSignInModeKey).
			r.state = runConfigSignInMode
			r.cfg.signIn = menu{}
			return r, nil
		}
		return r.startNextConfigStep()

	case configStepMsg:
		if msg.err != nil {
			r.cfg.close()
			return r.fallbackToHandoff(fallbackStepFailed)
		}
		r.cfg.stream = msg.stream
		r.cfg.stdin = msg.stdin
		r.cfg.session = promptSession{}
		return r, readEngineStream(r.send, msg.stream, configStream)

	case configStreamMsg:
		return r.handleConfigStream(msg.msg)

	case configStreamEndedMsg:
		if msg.stream != r.cfg.stream {
			// A finished step's stream closing behind its DoneMsg,
			// which already moved the session on.
			return r, nil
		}
		// The step's stream closed before its DoneMsg: the engine
		// stopped without saying how the step went, so the install
		// stops here, visibly, rather than waiting on a prompt that
		// will never come.
		logDiag("configuration stream: closed before the step finished")
		r.cfg.close()
		r.releaseRunFiles()
		r.lastFailed = nil
		r.stderrTail = nil
		r.runErr = msg.reason
		if r.runErr == nil {
			r.runErr = errEngineStoppedEarly
		}
		r.state = runFailed
		r.menu = menu{}
		return r, nil

	case configRecheckMsg:
		if msg.err != nil {
			// Same honest fallback as the first check: can't tell
			// what's still owed, so the terminal handoff decides
			// instead of guessing.
			r.cfg.close()
			return r.beginHandoff()
		}
		r.cfg.plan.needSecret = msg.check.NeedsSecret()
		return r.startNextConfigStep()

	case configAdoptedMsg:
		if msg.err != nil {
			r.cfg.close()
			r.releaseRunFiles()
			r.lastFailed = nil
			r.stderrTail = nil
			r.runErr = msg.err
			r.state = runFailed
			r.menu = menu{}
			return r, nil
		}
		r.cfg.close()
		// One retry per run after configuration (see handleStream).
		r.retriedAfterConfig = true
		return r.retryEngine()
	}
	return r, nil
}

// startNextConfigStep launches --init or --set-oidc-secret, whichever
// is still owed.
func (r engineRun) startNextConfigStep() (engineRun, tea.Cmd) {
	var step deploy.ConfigStep
	switch {
	case r.cfg.plan.needInit:
		step = deploy.ConfigStepInit
	case r.cfg.plan.needSecret:
		step = deploy.ConfigStepSecret
	default:
		return r.adoptAndRetry()
	}
	r.cfg.step = step
	start := r.startConfig
	if start == nil {
		start = defaultStartConfig
	}
	configTree, mode, late := r.cfg.plan.configTree, r.cfg.authMode, r.cfg.late
	return r, func() tea.Msg {
		stream, stdin, err := start(configTree, step, mode)
		msg := configStepMsg{stream: stream, stdin: stdin, err: err}
		if late != nil && !late.hand(func() { releaseConfigMsg(msg) }) {
			return nil
		}
		return msg
	}
}

// recheckAfterInit re-runs configure.sh --check once --init has
// landed. M7 leaves OIDC off by default, so the check made before
// --init can find the secret "not in use" even though --init is
// about to turn OIDC on; whether the secret is still owed is only
// knowable after --init actually runs, never assumed either way from
// the plan made before it.
func (r engineRun) recheckAfterInit() (engineRun, tea.Cmd) {
	recheck := r.recheckConfig
	if recheck == nil {
		recheck = deploy.RunConfigCheck
	}
	configTree, ctx := r.cfg.plan.configTree, r.cfg.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return r, func() tea.Msg {
		check, err := recheck(ctx, configTree)
		return configRecheckMsg{check: check, err: err}
	}
}

func (r engineRun) handleConfigStream(msg any) (engineRun, tea.Cmd) {
	switch m := msg.(type) {
	case engine.RawLineMsg:
		// A prompt-abort exits through the engine's refusal path;
		// DoneMsg routes to the failure screen.
		r.cfg.session, _ = r.cfg.session.applyLine(m.Text)
		return r, nil

	case engine.EventMsg:
		// configure.sh emits no phase events; tolerate and move on.
		return r, nil

	case engine.DoneMsg:
		sawPrompt := r.cfg.session.sawPrompt
		if m.Err == nil {
			// Step complete; mark it off and continue.
			r.cfg.stream = nil
			r.cfg.stdin = nil
			if r.cfg.step == deploy.ConfigStepInit {
				r.cfg.plan.needInit = false
				return r.recheckAfterInit()
			}
			r.cfg.plan.needSecret = false
			return r.startNextConfigStep()
		}
		r.cfg.close()
		if !sawPrompt {
			// Exited without ever speaking the protocol: a legacy
			// configure.sh. The terminal handoff is the honest path.
			return r.fallbackToHandoff(fallbackLegacyEngine)
		}
		// The engine gave up (rejections exhausted or input closed):
		// the failure screen says on which field. configure.sh's own
		// output can carry answers, so none of it is ever shown.
		r.releaseRunFiles()
		r.lastFailed = nil
		r.stderrTail = nil
		r.runErr = shownText(r.cfg.session.giveUpWords(r.cfg.origin))
		r.state = runFailed
		r.menu = menu{}
		return r, nil
	}
	return r, nil
}

// adoptAndRetry keeps the collected configuration for the launcher
// session, then re-runs the engine with it handed in. Nothing is written
// into the deployment folder: the configuration tree is two-way, so the
// session's .env-orbit and .orbit-secrets/ are copied into a private
// tree of their own (collectedConfig) and the retry's fresh tree is
// seeded from it; on a fresh install the engine writes them through its
// own install transaction, and its rollbacks take them out again (orbit
// docs/engine-events.md, Non-interactive contract, #1519). That is what
// makes the stopped screen's "nothing was changed" true.
//
// An update is the exception the contract names: its installer ignores
// a handed-in configuration and uses the folder's own, so an update's
// answers are still adopted into the folder, as before.
func (r engineRun) adoptAndRetry() (engineRun, tea.Cmd) {
	adopt := r.adoptConfig
	if adopt == nil {
		adopt = deploy.AdoptConfig
	}
	configTree := r.cfg.plan.configTree
	if r.action == deploy.ActionUpdate {
		targetDir := r.targetDir
		return r, func() tea.Msg {
			return configAdoptedMsg{err: adopt(configTree, targetDir)}
		}
	}
	if r.collected == nil {
		// A run outside an InstallModel keeps its own.
		r.collected = &collectedConfig{}
	}
	collected := r.collected
	return r, func() tea.Msg {
		return configAdoptedMsg{err: collected.keep(configTree, adopt)}
	}
}

// retryEngine starts a fresh engine run against the now-provisioned
// target: fresh console, fresh clock, fresh outcome evidence.
func (r engineRun) retryEngine() (engineRun, tea.Cmd) {
	// The retry is a new engine run with its own configuration tree.
	r.releaseRunFiles()
	r.configRefused = false
	r.waiting, r.completed, r.completion = nil, false, nil
	r.forgetStop()
	r.runErr = nil
	r.handoffErr = nil
	r.handoffByHand = false
	r.menu = menu{}
	return r.start(r.width, r.height)
}

// cancelConfigToMenu is Esc anywhere in the session: the configuration
// process is stopped, the run's files released, and the run left for
// the main menu. runLeft takes it out of runConfigCollect, so whatever
// the stopped process still delivers belongs to nothing.
func (r engineRun) cancelConfigToMenu() (engineRun, tea.Cmd) {
	r.cfg.close()
	r.releaseRunFiles()
	r.state = runLeft
	r.Done = true
	r.WantsMenu = true
	return r, nil
}

// handleConfigSignInModeKey drives the sign-in-mode screen (issue #154):
// up/down move the highlight between the two rows, enter chooses it and
// starts --init with that mode, esc cancels the whole session back to
// the main menu — the same cancel handleConfigKey gives the rest of
// this session.
func (r engineRun) handleConfigSignInModeKey(msg tea.KeyPressMsg) (engineRun, tea.Cmd) {
	if msg.Code == tea.KeyEsc {
		return r.cancelConfigToMenu()
	}
	var chosen string
	r.cfg.signIn, chosen = r.cfg.signIn.over(configSignInLabels()).update(msg)
	if chosen == "" {
		return r, nil
	}
	r.cfg.authMode = deploy.AuthModeLocal
	if chosen == signInSSO {
		r.cfg.authMode = deploy.AuthModeOIDC
	}
	r.state = runConfigCollect
	return r.startNextConfigStep()
}

// handleConfigKey is the typing surface (promptSession.key: typing,
// and Enter submits the answer line); esc cancels the whole session
// back to the main menu.
func (r engineRun) handleConfigKey(msg tea.KeyPressMsg) (engineRun, tea.Cmd) {
	if msg.Code == tea.KeyEsc {
		return r.cancelConfigToMenu()
	}
	var field, answer string
	r.cfg.session, field, answer = r.cfg.session.key(msg, r.cfg.stdin)
	if field == "APP_URL" && r.cfg.stdin != nil {
		r.cfg.origin = answer
	}
	return r, nil
}

// promptWords maps protocol field names to the same human labels
// install.sh's own TTY prompts use, plus the guidance a person actually
// needs to answer at that moment. An unknown field renders its protocol
// name, control codes shown as text — honest, never guessed at.
//
// notes carry worked examples, because naming a field is not the same as
// telling someone where to find its value: "your identity provider's
// https:// issuer" is true and still leaves an administrator staring at
// an empty line, which is exactly what happened on the first real test.
// The issuer examples are the two common self-hosted providers, shown in
// full so the shape — including Authentik's trailing slash, which its
// discovery document requires and which is easy to drop — is visible
// rather than described. origin is the answer already given for
// APP_URL, so the client-ID note can name this deployment's real
// callback URL instead of a placeholder; it is empty when unknown.
func promptWords(field, origin string) (label, hint string, notes []string) {
	switch field {
	case "APP_URL":
		return "Public Orbit origin", "the https:// address Orbit will live at", []string{
			"e.g. https://orbit.example.com",
		}
	case "OIDC_ISSUER":
		return "OIDC issuer URL", "must serve /.well-known/openid-configuration", []string{
			"Authentik  https://sso.example.com/application/o/orbit/",
			"Keycloak   https://sso.example.com/realms/orbit",
		}
	case "OIDC_CLIENT_ID":
		return "OIDC client ID", "from your identity provider's app registration", callbackNote(origin)
	case "OIDC_CLIENT_SECRET":
		return "OIDC client secret", "input hidden", []string{
			"issued with the client ID — not your account password",
		}
	// repair.sh --execute's own prompt fields (orbit#261 slice 4),
	// same grammar, same rendering.
	case "action-word":
		return "The action word", "type rotate to proceed — anything else cancels", nil
	case "checkpoint-passphrase":
		return "Checkpoint passphrase", "input hidden — protects the pre-rotation backup", nil
	case "checkpoint-passphrase-confirm":
		return "Confirm the passphrase", "input hidden", nil
	case "safe-batch":
		return "Run the safe repairs?", "y to proceed", nil
	default:
		// The engine's own word: drawn, never obeyed.
		return visibleText(field), "", nil
	}
}

// callbackNote names the redirect URI this deployment will actually sign
// in through, which is the value the identity provider's registration
// needs and the one most often got wrong. origin is the answer already
// given for APP_URL. The note is two rows: what to do, then the address
// alone, so it can be copied whole. When the origin is missing or the
// address is wider than a prose row on an 80-cell screen, the note falls
// back to the shape rather than a wrapped address someone might paste.
func callbackNote(origin string) []string {
	const callbackPath = "/api/auth/callback"
	generic := []string{"register <your Orbit URL>" + callbackPath, "as the redirect URI in your provider"}
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" || !strings.HasPrefix(origin, "https://") {
		return generic
	}
	address := origin + callbackPath
	if len(address) > measure(80, errorWidth) {
		return generic
	}
	return []string{"Register the redirect URI with your provider", address}
}

// rejectionWords maps the engine's rejection reason classes to honest
// words. Unknown classes render as given, control codes shown as text.
func rejectionWords(reason string) string {
	switch reason {
	case "empty":
		return "can't be empty"
	case "invalid-characters":
		return "contains whitespace or control characters"
	case "not-https":
		return "must start with https://"
	case "not-absolute-url":
		return "isn't a plain https:// address"
	case "forbidden-host":
		return "loopback and placeholder hosts aren't allowed"
	case "too-large":
		return "too large"
	default:
		// The engine's own word: drawn, never obeyed.
		return visibleText(reason)
	}
}

const (
	signInLocal = "Local accounts"
	signInSSO   = "Single sign-on (SSO) with an identity provider"
)

// configSignInLabels are the sign-in-mode screen's rows as the menu
// knows them: configSignInRows' labels, in order.
func configSignInLabels() []string {
	labels := make([]string, len(configSignInRows))
	for i, row := range configSignInRows {
		labels[i] = row.label
	}
	return labels
}

// configSignInRows is the sign-in-mode screen's copy, ratified 2026-09-09
// on issue #154 and reproduced here exactly — not to be reworded without
// a fresh ratification. Local accounts is first (and the default); both
// rows are otherwise shown as equals.
var configSignInRows = []struct {
	label string
	desc  []string
}{
	{signInLocal, []string{
		"People get a password managed by Orbit. Nothing else to set up.",
		"A provider can be added later with configure.sh.",
	}},
	{signInSSO, []string{
		"People sign in through your OIDC provider — Authentik, Keycloak,",
		"Entra ID and the like. Orbit asks for its address and client",
		"details next. Local accounts stay available as well.",
	}},
}

// viewConfigSignInMode renders the sign-in-mode screen (issue #154):
// the question --init is about to be answered for, asked before --init
// ever runs so the right ORBIT_CONFIGURE_AUTH_MODE can be set on it.
func (r engineRun) viewConfigSignInMode(width, height int) string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "How will people sign in?")

	// Unlike menuRow's individually centred labels, the two choices and
	// their descriptions are one left-aligned block, as ratified: every
	// line is padded to the block's width so skyBlock's per-line centring
	// moves the block as a whole and the descriptions stay under their
	// label.
	var block []string
	for i, row := range configSignInRows {
		label := "  " + style.MenuUnselected.Render(row.label)
		if i == r.cfg.signIn.sel {
			label = style.MenuCaret.Render(style.SymbolSelected) + " " + style.MenuSelected.Render(row.label)
		}
		block = append(block, label)
		for _, line := range row.desc {
			block = append(block, style.MutedText.Render("    "+line))
		}
		block = append(block, "")
	}
	blockWidth := 0
	for _, line := range block {
		blockWidth = max(blockWidth, lipgloss.Width(line))
	}
	for _, line := range block {
		fmt.Fprintln(&b, line+strings.Repeat(" ", blockWidth-lipgloss.Width(line)))
	}
	return skyBlock(r.console.sky, width, height, b.String())
}

// viewConfigCollect renders the session: preparing, or one field being
// asked for.
func (r engineRun) viewConfigCollect(width, height int) string {
	var b strings.Builder
	// Amber, as on the stop before it: the person is still inside that
	// stop (#229).
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark), "Orbit needs your configuration")

	r.cfg.session.view(&b, r.cfg.origin, width, r.console.spinner)
	return skyBlock(r.console.sky, width, height, b.String())
}
