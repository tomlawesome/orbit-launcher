// A small reader for the subset of YAML .gitlab-ci.yml uses, with anchors,
// aliases and merge keys resolved the way a YAML loader resolves them.
//
// go.mod's module graph carries no YAML library (gopkg.in/yaml.v3 is neither
// required nor in `go mod graph`), and importing one would add a dependency,
// so ci_boilerplate_test.go reads the pipeline through this instead. It
// supports block mappings and sequences, literal block scalars (|), single-line
// flow sequences, quoted and plain scalars, `&anchor`, `*alias` and `<<`. It
// stops with an error on anything else (folded `>` blocks, flow mappings,
// multi-line plain scalars), so a construct it cannot read never passes
// silently.
package supplychain

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

type yamlNode struct {
	kind  byte // 'm' mapping, 'q' sequence, 's' scalar
	keys  []string
	vals  map[string]*yamlNode
	items []*yamlNode
	str   string
}

type yamlAnchor struct {
	node   *yamlNode
	usedBy map[string]bool // top-level keys that define or alias it
}

type yamlDoc struct {
	root    *yamlNode
	anchors map[string]*yamlAnchor
}

type yamlParser struct {
	lines   []string
	pos     int
	top     string // the top-level key being parsed
	anchors map[string]*yamlAnchor
}

func parseMiniYAML(src string) (doc *yamlDoc, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	p := &yamlParser{lines: strings.Split(src, "\n"), anchors: map[string]*yamlAnchor{}}
	p.skipBlank()
	root := p.parseNode(0)
	p.skipBlank()
	if p.pos < len(p.lines) {
		panic(fmt.Sprintf("line %d: unexpected %q", p.pos+1, p.lines[p.pos]))
	}
	return &yamlDoc{root: root, anchors: p.anchors}, nil
}

func yamlIndent(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

func yamlSkippable(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#")
}

func (p *yamlParser) skipBlank() {
	for p.pos < len(p.lines) && yamlSkippable(p.lines[p.pos]) {
		p.pos++
	}
}

func isSeqItem(t string) bool { return t == "-" || strings.HasPrefix(t, "- ") }

// parseNode parses the block that starts at the current line, whose indent is
// at least min.
func (p *yamlParser) parseNode(min int) *yamlNode {
	p.skipBlank()
	if p.pos >= len(p.lines) {
		return &yamlNode{kind: 's'}
	}
	l := p.lines[p.pos]
	ind := yamlIndent(l)
	if ind < min {
		return &yamlNode{kind: 's'}
	}
	if isSeqItem(strings.TrimSpace(l)) {
		return p.parseSeq(ind)
	}
	if _, _, ok := splitKey(strings.TrimSpace(l)); ok {
		return p.parseMap(ind)
	}
	panic(fmt.Sprintf("line %d: cannot read %q", p.pos+1, l))
}

// splitKey splits "key: value" at the first colon followed by a space or the
// end of the text.
func splitKey(t string) (key, rest string, ok bool) {
	if strings.HasPrefix(t, "-") && isSeqItem(t) {
		return "", "", false
	}
	for i := 0; i < len(t); i++ {
		if t[i] == ':' && (i+1 == len(t) || t[i+1] == ' ') {
			return strings.Trim(t[:i], `"'`), strings.TrimSpace(t[i+1:]), true
		}
	}
	return "", "", false
}

func (p *yamlParser) parseSeq(ind int) *yamlNode {
	n := &yamlNode{kind: 'q'}
	for {
		p.skipBlank()
		if p.pos >= len(p.lines) {
			break
		}
		l := p.lines[p.pos]
		t := strings.TrimSpace(l)
		if yamlIndent(l) != ind || !isSeqItem(t) {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "-"))
		if rest == "" {
			p.pos++
			n.items = append(n.items, p.parseNode(ind+1))
			continue
		}
		offset := len(l) - len(strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(l, " "), "-"), " "))
		if _, _, isKey := splitKey(rest); isKey && !strings.HasPrefix(rest, "&") && !strings.HasPrefix(rest, "*") &&
			!strings.HasPrefix(rest, `"`) && !strings.HasPrefix(rest, "'") && !strings.HasPrefix(rest, "[") {
			// "- key: value": the mapping's indent is where the key starts.
			p.lines[p.pos] = strings.Repeat(" ", offset) + rest
			n.items = append(n.items, p.parseMap(offset))
			continue
		}
		p.pos++
		n.items = append(n.items, p.parseValue(rest, ind))
	}
	return n
}

