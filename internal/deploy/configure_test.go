package deploy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeOrbitSource serves an orbit-repo-shaped file tree, the same
// layout the real raw.githubusercontent source has, so the derivation
// from the install.sh override URL is what's actually under test.
func fakeOrbitSource(t *testing.T, files map[string]string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", server.URL+"/scripts/install.sh")
	return server.URL
}

func TestScriptSourceURLs_DerivesFromInstallOverride(t *testing.T) {
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", "https://example.test/repo/scripts/install.sh")
	scripts, root := scriptSourceURLs()
	if scripts != "https://example.test/repo/scripts" {
		t.Fatalf("scripts base = %q", scripts)
	}
	if root != "https://example.test/repo" {
		t.Fatalf("root base = %q", root)
	}
}

func TestScriptSourceURLs_DefaultPointsAtOrbitMain(t *testing.T) {
	scripts, root := scriptSourceURLs()
	if scripts != "https://raw.githubusercontent.com/tomlawesome/orbit/main/scripts" {
		t.Fatalf("scripts base = %q", scripts)
	}
	if root != "https://raw.githubusercontent.com/tomlawesome/orbit/main" {
		t.Fatalf("root base = %q", root)
	}
}

func TestFetchConfigTree_StagesScriptsAndTemplate(t *testing.T) {
	fakeOrbitSource(t, map[string]string{
		"/scripts/configure.sh":     "#!/usr/bin/env bash\necho configure\n",
		"/scripts/configuration.sh": "#!/usr/bin/env bash\necho configuration\n",
		// installer-ui.sh deliberately absent — orbit main doesn't
		// have it, and absence must be tolerated.
		"/.env-orbit.example": "APP_URL=\n",
	})

	treeDir, cleanup, err := FetchConfigTree(context.Background())
	if err != nil {
		t.Fatalf("FetchConfigTree: %v", err)
	}
	defer cleanup()

	for path, wantMode := range map[string]os.FileMode{
		"scripts/configure.sh":     0o700,
		"scripts/configuration.sh": 0o700,
		".env-orbit.example":       0o600,
	} {
		info, err := os.Stat(filepath.Join(treeDir, path))
		if err != nil {
			t.Fatalf("staged %s: %v", path, err)
		}
		if info.Mode().Perm() != wantMode {
			t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), wantMode)
		}
	}
	if _, err := os.Stat(filepath.Join(treeDir, "scripts/installer-ui.sh")); !os.IsNotExist(err) {
		t.Error("expected installer-ui.sh to be absent, not staged empty")
	}

	cleanup()
	if _, err := os.Stat(treeDir); !os.IsNotExist(err) {
		t.Error("cleanup did not remove the staged tree")
	}
}

func TestFetchConfigTree_RequiredScriptMissingFails(t *testing.T) {
	fakeOrbitSource(t, map[string]string{
		"/.env-orbit.example": "APP_URL=\n",
	})
	if _, _, err := FetchConfigTree(context.Background()); err == nil {
		t.Fatal("expected an error when configure.sh is absent")
	}
}

func TestFetchConfigTree_NonScriptContentRefused(t *testing.T) {
	fakeOrbitSource(t, map[string]string{
		"/scripts/configure.sh": "<html>404-but-200</html>",
		"/.env-orbit.example":   "APP_URL=\n",
	})
	if _, _, err := FetchConfigTree(context.Background()); err == nil {
		t.Fatal("expected an error for shebang-less configure.sh")
	}
}

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
	cmd := BuildConfigureCommand("/tmp/tree", ConfigStepInit)
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
