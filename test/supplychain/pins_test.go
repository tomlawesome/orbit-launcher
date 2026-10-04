// Pins that must move together: the Go toolchain and Playwright. #161 showed
// that Renovate bumps a job image without its companions -- STATICCHECK_VERSION
// for Go, the lockfile for Playwright -- and the pipeline fails with an error
// that reads like a broken tool rather than a version skew. These tests make
// that failure plain and name what to change, instead of leaving it to the
// first job that trips over the mismatch.
package supplychain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// root is declared in policy_test.go, in this same package.

var (
	goImagePattern = regexp.MustCompile(
		`(?m)^\s*GO_IMAGE:\s*golang:(\d+\.\d+\.\d+)-[\w.]+@sha256:[0-9a-f]{64}\s*$`)
	goVersionVarPattern = regexp.MustCompile(
		`(?m)^\s*GO_VERSION:\s*"(\d+\.\d+\.\d+)"\s*$`)
	goModVersionPattern = regexp.MustCompile(
		`(?m)^go (\d+\.\d+\.\d+)\s*$`)
	playwrightImagePattern = regexp.MustCompile(
		`(?m)^\s*PLAYWRIGHT_IMAGE:\s*mcr\.microsoft\.com/playwright:v(\d+\.\d+\.\d+)-[\w.]+@sha256:[0-9a-f]{64}\s*$`)
	exactSemver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// goImageVersion reads the Go release named in GO_IMAGE's tag, e.g.
// "golang:1.27.1-bookworm@sha256:..." gives "1.27.1".
func goImageVersion(ciYAML string) (string, error) {
	m := goImagePattern.FindStringSubmatch(ciYAML)
	if m == nil {
		return "", fmt.Errorf("no GO_IMAGE line matching golang:X.Y.Z-suffix@sha256:<64 hex>")
	}
	return m[1], nil
}

// goVersionVar reads the GO_VERSION pipeline variable, e.g. `"1.27.1"`.
func goVersionVar(ciYAML string) (string, error) {
	m := goVersionVarPattern.FindStringSubmatch(ciYAML)
	if m == nil {
		return "", fmt.Errorf(`no GO_VERSION: "X.Y.Z" line`)
	}
	return m[1], nil
}

// goModVersion reads go.mod's `go` directive, e.g. "go 1.27.1".
func goModVersion(modFile string) (string, error) {
	m := goModVersionPattern.FindStringSubmatch(modFile)
	if m == nil {
		return "", fmt.Errorf("no go X.Y.Z directive")
	}
	return m[1], nil
}

// playwrightImageVersion reads the Playwright release named in
// PLAYWRIGHT_IMAGE's tag, e.g. "mcr.microsoft.com/playwright:v1.63.0-noble@sha256:..."
// gives "1.63.0".
func playwrightImageVersion(ciYAML string) (string, error) {
	m := playwrightImagePattern.FindStringSubmatch(ciYAML)
	if m == nil {
		return "", fmt.Errorf("no PLAYWRIGHT_IMAGE line matching mcr.microsoft.com/playwright:vX.Y.Z-suffix@sha256:<64 hex>")
	}
	return m[1], nil
}

// playwrightPackageJSONVersion reads @playwright/test's devDependency entry,
// stripping a leading ^ or ~. What remains must be an exact X.Y.Z version: a
// real range (a wildcard, a comparator, an x-range) cannot be compared to the
// image or the lockfile, so it is itself a failure.
func playwrightPackageJSONVersion(pkgJSON string) (string, error) {
	var pkg struct {
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(pkgJSON), &pkg); err != nil {
		return "", fmt.Errorf("parsing package.json: %w", err)
	}
	raw, ok := pkg.DevDependencies["@playwright/test"]
	if !ok {
		return "", fmt.Errorf("no @playwright/test devDependency")
	}
	stripped := strings.TrimLeft(raw, "^~")
	if !exactSemver.MatchString(stripped) {
		return "", fmt.Errorf(
			"@playwright/test is pinned to %q, a range rather than an exact version; "+
				"pin an exact X.Y.Z so it can be checked against the image and the lockfile", raw)
	}
	return stripped, nil
}

// playwrightLockVersion reads node_modules/@playwright/test's resolved
// version from package-lock.json.
func playwrightLockVersion(lockJSON string) (string, error) {
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(lockJSON), &lock); err != nil {
		return "", fmt.Errorf("parsing package-lock.json: %w", err)
	}
	pkg, ok := lock.Packages["node_modules/@playwright/test"]
	if !ok || pkg.Version == "" {
		return "", fmt.Errorf("no node_modules/@playwright/test entry with a version")
	}
	return pkg.Version, nil
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{root}, parts...)...)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	return string(body)
}

