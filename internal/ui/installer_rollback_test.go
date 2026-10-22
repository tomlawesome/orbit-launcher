package ui

// #261: follow the installer's rollback. Written from the issue and
// Orbit's docs/engine-events.md alone, without the code. Three behaviours:
//
//  1. An install that ends with a recovery command (action=retry, the
//     stdout line "recovery-command: <command>" just before the terminal
//     failed event, which carries recovery=command) shows the command in
//     a copy box, says the next step, and offers Install again. An install
//     that ends action=abort shows "stopped" with the whole instruction
//     block and no "Open the guided installer".
//  2. A folder holding only the pre-provisioned .env-orbit (no
//     docker-compose.yml) preselects Install, not Update; a real directory
//     named orbit-install-backups does not make a folder look installed.
//  3. The success screen shows the installer's "still starting" lines for
//     optional services.
//
// Names assumed (none is new): the screens and rows are read as text. The
// wording the tests pin is the issue's own: "Copy command", "Install
// again" (rows), "Run this in a terminal, then choose Install again".

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// rollbackFolder is the folder the fake installer's commands name; made
// up, as the contract says.
const rollbackFolder = "/srv/orbit"

const rollbackStamp = "20261020T101010Z"

// engineCommand is what a failed rollback's recovery line carries: a
// command alone on its line, every word shell-quoted.
const engineCommand = "cd " + rollbackFolder + "/orbit-install-backups/" + rollbackStamp +
	"/engine && docker compose --project-name orbit --env-file .env-orbit down --remove-orphans"

const engineCommandWithVolumes = engineCommand +
	" && docker volume rm -- orbit_orbit-db-data orbit_orbit-documents-data"

const volumeOnlyCommand = "docker volume rm -- orbit_orbit-db-data"

// --- shared drivers ----------------------------------------------------

// skyOnly are the starfield glyphs; a field that is only one of them is
// sky, not text.
var skyOnly = map[string]bool{".": true, "·": true, "✦": true, "●": true, "⟡": true}

// flat is a screen's words with the starfield taken out, joined by single
// spaces, so a sentence the screen wrapped reads as one.
func flat(s string) string {
	var words []string
	for _, w := range strings.Fields(s) {
		if skyOnly[w] || w == "▸" {
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// hasRow reports whether a screen line is exactly the row label.
func hasRow(s, label string) bool {
	for _, line := range strings.Split(s, "\n") {
		if screenRow(line) == label {
			return true
		}
	}
	return false
}

// caretRowOf is the label of the row the caret is on, whatever it is.
func caretRowOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "▸") {
			return screenRow(line)
		}
	}
	return ""
}

// caretToRow presses Down until the caret is on label.
func caretToRow[M tea.Model](t *testing.T, m M, label string) M {
	t.Helper()
	for i := 0; i < 8; i++ {
		if caretRowOf(screen(m)) == label {
			return m
		}
		m, _ = updateAs(t, m, key(tea.KeyDown))
	}
	t.Fatalf("the caret never reached %q:\n%s", label, screen(m))
	return m
}

// installApp runs Install through the app against a real fake install.sh
// (the launcher's local script override), as a person would: Install,
// Standard, the notice, then the run to its end. It returns the app and
// the queue, so a test can press on from where the run stopped.
func installApp(t *testing.T, script string, width, height int) (AppModel, *sink) {
	t.Helper()
	installScriptAt(t, script)
	queue := newSink()
	m := NewAppModelNoAnimation().WithSender(queue.Send).WithoutVolumeCheck().WithVersion("v9.9.9")
	m.targetDir = t.TempDir()
	m.flowSeams = engineRunSeams{detect: func(string) (*deploy.Deployment, error) {
		return &deploy.Deployment{TargetDir: rollbackFolder, AppURL: "https://mail.example.com"}, nil
	}}
	m.flowReadClaim = func(context.Context, string, string, time.Time) (string, error) { return "", nil }
	m.flowNoticeDuration = time.Millisecond
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: height})

	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Install
	m = drive(t, m, cmd)
	m, cmd = updateAs(t, m, key(tea.KeyEnter)) // Standard -> the notice
	m = drive(t, m, cmd)
	if s := screen(m); !strings.Contains(s, "A note before you install") {
		t.Fatalf("not on Install's notice:\n%s", s)
	}
	m, cmd = updateAs(t, m, key(tea.KeyEnd), tea.KeyPressMsg{Text: noticePhrase}, key(tea.KeyEnter))
	m = drive(t, m, cmd, queue)
	return m, queue
}

