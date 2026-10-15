// No real personal data in the repository (#244). Fixtures, mockups and docs
// use invented names only; this guard keeps a real person's name, a real host
// login in a home-directory path, or a private network address from coming
// back into tracked text. Only the sha256 digest of each banned value is stored
// here, so the guard does not itself repeat what it forbids.
//
// Go's regexp has no lookbehind or lookahead, so the two boundary conditions
// the checks need (a `~` not glued to a preceding word character or slash, and
// a dotted quad not embedded in a longer run of digits and dots) are written
// out by hand in loginsOf and ipv4Matches.
package privacy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const root = "../.."

// bannedWords holds the sha256 of each banned word, lower case.
var bannedWords = map[string]bool{
	// a real person's surname, #244
	"4aa59b5a3761c43828cf2c0f44a385d94d078934936482f66e573cb444e7e481": true,
}

// bannedLogins holds the sha256 of each banned host login, lower case. A real
// login name leaks through a home-directory path, so only path segments are
// checked: a login can also be an ordinary first name that fixtures
// legitimately use.
var bannedLogins = map[string]bool{
	// the owner's host login, #244
	"e1608f75c5d7813f3d4031cb30bfb786507d98137538ff8e128a6ff74e84e643": true,
	// the account automated agents run under on the development host, #244
	"57de4cf40144bdf7d00010f2f5557a7d642c2b9705309bfade167dd313e2ca93": true,
}

// fakeAddresses lists, per tracked file, private or shared IPv4 addresses known
// to be invented: classifier or redaction test inputs on a textbook subnet,
// never paired with a host name or anything else that would place them on a
// real network. A new address needs a new reason here. It starts empty.
var fakeAddresses = map[string]map[string]bool{}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
var wordToken = regexp.MustCompile(`[a-z0-9]+`)

// wordsOf returns the lower-case words a line could spell a banned word with:
// camelCase runs split, and each token also tried without a trailing `s` or
// `es`, because a plural is the commonest way the word reappears.
func wordsOf(line string) map[string]bool {
	words := map[string]bool{}
	split := camelBoundary.ReplaceAllString(line, "$1 $2")
	for _, token := range wordToken.FindAllString(strings.ToLower(split), -1) {
		words[token] = true
		if len(token) <= 2 {
			continue
		}
		if strings.HasSuffix(token, "s") {
			words[strings.TrimSuffix(token, "s")] = true
		}
		if strings.HasSuffix(token, "es") {
			words[strings.TrimSuffix(token, "es")] = true
		}
	}
	return words
}

// homeDirectory matches `/home/<login>`, `/Users/<login>`, `C:\Users\<login>`
// (any slash, escaped or not, either case) and `~<login>`. The `~` form must
// not follow a word character or a slash; Go has no lookbehind, so loginsOf
// checks that itself.
var homeDirectory = regexp.MustCompile(`(?:/home/|[/\\]+[Uu][Ss][Ee][Rr][Ss][/\\]+|~)([A-Za-z0-9._-]+)`)

var loginTail = regexp.MustCompile(`[.-]+$`)

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// loginsOf returns the lower-case login candidates of every home-directory path
// in a line: the whole segment, the segment without a trailing `.` or `-` (end
// of a sentence), and the part before its first `.` or `-` (`/home/<login>-old`).
func loginsOf(line string) map[string]bool {
	logins := map[string]bool{}
	for _, m := range homeDirectory.FindAllStringSubmatchIndex(line, -1) {
		if line[m[0]] == '~' && m[0] > 0 && (isWordByte(line[m[0]-1]) || line[m[0]-1] == '/') {
			continue
		}
		login := strings.ToLower(line[m[2]:m[3]])
		logins[login] = true
		logins[loginTail.ReplaceAllString(login, "")] = true
		first, _, _ := strings.Cut(strings.NewReplacer("-", ".").Replace(login), ".")
		logins[first] = true
	}
	return logins
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// ipv4Matches returns every dotted quad in a line that is not embedded in a
// longer run of digits and dots (version strings, OIDs): it must not follow a
// digit or dot, and no digit may appear in the run of digits and dots after it.
// Scanning resumes after each match.
func ipv4Matches(line string) []string {
	var found []string
	for i := 0; i < len(line); {
		if i > 0 && (isDigit(line[i-1]) || line[i-1] == '.') {
			i++
			continue
		}
		p := i
		ok := true
		for octet := 0; octet < 4 && ok; octet++ {
			n := 0
			for n < 3 && p+n < len(line) && isDigit(line[p+n]) {
				n++
			}
			if n == 0 {
				ok = false
				break
			}
			p += n
			if octet < 3 {
				if p < len(line) && line[p] == '.' {
					p++
				} else {
					ok = false
				}
			}
		}
		if ok {
			for q := p; q < len(line) && (isDigit(line[q]) || line[q] == '.'); q++ {
				if isDigit(line[q]) {
					ok = false
					break
				}
			}
		}
		if !ok {
			i++
			continue
		}
		found = append(found, line[i:p])
		i = p
	}
	return found
}

// isPrivateOrShared reports whether an address is in an RFC 1918 private range
// or RFC 6598 shared (CGNAT) space. The documentation ranges of RFC 5737 fall
// outside both. An octet above 255 makes it no address at all.
func isPrivateOrShared(address string) bool {
	parts := strings.Split(address, ".")
	if len(parts) != 4 {
		return false
	}
	var o [4]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n > 255 {
			return false
		}
		o[i] = n
	}
	return o[0] == 10 ||
		o[0] == 172 && o[1] >= 16 && o[1] <= 31 ||
		o[0] == 192 && o[1] == 168 ||
		o[0] == 100 && o[1] >= 64 && o[1] <= 127
}

