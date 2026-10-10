package engine

// Pins #209 (engine vocabulary, #199 WI-5; EN-6, EN-16): the three
// line parsers — engine events, machine prompts and repair output —
// read `key=value` tokens through one keyValueFields(tokens), and the
// repair counts share one nonNegative(s) fallback. Event lines keep
// their column-0 `phase=` check; prompt.go keeps its attempt floor of 1.

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// callGraph maps each package-level function (methods by bare name) to
// the package-level functions it names, called or passed as a value.
func callGraph(files []*ast.File) (map[string]map[string]bool, map[string]*ast.FuncDecl) {
	decls := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				decls[fd.Name.Name] = fd
			}
		}
	}
	graph := map[string]map[string]bool{}
	for name, fd := range decls {
		edges := map[string]bool{}
		if fd.Body != nil {
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					if _, isFunc := decls[id.Name]; isFunc && id.Name != name {
						edges[id.Name] = true
					}
				}
				return true
			})
		}
		graph[name] = edges
	}
	return graph, decls
}

func reaches(graph map[string]map[string]bool, from, to string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range graph[cur] {
			if next == to {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

func countDecls(files []*ast.File, name string) int {
	n := 0
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
				n++
			}
		}
	}
	return n
}

// allParsers are the exported entry points of the three line parsers.
var allParsers = []string{
	"ParseEvent",
	"ParsePromptLine",
	"ParseFinding", "ParseDiagnosis", "ParsePlanAction", "ParsePlanSummary",
	"ParseExecuteResult", "ParseExecutionSummary", "ParseDangerous",
}

