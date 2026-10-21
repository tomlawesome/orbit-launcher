package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// Repair's first screen lists each expected service's state above the
// findings (#255). The states are read once, through the serviceStates
// seam, when the diagnosis is first shown, and kept on the model;
// rendering only reads that copy and never asks Docker.

// serviceNameWidth is the name column: glyph, space, then this many
// cells, so every state word starts 16 cells after the glyph.
const serviceNameWidth = 14

// serviceReading is what the one read produced.
type serviceReading struct {
	loaded   bool
	expected []string
	states   []deploy.ServiceState
	err      error
}

// realServiceStates is the production reader wired where the model is
// built.
func realServiceStates(dir string) ([]deploy.ServiceState, error) {
	return deploy.ServiceStates(context.Background(), dir)
}

// loadServices reads the services once and keeps the result. A model
// with no serviceStates seam (the c9 harness) reads nothing and shows
// no services block at all. The reading lives behind a pointer because
// the model is copied on every Update.
func (m RepairModel) loadServices() {
	if m.serviceStates == nil || m.services == nil || m.services.loaded {
		return
	}
	r := m.services
	r.loaded = true
	r.states, r.err = m.serviceStates(m.targetDir)
	r.expected = expectedServices(m.targetDir)
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
		case found.State == deploy.ContainerRunning:
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
// the screen, the plan included, has its rows (label and the blank row
// after it included); the full list is used if it fits, otherwise the
// running rows fold into one `✓ N running` row and every faulted or
// missing row stays.
func (m RepairModel) servicesBlock(room int) []string {
	if m.serviceStates == nil || m.services == nil || !m.services.loaded {
		return nil
	}
	if m.services.err != nil {
		return []string{style.MutedText.Render("services" + style.Separator + "couldn't check")}
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
	if len(rows)+2 > room && running > 0 {
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
