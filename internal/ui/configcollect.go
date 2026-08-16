package ui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// In-console guided configuration, driven by orbit's machine prompt
// protocol (docs/engine-events.md "Machine prompts (v0)", orbit#297).
// On the engine's configuration refusal the launcher stages a config
// tree (deploy.FetchConfigTree), seeds it from the target, and runs
// configure.sh's own guided flow with machine prompts — every answer
// validated by the engine's validators, never the launcher's guesses,
// and no value (least of all the secret) ever appearing in a protocol
// line. A legacy engine that doesn't speak the protocol fails fast
// with no prompt line, and the flow falls back to the same terminal
// handoff as before — capability detected by behaviour, never by
// version sniffing.

// configPlan is the prepared session: the staged tree plus what
// configure.sh --check says still needs collecting.
type configPlan struct {
	treeDir    string
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

// configAdoptedMsg reports the collected configuration landing in the
// target.
type configAdoptedMsg struct{ err error }

// Seams so flow tests drive the whole session with fakes.
type (
	prepareConfigFunc func(ctx context.Context, targetDir string) configPlanMsg
	startConfigFunc   func(treeDir string, step deploy.ConfigStep) (*engine.Stream, io.WriteCloser, error)
	adoptConfigFunc   func(treeDir, targetDir string) error
)

// configCollect is the live session state.
type configCollect struct {
	plan   configPlan
	step   deploy.ConfigStep
	stream *engine.Stream
	stdin  io.WriteCloser

	prompt    *engine.Prompt // non-nil while one answer is being typed
	reason    string         // honest words for the last rejection
	sawPrompt bool           // protocol capability evidence, per step
	input     []rune
	// origin is the accepted APP_URL answer, kept so a later field's
	// guidance can name this deployment's real callback URL. Only ever
	// set from a non-secret field.
	origin string
}

// close releases the session's process and staged tree.
func (c *configCollect) close() {
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

func defaultPrepareConfig(ctx context.Context, targetDir string) configPlanMsg {
	treeDir, cleanup, err := deploy.FetchConfigTree(ctx)
	if err != nil {
		return configPlanMsg{err: err}
	}
	if err := deploy.ImportTargetConfig(treeDir, targetDir); err != nil {
		cleanup()
		return configPlanMsg{err: err}
	}
	check, err := deploy.RunConfigCheck(ctx, treeDir)
	if err != nil {
		cleanup()
		return configPlanMsg{err: err}
	}
	return configPlanMsg{plan: configPlan{
		treeDir:    treeDir,
		cleanup:    cleanup,
		needInit:   check.NeedsInit(),
		needSecret: check.NeedsSecret(),
		unfixable:  check.Unfixable(),
	}}
}

func defaultStartConfig(treeDir string, step deploy.ConfigStep) (*engine.Stream, io.WriteCloser, error) {
	return engine.StartInteractive(deploy.BuildConfigureCommand(treeDir, step))
}

// beginConfigCollect starts the in-console path: prepare the session in
// the background while the screen shows the working state.
func (r engineRun) beginConfigCollect() (engineRun, tea.Cmd) {
	r.state = runConfigCollect
	r.cfg = configCollect{}
	prepare := r.prepareConfig
	if prepare == nil {
		prepare = defaultPrepareConfig
	}
	targetDir := r.targetDir
	return r, func() tea.Msg {
		return prepare(context.Background(), targetDir)
	}
}

// pumpConfig delivers the next configure-stream message.
func pumpConfig(s *engine.Stream) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-s.C
		if !ok {
			return nil
		}
		return configStreamMsg{msg: msg}
	}
}

