// Package policy derives .github/supply-chain-policy.json from the
// workflows, so the mechanical half of that file is generated rather than
// typed.
//
// The split matters. Everything derivable from the repository — which action,
// which SHA, which version the comment claims — is produced here and can never
// drift, because drift is a mismatch between two things one program writes.
// Only the fields nobody can derive (an update owner, an explanatory note) are
// human-authored, and those are carried across a regeneration untouched.
//
// The licence comes from the GitHub API and so is fetched only when writing
// (the source URL is built from the action's name and version). Checking is
// deliberately offline: a gate that needs the network to say yes is a gate
// that fails for reasons unrelated to the thing it guards.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SchemaVersion is bumped when the file's shape changes. Version 2 dropped
// reviewedOn/reviewBy: a date recording "when a human last looked" could only
// be moved by hand, and for an action that simply has not released, it went
// stale while nothing was wrong. Currency is driven by Renovate opening a
// bump, and by VerifyPins failing a bump that leaves this file behind.
const SchemaVersion = 2

const (
	PolicyPath   = ".github/supply-chain-policy.json"
	workflowsDir = ".github/workflows"
	secretScan   = ".github/workflows/secret-scan.yml"
	gitlabCI     = ".gitlab-ci.yml"
)

// Action is one third-party action pinned by the workflows.
type Action struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	License     string `json:"license"`
	Source      string `json:"source"`
	UpdateOwner string `json:"updateOwner"`
	Note        string `json:"note,omitempty"`
}

// Tool is a pinned dependency that is not an action: fetched and
// checksum-verified inside a job, so its version lives in an env block where
// no `uses:` check would ever see it.
type Tool struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Artifact    string `json:"artifact,omitempty"`
	SHA256      string `json:"sha256"`
	License     string `json:"license"`
	Source      string `json:"source"`
	UsedBy      string `json:"usedBy"`
	UpdateOwner string `json:"updateOwner"`
	Note        string `json:"note,omitempty"`
}

// Exception records a pin deliberately left out of the checks above.
type Exception struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type Policy struct {
	SchemaVersion int         `json:"schemaVersion"`
	Comment       []string    `json:"_comment"`
	Actions       []Action    `json:"actions"`
	Tools         []Tool      `json:"tools"`
	Exceptions    []Exception `json:"exceptions"`
}

// Pin is one `uses:` occurrence in one workflow file.
type Pin struct {
	Action  string // owner/repo, subpath stripped
	Full    string // owner/repo[/sub] as written
	Ref     string
	Comment string
	File    string
	Line    int
}

var (
	// A step may write `uses:` on its own line or inline after the list dash.
	// Both are valid YAML and GitHub accepts both, so a parser that only knows
	// the first would let an unpinned action through in the second form.
	usesLine = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s+(\S+?)@(\S+)(?:\s+#\s*(\S+))?`)
	sha40    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	envValue = func(key string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:[ \t]*(\S.*?)\s*$`)
	}
	// A pipeline variable in .gitlab-ci.yml at any depth: the top-level pin
	// and any job's own `variables:` copy alike. A job's override is what
	// that job runs, so gitleaksPinIn requires every copy to agree rather
	// than reading the top-level line alone (#216).
	gitlabVariable = func(key string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:[ \t]*(\S.*?)\s*$`)
	}
)

// IsSHA reports whether ref is a 40-character commit SHA.
func IsSHA(ref string) bool { return sha40.MatchString(ref) }

// ActionRepo reduces github/codeql-action/init to github/codeql-action. The
// policy records the repository somebody reviews, not each entry point.
func ActionRepo(full string) string {
	parts := strings.Split(full, "/")
	if len(parts) <= 2 {
		return full
	}
	return strings.Join(parts[:2], "/")
}

// CollectPins reads every workflow under root and returns each third-party
// `uses:` occurrence. Local composite actions (./path) are not third-party and
// are skipped.
func CollectPins(root string) ([]Pin, error) {
	dir := filepath.Join(root, workflowsDir)
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var pins []Pin
	var workflows int
	for _, f := range files {
		if f.IsDir() || (!strings.HasSuffix(f.Name(), ".yml") && !strings.HasSuffix(f.Name(), ".yaml")) {
			continue
		}
		workflows++
		body, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.Name(), err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			m := usesLine.FindStringSubmatch(line)
			if m == nil || strings.HasPrefix(m[1], "./") {
				continue
			}
			pins = append(pins, Pin{
				Action: ActionRepo(m[1]), Full: m[1], Ref: m[2],
				Comment: m[3], File: f.Name(), Line: i + 1,
			})
		}
	}
	if workflows == 0 {
		return nil, fmt.Errorf("no workflow files under %s: a check over nothing would pass vacuously", dir)
	}
	if len(pins) == 0 {
		return nil, fmt.Errorf("no `uses:` lines across %d workflows: a check over nothing would pass vacuously", workflows)
	}
	return pins, nil
}

