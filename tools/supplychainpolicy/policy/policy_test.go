package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorkflows builds a throwaway repository root containing the given
// workflow files, so the parser is tested against directories it really reads
// rather than against strings passed straight to a regexp.
func writeWorkflows(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestActionRepoStripsTheSubpath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"actions/checkout", "actions/checkout"},
		{"github/codeql-action/init", "github/codeql-action"},
		{"github/codeql-action/analyze", "github/codeql-action"},
		{"a/b/c/d", "a/b"},
	} {
		if got := ActionRepo(c.in); got != c.want {
			t.Errorf("ActionRepo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsSHAAcceptsOnlyFullLowercaseHashes(t *testing.T) {
	const good = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	for _, c := range []struct {
		in   string
		want bool
	}{
		{good, true},
		{"v7.0.1", false},
		{good[:39], false},
		{good + "0", false},
		{strings.ToUpper(good), false}, // GitHub writes lowercase; uppercase suggests hand-editing
		{"", false},
	} {
		if got := IsSHA(c.in); got != c.want {
			t.Errorf("IsSHA(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCollectPinsReadsRefAndVersionComment(t *testing.T) {
	root := writeWorkflows(t, map[string]string{"ci.yml": `
jobs:
  a:
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: github/codeql-action/init@cdf488f595d80d6e07e03d4674febd5ab45fa938 # v4.37.9
`})
	pins, err := CollectPins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 {
		t.Fatalf("got %d pins, want 2", len(pins))
	}
	if pins[0].Ref != "3d3c42e5aac5ba805825da76410c181273ba90b1" || pins[0].Comment != "v7.0.1" {
		t.Errorf("first pin parsed as ref=%q comment=%q", pins[0].Ref, pins[0].Comment)
	}
	if pins[1].Action != "github/codeql-action" || pins[1].Full != "github/codeql-action/init" {
		t.Errorf("subpath pin parsed as action=%q full=%q", pins[1].Action, pins[1].Full)
	}
	if pins[0].File != "ci.yml" || pins[0].Line != 5 {
		t.Errorf("location parsed as %s:%d, want ci.yml:5", pins[0].File, pins[0].Line)
	}
}

func TestCollectPinsSkipsLocalCompositeActions(t *testing.T) {
	root := writeWorkflows(t, map[string]string{"ci.yml": `
      - uses: ./.github/actions/setup
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
`})
	pins, err := CollectPins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 || pins[0].Action != "actions/checkout" {
		t.Fatalf("local composite action was not skipped: %+v", pins)
	}
}

// A parser that returns nothing would let every check above pass while proving
// nothing, so finding no workflows or no pins is an error rather than an empty
// result.
func TestCollectPinsRefusesToPassVacuously(t *testing.T) {
	if _, err := CollectPins(writeWorkflows(t, map[string]string{})); err == nil {
		t.Error("an empty workflows directory returned no error")
	}
	root := writeWorkflows(t, map[string]string{"ci.yml": "jobs:\n  a:\n    steps:\n      - run: echo hi\n"})
	if _, err := CollectPins(root); err == nil {
		t.Error("workflows with no `uses:` lines returned no error")
	}
	if _, err := CollectPins(t.TempDir()); err == nil {
		t.Error("a missing workflows directory returned no error")
	}
}

func TestDerivedActionsDeduplicatesSubpathsAndSorts(t *testing.T) {
	root := writeWorkflows(t, map[string]string{"a.yml": `
      - uses: github/codeql-action/init@cdf488f595d80d6e07e03d4674febd5ab45fa938 # v4.37.9
      - uses: github/codeql-action/analyze@cdf488f595d80d6e07e03d4674febd5ab45fa938 # v4.37.9
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
`})
	pins, err := CollectPins(root)
	if err != nil {
		t.Fatal(err)
	}
	got := DerivedActions(pins)
	if len(got) != 2 {
		t.Fatalf("got %d actions, want 2 (codeql's two subpaths are one repository)", len(got))
	}
	if got[0].Name != "actions/checkout" || got[1].Name != "github/codeql-action" {
		t.Errorf("actions are not sorted by name: %q then %q", got[0].Name, got[1].Name)
	}
	if got[1].Version != "v4.37.9" || got[1].Commit != "cdf488f595d80d6e07e03d4674febd5ab45fa938" {
		t.Errorf("codeql entry derived as %+v", got[1])
	}
}

func TestGitleaksPinReadsTheEnvBlock(t *testing.T) {
	const digest = "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb"
	root := writeWorkflows(t, map[string]string{"secret-scan.yml": `
    env:
      GITLEAKS_VERSION: 8.30.1
      GITLEAKS_SHA256: ` + digest + `
`})
	version, sha, err := GitleaksPin(root)
	if err != nil {
		t.Fatal(err)
	}
	if version != "8.30.1" || sha != digest {
		t.Errorf("parsed version=%q sha=%q", version, sha)
	}
}

func TestGitleaksPinFailsWhenTheWorkflowStopsPinning(t *testing.T) {
	root := writeWorkflows(t, map[string]string{"secret-scan.yml": "    env:\n      GITLEAKS_VERSION: 8.30.1\n"})
	if _, _, err := GitleaksPin(root); err == nil {
		t.Error("a missing GITLEAKS_SHA256 returned no error; the digest could vanish unnoticed")
	}
}

// TestCollectPinsReadsTheInlineListForm covers `- uses:` written on the dash
// line. Both forms are valid YAML and GitHub runs both, so a parser that knew
// only the block form would quietly ignore an unpinned action written the
// other way — a hole in exactly the check this package exists to provide.
func TestCollectPinsReadsTheInlineListForm(t *testing.T) {
	root := writeWorkflows(t, map[string]string{"ci.yml": "" +
		"      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n" +
		"      - name: block form\n" +
		"        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0\n"})
	pins, err := CollectPins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 {
		t.Fatalf("got %d pins, want 2 — both the inline and block forms must be seen", len(pins))
	}
	if pins[0].Action != "actions/checkout" || pins[1].Action != "actions/setup-go" {
		t.Errorf("parsed %q and %q", pins[0].Action, pins[1].Action)
	}
}

const (
	checkoutSHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	setupGoSHA  = "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
)

// goodPins and goodPolicy are a matching pair. Each case below breaks exactly
// one thing about them, so a case that fails to produce a problem is a rule
// that does not fire.
func goodPins() []Pin {
	return []Pin{
		{Action: "actions/checkout", Full: "actions/checkout", Ref: checkoutSHA, Comment: "v7.0.1", File: "ci.yml", Line: 10},
		{Action: "actions/setup-go", Full: "actions/setup-go", Ref: setupGoSHA, Comment: "v7.0.0", File: "ci.yml", Line: 14},
	}
}

func goodPolicy() Policy {
	return Policy{
		SchemaVersion: SchemaVersion,
		Actions: []Action{
			{Name: "actions/checkout", Version: "v7.0.1", Commit: checkoutSHA, License: "MIT", UpdateOwner: "maintainers"},
			{Name: "actions/setup-go", Version: "v7.0.0", Commit: setupGoSHA, License: "MIT", UpdateOwner: "maintainers"},
		},
		Exceptions: []Exception{},
	}
}

func TestVerifyPinsAcceptsAMatchingPair(t *testing.T) {
	if problems := VerifyPins(goodPins(), goodPolicy()); len(problems) != 0 {
		t.Fatalf("a matching policy reported problems: %v", problems)
	}
}

// TestVerifyPinsCatches is the standing proof that each rule fires. These were
// originally run by hand against the real repository, which proved them once;
// as cases they are proved on every run.
func TestVerifyPinsCatches(t *testing.T) {
	for _, c := range []struct {
		name    string
		pins    func([]Pin) []Pin
		policy  func(Policy) Policy
		wantSub string
	}{
		{
			name:    "a floating tag instead of a SHA",
			pins:    func(p []Pin) []Pin { p[0].Ref = "v7.0.1"; return p },
			wantSub: "not a 40-character commit SHA",
		},
		{
			name:    "a pin with no version comment",
			pins:    func(p []Pin) []Pin { p[0].Comment = ""; return p },
			wantSub: "no `# vX.Y.Z` comment",
		},
		{
			name:    "an action used in CI but absent from the policy",
			policy:  func(pol Policy) Policy { pol.Actions = pol.Actions[:1]; return pol },
			wantSub: "not recorded in the policy",
		},
		{
			name:    "a bumped pin the policy was not regenerated for",
			pins:    func(p []Pin) []Pin { p[1].Ref = "1111111111111111111111111111111111111111"; return p },
			wantSub: "but the policy records",
		},
		{
			name:    "a bumped version comment the policy was not regenerated for",
			pins:    func(p []Pin) []Pin { p[1].Comment = "v7.1.0"; return p },
			wantSub: "the policy records version",
		},
		{
			name: "a policy entry no workflow uses",
			policy: func(pol Policy) Policy {
				pol.Actions = append(pol.Actions, Action{Name: "actions/stale", License: "MIT", UpdateOwner: "x"})
				return pol
			},
			wantSub: "which no workflow uses",
		},
		{
			name:    "an entry with no licence",
			policy:  func(pol Policy) Policy { pol.Actions[0].License = ""; return pol },
			wantSub: "records no licence",
		},
		{
			name:    "an entry with no update owner",
			policy:  func(pol Policy) Policy { pol.Actions[0].UpdateOwner = ""; return pol },
			wantSub: "records no update owner",
		},
		{
			name: "a tool with no licence",
			policy: func(pol Policy) Policy {
				pol.Tools = append(pol.Tools, Tool{Name: "gitleaks", UpdateOwner: "x"})
				return pol
			},
			wantSub: "tool gitleaks records no licence",
		},
		{
			name: "an exception with no reason",
			policy: func(pol Policy) Policy {
				pol.Exceptions = append(pol.Exceptions, Exception{Name: "actions/checkout", Reason: "  "})
				return pol
			},
			wantSub: "records no reason",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			pins, pol := goodPins(), goodPolicy()
			if c.pins != nil {
				pins = c.pins(pins)
			}
			if c.policy != nil {
				pol = c.policy(pol)
			}
			problems := VerifyPins(pins, pol)
			if len(problems) == 0 {
				t.Fatalf("no problem reported for %s; the rule does not fire", c.name)
			}
			var joined string
			for _, p := range problems {
				joined += p.String() + "\n"
			}
			if !strings.Contains(joined, c.wantSub) {
				t.Errorf("problems did not mention %q:\n%s", c.wantSub, joined)
			}
		})
	}
}

// A recorded exception suppresses the "not recorded" complaint — that is what
// an exception is for — but must not suppress the SHA rule, which is the one
// thing no exception should buy.
func TestExceptionExcusesTheRecordButNotAFloatingTag(t *testing.T) {
	pol := goodPolicy()
	pol.Actions = pol.Actions[:1]
	pol.Exceptions = []Exception{{Name: "actions/setup-go", Reason: "vendored fork, reviewed 2026-09-01"}}

	if problems := VerifyPins(goodPins(), pol); len(problems) != 0 {
		t.Fatalf("an excepted action still reported problems: %v", problems)
	}

	pins := goodPins()
	pins[1].Ref = "v7.0.0"
	problems := VerifyPins(pins, pol)
	if len(problems) == 0 {
		t.Fatal("an exception silenced the commit-SHA rule; no exception should buy that")
	}
}

func TestProblemStringOmitsLocationWhenThereIsNone(t *testing.T) {
	withFile := Problem{File: "ci.yml", Line: 12, Msg: "boom"}
	if got := withFile.String(); got != "ci.yml:12: boom" {
		t.Errorf("String() = %q", got)
	}
	if got := (Problem{Msg: "boom"}).String(); got != "boom" {
		t.Errorf("String() = %q, want no location prefix", got)
	}
}
