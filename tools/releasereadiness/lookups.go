package main

import (
	"time"

	"github.com/tomlawesome/orbit-launcher/tools/releasereadiness/readiness"
)

// lookupConfig configures the real lookups. STUB.
type lookupConfig struct {
	Dir          string        // the repository
	Remote       string        // e.g. origin
	Target       string        // preview or main; its ref is <Remote>/<Target>
	GitBin       string        // default git
	GitTimeout   time.Duration // limit on every git call
	HTTPTimeout  time.Duration // limit on every HTTP call, body included
	MaxBody      int64         // cap on an HTTP body, in bytes
	GitLabAPI    string        // the project's API base, e.g. https://host/api/v4/projects/12
	GitLabToken  string
	OrbitRepoURL string // git URL of the Orbit mirror
	OrbitBranch  string
}

type realLookups struct{}

func newLookups(cfg lookupConfig) *realLookups { return &realLookups{} }

var _ readiness.Lookups = (*realLookups)(nil)

func (*realLookups) Tags() ([]string, error)                 { return nil, nil }
func (*realLookups) RemoteTagExists(string) (bool, error)    { return false, nil }
func (*realLookups) SourceFile(string) (string, bool, error) { return "", false, nil }
func (*realLookups) GuideBlob() (string, error)              { return "", nil }
func (*realLookups) RecordSignoff(string) (readiness.Signoff, error) {
	return readiness.Signoff{}, nil
}
func (*realLookups) ContainsTarget() (bool, error)                 { return false, nil }
func (*realLookups) MainTreeEqualsPreview() (bool, error)          { return false, nil }
func (*realLookups) UncoveredCommits() ([]readiness.Commit, error) { return nil, nil }
func (*realLookups) AuditIssues() ([]int, error)                   { return nil, nil }
func (*realLookups) IssueState(int) (string, error)                { return "", nil }
func (*realLookups) OrbitPin() (string, error)                     { return "", nil }
func (*realLookups) OrbitBranchTip() (string, string, error)       { return "", "", nil }
