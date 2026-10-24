package ui

// Pins #293 S1 (the recovery-command box must read back as the
// installer's command, word for word, as bash reads it). The installer
// writes every word of its recovery line with bash printf %q, which
// uses bare backslash escapes (My\ Orbit, it\'s, a\(b\)) in a UTF-8
// locale and the $'...' form with octal bytes (\303\251) in the C
// locale. The box cuts the line into rows joined by a trailing "\"; a
// cut placed inside an escape ends the command early or runs another.
//
// The oracle is bash itself: shims for cd, docker, sudo and rm print
// each word they are given on its own line, so the original command and
// the box's rows must print the same bytes. Fixtures are made by bash
// printf %q, never quoted by hand.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

const secS3Shims = `cd() { printf '%s\n' cd "$@"; }
docker() { printf '%s\n' docker "$@"; }
sudo() { printf '%s\n' sudo "$@"; }
rm() { printf '%s\n' rm "$@"; }
`

// secS3Env is the environment with LANG and every LC_* removed and
// LC_ALL set to lcAll, the shape of deploy's groupBEvalIn.
func secS3Env(lcAll string) []string {
	env := []string{}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "LC_") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "LC_ALL="+lcAll)
}

func secS3Bash(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is needed: %v", err)
	}
	return bash
}

// secS3InputFile writes NUL-separated items to a file for bash to read
// on stdin (a file, not a pipe, so bash's read buffers it).
func secS3InputFile(t *testing.T, items []string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "secs3-in")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range items {
		if strings.ContainsRune(s, 0) {
			t.Fatalf("item holds a NUL: %q", s)
		}
		f.WriteString(s)
		f.WriteString("\x00")
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// secS3PrintfQ quotes each raw word with bash printf %q under lcAll,
// LANG removed, in one bash process.
func secS3PrintfQ(t *testing.T, lcAll string, raw []string) []string {
	t.Helper()
	in := secS3InputFile(t, raw)
	cmd := exec.Command(secS3Bash(t), "-c", `while IFS= read -r -d '' w; do printf '%q\0' "$w"; done`)
	cmd.Env = secS3Env(lcAll)
	cmd.Stdin = in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("bash printf %%q under LC_ALL=%s: %v (%s)", lcAll, err, stderr.String())
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(got) != len(raw) {
		t.Fatalf("bash printf %%q gave %d words for %d", len(got), len(raw))
	}
	return got
}

type secS3ReadBack struct {
	out    string // what the shims printed
	status int
	stderr string
}

// secS3ReadBacks runs each script (the shims prepended) as bash reads
// it, under lcAll. Scripts are shared out among a few bash processes run
// side by side; each process evals its scripts in turn, writing a
// separator before each on stdout and stderr so every script's output,
// errors and exit status are kept apart.
func secS3ReadBacks(t *testing.T, lcAll string, scripts []string) []secS3ReadBack {
	t.Helper()
	bash := secS3Bash(t)
	procs := min(8, runtime.NumCPU(), max(1, len(scripts)/200))
	per := (len(scripts) + procs - 1) / procs
	res := make([]secS3ReadBack, len(scripts))
	errs := make([]error, procs)
	var wg sync.WaitGroup
	for p := 0; p < procs; p++ {
		lo, hi := p*per, min(len(scripts), (p+1)*per)
		if lo >= hi {
			continue
		}
		in := secS3InputFile(t, scripts[lo:hi])
		wg.Add(1)
		go func(p, lo, hi int, in *os.File) {
			defer wg.Done()
			errs[p] = secS3ReadBackChunk(bash, lcAll, in, res[lo:hi])
		}(p, lo, hi, in)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("bash read-back under LC_ALL=%s: %v", lcAll, err)
		}
	}
	return res
}

