package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the minimal set of values orbit-launcher's own guided
// configuration screen collects for a Standard-profile install — exactly
// orbit's install.sh/configure.sh strictly required fields (verified
// against scripts/configure.sh's run_check: APP_URL, OIDC_ISSUER,
// OIDC_CLIENT_ID, OIDC_CLIENT_SECRET, OIDC_CALLBACK_URL — ORBIT_IMAGE is
// written by install.sh itself, not by us). AI/Full profile fields
// (local model selection) aren't collected yet — Standard only, for now.
type Config struct {
	AppURL           string
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
}

// CallbackURL is derived, not collected — configure.sh's readiness check
// requires it to be exactly "<origin>/api/auth/callback" with no trailing
// slash on the origin, so asking a person to type it separately would
// only invite a mismatch.
func (c Config) CallbackURL() string {
	return strings.TrimRight(c.AppURL, "/") + "/api/auth/callback"
}

// WriteConfig writes a complete .env-orbit into targetDir containing
// exactly the required fields, mode 0600. configure.sh's default
// invocation (which install.sh always runs first) preserves existing
// values in an already-complete file — verified by reading
// scripts/configure.sh's ensure_environment_file and run_check directly
// rather than assuming — so this only ever needs to run once, before the
// first install.sh invocation.
func WriteConfig(targetDir string, c Config) error {
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}

	var b strings.Builder
	fmt.Fprintln(&b, "ORBIT_CONFIG_SCHEMA_VERSION=1")
	fmt.Fprintf(&b, "APP_URL=%s\n", c.AppURL)
	fmt.Fprintf(&b, "OIDC_ISSUER=%s\n", c.OIDCIssuer)
	fmt.Fprintf(&b, "OIDC_CLIENT_ID=%s\n", c.OIDCClientID)
	fmt.Fprintf(&b, "OIDC_CLIENT_SECRET=%s\n", c.OIDCClientSecret)
	fmt.Fprintf(&b, "OIDC_CALLBACK_URL=%s\n", c.CallbackURL())
	fmt.Fprintln(&b, "COMPOSE_PROFILES=")

	path := filepath.Join(targetDir, ".env-orbit")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write .env-orbit: %w", err)
	}
	return nil
}
