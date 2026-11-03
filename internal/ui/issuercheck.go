package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// The OIDC issuer check (#314, design note 2026-10-30): Enter on a
// well-shaped OIDC_ISSUER answer checks it against the provider's
// discovery document (deploy.CheckOidcIssuer) before it goes to the
// configure step. A match goes on; a trailing-slash difference offers
// the issuer the provider publishes; another difference asks again; a
// check that could not be made lets the person keep the answer or
// correct it. The pattern is an async command returning a tagged
// message, a stale reply dropped by its answer (as configStepMsg's is).

// Every word this feature puts on screen, so the labels can be changed
// in one edit (their exact wording is pending the owner's
// confirmation). The reasons a check could not be made are deploy's
// (deploy.IssuerCheckReason).
const (
	issuerField          = "OIDC_ISSUER"
	issuerChecking       = "checking the address with your provider"
	issuerSlashWith      = "the provider publishes this issuer with a trailing slash"
	issuerSlashWithout   = "the provider publishes this issuer without the trailing slash"
	issuerDifferent      = "the provider names a different issuer"
	issuerCouldNotCheck  = "could not check this issuer: "
	issuerUsePublished   = "Use the published address"
	issuerCorrect        = "Correct the answer"
	issuerKeep           = "Keep the answer as typed"
	issuerPublishedLimit = 3 // screen rows a published issuer may take
)

// issuerCheckedMsg is one check's result for answer.
type issuerCheckedMsg struct {
	answer string
	result deploy.IssuerCheck
}

// issuerCheck is the check's state in the configuration session:
// pending while it runs, then choice holds the rows while the person
// decides on result. The zero value is no check.
type issuerCheck struct {
	answer  string
	pending bool
	result  deploy.IssuerCheck
	choice  menu
	cancel  context.CancelFunc
}

// busy is a check running or waiting for a decision: keys and pastes
// do not reach the answer.
func (c issuerCheck) busy() bool { return c.pending || c.deciding() }

func (c issuerCheck) deciding() bool { return len(c.choice.items) > 0 }

// stop cancels a check still running and forgets it.
func (c *issuerCheck) stop() {
	if c.cancel != nil {
		c.cancel()
	}
	*c = issuerCheck{}
}

func defaultCheckIssuer(ctx context.Context, answer string) deploy.IssuerCheck {
	return deploy.CheckOidcIssuer(ctx, nil, answer)
}

// wantsIssuerCheck reports whether Enter should check the answer before
// sending it: the issuer field, well shaped. A badly shaped answer goes
// to the engine, which rejects it, as before #314.
func (r engineRun) wantsIssuerCheck(msg tea.KeyPressMsg) (string, bool) {
	field, answer := r.cfg.session.asking()
	return answer, msg.Code == tea.KeyEnter && field == issuerField && deploy.WellShapedIssuer(answer)
}

// startIssuerCheck holds answer and checks it in the background. The
// check's context ends with the session (close).
func (r engineRun) startIssuerCheck(answer string) (engineRun, tea.Cmd) {
	check := r.checkIssuer
	if check == nil {
		check = defaultCheckIssuer
	}
	parent := r.cfg.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r.cfg.issuer.stop()
	r.cfg.issuer = issuerCheck{answer: answer, pending: true, cancel: cancel}
	r.cfg.session = r.cfg.session.hold()
	return r, func() tea.Msg {
		return issuerCheckedMsg{answer: answer, result: check(ctx, answer)}
	}
}

// issuerVerdictWords are the log's fixed phrases; the answer and the
// published value never reach the log.
var issuerVerdictWords = map[deploy.IssuerVerdict]string{
	deploy.IssuerMatches:      "matches",
	deploy.IssuerSlashDiffers: "differs by a trailing slash",
	deploy.IssuerDiffers:      "names a different issuer",
	deploy.IssuerUnchecked:    "not made",
}