type trackedFile struct {
	name string
	text string
}

// trackedTextFiles reads every file git tracks, skipping any that cannot be
// read (deleted in the working tree, or a directory entry such as a submodule)
// and any with a NUL in its first 8192 bytes (binary).
func trackedTextFiles(t *testing.T) []trackedFile {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	var files []trackedFile
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		head := data
		if len(head) > 8192 {
			head = head[:8192]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			continue
		}
		files = append(files, trackedFile{name: name, text: string(data)})
	}
	return files
}

func TestNoTrackedTextFileContainsABannedWord(t *testing.T) {
	var hits []string
	for _, f := range trackedTextFiles(t) {
		for i, line := range strings.Split(f.text, "\n") {
			for token := range wordsOf(line) {
				if bannedWords[digest(token)] {
					hits = append(hits, fmt.Sprintf("%s:%d", f.name, i+1))
				}
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("a banned word appears at:\n%s\nuse an invented name instead (#244)", strings.Join(hits, "\n"))
	}
}

func TestNoTrackedTextFileNamesARealHostLoginInAHomeDirectoryPath(t *testing.T) {
	var hits []string
	for _, f := range trackedTextFiles(t) {
		for i, line := range strings.Split(f.text, "\n") {
			for login := range loginsOf(line) {
				if bannedLogins[digest(login)] {
					hits = append(hits, fmt.Sprintf("%s:%d", f.name, i+1))
				}
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("a real host login appears in a home-directory path at:\n%s\n"+
			"use ~/..., <project root> or an invented account such as /home/alex (#244)", strings.Join(hits, "\n"))
	}
}

func TestNoTrackedTextFileHoldsAPrivateOrSharedAddressOutsideTheKnownFakeFixtures(t *testing.T) {
	var hits []string
	for _, f := range trackedTextFiles(t) {
		for i, line := range strings.Split(f.text, "\n") {
			for _, address := range ipv4Matches(line) {
				if !isPrivateOrShared(address) || fakeAddresses[f.name][address] {
					continue
				}
				hits = append(hits, fmt.Sprintf("%s:%d %s", f.name, i+1, address))
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("a private or shared IPv4 address appears at:\n%s\n"+
			"use a documentation address (192.0.2.x, 198.51.100.x, 203.0.113.x) "+
			"or add a clearly fake fixture to fakeAddresses (#244)", strings.Join(hits, "\n"))
	}
}

// The matchers, tested directly with invented words (no digests, no real
// names). Private addresses are built from numbers, not written out, so this
// file does not trip its own address check.

func quad(a, b, c, d int) string { return fmt.Sprintf("%d.%d.%d.%d", a, b, c, d) }

func requireWords(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	for _, w := range want {
		if !got[w] {
			t.Errorf("%q missing from %v", w, got)
		}
	}
}

func TestWordsOfFindsASurnameInItsPluralAndPossessiveForms(t *testing.T) {
	requireWords(t, wordsOf("the Brightwells' boiler"), "brightwell")
	requireWords(t, wordsOf("BRIGHTWELLS"), "brightwell")
}

func TestWordsOfFindsASurnameInsideACamelCaseOrUpperSnakeRun(t *testing.T) {
	requireWords(t, wordsOf("const brightwellItems = []"), "brightwell", "items")
	requireWords(t, wordsOf("BRIGHTWELL_ITEMS"), "brightwell")
}

func TestWordsOfStillFindsTheBareWordAndDoesNotJoinSeparateWords(t *testing.T) {
	requireWords(t, wordsOf("brightwell"), "brightwell")
	if wordsOf("bright well")["brightwell"] {
		t.Error("separate words were joined")
	}
}

func TestLoginsOfFindsALoginThatEndsASentenceOrIsFollowedByASuffix(t *testing.T) {
	requireWords(t, loginsOf("files live in /home/alex."), "alex")
	requireWords(t, loginsOf("under ~alex."), "alex")
	requireWords(t, loginsOf("/home/alex-old"), "alex")
}

func TestLoginsOfFindsADottedLoginThatEndsASentence(t *testing.T) {
	requireWords(t, loginsOf("files live in /home/al-ex."), "al-ex")
	requireWords(t, loginsOf("under ~al.ex."), "al.ex")
}

func TestLoginsOfFindsALoginInAnAllCapsWindowsPath(t *testing.T) {
	requireWords(t, loginsOf(`C:\USERS\alex\x`), "alex")
}

func TestLoginsOfFindsALoginInALowerCaseWindowsPathAndInAnOrdinaryPath(t *testing.T) {
	requireWords(t, loginsOf(`c:\users\alex\x`), "alex")
	requireWords(t, loginsOf("/home/alex/projects"), "alex")
}

func TestLoginsOfIgnoresAPathWithNoLeadingSlash(t *testing.T) {
	if got := loginsOf("home/alex"); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestLoginsOfIgnoresATildeGluedToAWordCharacterOrSlash(t *testing.T) {
	for _, line := range []string{"a~alex", "_~alex", "dir/~alex"} {
		if got := loginsOf(line); len(got) != 0 {
			t.Errorf("%q: got %v, want none", line, got)
		}
	}
	requireWords(t, loginsOf("cd ~alex"), "alex")
	requireWords(t, loginsOf("see (~alex)"), "alex")
	requireWords(t, loginsOf("a.~alex"), "alex")
}

func TestIPv4MatchesFindsADottedQuadAndNotOneInsideALongerRun(t *testing.T) {
	address := quad(10, 0, 0, 1)
	for _, line := range []string{address, "host " + address + ":8080", "(" + address + ").", address + ".", "x" + address} {
		got := ipv4Matches(line)
		if len(got) != 1 || got[0] != address {
			t.Errorf("%q: got %v, want [%s]", line, got, address)
		}
	}
	for _, line := range []string{"1.2.3.4.5", address + ".5", "." + address, "1.2.3.4567", "12345.1.1.1", "1.2.3", "v1.2.3"} {
		if got := ipv4Matches(line); len(got) != 0 {
			t.Errorf("%q: got %v, want none", line, got)
		}
	}
	if got := ipv4Matches(quad(10, 0, 0, 1) + " " + quad(192, 0, 2, 1)); len(got) != 2 {
		t.Errorf("two addresses on a line: got %v", got)
	}
}

func TestIsPrivateOrSharedClassifiesTheRanges(t *testing.T) {
	for _, c := range []struct {
		address string
		want    bool
	}{
		{quad(10, 0, 0, 1), true},
		{quad(10, 255, 255, 255), true},
		{quad(172, 16, 0, 1), true},
		{quad(172, 31, 255, 1), true},
		{quad(172, 15, 0, 1), false},
		{quad(172, 32, 0, 1), false},
		{quad(192, 168, 1, 1), true},
		{quad(192, 169, 1, 1), false},
		{quad(100, 64, 0, 1), true},
		{quad(100, 127, 0, 1), true},
		{quad(100, 63, 0, 1), false},
		{quad(100, 128, 0, 1), false},
		{"192.0.2.1", false},
		{"198.51.100.7", false},
		{"203.0.113.9", false},
		{"1.2.3.4", false},
		{"300.1.1.1", false},
		{"10.0.0.300", false},
		{"1.2.3.4.5", false},
	} {
		if got := isPrivateOrShared(c.address); got != c.want {
			t.Errorf("isPrivateOrShared(%q) = %v, want %v", c.address, got, c.want)
		}
	}
	// 300.1.1.1 is matched as a dotted quad but is no address.
	if got := ipv4Matches("300.1.1.1"); len(got) != 1 || isPrivateOrShared(got[0]) {
		t.Errorf("300.1.1.1: got %v", got)
	}
}
