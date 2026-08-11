package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfig_CallbackURL(t *testing.T) {
	tests := []struct {
		appURL string
		want   string
	}{
		{"https://mail.example.com", "https://mail.example.com/api/auth/callback"},
		{"https://mail.example.com/", "https://mail.example.com/api/auth/callback"},
	}
	for _, tt := range tests {
		c := Config{AppURL: tt.appURL}
		if got := c.CallbackURL(); got != tt.want {
			t.Errorf("CallbackURL() for %q = %q, want %q", tt.appURL, got, tt.want)
		}
	}
}

func TestWriteConfig_WritesAllRequiredFields(t *testing.T) {
	dir := t.TempDir()
	c := Config{
		AppURL:           "https://mail.example.com",
		OIDCIssuer:       "https://auth.example.com/application/o/orbit/",
		OIDCClientID:     "orbit-client",
		OIDCClientSecret: "s3cr3t",
	}
	if err := WriteConfig(dir, c); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	path := filepath.Join(dir, ".env-orbit")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat .env-orbit: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf(".env-orbit mode = %o, want 0600", perm)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .env-orbit: %v", err)
	}
	body := string(content)

	for _, want := range []string{
		"APP_URL=https://mail.example.com",
		"OIDC_ISSUER=https://auth.example.com/application/o/orbit/",
		"OIDC_CLIENT_ID=orbit-client",
		"OIDC_CLIENT_SECRET=s3cr3t",
		"OIDC_CALLBACK_URL=https://mail.example.com/api/auth/callback",
	} {
		if !strings.Contains(body, want) {
			t.Errorf(".env-orbit missing expected line %q; got:\n%s", want, body)
		}
	}

	// ORBIT_IMAGE is deliberately never written here — install.sh's own
	// prepare_configuration writes it via `ORBIT_IMAGE=... configure.sh`.
	if strings.Contains(body, "ORBIT_IMAGE=") {
		t.Error(".env-orbit should not set ORBIT_IMAGE — install.sh writes that itself")
	}
}

func TestWriteConfig_CreatesTargetDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "target")
	if err := WriteConfig(dir, Config{AppURL: "https://mail.example.com"}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env-orbit")); err != nil {
		t.Errorf(".env-orbit not created in nested target dir: %v", err)
	}
}
