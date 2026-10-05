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

// A package the profile measures but the floors file does not name is
// reported, and passes: a floor is only ever added by review (see
// package floor).
func TestRun_PackageWithoutFloorIsReportedAndPasses(t *testing.T) {
	profile := writeTemp(t, "coverage.out", samplePassingProfile+"example.com/mod/other/b.go:1.1,2.2 4 0\n")
	floors := writeTemp(t, "floors.txt", samplePassingFloors)
	var stdout, stderr bytes.Buffer

	code := run(profile, floors, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example.com/mod/other") || !strings.Contains(stdout.String(), "got   0.0%  (no floor set)") {
		t.Errorf("stdout does not report the unfloored package as such: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "1 package(s) checked against "+floors) {
		t.Errorf("stdout's summary does not name the one floor and its file: %q", stdout.String())
	}
}

// Input that cannot be parsed fails closed with exit 2, never a pass.
func TestRun_MalformedInputExitsTwo(t *testing.T) {
	cases := map[string][2]string{
		"profile with no mode header": {"example.com/mod/pkg/a.go:1.1,2.2 10 10\n", samplePassingFloors},
		"floor that is not a number":  {samplePassingProfile, "example.com/mod/pkg high\n"},
	}
	for name, in := range cases {
		var stdout, stderr bytes.Buffer
		code := run(writeTemp(t, "coverage.out", in[0]), writeTemp(t, "floors.txt", in[1]), &stdout, &stderr)
		if code != 2 || !strings.HasPrefix(stderr.String(), "coveragefloor: ") {
			t.Errorf("%s: exit code = %d, stderr %q; want 2 and the error", name, code, stderr.String())
		}
	}
}

func TestCLI_WrongArgumentCountIsAUsageError(t *testing.T) {
	for _, args := range [][]string{{"coveragefloor"}, {"coveragefloor", "a", "b", "c"}} {
		var stdout, stderr bytes.Buffer
		code := cli(args, &stdout, &stderr)
		if code != 2 || !strings.HasPrefix(stderr.String(), "usage: coveragefloor") || stdout.Len() != 0 {
			t.Errorf("%q: exit code = %d, stdout %q, stderr %q; want 2 and the usage line", args, code, stdout.String(), stderr.String())
		}
	}
}

// With only a profile, the floors come from .github/coverage-floors.txt
// in the directory it runs in -- the module root, as CI runs it; a
// second argument replaces that file.
func TestCLI_FloorsFileDefaultsToTheRepoOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, defaultFloorsPath), []byte(samplePassingFloors), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	profile := writeTemp(t, "coverage.out", samplePassingProfile)
	var stdout, stderr bytes.Buffer

	if code := cli([]string{"coveragefloor", profile}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "checked against "+defaultFloorsPath) {
		t.Fatalf("default floors: exit code = %d, stdout %q, stderr %q; want 0 against %s", code, stdout.String(), stderr.String(), defaultFloorsPath)
	}

	stricter := writeTemp(t, "floors.txt", "example.com/mod/pkg 100.0\nexample.com/mod/gone 50.0\n")
	stdout.Reset()
	stderr.Reset()
	if code := cli([]string{"coveragefloor", profile, stricter}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "example.com/mod/gone") {
		t.Fatalf("explicit floors: exit code = %d, stderr %q; want 1 and the stale floor, proving the second argument was read", code, stderr.String())
	}
}
