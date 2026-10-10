package deploy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// In-console guided configuration — orbit docs/engine-events.md
// "Machine prompts (v0)". install.sh never speaks this protocol; a
// consumer runs scripts/configure.sh directly with
// ORBIT_CONFIGURE_PROMPTS=machine. configure.sh anchors itself to the
// tree it lives in (it cds to its own parent-of-scripts), so the
// launcher needs a private tree shaped like an orbit installation.
// That tree is the one install.sh hands over: BuildInstallCommand gives
// every run an empty private directory (ORBIT_LAUNCHER_CONFIG_TREE),
// and on its configuration refusal install.sh copies configure.sh, its
// siblings and .env-orbit.example into it from the assets it has
// already verified against the image (ai/orbit#1225). Nothing is
// downloaded (#190): an install.sh that hands over no tree means the
// terminal handoff instead. The tree is seeded with the target's
// existing configuration when there is one (update_managed_keys
// preserves unrelated keys), machine prompts are driven there, and the
// produced .env-orbit and .orbit-secrets are adopted back into the
// target. install.sh was designed for exactly this "pre-provisioned
// configuration shape": its own prepare_configuration re-checks
// readiness and proceeds without prompting when the provisioned
// configuration is complete — verified empirically against orbit
// develop (readiness reports only ORBIT_IMAGE missing after machine
// --init and --set-oidc-secret, and install.sh persists ORBIT_IMAGE
// itself from the image it resolves).

// ErrNoConfigTree means install.sh handed over no configure tree — an
// install.sh from before ai/orbit#1225, or a refusal it didn't copy the
// tree for. There is nothing verified to run in-console, so the caller
// falls back to the terminal handoff, which runs the verified install.sh
// and so the image's own configure.sh.
var ErrNoConfigTree = errors.New("install.sh handed over no configuration tree")

// configureScript is where configure.sh sits in a configure tree.
const configureScript = "scripts/configure.sh"

// OpenConfigTree starts a configuration session in the configure tree
// install.sh handed over (see BuildInstallCommand). The returned
// endSession clears what a session leaves in the tree — the collected
// .env-orbit and .orbit-secrets, which hold a secret once
// --set-oidc-secret has run — and keeps the verified scripts, so a
// second attempt in the same run starts clean. The tree itself belongs
// to the run, whose cleanup removes it.
func OpenConfigTree(treeDir string) (endSession func(), err error) {
	if treeDir == "" {
		return nil, ErrNoConfigTree
	}
	if _, err := os.Lstat(filepath.Join(treeDir, configureScript)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoConfigTree
		}
		return nil, fmt.Errorf("configuration tree: %w", err)
	}
	// Run configure.sh only from a path nobody else could have put it
	// in (#191).
	if err := requireTrustedScripts(treeDir, "configure.sh"); err != nil {
		return nil, err
	}
	return func() {
		os.Remove(filepath.Join(treeDir, ".env-orbit"))
		os.RemoveAll(filepath.Join(treeDir, ".orbit-secrets"))
	}, nil
}

// ImportTargetConfig seeds the configure tree with the target's existing
// configuration, so a reconfiguration preserves everything the person
// isn't being asked about. A target with no configuration (fresh
// install) imports nothing.
func ImportTargetConfig(treeDir, targetDir string) error {
	// Nothing is read or tidied in a directory the launcher does not
	// trust (#191). A target that doesn't exist yet has nothing to
	// import.
	if _, err := os.Lstat(targetDir); os.IsNotExist(err) {
		return nil
	}
	if err := requireTrustedConfigDirs(targetDir); err != nil {
		return err
	}
	// A crashed adoption's temp is not a secret; importing it would
	// adopt it back under that name.
	removeStaleTemps(targetDir)
	// Read the configuration only from paths nobody else could have
	// written (#191).
	if err := requireTrustedIfPresent(targetDir, ".env-orbit"); err != nil {
		return err
	}
	if err := copyConfigFile(filepath.Join(targetDir, ".env-orbit"), filepath.Join(treeDir, ".env-orbit")); err != nil {
		return err
	}
	return copySecretsDir(filepath.Join(targetDir, ".orbit-secrets"), filepath.Join(treeDir, ".orbit-secrets"), func(name string) error {
		return RequireTrustedPath(targetDir, filepath.Join(".orbit-secrets", name))
	})
}