// handleIssuerChecked acts on the running check's result; any other
// result (an earlier answer's) is dropped.
func (r engineRun) handleIssuerChecked(msg issuerCheckedMsg) (engineRun, tea.Cmd) {
	c := r.cfg.issuer
	if !c.pending || msg.answer != c.answer {
		return r, nil
	}
	r.cfg.issuer.stop()
	res := msg.result
	logDiag("OIDC issuer check: " + issuerVerdictWords[res.Verdict])
	switch res.Verdict {
	case deploy.IssuerMatches:
		return r.sendConfigAnswer(c.answer), nil
	case deploy.IssuerSlashDiffers:
		if r.showable(res.Published) {
			r.cfg.issuer = issuerCheck{answer: c.answer, result: res, choice: newMenu(issuerUsePublished, issuerCorrect)}
			return r, nil
		}
		r.cfg.session = r.cfg.session.reopen(slashWords(c.answer), "")
	case deploy.IssuerDiffers:
		detail := ""
		if r.showable(res.Published) {
			detail = res.Published
		}
		r.cfg.session = r.cfg.session.reopen(issuerDifferent, detail)
	default:
		r.cfg.issuer = issuerCheck{answer: c.answer, result: res, choice: newMenu(issuerCorrect, issuerKeep)}
	}
	return r, nil
}

// decideIssuer is a key on the decision menu.
func (r engineRun) decideIssuer(msg tea.KeyPressMsg) (engineRun, tea.Cmd) {
	var chosen string
	r.cfg.issuer.choice, chosen = r.cfg.issuer.choice.update(msg)
	c := r.cfg.issuer
	switch chosen {
	case issuerUsePublished:
		r.cfg.issuer.stop()
		return r.sendConfigAnswer(c.result.Published), nil
	case issuerKeep:
		r.cfg.issuer.stop()
		return r.sendConfigAnswer(c.answer), nil
	case issuerCorrect:
		r.cfg.issuer.stop()
		r.cfg.session = r.cfg.session.reopen(c.sentence(), "")
	}
	return r, nil
}

// sentence is the decision screen's reason, without the colon that
// introduces a published value.
func (c issuerCheck) sentence() string {
	if c.result.Verdict == deploy.IssuerSlashDiffers {
		return slashWords(c.answer)
	}
	return issuerCouldNotCheck + string(c.result.Reason)
}

// slashWords says which way the provider's issuer differs from answer:
// a slash difference is one trailing slash, so the answer's own end
// tells, whether or not the published value may be shown.
func slashWords(answer string) string {
	if strings.HasSuffix(answer, "/") {
		return issuerSlashWithout
	}
	return issuerSlashWith
}

// showable reports whether a published issuer may be drawn: echoable
// (printable ASCII, bounded, deploy.EchoableIssuer, applied again here
// before anything is drawn) and within issuerPublishedLimit rows.
func (r engineRun) showable(published string) bool {
	return published != "" && deploy.EchoableIssuer(published) &&
		len(valueRows(published, r.width)) <= issuerPublishedLimit
}

// valueRows cuts a value with no spaces (an address) into rows of the
// screen's prose width; an unsized screen (width 0) has it whole.
func valueRows(value string, width int) []string {
	if width <= 0 {
		return []string{value}
	}
	n := measure(width, errorWidth)
	var rows []string
	for len(value) > n {
		rows = append(rows, value[:n])
		value = value[n:]
	}
	return append(rows, value)
}

// view writes the check's part of the configuration screen, under the
// held answer: the working line, or the decision.
func (c issuerCheck) view(b *strings.Builder, width, frame int) {
	switch {
	case c.pending:
		fmt.Fprintln(b)
		fmt.Fprintln(b, workingLine(frame, issuerChecking, ""))
	case c.deciding():
		fmt.Fprintln(b)
		published := c.result.Verdict == deploy.IssuerSlashDiffers
		sentence := c.sentence()
		if published {
			sentence += ":"
		}
		rows := []string{sentence}
		if width > 0 {
			rows = wrapWordsIn(sentence, measure(width, errorWidth), textArea(width))
		}
		for _, row := range rows {
			fmt.Fprintln(b, style.DegradedText.Render(row))
		}
		if published {
			for _, row := range valueRows(c.result.Published, width) {
				fmt.Fprintln(b, style.Ink.Render(row))
			}
		}
		fmt.Fprintln(b)
		c.choice.write(b)
	}
}

// sendConfigAnswer sends value as the open field's answer, keeping the
// APP_URL answer for later guidance.
func (r engineRun) sendConfigAnswer(value string) engineRun {
	var field, answer string
	r.cfg.session, field, answer = r.cfg.session.submit(value, r.cfg.stdin)
	if field == "APP_URL" && r.cfg.stdin != nil {
		r.cfg.origin = answer
	}
	return r
}
