package supplychain

// Renovate's commits must pass the private-details scan, which refuses any
// real email address in a commit's author or committer (#327, owner decision
// 7a). renovate.json must therefore set gitAuthor to `Name <address>` where
// the address's domain ends in `.invalid` (RFC 6761), a domain that can never
// receive mail.

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// gitAuthorRe matches `Name <local@domain>`; the name is checked for content
// separately so that a whitespace-only name is reported as missing.
var gitAuthorRe = regexp.MustCompile(`^([^<>]*)<([^<>\s@]+)@([^<>\s@]+)>$`)

func TestRenovateCommitsUnderAnInvalidAddress(t *testing.T) {
	var cfg struct {
		GitAuthor *string `json:"gitAuthor"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "renovate.json")), &cfg); err != nil {
		t.Fatalf("parsing renovate.json: %v", err)
	}

	const rule = "renovate.json must set gitAuthor to `Name <address>` where the address's domain ends in `.invalid` (#327): " +
		"Renovate's commits must pass the private-details scan, which refuses real email addresses in commit authors"

	if cfg.GitAuthor == nil {
		t.Fatalf("renovate.json has no gitAuthor, so Renovate commits under a real address and fails the private-details scan. Rule: %s", rule)
	}

	m := gitAuthorRe.FindStringSubmatch(*cfg.GitAuthor)
	if m == nil {
		t.Fatalf("renovate.json gitAuthor %q is malformed; it must be `Name <address>`. Rule: %s", *cfg.GitAuthor, rule)
	}
	if strings.TrimSpace(m[1]) == "" {
		t.Fatalf("renovate.json gitAuthor %q has an empty name; the name part must be non-empty. Rule: %s", *cfg.GitAuthor, rule)
	}
	domain := m[3]
	if !strings.HasSuffix(domain, ".invalid") || domain == ".invalid" {
		t.Fatalf("renovate.json gitAuthor %q has domain %q, which does not end in `.invalid`. Rule: %s", *cfg.GitAuthor, domain, rule)
	}
}