func secS3ReadBackChunk(bash, lcAll string, in *os.File, res []secS3ReadBack) error {
	cmd := exec.Command(bash, "-c", `
shims=$1
while IFS= read -r -d '' s; do
	printf '\036'
	printf '\036' >&2
	eval "$shims$s"
	printf '\037%d' "$?"
done`, "_", secS3Shims)
	cmd.Env = secS3Env(lcAll)
	cmd.Stdin = in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, stderr.String())
	}
	parts := strings.Split(string(out), "\036")
	eparts := strings.Split(stderr.String(), "\036")
	if len(parts) != len(res)+1 || parts[0] != "" || len(eparts) != len(res)+1 || eparts[0] != "" {
		return fmt.Errorf("gave %d records (%d on stderr) for %d scripts; stderr before the first: %q", len(parts)-1, len(eparts)-1, len(res), eparts[0])
	}
	for i, p := range parts[1:] {
		j := strings.LastIndex(p, "\037")
		if j < 0 {
			return fmt.Errorf("record %d has no status: %q", i, p)
		}
		status, err := strconv.Atoi(p[j+1:])
		if err != nil {
			return fmt.Errorf("record %d: bad status %q", i, p[j+1:])
		}
		res[i] = secS3ReadBack{out: p[:j], status: status, stderr: eparts[i+1]}
	}
	return nil
}

// secS3RecoveryLine is the installer's recovery line for an already
// quoted engine-folder word.
func secS3RecoveryLine(quoted string) string {
	return "cd " + quoted + " && docker compose --project-name orbit --env-file .env-orbit down --remove-orphans"
}

func secS3EnginePath(folder string) string {
	return folder + "/orbit-install-backups/20261020T101010Z/engine"
}

// secS3BoxCase is one recovery line drawn at one width.
type secS3BoxCase struct {
	label  string
	line   string
	width  int
	framed []string // the box's rows, ANSI stripped, frame on
	rows   []string // the box's rows, frame off
}

type secS3Job struct {
	label, line string
	width       int
}

func secS3Draw(j secS3Job) (secS3BoxCase, error) {
	styled := commandBox(j.line, j.width)
	framed := strings.Split(ansi.Strip(strings.Join(styled, "\n")), "\n")
	rows, ok := gbStBox(strings.Join(framed, "\n"))
	if !ok || len(rows) == 0 {
		return secS3BoxCase{}, fmt.Errorf("%s at width %d: commandBox drew no box:\n%s", j.label, j.width, strings.Join(framed, "\n"))
	}
	return secS3BoxCase{label: j.label, line: j.line, width: j.width, framed: framed, rows: rows}, nil
}

// secS3DrawAll draws every job's box, several at a time.
func secS3DrawAll(t *testing.T, jobs []secS3Job) []secS3BoxCase {
	t.Helper()
	cases := make([]secS3BoxCase, len(jobs))
	errs := make([]error, len(jobs))
	workers := runtime.NumCPU()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < len(jobs); i += workers {
				cases[i], errs[i] = secS3Draw(jobs[i])
			}
		}(w)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	return cases
}

// secS3Cached keeps each fixture set once built, so the shape test and
// the read-back tests draw the same boxes without drawing them twice.
var secS3Cached = struct {
	sync.Mutex
	m map[string][]secS3BoxCase
}{m: map[string][]secS3BoxCase{}}

func secS3Cache(t *testing.T, key string, build func(*testing.T) []secS3BoxCase) []secS3BoxCase {
	t.Helper()
	secS3Cached.Lock()
	defer secS3Cached.Unlock()
	if c, ok := secS3Cached.m[key]; ok {
		return c
	}
	c := build(t)
	secS3Cached.m[key] = c
	return c
}

