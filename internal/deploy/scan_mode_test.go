package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

// #345: Detect reads DOCUMENT_SCAN_MODE from .env-orbit into ScanMode, so
// the launcher can tell the virus scanner is off.
func TestDetect_ReadsScanMode(t *testing.T) {
	for _, c := range []struct{ name, line, want string }{
		{"disabled", "DOCUMENT_SCAN_MODE=disabled\n", "disabled"},
		{"quoted", "DOCUMENT_SCAN_MODE=\"disabled\"\n", "disabled"},
		{"enabled", "DOCUMENT_SCAN_MODE=enabled\n", "enabled"},
		{"required", "DOCUMENT_SCAN_MODE=required\n", "required"},
		{"empty", "DOCUMENT_SCAN_MODE=\n", ""},
		{"unset", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			env := "APP_URL=https://mail.example.test\n" + c.line
			if err := os.WriteFile(filepath.Join(dir, EnvFile), []byte(env), 0o600); err != nil {
				t.Fatal(err)
			}
			d, err := Detect(dir)
			if err != nil || d == nil {
				t.Fatalf("Detect = %v, %v", d, err)
			}
			if d.ScanMode != c.want {
				t.Errorf("ScanMode = %q, want %q", d.ScanMode, c.want)
			}
		})
	}
}
