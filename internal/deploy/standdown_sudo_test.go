package deploy

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// passwdHomeOrSkip returns the running account's home folder as the
// account database records it, or skips the test when that folder is
// missing or not owned by the running account (an odd CI account).
func passwdHomeOrSkip(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot look up the current account: %v", err)
	}
	if u.HomeDir == "" {
		t.Skip("the account database records no home folder for this account")
	}
	info, err := os.Stat(u.HomeDir)
	if err != nil {
		t.Skipf("cannot stat the account's home folder: %v", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no owner information on this platform")
	}
	if int(st.Uid) != os.Getuid() {
		t.Skipf("home folder %q is owned by uid %d, not the running uid %d", u.HomeDir, st.Uid, os.Getuid())
	}
	return u.HomeDir
}

// Under sudo or as root, HOME names another account's folder (Debian
// sudo sets /root). The owner's home, as the account database records
// it, is still the home folder and is never offered for deletion.
func TestRemovalCommand_RefusesTheOwnersHomeWhateverHOMESays(t *testing.T) {
	home := passwdHomeOrSkip(t)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "root"))
	for _, dir := range []string{home, home + "/", home + "/."} {
		words, err := RemovalCommandWords(dir)
		if !errors.Is(err, ErrHomeFolder) {
			t.Errorf("RemovalCommandWords(%q) error = %v, want ErrHomeFolder", dir, err)
		}
		if len(words) != 0 {
			t.Errorf("RemovalCommandWords(%q) = %q alongside its refusal, want no words", dir, words)
		}
		cmd, err := RemovalCommand(dir)
		if !errors.Is(err, ErrHomeFolder) {
			t.Errorf("RemovalCommand(%q) error = %v, want ErrHomeFolder", dir, err)
		}
		if cmd != "" {
			t.Errorf("RemovalCommand(%q) = %q alongside its refusal, want no command", dir, cmd)
		}
	}
}

// Guard (passes today): the owner lookup refuses only the home itself.
func TestRemovalCommandWords_StillNamesAFolderThatIsNotTheOwnersHome(t *testing.T) {
	home := passwdHomeOrSkip(t)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "root"))
	for _, dir := range []string{
		t.TempDir(),
		filepath.Join(home, "orbit-launcher-test-does-not-exist"),
	} {
		words, err := RemovalCommandWords(dir)
		if err != nil {
			t.Errorf("RemovalCommandWords(%q): %v, want the words", dir, err)
			continue
		}
		if len(words) == 0 || words[len(words)-1] != shellQuote(dir) {
			t.Errorf("RemovalCommandWords(%q) = %q, want it to end with %q", dir, words, shellQuote(dir))
		}
		if joined := strings.Join(words, " "); !strings.Contains(joined, "rm -rf") {
			t.Errorf("RemovalCommandWords(%q) = %q, want the folder removal", dir, joined)
		}
	}
}

// Unchanged: the volume half is never refused for the home folder.
func TestVolumeRemovalCommandWords_StillOffersTheOwnersHomeWhateverHOMESays(t *testing.T) {
	home := passwdHomeOrSkip(t)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "root"))
	words, err := VolumeRemovalCommandWords(home)
	if err != nil {
		t.Fatalf("VolumeRemovalCommandWords(%q): %v, want the words", home, err)
	}
	want := []string{
		"docker", "compose",
		"--project-directory", shellQuote(home),
		"--env-file", shellQuote(filepath.Join(home, EnvFile)),
		"down", "-v",
	}
	if !reflect.DeepEqual(words, want) {
		t.Errorf("got %q\nwant %q", words, want)
	}
	for _, w := range words {
		if w == "rm" || w == "sudo" || w == "&&" {
			t.Errorf("the words contain %q, but this half removes no folder: %q", w, words)
		}
	}
}
