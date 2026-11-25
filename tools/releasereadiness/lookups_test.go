package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/tools/releasereadiness/readiness"
)

// ---- a throwaway repository with a bare repository as its remote ----------

type fixture struct {
	t      *testing.T
	dir    string // the working clone
	remote string // the bare repository
}

func sh(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Ada Tester", "GIT_AUTHOR_EMAIL=tom@example.invalid",
		"GIT_COMMITTER_NAME=Ada Tester", "GIT_COMMITTER_EMAIL=tom@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, dir: filepath.Join(root, "work"), remote: filepath.Join(root, "remote.git")}
	sh(t, root, nil, "init", "--bare", "-b", "main", f.remote)
	sh(t, root, nil, "init", "-b", "main", f.dir)
	sh(t, f.dir, nil, "remote", "add", "origin", f.remote)
	f.write("README.md", "hello\n")
	f.write("docs/releasing.md", "# Releasing\n\n## What you do, in short\n\n1. a\n")
	f.commit("initial")
	sh(t, f.dir, nil, "push", "-q", "origin", "main")
	return f
}

func (f *fixture) git(args ...string) string { f.t.Helper(); return sh(f.t, f.dir, nil, args...) }

func (f *fixture) write(path, content string) {
	f.t.Helper()
	p := filepath.Join(f.dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// commit stages everything and commits; extra holds `-c`-style options such
// as "--trailer", "Signed-off-by: X". Returns the new commit's id.
func (f *fixture) commit(msg string, extra ...string) string {
	f.t.Helper()
	f.git("add", "-A")
	f.git(append([]string{"commit", "-q", "-m", msg}, extra...)...)
	return f.git("rev-parse", "HEAD")
}

func (f *fixture) commitAs(author, msg string, extra ...string) string {
	f.t.Helper()
	f.git("add", "-A")
	sh(f.t, f.dir, []string{"GIT_AUTHOR_NAME=" + author, "GIT_COMMITTER_NAME=" + author},
		append([]string{"commit", "-q", "-m", msg}, extra...)...)
	return f.git("rev-parse", "HEAD")
}

// branchFrom pushes main as origin/<name> and checks out a local <name>.
func (f *fixture) publish(branch, from string) {
	f.t.Helper()
	f.git("push", "-q", "origin", from+":refs/heads/"+branch)
	f.git("fetch", "-q", "origin")
}

func (f *fixture) cfg(target string) lookupConfig {
	return lookupConfig{
		Dir: f.dir, Remote: "origin", Target: target,
		GitTimeout: 30 * time.Second, HTTPTimeout: 5 * time.Second, MaxBody: 1 << 20,
	}
}

func (f *fixture) lookups(target string) *realLookups { return newLookups(f.cfg(target)) }

const recordFile = "docs/releases/v0.4.7.md"

func recordWith(signLines ...string) string {
	return "version: v0.4.7\nguide-blob: x\nstep 1: after-merge\n" + strings.Join(signLines, "\n") + "\n"
}

// ---- sign-off ---------------------------------------------------------------

func TestLookups_SignOff(t *testing.T) {
	cases := []struct {
		name    string
		record  string
		author  string
		trailer []string // --trailer values
		signed  bool
		whyHas  string
	}{
		{"signed: line = author = trailer", recordWith("Signed-off-by: Ada Tester"), "Ada Tester", []string{"Signed-off-by: Ada Tester"}, true, ""},
		{"no trailer", recordWith("Signed-off-by: Ada Tester"), "Ada Tester", nil, false, "trailer"},
		{"line differs from the author", recordWith("Signed-off-by: Someone Else"), "Ada Tester", []string{"Signed-off-by: Someone Else"}, false, "author"},
		{"trailer differs", recordWith("Signed-off-by: Ada Tester"), "Ada Tester", []string{"Signed-off-by: Someone Else"}, false, "trailer"},
		{"email in the line", recordWith("Signed-off-by: Ada Tester <tom@example.invalid>"), "Ada Tester", []string{"Signed-off-by: Ada Tester"}, false, "email"},
		{"no line", recordWith(), "Ada Tester", []string{"Signed-off-by: Ada Tester"}, false, "Signed-off-by"},
		{"two lines", recordWith("Signed-off-by: Ada Tester", "Signed-off-by: Ada Tester"), "Ada Tester", []string{"Signed-off-by: Ada Tester"}, false, "one"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.write(recordFile, c.record)
			var extra []string
			for _, tr := range c.trailer {
				extra = append(extra, "--trailer", tr)
			}
			f.commitAs(c.author, "record", extra...)
			got, err := f.lookups("main").RecordSignoff(recordFile)
			if err != nil {
				t.Fatal(err)
			}
			// #364: a fault in the file's own line is flagged as one, so the
			// hint sends the author to the file, not to --trailer.
			inFile := map[string]bool{"line differs from the author": true, "email in the line": true, "no line": true, "two lines": true}[c.name]
			if got.InFile != inFile {
				t.Errorf("InFile = %v, want %v (why: %q)", got.InFile, inFile, got.Why)
			}
			if got.Signed != c.signed {
				t.Errorf("Signed = %v, want %v (why: %q)", got.Signed, c.signed, got.Why)
			}
			if !c.signed && !strings.Contains(strings.ToLower(got.Why), strings.ToLower(c.whyHas)) {
				t.Errorf("Why = %q, want it to mention %q", got.Why, c.whyHas)
			}
		})
	}
}

func TestLookups_SignOffReadsTheLastCommitTouchingTheRecordNotHEAD(t *testing.T) {
	f := newFixture(t)
	f.write(recordFile, recordWith("Signed-off-by: Ada Tester"))
	f.commitAs("Ada Tester", "record", "--trailer", "Signed-off-by: Ada Tester")
	f.write("other.txt", "x\n")
	f.commitAs("Another Person", "unrelated, no trailer")
	got, err := f.lookups("main").RecordSignoff(recordFile)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Signed {
		t.Errorf("a later unrelated commit by someone else must not unsign the record: %q", got.Why)
	}
}

// ---- guide blob -------------------------------------------------------------

func TestLookups_GuideBlobMatchesTheGitBlobOfTheGuide(t *testing.T) {
	f := newFixture(t)
	got, err := f.lookups("main").GuideBlob()
	if err != nil {
		t.Fatal(err)
	}
	want := f.git("rev-parse", "HEAD:docs/releasing.md")
	if got != want || got == "" {
		t.Errorf("GuideBlob = %q, want %q", got, want)
	}
}

func TestLookups_GuideBlobStopsMatchingAfterAGuideEdit(t *testing.T) {
	f := newFixture(t)
	before, _ := f.lookups("main").GuideBlob()
	f.write("docs/releasing.md", "# Releasing\n\n## What you do, in short\n\n1. a\n2. b\n")
	f.commit("edit guide")
	after, err := f.lookups("main").GuideBlob()
	if err != nil {
		t.Fatal(err)
	}
	if before == "" || after == before {
		t.Errorf("blob before %q after %q: must differ after an edit", before, after)
	}
}

func TestLookups_GuideBlobSurvivesAnUnrelatedCommitAndARebase(t *testing.T) {
	f := newFixture(t)
	before, _ := f.lookups("main").GuideBlob()
	f.git("checkout", "-q", "-b", "feat")
	f.write("a.txt", "a\n")
	f.commit("unrelated")
	if got, _ := f.lookups("main").GuideBlob(); before == "" || got != before {
		t.Errorf("after an unrelated commit: %q, want %q", got, before)
	}
	f.git("checkout", "-q", "main")
	f.write("b.txt", "b\n")
	f.commit("main moves on")
	f.git("checkout", "-q", "feat")
	f.git("rebase", "-q", "main")
	if got, _ := f.lookups("main").GuideBlob(); got != before {
		t.Errorf("after a rebase: %q, want %q", got, before)
	}
}

// ---- contains-target --------------------------------------------------------

func TestLookups_ContainsTarget(t *testing.T) {
	f := newFixture(t)
	f.publish("preview", "main")
	f.git("checkout", "-q", "-b", "dev")
	f.write("d.txt", "d\n")
	f.commit("on dev")
	got, err := f.lookups("preview").ContainsTarget()
	if err != nil || !got {
		t.Errorf("dev contains origin/preview: got %v, %v; want true", got, err)
	}

	// preview moves on elsewhere and is fetched: dev no longer contains it.
	other := filepath.Join(t.TempDir(), "other")
	sh(t, filepath.Dir(other), nil, "clone", "-q", f.remote, other)
	if err := os.WriteFile(filepath.Join(other, "p.txt"), []byte("p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, other, nil, "add", "-A")
	sh(t, other, nil, "commit", "-q", "-m", "preview only")
	sh(t, other, nil, "push", "-q", "origin", "HEAD:refs/heads/preview")
	f.git("fetch", "-q", "origin")
	got, err = f.lookups("preview").ContainsTarget()
	if err != nil || got {
		t.Errorf("preview has a commit dev lacks: got %v, %v; want false", got, err)
	}
}

func TestLookups_ContainsTargetWithAnUnfetchedTargetErrorsNamingTheFetchCommand(t *testing.T) {
	f := newFixture(t)
	_, err := f.lookups("preview").ContainsTarget() // origin/preview was never fetched
	if err == nil {
		t.Fatal("want an error when the target ref was never fetched")
	}
	if !strings.Contains(err.Error(), "git fetch") || !strings.Contains(err.Error(), "preview") {
		t.Errorf("error %q must name the fetch command and the branch", err)
	}
}

// ---- main tree --------------------------------------------------------------

func TestLookups_MainTreeEqualsPreview(t *testing.T) {
	t.Run("true for a back-merged preview", func(t *testing.T) {
		f := newFixture(t)
		f.git("checkout", "-q", "-b", "preview")
		f.write("p.txt", "p\n")
		f.commit("preview work")
		f.git("checkout", "-q", "main")
		f.git("merge", "-q", "--no-ff", "-m", "promote", "preview")
		f.git("push", "-q", "origin", "main")
		f.git("checkout", "-q", "preview")
		f.git("merge", "-q", "--ff-only", "main") // the back-merge
		got, err := f.lookups("main").MainTreeEqualsPreview()
		if err != nil || !got {
			t.Errorf("got %v, %v; want true", got, err)
		}
	})
	t.Run("false when main has a commit preview lacks", func(t *testing.T) {
		f := newFixture(t)
		f.git("checkout", "-q", "-b", "preview")
		f.write("p.txt", "p\n")
		f.commit("preview work")
		f.git("checkout", "-q", "main")
		f.write("m.txt", "m\n")
		f.commit("main only")
		f.git("push", "-q", "origin", "main")
		f.git("checkout", "-q", "preview")
		got, err := f.lookups("main").MainTreeEqualsPreview()
		if err != nil || got {
			t.Errorf("got %v, %v; want false", got, err)
		}
		// The back-merge repairs it.
		f.git("merge", "-q", "--no-edit", "main")
		got, err = f.lookups("main").MainTreeEqualsPreview()
		if err != nil || !got {
			t.Errorf("after the back-merge: got %v, %v; want true", got, err)
		}
	})
	t.Run("false when the merge would change files", func(t *testing.T) {
		f := newFixture(t)
		f.git("checkout", "-q", "-b", "preview")
		f.write("README.md", "preview text\n")
		f.commit("preview edits README")
		f.git("checkout", "-q", "main")
		f.write("README.md", "main text\n")
		f.commit("main edits README")
		f.git("push", "-q", "origin", "main")
		f.git("checkout", "-q", "preview")
		got, err := f.lookups("main").MainTreeEqualsPreview()
		if err != nil || got {
			t.Errorf("a conflicting merge: got %v, %v; want false and no error", got, err)
		}
		f.git("merge", "-q", "-s", "ours", "--no-edit", "main") // the back-merge, keeping preview's files
		got, err = f.lookups("main").MainTreeEqualsPreview()
		if err != nil || !got {
			t.Errorf("after the back-merge: got %v, %v; want true", got, err)
		}
	})
}

// ---- audit coverage ---------------------------------------------------------

func TestLookups_AuditCoverageIsComputedFromAuditedThroughLinesAcrossRecords(t *testing.T) {
	f := newFixture(t)
	f.publish("preview", "main")
	f.git("checkout", "-q", "-b", "dev")
	f.write("a.go", "package a\n")
	c1 := f.commit("c1")
	f.write("b.go", "package b\n")
	c2 := f.commit("c2")
	f.write(".agents/reviews/audit-1.md", "# Audit: batch 1 (#361)\n\nAudited-through: "+c1+"\n")
	f.write(".agents/reviews/audit-2.md", "# Audit: batch 2 (#362)\n\nAudited-through: "+c2+"\n")
	f.commit("audit records")
	f.write("c.go", "package c\n")
	c3 := f.commit("c3, after every audit")

	l := f.lookups("preview")
	got, err := l.UncoveredCommits()
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]readiness.Commit{}
	for _, c := range got {
		set[c.SHA] = c
	}
	if _, ok := set[c3]; !ok {
		t.Errorf("c3 %s is after every Audited-through and must be uncovered; got %v", c3, got)
	}
	for _, covered := range []string{c1, c2} {
		if _, ok := set[covered]; ok {
			t.Errorf("%s is an ancestor of an Audited-through and must be covered", covered)
		}
	}
	if c := set[c3]; len(c.Files) != 1 || c.Files[0] != "c.go" {
		t.Errorf("c3's Files = %v, want [c.go]", c.Files)
	}
	for _, c := range got {
		if len(c.Files) == 0 {
			t.Errorf("commit %s has no Files", c.SHA)
		}
	}

	issues, err := l.AuditIssues()
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || !((issues[0] == 361 && issues[1] == 362) || (issues[0] == 362 && issues[1] == 361)) {
		t.Errorf("AuditIssues = %v, want #361 and #362 (both Audited-through commits are in target..source)", issues)
	}
}

func TestLookups_AuditIgnoresMergeCommitsAndAuditsBeforeTheRange(t *testing.T) {
	f := newFixture(t)
	f.write("old.go", "package old\n")
	old := f.commit("old, already on preview")
	f.write(".agents/reviews/audit-0.md", "# Audit: old (#300)\n\nAudited-through: "+old+"\n")
	f.commit("old audit record")
	f.publish("preview", "main")
	f.git("push", "-q", "origin", "HEAD:refs/heads/preview", "-f")
	f.git("fetch", "-q", "origin")
	f.git("checkout", "-q", "-b", "dev")
	f.git("checkout", "-q", "-b", "side")
	f.write("s.go", "package s\n")
	f.commit("side work")
	f.git("checkout", "-q", "dev")
	f.write("d.go", "package d\n")
	f.commit("dev work")
	f.git("merge", "-q", "--no-ff", "-m", "merge side", "side")

	l := f.lookups("preview")
	got, err := l.UncoveredCommits()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		parents := f.git("rev-list", "--parents", "-n1", c.SHA)
		if len(strings.Fields(parents)) > 2 {
			t.Errorf("merge commit %s must not be listed", c.SHA)
		}
	}
	if len(got) != 2 {
		t.Errorf("want the two non-merge commits (side work, dev work); got %v", got)
	}
	issues, err := l.AuditIssues()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range issues {
		if n == 300 {
			t.Errorf("#300's Audited-through is before target..source and must not be listed; got %v", issues)
		}
	}
}

func TestLookups_ARenameIntoDocsReleasesTouchesTheCodeFilesOldPath(t *testing.T) {
	f := newFixture(t)
	f.write("tools/x.go", "package x\n")
	f.commit("add x")
	f.publish("preview", "main")
	f.git("checkout", "-q", "-b", "dev")
	if err := os.MkdirAll(filepath.Join(f.dir, "docs", "releases"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.git("mv", "tools/x.go", "docs/releases/x.go")
	moved := f.commit("move x into releases")

	got, err := f.lookups("preview").UncoveredCommits()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.SHA != moved {
			continue
		}
		for _, file := range c.Files {
			if file == "tools/x.go" {
				return
			}
		}
		t.Fatalf("the rename's Files = %v, want it to include the old path tools/x.go", c.Files)
	}
	t.Fatalf("the commit %s moving code into docs/releases must be reported as uncovered; got %v", moved, got)
}

// ---- tag-free ---------------------------------------------------------------

func TestLookups_TagFreeReadsTheRemoteNotTheLocalTags(t *testing.T) {
	f := newFixture(t)
	f.git("tag", "v0.4.8") // local only
	l := prepareLookups(f.cfg("main"), true)
	if got, err := l.RemoteTagExists("v0.4.8"); err != nil || got {
		t.Errorf("a local-only tag: got %v, %v; want false (the remote has none)", got, err)
	}
	// On the remote only: pushed from another clone, never fetched here.
	other := filepath.Join(t.TempDir(), "other")
	sh(t, filepath.Dir(other), nil, "clone", "-q", f.remote, other)
	sh(t, other, nil, "tag", "v0.4.7")
	sh(t, other, nil, "push", "-q", "origin", "v0.4.7")
	// The tool fetches the remote's tags before the checks (review of 9500af3).
	l = prepareLookups(f.cfg("main"), true)
	if got, err := l.RemoteTagExists("v0.4.7"); err != nil || !got {
		t.Errorf("a remote-only tag: got %v, %v; want true", got, err)
	}
	if got, err := l.RemoteTagExists("v0.4.70"); err != nil || got {
		t.Errorf("v0.4.70 must not match v0.4.7's tag: got %v, %v", got, err)
	}
}

// ---- time limits and caps ---------------------------------------------------

func TestLookups_AHungGitChildFailsItsCheckNamingTheLimit(t *testing.T) {
	f := newFixture(t)
	hung := filepath.Join(t.TempDir(), "git")
	// A child that outlives its parent holds the output pipe open.
	if err := os.WriteFile(hung, []byte("#!/bin/sh\nsleep 30\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := f.cfg("main")
	cfg.GitBin = hung
	cfg.GitTimeout = 300 * time.Millisecond
	start := time.Now()
	_, err := newLookups(cfg).RemoteTagExists("v0.4.7")
	if err == nil {
		t.Fatal("a hung git must be an error")
	}
	if !strings.Contains(err.Error(), "300ms") {
		t.Errorf("error %q must name the 300ms limit", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("returned after %v: the limit was not enforced", d)
	}
}

func api(t *testing.T, h http.HandlerFunc) (cfg func(lookupConfig) lookupConfig, stop func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	return func(c lookupConfig) lookupConfig { c.GitLabAPI = srv.URL; return c }, srv.Close
}

func TestLookups_IssueStateReadsTheState(t *testing.T) {
	with, stop := api(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "362") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"iid":362,"state":"closed"}`))
	})
	defer stop()
	got, err := newLookups(with(lookupConfig{HTTPTimeout: 5 * time.Second, MaxBody: 1 << 20})).IssueState(362)
	if err != nil || got != "closed" {
		t.Errorf("got %q, %v; want closed", got, err)
	}
}

func TestLookups_AStalledHTTPBodyFailsNamingTheLimit(t *testing.T) {
	release := make(chan struct{})
	with, stop := api(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte(`{"state":`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer stop()
	defer close(release)
	start := time.Now()
	_, err := newLookups(with(lookupConfig{HTTPTimeout: 300 * time.Millisecond, MaxBody: 1 << 20})).IssueState(362)
	if err == nil {
		t.Fatal("a stalled body must be an error")
	}
	if !strings.Contains(err.Error(), "300ms") {
		t.Errorf("error %q must name the 300ms limit", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("returned after %v: the limit was not enforced", d)
	}
}

func TestLookups_AnHTTPBodyOverTheCapIsRefused(t *testing.T) {
	with, stop := api(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"closed","pad":"` + strings.Repeat("x", 64*1024) + `"}`))
	})
	defer stop()
	_, err := newLookups(with(lookupConfig{HTTPTimeout: 5 * time.Second, MaxBody: 1024})).IssueState(362)
	if err == nil {
		t.Fatal("a body over the cap must be refused")
	}
	if !strings.Contains(err.Error(), "1024") {
		t.Errorf("error %q must name the 1024-byte cap", err)
	}
}

func TestLookups_ErrorsNameTheCallWithoutHeaderQueryOrBodyText(t *testing.T) {
	const token = "placeholder-secret-three"
	with, stop := api(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "SECRETBODYTEXT "+r.Header.Get("PRIVATE-TOKEN")+r.Header.Get("Authorization")+r.URL.RawQuery, http.StatusInternalServerError)
	})
	defer stop()
	cfg := with(lookupConfig{HTTPTimeout: 5 * time.Second, MaxBody: 1 << 20, GitLabToken: token})
	cfg.GitLabAPI += "/projects/7?private_token=" + token
	_, err := newLookups(cfg).IssueState(362)
	if err == nil {
		t.Fatal("a 500 must be an error")
	}
	msg := err.Error()
	for _, leak := range []string{"SECRETBODYTEXT", token, "private_token", "?"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error %q leaks %q", msg, leak)
		}
	}
	if !strings.Contains(msg, "362") || !strings.Contains(msg, "500") {
		t.Errorf("error %q must name the call (issue 362) and the status", msg)
	}
}

