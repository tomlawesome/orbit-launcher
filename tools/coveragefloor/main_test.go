package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", p, err)
	}
	return p
}

const samplePassingProfile = `mode: atomic
example.com/mod/pkg/a.go:1.1,2.2 10 10
`

const samplePassingFloors = `example.com/mod/pkg 100.0
`

const sampleFailingProfile = `mode: atomic
example.com/mod/pkg/a.go:1.1,2.2 10 0
`

func TestRun_PassExitsZero(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile)
	floors := writeTemp(t, "floors.txt", samplePassingFloors)
	var stdout, stderr bytes.Buffer

	code := run(profile, floors, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example.com/mod/pkg") {
		t.Errorf("stdout missing package report: %q", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr should be empty on success, got %q", stderr.String())
	}
}

func TestRun_RegressionExitsOne(t *testing.T) {
	profile := writeTemp(t, "coverage.out", sampleFailingProfile)
	floors := writeTemp(t, "floors.txt", samplePassingFloors)
	var stdout, stderr bytes.Buffer

	code := run(profile, floors, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "example.com/mod/pkg") {
		t.Errorf("stderr missing the failing package: %q", stderr.String())
	}
}

func TestRun_MissingProfileExitsTwo(t *testing.T) {
	floors := writeTemp(t, "floors.txt", samplePassingFloors)
	var stdout, stderr bytes.Buffer

	code := run(filepath.Join(t.TempDir(), "does-not-exist.out"), floors, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "coveragefloor:") {
		t.Errorf("stderr missing the command's error prefix: %q", stderr.String())
	}
}

func TestRun_MissingFloorsFileExitsTwo(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile)
	var stdout, stderr bytes.Buffer

	code := run(profile, filepath.Join(t.TempDir(), "does-not-exist.txt"), &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%s", code, stderr.String())
	}
}
