package ui

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// Owner decisions on the v0.4.3 Lows, 2026-10-21 (#236, #267). Written
// from the decisions alone, before the code that carries them.

// ---------------------------------------------------------------------
// A. The stale-volume check: Enter waits for it (#267, A-Rslice-1).
// ---------------------------------------------------------------------

// checkingInstall is an Install flow on its profile screen whose
// pre-flight has not answered. It returns the model and the message the
// pre-flight will deliver (volumes, possibly none), withheld for now.
func checkingInstall(t *testing.T, volumes ...deploy.DatabaseVolume) (InstallModel, tea.Msg) {
	t.Helper()
	m, _ := noConfirmModel(t, noConfirmDir, 80, 26, engineRunSeams{})
	m.checkVolumes = staleVolumeCheck(volumes...)
	answer := m.Init()()
	if answer == nil {
		t.Fatal("fixture: the pre-flight command produced no message")
	}
	return m, answer
}

func TestDecisionsLows_EnterBeforeTheVolumeAnswerWaitsAndSaysItIsChecking(t *testing.T) {
	m, _ := checkingInstall(t)
	m, _ = updateAs(t, m, key(tea.KeyEnter))

	if m.state == installStateNotice || m.state == installStateRunning || m.state == installStateUnavailableProfile {
		t.Errorf("Enter moved on to state %v before the check had answered", m.state)
	}
	if s := screen(m); !strings.Contains(s, "Checking") {
		t.Errorf("the waiting screen should carry a working line starting \"Checking\":\n%s", s)
	}
}

func TestDecisionsLows_AWaitingEnterThenVolumesShowsTheStaleVolumeScreen(t *testing.T) {
	m, answer := checkingInstall(t, deploy.DatabaseVolume{Name: "orbit-db-data"})
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	m, _ = updateAs(t, m, answer)

	if m.state != installStateStaleVolume {
		t.Errorf("state = %v, want the stale-volume screen once the answer arrives", m.state)
	}
	if s := screen(m); !strings.Contains(s, "orbit-db-data") || !strings.Contains(s, "Continue anyway") {
		t.Errorf("the stale-volume screen should name the volume and offer Continue anyway:\n%s", s)
	}
}

// With no volumes found the waiting Enter carries on exactly as an Enter
// pressed after the answer. A timed-out check reports no volumes too, so
// this is also what ends the 5 s wait.
func TestDecisionsLows_AWaitingEnterThenNoVolumesContinuesLikeEnterAfterTheAnswer(t *testing.T) {
	waiting, answer := checkingInstall(t)
	waiting, _ = updateAs(t, waiting, key(tea.KeyEnter))
	if waiting.state != installStateProfile || !strings.Contains(screen(waiting), "Checking") {
		t.Errorf("before the answer the flow should still wait on the profile screen, state = %v:\n%s", waiting.state, screen(waiting))
	}
	waiting, _ = updateAs(t, waiting, answer)

	answered, answer2 := checkingInstall(t)
	answered, _ = updateAs(t, answered, answer2)
	answered, _ = updateAs(t, answered, key(tea.KeyEnter))

	if answered.state != installStateNotice {
		t.Fatalf("reference: Enter after the answer reached state %v, want the notice", answered.state)
	}
	if waiting.state != answered.state {
		t.Errorf("waiting Enter then answer reached state %v, want %v", waiting.state, answered.state)
	}
	if got, want := screen(waiting), screen(answered); got != want {
		t.Errorf("screens differ.\nwaiting Enter then answer:\n%s\nEnter after the answer:\n%s", got, want)
	}
	if strings.Contains(screen(waiting), "Checking") {
		t.Errorf("the working line should be gone once the answer arrived:\n%s", screen(waiting))
	}
}

func TestDecisionsLows_EnterAfterAnNoVolumeAnswerProceedsAtOnce(t *testing.T) {
	m, answer := checkingInstall(t)
	m, _ = updateAs(t, m, answer)
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.state != installStateNotice {
		t.Errorf("state = %v, want the notice straight away", m.state)
	}
	if strings.Contains(screen(m), "Checking") {
		t.Errorf("nothing is being waited for:\n%s", screen(m))
	}
}

// ---------------------------------------------------------------------
// B. The client-ID callback note is two rows (#267, A2-Qslice-1).
// ---------------------------------------------------------------------

const callbackRow1 = "Register the redirect URI with your provider"

// callbackRows returns the trimmed text of the prompt screen's lines for
// the OIDC client ID question.
func callbackRows(origin string) []string {
	var rows []string
	for _, l := range promptWrapScreen("OIDC_CLIENT_ID", "text", origin, 80) {
		rows = append(rows, strings.Trim(l, " │|.·✦●⟡"))
	}
	return rows
}

func TestDecisionsLows_ClientIDCallbackNoteIsTwoRowsForEveryFittingAddress(t *testing.T) {
	cases := map[string]string{
		"short":          "https://orbit.example",
		"46 to 49 chars": "https://orbit-mail.example.com",
	}
	for name, origin := range cases {
		t.Run(name, func(t *testing.T) {
			address := origin + "/api/auth/callback"
			if name == "46 to 49 chars" && (len(address) < 46 || len(address) > 49) {
				t.Fatalf("fixture: address is %d chars, want 46 to 49", len(address))
			}
			rows := callbackRows(origin)
			at := -1
			for i, r := range rows {
				if r == callbackRow1 {
					at = i
					break
				}
			}
			if at < 0 {
				t.Fatalf("no row is exactly %q:\n%s", callbackRow1, strings.Join(rows, "\n"))
			}
			if at+1 >= len(rows) || rows[at+1] != address {
				next := "(none)"
				if at+1 < len(rows) {
					next = rows[at+1]
				}
				t.Errorf("the row after row 1 is %q, want the address alone: %q", next, address)
			}
			if flat := oneLine(strings.Join(rows, "\n")); strings.Contains(flat, "as the redirect URI in your provider") {
				t.Errorf("the old wording is still on the screen:\n%s", strings.Join(rows, "\n"))
			}
		})
	}
}