// handleConfigMsg advances the session. Any failure that isn't the
// person cancelling falls back to the terminal handoff — the engine's
// own interactive flow remains the path that always works.
func (r engineRun) handleConfigMsg(msg tea.Msg) (engineRun, tea.Cmd) {
	if r.state != runConfigCollect {
		// A cancelled session's process can still deliver its queued
		// messages; they belong to nothing now.
		return r, nil
	}
	switch msg := msg.(type) {
	case configPlanMsg:
		if msg.err != nil || len(msg.plan.unfixable) > 0 {
			// Can't collect here (fetch failed, or fields beyond the
			// protocol's vocabulary are missing) — the guided installer
			// in the real terminal can.
			r.cfg.close()
			return r.beginHandoff()
		}
		r.cfg.plan = msg.plan
		if !msg.plan.needInit && !msg.plan.needSecret {
			// Nothing left to collect (a prior session already
			// produced a complete configuration): adopt and retry.
			return r.adoptAndRetry()
		}
		return r.startNextConfigStep()

	case configStepMsg:
		if msg.err != nil {
			r.cfg.close()
			return r.beginHandoff()
		}
		r.cfg.stream = msg.stream
		r.cfg.stdin = msg.stdin
		r.cfg.sawPrompt = false
		r.cfg.prompt = nil
		r.cfg.reason = ""
		return r, pumpConfig(msg.stream)

	case configStreamMsg:
		return r.handleConfigStream(msg.msg)

	case configAdoptedMsg:
		if msg.err != nil {
			r.cfg.close()
			r.runErr = msg.err
			r.state = runFailed
			r.menuSel = 0
			return r, nil
		}
		r.cfg.close()
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
	treeDir := r.cfg.plan.treeDir
	return r, func() tea.Msg {
		stream, stdin, err := start(treeDir, step)
		return configStepMsg{stream: stream, stdin: stdin, err: err}
	}
}

func (r engineRun) handleConfigStream(msg any) (engineRun, tea.Cmd) {
	switch m := msg.(type) {
	case engine.RawLineMsg:
		if parsed, ok := engine.ParsePromptLine(m.Text); ok {
			switch p := parsed.(type) {
			case engine.Prompt:
				prompt := p
				r.cfg.prompt = &prompt
				r.cfg.sawPrompt = true
				r.cfg.input = nil
			case engine.PromptReject:
				r.cfg.reason = rejectionWords(p.Reason)
				r.cfg.prompt = nil
			case engine.PromptAccept:
				r.cfg.reason = ""
				r.cfg.prompt = nil
			case engine.PromptAbort:
				// The engine gives up (third rejection or closed
				// input) and exits through its refusal path; DoneMsg
				// routes back to the refusal menu.
				r.cfg.prompt = nil
			}
		}
		return r, pumpConfig(r.cfg.stream)

	case engine.EventMsg:
		// configure.sh emits no phase events; tolerate and move on.
		return r, pumpConfig(r.cfg.stream)

	case engine.DoneMsg:
		sawPrompt := r.cfg.sawPrompt
		if m.Err == nil {
			// Step complete; mark it off and continue.
			if r.cfg.step == deploy.ConfigStepInit {
				r.cfg.plan.needInit = false
			} else {
				r.cfg.plan.needSecret = false
			}
			r.cfg.stream = nil
			r.cfg.stdin = nil
			return r.startNextConfigStep()
		}
		r.cfg.close()
		if !sawPrompt {
			// Exited without ever speaking the protocol: a legacy
			// configure.sh. The terminal handoff is the honest path.
			return r.beginHandoff()
		}
		// The engine aborted (rejections exhausted or input closed) —
		// back to the refusal menu; the person decides what's next.
		r.state = runConfigPrompt
		r.menuSel = 0
		return r, nil
	}
	return r, nil
}

// adoptAndRetry lands the collected configuration in the target, then
// re-runs the engine.
func (r engineRun) adoptAndRetry() (engineRun, tea.Cmd) {
	adopt := r.adoptConfig
	if adopt == nil {
		adopt = deploy.AdoptConfig
	}
	treeDir, targetDir := r.cfg.plan.treeDir, r.targetDir
	return r, func() tea.Msg {
		return configAdoptedMsg{err: adopt(treeDir, targetDir)}
	}
}

// retryEngine starts a fresh engine run against the now-provisioned
// target: fresh console, fresh clock, fresh outcome evidence.
func (r engineRun) retryEngine() (engineRun, tea.Cmd) {
	r.configRefused = false
	r.lastFailed = nil
	r.stderrTail = nil
	r.runErr = nil
	r.menuSel = 0
	return r.start(r.width, r.height)
}

// handleConfigKey is the typing surface: append, backspace, enter
// submits the answer line, esc cancels the whole session back to the
// refusal menu.
func (r engineRun) handleConfigKey(msg tea.KeyMsg) (engineRun, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		r.cfg.close()
		r.state = runConfigPrompt
		r.menuSel = 0
		return r, nil
	}
	if r.cfg.prompt == nil {
		return r, nil
	}
	switch msg.Type {
	case tea.KeyRunes:
		r.cfg.input = append(r.cfg.input, msg.Runes...)
	case tea.KeySpace:
		r.cfg.input = append(r.cfg.input, ' ')
	case tea.KeyBackspace:
		if len(r.cfg.input) > 0 {
			r.cfg.input = r.cfg.input[:len(r.cfg.input)-1]
		}
	case tea.KeyEnter:
		if r.cfg.stdin != nil {
			answer := string(r.cfg.input)
			fmt.Fprintln(r.cfg.stdin, answer)
			if r.cfg.prompt.Field == "APP_URL" {
				r.cfg.origin = answer
			}
		}
		r.cfg.prompt = nil
		r.cfg.input = nil
	}
	return r, nil
}

