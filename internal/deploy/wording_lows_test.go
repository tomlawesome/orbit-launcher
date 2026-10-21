package deploy

// The owner's stand-down timeout wording, 2026-10-21 (#236). Written from
// the decision alone: StandDownCommandWords gives the command to run by
// hand, and a timed-out StandDown returns a *StandDownTimeoutError that
// carries it with the limit that applied.

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStandDownCommandWords_AreTheCommandStandDownRuns(t *testing.T) {
	got := StandDownCommandWords("/opt/orbit")
	want := []string{"docker", "compose", "--project-directory", "/opt/orbit", "--env-file", "/opt/orbit/.env-orbit", "down"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StandDownCommandWords(/opt/orbit) = %q, want %q", got, want)
	}
	// The words are what standDownCommand runs, so they cannot drift.
	run := standDownCommand(context.Background(), "/opt/orbit")
	if tail := run.Args[len(run.Args)-len(want)+1:]; !reflect.DeepEqual(tail, want[1:]) {
		t.Errorf("the command runs %q, the words say %q", tail, want[1:])
	}
}

// A path with a space or a quote is quoted so that a POSIX shell gives the
// same path back: pasting the words into sh must pass the arguments on
// unchanged.
func TestStandDownCommandWords_QuoteSoAShellGivesTheSamePathBack(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh here")
	}
	for _, dir := range []string{"/opt/orbit", "/opt/my orbit", "/opt/o'brien orbit", `/opt/a"b$c`, "/opt/two  spaces"} {
		t.Run(dir, func(t *testing.T) {
			words := StandDownCommandWords(dir)
			if len(words) != 7 || words[0] != "docker" || words[1] != "compose" || words[6] != "down" {
				t.Fatalf("words = %q, want docker compose ... down (7 words)", words)
			}
			out, err := exec.Command(sh, "-c", `printf '%s\n' `+strings.Join(words[2:], " ")).Output()
			if err != nil {
				t.Fatalf("sh: %v", err)
			}
			got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			want := []string{"--project-directory", dir, "--env-file", filepath.Join(dir, EnvFile), "down"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("sh sees %q, want %q (words %q)", got, want, words)
			}
		})
	}
}

func TestStandDown_ALimitTimeoutIsAStandDownTimeoutErrorNamingThatLimit(t *testing.T) {
	hangingDockerOnPath(t)
	setStandDownTimeout(t, 150*time.Millisecond)
	dir := trustedDeployment(t)

	err := StandDown(context.Background(), dir)
	var te *StandDownTimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v (%T), want a *StandDownTimeoutError", err, err)
	}
	if te.Limit != 150*time.Millisecond {
		t.Errorf("Limit = %v, want 150ms", te.Limit)
	}
	if !reflect.DeepEqual(te.Words, StandDownCommandWords(dir)) {
		t.Errorf("Words = %q, want %q", te.Words, StandDownCommandWords(dir))
	}
}

// A caller's earlier deadline is the limit that applied.
func TestStandDown_ACallerDeadlineIsTheLimitInTheTimeoutError(t *testing.T) {
	hangingDockerOnPath(t)
	setStandDownTimeout(t, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := StandDown(ctx, trustedDeployment(t))
	var te *StandDownTimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v (%T), want a *StandDownTimeoutError", err, err)
	}
	if te.Limit <= 0 || te.Limit > time.Second {
		t.Errorf("Limit = %v, want the caller's short deadline (about 150ms)", te.Limit)
	}
}

// Guard: only a timeout is a timeout error. Docker failing on its own is
// reported with its words as before.
func TestStandDown_AnotherFailureIsNotAStandDownTimeoutError(t *testing.T) {
	fakeDockerPrinting(t, "no such service: orbit\n", 1)
	err := StandDown(t.Context(), trustedDeployment(t))
	if err == nil {
		t.Fatal("expected an error when docker compose down fails")
	}
	var te *StandDownTimeoutError
	if errors.As(err, &te) {
		t.Errorf("a failure that was not a timeout is a *StandDownTimeoutError: %v", err)
	}
	if !strings.Contains(err.Error(), "no such service: orbit") {
		t.Errorf("error should still carry docker's output, got: %v", err)
	}
}

// A refusal before Docker runs is not a timeout either.
func TestStandDown_ARefusedDirectoryIsNotAStandDownTimeoutError(t *testing.T) {
	err := StandDown(t.Context(), t.TempDir()+"/missing")
	var te *StandDownTimeoutError
	if err == nil || errors.As(err, &te) {
		t.Errorf("err = %v, want a refusal that is not a *StandDownTimeoutError", err)
	}
}
