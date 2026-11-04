package main

import (
	"errors"
	"io"
	"os"
	"syscall"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ui"
)

func TestHangupInput_AFailedReadIsOneHangUpAndEndOfFile(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	_ = r.Close() // every read now fails with os.ErrClosed

	var sent []tea.Msg
	in := &hangupInput{File: r, send: func(m tea.Msg) { sent = append(sent, m) }}
	buf := make([]byte, 8)
	for i := 0; i < 3; i++ {
		n, err := in.Read(buf)
		if n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("read %d = (%d, %v), want (0, io.EOF)", i, n, err)
		}
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d messages over 3 failed reads, want 1: %v", len(sent), sent)
	}
	want := ui.SignalQuitMsg{Signal: syscall.SIGHUP}
	if sent[0] != tea.Msg(want) {
		t.Fatalf("sent %#v, want %#v", sent[0], want)
	}
}

func TestHangupInput_ASuccessfulReadPassesThrough(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	if _, err := w.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}

	sent := 0
	in := &hangupInput{File: r, send: func(tea.Msg) { sent++ }}
	buf := make([]byte, 8)
	n, err := in.Read(buf)
	if err != nil || n != 1 || buf[0] != 'q' {
		t.Fatalf("read = (%d, %v, %q), want (1, nil, \"q\")", n, err, buf[:n])
	}
	if sent != 0 {
		t.Fatalf("a successful read sent %d messages, want none", sent)
	}
}

func TestHangupInput_HandsBackItsTerminalFile(t *testing.T) {
	in := &hangupInput{File: os.Stdin}
	if got := in.TerminalFile(); got != os.Stdin {
		t.Fatalf("TerminalFile() = %v, want os.Stdin", got)
	}
}
