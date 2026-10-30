// A live subtest's result must never be used (#310, SEC-SP1-1). go test
// -skip/-run makes a filtered-out subtest return true, so `installed :=
// t.Run("Install", ...)` cannot tell a sibling that the subtest really ran;
// a quarantined Install would let Remove run against nothing. The subtest
// records its own success in a variable instead.
package supplychain

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var tRunToken = regexp.MustCompile(`(^|[^A-Za-z0-9_])t\.Run\(`)

// usesTRunResult reports whether the line has a t.Run( call outside a //
// comment that is not the start of the statement.
func usesTRunResult(line string) bool {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	if !tRunToken.MatchString(line) {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(line), "t.Run(")
}

func TestLiveSubtestResultClassifier(t *testing.T) {
	for _, c := range []struct {
		line string
		bad  bool
	}{
		{"\tt.Run(\"Remove\", func(t *testing.T) {", false},
		{"t.Run(\"x\", f)", false},
		{"\tinstalled := t.Run(\"Install\", func(t *testing.T) {", true},
		{"\tinstalled = t.Run(\"Install\", f)", true},
		{"\tif t.Run(\"Install\", f) {", true},
		{"\tif !t.Run(\"Install\", f) {", true},
		{"\tok := a && t.Run(\"Install\", f)", true},
		{"\treturn t.Run(\"Install\", f)", true},
		{"\t// installed := t.Run(\"Install\", f) is what not to write", false},
		{"\tt.Run(\"Remove\", f) // not installed := t.Run(", false},
		{"\tx := subt.Run(\"Install\", f)", false},
		{"\tx := 1", false},
	} {
		if got := usesTRunResult(c.line); got != c.bad {
			t.Errorf("usesTRunResult(%q) = %v, want %v", c.line, got, c.bad)
		}
	}
}

func TestLiveSubtestsNeverUseTheResultOfRun(t *testing.T) {
	dir := filepath.Join(root, "test", "live")
	var bad []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for i, l := range strings.Split(string(body), "\n") {
			if usesTRunResult(l) {
				bad = append(bad, fmt.Sprintf("%s:%d: t.Run's result is used; go test -skip/-run makes a filtered subtest return true, so the result cannot tell a sibling the subtest ran (#310, SEC-SP1-1). Record success in a variable at the end of the subtest body instead. Line: %s", filepath.ToSlash(rel), i+1, strings.TrimSpace(l)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Error(b)
	}
}

func TestQuarantineHeaderSaysADependentSubtestSkipsItself(t *testing.T) {
	header := leadingComment(readRepoFile(t, quarantineFile))
	flat := strings.Join(strings.Fields(strings.ReplaceAll(header, "\n#", " ")), " ")
	for _, s := range regexp.MustCompile(`[.!?]\s`).Split(flat+" ", -1) {
		l := strings.ToLower(s)
		if strings.Contains(l, "skips itself") && strings.Contains(s, "Remove") {
			return
		}
	}
	t.Errorf("%s's header must have a sentence with \"skips itself\" and \"Remove\": quarantining TestX/Install makes the dependent Remove subtest skip with it rather than fail (#310, SEC-SP1-1)", quarantineFile)
}
