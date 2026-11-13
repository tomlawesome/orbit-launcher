package ui

import (
	"strings"
	"testing"
)

// Configure runs as an update, and an update never sends the scanner
// choice (#340): the deployment's own DOCUMENT_SCAN_MODE stands (#286).

// cfg286ScannerFolder is cfg286Folder with DOCUMENT_SCAN_MODE set to
// mode; "" leaves the line out.
func cfg286ScannerFolder(t *testing.T, dir, profiles, mode string) string {
	t.Helper()
	cfg286Folder(t, dir, profiles, true)
	if mode == "" {
		return dir
	}
	body := "APP_URL=https://mail.example.com\nDOCUMENT_SCAN_MODE=" + mode + "\n"
	if profiles != "-" {
		body += "COMPOSE_PROFILES=" + profiles + "\n"
	}
	writeEnv(t, dir, body)
	return dir
}

// Fault: Configure's run carrying ORBIT_INSTALL_SCANNER, which would let
// a profile change also flip the deployment's scanner. Every direction,
// with the scanner on and off, sees the variable unset.
func TestConfigure286_RunNeverCarriesTheScannerChoice(t *testing.T) {
	for _, c := range []struct{ name, profiles, mode, to string }{
		{"standard to documents", "-", "", "Documents"},
		{"documents to standard", "processing", "", "Standard"},
		{"scanner off, standard to documents", "-", "disabled", "Documents"},
	} {
		t.Run(c.name, func(t *testing.T) {
			target, out := scanner340Recorder(t)
			cfg286ScannerFolder(t, target, c.profiles, c.mode)
			m, queue := cfg286App(t, target)
			m = cfg286ToConfirm(t, m, c.to)
			cfg286Start(t, m, queue, c.to)
			if got := docs260Lines(t, out, "scanner"); len(got) != 1 || got[0] != "<unset>" {
				t.Errorf("the installer saw %s = %q, want exactly one run with it unset", scanner340Env, got)
			}
			scanner340NoChoiceInArgv(t, out)
		})
	}
}

// Fault: Configure's table offering Install's Scanner row. The table has
// the four profile rows only, at every width.
func TestConfigure286_TableHasNoScannerRow(t *testing.T) {
	for _, w := range []int{120, 80, 50} {
		dir := cfg286Folder(t, shortTempDir(t), "-", true)
		m, _ := cfg286App(t, dir)
		m = cfg286Resize(t, m, w, 40)
		m = cfg286Open(t, m)
		cfg286OnTable(t, m)
		s := flat(screen(m))
		if strings.Contains(s, installScanner) || strings.Contains(s, installScannerAdds) {
			t.Errorf("at %d columns Configure's table shows the scanner row:\n%s", w, screen(m))
		}
	}
}

// Fault: Configure counting the scanner the deployment does not run (or
// the other way round). With 4 GB, Documents is "not enough" while the
// scanner runs (7 GB) and fits when DOCUMENT_SCAN_MODE is disabled (3 GB).
func TestConfigure286_MemoryNeedFollowsTheDeploymentsScanner(t *testing.T) {
	for _, c := range []struct{ mode, want string }{
		{"", "not enough"},
		{"disabled", "+1 GB"},
	} {
		dir := cfg286ScannerFolder(t, shortTempDir(t), "-", c.mode)
		m, _ := cfg286App(t, dir)
		docs260Memory(t, 4*docs260GB, true)
		m = cfg286Open(t, m)
		if status, _ := cfg286TableStatus(t, screen(m)); status["Documents"] != c.want {
			t.Errorf("DOCUMENT_SCAN_MODE=%q with 4 GB: Documents %q, want %q:\n%s", c.mode, status["Documents"], c.want, screen(m))
		}
	}
}
