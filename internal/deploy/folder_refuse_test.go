package deploy

// #272 Part 2: BuildInstallCommand and BuildEngineCommand refuse a folder
// holding any control rune (unicode.IsControl), before staging anything,
// and say why without echoing the raw rune. Format runes such as U+202E
// are not refused (the screen escapes them instead).

import (
	"os"
	"path/filepath"
	"testing"
	"unicode"
)

var folderRefuseScript = []byte("#!/usr/bin/env bash\n")

// folderRefuseBuilders runs the same assertions against both builders.
var folderRefuseBuilders = []struct {
	name  string
	build func(dir string) (cleanup func() error, gotCmd bool, err error)
}{
	{"install", func(dir string) (func() error, bool, error) {
		cmd, cleanup, err := BuildInstallCommand(folderRefuseScript, dir)
		return cleanup, cmd != nil, err
	}},
	{"engine", func(dir string) (func() error, bool, error) {
		cmd, cleanup, err := BuildEngineCommand(folderRefuseScript, dir, ActionInstall)
		return cleanup, cmd != nil, err
	}},
}

// folderRefuseStaged points TMPDIR at a fresh directory and returns it plus
// a function listing any staged install files in it.
func folderRefuseStaged(t *testing.T) (staged func() []string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return func() []string {
		m, err := filepath.Glob(filepath.Join(tmp, "orbit-launcher-install-*"))
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		return m
	}
}

func hasControlRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// e, f, g: each control rune is refused by both builders, nothing staged,
// error text free of raw control runes.
func TestFolderRefuse_ControlRunesRefusedByBothBuilders(t *testing.T) {
	runes := []struct {
		name string
		r    rune
	}{
		{"CR", '\r'}, {"LF", '\n'}, {"TAB", '\t'}, {"ESC", '\x1b'},
		{"DEL", '\x7f'}, {"NEL", '\u0085'},
	}
	places := []struct {
		name string
		make func(base string, r rune) string
	}{
		{"inside the last name", func(b string, r rune) string { return filepath.Join(b, "ab"+string(r)+"cd") }},
		{"at the end", func(b string, r rune) string { return filepath.Join(b, "orbit") + string(r) }},
		{"in a parent name", func(b string, r rune) string { return filepath.Join(b, "x"+string(r)+"y", "orbit") }},
	}
	for _, b := range folderRefuseBuilders {
		for _, rc := range runes {
			for _, pl := range places {
				t.Run(b.name+"/"+rc.name+"/"+pl.name, func(t *testing.T) {
					staged := folderRefuseStaged(t)
					dir := pl.make(t.TempDir(), rc.r)

					cleanup, gotCmd, err := b.build(dir)
					if cleanup != nil {
						defer cleanup()
					}
					if err == nil {
						t.Fatalf("folder %q: expected an error, got nil", dir)
					}
					if gotCmd {
						t.Errorf("folder %q: expected no command alongside the error", dir)
					}
					if msg := err.Error(); msg == "" || hasControlRune(msg) {
						t.Errorf("error text %q must be non-empty and hold no raw control rune", msg)
					}
					if got := staged(); len(got) != 0 {
						t.Errorf("refused call staged %v in TMPDIR, want nothing", got)
					}
				})
			}
		}
	}
}

// Non-vacuity for f: a successful build does leave a staged file in TMPDIR
// until cleanup runs, so the absence check above can see one.
func TestFolderRefuse_StagedFileIsVisibleInTmpdirOnSuccess(t *testing.T) {
	for _, b := range folderRefuseBuilders {
		t.Run(b.name, func(t *testing.T) {
			staged := folderRefuseStaged(t)
			cleanup, gotCmd, err := b.build(t.TempDir())
			if err != nil || !gotCmd {
				t.Fatalf("plain folder: err = %v, command returned = %v", err, gotCmd)
			}
			if got := staged(); len(got) != 1 {
				t.Errorf("staged entries before cleanup = %v, want exactly 1", got)
			}
			if err := cleanup(); err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			if got := staged(); len(got) != 0 {
				t.Errorf("staged entries after cleanup = %v, want none", got)
			}
		})
	}
}

// h: plain, spaced, non-ASCII and format-rune (U+202E) folders still build.
func TestFolderRefuse_OrdinaryAndFormatRuneFoldersStillBuild(t *testing.T) {
	names := []struct{ name, dir string }{
		{"plain", "orbit"},
		{"spaces", "my orbit folder"},
		{"non-ascii", "café-über-日本"},
		{"U+202E", "evil\u202etxt"},
	}
	for _, b := range folderRefuseBuilders {
		for _, n := range names {
			t.Run(b.name+"/"+n.name, func(t *testing.T) {
				folderRefuseStaged(t)
				dir := filepath.Join(t.TempDir(), n.dir)
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Skipf("filesystem cannot hold this folder name: %v", err)
				}
				cleanup, gotCmd, err := b.build(dir)
				if err != nil {
					t.Fatalf("folder %q: unexpected error %v", dir, err)
				}
				defer cleanup()
				if !gotCmd {
					t.Errorf("folder %q: expected a command", dir)
				}
			})
		}
	}
}