// ---------------------------------------------------------------------
// C. The unreadable-settings screen's advice depends on the cause
// (#236, 236-c1).
// ---------------------------------------------------------------------

const (
	adviceByAnotherAccount = "probably installed by another account"
	advicePermission1      = "Orbit here was probably installed by another account."
	advicePermission2      = "Run the launcher as that account, or make the file readable to this one."
	adviceDirectory        = ".env-orbit is a folder, but it should be a file. Rename or remove the folder, then run Install again."
	adviceWrongType        = ".env-orbit is the wrong file type. Rename or remove it, then run Install again."
	adviceTooLong          = "A line in .env-orbit is too long to be a setting, so the file may be damaged. Open it in a text editor and check it."
)

func fifoEnvDir(t *testing.T) string {
	t.Helper()
	dir := shortTempDir(t)
	if err := syscall.Mkfifo(filepath.Join(dir, deploy.EnvFile), 0o600); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}
	return dir
}

func longLineEnvDir(t *testing.T) string {
	t.Helper()
	dir := shortTempDir(t)
	body := "APP_URL=https://mail.example.test\nJUNK=" + strings.Repeat("a", 70*1024) + "\n"
	if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := deploy.Detect(dir); err == nil || d != nil {
		t.Fatalf("fixture: Detect = %v, %v; want an error and no deployment", d, err)
	}
	return dir
}

// loopEnvDir makes .env-orbit a symlink to itself, so opening it fails
// with an error that is none of the named causes.
func loopEnvDir(t *testing.T) string {
	t.Helper()
	dir := shortTempDir(t)
	if err := os.Symlink(deploy.EnvFile, filepath.Join(dir, deploy.EnvFile)); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	if d, err := deploy.Detect(dir); err == nil || d != nil {
		t.Fatalf("fixture: Detect = %v, %v; want an error and no deployment", d, err)
	}
	return dir
}

// unreadableAdvice opens Update and Remove on dir and runs check on each
// screen, flattened to running text.
func unreadableAdvice(t *testing.T, dir string, check func(t *testing.T, flat, raw string)) {
	t.Helper()
	for name, open := range map[string]func(*testing.T, AppModel) AppModel{
		"Remove": openRemove,
		"Update": func(t *testing.T, m AppModel) AppModel { return openUpdate(t, m, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			m := NewAppModelNoAnimation().WithoutVolumeCheck()
			m.targetDir = dir
			m = open(t, m)
			s := screen(m)
			assertCouldntReadScreen(t, s, dir)
			check(t, oneLine(s), s)
		})
	}
}

func wantAdvice(t *testing.T, flat, raw string, shown []string, hidden ...string) {
	t.Helper()
	for _, w := range shown {
		if !strings.Contains(flat, w) {
			t.Errorf("screen should say %q:\n%s", w, raw)
		}
	}
	for _, h := range hidden {
		if strings.Contains(flat, h) {
			t.Errorf("screen should not say %q:\n%s", h, raw)
		}
	}
}

func TestDecisionsLows_UnreadableAdviceForAFolderSaysRenameOrRemoveThenInstall(t *testing.T) {
	unreadableAdvice(t, directoryEnvDir(t), func(t *testing.T, flat, raw string) {
		wantAdvice(t, flat, raw, []string{adviceDirectory}, adviceByAnotherAccount)
	})
}

func TestDecisionsLows_UnreadableAdviceForANamedPipeSaysWrongFileType(t *testing.T) {
	unreadableAdvice(t, fifoEnvDir(t), func(t *testing.T, flat, raw string) {
		wantAdvice(t, flat, raw, []string{adviceWrongType}, adviceByAnotherAccount)
	})
}

func TestDecisionsLows_UnreadableAdviceForAnOverlongLineSaysTheFileMayBeDamaged(t *testing.T) {
	unreadableAdvice(t, longLineEnvDir(t), func(t *testing.T, flat, raw string) {
		wantAdvice(t, flat, raw, []string{adviceTooLong}, adviceByAnotherAccount)
	})
}

func TestDecisionsLows_UnreadableAdviceForPermissionDeniedIsUnchanged(t *testing.T) {
	unreadableAdvice(t, unreadableEnvDir(t), func(t *testing.T, flat, raw string) {
		wantAdvice(t, flat, raw, []string{advicePermission1, advicePermission2}, adviceDirectory, adviceWrongType, adviceTooLong)
	})
}

func TestDecisionsLows_UnreadableAdviceForAnyOtherErrorKeepsThePermissionSentences(t *testing.T) {
	unreadableAdvice(t, loopEnvDir(t), func(t *testing.T, flat, raw string) {
		wantAdvice(t, flat, raw, []string{advicePermission1, advicePermission2}, adviceDirectory, adviceWrongType, adviceTooLong)
	})
}
