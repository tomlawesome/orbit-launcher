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
// Licence and source URL come from the GitHub API and so are fetched only when
// writing. Checking is deliberately offline: a gate that needs the network to
// say yes is a gate that fails for reasons unrelated to the thing it guards.
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
// stale while nothing was wrong. Currency is driven by Dependabot opening a
// bump, and by the commit check below refusing a bump that leaves this file
// behind.
const SchemaVersion = 2

const (
	PolicyPath   = ".github/supply-chain-policy.json"
	workflowsDir = ".github/workflows"
	secretScan   = ".github/workflows/secret-scan.yml"
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
		return regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(\S+)\s*$`)
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

// GitleaksPin reads the version and digest that secret-scan.yml actually pins.
func GitleaksPin(root string) (version, sha256 string, err error) {
	body, err := os.ReadFile(filepath.Join(root, secretScan))
	if err != nil {
		return "", "", fmt.Errorf("reading secret-scan.yml: %w", err)
	}
	get := func(k string) string {
		if m := envValue(k).FindStringSubmatch(string(body)); m != nil {
			return m[1]
		}
		return ""
	}
	version, sha256 = get("GITLEAKS_VERSION"), get("GITLEAKS_SHA256")
	if version == "" || sha256 == "" {
		return "", "", fmt.Errorf("secret-scan.yml no longer sets GITLEAKS_VERSION and GITLEAKS_SHA256")
	}
	return version, sha256, nil
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
		return p, fmt.Errorf("policy schemaVersion is %d, this tool understands %d", p.SchemaVersion, SchemaVersion)
	}
	return p, nil
}

// DerivedActions returns the mechanical fields for every distinct action the
// workflows pin, sorted by name. Human-authored fields are left empty; Merge
// carries those across.
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

// Marshal renders a policy exactly as it is written to disk, so a comparison
// against the committed bytes is meaningful.
func Marshal(p Policy) ([]byte, error) {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
