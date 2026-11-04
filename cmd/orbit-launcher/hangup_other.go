//go:build !linux

package main

import "os"

// isTerminal is false off Linux, the one system the launcher supports,
// which leaves the choice of input to Bubble Tea there.
func isTerminal(*os.File) bool { return false }
