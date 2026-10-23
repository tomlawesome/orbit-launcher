package deploy

// Pins #277 W2 (owner answer 15a, refining the escapes): when shell
// quoting puts a word in bash's $'...' form, a non-printing character
// above U+007F (a bidi control, U+0085, ...) is written as its UTF-8
// bytes in \xHH form, never \u or \U, so the word means the same bytes
// in any locale. ASCII escapes (\x1b, \n, \\, \') are unchanged.

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type groupBByteCase struct {
	word string
	want string // exact quoted form
}

var groupBByteCases = []groupBByteCase{
	{"rlo‮exe", `$'rlo\xe2\x80\xaeexe'`},
	{"nel\u0085x", `$'nel\xc2\x85x'`},
	{"isolate⁦x", `$'isolate\xe2\x81\xa6x'`},
	{"‮\x1b", `$'\xe2\x80\xae\x1b'`},
	{"it's‮\n\\", `$'it\'s\xe2\x80\xae\n\\'`},
}

func TestGroupB_W2_NonASCIIEscapesAreUTF8Bytes(t *testing.T) {
	for _, c := range groupBByteCases {
		got := shellQuote(c.word)
		if got != c.want {
			t.Errorf("shellQuote(%q) = %s, want %s", c.word, got, c.want)
		}
		if strings.Contains(got, `\u`) || strings.Contains(got, `\U`) {
			t.Errorf("shellQuote(%q) = %s: must not use \\u or \\U", c.word, got)
		}
	}
}

// groupBEvalIn evaluates the quoted word in bash under the given locale
// (LANG unset) and returns the exact bytes it produces.
func groupBEvalIn(t *testing.T, lcAll, quoted string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is needed: %v", err)
	}
	cmd := exec.Command(bash, "-c", `eval "w=$1"; printf %s "$w"`, "_", quoted)
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "LC_") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = append(env, "LC_ALL="+lcAll)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash eval of %s under LC_ALL=%s: %v (%s)", quoted, lcAll, err, stderr.String())
	}
	return string(out)
}

func groupBRawWords() []string {
	var ws []string
	for _, c := range groupBByteCases {
		ws = append(ws, c.word)
	}
	return ws
}

func TestGroupB_W2_NonASCIIWordSurvivesBashInCLocale(t *testing.T) {
	for _, w := range groupBRawWords() {
		q := shellQuote(w)
		if got := groupBEvalIn(t, "C", q); got != w {
			t.Errorf("under LC_ALL=C bash turns %s into %q, want %q", q, got, w)
		}
	}
}

func TestGroupB_W2_NonASCIIWordSurvivesBashInUTF8Locale(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	if out, err := exec.Command(bash, "-c", `[ "$(LC_ALL=C.UTF-8 locale charmap 2>/dev/null)" = UTF-8 ]`).CombinedOutput(); err != nil {
		t.Skipf("bash/locale lacks C.UTF-8: %v %s", err, out)
	}
	for _, w := range groupBRawWords() {
		q := shellQuote(w)
		if got := groupBEvalIn(t, "C.UTF-8", q); got != w {
			t.Errorf("under LC_ALL=C.UTF-8 bash turns %s into %q, want %q", q, got, w)
		}
	}
}
