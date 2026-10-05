package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func fixedTags(tags ...string) func() ([]string, error) {
	return func() ([]string, error) { return tags, nil }
}

func TestRunPrintsTheNextMinorVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, fixedTags("v0.4.2", "preview"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code %d, want 0; stderr: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "0.5.0\n" {
		t.Errorf("stdout = %q, want %q", got, "0.5.0\n")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}

func TestRunHotfixFlagPrintsTheNextPatchVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-hotfix"}, fixedTags("v0.4.2"), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d, want 0; stderr: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "0.4.3\n" {
		t.Errorf("stdout = %q, want %q", got, "0.4.3\n")
	}
}

func TestRunFailsWithExitOneWhenTagsCannotBeRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	failing := func() ([]string, error) { return nil, errors.New("not a git repository") }
	if code := run(nil, failing, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q; a failed run must not print a version", stdout.String())
	}
	if got := stderr.String(); got != "calculateversion: not a git repository\n" {
		t.Errorf("stderr = %q", got)
	}
}

func TestRunRejectsAnUnknownFlagWithExitTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	tags := func() ([]string, error) { called = true; return nil, nil }
	if code := run([]string{"-bogus"}, tags, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if called {
		t.Error("tags were read despite a bad flag")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "-bogus") {
		t.Errorf("stdout = %q, stderr = %q; want the bad flag named on stderr only", stdout.String(), stderr.String())
	}
}

func TestRunHelpExitsZeroAndDescribesHotfix(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-h"}, fixedTags(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "-hotfix") {
		t.Errorf("stdout = %q, stderr = %q; want usage naming -hotfix on stderr", stdout.String(), stderr.String())
	}
}

// git runs git in dir with the user's and system's configuration ignored, so
// a signing or hooks setting on the host cannot change what the test builds.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitTagsListsEveryTagInTheRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "first")
	for _, tag := range []string{"v0.1.0", "v0.2.0", "preview"} {
		git(t, dir, "tag", tag)
	}

	got, err := gitTags(dir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{"preview", "v0.1.0", "v0.2.0"}
	if !slices.Equal(got, want) {
		t.Errorf("gitTags() = %q, want %q", got, want)
	}
}

func TestGitTagsReturnsNothingForARepositoryWithoutTags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")

	got, err := gitTags(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("gitTags() = %q, want no tags (and no empty-string entry)", got)
	}
}

func TestGitTagsFailsOutsideARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", dir) // stop git finding a repository above the temp dir

	_, err := gitTags(dir)
	if err == nil {
		t.Fatal("gitTags outside a repository returned no error")
	}
	if !strings.Contains(err.Error(), "git tag --list") {
		t.Errorf("error %q does not name the command that failed", err)
	}
}
