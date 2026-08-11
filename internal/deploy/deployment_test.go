package deploy

import (
	"os"
	"path/filepath"
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
	want := "docker compose --project-directory /opt/orbit down -v && sudo rm -rf /opt/orbit"
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