// TestGoPinsAgree is the check #179 asks for: GO_IMAGE, GO_VERSION and
// go.mod's go directive must name the same Go release. GO_SHA256 and
// STATICCHECK_VERSION cannot be derived here -- the former is a checksum
// Renovate does not compute, the latter a separate project's release -- so a
// mismatch names them instead of checking them.
func TestGoPinsAgree(t *testing.T) {
	ciYAML := readRepoFile(t, ".gitlab-ci.yml")
	modFile := readRepoFile(t, "go.mod")

	imageVersion, err := goImageVersion(ciYAML)
	if err != nil {
		t.Fatalf("reading GO_IMAGE from .gitlab-ci.yml: %v", err)
	}
	versionVar, err := goVersionVar(ciYAML)
	if err != nil {
		t.Fatalf("reading GO_VERSION from .gitlab-ci.yml: %v", err)
	}
	modVersion, err := goModVersion(modFile)
	if err != nil {
		t.Fatalf("reading go.mod's go directive: %v", err)
	}

	if imageVersion == versionVar && versionVar == modVersion {
		return
	}
	t.Errorf("Go version pins disagree: .gitlab-ci.yml's GO_IMAGE says %s, "+
		"GO_VERSION says %s, go.mod's go directive says %s.\n"+
		"GO_SHA256 and STATICCHECK_VERSION must move with them: GO_SHA256 to the new "+
		"release's tarball checksum, STATICCHECK_VERSION to a staticcheck release that "+
		"supports that Go release.",
		imageVersion, versionVar, modVersion)
}

// TestPlaywrightPinsAgree is the check #179 asks for: PLAYWRIGHT_IMAGE,
// test/visual/package.json's @playwright/test and package-lock.json's
// resolved version must all name the same Playwright release.
func TestPlaywrightPinsAgree(t *testing.T) {
	ciYAML := readRepoFile(t, ".gitlab-ci.yml")
	pkgJSON := readRepoFile(t, "test", "visual", "package.json")
	lockJSON := readRepoFile(t, "test", "visual", "package-lock.json")

	imageVersion, err := playwrightImageVersion(ciYAML)
	if err != nil {
		t.Fatalf("reading PLAYWRIGHT_IMAGE from .gitlab-ci.yml: %v", err)
	}
	pkgVersion, err := playwrightPackageJSONVersion(pkgJSON)
	if err != nil {
		t.Fatalf("reading @playwright/test from test/visual/package.json: %v", err)
	}
	lockVersion, err := playwrightLockVersion(lockJSON)
	if err != nil {
		t.Fatalf("reading @playwright/test from test/visual/package-lock.json: %v", err)
	}

	if imageVersion == pkgVersion && pkgVersion == lockVersion {
		return
	}
	t.Errorf("Playwright version pins disagree: .gitlab-ci.yml's PLAYWRIGHT_IMAGE says %s, "+
		"test/visual/package.json's @playwright/test says %s, "+
		"test/visual/package-lock.json's resolved @playwright/test says %s.\n"+
		"Move PLAYWRIGHT_IMAGE, package.json's @playwright/test and the lockfile together "+
		"(edit package.json, then run npm install in test/visual to update the lockfile).",
		imageVersion, pkgVersion, lockVersion)
}

