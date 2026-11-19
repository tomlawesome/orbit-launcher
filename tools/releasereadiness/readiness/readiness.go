// Package readiness is the release-readiness check (#359). STUB: declarations
// only, so the tests compile and fail; the implementation comes later.
package readiness

// Result is one check's outcome.
type Result struct {
	ID      string
	OK      bool
	Message string
}

// Report is every check's result for one target, in the order they ran.
type Report struct {
	Target  string
	Results []Result
}

// Ready reports whether every check passed.
func (r Report) Ready() bool { return false }

// Render is the text the command prints: ok/FAIL per check, then the verdict.
func (r Report) Render() string { return "" }

// Commit is a non-merge commit in target..source and the files it touches.
type Commit struct {
	SHA   string
	Files []string
}

// Signoff is the verdict on a release record's sign-off.
type Signoff struct {
	Signed bool
	Why    string
}

// Lookups is everything the checks read from outside the repository text.
type Lookups interface {
	Tags() ([]string, error)
	RemoteTagExists(tag string) (bool, error)
	SourceFile(path string) (content string, found bool, err error)
	GuideBlob() (string, error)
	RecordSignoff(path string) (Signoff, error)
	ContainsTarget() (bool, error)
	MainTreeEqualsPreview() (bool, error)
	UncoveredCommits() ([]Commit, error)
	AuditIssues() ([]int, error)
	IssueState(n int) (string, error)
	OrbitPin() (string, error)
	OrbitBranchTip() (branch, commit string, err error)
}

// Run runs every check for target ("preview" or "main").
func Run(target, repoDir string, l Lookups) Report { return Report{} }