// AdoptConfig moves the collected configuration into the target:
// .env-orbit (0600) and .orbit-secrets (0700, entries 0600), creating
// the target directory if this is a fresh install. Every file is first
// staged beside its destination and only renamed in once all of them
// are written, so a failure while saving (disk full, permissions)
// leaves the existing configuration untouched. Target secrets the
// session didn't produce are left in place.
func AdoptConfig(treeDir, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("prepare target: %w", err)
	}
	envSrc := filepath.Join(treeDir, ".env-orbit")
	if _, err := os.Stat(envSrc); err != nil {
		return fmt.Errorf("configuration session left no .env-orbit: %w", err)
	}
	// Nothing is written or tidied in a directory the launcher does not
	// trust (#191).
	if err := requireTrustedConfigDirs(targetDir); err != nil {
		return err
	}
	removeStaleTemps(targetDir)

	plan, err := planAdoption(treeDir, targetDir)
	if err != nil {
		return err
	}

	staged, err := stageAdoption(plan)
	if err != nil {
		return fmt.Errorf("saving the new configuration failed — the existing configuration is unchanged: %w", err)
	}

	// .env-orbit goes last: install.sh's readiness check reads it, so its
	// new values are the signal that the set is complete.
	for i, s := range staged {
		if err := os.Rename(s.tmp, s.final); err != nil {
			for _, rest := range staged[i:] {
				os.Remove(rest.tmp)
			}
			return fmt.Errorf("switching to the new configuration failed partway — check .env-orbit and .orbit-secrets in %s: %w", targetDir, err)
		}
	}
	return nil
}

// adoptionPlan is what an adoption will write, worked out and checked
// before anything in the target is touched.
type adoptionPlan struct {
	// secretsDir is the target's .orbit-secrets, or "" when the session
	// produced no secrets directory (nothing is done to the target's).
	secretsDir string
	// secretsDirExisted records whether secretsDir was already there, so
	// a failed staging can take back one it created.
	secretsDirExisted bool
	// copies are the files to carry, secrets first and .env-orbit last.
	copies []fileCopy
}

type fileCopy struct{ src, dst string }

// planAdoption lists the files to carry and refuses, before anything is
// written, any destination that isn't free or a regular file to replace.
func planAdoption(treeDir, targetDir string) (adoptionPlan, error) {
	var plan adoptionPlan
	envSrc := filepath.Join(treeDir, ".env-orbit")
	if err := requireRegularSource(envSrc); err != nil {
		return plan, err
	}
	envDst := filepath.Join(targetDir, ".env-orbit")
	if err := requireReplaceable(envDst); err != nil {
		return plan, err
	}

	secretsSrc := filepath.Join(treeDir, ".orbit-secrets")
	info, err := os.Lstat(secretsSrc)
	switch {
	case os.IsNotExist(err):
		plan.copies = append(plan.copies, fileCopy{envSrc, envDst})
		return plan, nil
	case err != nil:
		return plan, err
	case !info.IsDir():
		return plan, fmt.Errorf("%s is not a directory", secretsSrc)
	}

	plan.secretsDir = filepath.Join(targetDir, ".orbit-secrets")
	info, err = os.Lstat(plan.secretsDir)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return plan, err
	case !info.IsDir():
		return plan, fmt.Errorf("%s is not a directory", plan.secretsDir)
	default:
		plan.secretsDirExisted = true
	}

	entries, err := os.ReadDir(secretsSrc)
	if err != nil {
		return plan, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		dst := filepath.Join(plan.secretsDir, entry.Name())
		if plan.secretsDirExisted {
			if err := requireReplaceable(dst); err != nil {
				return plan, err
			}
		}
		plan.copies = append(plan.copies, fileCopy{filepath.Join(secretsSrc, entry.Name()), dst})
	}
	plan.copies = append(plan.copies, fileCopy{envSrc, envDst})
	return plan, nil
}