// secS3CheckReadBacks runs every case's original line and box rows
// through bash and reports any whose read-backs differ. It returns the
// box read-backs, in case order, for further checks.
func secS3CheckReadBacks(t *testing.T, lcAll string, cases []secS3BoxCase) []secS3ReadBack {
	t.Helper()
	// Each distinct original line is read back once; then every box.
	lineAt := map[string]int{}
	var scripts []string
	for _, c := range cases {
		if _, ok := lineAt[c.line]; !ok {
			lineAt[c.line] = len(scripts)
			scripts = append(scripts, c.line)
		}
	}
	first := len(scripts)
	for _, c := range cases {
		scripts = append(scripts, strings.Join(c.rows, "\n"))
	}
	res := secS3ReadBacks(t, lcAll, scripts)
	boxes := res[first:]
	failed := 0
	for i, c := range cases {
		want, got := res[lineAt[c.line]], boxes[i]
		if want.status != 0 || want.stderr != "" {
			t.Fatalf("%s: the original line does not run cleanly in bash (status %d): %s\n%s", c.label, want.status, want.stderr, c.line)
		}
		if got.out == want.out && got.status == 0 && got.stderr == "" {
			continue
		}
		failed++
		if failed <= 5 {
			t.Errorf("%s at width %d (LC_ALL=%s): the box does not read back as the command\nrows:\n%s\nbash read it as:\n%s(status %d, stderr %q)\nwant:\n%s",
				c.label, c.width, lcAll, strings.Join(c.rows, "\n"), secS3Brackets(got.out), got.status, got.stderr, secS3Brackets(want.out))
		}
	}
	if failed > 0 {
		t.Errorf("LC_ALL=%s: %d of %d boxes do not read back as their command", lcAll, failed, len(cases))
	}
	return boxes
}

func secS3Brackets(out string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		b.WriteString("[" + l + "] ")
	}
	return b.String() + "\n"
}

