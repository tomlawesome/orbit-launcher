package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/licencereview/review"
)

func write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo makes a fresh directory shaped like the repo root -- an extras
// directory holding one hand-kept notice -- and runs the test in it, as
// the command is run from the module root. It returns a stand-in for
// review.Shipped: one dependency, example.com/m v1.0.0, with a licence
// file at its root.
func repo(t *testing.T) func() ([]review.Package, error) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, extrasDir, "table.txt"), "Module: example.com/m\nAbout: a table from elsewhere\n\nElsewhere's notice\n")
	t.Chdir(root)

	mod := t.TempDir()
	write(t, filepath.Join(mod, "LICENSE"), "M's licence text")
	pkgs := []review.Package{{ImportPath: "example.com/m", Dir: mod, Module: &review.Module{Path: "example.com/m", Version: "v1.0.0", Dir: mod}}}
	return func() ([]review.Package, error) { return pkgs, nil }
}

func licencenotices(t *testing.T, lister func() ([]review.Package, error), args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = cli(append([]string{"licencenotices"}, args...), &out, &errOut, lister)
	return code, out.String(), errOut.String()
}

func readOut(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading %s: %v", outPath, err)
	}
	return string(b)
}

// Without -check the command writes the notices: the Go distribution's
// licence, each linked module's, and the hand-kept extras.
func TestCLI_WritesEveryNotice(t *testing.T) {
	lister := repo(t)
	if code, stdout, stderr := licencenotices(t, lister); code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q; want a silent 0", code, stdout, stderr)
	}
	got := readOut(t)
	for _, want := range []string{
		"==== Go standard library and runtime (LICENSE) ====\n\nCopyright 2009 The Go Authors.",
		"==== example.com/m v1.0.0 (LICENSE) ====\n\nM's licence text",
		"==== Carried by example.com/m: a table from elsewhere ====\n\nElsewhere's notice",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q", outPath, want)
		}
	}
}

// -check passes on the file the command just wrote, and fails, without
// rewriting it, once that file and what is linked disagree.
func TestCLI_CheckFailsOnlyWhenStale(t *testing.T) {
	lister := repo(t)
	if code, _, stderr := licencenotices(t, lister); code != 0 {
		t.Fatalf("write: exit %d, stderr %q", code, stderr)
	}
	code, stdout, stderr := licencenotices(t, lister, "-check")
	if code != 0 || stdout != outPath+" is current\n" || stderr != "" {
		t.Fatalf("fresh file: exit %d, stdout %q, stderr %q; want 0 and %q is current", code, stdout, stderr, outPath)
	}

	stale := readOut(t) + "edited by hand\n"
	write(t, outPath, stale)
	code, stdout, stderr = licencenotices(t, lister, "-check")
	if code != 1 || stdout != "" || !strings.Contains(stderr, outPath+" is stale") || !strings.Contains(stderr, "go run ./tools/licencenotices") {
		t.Errorf("stale file: exit %d, stdout %q, stderr %q; want 1 and how to regenerate it", code, stdout, stderr)
	}
	if readOut(t) != stale {
		t.Error("-check rewrote the stale file; it must only report it")
	}
}

// Anything that stops the notices being built fails the command, with
// the reason on stderr, and writes nothing: shipping without a notice
// is what this exists to stop.
func TestCLI_FailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) func() ([]review.Package, error)
		args  []string
		want  string
	}{
		{"go list fails", func(t *testing.T) func() ([]review.Package, error) {
			repo(t)
			return func() ([]review.Package, error) { return nil, errors.New("go list for linux/arm64: boom") }
		}, nil, "boom"},
		{"go env cannot run", func(t *testing.T) func() ([]review.Package, error) {
			l := repo(t)
			t.Setenv("PATH", t.TempDir())
			return l
		}, nil, "go env GOROOT"},
		{"no Go licence", func(t *testing.T) func() ([]review.Package, error) {
			l := repo(t)
			t.Setenv("GOROOT", t.TempDir())
			return l
		}, nil, "LICENSE"},
		{"an extra cannot be read", func(t *testing.T) func() ([]review.Package, error) {
			l := repo(t)
			if err := os.Mkdir(filepath.Join(extrasDir, "dir.txt"), 0o755); err != nil {
				t.Fatal(err)
			}
			return l
		}, nil, "dir.txt"},
		{"an extra is malformed", func(t *testing.T) func() ([]review.Package, error) {
			l := repo(t)
			write(t, filepath.Join(extrasDir, "bad.txt"), "no header\n")
			return l
		}, nil, "bad.txt"},
		{"an extra names a module nothing links", func(t *testing.T) func() ([]review.Package, error) {
			l := repo(t)
			write(t, filepath.Join(extrasDir, "gone.txt"), "Module: example.com/gone\nAbout: x\n\nnotice\n")
			return l
		}, nil, "example.com/gone"},
		{"no notices directory to write into", func(t *testing.T) func() ([]review.Package, error) {
			l := repo(t)
			if err := os.RemoveAll("internal"); err != nil {
				t.Fatal(err)
			}
			return l
		}, nil, outPath},
		{"-check with no committed file", repo, []string{"-check"}, outPath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lister := c.setup(t)
			code, _, stderr := licencenotices(t, lister, c.args...)
			if code != 1 || !strings.HasPrefix(stderr, "licencenotices: ") || !strings.Contains(stderr, c.want) {
				t.Errorf("exit %d, stderr %q; want 1 and an error naming %q", code, stderr, c.want)
			}
			if _, err := os.Stat(outPath); err == nil {
				t.Errorf("%s was written despite the failure", outPath)
			}
		})
	}
}

// Flags behave as Go's default flag set's do: a bad one is a usage error
// (exit 2) that never builds anything, and -h prints usage and exits 0.
func TestCLI_FlagErrorsAndHelp(t *testing.T) {
	ran := false
	lister := func() ([]review.Package, error) { ran = true; return nil, nil }

	code, _, stderr := licencenotices(t, lister, "-bogus")
	if code != 2 || !strings.Contains(stderr, "-bogus") || !strings.Contains(stderr, "Usage of licencenotices") {
		t.Errorf("bad flag: exit %d, stderr %q; want 2 and usage", code, stderr)
	}
	code, _, stderr = licencenotices(t, lister, "-h")
	if code != 0 || !strings.Contains(stderr, "-check") {
		t.Errorf("-h: exit %d, stderr %q; want 0 and usage", code, stderr)
	}
	if ran {
		t.Error("the command ran despite a flag error or -h")
	}
}
