package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// In-console guided configuration — orbit docs/engine-events.md
// "Machine prompts (v0)". install.sh never speaks this protocol; a
// consumer runs scripts/configure.sh directly with
// ORBIT_CONFIGURE_PROMPTS=machine. configure.sh anchors itself to the
// tree it lives in (it cds to its own parent-of-scripts), so the
// launcher stages a private temp tree shaped like an orbit
// installation: the configuration scripts fetched fresh from the same
// channel as install.sh, seeded with the target's existing
// configuration when there is one (update_managed_keys preserves
// unrelated keys), machine prompts driven there, and the produced
// .env-orbit and .orbit-secrets adopted back into the target.
// install.sh was designed for exactly this "pre-provisioned
// configuration shape": its own prepare_configuration re-checks
// readiness and proceeds without prompting when the provisioned
// configuration is complete — verified empirically against orbit
// develop (readiness reports only ORBIT_IMAGE missing after machine
// --init and --set-oidc-secret, and install.sh persists ORBIT_IMAGE
// itself from the image it resolves).

// configScriptNames are the sibling scripts a configure run needs.
// installer-ui.sh doesn't exist on orbit main and configure.sh sources
// it conditionally, so only configure.sh itself is required.
var configScriptNames = []struct {
	name     string
	required bool
}{
	{"configure.sh", true},
	{"configuration.sh", false},
	{"installer-ui.sh", false},
}

// envExampleName is the configuration template, at the tree root.
const envExampleName = ".env-orbit.example"

// scriptSourceURLs resolves where sibling scripts and root files come
// from, derived from the same install.sh URL FetchInstallScript uses
// (including its CI override): .../scripts/install.sh yields
// .../scripts for scripts and ... for root files.
func scriptSourceURLs() (scriptsBase, rootBase string) {
	source := installScriptURL
	if override := os.Getenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL"); override != "" {
		source = override
	}
	scriptsBase = urlDir(source)
	rootBase = scriptsBase
	if strings.HasSuffix(scriptsBase, "/scripts") {
		rootBase = urlDir(scriptsBase)
	}
	return scriptsBase, rootBase
}

// urlDir is path.Dir for URLs — path.Dir would collapse the scheme's
// double slash.
func urlDir(url string) string {
	if i := strings.LastIndex(url, "/"); i > 0 {
		return url[:i]
	}
	return url
}

// FetchConfigTree fetches the configuration script set and stages it
// into a fresh private temp tree shaped like an orbit installation.
// The returned cleanup removes the whole tree — call it once the
// configuration session is over, success or not (the tree holds a
// collected secret once --set-oidc-secret has run).
func FetchConfigTree(ctx context.Context) (treeDir string, cleanup func(), err error) {
	scriptsBase, rootBase := scriptSourceURLs()

	files := map[string][]byte{}
	for _, s := range configScriptNames {
		body, err := fetchScriptFile(ctx, scriptsBase+"/"+s.name, s.required)
		if err != nil {
			return "", nil, fmt.Errorf("fetch %s: %w", s.name, err)
		}
		if body != nil {
			files[filepath.Join("scripts", s.name)] = body
		}
	}
	example, err := fetchFile(ctx, rootBase+"/"+envExampleName)
	if err != nil {
		return "", nil, fmt.Errorf("fetch %s: %w", envExampleName, err)
	}
	files[envExampleName] = example

	treeDir, err = os.MkdirTemp("", "orbit-launcher-config-*")
	if err != nil {
		return "", nil, fmt.Errorf("stage configuration tree: %w", err)
	}
	cleanup = func() { os.RemoveAll(treeDir) }

	if err := os.Mkdir(filepath.Join(treeDir, "scripts"), 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("stage configuration tree: %w", err)
	}
	for name, body := range files {
		mode := os.FileMode(0o600)
		if strings.HasPrefix(name, "scripts"+string(filepath.Separator)) {
			mode = 0o700
		}
		if err := os.WriteFile(filepath.Join(treeDir, name), body, mode); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("stage configuration tree: %w", err)
		}
	}
	return treeDir, cleanup, nil
}

// fetchScriptFile fetches one script, tolerating absence (nil, nil)
// when the script is optional — orbit main simply doesn't have some of
// them yet.
func fetchScriptFile(ctx context.Context, url string, required bool) ([]byte, error) {
	body, err := fetchFile(ctx, url)
	if err != nil {
		if !required {
			return nil, nil
		}
		return nil, err
	}
	if !strings.HasPrefix(string(body), "#!") {
		return nil, fmt.Errorf("fetched content does not look like a script (no shebang) — refusing to run it")
	}
	return body, nil
}

// statusError is a non-200 response, distinguishable from transport
// failures so callers can treat absence as a capability signal.
type statusError struct{ status string }

func (e statusError) Error() string { return "unexpected status " + e.status }

// fetchFile downloads one file with the same size discipline as
// install.sh's fetch.
func fetchFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError{status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInstallScriptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxInstallScriptBytes {
		return nil, fmt.Errorf("larger than the %d byte limit", maxInstallScriptBytes)
	}
	return body, nil
}