const startEvents = `echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
echo "phase=compose component=compose state=completed reason=compose-validation action=check elapsed=1s"
`

// failedScript is an installer that ends with the given terminal event
// line, the given stdout lines before it, and the given stderr lines.
func failedScript(stdoutBefore []string, terminal string, stderr []string) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n" + startEvents)
	for _, l := range stdoutBefore {
		fmt.Fprintf(&b, "printf '%%s\\n' %s\n", shQuote(l))
	}
	fmt.Fprintf(&b, "echo %s\n", shQuote(terminal))
	for _, l := range stderr {
		fmt.Fprintf(&b, "printf '%%s\\n' %s >&2\n", shQuote(l))
	}
	b.WriteString("exit 1\n")
	return b.String()
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

const (
	retryEvent = "phase=application component=application state=failed reason=application-health action=retry elapsed=3s recovery=command"
	abortEvent = "phase=application component=application state=failed reason=application-health action=abort elapsed=3s"
)

// retryStderr is the installer's stderr after a rollback that could not
// stop the containers (issue #261 item 1, verbatim).
func retryStderr(command string) []string {
	return []string{
		"Orbit installer: This install could not be fully rolled back: the containers could not be stopped.",
		"Orbit installer: Install again to finish putting the folder back. If the containers still will not stop, run this in a terminal first, then install again:",
		command,
		"Orbit installer: .env-orbit and .orbit-secrets will be put back exactly as they were before this install; anything changed in them while it ran is replaced. A copy of this folder's original files is kept in " + rollbackFolder + "/orbit-install-backups/" + rollbackStamp + "/originals.",
	}
}

func retryApp(t *testing.T, command string) (AppModel, *sink) {
	t.Helper()
	return installApp(t, failedScript(
		[]string{"recovery-command: " + command}, retryEvent, retryStderr(command)), 80, 40)
}

// --- 1. An unfinished rollback: the command in a copy box ----------------

// The command, alone, as the installer printed it: on the failed screen
// the person can read it whole and has a Copy command row.
func TestRollbackRetry_ShowsTheRecoveryCommandWithACopyRow(t *testing.T) {
	for name, command := range map[string]string{
		"containers could not be stopped":       engineCommand,
		"with the volumes this attempt made":    engineCommandWithVolumes,
		"a volume could not be removed (alone)": volumeOnlyCommand,
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := retryApp(t, command)
			s := screen(m)
			if !strings.Contains(s, "Install stopped") {
				t.Fatalf("the failed install is not shown as stopped:\n%s", s)
			}
			if !strings.Contains(flat(s), command) {
				t.Errorf("the screen does not show the recovery command whole:\nwant %q in\n%s", command, s)
			}
			if !hasRow(s, "Copy command") {
				t.Errorf("no Copy command row for the command:\n%s", s)
			}
			if strings.Contains(s, "recovery-command:") {
				t.Errorf("the protocol prefix leaked onto the screen:\n%s", s)
			}
		})
	}
}

// The command comes from the stdout line the contract adds, not from
// matching the stderr prose: with the stderr block cut away (a short
// installer message), the box still has the command.
func TestRollbackRetry_TheCommandComesFromTheStdoutLineNotTheProse(t *testing.T) {
	m, _ := installApp(t, failedScript(
		[]string{"recovery-command: " + engineCommand}, retryEvent,
		[]string{"Orbit installer: something went wrong; nothing else is said here."}), 80, 40)
	s := screen(m)
	if !strings.Contains(flat(s), engineCommand) {
		t.Errorf("the command from the recovery-command line is not shown:\n%s", s)
	}
	if !hasRow(s, "Copy command") {
		t.Errorf("no Copy command row:\n%s", s)
	}
}

// The next step is said in one short line, in the issue's words.
func TestRollbackRetry_SaysTheNextStepInOneShortLine(t *testing.T) {
	m, _ := retryApp(t, engineCommand)
	if s := screen(m); !strings.Contains(flat(s), "Run this in a terminal, then choose Install again") {
		t.Errorf("the next step is not stated:\n%s", s)
	}
}

