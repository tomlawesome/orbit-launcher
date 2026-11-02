package live

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestSentinelFile_IsWhereTheDocumentStoreKeepsIt(t *testing.T) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(sentinelStorageKey) {
		t.Fatalf("storage key %q is not 64 lower-case hex characters", sentinelStorageKey)
	}
	want := "/var/lib/orbit/documents/objects/32/43/" + sentinelStorageKey + ".bin"
	if got := sentinelFilePath(); got != want {
		t.Errorf("sentinelFilePath() = %q, want %q", got, want)
	}
	// The same value `printf %s <content> | sha256sum` prints; the leg compares it with the in-container sha256sum.
	if got := sentinelFileSHA256(); !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(got) {
		t.Errorf("sentinelFileSHA256() = %q, want 64 hex characters", got)
	}
}

func TestUpdateSentinelSeedSQL_NamesEverySentinelOnce(t *testing.T) {
	sql := updateSentinelSeedSQL()
	for _, want := range []string{
		sentinelHouseholdID, sentinelSectionID, sentinelItemID, sentinelItemName,
		sentinelDocumentID, sentinelDocumentName, sentinelFileSHA256(), sentinelStorageKey,
	} {
		if !strings.Contains(sql, "'"+want+"'") {
			t.Errorf("the seed SQL does not carry %q", want)
		}
	}
	if strings.Contains(sql, "%!") {
		t.Errorf("the seed SQL has a formatting error:\n%s", sql)
	}
	// The row names the file's size, which the object store checks.
	if !strings.Contains(sql, fmt.Sprintf(", %d, ", len(sentinelFileContent))) {
		t.Errorf("the seed SQL does not carry the file's size (%d):\n%s", len(sentinelFileContent), sql)
	}
}

func TestSentinelChanges(t *testing.T) {
	want := wantedSentinels()
	if got := sentinelChanges(want, want); len(got) != 0 {
		t.Errorf("unchanged sentinels reported as changed: %v", got)
	}
	cases := []struct {
		name    string
		mutate  func(*updateSentinels)
		mention string
	}{
		{"item changed", func(s *updateSentinels) { s.ItemName = "other" }, "item sentinel changed"},
		{"item gone", func(s *updateSentinels) { s.ItemName = "" }, "item sentinel is gone"},
		{"document row changed", func(s *updateSentinels) { s.DocumentName = "other" }, "document row sentinel changed"},
		{"document file gone", func(s *updateSentinels) { s.FileSHA256 = "" }, "document file sentinel is gone"},
		{"document file changed", func(s *updateSentinels) { s.FileSHA256 = "abc" }, "document file sentinel changed"},
	}
	for _, c := range cases {
		got := want
		c.mutate(&got)
		problems := sentinelChanges(want, got)
		if len(problems) != 1 || !strings.Contains(problems[0], c.mention) {
			t.Errorf("%s: got %v, want one problem mentioning %q", c.name, problems, c.mention)
		}
	}
}

func TestManifestVersion(t *testing.T) {
	if got, err := manifestVersion([]byte(`{"version":"v0.3.4"}`)); err != nil || got != "v0.3.4" {
		t.Errorf("manifestVersion = %q, %v; want v0.3.4", got, err)
	}
	if _, err := manifestVersion([]byte(`{}`)); err == nil {
		t.Error("a manifest with no version was accepted")
	}
	if _, err := manifestVersion([]byte(`nope`)); err == nil {
		t.Error("a manifest that is not JSON was accepted")
	}
}

func TestHealthStatus(t *testing.T) {
	if got, err := healthStatus([]byte(`{"status":"ready","service":"orbit"}`)); err != nil || got != "ready" {
		t.Errorf("healthStatus = %q, %v; want ready", got, err)
	}
	if got, err := healthStatus([]byte(`{"status":"degraded"}`)); err != nil || got != "degraded" {
		t.Errorf("healthStatus = %q, %v; want degraded", got, err)
	}
	for _, body := range []string{`{}`, `<html>`} {
		if _, err := healthStatus([]byte(body)); err == nil {
			t.Errorf("healthStatus(%q) accepted a body with no status", body)
		}
	}
}
