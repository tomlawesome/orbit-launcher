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