// requireRegularSource refuses a source that isn't a regular file, so a
// symlink is never followed out of the tree.
func requireRegularSource(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// requireReplaceable accepts a destination that doesn't exist yet or is
// a regular file; anything else is refused, never replaced.
func requireReplaceable(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

type stagedFile struct{ tmp, final string }

// stageAdoption writes every planned file to a temp beside its
// destination. These writes are the only steps that can run out of
// space or hit a permission error, and none of them touches a live
// file; on failure every temp written so far is removed.
func stageAdoption(plan adoptionPlan) (staged []stagedFile, err error) {
	defer func() {
		if err == nil {
			return
		}
		for _, s := range staged {
			os.Remove(s.tmp)
		}
		staged = nil
		if plan.secretsDir != "" && !plan.secretsDirExisted {
			os.Remove(plan.secretsDir)
		}
	}()
	if plan.secretsDir != "" {
		if err := os.MkdirAll(plan.secretsDir, 0o700); err != nil {
			return staged, err
		}
		if err := os.Chmod(plan.secretsDir, 0o700); err != nil {
			return staged, err
		}
	}
	for _, c := range plan.copies {
		body, err := os.ReadFile(c.src)
		if err != nil {
			return staged, err
		}
		tmp, err := stageFile(c.dst, body)
		if err != nil {
			return staged, err
		}
		staged = append(staged, stagedFile{tmp: tmp, final: c.dst})
	}
	return staged, nil
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
	return writeFileAtomically(dst, body)
}

// stageFile writes body to a new temp file beside dst and flushes it to
// disk, leaving dst itself untouched. CreateTemp makes the file 0600, so
// a secret never exists at a looser mode, and renaming it over dst
// carries that mode with it.
func stageFile(dst string, body []byte) (tmp string, err error) {
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp = f.Name()
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// writeFileAtomically replaces dst by renaming a complete staged copy
// over it, so dst is always either the whole old file or the whole new
// one — never empty or half-written after a crash.
func writeFileAtomically(dst string, body []byte) error {
	tmp, err := stageFile(dst, body)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// removeStaleTemps clears staging temps an earlier adoption left behind
// when it crashed before switching them in. Best effort: a temp that
// can't be removed is only clutter.
func removeStaleTemps(targetDir string) {
	for _, pattern := range []string{
		filepath.Join(targetDir, ".env-orbit.tmp-*"),
		filepath.Join(targetDir, ".orbit-secrets", "*.tmp-*"),
	} {
		stale, _ := filepath.Glob(pattern)
		for _, path := range stale {
			os.Remove(path)
		}
	}
}

// copySecretsDir copies src's regular files into dst, each only once
// trust(name) accepts it.
func copySecretsDir(src, dst string, trust func(name string) error) error {
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
		if err := trust(entry.Name()); err != nil {
			return err
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

// AuthMode is the sign-in choice the launcher's own screen collects
// before a guided --init (issue #154), passed through to configure.sh
// as ORBIT_CONFIGURE_AUTH_MODE. The zero value means "not chosen yet" —
// the state the very first readiness check is made in, before the
// person has ever seen the question.
type AuthMode string

const (
	// AuthModeLocal asks configure.sh to collect APP_URL only and leave
	// ORBIT_AUTH_OIDC off.
	AuthModeLocal AuthMode = "local"
	// AuthModeOIDC asks configure.sh to collect APP_URL, OIDC_ISSUER and
	// OIDC_CLIENT_ID, same as every --init before this issue.
	AuthModeOIDC AuthMode = "oidc"
)

// BuildConfigureCommand builds one machine-prompt configure run in the
// configure tree. Setsid is load-bearing exactly as it is for the engine
// run: a legacy configure.sh (orbit main) ignores
// ORBIT_CONFIGURE_PROMPTS and would otherwise open /dev/tty and prompt
// straight through the alt screen; detached, it fails fast with no
// protocol line — which is precisely the launcher's signal to fall
// back to the terminal handoff.
//
// mode only matters for the --init step: it is the answer to the
// launcher's own sign-in-mode screen, and configure.sh's machine mode
// honours ORBIT_CONFIGURE_AUTH_MODE to skip asking the OIDC fields
// itself when local accounts were chosen. --set-oidc-secret needs no
// mode — it only ever runs because the previous --init already decided
// OIDC is on.
func BuildConfigureCommand(treeDir string, step ConfigStep, mode AuthMode) *exec.Cmd {
	cmd := detachedCommand(context.Background(), treeDir, "bash", configureScript, string(step))
	env := append(os.Environ(), "ORBIT_CONFIGURE_PROMPTS=machine")
	if step == ConfigStepInit && mode != "" {
		env = append(env, "ORBIT_CONFIGURE_AUTH_MODE="+string(mode))
	}
	cmd.Env = env
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

// RunConfigCheck runs configure.sh --check in the configure tree and
// parses its readiness report. A non-zero exit with a parseable report
// is the normal "something's missing" answer, not an error; an error
// means the check itself couldn't run (structural failure, legacy
// script misbehaviour). Cancelling ctx stops the check and everything
// it started, and returns without waiting on a pipe a child still holds
// (detachedCommand).
func RunConfigCheck(ctx context.Context, treeDir string) (ConfigCheck, error) {
	out, runErr := detachedCommand(ctx, treeDir, "bash", configureScript, "--check").Output()

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