// TestKeyValueFieldsIsTheOneTokenizer pins EN-6's single home: one
// keyValueFields, reached by every parser, and no other function in the
// package splitting on "=" itself.
func TestKeyValueFieldsIsTheOneTokenizer(t *testing.T) {
	fset, files := sourceFiles(t)
	if n := countDecls(files, "keyValueFields"); n != 1 {
		t.Fatalf("keyValueFields is declared %d times in engine, want exactly once", n)
	}
	graph, decls := callGraph(files)
	for _, p := range allParsers {
		if _, ok := decls[p]; !ok {
			t.Errorf("%s is not declared", p)
			continue
		}
		if !reaches(graph, p, "keyValueFields") {
			t.Errorf("%s does not reach keyValueFields: it tokenizes key=value on its own", p)
		}
	}
	var others []string
	for name, fd := range decls {
		if name == "keyValueFields" || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && ((lit.Kind == token.STRING && (lit.Value == `"="` || lit.Value == "`=`")) ||
				(lit.Kind == token.CHAR && lit.Value == `'='`)) {
				others = append(others, name+" at "+fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	sort.Strings(others)
	for _, o := range others {
		t.Errorf("a second key=value split outside keyValueFields: %s", o)
	}
}

// TestNonNegativeIsTheOneCountRule pins EN-16's single home: every
// repair parser that reads a count reaches one nonNegative helper.
func TestNonNegativeIsTheOneCountRule(t *testing.T) {
	_, files := sourceFiles(t)
	if n := countDecls(files, "nonNegative"); n != 1 {
		t.Fatalf("nonNegative is declared %d times in engine, want exactly once", n)
	}
	graph, _ := callGraph(files)
	for _, p := range []string{"ParseDiagnosis", "ParsePlanSummary", "ParseExecutionSummary", "ParseDangerous"} {
		if !reaches(graph, p, "nonNegative") {
			t.Errorf("%s does not reach nonNegative: it carries its own count fallback", p)
		}
	}
}

// carrier is one parser read through a field it carries verbatim and
// requires. tokens holds the line with the carried token at index slot.
type carrier struct {
	name   string
	key    string
	tokens []string
	slot   int
	parse  func(line string) (value string, ok bool)
}

var carriers = []carrier{
	{
		name:   "event",
		key:    "reason",
		tokens: []string{"phase=host", "component=host", "state=waiting", "", "action=begin", "elapsed=1s"},
		slot:   3,
		parse: func(line string) (string, bool) {
			e, ok := ParseEvent(line)
			return e.Reason, ok
		},
	},
	{
		name:   "prompt-reject",
		key:    "reason",
		tokens: []string{"prompt-reject", "field=APP_URL", ""},
		slot:   2,
		parse: func(line string) (string, bool) {
			msg, ok := ParsePromptLine(line)
			if !ok {
				return "", false
			}
			r, isReject := msg.(PromptReject)
			if !isReject {
				return "", false
			}
			return r.Reason, true
		},
	},
	{
		name:   "dangerous",
		key:    "reason",
		tokens: []string{"dangerous", "result=refused", "done=0", "failed=0", ""},
		slot:   4,
		parse: func(line string) (string, bool) {
			d, ok := ParseDangerous(line)
			return d.Reason, ok
		},
	},
	{
		name:   "finding",
		key:    "class",
		tokens: []string{"finding", "", "target=compose-file", "severity=warn"},
		slot:   1,
		parse: func(line string) (string, bool) {
			f, ok := ParseFinding(line)
			return f.Class, ok
		},
	},
}

// line renders c with the carried slot replaced by slotTokens, joined
// by sep, followed by trailing.
func (c carrier) line(slotTokens []string, sep, trailing string) string {
	var toks []string
	for i, tok := range c.tokens {
		if i == c.slot {
			toks = append(toks, slotTokens...)
			continue
		}
		toks = append(toks, tok)
	}
	return strings.Join(toks, sep) + trailing
}

type parsed struct {
	value string
	ok    bool
}

// TestParsersShareTokenizerBehaviour pins EN-6's observable rule: the
// three parsers tokenize identically. Where the decisions leave the
// exact outcome open (empty value, duplicate key, a bare word, an empty
// key) the test pins agreement only; where an outcome is the contract's
// or the common key=value rule, it pins the outcome too.
func TestParsersShareTokenizerBehaviour(t *testing.T) {
	for _, tc := range []struct {
		name     string
		slot     func(key string) []string
		sep      string
		trailing string
		want     *parsed // nil: every parser must agree, outcome unpinned
	}{
		{name: "plain value", slot: func(k string) []string { return []string{k + "=x-y"} }, sep: " ", want: &parsed{"x-y", true}},
		{name: "double spaces between tokens", slot: func(k string) []string { return []string{k + "=x-y"} }, sep: "  ", want: &parsed{"x-y", true}},
		{name: "tab between tokens", slot: func(k string) []string { return []string{k + "=x-y"} }, sep: "\t", want: &parsed{"x-y", true}},
		{name: "trailing whitespace", slot: func(k string) []string { return []string{k + "=x-y"} }, sep: " ", trailing: " \t", want: &parsed{"x-y", true}},
		{name: "trailing carriage return", slot: func(k string) []string { return []string{k + "=x-y"} }, sep: " ", trailing: "\r", want: &parsed{"x-y", true}},
		{name: "equals sign inside the value splits on the first", slot: func(k string) []string { return []string{k + "=a=b"} }, sep: " ", want: &parsed{"a=b", true}},
		{name: "unknown key beside the carried one", slot: func(k string) []string { return []string{"zz-future=1", k + "=x-y"} }, sep: " ", want: &parsed{"x-y", true}},
		{name: "empty value", slot: func(k string) []string { return []string{k + "="} }, sep: " "},
		{name: "duplicate key", slot: func(k string) []string { return []string{k + "=first", k + "=second"} }, sep: " "},
		{name: "bare word beside the carried one", slot: func(k string) []string { return []string{"stray", k + "=x-y"} }, sep: " "},
		{name: "empty key", slot: func(k string) []string { return []string{"=orphan", k + "=x-y"} }, sep: " "},
		{name: "carried key missing", slot: func(k string) []string { return nil }, sep: " ", want: &parsed{"", false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := map[string]parsed{}
			for _, c := range carriers {
				line := c.line(tc.slot(c.key), tc.sep, tc.trailing)
				v, ok := c.parse(line)
				if !ok {
					v = ""
				}
				got := parsed{v, ok}
				results[c.name] = got
				if tc.want != nil && got != *tc.want {
					t.Errorf("%s: %q → value %q ok %v, want value %q ok %v", c.name, line, got.value, got.ok, tc.want.value, tc.want.ok)
				}
			}
			if tc.want == nil {
				first := carriers[0].name
				for _, c := range carriers[1:] {
					if results[c.name] != results[first] {
						t.Errorf("parsers disagree: %s → %+v, %s → %+v", first, results[first], c.name, results[c.name])
					}
				}
			}
		})
	}
}

// TestEventsKeepColumnZero pins the half of EN-6 that stays: an event
// line is only an event when `phase=` starts at column 0, while the
// lead-word parsers keep accepting leading whitespace.
func TestEventsKeepColumnZero(t *testing.T) {
	const event = "phase=host component=host state=waiting reason=initial action=begin elapsed=1s"
	if _, ok := ParseEvent(event); !ok {
		t.Fatalf("%q: a column-0 event must parse", event)
	}
	for _, lead := range []string{" ", "  ", "\t", " \t"} {
		if _, ok := ParseEvent(lead + event); ok {
			t.Errorf("%q: an indented phase= line parsed as an event; events are column-0 only", lead+event)
		}
		for _, line := range []string{
			"prompt field=APP_URL kind=url required=true attempt=1",
			"prompt-reject field=APP_URL reason=not-https",
		} {
			if _, ok := ParsePromptLine(lead + line); !ok {
				t.Errorf("%q: the prompt parser stopped accepting leading whitespace", lead+line)
			}
		}
		for _, tc := range []struct {
			line  string
			parse func(string) bool
		}{
			{"finding class=secret-missing target=session-secret severity=warn", func(l string) bool { _, ok := ParseFinding(l); return ok }},
			{"diagnosis result=healthy checked=13 skipped=0", func(l string) bool { _, ok := ParseDiagnosis(l); return ok }},
			{"dangerous result=empty done=0 failed=0 reason=none", func(l string) bool { _, ok := ParseDangerous(l); return ok }},
		} {
			if !tc.parse(lead + tc.line) {
				t.Errorf("%q: the repair parser stopped accepting leading whitespace", lead+tc.line)
			}
		}
	}
}

// TestCountFieldsShareOneRule pins EN-16's observable rule: every repair
// count reads a malformed or negative value as 0, the same way in all
// eight places.
func TestCountFieldsShareOneRule(t *testing.T) {
	counts := []struct {
		name  string
		line  func(v string) string
		value func(line string) (int, bool)
	}{
		{"diagnosis checked", func(v string) string { return "diagnosis result=healthy checked=" + v + " skipped=0" },
			func(l string) (int, bool) { d, ok := ParseDiagnosis(l); return d.Checked, ok }},
		{"diagnosis skipped", func(v string) string { return "diagnosis result=healthy checked=0 skipped=" + v },
			func(l string) (int, bool) { d, ok := ParseDiagnosis(l); return d.Skipped, ok }},
		{"plan actions", func(v string) string { return "plan result=ready actions=" + v + " manual=0" },
			func(l string) (int, bool) { s, ok := ParsePlanSummary(l); return s.Actions, ok }},
		{"plan manual", func(v string) string { return "plan result=ready actions=0 manual=" + v },
			func(l string) (int, bool) { s, ok := ParsePlanSummary(l); return s.Manual, ok }},
		{"execution done", func(v string) string { return "execution result=complete done=" + v + " failed=0" },
			func(l string) (int, bool) { s, ok := ParseExecutionSummary(l); return s.Done, ok }},
		{"execution failed", func(v string) string { return "execution result=complete done=0 failed=" + v },
			func(l string) (int, bool) { s, ok := ParseExecutionSummary(l); return s.Failed, ok }},
		{"dangerous done", func(v string) string { return "dangerous result=complete done=" + v + " failed=0 reason=none" },
			func(l string) (int, bool) { d, ok := ParseDangerous(l); return d.Done, ok }},
		{"dangerous failed", func(v string) string { return "dangerous result=complete done=0 failed=" + v + " reason=none" },
			func(l string) (int, bool) { d, ok := ParseDangerous(l); return d.Failed, ok }},
	}
	for _, tc := range []struct {
		raw  string
		want int // -1: every count must agree, value unpinned
	}{
		{"7", 7},
		{"0", 0},
		{"-3", 0},
		{"many", 0},
		{"1.5", 0},
		{"4x", 0},
		{"99999999999999999999", -1},
	} {
		var first *int
		for _, c := range counts {
			line := c.line(tc.raw)
			got, ok := c.value(line)
			if !ok {
				t.Errorf("%s: %q did not parse", c.name, line)
				continue
			}
			if tc.want >= 0 && got != tc.want {
				t.Errorf("%s: %s=%q read as %d, want %d", c.name, c.name, tc.raw, got, tc.want)
			}
			if first == nil {
				first = &got
			} else if got != *first {
				t.Errorf("%s: %q read as %d, but %s read it as %d", c.name, tc.raw, got, counts[0].name, *first)
			}
		}
	}
}

// TestPromptAttemptKeepsFloorOfOne pins EN-16's deliberate exception:
// a prompt's attempt is 1-based, so anything below 1 reads as 1.
func TestPromptAttemptKeepsFloorOfOne(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"2", 2},
		{"1", 1},
		{"0", 1},
		{"-2", 1},
		{"soon", 1},
	} {
		line := "prompt field=APP_URL kind=url required=true attempt=" + tc.raw
		msg, ok := ParsePromptLine(line)
		if !ok {
			t.Errorf("%q did not parse", line)
			continue
		}
		p, isPrompt := msg.(Prompt)
		if !isPrompt {
			t.Errorf("%q parsed as %T, want Prompt", line, msg)
			continue
		}
		if p.Attempt != tc.want {
			t.Errorf("attempt=%s read as %d, want %d", tc.raw, p.Attempt, tc.want)
		}
	}
}
