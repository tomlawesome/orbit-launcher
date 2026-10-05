package deploy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestImportAndAdoptConfig_RoundTripWithModes(t *testing.T) {
	treeDir := t.TempDir()
	targetDir := t.TempDir()

	// A target with existing configuration, permissions deliberately
	// looser than the contract to prove they're restored on copy.
	if err := os.WriteFile(filepath.Join(targetDir, ".env-orbit"), []byte("APP_URL=https://kept.example\nEXTRA=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(targetDir, ".orbit-secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ImportTargetConfig(treeDir, targetDir); err != nil {
		t.Fatalf("ImportTargetConfig: %v", err)
	}
	imported, err := os.ReadFile(filepath.Join(treeDir, ".env-orbit"))
	if err != nil || !strings.Contains(string(imported), "EXTRA=1") {
		t.Fatalf("imported .env-orbit lost content: %q err=%v", imported, err)
	}

	// The configure session edits the tree's copy; adoption carries it
	// back with contract modes.
	if err := os.WriteFile(filepath.Join(treeDir, ".env-orbit"), []byte("APP_URL=https://new.example\nEXTRA=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AdoptConfig(treeDir, targetDir); err != nil {
		t.Fatalf("AdoptConfig: %v", err)
	}

	adopted, err := os.ReadFile(filepath.Join(targetDir, ".env-orbit"))
	if err != nil || !strings.Contains(string(adopted), "https://new.example") {
		t.Fatalf("adopted .env-orbit wrong: %q err=%v", adopted, err)
	}
	info, _ := os.Stat(filepath.Join(targetDir, ".env-orbit"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env-orbit mode = %o, want 600", info.Mode().Perm())
	}
	info, _ = os.Stat(filepath.Join(targetDir, ".orbit-secrets"))
	if info.Mode().Perm() != 0o700 {
		t.Errorf(".orbit-secrets mode = %o, want 700", info.Mode().Perm())
	}
	info, _ = os.Stat(filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secret mode = %o, want 600", info.Mode().Perm())
	}
}

func TestAdoptConfig_CreatesFreshTarget(t *testing.T) {
	treeDir := t.TempDir()
	targetDir := filepath.Join(t.TempDir(), "brand-new")
	if err := os.WriteFile(filepath.Join(treeDir, ".env-orbit"), []byte("APP_URL=https://x.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AdoptConfig(treeDir, targetDir); err != nil {
		t.Fatalf("AdoptConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, ".env-orbit")); err != nil {
		t.Fatalf("expected .env-orbit in the fresh target: %v", err)
	}
}

func TestAdoptConfig_NoEnvOrbitIsAnError(t *testing.T) {
	if err := AdoptConfig(t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("expected an error when the session produced no .env-orbit")
	}
}

// fakeCheckTree writes a minimal tree whose configure.sh --check
// prints the given report.
func fakeCheckTree(t *testing.T, report string, exit int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/usr/bin/env bash\nprintf '%s'\nexit %d\n", report, exit)
	if err := os.WriteFile(filepath.Join(dir, "scripts", "configure.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunConfigCheck_ParsesMissingFields(t *testing.T) {
	dir := fakeCheckTree(t, `ready APP_URL\nmissing ORBIT_IMAGE\nmissing OIDC_CLIENT_SECRET\noptional ai\n`, 1)
	check, err := RunConfigCheck(context.Background(), dir)
	if err != nil {
		t.Fatalf("RunConfigCheck: %v", err)
	}
	if check.NeedsInit() {
		t.Error("NeedsInit should be false — no guided field missing")
	}
	if !check.NeedsSecret() {
		t.Error("NeedsSecret should be true")
	}
	if len(check.Unfixable()) != 0 {
		t.Errorf("ORBIT_IMAGE is install.sh's to fill — Unfixable = %v", check.Unfixable())
	}
}

func TestRunConfigCheck_GuidedAndUnfixable(t *testing.T) {
	dir := fakeCheckTree(t, `missing APP_URL\nmissing SOME_NEW_REQUIRED_FIELD\n`, 1)
	check, err := RunConfigCheck(context.Background(), dir)
	if err != nil {
		t.Fatalf("RunConfigCheck: %v", err)
	}
	if !check.NeedsInit() {
		t.Error("NeedsInit should be true")
	}
	unfixable := check.Unfixable()
	if len(unfixable) != 1 || unfixable[0] != "SOME_NEW_REQUIRED_FIELD" {
		t.Errorf("Unfixable = %v", unfixable)
	}
}

func TestRunConfigCheck_AllReady(t *testing.T) {
	dir := fakeCheckTree(t, `ready APP_URL\nready OIDC_CLIENT_SECRET\n`, 0)
	check, err := RunConfigCheck(context.Background(), dir)
	if err != nil {
		t.Fatalf("RunConfigCheck: %v", err)
	}
	if check.NeedsInit() || check.NeedsSecret() || len(check.Unfixable()) > 0 {
		t.Errorf("expected a clean check, got %+v", check)
	}
}

func TestRunConfigCheck_NoReportIsAnError(t *testing.T) {
	dir := fakeCheckTree(t, `Orbit configuration: something structural broke\n`, 1)
	if _, err := RunConfigCheck(context.Background(), dir); err == nil {
		t.Fatal("expected an error when the check produced no readiness report")
	}
}

func TestBuildConfigureCommand_Shape(t *testing.T) {
	cmd := BuildConfigureCommand("/tmp/tree", ConfigStepInit, AuthModeLocal)
	want := []string{"bash", "scripts/configure.sh", "--init"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %v", cmd.Args)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", cmd.Args, want)
		}
	}
	if cmd.Dir != "/tmp/tree" {
		t.Errorf("dir = %q", cmd.Dir)
	}
	machine := false
	for _, e := range cmd.Env {
		if e == "ORBIT_CONFIGURE_PROMPTS=machine" {
			machine = true
		}
	}
	if !machine {
		t.Error("ORBIT_CONFIGURE_PROMPTS=machine missing from the environment")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("Setsid must be set — a legacy configure.sh would otherwise reach /dev/tty")
	}
}

// envHas reports whether cmd's environment carries exactly this entry.
func envHas(cmd *exec.Cmd, entry string) bool {
	for _, e := range cmd.Env {
		if e == entry {
			return true
		}
	}
	return false
}

func TestBuildConfigureCommand_InitCarriesAuthModeEnv(t *testing.T) {
	local := BuildConfigureCommand("/tmp/tree", ConfigStepInit, AuthModeLocal)
	if !envHas(local, "ORBIT_CONFIGURE_AUTH_MODE=local") {
		t.Errorf("local --init env = %v, missing ORBIT_CONFIGURE_AUTH_MODE=local", local.Env)
	}

	oidc := BuildConfigureCommand("/tmp/tree", ConfigStepInit, AuthModeOIDC)
	if !envHas(oidc, "ORBIT_CONFIGURE_AUTH_MODE=oidc") {
		t.Errorf("oidc --init env = %v, missing ORBIT_CONFIGURE_AUTH_MODE=oidc", oidc.Env)
	}
}

// TestBuildConfigureCommand_SecretStepOmitsAuthModeEnv: the secret step
// only ever runs because a prior --init already decided OIDC is on, so
// it needs no mode of its own — and must not pick up a stray local one
// from the same session.
func TestBuildConfigureCommand_SecretStepOmitsAuthModeEnv(t *testing.T) {
	cmd := BuildConfigureCommand("/tmp/tree", ConfigStepSecret, AuthModeLocal)
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "ORBIT_CONFIGURE_AUTH_MODE=") {
			t.Errorf("--set-oidc-secret env carried a mode: %v", cmd.Env)
		}
	}
}

// TestBuildConfigureCommand_UnknownModeOmitsEnv: an --init built before
// the sign-in screen has an answer (mode "") must not invent a mode —
// the launcher never calls this with an unknown mode for --init in
// practice, but the command builder itself should stay honest either way.
func TestBuildConfigureCommand_UnknownModeOmitsEnv(t *testing.T) {
	cmd := BuildConfigureCommand("/tmp/tree", ConfigStepInit, "")
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "ORBIT_CONFIGURE_AUTH_MODE=") {
			t.Errorf("an unknown mode must not set ORBIT_CONFIGURE_AUTH_MODE: %v", cmd.Env)
		}
	}
}

// lockDir removes every permission from dir for the rest of the test, so
// anything inside it can neither be listed, stat'ed nor read, and restores
// them afterwards so t.TempDir's own cleanup can still remove it.
func lockDir(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions don't block access")
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}

// truncatedBodyServer promises more bytes than it sends, so the client's
// read fails part-way through — what a dropped connection looks like.
func truncatedBodyServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.Write([]byte("#!/usr/bin/env bash\necho half a scr"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A fresh install has no configuration to carry over, and that is not
// an error: the tree simply stays unseeded.
func TestImportTargetConfig_FreshTargetImportsNothing(t *testing.T) {
	treeDir := t.TempDir()
	if err := ImportTargetConfig(treeDir, t.TempDir()); err != nil {
		t.Fatalf("ImportTargetConfig: %v", err)
	}
	for _, name := range []string{".env-orbit", ".orbit-secrets"} {
		if _, err := os.Lstat(filepath.Join(treeDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s appeared in the tree from an empty target (err=%v)", name, err)
		}
	}
}

// A symlinked .env-orbit is refused rather than followed, so the launcher
// can never be steered into copying (and later writing back) some other
// file on the machine.
func TestImportTargetConfig_RefusesASymlinkedEnvFile(t *testing.T) {
	treeDir := t.TempDir()
	targetDir := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(elsewhere, []byte("NOT_ORBIT=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(targetDir, ".env-orbit")); err != nil {
		t.Fatal(err)
	}

	err := ImportTargetConfig(treeDir, targetDir)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected a not-a-regular-file refusal, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(treeDir, ".env-orbit")); !os.IsNotExist(err) {
		t.Error("the symlink's target was copied into the tree")
	}
}

func TestImportTargetConfig_UnreadableEnvFileIsAnError(t *testing.T) {
	targetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetDir, ".env-orbit"), []byte("APP_URL=x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions don't block reads")
	}
	treeDir := t.TempDir()
	if err := ImportTargetConfig(treeDir, targetDir); err == nil {
		t.Fatal("expected an error for an unreadable .env-orbit")
	}
	if _, err := os.Lstat(filepath.Join(treeDir, ".env-orbit")); !os.IsNotExist(err) {
		t.Error("an unreadable source still produced a copy in the tree")
	}
}

// An inaccessible target is an error, not "fresh install": treating it
// as empty would start a reconfiguration from nothing over a real one.
func TestImportTargetConfig_InaccessibleTargetIsAnErrorNotAFreshInstall(t *testing.T) {
	targetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetDir, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockDir(t, targetDir)
	if err := ImportTargetConfig(t.TempDir(), targetDir); err == nil {
		t.Fatal("expected an error for a target that cannot be inspected")
	}
}

func TestImportTargetConfig_RefusesASecretsPathThatIsNotADirectory(t *testing.T) {
	targetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetDir, ".orbit-secrets"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ImportTargetConfig(t.TempDir(), targetDir)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected a not-a-directory refusal, got %v", err)
	}
}

// Only regular files are carried: a symlink or subdirectory inside the
// secrets directory is skipped, never followed.
func TestImportTargetConfig_CopiesOnlyRegularSecretFiles(t *testing.T) {
	targetDir := t.TempDir()
	secrets := filepath.Join(targetDir, ".orbit-secrets")
	if err := os.MkdirAll(filepath.Join(secrets, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "oidc-client-secret"), []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "private-key")
	if err := os.WriteFile(elsewhere, []byte("not orbit's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(secrets, "linked")); err != nil {
		t.Fatal(err)
	}

	treeDir := t.TempDir()
	if err := ImportTargetConfig(treeDir, targetDir); err != nil {
		t.Fatalf("ImportTargetConfig: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(treeDir, ".orbit-secrets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "oidc-client-secret" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("tree secrets = %v, want only [oidc-client-secret]", names)
	}
}

func TestImportTargetConfig_UnreadableSecretsDirIsAnError(t *testing.T) {
	targetDir := t.TempDir()
	secrets := filepath.Join(targetDir, ".orbit-secrets")
	if err := os.Mkdir(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	lockDir(t, secrets)
	if err := ImportTargetConfig(t.TempDir(), targetDir); err == nil {
		t.Fatal("expected an error for a secrets directory that cannot be listed")
	}
}

func TestImportTargetConfig_UnreadableSecretFileIsAnError(t *testing.T) {
	targetDir := t.TempDir()
	secrets := filepath.Join(targetDir, ".orbit-secrets")
	if err := os.Mkdir(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "oidc-client-secret"), []byte("s\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions don't block reads")
	}
	if err := ImportTargetConfig(t.TempDir(), targetDir); err == nil {
		t.Fatal("expected an error for a secret that cannot be read")
	}
}

func TestCopySecretsDir_UninspectableSourceIsAnError(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, ".orbit-secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	lockDir(t, parent)
	dst := filepath.Join(t.TempDir(), ".orbit-secrets")
	if err := copySecretsDir(filepath.Join(parent, ".orbit-secrets"), dst); err == nil {
		t.Fatal("expected an error when the source cannot be inspected")
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Error("a failed inspection still created the destination")
	}
}

func TestAdoptConfig_TargetUnderAFileIsAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	treeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(treeDir, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := AdoptConfig(treeDir, filepath.Join(file, "orbit"))
	if err == nil || !strings.Contains(err.Error(), "prepare target") {
		t.Fatalf("expected a prepare-target error, got %v", err)
	}
}

// If the configuration can't be written into the target, adoption stops
// there: the secrets are not carried over on their own.
func TestAdoptConfig_UnwritableEnvFileStopsBeforeTheSecrets(t *testing.T) {
	treeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(treeDir, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(treeDir, ".orbit-secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(treeDir, ".orbit-secrets", "oidc-client-secret"), []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetDir := t.TempDir()
	// A directory where the file should go makes the write fail.
	if err := os.Mkdir(filepath.Join(targetDir, ".env-orbit"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := AdoptConfig(treeDir, targetDir); err == nil {
		t.Fatal("expected an error when .env-orbit cannot be written")
	}
	if _, err := os.Lstat(filepath.Join(targetDir, ".orbit-secrets")); !os.IsNotExist(err) {
		t.Error("secrets were adopted even though .env-orbit failed")
	}
}

// A regular file sitting where the secrets directory belongs is refused,
// not replaced.
func TestAdoptConfig_LeavesAFileInTheSecretsPlaceAlone(t *testing.T) {
	treeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(treeDir, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(treeDir, ".orbit-secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	targetDir := t.TempDir()
	blocker := filepath.Join(targetDir, ".orbit-secrets")
	if err := os.WriteFile(blocker, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := AdoptConfig(treeDir, targetDir); err == nil {
		t.Fatal("expected an error when .orbit-secrets is a file in the target")
	}
	if body, err := os.ReadFile(blocker); err != nil || string(body) != "keep me\n" {
		t.Errorf("the file in the secrets place was changed: %q err=%v", body, err)
	}
}

// writeTestFile writes body at path with mode, creating parents.
func writeTestFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is filtered by the umask; set it exactly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// assertNoTemps fails if any staging temp is left beside the live files.
func assertNoTemps(t *testing.T, targetDir string) {
	t.Helper()
	for _, pattern := range []string{
		filepath.Join(targetDir, ".env-orbit.tmp-*"),
		filepath.Join(targetDir, ".orbit-secrets", "*.tmp-*"),
	} {
		if left, _ := filepath.Glob(pattern); len(left) != 0 {
			t.Errorf("temp files left behind: %v", left)
		}
	}
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

// A secret that cannot be carried over stops the adoption before any
// live file is touched: the working configuration stays exactly as it
// was, and no half-written temp is left beside it.
func TestAdoptConfig_SecretFailureLeavesTheLiveConfigUntouched(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions don't block reads")
	}
	targetDir := t.TempDir()
	writeTestFile(t, filepath.Join(targetDir, ".env-orbit"), "APP_URL=https://kept.example\nOIDC_CLIENT_ID=old\n", 0o600)
	writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret"), "old-secret\n", 0o600)

	treeDir := t.TempDir()
	writeTestFile(t, filepath.Join(treeDir, ".env-orbit"), "APP_URL=https://kept.example\nOIDC_CLIENT_ID=new\n", 0o600)
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "oidc-client-secret"), "new-secret\n", 0o600)
	// Sorts after the real secret, so a copy-as-you-go adoption has
	// already overwritten .env-orbit and the first secret by the time it
	// fails here.
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "zz-unreadable"), "x\n", 0o000)

	err := AdoptConfig(treeDir, targetDir)
	if err == nil {
		t.Fatal("expected an error for a secret that cannot be read")
	}
	if env, _ := os.ReadFile(filepath.Join(targetDir, ".env-orbit")); !strings.Contains(string(env), "OIDC_CLIENT_ID=old") {
		t.Errorf(".env-orbit was changed by a failed adoption: %q", env)
	}
	if secret, _ := os.ReadFile(filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret")); string(secret) != "old-secret\n" {
		t.Errorf("secret was changed by a failed adoption: %q", secret)
	}
	if !strings.Contains(err.Error(), "existing configuration is unchanged") {
		t.Errorf("error does not say the existing configuration is unchanged: %v", err)
	}
	assertNoTemps(t, targetDir)
}

// Live files are replaced by renaming a complete new file over them, so
// at every instant each one is either the whole old file or the whole
// new one — never truncated in place.
func TestAdoptConfig_ReplacesLiveFilesByRename(t *testing.T) {
	targetDir := t.TempDir()
	envPath := filepath.Join(targetDir, ".env-orbit")
	secretPath := filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret")
	writeTestFile(t, envPath, "APP_URL=https://old.example\n", 0o644)
	writeTestFile(t, secretPath, "old-secret\n", 0o644)
	envBefore, secretBefore := inode(t, envPath), inode(t, secretPath)

	treeDir := t.TempDir()
	writeTestFile(t, filepath.Join(treeDir, ".env-orbit"), "APP_URL=https://new.example\n", 0o600)
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "oidc-client-secret"), "new-secret\n", 0o600)

	if err := AdoptConfig(treeDir, targetDir); err != nil {
		t.Fatalf("AdoptConfig: %v", err)
	}
	for path, want := range map[string]string{envPath: "APP_URL=https://new.example\n", secretPath: "new-secret\n"} {
		if body, err := os.ReadFile(path); err != nil || string(body) != want {
			t.Errorf("%s = %q err=%v, want %q", path, body, err, want)
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
	if inode(t, envPath) == envBefore {
		t.Error(".env-orbit was rewritten in place, not replaced by rename")
	}
	if inode(t, secretPath) == secretBefore {
		t.Error("the secret was rewritten in place, not replaced by rename")
	}
	assertNoTemps(t, targetDir)
}

// Temps left by a crash mid-adoption are swept on the next one.
func TestAdoptConfig_ClearsAStaleTempFromAnEarlierCrash(t *testing.T) {
	targetDir := t.TempDir()
	writeTestFile(t, filepath.Join(targetDir, ".env-orbit.tmp-stale"), "APP_URL=half\n", 0o600)
	writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret.tmp-stale"), "half\n", 0o600)

	treeDir := t.TempDir()
	writeTestFile(t, filepath.Join(treeDir, ".env-orbit"), "APP_URL=https://x.example\n", 0o600)
	writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "oidc-client-secret"), "s\n", 0o600)

	if err := AdoptConfig(treeDir, targetDir); err != nil {
		t.Fatalf("AdoptConfig: %v", err)
	}
	assertNoTemps(t, targetDir)
	if body, _ := os.ReadFile(filepath.Join(targetDir, ".env-orbit")); string(body) != "APP_URL=https://x.example\n" {
		t.Errorf(".env-orbit = %q", body)
	}
	if body, _ := os.ReadFile(filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret")); string(body) != "s\n" {
		t.Errorf("secret = %q", body)
	}
}

// A temp left by a crashed adoption is not a secret: importing it would
// carry it into the tree and adopt it back under that name.
func TestImportTargetConfig_IgnoresAStaleTempSecret(t *testing.T) {
	targetDir := t.TempDir()
	writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret"), "s\n", 0o600)
	writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets", "oidc-client-secret.tmp-stale"), "half\n", 0o600)

	treeDir := t.TempDir()
	if err := ImportTargetConfig(treeDir, targetDir); err != nil {
		t.Fatalf("ImportTargetConfig: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(treeDir, ".orbit-secrets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "oidc-client-secret" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("tree secrets = %v, want only [oidc-client-secret]", names)
	}
}

func TestRunConfigCheck_CleanExitWithNoReportIsAnError(t *testing.T) {
	dir := fakeCheckTree(t, `all good, probably\n`, 0)
	_, err := RunConfigCheck(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "no readiness report") {
		t.Fatalf("expected a no-readiness-report error, got %v", err)
	}
}

// handedOverTree is a configure tree as install.sh leaves it in
// ORBIT_LAUNCHER_CONFIG_TREE on a configuration refusal (ai/orbit#1225).
func handedOverTree(t *testing.T) string {
	t.Helper()
	tree := t.TempDir()
	if err := os.Mkdir(filepath.Join(tree, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"scripts/configure.sh":     "#!/usr/bin/env bash\necho configure\n",
		"scripts/configuration.sh": "#!/usr/bin/env bash\n",
		".env-orbit.example":       "APP_URL=\n",
	} {
		if err := os.WriteFile(filepath.Join(tree, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tree
}

// #190: the configure tree is what install.sh handed over, never a
// download — opening it fetches nothing and writes nothing.
func TestOpenConfigTree_UsesTheHandedOverTreeWithoutFetching(t *testing.T) {
	requests := noScriptSource(t)
	tree := handedOverTree(t)

	endSession, err := OpenConfigTree(tree)
	if err != nil {
		t.Fatalf("OpenConfigTree: %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("opening the configure tree made %d HTTP requests", n)
	}

	// What a session leaves behind — the collected configuration and
	// the secret — goes when it ends; the verified scripts stay for a
	// second attempt in the same run, and the tree itself is the run's
	// to remove.
	if err := os.WriteFile(filepath.Join(tree, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, ".orbit-secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".orbit-secrets", "oidc-client-secret"), []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	endSession()
	for _, gone := range []string{".env-orbit", ".orbit-secrets"} {
		if _, err := os.Lstat(filepath.Join(tree, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the end of the session", gone)
		}
	}
	for _, kept := range []string{"scripts/configure.sh", ".env-orbit.example"} {
		if _, err := os.Lstat(filepath.Join(tree, kept)); err != nil {
			t.Errorf("%s was removed with the session: %v", kept, err)
		}
	}
}

// An install.sh without ai/orbit#1225 leaves the directory empty: there
// is nothing verified to run, so the caller falls back to the terminal
// handoff rather than fetching anything.
func TestOpenConfigTree_EmptyTreeIsNoTree(t *testing.T) {
	requests := noScriptSource(t)
	endSession, err := OpenConfigTree(t.TempDir())
	if !errors.Is(err, ErrNoConfigTree) {
		t.Fatalf("err = %v, want ErrNoConfigTree", err)
	}
	if endSession != nil {
		t.Error("a missing tree returned a session")
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("an empty tree made %d HTTP requests; there is no fallback fetch", n)
	}
}

func TestOpenConfigTree_NoTreeAtAll(t *testing.T) {
	if _, err := OpenConfigTree(""); !errors.Is(err, ErrNoConfigTree) {
		t.Fatalf("err = %v, want ErrNoConfigTree", err)
	}
}
