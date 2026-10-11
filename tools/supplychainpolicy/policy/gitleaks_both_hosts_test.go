package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// GitleaksPin reads the pin from both hosts (#216, SU-8): GitHub's
// secret-scan.yml and GitLab's .gitlab-ci.yml each type it, they quote it
// differently, and a value that differs on one host must not pass because the
// other is the only one read.

const (
	hostsDigest      = "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb"
	hostsOtherDigest = "0000000000000000000000000000000000000000000000000000000000000000"
)

// hostsRepo writes a GitHub secret-scan.yml and a GitLab pipeline file whose
// pin lines are exactly the strings given (so the quoting is the caller's).
func hostsRepo(t *testing.T, githubVersion, githubSum, gitlabVersion, gitlabSum string) string {
	t.Helper()
	root := writeWorkflows(t, map[string]string{"secret-scan.yml": "" +
		"    env:\n" +
		"      GITLEAKS_VERSION: " + githubVersion + "\n" +
		"      GITLEAKS_SHA256: " + githubSum + "\n"})
	gitlab := "variables:\n" +
		"  GITLEAKS_VERSION: " + gitlabVersion + "\n" +
		"  GITLEAKS_SHA256: " + gitlabSum + "\n"
	if err := os.WriteFile(filepath.Join(root, ".gitlab-ci.yml"), []byte(gitlab), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// Unquoted GitHub beside quoted GitLab is the real repository's shape. Every
// other quoting must give the same clean answer, never a value with quotes in
// it. Cases with a quoted GitHub value fail on the current single-file reader.
func TestGitleaksPinIgnoresQuotingOnEitherHost(t *testing.T) {
	q := func(s string) string { return `"` + s + `"` }
	sq := func(s string) string { return `'` + s + `'` }
	for _, c := range []struct {
		name                                               string
		githubVersion, githubSum, gitlabVersion, gitlabSum string
	}{
		{"github bare, gitlab double-quoted (the real shape)", "8.30.1", hostsDigest, q("8.30.1"), hostsDigest},
		{"github double-quoted, gitlab bare", q("8.30.1"), q(hostsDigest), "8.30.1", hostsDigest},
		{"github single-quoted, gitlab double-quoted", sq("8.30.1"), sq(hostsDigest), q("8.30.1"), q(hostsDigest)},
		{"github bare, gitlab bare", "8.30.1", hostsDigest, "8.30.1", hostsDigest},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := hostsRepo(t, c.githubVersion, c.githubSum, c.gitlabVersion, c.gitlabSum)
			version, sum, err := GitleaksPin(root)
			if err != nil {
				t.Fatalf("GitleaksPin on two agreeing files: %v", err)
			}
			if version != "8.30.1" || sum != hostsDigest {
				t.Errorf("GitleaksPin = %q / %q, want the bare 8.30.1 / %s", version, sum, hostsDigest)
			}
		})
	}
}

// A pin that moved on one host only is exactly the drift SU-8 is about. Each
// refusal is paired with TestGitleaksPinIgnoresQuotingOnEitherHost, which
// shows the same fixture shape is accepted when the hosts agree.
func TestGitleaksPinRefusesHostsThatDisagree(t *testing.T) {
	for _, c := range []struct {
		name                                               string
		githubVersion, githubSum, gitlabVersion, gitlabSum string
	}{
		{"versions differ", "8.30.1", hostsDigest, `"8.31.0"`, hostsDigest},
		{"digests differ", "8.30.1", hostsDigest, `"8.30.1"`, hostsOtherDigest},
		{"versions differ only behind quotes", `"8.30.1"`, hostsDigest, `"8.30.2"`, hostsDigest},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := hostsRepo(t, c.githubVersion, c.githubSum, c.gitlabVersion, c.gitlabSum)
			if version, sum, err := GitleaksPin(root); err == nil {
				t.Errorf("GitleaksPin accepted disagreeing hosts and returned %q / %q", version, sum)
			}
		})
	}
}

// A GitLab file that stops pinning is as bad as a GitHub one that does
// (TestGitleaksPinFailsWhenTheWorkflowStopsPinning covers GitHub's side).
func TestGitleaksPinFailsWhenGitLabStopsPinning(t *testing.T) {
	root := hostsRepo(t, "8.30.1", hostsDigest, `"8.30.1"`, hostsDigest)
	body := "variables:\n  GITLEAKS_VERSION: \"8.30.1\"\n"
	if err := os.WriteFile(filepath.Join(root, ".gitlab-ci.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GitleaksPin(root); err == nil {
		t.Error("a missing GITLEAKS_SHA256 in .gitlab-ci.yml returned no error; the digest could vanish there unnoticed")
	}
}

// A job's own `variables:` beats the pipeline's in GitLab, so a job-level
// GITLEAKS_VERSION is what that job runs. Reading only the top-level line
// would pass a job that runs a different gitleaks (#216): every line that
// sets the key, at any depth, must agree, as on the GitHub side.
func TestGitleaksPinRefusesAJobLevelOverride(t *testing.T) {
	job := func(version string) string {
		return "gitleaks:\n  variables:\n    GITLEAKS_VERSION: " + version + "\n  script:\n    - gitleaks version\n"
	}
	for _, c := range []struct {
		name, override string
		wantErr        bool
	}{
		{"a different version in a job", `"9.9.9"`, true},
		{"the same version in a job", `"8.30.1"`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := hostsRepo(t, "8.30.1", hostsDigest, `"8.30.1"`, hostsDigest)
			path := filepath.Join(root, ".gitlab-ci.yml")
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(body, job(c.override)...), 0o644); err != nil {
				t.Fatal(err)
			}
			version, _, err := GitleaksPin(root)
			if c.wantErr && err == nil {
				t.Errorf("GitleaksPin accepted a job that overrides GITLEAKS_VERSION to %s and returned %q", c.override, version)
			}
			if !c.wantErr && err != nil {
				t.Errorf("GitleaksPin refused a job-level copy that agrees: %v", err)
			}
		})
	}
}
