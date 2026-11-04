package main

import (
	"io"
	"os"
	"sync/atomic"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ui"
)

// hangupInput is the terminal Bubble Tea reads keys from, with the end of
// input treated as a hang-up (#335), as a shell treats a failed read
// from its terminal. Closing the terminal (a dropped SSH session) makes
// the kernel hang it up: the next read returns end of file, or EIO when
// it lands just before the hang-up completes. Bubble Tea drops end of
// file silently, leaving a launcher nobody can reach running on (with no
// controlling terminal no SIGHUP follows), and turns EIO into a killed
// program that exits 1 before the engine's stop has run. Either way the
// launcher is sent the message SIGHUP sends, and Bubble Tea sees only end
// of file, so its event loop keeps running until the stop has finished.
type hangupInput struct {
	*os.File
	send func(tea.Msg)
	hung atomic.Bool
}

func (in *hangupInput) Read(b []byte) (int, error) {
	n, err := in.File.Read(b)
	if err == nil {
		return n, nil
	}
	// A repeated SIGHUP never forces a stop already under way
	// (ui.SignalQuitMsg), so this one racing the real one is harmless;
	// it is sent once all the same.
	if in.hung.CompareAndSwap(false, true) {
		in.send(ui.SignalQuitMsg{Signal: syscall.SIGHUP})
	}
	return n, io.EOF
}

// withHangupInput is the program option that reads the keys through
// hangupInput. Bubble Tea reads stdin when it is a terminal and opens
// /dev/tty otherwise; that second case is left to it, so the option is
// none then.
func withHangupInput(send func(tea.Msg)) tea.ProgramOption {
	if !isTerminal(os.Stdin) {
		return func(*tea.Program) {}
	}
	return tea.WithInput(&hangupInput{File: os.Stdin, send: send})
}
