package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The volume half of the removal command is offered on its own for the
// folders whose deletion the launcher refuses (the filesystem root and
// the home folder): it stops the stack and drops its data volumes, and
// never touches the folder.

func TestVolumeRemovalCommandWords_RefusesAnEmptyDirectory(t *testing.T) {
	words, err := VolumeRemovalCommandWords("")
	if !errors.Is(err, ErrNoTargetDir) {
		t.Fatalf("VolumeRemovalCommandWords(\"\") error = %v, want ErrNoTargetDir", err)
	}
	if len(words) != 0 {
		t.Errorf("VolumeRemovalCommandWords(\"\") returned %q alongside its error, want no words", words)
	}
}

// Root and home are not refused here: this half deletes no folder.
func TestVolumeRemovalCommandWords_IsTheComposeDownAndNothingElse(t *testing.T) {
	const home = "/home/someone"
	t.Setenv("HOME", home)
	for _, dir := range []string{"/opt/orbit", "/mnt/My Drive/orbit", "/", home} {
		words, err := VolumeRemovalCommandWords(dir)
		if err != nil {
			t.Errorf("VolumeRemovalCommandWords(%q): %v, want the words", dir, err)
			continue
		}
		want := []string{
			"docker", "compose",
			"--project-directory", shellQuote(dir),
			"--env-file", shellQuote(filepath.Join(dir, EnvFile)),
			"down", "-v",
		}
		if !reflect.DeepEqual(words, want) {
			t.Errorf("dir %q:\n got %q\nwant %q", dir, words, want)
		}
		for _, w := range words {
			if w == "rm" || w == "sudo" || w == "&&" {
				t.Errorf("dir %q: the words contain %q, but this half removes no folder: %q", dir, w, words)
			}
		}
	}
}

// One source for both halves: the full command is the volume half, then
// the folder removal.
func TestRemovalCommandWords_IsTheVolumeHalfThenTheFolderRemoval(t *testing.T) {
	const dir = "/opt/orbit"
	vol, err := VolumeRemovalCommandWords(dir)
	if err != nil {
		t.Fatal(err)
	}
	full, err := RemovalCommandWords(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{}, vol...), "&&", "sudo", "rm", "-rf", shellQuote(dir))
	if !reflect.DeepEqual(full, want) {
		t.Errorf("RemovalCommandWords(%q):\n got %q\nwant %q", dir, full, want)
	}
}

// A symlink to the home folder is the home folder: the refusal follows
// the link in either direction.
func TestRemovalCommand_RefusesASymlinkedHomeFolder(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	cases := []struct{ name, home, dir string }{
		{"HOME is the real folder, target is the link", real, link},
		{"HOME is the link, target is the real folder", link, real},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", c.home)
			words, err := RemovalCommandWords(c.dir)
			if !errors.Is(err, ErrHomeFolder) {
				t.Errorf("RemovalCommandWords(%q) error = %v, want ErrHomeFolder", c.dir, err)
			}
			if len(words) != 0 {
				t.Errorf("RemovalCommandWords(%q) = %q alongside its refusal, want no words", c.dir, words)
			}
			cmd, err := RemovalCommand(c.dir)
			if !errors.Is(err, ErrHomeFolder) {
				t.Errorf("RemovalCommand(%q) error = %v, want ErrHomeFolder", c.dir, err)
			}
			if cmd != "" {
				t.Errorf("RemovalCommand(%q) = %q alongside its refusal, want no command", c.dir, cmd)
			}
		})
	}
}

// Guard (passes today): a folder inside a symlinked home is still a
// folder of its own and is named, as the caller spelled it.
func TestRemovalCommandWords_StillNamesAFolderInsideASymlinkedHome(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	t.Setenv("HOME", real)
	dir := filepath.Join(link, "orbit")
	words, err := RemovalCommandWords(dir)
	if err != nil {
		t.Fatalf("RemovalCommandWords(%q): %v, want a command for a folder inside home", dir, err)
	}
	if len(words) == 0 || words[len(words)-1] != shellQuote(dir) {
		t.Errorf("RemovalCommandWords(%q) = %q, want its last word to be %q", dir, words, shellQuote(dir))
	}
	if !strings.Contains(strings.Join(words, " "), "rm -rf") {
		t.Errorf("RemovalCommandWords(%q) = %q, want the folder removal", dir, words)
	}
}
