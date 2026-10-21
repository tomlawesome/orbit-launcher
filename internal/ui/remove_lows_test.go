package ui

// Lows from #236 for the Remove lane (blind tests, written from the issue
// text and the security ledger only).

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// lowsRemoveModel is Remove at 80x24 with the given stand-down.
func lowsRemoveModel(d *deploy.Deployment, standDown func(context.Context, string) error) RemoveModel {
	m := NewRemoveModel(d)
	m.standDown = standDown
	m.clipboard = io.Discard
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(RemoveModel)
}

// 236-1: a stand-down is reversible, so its working screen must not
// borrow Repair's "stopping halfway is the one unsafe step". It still says
// it cannot be stopped from here (standdown_uninterruptible_test.go).
func TestRemove_WorkingScreenDoesNotCallStoppingUnsafe(t *testing.T) {
	m, _ := removeStandingDown(t, standDownOK)
	s := strings.ToLower(screen(m))
	if !strings.Contains(s, "standing down orbit") {
		t.Fatalf("not the working screen:\n%s", s)
	}
	for _, bad := range []string{"unsafe", "halfway"} {
		if strings.Contains(s, bad) {
			t.Errorf("the Remove working screen says %q, which is untrue for a reversible stand-down:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, cannotStop) {
		t.Errorf("the working screen no longer says %q:\n%s", cannotStop, s)
	}
}

// 236-14: Compose prints a progress line per container, about 36 for
// Orbit. The failed screen must cap that output so Menu and Exit stay on a
// 24-row terminal, while still showing part of the error. Driven through
// the real deploy.StandDown with a fake docker, so it holds wherever the
// cap lives.
func TestRemove_FailedScreenCapsComposeOutputSoTheMenuStaysOnScreen(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	testutil.StageTree(t, dir, 0o755, testutil.File{Path: "scripts/repair.sh", Body: "#!/usr/bin/env bash\n", Mode: 0o644})
	testutil.FakeExecutable(t, "docker", `#!/usr/bin/env bash
for i in $(seq 1 40); do echo " Container orbit-service-$i  Stopping" >&2; done
echo "Error response from daemon: network orbit_default has active endpoints" >&2
exit 1
`)

	m := lowsRemoveModel(&deploy.Deployment{TargetDir: dir, AppURL: "https://mail.example.com"}, deploy.StandDown)
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Stand down Orbit is preselected
	if cmd == nil {
		t.Fatal("confirming started nothing")
	}
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateFailed {
		t.Fatalf("state = %v, want removeStateFailed:\n%s", m.state, screen(m))
	}

	s := screen(m)
	lines := strings.Split(strings.TrimRight(s, "\n \t"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	exit := -1
	for i, l := range lines {
		if strings.Contains(l, "Exit") {
			exit = i
		}
	}
	if exit < 0 || exit >= 24 {
		t.Errorf("no Exit row within the 24 rows of the terminal (found at index %d of %d lines); the output is not capped", exit, len(lines))
	}
	if !strings.Contains(s, "▸ Menu") {
		t.Errorf("the Menu row is missing:\n%s", s)
	}
	if n := strings.Count(s, "Stopping"); n > 12 {
		t.Errorf("%d progress lines on the failed screen, want a short tail (the engine failure screen keeps six)", n)
	}
	if !strings.Contains(s, "active endpoints") && !strings.Contains(s, "Stand-down stopped") {
		t.Errorf("none of the error survived the cap:\n%s", s)
	}
}

// 236-c9 (SP1-3): a Remove for the filesystem root or the home directory
// offers no removal command: nothing to paste, nothing to copy.
func TestRemove_DoneScreenOffersNoCommandForRootOrHome(t *testing.T) {
	const home = "/home/someone"
	t.Setenv("HOME", home)
	for _, target := range []string{"/", home} {
		m := lowsRemoveModel(&deploy.Deployment{TargetDir: target}, standDownOK)
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		m, _ = updateAs(t, m, cmd())
		if m.state != removeStateDone {
			t.Fatalf("target %q: state = %v, want removeStateDone", target, m.state)
		}
		s := screen(m)
		if strings.Contains(s, "rm -rf") || strings.Contains(s, removeCopy) {
			t.Errorf("target %q: the removed screen still offers a deleting command:\n%s", target, s)
		}
	}
}
