package deploy

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Pins #212 (#199 WI-8, EN-5): the configuration code's "not a regular
// file" and "not a directory" refusals read reason-first, as the trusted
// path check's do ("refusing: it is not a regular file — <path>"). The
// reason comes first so a long path cannot push it off a narrow screen.
// The errors stay plain: the UI tells an untrusted-path refusal from
// any other failure by type, and that routing must not change.

// wi8ReasonFirst fails unless err names reason before it names the file
// (matched by base name), and names the file at all.
func wi8ReasonFirst(t *testing.T, err error, reason, base string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %q refusal, got no error", reason)
	}
	msg := err.Error()
	r, p := strings.Index(msg, reason), strings.Index(msg, base)
	if r < 0 {
		t.Fatalf("error %q does not say %q", msg, reason)
	}
	if p < 0 {
		t.Fatalf("error %q does not name %q", msg, base)
	}
	if p < r {
		t.Errorf("path comes before the reason in %q; want the reason first", msg)
	}
}

func wi8WantPlain(t *testing.T, err error) {
	t.Helper()
	var untrusted *UntrustedPathError
	if errors.As(err, &untrusted) {
		t.Errorf("a private-tree refusal must stay a plain error, got UntrustedPathError: %v", err)
	}
}

func TestAdoptConfig_NotARegularFileReadsReasonFirst(t *testing.T) {
	t.Run("session env file is a symlink", func(t *testing.T) {
		targetDir := liveTarget(t)
		treeDir := newSession(t)
		outside := filepath.Join(t.TempDir(), "outside")
		writeTestFile(t, outside, "APP_URL=https://outside.example\n", 0o600)
		envSrc := filepath.Join(treeDir, ".env-orbit")
		if err := os.Remove(envSrc); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, envSrc); err != nil {
			t.Fatal(err)
		}
		err := AdoptConfig(treeDir, targetDir)
		wi8ReasonFirst(t, err, "not a regular file", ".env-orbit")
		wi8WantPlain(t, err)
	})

	t.Run("live secret is a symlink", func(t *testing.T) {
		targetDir := liveTarget(t)
		elsewhere := filepath.Join(t.TempDir(), "elsewhere")
		writeTestFile(t, elsewhere, "someone else's file\n", 0o600)
		if err := os.Symlink(elsewhere, filepath.Join(targetDir, ".orbit-secrets", "zz-linked")); err != nil {
			t.Fatal(err)
		}
		treeDir := newSession(t)
		writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets", "zz-linked"), "new-linked\n", 0o600)
		err := AdoptConfig(treeDir, targetDir)
		wi8ReasonFirst(t, err, "not a regular file", "zz-linked")
		wi8WantPlain(t, err)
	})

	t.Run("live env file is a directory", func(t *testing.T) {
		targetDir := liveTarget(t)
		if err := os.Remove(filepath.Join(targetDir, ".env-orbit")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(targetDir, ".env-orbit"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := AdoptConfig(newSession(t), targetDir)
		wi8ReasonFirst(t, err, "not a regular file", ".env-orbit")
		wi8WantPlain(t, err)
	})
}

func TestAdoptConfig_NotADirectoryReadsReasonFirst(t *testing.T) {
	t.Run("session secrets path is a file", func(t *testing.T) {
		targetDir := liveTarget(t)
		treeDir := t.TempDir()
		writeTestFile(t, filepath.Join(treeDir, ".env-orbit"), "APP_URL=https://kept.example\n", 0o600)
		writeTestFile(t, filepath.Join(treeDir, ".orbit-secrets"), "not a directory\n", 0o600)
		err := AdoptConfig(treeDir, targetDir)
		wi8ReasonFirst(t, err, "not a directory", ".orbit-secrets")
		wi8WantPlain(t, err)
	})

	t.Run("live secrets path is a file", func(t *testing.T) {
		targetDir := liveTarget(t)
		if err := os.RemoveAll(filepath.Join(targetDir, ".orbit-secrets")); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets"), "in the way\n", 0o600)
		// This path is caught by the trusted-path check, so its refusal
		// is an UntrustedPathError and already reason-first: the case
		// proves the reason-first reading can be present and must stay.
		wi8ReasonFirst(t, AdoptConfig(newSession(t), targetDir), "not a directory", ".orbit-secrets")
	})
}

func TestImportTargetConfig_NotARegularFileOrDirectoryReadsReasonFirst(t *testing.T) {
	t.Run("target env file is a symlink", func(t *testing.T) {
		treeDir, targetDir := t.TempDir(), t.TempDir()
		elsewhere := filepath.Join(t.TempDir(), "unrelated")
		writeTestFile(t, elsewhere, "NOT_ORBIT=1\n", 0o600)
		if err := os.Symlink(elsewhere, filepath.Join(targetDir, ".env-orbit")); err != nil {
			t.Fatal(err)
		}
		wi8ReasonFirst(t, ImportTargetConfig(treeDir, targetDir), "not a regular file", ".env-orbit")
	})

	t.Run("target secrets path is a file", func(t *testing.T) {
		targetDir := t.TempDir()
		writeTestFile(t, filepath.Join(targetDir, ".orbit-secrets"), "x", 0o600)
		wi8ReasonFirst(t, ImportTargetConfig(t.TempDir(), targetDir), "not a directory", ".orbit-secrets")
	})
}

// wi8PathFirstLiteral reports whether a format literal says the path
// first: a "%" verb ahead of "not a regular file" / "not a directory".
func wi8PathFirstLiteral(s string) bool {
	for _, reason := range []string{"not a regular file", "not a directory"} {
		if i := strings.Index(s, reason); i >= 0 && strings.Contains(s[:i], "%") {
			return true
		}
	}
	return false
}

// The scan is what keeps all six sites honest, including ones no test
// reaches: no format string in internal/deploy (the trusted check
// aside, which is already reason-first and untouched) puts a path
// verb ahead of the reason.
func TestDeploySourceHasNoPathFirstNotARegularFile(t *testing.T) {
	// The scan must be able to see what it hunts for.
	if !wi8PathFirstLiteral(`"%s is not a regular file"`) || wi8PathFirstLiteral(`"refusing: it is not a regular file — %s"`) {
		t.Fatal("the path-first detector is wrong")
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || path == "trusted.go" {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && wi8PathFirstLiteral(s) {
				t.Errorf("%s: %s says the path before the reason; word it reason-first", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
	if scanned < 5 {
		t.Fatalf("scanned only %d source files; the scan is not looking at the package", scanned)
	}
}