func TestGoImageVersionParsing(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "ok",
			input: "  GO_IMAGE: golang:1.27.1-bookworm@sha256:" + strings.Repeat("a", 64),
			want:  "1.27.1",
		},
		{
			name:  "surrounded by other variables",
			input: "GOCACHE: foo\nGO_IMAGE: golang:1.26.8-bookworm@sha256:" + strings.Repeat("b", 64) + "\nGO_VERSION: \"1.26.8\"",
			want:  "1.26.8",
		},
		{
			name:    "missing",
			input:   "GO_VERSION: \"1.27.1\"",
			wantErr: true,
		},
		{
			name:    "short digest",
			input:   "GO_IMAGE: golang:1.27.1-bookworm@sha256:" + strings.Repeat("a", 10),
			wantErr: true,
		},
		{
			name:    "no digest at all",
			input:   "GO_IMAGE: golang:1.27.1-bookworm",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := goImageVersion(c.input)
			if (err != nil) != c.wantErr {
				t.Fatalf("goImageVersion(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Errorf("goImageVersion(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestGoVersionVarParsing(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "ok", input: `GO_VERSION: "1.27.1"`, want: "1.27.1"},
		{name: "indented", input: "  GO_VERSION: \"1.26.8\"\n", want: "1.26.8"},
		{name: "missing quotes", input: "GO_VERSION: 1.27.1", wantErr: true},
		{name: "missing", input: "GO_IMAGE: golang:1.27.1-bookworm@sha256:abc", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := goVersionVar(c.input)
			if (err != nil) != c.wantErr {
				t.Fatalf("goVersionVar(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Errorf("goVersionVar(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestGoModVersionParsing(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "ok", input: "module example.com/foo\n\ngo 1.27.1\n\nrequire (\n)\n", want: "1.27.1"},
		{name: "two part version", input: "module example.com/foo\n\ngo 1.27\n", wantErr: true},
		{name: "missing", input: "module example.com/foo\n", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := goModVersion(c.input)
			if (err != nil) != c.wantErr {
				t.Fatalf("goModVersion(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Errorf("goModVersion(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestPlaywrightImageVersionParsing(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "ok",
			input: "  PLAYWRIGHT_IMAGE: mcr.microsoft.com/playwright:v1.63.0-noble@sha256:" + strings.Repeat("c", 64),
			want:  "1.63.0",
		},
		{
			name:    "missing v prefix",
			input:   "PLAYWRIGHT_IMAGE: mcr.microsoft.com/playwright:1.63.0-noble@sha256:" + strings.Repeat("c", 64),
			wantErr: true,
		},
		{
			name:    "missing",
			input:   "GO_VERSION: \"1.27.1\"",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := playwrightImageVersion(c.input)
			if (err != nil) != c.wantErr {
				t.Fatalf("playwrightImageVersion(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Errorf("playwrightImageVersion(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestPlaywrightPackageJSONVersionParsing(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "caret pin",
			input: `{"devDependencies": {"@playwright/test": "^1.63.0"}}`,
			want:  "1.63.0",
		},
		{
			name:  "tilde pin",
			input: `{"devDependencies": {"@playwright/test": "~1.63.0"}}`,
			want:  "1.63.0",
		},
		{
			name:  "bare exact",
			input: `{"devDependencies": {"@playwright/test": "1.63.0"}}`,
			want:  "1.63.0",
		},
		{
			name:    "x-range is not an exact pin",
			input:   `{"devDependencies": {"@playwright/test": "1.63.x"}}`,
			wantErr: true,
		},
		{
			name:    "comparator range is not an exact pin",
			input:   `{"devDependencies": {"@playwright/test": ">=1.63.0 <2.0.0"}}`,
			wantErr: true,
		},
		{
			name:    "wildcard is not an exact pin",
			input:   `{"devDependencies": {"@playwright/test": "*"}}`,
			wantErr: true,
		},
		{
			name:    "missing dependency",
			input:   `{"devDependencies": {}}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := playwrightPackageJSONVersion(c.input)
			if (err != nil) != c.wantErr {
				t.Fatalf("playwrightPackageJSONVersion(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Errorf("playwrightPackageJSONVersion(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestPlaywrightLockVersionParsing(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "ok",
			input: `{"packages": {"node_modules/@playwright/test": {"version": "1.63.0", "resolved": "x"}}}`,
			want:  "1.63.0",
		},
		{
			name:    "missing entry",
			input:   `{"packages": {}}`,
			wantErr: true,
		},
		{
			name:    "empty version",
			input:   `{"packages": {"node_modules/@playwright/test": {"version": ""}}}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := playwrightLockVersion(c.input)
			if (err != nil) != c.wantErr {
				t.Fatalf("playwrightLockVersion(%q) error = %v, wantErr %v", c.input, err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Errorf("playwrightLockVersion(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}