// GitleaksPin reads the version and digest the two hosts pin (#216, SU-8):
// GitHub's secret-scan.yml and GitLab's .gitlab-ci.yml each type the pin, and
// they quote it differently. Both are read, quote- and comment-tolerantly, and
// a pin that is missing on either host or differs between them is an error:
// reading one file alone would let the other drift unseen.
func GitleaksPin(root string) (version, sha256 string, err error) {
	ghVersion, ghSum, err := gitleaksPinIn(root, secretScan, envValue)
	if err != nil {
		return "", "", err
	}
	glVersion, glSum, err := gitleaksPinIn(root, gitlabCI, gitlabVariable)
	if err != nil {
		return "", "", err
	}
	if ghVersion != glVersion || ghSum != glSum {
		return "", "", fmt.Errorf("the hosts pin gitleaks differently: secret-scan.yml %s / %s, .gitlab-ci.yml %s / %s",
			ghVersion, ghSum, glVersion, glSum)
	}
	return ghVersion, ghSum, nil
}

// gitleaksPinIn reads GITLEAKS_VERSION and GITLEAKS_SHA256 from one file.
// Every line setting a key must agree, so a second copy cannot hide.
func gitleaksPinIn(root, rel string, line func(string) *regexp.Regexp) (version, sha256 string, err error) {
	name := filepath.Base(rel)
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", name, err)
	}
	get := func(k string) (string, error) {
		v := ""
		for _, m := range line(k).FindAllStringSubmatch(string(body), -1) {
			got := pinValue(m[1])
			if v != "" && got != v {
				return "", fmt.Errorf("%s sets %s twice, to %s and %s", name, k, v, got)
			}
			v = got
		}
		return v, nil
	}
	if version, err = get("GITLEAKS_VERSION"); err != nil {
		return "", "", err
	}
	if sha256, err = get("GITLEAKS_SHA256"); err != nil {
		return "", "", err
	}
	if version == "" || sha256 == "" {
		return "", "", fmt.Errorf("%s no longer sets GITLEAKS_VERSION and GITLEAKS_SHA256", name)
	}
	return version, sha256, nil
}

// pinValue strips a trailing YAML comment and one pair of matching quotes,
// so `8.30.1`, `"8.30.1"` and `'8.30.1' # note` all read as 8.30.1.
func pinValue(raw string) string {
	v := strings.TrimSpace(raw)
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}
	return v
}