// ---- tags come from a fetched snapshot (review of 9500af3) ------------------

// In CI the credential drop leaves origin anonymous before the tool runs, so
// the tag checks must read what a fetch made earlier wrote, not ls-remote.
func TestLookups_TagsAreReadFromTheFetchedSnapshotWithNoFetch(t *testing.T) {
	f := newFixture(t)
	f.git("tag", "v0.4.8") // local only: never on the remote
	other := filepath.Join(t.TempDir(), "other")
	sh(t, filepath.Dir(other), nil, "clone", "-q", f.remote, other)
	sh(t, other, nil, "tag", "v0.4.6")
	sh(t, other, nil, "push", "-q", "origin", "v0.4.6")

	if err := prepareLookups(f.cfg("main"), true).fetchErr; err != nil {
		t.Fatalf("the fetch failed: %v", err)
	}
	// The remote goes away, as origin's credentials do in CI; -no-fetch.
	if err := os.RemoveAll(f.remote); err != nil {
		t.Fatal(err)
	}
	l := prepareLookups(f.cfg("main"), false)
	if got, err := l.RemoteTagExists("v0.4.6"); err != nil || !got {
		t.Errorf("v0.4.6 is on the remote and was fetched: got %v, %v; want true", got, err)
	}
	if got, err := l.RemoteTagExists("v0.4.8"); err != nil || got {
		t.Errorf("v0.4.8 is local only: got %v, %v; want false", got, err)
	}
	tags, err := l.Tags()
	if err != nil || strings.Join(tags, ",") != "v0.4.6" {
		t.Errorf("Tags = %v, %v; want only the remote's v0.4.6", tags, err)
	}
}

