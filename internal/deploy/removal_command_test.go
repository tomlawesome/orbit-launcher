package deploy

import (
	"strings"
	"testing"
)

// #205 (EN-1, EN-12): a removal command only ever names a directory the
// launcher actually found, so there is no command for no directory. The
// words are the single source; the one-line command is those words
// joined, so a screen built from one and a clipboard built from the
// other cannot disagree.

// mustRemovalCommand is RemovalCommand for a directory that exists in
// the test's story; an error there is a test failure.
func mustRemovalCommand(t *testing.T, dir string) string {
	t.Helper()
	cmd, err := RemovalCommand(dir)
	if err != nil {
		t.Fatalf("RemovalCommand(%q): %v", dir, err)
	}
	return cmd
}

func TestRemovalCommand_RefusesAnEmptyDirectory(t *testing.T) {
	cmd, err := RemovalCommand("")
	if err == nil {
		t.Fatalf("RemovalCommand(\"\") = %q with no error, want an error and no command", cmd)
	}
	if cmd != "" {
		t.Errorf("RemovalCommand(\"\") returned %q alongside its error, want no command", cmd)
	}
}

func TestRemovalCommandWords_RefusesAnEmptyDirectory(t *testing.T) {
	words, err := RemovalCommandWords("")
	if err == nil {
		t.Fatalf("RemovalCommandWords(\"\") = %q with no error, want an error and no words", words)
	}
	if len(words) != 0 {
		t.Errorf("RemovalCommandWords(\"\") returned %q alongside its error, want no words", words)
	}
}

// The command is its words joined by single spaces, nothing more.
func TestRemovalCommand_IsItsWordsJoined(t *testing.T) {
	for _, dir := range []string{"/opt/orbit", "/srv/containers/mail/orbit-production", "/mnt/My Drive/orbit", "/opt/tom's orbit"} {
		words, err := RemovalCommandWords(dir)
		if err != nil {
			t.Fatalf("RemovalCommandWords(%q): %v", dir, err)
		}
		if got, want := mustRemovalCommand(t, dir), strings.Join(words, " "); got != want {
			t.Errorf("dir %q:\n command: %s\n   words: %s", dir, got, want)
		}
	}
}

// Each word is already quoted for the shell: a directory with a space or
// a quote in it is one word, in the places the command names it.
func TestRemovalCommandWords_QuoteTheDirectoryAsOneWord(t *testing.T) {
	for _, dir := range []string{"/opt/orbit", "/mnt/My Drive/orbit", "/opt/tom's orbit"} {
		words, err := RemovalCommandWords(dir)
		if err != nil {
			t.Fatalf("RemovalCommandWords(%q): %v", dir, err)
		}
		quoted := shellQuote(dir)
		after := func(flag string) string {
			for i, w := range words {
				if w == flag && i+1 < len(words) {
					return words[i+1]
				}
			}
			return ""
		}
		if got := after("--project-directory"); got != quoted {
			t.Errorf("dir %q: word after --project-directory = %q, want %q (words %q)", dir, got, quoted, words)
		}
		if got := after("--env-file"); got != shellQuote(dir+"/.env-orbit") {
			t.Errorf("dir %q: word after --env-file = %q, want %q (words %q)", dir, got, shellQuote(dir+"/.env-orbit"), words)
		}
		if last := words[len(words)-1]; last != quoted {
			t.Errorf("dir %q: last word = %q, want the directory %q (words %q)", dir, last, quoted, words)
		}
	}
}
