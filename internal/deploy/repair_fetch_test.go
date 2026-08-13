package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchRepairScript_AbsenceIsUnavailable(t *testing.T) {
	fakeOrbitSource(t, map[string]string{
		"/scripts/install.sh": "#!/bin/bash\n",
		// no repair.sh — orbit main today
	})
	_, err := FetchRepairScript(context.Background())
	if !errors.Is(err, ErrRepairUnavailable) {
		t.Fatalf("expected ErrRepairUnavailable, got %v", err)
	}
}

func TestFetchRepairScript_FetchesFromScriptsBase(t *testing.T) {
	fakeOrbitSource(t, map[string]string{
		"/scripts/repair.sh": "#!/usr/bin/env bash\necho repair\n",
	})
	body, err := FetchRepairScript(context.Background())
	if err != nil {
		t.Fatalf("FetchRepairScript: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty script")
	}
}

func TestFetchRepairScript_NonScriptContentIsUnavailable(t *testing.T) {
	fakeOrbitSource(t, map[string]string{
		"/scripts/repair.sh": "<html>an error page that returned 200</html>",
	})
	_, err := FetchRepairScript(context.Background())
	if !errors.Is(err, ErrRepairUnavailable) {
		t.Fatalf("expected ErrRepairUnavailable for shebang-less content, got %v", err)
	}
}

func TestStageRepairScript_WritesExecutableIntoTargetScripts(t *testing.T) {
	targetDir := t.TempDir()
	script := []byte("#!/usr/bin/env bash\necho check\n")
	if err := StageRepairScript(targetDir, script); err != nil {
		t.Fatalf("StageRepairScript: %v", err)
	}
	staged := filepath.Join(targetDir, "scripts", "repair.sh")
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("staged script: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %o, want 700", info.Mode().Perm())
	}

	// Re-staging overwrites — the script must never go stale.
	if err := StageRepairScript(targetDir, []byte("#!/bin/bash\necho two\n")); err != nil {
		t.Fatalf("re-stage: %v", err)
	}
	body, _ := os.ReadFile(staged)
	if string(body) != "#!/bin/bash\necho two\n" {
		t.Errorf("re-stage did not overwrite: %q", body)
	}
}

func TestStageRepairScript_MissingTargetIsAnError(t *testing.T) {
	if err := StageRepairScript(filepath.Join(t.TempDir(), "nowhere"), []byte("#!/bin/bash\n")); err == nil {
		t.Fatal("expected an error for a missing target directory")
	}
}

func TestBuildRepairCommand_Shape(t *testing.T) {
	cmd := BuildRepairCommand("/tmp/target", RepairPlan)
	want := []string{"bash", "scripts/repair.sh", "--plan"}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", cmd.Args, want)
		}
	}
	if cmd.Dir != "/tmp/target" {
		t.Errorf("dir = %q", cmd.Dir)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("Setsid must be set")
	}
}
