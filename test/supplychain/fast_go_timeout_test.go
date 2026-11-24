package supplychain

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// #369: the `fast` job's coverage run hands its arguments to `go test`. With
// no -timeout each package gets Go's 10 minute default, and test/pty needs
// about 8 when green, so a busy runner fails the job. The -timeout must sit
// above Go's default and below the job's own `timeout:`, so a real hang
// fails by test name before GitLab kills the job.
func TestFast_GoTestTimeoutSitsBetweenGoDefaultAndJobTimeout(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	fast, err := doc.effectiveJob("fast")
	if err != nil {
		t.Fatal(err)
	}
	tn, ok := fast.Keys["timeout"]
	if !ok || tn.kind != 's' {
		t.Fatal("the `fast` job has no plain `timeout:` to compare against")
	}
	jobTimeout, err := time.ParseDuration(strings.TrimSpace(tn.str))
	if err != nil {
		t.Fatalf("reading the `fast` job's timeout %q: %v", tn.str, err)
	}

	re := regexp.MustCompile(`(?:^|\s)-{1,2}timeout[= ]\s*(\S+)`)
	var goTimeout time.Duration
	found := false
	for _, l := range strings.Split(fast.Script, "\n") {
		if !strings.Contains(l, "coverage.sh") {
			continue
		}
		m := re.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		d, err := time.ParseDuration(m[1])
		if err != nil {
			t.Fatalf("reading go test -timeout %q in %q: %v", m[1], l, err)
		}
		goTimeout, found = d, true
	}
	if !found {
		t.Fatalf("the `fast` job's `scripts/coverage.sh -race` line passes no -timeout to go test, so each package gets Go's 10m default and test/pty (about 8m green) fails on a busy runner (#369). Change it to `scripts/coverage.sh -race -timeout 20m`: longer than 10m, shorter than the job's %s", jobTimeout)
	}
	if goTimeout <= 10*time.Minute || goTimeout >= jobTimeout {
		t.Errorf("go test -timeout is %s; it must be longer than Go's 10m default and shorter than the `fast` job's timeout of %s (#369), so a hang fails by test name before GitLab kills the job", goTimeout, jobTimeout)
	}
}
