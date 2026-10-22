package ui

import tea "charm.land/bubbletea/v2"

// A key press arrives as a key code plus the modifiers held with it, so a
// control combination is a code-and-modifier pair rather than a key of
// its own — there is no single constant for Ctrl-C to compare against.
// Every screen here treats it the same way (abandon, cleanly), so the
// test lives in one place.
func isCtrlC(msg tea.KeyPressMsg) bool {
	return msg.Code == 'c' && msg.Mod == tea.ModCtrl
}

// SignalQuitMsg asks the app to quit as Ctrl-C does, except that it is
// not ignored while a working step runs: it waits for the step to
// finish, then quits. cmd/orbit-launcher sends it for SIGHUP and SIGTERM,
// so a dropped session never leaves the launcher on a screen nobody sees.
// A repeated signal never forces a stop that is already under way; only a
// second Ctrl-C key does.
type SignalQuitMsg struct{}

// ctrlCPress is the key press a SignalQuitMsg acts as wherever Ctrl-C is
// not ignored, so the two cannot drift apart.
var ctrlCPress = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