// ImportTargetConfig seeds the staged tree with the target's existing
// configuration, so a reconfiguration preserves everything the person
// isn't being asked about. A target with no configuration (fresh
// install) imports nothing.
func ImportTargetConfig(treeDir, targetDir string) error {
	if err := copyConfigFile(filepath.Join(targetDir, ".env-orbit"), filepath.Join(treeDir, ".env-orbit")); err != nil {
		return err
	}
	return copySecretsDir(filepath.Join(targetDir, ".orbit-secrets"), filepath.Join(treeDir, ".orbit-secrets"))
}

// AdoptConfig moves the collected configuration into the target:
// .env-orbit (0600) and .orbit-secrets (0700, entries 0600), creating
// the target directory if this is a fresh install.
func AdoptConfig(treeDir, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("prepare target: %w", err)
	}
	src := filepath.Join(treeDir, ".env-orbit")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("configuration session left no .env-orbit: %w", err)
	}
	if err := copyConfigFile(src, filepath.Join(targetDir, ".env-orbit")); err != nil {
		return err
	}
	return copySecretsDir(filepath.Join(treeDir, ".orbit-secrets"), filepath.Join(targetDir, ".orbit-secrets"))
}

func copyConfigFile(src, dst string) error {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, body, 0o600); err != nil {
		return err
	}
	// WriteFile's mode only applies on creation; an existing file keeps
	// its old mode, and 0600 is part of the engine's own contract.
	return os.Chmod(dst, 0o600)
}

func copySecretsDir(src, dst string) error {
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if err := copyConfigFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// ConfigStep is one machine-prompt configure invocation.
type ConfigStep string

const (
	// ConfigStepInit collects APP_URL, OIDC_ISSUER and OIDC_CLIENT_ID
	// (OIDC_CALLBACK_URL is derived from an accepted APP_URL).
	ConfigStepInit ConfigStep = "--init"
	// ConfigStepSecret collects the OIDC client secret.
	ConfigStepSecret ConfigStep = "--set-oidc-secret"
)

// BuildConfigureCommand builds one machine-prompt configure run in the
// staged tree. Setsid is load-bearing exactly as it is for the engine
// run: a legacy configure.sh (orbit main) ignores
// ORBIT_CONFIGURE_PROMPTS and would otherwise open /dev/tty and prompt
// straight through the alt screen; detached, it fails fast with no
// protocol line — which is precisely the launcher's signal to fall
// back to the terminal handoff.
func BuildConfigureCommand(treeDir string, step ConfigStep) *exec.Cmd {
	cmd := exec.Command("bash", "scripts/configure.sh", string(step))
	cmd.Dir = treeDir
	cmd.Env = append(os.Environ(), "ORBIT_CONFIGURE_PROMPTS=machine")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// ConfigCheck is configure.sh --check's readiness report, reduced to
// what the launcher decides with: which required fields are missing.
type ConfigCheck struct{ Missing []string }

// guidedFields are the fields machine --init collects (or derives).
var guidedFields = map[string]bool{
	"APP_URL":           true,
	"OIDC_ISSUER":       true,
	"OIDC_CLIENT_ID":    true,
	"OIDC_CALLBACK_URL": true,
}

// NeedsInit reports whether a machine --init run is required.
func (c ConfigCheck) NeedsInit() bool {
	for _, f := range c.Missing {
		if guidedFields[f] {
			return true
		}
	}
	return false
}

// NeedsSecret reports whether a machine --set-oidc-secret run is
// required.
func (c ConfigCheck) NeedsSecret() bool {
	for _, f := range c.Missing {
		if f == "OIDC_CLIENT_SECRET" {
			return true
		}
	}
	return false
}

// Unfixable lists missing required fields the machine protocol cannot
// collect. ORBIT_IMAGE is excluded: install.sh persists it itself from
// the image it resolves, before its own readiness gate.
func (c ConfigCheck) Unfixable() []string {
	var out []string
	for _, f := range c.Missing {
		if guidedFields[f] || f == "OIDC_CLIENT_SECRET" || f == "ORBIT_IMAGE" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// RunConfigCheck runs configure.sh --check in the staged tree and
// parses its readiness report. A non-zero exit with a parseable report
// is the normal "something's missing" answer, not an error; an error
// means the check itself couldn't run (structural failure, legacy
// script misbehaviour).
func RunConfigCheck(ctx context.Context, treeDir string) (ConfigCheck, error) {
	cmd := exec.CommandContext(ctx, "bash", "scripts/configure.sh", "--check")
	cmd.Dir = treeDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, runErr := cmd.Output()

	var check ConfigCheck
	sawReport := false
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "ready", "optional":
			sawReport = true
		case "missing":
			sawReport = true
			check.Missing = append(check.Missing, fields[1])
		}
	}
	if !sawReport {
		if runErr != nil {
			return ConfigCheck{}, fmt.Errorf("configuration check failed: %w", runErr)
		}
		return ConfigCheck{}, fmt.Errorf("configuration check produced no readiness report")
	}
	return check, nil
}