// Install again is offered for retry, and choosing it does not leave the
// failed screen standing: the install runs again (or goes back through
// the steps that start one).
func TestRollbackRetry_OffersInstallAgainAndItStartsOver(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	script := strings.Replace(
		failedScript([]string{"recovery-command: " + engineCommand}, retryEvent, retryStderr(engineCommand)),
		startEvents, startEvents+fmt.Sprintf("echo run >> %s\n", shQuote(counter)), 1)
	m, queue := installApp(t, script, 80, 40)

	s := screen(m)
	if !hasRow(s, "Install again") {
		t.Fatalf("no Install again row after a retry ending:\n%s", s)
	}
	m = caretToRow(t, m, "Install again")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if isQuit(cmd) {
		t.Fatal("Install again quit the launcher")
	}
	m = drive(t, m, cmd, queue)

	after := screen(m)
	runs, _ := os.ReadFile(counter)
	restarted := strings.Count(string(runs), "run") >= 2 ||
		strings.Contains(after, "A note before you install") ||
		strings.Contains(after, "Choose a deployment profile")
	if !restarted {
		t.Errorf("Install again did not start another install (engine ran %d time(s)):\n%s", strings.Count(string(runs), "run"), after)
	}
}

// Choosing Copy command copies and stays on the screen, with the row
// saying so (the copied grammar the other copy rows use).
func TestRollbackRetry_CopyCommandCopiesAndStays(t *testing.T) {
	m, _ := retryApp(t, engineCommand)
	m = caretToRow(t, m, "Copy command")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if isQuit(cmd) {
		t.Fatal("Copy command quit the launcher")
	}
	m = drive(t, m, cmd)
	s := screen(m)
	if !hasRow(s, "Copy command · copied") {
		t.Errorf("the row did not say the command was copied:\n%s", s)
	}
	if !strings.Contains(s, "Install stopped") || !hasRow(s, "Install again") {
		t.Errorf("copying left the failed screen:\n%s", s)
	}
}

// A failure with no recovery command is an ordinary failure: no copy box,
// and no offer to install again on the strength of a rollback.
func TestRollbackRetry_NoRecoveryCommandNoCopyRow(t *testing.T) {
	m, _ := installApp(t, failedScript(nil,
		"phase=application component=application state=failed reason=application-health action=retry elapsed=3s",
		[]string{"Orbit installer: the application did not become ready."}), 80, 40)
	s := screen(m)
	if !strings.Contains(s, "Install stopped") {
		t.Fatalf("not the failed screen:\n%s", s)
	}
	if hasRow(s, "Copy command") {
		t.Errorf("a Copy command row with no command to copy:\n%s", s)
	}
}

// abortStderr is a whole instruction block, longer than the six lines the
// failed screen shows today: the folder could not be put back and no
// record was kept, so the installer says what to do, line by line.
func abortStderr() []string {
	return []string{
		"Orbit installer: This install could not be rolled back.",
		"Orbit installer: One entry in the folder could not be moved.",
		"Orbit installer: Its path is named below.",
		"Orbit installer: The containers and volumes were removed.",
		"Orbit installer: A copy of the original files is kept.",
		"Orbit installer: Nothing in orbit-install-backups is read or deleted.",
		"Orbit installer: Fix the cause named above.",
		"Orbit installer: Then install again.",
		"Orbit installer: Run this in a terminal:",
		"ls -ld " + rollbackFolder + "/orbit-install-backups",
	}
}

// abort: "stopped", the whole instruction block (not the last six lines),
// and no Open the guided installer, which cannot clear it.
func TestRollbackAbort_ShowsTheWholeInstructionBlockAndNoGuidedInstaller(t *testing.T) {
	m, _ := installApp(t, failedScript(nil, abortEvent, abortStderr()), 80, 40)
	s := screen(m)
	if !strings.Contains(s, "Install stopped") {
		t.Fatalf("an abort is not shown as stopped:\n%s", s)
	}
	for _, line := range abortStderr() {
		if !strings.Contains(flat(s), line) {
			t.Errorf("the instruction block is cut short: %q is missing from\n%s", line, s)
		}
	}
	if hasRow(s, "Open the guided installer") {
		t.Errorf("Open the guided installer is offered after an abort:\n%s", s)
	}
	if hasRow(s, "Install again") {
		t.Errorf("Install again is offered after an abort (no retry can clear it):\n%s", s)
	}
	for _, want := range []string{"Menu", "Exit"} {
		if !hasRow(s, want) {
			t.Errorf("no %s row to leave by:\n%s", want, s)
		}
	}
}