// secS3CheckShape fails for a box whose rows break the drawn shape: a
// framed row wider than width-4 cells, a row but the last not ending in
// "\", a row holding "…".
func secS3CheckShape(t *testing.T, cases []secS3BoxCase) {
	t.Helper()
	failed := 0
	for _, c := range cases {
		bad := ""
		for i, f := range c.framed {
			if w := lipgloss.Width(f); w > c.width-4 {
				bad = fmt.Sprintf("framed row %d is %d cells, over %d: %q", i, w, c.width-4, f)
			}
		}
		for i, r := range c.rows {
			if i < len(c.rows)-1 && !strings.HasSuffix(r, "\\") {
				bad = fmt.Sprintf("row %d does not end in \\: %q", i, r)
			}
			if strings.Contains(r, "…") {
				bad = fmt.Sprintf("row %d holds …: %q", i, r)
			}
		}
		if bad != "" {
			failed++
			if failed <= 5 {
				t.Errorf("%s at width %d: %s\n%s", c.label, c.width, bad, strings.Join(c.framed, "\n"))
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d of %d boxes break the shape", failed, len(cases))
	}
}

// --- fixtures ---------------------------------------------------------------

var secS3BareNames = []string{"My Orbit", "it's", "a(b)", "a$b", `x\y`, "#tag", "a!b"}

var secS3BareWidths = []int{50, 80, 120}

// secS3BareCases is case 1: each name under /home/ + N×u + / for
// N = 0..80, quoted by printf %q under C.UTF-8, drawn at 50, 80, 120.
func secS3BareCases(t *testing.T) []secS3BoxCase {
	t.Helper()
	var labels, raw []string
	for _, name := range secS3BareNames {
		for n := 0; n <= 80; n++ {
			folder := "/home/" + strings.Repeat("u", n) + "/" + name
			raw = append(raw, secS3EnginePath(folder))
			labels = append(labels, fmt.Sprintf("name %q, N=%d", name, n))
		}
	}
	quoted := secS3PrintfQ(t, "C.UTF-8", raw)
	var jobs []secS3Job
	for i, q := range quoted {
		line := secS3RecoveryLine(q)
		for _, w := range secS3BareWidths {
			jobs = append(jobs, secS3Job{labels[i], line, w})
		}
	}
	return secS3DrawAll(t, jobs)
}

// secS3OctalCases is case 2: café/orbit, and /srv/ + a×é +
// $(echo INJECTED) + b×é + x for a, b in 1..30, quoted by printf %q
// under C (so é is \303\251 inside $'...'), drawn at widths 30..60.
func secS3OctalCases(t *testing.T) []secS3BoxCase {
	t.Helper()
	const e = "é"
	labels := []string{"name caf" + e + "/orbit"}
	raw := []string{secS3EnginePath("caf" + e + "/orbit")}
	for a := 1; a <= 30; a++ {
		for b := 1; b <= 30; b++ {
			folder := "/srv/" + strings.Repeat(e, a) + "$(echo INJECTED)" + strings.Repeat(e, b) + "x"
			raw = append(raw, secS3EnginePath(folder))
			labels = append(labels, fmt.Sprintf("marker name a=%d b=%d", a, b))
		}
	}
	quoted := secS3PrintfQ(t, "C", raw)
	for i, q := range quoted {
		if !strings.HasPrefix(q, "$'") {
			t.Fatalf("%s: printf %%q under LC_ALL=C did not give the $'...' form: %s", labels[i], q)
		}
	}
	var jobs []secS3Job
	for i, q := range quoted {
		line := secS3RecoveryLine(q)
		for w := 30; w <= 60; w++ {
			jobs = append(jobs, secS3Job{labels[i], line, w})
		}
	}
	return secS3DrawAll(t, jobs)
}

// --- the tests --------------------------------------------------------------

// Case 1: bare backslash escapes from printf %q in a UTF-8 locale. At
// head, N=59 "My Orbit" at width 80 ends a row in My\\ and bash runs
// "Orbit/..." as a second command.
func TestSecS3_RecoveryBoxReadsBackPrintfQBareEscapes(t *testing.T) {
	secS3CheckReadBacks(t, "C.UTF-8", secS3Cache(t, "bare", secS3BareCases))
}

// Case 2: the $'...' form with octal bytes, LC_ALL=C. The box must read
// back as the command, and the marker's command substitution must never
// run: "INJECTED" never stands on a line of its own in the read-back.
func TestSecS3_RecoveryBoxReadsBackAnsiCOctalEscapes(t *testing.T) {
	cases := secS3Cache(t, "octal", secS3OctalCases)
	boxes := secS3CheckReadBacks(t, "C", cases)
	ran := 0
	for i, rb := range boxes {
		ownLine := false
		for _, l := range strings.Split(rb.out, "\n") {
			ownLine = ownLine || l == "INJECTED"
		}
		// "INJECTED" may appear only inside the literal $(echo INJECTED).
		bare := strings.Count(rb.out, "INJECTED") != strings.Count(rb.out, "$(echo INJECTED)")
		if ownLine || bare {
			ran++
			if ran <= 5 {
				t.Errorf("%s at width %d: the box's rows ran $(echo INJECTED)\nrows:\n%s\nbash read it as:\n%s", cases[i].label, cases[i].width, strings.Join(cases[i].rows, "\n"), secS3Brackets(rb.out))
			}
		}
	}
	if ran > 0 {
		t.Errorf("%d of %d boxes ran the marker's command substitution", ran, len(cases))
	}
}

// Case 3: the shape every box from cases 1 and 2 keeps.
func TestSecS3_BoxRowsKeepTheirShape(t *testing.T) {
	secS3CheckShape(t, secS3Cache(t, "bare", secS3BareCases))
	secS3CheckShape(t, secS3Cache(t, "octal", secS3OctalCases))
}

// Case 4: the Remove command's single-quoted words (shellQuote, with
// '\” for an apostrophe) still read back through wrapShellCommand,
// judged by the same bash reader.
func TestSecS3_SingleQuotedRemovalStillReadsBack(t *testing.T) {
	var cases []secS3BoxCase
	for _, dir := range []string{"/home/sam/my orbit", "/opt/it's"} {
		line := removalLine(t, dir)
		for _, limit := range []int{30, 42, 56} {
			cases = append(cases, secS3BoxCase{
				label: fmt.Sprintf("removal of %q, limit %d", dir, limit),
				line:  line,
				width: limit,
				rows:  wrapShellCommand(removalWords(t, dir), limit),
			})
		}
	}
	secS3CheckReadBacks(t, "C.UTF-8", cases)
}
