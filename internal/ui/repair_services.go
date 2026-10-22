package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// Repair's first screen lists each expected service's state above the
// findings (#255). The states are read once per diagnosis, through the
// serviceStates seam, by a command the diagnosis hands back (#277: never
// on the screen's own thread), and kept on the model: the reading is
// dropped when a run or a fresh diagnosis starts. Until the answer comes
// the block says the services are still being checked. Rendering only
// reads that copy and never asks Docker.

// serviceNameWidth is the name column: glyph, space, then this many
// cells, so every state word starts 16 cells after the glyph.
const serviceNameWidth = 14

// serviceReading is what the one read produced. pending is the read
// under way, loaded its answer in.
type serviceReading struct {
	pending  bool
	loaded   bool
	expected []string
	states   []deploy.ServiceState
	err      error
}

// repairServicesMsg is the services read's answer; reading says which
// diagnosis asked, so an answer that arrives after a run or a fresh
// diagnosis has replaced it is dropped.
type repairServicesMsg struct {
	reading  *serviceReading
	expected []string
	states   []deploy.ServiceState
	err      error
}

// realServiceStates is the production reader wired where the model is
// built.
func realServiceStates(dir string) ([]deploy.ServiceState, error) {
	return deploy.ServiceStates(context.Background(), dir)
}

// readServices starts the one read of the services, returning the
// command that runs it off the screen's thread, or nil when it has
// started already. A model with no serviceStates seam (the c9 harness)
// reads nothing and shows no services block at all. The reading lives
// behind a pointer because the model is copied on every Update; the
// command never touches it.
func (m RepairModel) readServices() tea.Cmd {
	r := m.services
	if m.serviceStates == nil || r == nil || r.loaded || r.pending {
		return nil
	}
	r.pending = true
	read, dir := m.serviceStates, m.targetDir
	return func() tea.Msg {
		states, err := read(dir)
		return repairServicesMsg{reading: r, expected: expectedServices(dir), states: states, err: err}
	}
}

// gotServices keeps the read's answer if it belongs to the current
// diagnosis.
func (m RepairModel) gotServices(msg repairServicesMsg) RepairModel {
	if msg.reading == nil || msg.reading != m.services {
		return m
	}
	m.services = &serviceReading{loaded: true, expected: msg.expected, states: msg.states, err: msg.err}
	return m
}

// expectedServices are the services the deployment's profiles call for,
// in fixed order.
func expectedServices(dir string) []string {
	out := []string{"orbit-db", "orbit-app", "orbit-clamav"}
	d, _ := deploy.Detect(dir)
	if d == nil {
		return out
	}
	has := func(names ...string) bool {
		for _, p := range d.Profiles {
			for _, n := range names {
				if p == n {
					return true
				}
			}
		}
		return false
	}
	if has("processing", "full") {
		out = append(out, "orbit-tika")
	}
	if has("ai", "full") {
		out = append(out, "orbit-ollama")
	}
	return out
}

// serviceRow is one service's line: faulted rows are never collapsed.
type serviceRow struct {
	glyph, name, word string
	running           bool
}

func serviceRows(expected []string, have []deploy.ServiceState) []serviceRow {
	rows := make([]serviceRow, 0, len(expected))
	for _, name := range expected {
		var found *deploy.ServiceState
		for i := range have {
			if have[i].Service == name {
				found = &have[i]
				break
			}
		}
		r := serviceRow{name: name}
		switch {
		case found == nil:
			r.glyph, r.word = style.SymbolFailure, "missing"
		case found.State == deploy.ContainerRunning && found.Health == deploy.HealthUnhealthy:
			r.glyph, r.word = style.SymbolWarn, deploy.HealthUnhealthy
		case found.State == deploy.ContainerRunning && found.Health == deploy.HealthStarting:
			r.glyph, r.word = style.SymbolWarn, deploy.HealthStarting
		case found.Running():
			r.glyph, r.word, r.running = style.SymbolSuccess, deploy.ContainerRunning, true
		case found.State == deploy.ContainerRestarting:
			r.glyph, r.word = style.SymbolWarn, deploy.ContainerRestarting
		default:
			r.glyph, r.word = style.SymbolWarn, "stopped"
		}
		rows = append(rows, r)
	}
	return rows
}

// servicesBlock returns the block's rows, or nil when there is none to
// show. room is the rows the screen has left for it once the rest of
// the screen, the plan included, has its rows. The block draws its label
// and one row per service, nothing more (viewDiagnosis joins it straight
// onto the findings), so the full list is used when those rows fit room
// (#277); otherwise the running rows fold into one `✓ N running` row and
// every faulted or missing row stays.
func (m RepairModel) servicesBlock(room int) []string {
	if m.serviceStates == nil || m.services == nil {
		return nil
	}
	if !m.services.loaded {
		if m.services.pending {
			return []string{style.MutedText.Render("services" + style.Separator + "checking")}
		}
		return nil
	}
	if m.services.err != nil {
		return []string{style.MutedText.Render("services" + style.Separator + "could not check")}
	}
	rows := serviceRows(m.services.expected, m.services.states)
	var folded, faulted []serviceRow
	running := 0
	for _, r := range rows {
		if r.running {
			running++
		} else {
			faulted = append(faulted, r)
		}
	}
	if 1+len(rows) > room && running > 0 { // the label, then a row each
		folded = faulted
	} else {
		folded = nil
	}
	const plainWidth = 2 + serviceNameWidth
	width := 0
	for _, r := range rows {
		if w := plainWidth + len(r.word); w > width {
			width = w
		}
	}
	pad := func(used int) string { return strings.Repeat("\u00a0", max(0, width-used)) }
	out := []string{style.Tagline.Render("services") + pad(len("services"))}
	line := func(r serviceRow) string {
		glyphStyle, wordStyle := style.DegradedText, style.DegradedText
		switch {
		case r.running:
			glyphStyle, wordStyle = style.SuccessText, style.MutedText
		case r.glyph == style.SymbolFailure:
			glyphStyle, wordStyle = style.ErrorText, style.ErrorText
		}
		name := r.name + strings.Repeat(" ", max(0, serviceNameWidth-len(r.name)))
		return glyphStyle.Render(r.glyph) + " " + style.Ink.Render(name) + wordStyle.Render(r.word) + pad(plainWidth+len(r.word))
	}
	if folded != nil {
		sum := fmt.Sprintf("%s %d %s", style.SymbolSuccess, running, deploy.ContainerRunning)
		out = append(out, style.SuccessText.Render(style.SymbolSuccess)+style.MutedText.Render(fmt.Sprintf(" %d %s", running, deploy.ContainerRunning))+pad(len([]rune(sum))))
		for _, r := range faulted {
			out = append(out, line(r))
		}
		return out
	}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return out
}
