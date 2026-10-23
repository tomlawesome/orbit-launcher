package deploy

// Pins #277 W2 (ratified, answer 9a): what the screen shows and what is
// copied are the same bytes. A shell word holding a non-printing
// character is quoted in bash's $'...' form, with the escapes the box
// already shows (\x1b, \n, \u202e, \\, \'); an ordinary word is quoted
// as before. The recovery command is the installer's own line and is not
// touched. Applies to Remove's command and Repair's chown command.

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"unicode"
)

// oddWords hold something the screen would have to escape.
var groupBOddWords = []string{
	"/home/you/odd\x1bname",
	"a\nb",
	"tab\there",
	"cr\rhere",
	"del\x7fx",
	"bell\ax",
	"rlo\u202eexe.txt",
	"it's\x1b",
	`back\slash` + "\x1b",
	"x\\" + "\x1b",
	`lit\x41` + "\x1b",
	"\x1b[31m",
	"mixed é日本\x1b",
	"two\x1b\x1bescapes and a space",
}

// plainWords have only printable characters and need no $'...' form.
var groupBPlainWords = []string{
	"/opt/orbit",
	"/mnt/My Drive/orbit",
	"/opt/tom's orbit",
	`/opt/back\slash`,
	"/srv/é日本/orbit",
	"",
}

// groupBBash prints each word of script (shell text) as bash reads it,
// separated by NULs so a word may hold a newline.
func groupBBash(t *testing.T, script string) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is needed to check $'...' quoting: %v", err)
	}
	cmd := exec.Command(bash, "-c", `printf '%s\0' `+script)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C.UTF-8")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash -c %q: %v (%s)", script, err, stderr.String())
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func groupBPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0x202e || !unicode.IsPrint(r) && r != ' ' {
			return false
		}
	}
	return true
}

func TestGroupB_W2_ShellQuoteEvaluatesToTheRawWordInBash(t *testing.T) {
	for _, w := range groupBOddWords {
		q := shellQuote(w)
		if !strings.HasPrefix(q, "$'") || !strings.HasSuffix(q, "'") {
			t.Errorf("shellQuote(%q) = %q, want the $'...' form", w, q)
		}
		if !groupBPrintable(q) {
			t.Errorf("shellQuote(%q) = %q holds a non-printing character", w, q)
		}
		got := groupBBash(t, q)
		if len(got) != 1 || got[0] != w {
			t.Errorf("bash evaluates shellQuote(%q) = %q to %q, want the raw word", w, q, got)
		}
	}
}

// The escapes are the ones the box already shows.
func TestGroupB_W2_ShellQuoteUsesTheBoxEscapes(t *testing.T) {
	cases := map[string]string{
		"/home/you/odd\x1bname": `$'/home/you/odd\x1bname'`,
		"a\nb":                  `$'a\nb'`,
		"rlo\u202eexe":               `$'rlo\u202eexe'`,
		"it's\x1b":              `$'it\'s\x1b'`,
		"a\\b\x1b":              `$'a\\b\x1b'`,
	}
	for w, want := range cases {
		if got := shellQuote(w); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", w, got, want)
		}
	}
}

// A word with only printable characters is not in $'...' form and still
// reaches the shell as one word equal to itself.
func TestGroupB_W2_PrintableWordsAreNotInTheAnsiCForm(t *testing.T) {
	for _, w := range groupBPlainWords {
		q := shellQuote(w)
		if strings.HasPrefix(q, "$'") {
			t.Errorf("shellQuote(%q) = %q: a printable word must not use $'...'", w, q)
		}
		got := groupBBash(t, q)
		if len(got) != 1 || got[0] != w {
			t.Errorf("bash evaluates shellQuote(%q) = %q to %q, want the raw word", w, q, got)
		}
	}
}

// groupBEvalWords runs the command's words through bash, "&&" quoted so
// it is printed, not run, and returns what bash makes of them.
func groupBEvalWords(t *testing.T, words []string) []string {
	t.Helper()
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = w
		if w == "&&" {
			parts[i] = "'&&'"
		}
	}
	return groupBBash(t, strings.Join(parts, " "))
}

func TestGroupB_W2_RemovalCommandForAnOddFolderIsFaithful(t *testing.T) {
	for _, dir := range groupBOddWords {
		dir = "/srv/" + strings.TrimPrefix(dir, "/") // no "//": the paths are joined cleanly
		words, err := RemovalCommandWords(dir)
		if err != nil {
			t.Fatalf("RemovalCommandWords(%q): %v", dir, err)
		}
		for _, w := range words {
			if !groupBPrintable(w) {
				t.Errorf("dir %q: word %q holds a non-printing character", dir, w)
			}
		}
		line, err := RemovalCommand(dir)
		if err != nil {
			t.Fatalf("RemovalCommand(%q): %v", dir, err)
		}
		if line != strings.Join(words, " ") {
			t.Errorf("dir %q: the one-line command is not its words joined:\n%s\n%s", dir, line, strings.Join(words, " "))
		}
		if !groupBPrintable(line) {
			t.Errorf("dir %q: the command holds a non-printing character: %q", dir, line)
		}
		got := groupBEvalWords(t, words)
		after := func(flag string) string {
			for i, w := range got {
				if w == flag && i+1 < len(got) {
					return got[i+1]
				}
			}
			return "<missing>"
		}
		if v := after("--project-directory"); v != dir {
			t.Errorf("dir %q: bash reads --project-directory as %q", dir, v)
		}
		if v := after("--env-file"); v != dir+"/.env-orbit" {
			t.Errorf("dir %q: bash reads --env-file as %q", dir, v)
		}
		if last := got[len(got)-1]; last != dir {
			t.Errorf("dir %q: bash reads the folder to remove as %q: rm would miss it", dir, last)
		}
	}
}

func TestGroupB_W2_ChownCommandForAnOddFolderIsFaithful(t *testing.T) {
	for _, dir := range groupBOddWords {
		dir = "/srv/" + strings.TrimPrefix(dir, "/") // no "//": the paths are joined cleanly
		own := Ownership{Account: "alice", Group: "1000", Files: []ForeignFile{{Path: ".env-orbit", Owner: "root"}}}
		words := ChownCommandWords(dir, own)
		for _, w := range words {
			if !groupBPrintable(w) {
				t.Errorf("dir %q: word %q holds a non-printing character", dir, w)
			}
		}
		got := groupBEvalWords(t, words)
		if len(got) < 2 || got[len(got)-1] != dir+"/.env-orbit" {
			t.Errorf("dir %q: bash reads the chown command as %q, want it to end with the .env-orbit path", dir, got)
		}
	}
}
