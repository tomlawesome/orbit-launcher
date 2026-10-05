package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect_ReturnsNilWithoutErrorWhenNotInstalled(t *testing.T) {
	d, err := Detect(t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d != nil {
		t.Errorf("Detect on an empty directory = %+v, want nil", d)
	}
}

func TestDetect_ParsesRecognisedFields(t *testing.T) {
	dir := t.TempDir()
	env := "APP_URL=https://mail.example.com\n" +
		"COMPOSE_PROFILES=processing,ai\n" +
		"ORBIT_IMAGE=ghcr.io/tomlawesome/orbit@sha256:abc\n" +
		"# a comment\n" +
		"\n" +
		"POSTGRES_DB=orbit\n"
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(env), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d == nil {
		t.Fatal("Detect = nil, want a recognised deployment")
	}
	if d.AppURL != "https://mail.example.com" {
		t.Errorf("AppURL = %q, want https://mail.example.com", d.AppURL)
	}
	if got, want := d.Profiles, []string{"processing", "ai"}; !equalStrings(got, want) {
		t.Errorf("Profiles = %v, want %v", got, want)
	}
	if d.Image != "ghcr.io/tomlawesome/orbit@sha256:abc" {
		t.Errorf("Image = %q, want the fixture image", d.Image)
	}
	if d.InstalledAt.IsZero() {
		t.Error("InstalledAt should be set from the file's mtime")
	}
}

func TestDetect_EmptyProfilesIsNil(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte("COMPOSE_PROFILES=\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(d.Profiles) != 0 {
		t.Errorf("Profiles = %v, want empty", d.Profiles)
	}
}

func TestRemovalCommand_IsExactAndScopedToTheTarget(t *testing.T) {
	got := RemovalCommand("/opt/orbit")
	want := "docker compose --project-directory /opt/orbit --env-file /opt/orbit/.env-orbit down -v && sudo rm -rf /opt/orbit"
	if got != want {
		t.Errorf("RemovalCommand(%q) = %q, want %q", "/opt/orbit", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDetect_ReadsTheAppliedVersionTrimmed(t *testing.T) {
	dir := t.TempDir()
	env := "ORBIT_CONFIG_APPLIED_VERSION= v1.2.0 \n"
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(env), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.Version != "v1.2.0" {
		t.Errorf("Version = %q, want v1.2.0", d.Version)
	}
}

// A line with no "=" is skipped, not fatal: one stray line must not hide
// the rest of a real deployment.
func TestDetect_SkipsLinesWithoutAnEquals(t *testing.T) {
	dir := t.TempDir()
	env := "export\nAPP_URL=https://orbit.example.com\n"
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(env), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d.AppURL != "https://orbit.example.com" {
		t.Errorf("AppURL = %q, want the line after the stray one", d.AppURL)
	}
}

// A directory that can't be inspected is an error, not "nothing
// installed": reading it as a fresh target would offer to install over
// a deployment the launcher simply couldn't see.
func TestDetect_InaccessibleTargetIsAnErrorNotAbsence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte("APP_URL=x\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	lockDir(t, dir)
	d, err := Detect(dir)
	if err == nil {
		t.Fatal("expected an error for a target that cannot be inspected")
	}
	if d != nil {
		t.Errorf("Detect = %+v, want nil alongside the error", d)
	}
}

func TestDetect_UnreadableEnvFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte("APP_URL=x\n"), 0o000); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions don't block reads")
	}
	d, err := Detect(dir)
	if err == nil {
		t.Fatal("expected an error for an unreadable .env-orbit")
	}
	if d != nil {
		t.Errorf("Detect = %+v, want nil alongside the error", d)
	}
}

// A line longer than the scanner's buffer stops the read: reporting the
// half-parsed deployment would show the wrong URL or image.
func TestDetect_OverlongLineIsAnErrorNotAPartialDeployment(t *testing.T) {
	dir := t.TempDir()
	env := "APP_URL=https://orbit.example.com\nNOTES=" + strings.Repeat("x", 70*1024) + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(env), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	d, err := Detect(dir)
	if err == nil {
		t.Fatal("expected an error for a line past the scanner's limit")
	}
	if d != nil {
		t.Errorf("Detect = %+v, want nil alongside the error", d)
	}
}