const tagChangelog = "# Changelog\n\n## [Unreleased]\n\n## [0.4.7] - 2026-11-20\n\n### Fixed\n\n- A fix.\n"

func TestLookups_NothingFetchedMakesTheTagChecksCouldNotCheck(t *testing.T) {
	f := newFixture(t)
	f.write("CHANGELOG.md", tagChangelog)
	f.commit("changelog")
	f.git("tag", "v0.4.6") // local tags are not the remote's
	rep := readiness.Run("preview", f.dir, prepareLookups(f.cfg("preview"), false))
	for _, id := range []string{"tag-free", "version"} {
		for _, r := range rep.Results {
			if r.ID != id {
				continue
			}
			if r.OK || !strings.Contains(r.Message, "could not check") || !strings.Contains(r.Message, "fetch") {
				t.Errorf("%s with nothing fetched: ok=%v %q; want FAIL could not check, naming the fetch", id, r.OK, r.Message)
			}
		}
	}
}

func TestLookups_AFailedFetchFailsOnlyTheTagChecks(t *testing.T) {
	f := newFixture(t)
	f.write("CHANGELOG.md", tagChangelog)
	f.commit("changelog")
	cfg := f.cfg("main")
	cfg.Remote = "nowhere"
	l := prepareLookups(cfg, true)
	if l.fetchErr == nil {
		t.Fatal("fetching from a remote that does not exist must fail")
	}
	if _, err := l.RemoteTagExists("v0.4.7"); err == nil || !strings.Contains(err.Error(), "fetch") {
		t.Errorf("RemoteTagExists after a failed fetch: %v; want an error naming the fetch", err)
	}
	if _, found, err := l.SourceFile("CHANGELOG.md"); err != nil || !found {
		t.Errorf("a failed tag fetch must not stop the other lookups: %v %v", found, err)
	}
}