// Load reads the committed policy.
func Load(root string) (Policy, error) {
	var p Policy
	raw, err := os.ReadFile(filepath.Join(root, PolicyPath))
	if err != nil {
		return p, fmt.Errorf("reading the supply-chain policy: %w", err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("supply-chain policy is not valid JSON: %w", err)
	}
	if p.SchemaVersion != SchemaVersion {
		return p, &SchemaError{Got: p.SchemaVersion, Want: SchemaVersion}
	}
	return p, nil
}

// SchemaError is a policy that parsed but was written for another schema.
// It is a type so -write can tell it from an unreadable file: an older
// schema's fields are still there to carry across, a parse failure's are not.
type SchemaError struct{ Got, Want int }

func (e *SchemaError) Error() string {
	return fmt.Sprintf("policy schemaVersion is %d, this tool understands %d", e.Got, e.Want)
}

// DerivedActions returns the mechanical fields for every distinct action the
// workflows pin, sorted by name. Human-authored fields are left empty; the
// -write command (regenerate in tools/supplychainpolicy) carries those across
// from the previous file.
func DerivedActions(pins []Pin) []Action {
	byName := map[string]Action{}
	for _, p := range pins {
		a := byName[p.Action]
		a.Name, a.Commit = p.Action, p.Ref
		if p.Comment != "" {
			a.Version = p.Comment
		}
		byName[p.Action] = a
	}
	out := make([]Action, 0, len(byName))
	for _, a := range byName {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WithoutExceptions drops the actions pol.Exceptions names: an excepted
// action is deliberately not recorded, so neither the generator nor the
// currency gate expects it in Actions.
func WithoutExceptions(actions []Action, exceptions []Exception) []Action {
	excepted := map[string]bool{}
	for _, e := range exceptions {
		excepted[e.Name] = true
	}
	out := make([]Action, 0, len(actions))
	for _, a := range actions {
		if !excepted[a.Name] {
			out = append(out, a)
		}
	}
	return out
}

// Marshal renders a policy exactly as it is written to disk, so a comparison
// against the committed bytes is meaningful.
func Marshal(p Policy) ([]byte, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Problem is one disagreement between what the workflows pin and what the
// policy records. Where it can be attributed to a line, it is.
type Problem struct {
	File string
	Line int
	Msg  string
	// Conflict marks two workflows pinning one action at different commits,
	// which regenerating the policy cannot clear.
	Conflict bool
}

func (p Problem) String() string {
	if p.File == "" {
		return p.Msg
	}
	return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Msg)
}

// VerifyPins reports every disagreement between a set of pins and a policy.
//
// It takes its inputs rather than reading them so the rules can be tested
// against constructed cases. A rule proved only by breaking the real
// repository is proved once, by hand, and never again.
func VerifyPins(pins []Pin, pol Policy) []Problem {
	recorded := map[string]Action{}
	for _, a := range pol.Actions {
		recorded[a.Name] = a
	}
	excepted := map[string]bool{}
	for _, e := range pol.Exceptions {
		excepted[e.Name] = true
	}

	var problems []Problem
	add := func(f string, l int, format string, args ...any) {
		problems = append(problems, Problem{File: f, Line: l, Msg: fmt.Sprintf(format, args...)})
	}

	// Two workflows pinning one action at different commits is a fault in the
	// workflows: no policy entry can match both, and deriving the policy keeps
	// only the last pin, so "regenerate" would hide one of them. Name both
	// places and leave the policy comparison for the conflicted action until
	// the workflows agree.
	firstPin := map[string]Pin{}
	conflicted := map[string]bool{}
	for _, p := range pins {
		if !IsSHA(p.Ref) {
			continue
		}
		first, seen := firstPin[p.Action]
		if !seen {
			firstPin[p.Action] = p
		} else if first.Ref != p.Ref {
			conflicted[p.Action] = true
			add(p.File, p.Line, "%s is pinned to %s here but to %s at %s:%d. "+
				"Workflows must pin one action to one commit; change one of the two to match the other.",
				p.Action, p.Ref, first.Ref, first.File, first.Line)
			problems[len(problems)-1].Conflict = true
		}
	}

	used := map[string]bool{}
	for _, p := range pins {
		used[p.Action] = true

		if !IsSHA(p.Ref) {
			add(p.File, p.Line, "%s is pinned to %q, which is not a 40-character commit SHA. "+
				"A tag can be moved by whoever owns it; a SHA cannot.", p.Full, p.Ref)
			continue
		}
		if p.Comment == "" {
			add(p.File, p.Line, "%s has no `# vX.Y.Z` comment beside its SHA, so a reviewer "+
				"cannot tell what the hash is without resolving it by hand.", p.Full)
		}
		if excepted[p.Action] {
			continue
		}
		a, ok := recorded[p.Action]
		if !ok {
			add(p.File, p.Line, "%s is used in CI but not recorded in the policy.", p.Action)
			continue
		}
		if a.Commit != p.Ref && !conflicted[p.Action] {
			add(p.File, p.Line, "%s is pinned to %s but the policy records %s. "+
				"One of the two is stale; the workflow is what actually runs.", p.Action, p.Ref, a.Commit)
		}
		if p.Comment != "" && a.Version != p.Comment {
			add(p.File, p.Line, "%s says %q beside the pin but the policy records version %q. "+
				"A comment that disagrees with the record is worse than no comment.",
				p.Action, p.Comment, a.Version)
		}
	}

	for _, a := range pol.Actions {
		if !used[a.Name] {
			add("", 0, "the policy records %s, which no workflow uses. "+
				"Stale entries make the file look more thorough than it is.", a.Name)
		}
		if a.License == "" {
			add("", 0, "action %s records no licence.", a.Name)
		}
		if a.UpdateOwner == "" {
			add("", 0, "action %s records no update owner; an unowned pin is nobody's job.", a.Name)
		}
	}
	for _, t := range pol.Tools {
		if t.License == "" {
			add("", 0, "tool %s records no licence.", t.Name)
		}
		if t.UpdateOwner == "" {
			add("", 0, "tool %s records no update owner.", t.Name)
		}
	}
	for _, e := range pol.Exceptions {
		if strings.TrimSpace(e.Reason) == "" {
			add("", 0, "exception %s records no reason; an unexplained exception is just an absence.", e.Name)
		}
	}
	return problems
}

// Verify runs every offline check against a repository: the pin rules above,
// plus the pinned tools, whose versions live in a workflow env block where no
// `uses:` rule would ever see them drift.
//
// The error return is for reading the repository; a policy that simply
// disagrees with the workflows comes back as problems, not as an error.
func Verify(root string) ([]Problem, error) {
	pins, err := CollectPins(root)
	if err != nil {
		return nil, err
	}
	pol, err := Load(root)
	if err != nil {
		return nil, err
	}
	problems := VerifyPins(pins, pol)

	version, sha256, err := GitleaksPin(root)
	if err != nil {
		return nil, err
	}
	var gl *Tool
	for i := range pol.Tools {
		if pol.Tools[i].Name == "gitleaks" {
			gl = &pol.Tools[i]
		}
	}
	if gl == nil {
		problems = append(problems, Problem{Msg: "the policy records no gitleaks entry, but secret-scan.yml pins one"})
		return problems, nil
	}
	if gl.Version != version {
		problems = append(problems, Problem{Msg: fmt.Sprintf(
			"secret-scan.yml pins gitleaks %s but the policy records %s.", version, gl.Version)})
	}
	if gl.SHA256 != sha256 {
		problems = append(problems, Problem{Msg: fmt.Sprintf(
			"secret-scan.yml pins gitleaks digest %s but the policy records %s.", sha256, gl.SHA256)})
	}
	return problems, nil
}