func (p *yamlParser) parseMap(ind int) *yamlNode {
	n := &yamlNode{kind: 'm', vals: map[string]*yamlNode{}}
	var merges []*yamlNode
	for {
		p.skipBlank()
		if p.pos >= len(p.lines) {
			break
		}
		l := p.lines[p.pos]
		t := strings.TrimSpace(l)
		if yamlIndent(l) != ind || isSeqItem(t) {
			break
		}
		key, rest, ok := splitKey(t)
		if !ok {
			panic(fmt.Sprintf("line %d: expected a key, got %q", p.pos+1, l))
		}
		if ind == 0 {
			p.top = key
		}
		p.pos++
		var v *yamlNode
		if rest == "" {
			p.skipBlank()
			switch {
			case p.pos < len(p.lines) && yamlIndent(p.lines[p.pos]) > ind:
				v = p.parseNode(ind + 1)
			case p.pos < len(p.lines) && yamlIndent(p.lines[p.pos]) == ind && isSeqItem(strings.TrimSpace(p.lines[p.pos])):
				v = p.parseSeq(ind) // a sequence at its key's own indent
			default:
				v = &yamlNode{kind: 's'}
			}
		} else {
			v = p.parseValue(rest, ind)
		}
		if key == "<<" {
			merges = append(merges, v)
			continue
		}
		if _, dup := n.vals[key]; dup {
			panic(fmt.Sprintf("line %d: duplicate key %q", p.pos, key))
		}
		n.keys = append(n.keys, key)
		n.vals[key] = v
	}
	// Merge keys fill in only the keys the mapping does not set itself.
	for _, m := range merges {
		srcs := []*yamlNode{m}
		if m.kind == 'q' {
			srcs = m.items
		}
		for _, s := range srcs {
			if s.kind != 'm' {
				panic("a merge key must name a mapping")
			}
			for _, k := range s.keys {
				if _, set := n.vals[k]; !set {
					n.keys = append(n.keys, k)
					n.vals[k] = s.vals[k]
				}
			}
		}
	}
	return n
}

// parseValue reads the value text after "key:" or "- ". The line has already
// been consumed; parent is the indent of the line it was on.
func (p *yamlParser) parseValue(text string, parent int) *yamlNode {
	text = strings.TrimSpace(text)
	anchor := ""
	if strings.HasPrefix(text, "&") {
		name, after, _ := strings.Cut(text[1:], " ")
		anchor, text = name, strings.TrimSpace(after)
	}
	var v *yamlNode
	switch {
	case text == "" || strings.HasPrefix(text, "#"):
		p.skipBlank()
		if p.pos < len(p.lines) && yamlIndent(p.lines[p.pos]) > parent {
			v = p.parseNode(parent + 1)
		} else {
			v = &yamlNode{kind: 's'}
		}
	case strings.HasPrefix(text, "*"):
		name := strings.TrimSpace(strings.SplitN(text[1:], " #", 2)[0])
		a, ok := p.anchors[name]
		if !ok {
			panic(fmt.Sprintf("line %d: alias *%s has no anchor before it", p.pos, name))
		}
		a.usedBy[p.top] = true
		v = a.node
	case text[0] == '|':
		v = p.blockScalar(parent)
	case text[0] == '>':
		// Folding joins the lines with spaces, so a script written this way
		// runs as one command. The pipeline has no folded block; reading one
		// as literal would hide exactly that change (#217).
		panic(fmt.Sprintf("line %d: folded block scalars (>) are not supported; use |", p.pos))
	case text[0] == '[':
		v = p.flowSeq(text)
	case text[0] == '{':
		panic(fmt.Sprintf("line %d: flow mappings are not supported", p.pos))
	default:
		v = &yamlNode{kind: 's', str: plainScalar(text)}
	}
	if anchor != "" {
		// The job that defines the anchor uses it too.
		p.anchors[anchor] = &yamlAnchor{node: v, usedBy: map[string]bool{p.top: true}}
	}
	return v
}

func (p *yamlParser) flowSeq(text string) *yamlNode {
	end := strings.LastIndex(text, "]")
	if end < 0 {
		panic(fmt.Sprintf("line %d: multi-line flow sequences are not supported", p.pos))
	}
	n := &yamlNode{kind: 'q'}
	inner := strings.TrimSpace(text[1:end])
	if inner == "" {
		return n
	}
	for _, it := range strings.Split(inner, ",") {
		it = strings.TrimSpace(it)
		if strings.HasPrefix(it, "*") {
			a, ok := p.anchors[it[1:]]
			if !ok {
				panic(fmt.Sprintf("line %d: alias %s has no anchor before it", p.pos, it))
			}
			a.usedBy[p.top] = true
			n.items = append(n.items, a.node)
			continue
		}
		n.items = append(n.items, &yamlNode{kind: 's', str: plainScalar(it)})
	}
	return n
}

