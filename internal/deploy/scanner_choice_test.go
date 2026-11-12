package deploy

// Blind tests for #340 (the virus-scanner choice on Install), written
// from the design note's test specification alone: items 6 and 9.

import (
	"os/exec"
	"slices"
	"testing"
)

// Spec 6: the constants and WithScanner. An empty scanner adds nothing
// (an update never sends one); a real one is appended last, after the
// person's own environment.
func TestScanner340_ConstantsAreTheEngineWords(t *testing.T) {
	if ScannerEnv != "ORBIT_INSTALL_SCANNER" {
		t.Errorf("ScannerEnv = %q, want ORBIT_INSTALL_SCANNER", ScannerEnv)
	}
	if ScannerOn != "on" {
		t.Errorf("ScannerOn = %q, want on", ScannerOn)
	}
	if ScannerOff != "off" {
		t.Errorf("ScannerOff = %q, want off", ScannerOff)
	}
}

func TestScanner340_WithScannerEmptyAddsNothing(t *testing.T) {
	cmd := &exec.Cmd{Env: []string{"A=1", "B=2"}}
	WithScanner(cmd, "")
	if want := []string{"A=1", "B=2"}; !slices.Equal(cmd.Env, want) {
		t.Errorf("Env = %q after an empty scanner, want it unchanged %q", cmd.Env, want)
	}
	bare := &exec.Cmd{}
	WithScanner(bare, "")
	if len(bare.Env) != 0 {
		t.Errorf("an empty scanner put %q into an empty environment", bare.Env)
	}
}

func TestScanner340_WithScannerAppendsLast(t *testing.T) {
	for _, c := range []struct{ value, want string }{
		{ScannerOff, "ORBIT_INSTALL_SCANNER=off"},
		{ScannerOn, "ORBIT_INSTALL_SCANNER=on"},
	} {
		cmd := &exec.Cmd{Env: []string{"A=1", "ORBIT_INSTALL_SCANNER=stale", "B=2"}}
		WithScanner(cmd, c.value)
		if n := len(cmd.Env); n != 4 || cmd.Env[3] != c.want || !slices.Equal(cmd.Env[:3], []string{"A=1", "ORBIT_INSTALL_SCANNER=stale", "B=2"}) {
			t.Errorf("WithScanner(%q): Env = %q, want the old three then %q last", c.value, cmd.Env, c.want)
		}
	}
}

// Spec 9: what a machine needs, from MemoryDefaults and not literals,
// so the live drift test keeps its meaning.
func TestScanner340_ProfileNeedFollowsTheScanner(t *testing.T) {
	orbit := MemoryDefaults["ORBIT_MEMORY_LIMIT"]
	scanner := MemoryDefaults["CLAMAV_MEMORY_LIMIT"]
	reader := MemoryDefaults["TIKA_MEMORY_LIMIT"]
	for _, c := range []struct {
		profile string
		on      bool
		want    uint64
	}{
		{ProfileStandard, true, orbit + scanner},
		{ProfileStandard, false, orbit},
		{ProfileDocuments, true, orbit + scanner + reader},
		{ProfileDocuments, false, orbit + reader},
		{"", true, 0},
		{"", false, 0},
	} {
		if got := ProfileNeed(c.profile, c.on); got != c.want {
			t.Errorf("ProfileNeed(%q, %v) = %d, want %d", c.profile, c.on, got, c.want)
		}
	}
	// The shipped defaults, so a changed table is noticed here too.
	if got := ProfileNeed(ProfileStandard, false); got != 2<<30 {
		t.Errorf("Standard with the scanner off needs %d, want 2 GiB", got)
	}
	if got := ProfileNeed(ProfileDocuments, false); got != 3<<30 {
		t.Errorf("Documents with the scanner off needs %d, want 3 GiB", got)
	}
}
