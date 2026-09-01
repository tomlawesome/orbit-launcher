// Package supplychain checks that .github/supply-chain-policy.json actually
// describes the pins the workflows use.
//
// A commit SHA proves a reference cannot move. It says nothing about whether
// anyone read what that commit contains, or when. So a pin reviewed yesterday,
// one reviewed two years ago, and one nobody has ever opened all look
// identical. The policy file records the review; this test stops the record
// and the workflows drifting apart, which is the only thing that keeps the
// record worth reading.
package supplychain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	policyPath    = "../../.github/supply-chain-policy.json"
	workflowsDir  = "../../.github/workflows"
	secretScanYML = "../../.github/workflows/secret-scan.yml"
	dateLayout    = "2006-01-02"
)

type entry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	License     string `json:"license"`
	Source      string `json:"source"`
	UpdateOwner string `json:"updateOwner"`
	ReviewedOn  string `json:"reviewedOn"`
	ReviewBy    string `json:"reviewBy"`
}

type tool struct {
	entry
	SHA256 string `json:"sha256"`
}

type exception struct {
	Name     string `json:"name"`
	Reason   string `json:"reason"`
	ReviewBy string `json:"reviewBy"`
}

type policy struct {
	SchemaVersion int         `json:"schemaVersion"`
	Actions       []entry     `json:"actions"`
	Tools         []tool      `json:"tools"`
	Exceptions    []exception `json:"exceptions"`
}

// usesLine matches `uses: owner/repo[/sub]@ref` with the version comment that
// must sit beside it.
var usesLine = regexp.MustCompile(`^\s*uses:\s+(\S+?)@(\S+)(?:\s+#\s*(\S+))?`)

var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// pin is one `uses:` occurrence in one workflow.
type pin struct {
	action  string // owner/repo, subpath stripped
	full    string // owner/repo[/sub]
	ref     string
	comment string
	file    string
	line    int
}

func loadPolicy(t *testing.T) policy {
	t.Helper()
	raw, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("cannot read the supply-chain policy: %v", err)
	}
	var p policy
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("supply-chain policy is not valid JSON: %v", err)
	}
	if p.SchemaVersion != 1 {
		t.Fatalf("unknown policy schemaVersion %d; this test understands 1", p.SchemaVersion)
	}
	return p
}

// actionRepo reduces `github/codeql-action/init` to `github/codeql-action`,
// because the policy records the repository that gets reviewed, not each
// subpath entry point.
func actionRepo(full string) string {
	parts := strings.Split(full, "/")
	if len(parts) <= 2 {
		return full
	}
	return strings.Join(parts[:2], "/")
}

