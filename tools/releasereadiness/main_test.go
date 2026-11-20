package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/releasereadiness/readiness"
)

func okReport() readiness.Report {
	return readiness.Report{Target: "preview", Results: []readiness.Result{
		{ID: "version", OK: true, Message: "0.4.7"},
		{ID: "tag-free", OK: true, Message: "free"},
	}}
}

func badReport() readiness.Report {
	return readiness.Report{Target: "preview", Results: []readiness.Result{
		{ID: "version", OK: true, Message: "0.4.7"},
		{ID: "tag-free", OK: false, Message: "the tag v0.4.7 already exists: this version shipped."},
	}}
}

// runCmd runs the command with a check function that records its calls.
func runCmd(args []string, rep readiness.Report) (code int, out, errOut string, calls []string) {
	var stdout, stderr bytes.Buffer
	code = run(args, func(target, remote string) readiness.Report {
		calls = append(calls, target+"@"+remote)
		return rep
	}, &stdout, &stderr)
	return code, stdout.String(), stderr.String(), calls
}

func TestMain_TargetIsRequired(t *testing.T) {
	code, _, errOut, calls := runCmd(nil, okReport())
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "target") {
		t.Errorf("stderr %q does not mention -target", errOut)
	}
	if len(calls) != 0 {
		t.Errorf("the checks ran without a target: %v", calls)
	}
}

func TestMain_TargetIsOnlyPreviewOrMain(t *testing.T) {
	for _, target := range []string{"dev", "batch-a", "", "Preview", "preview main"} {
		code, _, errOut, calls := runCmd([]string{"-target", target}, okReport())
		if code != 2 {
			t.Errorf("-target %q: exit = %d, want 2", target, code)
		}
		if errOut == "" {
			t.Errorf("-target %q: nothing on stderr", target)
		}
		if len(calls) != 0 {
			t.Errorf("-target %q: the checks ran: %v", target, calls)
		}
	}
}

func TestMain_AnUnknownFlagExitsTwo(t *testing.T) {
	code, _, _, calls := runCmd([]string{"-target", "preview", "-bogus"}, okReport())
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if len(calls) != 0 {
		t.Errorf("the checks ran despite a bad flag: %v", calls)
	}
}

func TestMain_PassesTheTargetAndRemoteToTheChecks(t *testing.T) {
	_, _, _, calls := runCmd([]string{"-target", "main", "-remote", "gitlab"}, okReport())
	if len(calls) != 1 || calls[0] != "main@gitlab" {
		t.Errorf("calls = %v, want [main@gitlab]", calls)
	}
}

func TestMain_ReadyExitsZeroAndSaysReady(t *testing.T) {
	for _, target := range []string{"preview", "main"} {
		code, out, errOut, _ := runCmd([]string{"-target", target}, okReport())
		if code != 0 {
			t.Errorf("%s: exit = %d, want 0 (stderr %q)", target, code, errOut)
		}
		if !strings.Contains(out, "Ready") {
			t.Errorf("%s: stdout lacks Ready:\n%s", target, out)
		}
	}
}

func TestMain_NotReadyExitsOneAndSaysHowManyFailed(t *testing.T) {
	code, out, _, _ := runCmd([]string{"-target", "preview"}, badReport())
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "Not ready: 1 of 2 checks failed") {
		t.Errorf("stdout lacks the Not ready line:\n%s", out)
	}
	if !strings.Contains(out, "tag-free") {
		t.Errorf("stdout does not name the failed check:\n%s", out)
	}
}
