package review

import (
	"testing"
)

// X-Q8 (#195): comment markers are read only outside string literals, and a
// block comment opened in the middle of a line is scanned to its close. Today
// "//" is found anywhere in a line (including inside a string) and "/*" is
// only recognised at the start of a line.

// noticeFor reports whether a Go file with this body (after a package
// clause) is flagged as carrying a provenance notice.
func noticeFor(t *testing.T, body string) bool {
	t.Helper()
	root := fakeModule(t, map[string]string{"LICENSE": "MIT", "f.go": "package p\n\n" + body})
	got := scan(t, pkg(root, "example.com/m", ".", []string{"f.go"}))
	switch {
	case len(got) == 0:
		return false
	case len(got) == 1 && got[0].Kind == Notice:
		return true
	}
	t.Fatalf("%q: got %v, want nothing or one notice", body, got)
	return false
}

// Provenance words inside a string literal are not a comment, wherever the
// "//" or "/*" inside the string sits.
func TestScan_CommentMarkersInsideStringsAreNotComments(t *testing.T) {
	for name, body := range map[string]string{
		"slashes at the start of a string":    "s := \"// ported from tmux\"\n",
		"slashes mid-string (a URL)":          "u := \"https://example.com/adapted from\"\n",
		"slashes in a raw string":             "s := `// Licensed under the Apache License`\n",
		"slashes in a raw string, mid-line":   "s := `see // copied from x`\n",
		"block opener in a string":            "s := \"/* ported from\"\nt := 1\n",
		"block opener in a raw string":        "s := `/* adapted from`\nt := 1\n",
		"escaped quote keeps the string open": "s := \"a \\\" // copied from b\"\n",
		"raw string over several lines":       "var s = `\n// Ported from tmux\n// SPDX-License-Identifier: MIT\n`\n",
		"raw string with a block comment":     "var s = `\n/*\n * Borrowed from musl.\n */\n`\n",
	} {
		t.Run(name, func(t *testing.T) {
			if noticeFor(t, body) {
				t.Errorf("%q: flagged a notice, but the words are inside a string", body)
			}
		})
	}
}

// A real comment after a string, or after a quote character that is not a
// string, is still read.
func TestScan_CommentAfterStringsAndRunesIsStillRead(t *testing.T) {
	for name, body := range map[string]string{
		"after a string with slashes in it": "s := \"a//b\" // adapted from foo\n",
		"after a raw string":                "s := `x` // translated from the C original\n",
		"after an escaped quote":            "s := \"a \\\" b\" // ported from tmux\n",
		"after a double-quote rune":         "q := '\"' // adapted from x\n",
		"after a backslash-quote rune":      "q := '\\'' // adapted from x\n",
		"after a raw string over lines":     "s := `a\nb` // ported from tmux\n",
	} {
		t.Run(name, func(t *testing.T) {
			if !noticeFor(t, body) {
				t.Errorf("%q: no notice, but a real comment follows", body)
			}
		})
	}
}

// A block comment opened after code on the same line is a comment: its text
// is read, on that line and on the lines that follow until its close.
func TestScan_BlockCommentOpenedMidLineIsScanned(t *testing.T) {
	for name, body := range map[string]string{
		"one line":                     "x := 1 /* ported from tmux */\n",
		"opened mid-line, text inside": "x := 1 /* adapted from foo.c\n   and more */\n",
		"text on a later line":         "x := 1 /* opens here\n * Borrowed from musl.\n */\n",
		"closed, then a line comment":  "x := 1 /* c */ // translated from the C original\n",
		"after a string":               "s := \"x\"; /* copied from y */\n",
	} {
		t.Run(name, func(t *testing.T) {
			if !noticeFor(t, body) {
				t.Errorf("%q: no notice, but the words are in a block comment", body)
			}
		})
	}
}

// Code after the close of a mid-line block comment is code again.
func TestScan_CodeAfterAMidLineBlockCommentIsNotComment(t *testing.T) {
	for name, body := range map[string]string{
		"same line":                      "x := 1 /* plain */ + len(\"ported from\")\n",
		"next line":                      "x := 1 /* plain\n   still plain */\ns := \"copied from\"\n",
		"closing line":                   "x := 1 /* opens\n   closes */ s := \"adapted from\"\n",
		"opener in a string, later line": "s := \"/*\"\nt := \"ported from\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if noticeFor(t, body) {
				t.Errorf("%q: flagged a notice, but the words are in code", body)
			}
		})
	}
}
