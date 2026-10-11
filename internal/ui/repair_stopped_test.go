package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// #237: a safe repair or credential rotation that stops partway is not
// "Diagnosis stopped". It gets its own title, says it may have changed
// the deployment, and offers "Diagnose again" like the after-picture.

// plain is a screen with its wrapping undone, so a sentence is found
// whole wherever it broke.
func plain(m RepairModel) string { return strings.Join(strings.Fields(screen(m)), " ") }

// stoppedMutation is a model that was running a mutation (repairExecuting
// or repairRotating) whose script had started (a live stream) when the
// run ended without the summary line.
func stoppedMutation(t *testing.T, state repairState) RepairModel {
	t.Helper()
	m := sizedRepair(t)
	m.state = state
	m.stream = sleepingStream(t)
	return m
}

var mutationCases = []struct {
	name  string
	state repairState
	title string
}{
	{"safe repairs", repairExecuting, "Repairs stopped"},
	{"credential rotation", repairRotating, "Credential rotation stopped"},
}

func assertStoppedMutationScreen(t *testing.T, m RepairModel, title string) {
	t.Helper()
	s := plain(m)
	if m.state != repairError {
		t.Fatalf("state = %v, want repairError; screen:\n%s", m.state, screen(m))
	}
	for _, want := range []string{
		title,
		"exit status 4",
		"it may have changed the deployment partway",
		"backups were taken first",
		"diagnose again to see where it stands",
		"Diagnose again",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen(m))
		}
	}
	if strings.Contains(s, "Diagnosis stopped") {
		t.Errorf("a run that may have changed the deployment is titled as a failed read-only check:\n%s", screen(m))
	}
	// Rows, in order: Diagnose again, Menu, Exit; the first is selected.
	raw := screen(m)
	again, menu, exit := strings.Index(raw, "Diagnose again"), strings.LastIndex(raw, "Menu"), strings.LastIndex(raw, "Exit")
	if again < 0 || menu < again || exit < menu {
		t.Errorf("rows are not Diagnose again, Menu, Exit in that order:\n%s", raw)
	}
	if !strings.Contains(raw, "▸ Diagnose again") {
		t.Errorf("Diagnose again is not the selected row:\n%s", raw)
	}
}

func TestRepairModel_FailedRunIsTitledForWhatStopped(t *testing.T) {
	for _, tc := range mutationCases {
		t.Run(tc.name, func(t *testing.T) {
			m := stoppedMutation(t, tc.state)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})
			if m.stoppedIn != tc.state {
				t.Errorf("stoppedIn = %v, want %v", m.stoppedIn, tc.state)
			}
			assertStoppedMutationScreen(t, m, tc.title)
		})
	}
}

// The stream closing before the run reported back is the same stop.
func TestRepairModel_StreamEndedEarlyIsTitledForWhatStopped(t *testing.T) {
	for _, tc := range mutationCases {
		t.Run(tc.name, func(t *testing.T) {
			m := stoppedMutation(t, tc.state)
			m, _ = updateAs(t, m, repairStreamEndedMsg{stream: m.stream, reason: errors.New("exit status 4")})
			if m.stoppedIn != tc.state {
				t.Errorf("stoppedIn = %v, want %v", m.stoppedIn, tc.state)
			}
			assertStoppedMutationScreen(t, m, tc.title)
		})
	}
}

// "Diagnose again" on the stopped screen starts a fresh --plan run and
// forgets the old run's evidence, as it does after the after-picture.
func TestRepairModel_DiagnoseAgainAfterAStoppedRunStartsAFreshPlan(t *testing.T) {
	for _, tc := range mutationCases {
		t.Run(tc.name, func(t *testing.T) {
			m := stoppedMutation(t, tc.state)
			var modes []deploy.RepairMode
			m.prepareRepair = func(_ context.Context, _ string, mode deploy.RepairMode) (*engine.Stream, error) {
				modes = append(modes, mode)
				return nil, errors.New("stop here")
			}
			m.planActions = []engine.PlanAction{{Action: "fix-permissions", Resolves: "managed-file-permissions", Mutation: "reversible", Backup: "not-required"}}
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})

			m, cmd := updateAs(t, m, key(tea.KeyEnter)) // "Diagnose again" is first
			if m.state != repairPreparing {
				t.Fatalf("state = %v, want repairPreparing", m.state)
			}
			if m.planActions != nil || m.diagnosis != nil || m.execSummary != nil {
				t.Error("the stopped run's evidence must not carry into the fresh diagnosis")
			}
			if !strings.Contains(screen(m), "reading the deployment") {
				t.Errorf("screen:\n%s", screen(m))
			}
			if cmd == nil {
				t.Fatal("Diagnose again started nothing")
			}
			cmd()
			if len(modes) != 1 || modes[0] != deploy.RepairPlan {
				t.Fatalf("Diagnose again ran modes %v, want one --plan run", modes)
			}
		})
	}
}