func collectPins(t *testing.T) []pin {
	t.Helper()
	files, err := os.ReadDir(workflowsDir)
	if err != nil {
		t.Fatalf("cannot read workflows directory: %v", err)
	}
	var pins []pin
	var seen int
	for _, f := range files {
		if f.IsDir() || (!strings.HasSuffix(f.Name(), ".yml") && !strings.HasSuffix(f.Name(), ".yaml")) {
			continue
		}
		seen++
		body, err := os.ReadFile(filepath.Join(workflowsDir, f.Name()))
		if err != nil {
			t.Fatalf("cannot read %s: %v", f.Name(), err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			m := usesLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// Local composite actions (./path) are not third-party pins.
			if strings.HasPrefix(m[1], "./") {
				continue
			}
			pins = append(pins, pin{
				action: actionRepo(m[1]), full: m[1], ref: m[2],
				comment: m[3], file: f.Name(), line: i + 1,
			})
		}
	}
	if seen == 0 {
		t.Fatal("found no workflow files; this test would pass vacuously")
	}
	if len(pins) == 0 {
		t.Fatal("found no `uses:` lines across the workflows; this test would pass vacuously")
	}
	return pins
}

func TestEveryActionIsPinnedToACommitSHA(t *testing.T) {
	for _, p := range collectPins(t) {
		if !sha40.MatchString(p.ref) {
			t.Errorf("%s:%d: %s is pinned to %q, which is not a 40-character commit SHA.\n"+
				"A tag can be moved by whoever owns it; a SHA cannot.",
				p.file, p.line, p.full, p.ref)
		}
	}
}

func TestEveryPinCarriesItsVersionComment(t *testing.T) {
	for _, p := range collectPins(t) {
		if p.comment == "" {
			t.Errorf("%s:%d: %s has no `# vX.Y.Z` comment beside its SHA.\n"+
				"Without it a reviewer cannot tell what the hash is without resolving it by hand.",
				p.file, p.line, p.full)
		}
	}
}

func TestEveryPinIsRecordedInThePolicy(t *testing.T) {
	pol := loadPolicy(t)
	recorded := map[string]entry{}
	for _, e := range pol.Actions {
		recorded[e.Name] = e
	}
	excepted := map[string]bool{}
	for _, e := range pol.Exceptions {
		excepted[e.Name] = true
	}

	for _, p := range collectPins(t) {
		if excepted[p.action] {
			continue
		}
		e, ok := recorded[p.action]
		if !ok {
			t.Errorf("%s:%d: %s is used in CI but not recorded in the supply-chain policy.\n"+
				"Add an entry with its version, licence and review dates, or record a deliberate exception.",
				p.file, p.line, p.action)
			continue
		}
		if e.Commit != p.ref {
			t.Errorf("%s:%d: %s is pinned to %s but the policy records %s.\n"+
				"One of the two is stale; the workflow is what actually runs.",
				p.file, p.line, p.action, p.ref, e.Commit)
		}
		if p.comment != "" && e.Version != p.comment {
			t.Errorf("%s:%d: %s says %q beside the pin but the policy records version %q.\n"+
				"A comment that disagrees with the record is worse than no comment.",
				p.file, p.line, p.action, p.comment, e.Version)
		}
	}
}

func TestPolicyRecordsNothingTheWorkflowsNoLongerUse(t *testing.T) {
	pol := loadPolicy(t)
	used := map[string]bool{}
	for _, p := range collectPins(t) {
		used[p.action] = true
	}
	var orphans []string
	for _, e := range pol.Actions {
		if !used[e.Name] {
			orphans = append(orphans, e.Name)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("the policy records actions no workflow uses: %s.\n"+
			"Stale entries make the file look more thorough than it is; remove them.",
			strings.Join(orphans, ", "))
	}
}

func TestEveryEntryIsCompleteAndReviewed(t *testing.T) {
	pol := loadPolicy(t)
	now := time.Now().UTC()

	check := func(kind, name, license, owner, reviewedOn, reviewBy string) {
		if license == "" {
			t.Errorf("%s %s records no licence.", kind, name)
		}
		if owner == "" {
			t.Errorf("%s %s records no update owner; an unowned pin is nobody's job.", kind, name)
		}
		on, err := time.Parse(dateLayout, reviewedOn)
		if err != nil {
			t.Errorf("%s %s has an unparseable reviewedOn %q (want YYYY-MM-DD).", kind, name, reviewedOn)
			return
		}
		by, err := time.Parse(dateLayout, reviewBy)
		if err != nil {
			t.Errorf("%s %s has an unparseable reviewBy %q (want YYYY-MM-DD).", kind, name, reviewBy)
			return
		}
		if !by.After(on) {
			t.Errorf("%s %s: reviewBy %s is not after reviewedOn %s.", kind, name, reviewBy, reviewedOn)
		}
		if by.Before(now) {
			t.Errorf("%s %s: its review lapsed on %s.\n"+
				"Check the pin is still the version you want, then move reviewedOn and reviewBy.\n"+
				"This is the whole point of the file: a pin nobody has looked at should stop looking "+
				"identical to one reviewed yesterday.", kind, name, reviewBy)
		}
	}

	for _, e := range pol.Actions {
		check("action", e.Name, e.License, e.UpdateOwner, e.ReviewedOn, e.ReviewBy)
	}
	for _, tl := range pol.Tools {
		check("tool", tl.Name, tl.License, tl.UpdateOwner, tl.ReviewedOn, tl.ReviewBy)
	}
	for _, ex := range pol.Exceptions {
		if strings.TrimSpace(ex.Reason) == "" {
			t.Errorf("exception %s records no reason; an unexplained exception is just an absence.", ex.Name)
		}
		if _, err := time.Parse(dateLayout, ex.ReviewBy); err != nil {
			t.Errorf("exception %s has an unparseable reviewBy %q (want YYYY-MM-DD).", ex.Name, ex.ReviewBy)
		}
	}
}

// TestPinnedToolMatchesTheWorkflow covers the one supply-chain pin that is not
// an action: gitleaks is downloaded and checksum-verified at job time, so the
// version and digest live in the workflow's env block rather than a `uses:`
// line, and nothing else would notice them drifting from the policy.
func TestPinnedToolMatchesTheWorkflow(t *testing.T) {
	pol := loadPolicy(t)
	var gl *tool
	for i := range pol.Tools {
		if pol.Tools[i].Name == "gitleaks" {
			gl = &pol.Tools[i]
		}
	}
	if gl == nil {
		t.Fatal("the policy records no gitleaks entry, but secret-scan.yml pins one")
	}

	body, err := os.ReadFile(secretScanYML)
	if err != nil {
		t.Fatalf("cannot read secret-scan.yml: %v", err)
	}
	find := func(key string) string {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(\S+)\s*$`)
		if m := re.FindStringSubmatch(string(body)); m != nil {
			return m[1]
		}
		return ""
	}

	for _, c := range []struct{ key, want, got string }{
		{"GITLEAKS_VERSION", gl.Version, find("GITLEAKS_VERSION")},
		{"GITLEAKS_SHA256", gl.SHA256, find("GITLEAKS_SHA256")},
	} {
		if c.got == "" {
			t.Errorf("secret-scan.yml no longer sets %s; this check cannot see what is pinned.", c.key)
			continue
		}
		if c.got != c.want {
			t.Errorf("secret-scan.yml pins %s=%s but the policy records %s.",
				c.key, c.got, c.want)
		}
	}
}