// blockScalar reads the indented lines under a "|" header (comment lines
// inside it are content), joined with newlines as YAML keeps them.
func (p *yamlParser) blockScalar(parent int) *yamlNode {
	var body []string
	contentIndent := -1
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if strings.TrimSpace(l) != "" {
			if yamlIndent(l) <= parent {
				break
			}
			if contentIndent < 0 {
				contentIndent = yamlIndent(l)
			}
		}
		p.pos++
		if strings.TrimSpace(l) == "" {
			body = append(body, "")
		} else {
			body = append(body, l[min(contentIndent, len(l)):])
		}
	}
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
	}
	return &yamlNode{kind: 's', str: strings.Join(body, "\n")}
}

func plainScalar(s string) string {
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		q := s[0]
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' && q == '"' {
				i++
				continue
			}
			if s[i] == q {
				return s[1:i]
			}
		}
		return s
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// dump renders a node as one canonical string (mapping keys sorted), so two
// trees compare equal exactly when they hold the same data.
func (n *yamlNode) dump() string {
	switch n.kind {
	case 'm':
		ks := append([]string(nil), n.keys...)
		sort.Strings(ks)
		parts := make([]string, len(ks))
		for i, k := range ks {
			parts[i] = fmt.Sprintf("%q: %s", k, n.vals[k].dump())
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case 'q':
		parts := make([]string, len(n.items))
		for i, it := range n.items {
			parts[i] = it.dump()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprintf("%q", n.str)
}

// flatScripts returns the strings of a script-like node, flattening nested
// sequences the way GitLab does. A mapping is an error, not an empty entry: a
// plain step holding ": ", such as `- curl -H "Accept: json" ...`, reads as a
// one-key mapping, and skipping it would hide that step from every
// comparison (#236). GitLab refuses such a script too; quote the step.
func (n *yamlNode) flatScripts() ([]string, error) {
	if n == nil {
		return nil, nil
	}
	switch n.kind {
	case 's':
		if n.str == "" {
			return nil, nil
		}
		return []string{n.str}, nil
	case 'm':
		return nil, fmt.Errorf("a script entry reads as a mapping (%s): a plain step containing \": \" must be quoted", n.dump())
	}
	var out []string
	for _, it := range n.items {
		s, err := it.flatScripts()
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	return out, nil
}

// find reports whether a mapping key of this name exists anywhere below n.
func (n *yamlNode) find(key string) (*yamlNode, bool) {
	switch n.kind {
	case 'm':
		for _, k := range n.keys {
			if k == key {
				return n.vals[k], true
			}
			if v, ok := n.vals[k].find(key); ok {
				return v, true
			}
		}
	case 'q':
		for _, it := range n.items {
			if v, ok := it.find(key); ok {
				return v, true
			}
		}
	}
	return nil, false
}

// jobEffective is what a job runs and how, once `extends:` (GitLab's deep
// merge: mappings merge, anything else is replaced) and `default:` (a job
// keyword wins outright; `default:` fills in only the keywords the job does
// not set) are applied: the non-blank lines the runner executes, in order,
// and every other key of the job.
type jobEffective struct {
	Script string
	Keys   map[string]*yamlNode // every key but the three script hooks
}

func isScriptHook(k string) bool {
	return k == "before_script" || k == "script" || k == "after_script"
}

func (d *yamlDoc) effectiveJob(name string) (jobEffective, error) {
	job, ok := d.root.vals[name]
	if !ok || job.kind != 'm' {
		return jobEffective{}, fmt.Errorf("no job %q", name)
	}
	merged, err := d.extendsMerge(job, 0)
	if err != nil {
		return jobEffective{}, err
	}
	def := d.root.vals["default"]
	if def != nil && def.kind != 'm' {
		def = nil
	}
	var all []string
	for _, hook := range []string{"before_script", "script", "after_script"} {
		v, set := merged.vals[hook]
		if !set && def != nil && hook != "script" {
			v = def.vals[hook]
		}
		s, err := v.flatScripts()
		if err != nil {
			return jobEffective{}, fmt.Errorf("job %q %s: %w", name, hook, err)
		}
		all = append(all, s...)
	}
	keys := map[string]*yamlNode{}
	for _, k := range merged.keys {
		if !isScriptHook(k) {
			keys[k] = merged.vals[k]
		}
	}
	if def != nil {
		for _, k := range def.keys {
			if _, set := keys[k]; !set && !isScriptHook(k) {
				keys[k] = def.vals[k]
			}
		}
	}
	// Line by line: the runner executes every entry in one shell, so where one
	// entry ends and the next begins carries no meaning, and an anchor may
	// hold a few lines as a block or as separate entries.
	var lines []string
	for _, entry := range all {
		for _, l := range strings.Split(entry, "\n") {
			if l = strings.TrimRight(l, " "); l != "" {
				lines = append(lines, l)
			}
		}
	}
	return jobEffective{Script: strings.Join(lines, "\n"), Keys: keys}, nil
}

func (d *yamlDoc) extendsMerge(job *yamlNode, depth int) (*yamlNode, error) {
	if depth > 10 {
		return nil, fmt.Errorf("extends nests too deep")
	}
	ext, ok := job.vals["extends"]
	if !ok {
		return job, nil
	}
	parents := ext.items
	if ext.kind == 's' {
		parents = []*yamlNode{ext}
	}
	out := &yamlNode{kind: 'm', vals: map[string]*yamlNode{}}
	for _, pn := range parents {
		parent, ok := d.root.vals[pn.str]
		if !ok || parent.kind != 'm' {
			return nil, fmt.Errorf("extends %q: no such template", pn.str)
		}
		pm, err := d.extendsMerge(parent, depth+1)
		if err != nil {
			return nil, err
		}
		out = deepMerge(out, pm)
	}
	return deepMerge(out, job), nil
}

func deepMerge(base, over *yamlNode) *yamlNode {
	out := &yamlNode{kind: 'm', vals: map[string]*yamlNode{}}
	for _, k := range base.keys {
		out.keys = append(out.keys, k)
		out.vals[k] = base.vals[k]
	}
	for _, k := range over.keys {
		ov := over.vals[k]
		if bv, ok := out.vals[k]; ok {
			if bv.kind == 'm' && ov.kind == 'm' {
				out.vals[k] = deepMerge(bv, ov)
			} else {
				out.vals[k] = ov
			}
			continue
		}
		out.keys = append(out.keys, k)
		out.vals[k] = ov
	}
	return out
}

// A folded block (`>`) joins its lines with spaces, so an apt prologue
// written as `>` runs as one command -- `export ... apt-get update -qq` -- and
// apt-get update never runs (#217). The pipeline uses no folded block, so the
// reader refuses one rather than read it as a literal block and pass.
func TestMiniYAMLRefusesFoldedBlocks(t *testing.T) {
	for _, src := range []string{
		".a: &a >\n  export X=1\n  apt-get update -qq\n",
		"job:\n  script:\n    - >-\n      one\n      two\n",
		"job:\n  description: >\n    text\n",
	} {
		if _, err := parseMiniYAML(src); err == nil {
			t.Errorf("parseMiniYAML read a folded block without an error:\n%s", src)
		}
	}
	// The literal form stays readable, lines kept apart.
	doc, err := parseMiniYAML(".a: &a |\n  export X=1\n  apt-get update -qq\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.root.vals[".a"].str; got != "export X=1\napt-get update -qq" {
		t.Errorf("literal block read as %q", got)
	}
}

// A plain script step containing ": " reads as a one-key mapping. The guard
// used to skip it, so such a step could change unnoticed (#236); it must
// stop with an error, while the quoted form stays a readable step.
func TestMiniYAMLRefusesScriptStepThatReadsAsMapping(t *testing.T) {
	doc, err := parseMiniYAML("job:\n  script:\n    - echo one\n    - curl -H \"Accept: json\" https://example.invalid/\n")
	if err != nil {
		t.Fatal(err)
	}
	if e, err := doc.effectiveJob("job"); err == nil {
		t.Errorf("a script step that reads as a mapping was accepted; effective script %q", e.Script)
	}
	doc, err = parseMiniYAML("job:\n  script:\n    - echo one\n    - 'curl -H \"Accept: json\" https://example.invalid/'\n")
	if err != nil {
		t.Fatal(err)
	}
	e, err := doc.effectiveJob("job")
	if err != nil {
		t.Fatalf("quoted step refused: %v", err)
	}
	if want := "echo one\ncurl -H \"Accept: json\" https://example.invalid/"; e.Script != want {
		t.Errorf("quoted step read as %q, want %q", e.Script, want)
	}
}
