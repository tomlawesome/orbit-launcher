package ui

import "testing"

// The live update leg drives the earlier release's launcher by the digit of
// the row in the launcher under test's menu (#307), so Install must keep its
// row while any earlier release in the window is tested.
func TestMainMenu_InstallIsTheFirstRow(t *testing.T) {
	if len(MainMenu) == 0 || MainMenu[0].Label != "Install" {
		t.Fatalf("MainMenu[0] = %+v, want Install: the live update leg drives the earlier release's launcher by the digit of the row in the launcher under test's menu (#307)", MainMenu)
	}
}