// The abort block is shown whole at a narrow width too, wrapped not cut.
func TestRollbackAbort_TheBlockSurvivesANarrowWindow(t *testing.T) {
	m, _ := installApp(t, failedScript(nil, abortEvent, abortStderr()), 50, 60)
	s := screen(m)
	for _, line := range abortStderr() {
		if !strings.Contains(flat(s), line) {
			t.Errorf("at 50 columns %q is missing from\n%s", line, s)
		}
	}
}

// --- 2. A pre-provisioned folder preselects Install ----------------------

func provisionedFolder(t *testing.T, withCompose bool) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	if err := os.Mkdir(filepath.Join(dir, ".orbit-secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if withCompose {
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("name: orbit\nservices: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func splashFor(t *testing.T, dir string) AppModel {
	t.Helper()
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = dir
	m = m.WithDeploymentStatus(nil)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	return m
}

// After a rollback the folder holds the pre-provisioned .env-orbit and
// .orbit-secrets and no docker-compose.yml: the engine would refuse an
// Update there, so the menu starts on Install.
func TestPreProvisioned_FolderWithoutComposeFilePreselectsInstall(t *testing.T) {
	m := splashFor(t, provisionedFolder(t, false))
	if m.splash.menu.sel != menuInstall {
		t.Errorf("selected = %d, want Install (%d) preselected for a pre-provisioned folder:\n%s", m.splash.menu.sel, menuInstall, screen(m))
	}
	if got := caretRowOf(screen(m)); got != "Install" {
		t.Errorf("the caret is on %q, want Install", got)
	}
}

// ...and Enter on it goes to the install flow, not the update one.
func TestPreProvisioned_EnterGoesToTheInstallFlow(t *testing.T) {
	m := splashFor(t, provisionedFolder(t, false))
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateInstall {
		t.Errorf("Enter went to state %v, want the install flow:\n%s", m.state, screen(m))
	}
}

// With the compose file there, it is an installed deployment: Update.
func TestPreProvisioned_FolderWithComposeFileStillPreselectsUpdate(t *testing.T) {
	m := splashFor(t, provisionedFolder(t, true))
	if m.splash.menu.sel != menuUpdate {
		t.Errorf("selected = %d, want Update (%d) for an installed deployment", m.splash.menu.sel, menuUpdate)
	}
}

// The installer's rollback leftovers sit in orbit-install-backups, a real
// directory: they neither make an empty folder look installed nor stop a
// pre-provisioned one preselecting Install.
func TestPreProvisioned_BackupsDirectoryIsIgnored(t *testing.T) {
	t.Run("pre-provisioned folder with leftovers", func(t *testing.T) {
		dir := provisionedFolder(t, false)
		leftovers := filepath.Join(dir, "orbit-install-backups", rollbackStamp, "originals")
		if err := os.MkdirAll(leftovers, 0o700); err != nil {
			t.Fatal(err)
		}
		m := splashFor(t, dir)
		if m.splash.menu.sel != menuInstall {
			t.Errorf("selected = %d, want Install (%d):\n%s", m.splash.menu.sel, menuInstall, screen(m))
		}
	})
	t.Run("empty folder with only leftovers", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "orbit-install-backups", rollbackStamp, "moved"), 0o700); err != nil {
			t.Fatal(err)
		}
		if d, err := deploy.Detect(dir); err != nil || d != nil {
			t.Fatalf("Detect = %v, %v; a folder with only leftovers holds no deployment", d, err)
		}
		m := splashFor(t, dir)
		if m.splash.menu.sel != menuInstall || m.splash.state != stateDormant {
			t.Errorf("selected = %d, state = %v; want Install and dormant", m.splash.menu.sel, m.splash.state)
		}
	})
	t.Run("an installed deployment with leftovers is still installed", func(t *testing.T) {
		dir := provisionedFolder(t, true)
		if err := os.MkdirAll(filepath.Join(dir, "orbit-install-backups", rollbackStamp), 0o700); err != nil {
			t.Fatal(err)
		}
		if m := splashFor(t, dir); m.splash.menu.sel != menuUpdate {
			t.Errorf("selected = %d, want Update (%d)", m.splash.menu.sel, menuUpdate)
		}
	})
}