// promptWords maps protocol field names to the same human labels
// install.sh's own TTY prompts use, plus the guidance a person actually
// needs to answer at that moment. An unknown field renders its protocol
// name — honest, never guessed at.
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
		return field, "", nil
	}
}

// callbackNote names the redirect URI this deployment will actually sign
// in through, which is the value the identity provider's registration
// needs and the one most often got wrong. origin is the answer already
// given for APP_URL; when it is missing or long enough to break the
// 80-cell bar, the note falls back to the shape rather than a truncated
// address someone might paste.
func callbackNote(origin string) []string {
	const callbackPath = "/api/auth/callback"
	generic := []string{"register <your Orbit URL>" + callbackPath, "as the redirect URI in your provider"}
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" || !strings.HasPrefix(origin, "https://") {
		return generic
	}
	line := "register " + origin + callbackPath
	if len(line) > 76 {
		return generic
	}
	return []string{line, "as the redirect URI in your provider"}
}

// rejectionWords maps the engine's rejection reason classes to honest
// words. Unknown classes render verbatim.
func rejectionWords(reason string) string {
	switch reason {
	case "empty":
		return "cannot be empty"
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
		return reason
	}
}

// viewConfigCollect renders the session: preparing, or one field being
// asked for.
func (r engineRun) viewConfigCollect(width, height int) string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Orbit needs your configuration"))
	fmt.Fprintln(&b)

	p := r.cfg.prompt
	if p == nil {
		fmt.Fprintln(&b, style.MutedText.Render("talking to the engine…"))
		return skyBlock(r.console.sky, width, height, b.String())
	}

	label, hint, notes := promptWords(p.Field, r.cfg.origin)
	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(label))
	if hint != "" {
		fmt.Fprintln(&b, style.Tagline.Render(hint))
	}
	for _, note := range notes {
		fmt.Fprintln(&b, style.MutedText.Render(note))
	}
	fmt.Fprintln(&b)

	// The input row. A secret is never echoed — not even its length.
	shown := string(r.cfg.input)
	if p.Kind == "secret" {
		shown = ""
	}
	cursor := style.AccentText.Render("▏")
	fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+lipgloss.NewStyle().Foreground(style.Text).Render(shown)+cursor)

	if r.cfg.reason != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, style.DegradedText.Render(r.cfg.reason))
	}
	if p.Attempt > 1 {
		fmt.Fprintln(&b, style.Tagline.Render(fmt.Sprintf("attempt %d of 3", p.Attempt)))
	}
	return skyBlock(r.console.sky, width, height, b.String())
}