// The stopped screen's Menu and Exit rows behave as on the
// after-picture (the executedMenu handling): same keys, same outcome.
func TestRepairModel_StoppedRunMenuAndExitRowsMatchTheAfterPicture(t *testing.T) {
	for _, tc := range mutationCases {
		t.Run(tc.name, func(t *testing.T) {
			stopped := stoppedMutation(t, tc.state)
			stopped, _ = repairDone(t, stopped, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})

			toMenu, _ := updateAs(t, stopped, key(tea.KeyDown))
			toMenu, _ = updateAs(t, toMenu, key(tea.KeyEnter))
			if !toMenu.Done || !toMenu.WantsMenu {
				t.Errorf("Menu row: Done=%v WantsMenu=%v, want both", toMenu.Done, toMenu.WantsMenu)
			}

			wrapped, _ := updateAs(t, stopped, key(tea.KeyUp)) // up from the top wraps to Exit
			if !strings.Contains(screen(wrapped), "▸ Exit") {
				t.Errorf("Up from the top did not wrap to Exit:\n%s", screen(wrapped))
			}
			if _, cmd := updateAs(t, wrapped, key(tea.KeyEnter)); !isQuit(cmd) {
				t.Error("Exit row did not quit")
			}
		})
	}
}

// A start failure never ran anything: it keeps today's two-row menu
// and makes no claim that the deployment changed. The title still names
// what was being started.
func TestRepairModel_MutationThatNeverStartedClaimsNoChange(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting // no stream: nothing was started
	m, _ = updateAs(t, m, repairReadyMsg{err: errors.New("fetch repair.sh: connection refused")})
	s := plain(m)
	if m.state != repairError {
		t.Fatalf("state = %v, want repairError", m.state)
	}
	if !strings.Contains(s, "Repairs stopped") || !strings.Contains(s, "fetch repair.sh: connection refused") {
		t.Errorf("screen:\n%s", screen(m))
	}
	for _, not := range []string{"Diagnosis stopped", "Diagnose again", "may have changed the deployment", "backups were taken first"} {
		if strings.Contains(s, not) {
			t.Errorf("a run that never started shows %q:\n%s", not, screen(m))
		}
	}
	if raw := screen(m); !strings.Contains(raw, "▸ Menu") || !strings.Contains(raw, "Exit") {
		t.Errorf("rows are not Menu, Exit:\n%s", raw)
	}
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if !m.Done || !m.WantsMenu {
		t.Errorf("Menu row: Done=%v WantsMenu=%v, want both", m.Done, m.WantsMenu)
	}
}

// Control (passes today): the existing check-mode failure keeps its
// title and its two rows, whatever a mutation's screen does.
func TestRepairModel_FailedCheckKeepsDiagnosisStoppedAndNoDiagnoseAgain(t *testing.T) {
	m := sizedRepair(t)
	m.stream = sleepingStream(t)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1")})
	s := plain(m)
	if m.state != repairError {
		t.Fatalf("state = %v, want repairError", m.state)
	}
	if !strings.Contains(s, "Diagnosis stopped") || !strings.Contains(s, "exit status 1") {
		t.Errorf("screen:\n%s", screen(m))
	}
	for _, not := range []string{"Repairs stopped", "Credential rotation stopped", "Diagnose again", "backups were taken first", "may have changed the deployment"} {
		if strings.Contains(s, not) {
			t.Errorf("a failed read-only check shows %q:\n%s", not, screen(m))
		}
	}
	if m.stoppedIn == repairExecuting || m.stoppedIn == repairRotating {
		t.Errorf("stoppedIn = %v for a failed check", m.stoppedIn)
	}
	// Menu is the first row there; Enter leaves for the main menu.
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if !m.Done || !m.WantsMenu {
		t.Errorf("Menu row: Done=%v WantsMenu=%v, want both", m.Done, m.WantsMenu)
	}
}