// The same through Detect itself: the leftovers directory is not a reason
// to read the folder as anything, and a deployment beside it is read as
// before. (A symlink or a file of that name is not ignored; the installer
// refuses it, and nothing here treats it as leftovers.)
func TestPreProvisioned_DetectReadsADeploymentBesideTheBackupsDirectory(t *testing.T) {
	dir := provisionedFolder(t, true)
	if err := os.MkdirAll(filepath.Join(dir, "orbit-install-backups", rollbackStamp, "engine"), 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := deploy.Detect(dir)
	if err != nil || d == nil {
		t.Fatalf("Detect = %v, %v; want the deployment", d, err)
	}
	if d.AppURL != "https://mail.example.com" {
		t.Errorf("AppURL = %q, want the deployment's address", d.AppURL)
	}
}

// --- 3. "Still starting" lines on the success screen ---------------------

const optionalScript = `#!/usr/bin/env bash
` + startEvents + `echo "phase=application component=application state=healthy reason=application-health action=health elapsed=2s"
echo "phase=optional component=clamav state=waiting reason=optional-status action=continue elapsed=5s"
echo "phase=optional component=tika state=waiting reason=optional-status action=continue elapsed=6s"
echo "phase=optional component=ollama state=waiting reason=optional-status action=continue elapsed=7s"
echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=8s"
echo
echo "Orbit is ready."
echo "Public URL: https://mail.example.com"
echo "Version: v1.2.3"
echo "Optional profiles: full"
echo "Scanner: still starting (first signature download); uploads are refused until it is ready."
echo "Document processing: stopped (exit 2); run Repair."
echo "Local model service: not running (its container is missing); run Repair."
echo "Status: docker compose --env-file .env-orbit ps"
echo 'Export a recovery bundle now: run "bash scripts/backup.sh" then "bash scripts/export-recovery-bundle.sh <backup.tar>" in the deployment directory.'
exit 0
`

var optionalLines = []string{
	"Scanner: still starting (first signature download); uploads are refused until it is ready.",
	"Document processing: stopped (exit 2); run Repair.",
	"Local model service: not running (its container is missing); run Repair.",
}

// An optional service still starting or stopped no longer fails the
// install; the success screen carries the installer's line for each.
func TestOptionalServices_SuccessScreenShowsTheStillStartingLines(t *testing.T) {
	for _, width := range []int{80, 50} {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			m, _ := installApp(t, optionalScript, width, 40)
			if m.state != appStateSuccess {
				t.Fatalf("a run ending waiting/optional-status/continue did not reach the success screen (state %v):\n%s", m.state, screen(m))
			}
			s := screen(m)
			for _, line := range optionalLines {
				if !strings.Contains(flat(s), line) {
					t.Errorf("the success screen lacks the installer's line %q:\n%s", line, s)
				}
			}
			for _, want := range []string{"Get into Orbit", "https://mail.example.com"} {
				if !strings.Contains(s, want) {
					t.Errorf("the success screen lost %q:\n%s", want, s)
				}
			}
		})
	}
}

// Only the service lines are added; the rest of the completion text stays
// off the success screen, which has its own foot.
func TestOptionalServices_OtherCompletionLinesStayOff(t *testing.T) {
	m, _ := installApp(t, optionalScript, 80, 40)
	s := flat(screen(m))
	for _, noise := range []string{"Export a recovery bundle", "Optional profiles", "docker compose --env-file"} {
		if strings.Contains(s, noise) {
			t.Errorf("the success screen shows completion text %q that is not a service line:\n%s", noise, screen(m))
		}
	}
}

// An install with every optional service ready shows nothing extra.
func TestOptionalServices_NoneWaitingShowsNoStillStartingLine(t *testing.T) {
	m, _ := installApp(t, "#!/usr/bin/env bash\n"+startEvents+`echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=3s"
echo "Orbit is ready."
echo "Public URL: https://mail.example.com"
exit 0
`, 80, 40)
	if m.state != appStateSuccess {
		t.Fatalf("state %v, want the success screen:\n%s", m.state, screen(m))
	}
	for _, word := range []string{"still starting", "Scanner", "stopped (exit"} {
		if strings.Contains(screen(m), word) {
			t.Errorf("the success screen mentions %q though no optional service was waiting:\n%s", word, screen(m))
		}
	}
}
