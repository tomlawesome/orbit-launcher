package ui

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// handoverProbe is install.sh's stdin: exec copies it while the command
// runs, so each Read sees whether the terminal is marked handed over.
type handoverProbe struct{ seen []bool }

func (p *handoverProbe) Read([]byte) (int, error) {
	p.seen = append(p.seen, TerminalHandedOver())
	return 0, io.EOF
}

// The terminal is marked handed over exactly while install.sh runs, so a
// Ctrl-C then goes to install.sh, and the mark clears when it ends.
func TestHandoffCommand_MarksTheTerminalHandedOverOnlyWhileItRuns(t *testing.T) {
	probe := &handoverProbe{}
	c := &handoffCommand{exec.Command("cat")}
	c.SetStdin(probe)
	c.SetStdout(io.Discard)
	c.SetStderr(io.Discard)

	if TerminalHandedOver() {
		t.Fatal("the terminal is marked handed over before install.sh starts")
	}
	if err := c.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(probe.seen) == 0 || !probe.seen[0] {
		t.Errorf("while install.sh ran, TerminalHandedOver() = %v, want true", probe.seen)
	}
	if TerminalHandedOver() {
		t.Error("the terminal is still marked handed over after install.sh ended")
	}
}

// A stream the command already has is kept, as ExecProcess's setters
// keep it; only a missing one is filled in.
func TestHandoffCommand_SettersKeepAStreamTheCommandAlreadyHas(t *testing.T) {
	own := &bytes.Buffer{}
	ownIn := strings.NewReader("")
	c := &handoffCommand{exec.Command("true")}
	c.Stdin, c.Stdout, c.Stderr = ownIn, own, own

	c.SetStdin(strings.NewReader("other"))
	c.SetStdout(io.Discard)
	c.SetStderr(io.Discard)
	if c.Stdin != ownIn || c.Stdout != own || c.Stderr != own {
		t.Error("a setter replaced a stream the command already had")
	}

	empty := &handoffCommand{exec.Command("true")}
	empty.SetStdin(ownIn)
	empty.SetStdout(own)
	empty.SetStderr(own)
	if empty.Stdin != ownIn || empty.Stdout != own || empty.Stderr != own {
		t.Error("a setter left a missing stream unset")
	}
}
