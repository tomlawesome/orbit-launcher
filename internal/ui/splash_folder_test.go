package ui

// #316: the splash's identity block ends with the folder row, the folder
// the launcher was started in, in every state. Written from the design
// note (issue note 46624, section 1) before the code.
//
// The tests drive the app the way main does (a folder, a health probe,
// Init) and read the screen; they reach no field of the splash.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// recordedImage is the ORBIT_IMAGE testutil.EnvOrbit records.
const recordedImage = "ghcr.io/tomlawesome/orbit@sha256:abc"

// installedFolder is a short folder holding an installed deployment: the
// record (testutil.EnvOrbit, plus extra lines) and a Compose file.
func installedFolder(t *testing.T, extra ...string) string {
	t.Helper()
	dir := shortTempDir(t)
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile), extra...)
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))
	if d, err := deploy.Detect(dir); err != nil || d == nil || d.Image != recordedImage {
		t.Fatalf("setup: the folder does not read back as the record: %+v, %v", d, err)
	}
	return dir
}

// appOpt is one more seam given to folderApp before the deployment is
// applied.
type appOpt func(AppModel) AppModel

// runningAs is a running reader that always answers app and err.
func runningAs(app deploy.RunningApp, err error) appOpt {
	return func(m AppModel) AppModel {
		return m.WithRunningReader(func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
			return app, err
		})
	}
}

// reading is a running reader the test supplies.
func reading(read func(context.Context, *deploy.Deployment) (deploy.RunningApp, error)) appOpt {
	return func(m AppModel) AppModel { return m.WithRunningReader(read) }
}

// folderApp is the app on its splash, started in dir, whose health probe
// answers health, sized to width x height. No Docker is asked anywhere
// but through the opts.
func folderApp(t *testing.T, dir string, width, height int, health deploy.HealthReason, opts ...appOpt) AppModel {
	t.Helper()
	m := NewAppModelNoAnimation().WithoutVolumeCheck().WithServicesReader(nil)
	m.targetDir = dir
	for _, o := range opts {
		m = o(m)
	}
	return sized(t, m.WithDeploymentStatus(func(context.Context, string) deploy.HealthReason { return health }), width, height)
}

// sized gives the app its terminal.
func sized(t *testing.T, m AppModel, width, height int) AppModel {
	t.Helper()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// started runs the splash's start-up commands to the end, as the program
// would: the health probe and the running check answer.
func started(t *testing.T, m AppModel) AppModel {
	t.Helper()
	return drive(t, m, m.Init())
}

// rawRowOf is the row of the model's view (colour escapes kept) holding
// sub.
func rawRowOf(t *testing.T, m tea.Model, sub string) string {
	t.Helper()
	for _, l := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no row with %q:\n%s", sub, screen(m))
	return ""
}

// textOf is a screen row's text: the padding and the starfield taken off
// both ends. A full stop is not taken off, so a sentence keeps its own.
func textOf(row string) string { return strings.Trim(row, " ·✦●*+▸⟡") }

// The folder row sits directly beneath the status word, in the faint
// style, and says the whole folder with no ~.
func TestSplashFolder_RowSitsBeneathTheStatusWord(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))

	s := screen(m)
	fqdn, word, folder := lineIndex(s, "mail.example.com"), lineIndex(s, "alive"), lineIndex(s, dir)
	if fqdn < 0 || word != fqdn+1 || folder != word+1 {
		t.Fatalf("rows: address %d, status word %d, folder %d, want three rows in a row:\n%s", fqdn, word, folder, s)
	}
	if got := textOf(rowsOf(s)[folder]); got != dir {
		t.Errorf("the folder row says %q, want %q and nothing before or after it", got, dir)
	}
	if blank := textOf(rowsOf(s)[folder+1]); blank != "" {
		t.Errorf("the row after the folder row is %q, want the blank row above the menu:\n%s", blank, s)
	}
	if row := rawRowOf(t, m, dir); !strings.Contains(row, style.Tagline.Render(dir)) {
		t.Errorf("the folder row is not in the faint style (style.Tagline): %q", row)
	}
}

// The folder is the operator's next act to compare, and ~ means a
// different folder for each account: the path is never shortened.
func TestSplashFolder_NeverShortensTheFolderWithATilde(t *testing.T) {
	home := shortTempDir(t)
	dir := filepath.Join(home, "orbit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))
	t.Setenv("HOME", home)

	s := screen(started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding)))
	if !strings.Contains(s, dir) || strings.Contains(s, "~") {
		t.Errorf("the folder is under HOME and must still read in full (%s), with no ~:\n%s", dir, s)
	}
}

// Dormant shows it too: it is the folder Install will write.
func TestSplashFolder_DormantShowsIt(t *testing.T) {
	dir := shortTempDir(t)
	m := folderApp(t, dir, 80, 26, deploy.HealthResponding)

	s := screen(m)
	word, folder := lineIndex(s, "dormant"), lineIndex(s, dir)
	if word < 0 || folder != word+1 {
		t.Fatalf("dormant on row %d, folder on row %d, want the folder directly beneath:\n%s", word, folder, s)
	}
	if got := textOf(rowsOf(s)[folder]); got != dir {
		t.Errorf("the folder row says %q, want %q", got, dir)
	}
}

// An unreadable record still has a folder.
func TestSplashFolder_UnreadableShowsIt(t *testing.T) {
	dir := unreadableEnvDir(t)
	m := folderApp(t, dir, 80, 26, deploy.HealthResponding)

	s := screen(m)
	word, folder := lineIndex(s, "can't read "+deploy.EnvFile), lineIndex(s, dir)
	if word < 0 || folder != word+1 {
		t.Fatalf("the unreadable word on row %d, folder on row %d, want the folder directly beneath:\n%s", word, folder, s)
	}
}

// A degraded splash shows it beneath the drawer when the drawer is open.
func TestSplashFolder_DegradedShowsItBeneathTheDrawer(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthNotResponding))
	m, _ = updateAs(t, m, key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyUp)) // Install, then the status row
	m, _ = updateAs(t, m, key(tea.KeyRight))                              // open the drawer

	s := screen(m)
	word, reason, folder := lineIndex(s, "degraded"), lineIndex(s, "not responding"), lineIndex(s, dir)
	if word < 0 || reason <= word || folder != reason+1 {
		t.Fatalf("status word %d, reason %d, folder %d, want the folder directly beneath the open drawer:\n%s", word, reason, folder, s)
	}
}

// The folder row is one row, never wrapped: wider than width-2 cells it
// is cut from the start, so the last components, which tell two
// deployment folders apart, always read.
func TestSplashFolder_LongPathIsCutFromTheStart(t *testing.T) {
	parts := []string{strings.Repeat("a", 30), strings.Repeat("b", 30), strings.Repeat("c", 30), "deployments", "production"}
	dir := filepath.Join(append([]string{shortTempDir(t)}, parts...)...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))

	for _, width := range []int{80, 60} {
		m := started(t, folderApp(t, dir, width, 26, deploy.HealthResponding))
		s := screen(m)
		if strings.Contains(s, dir) {
			t.Fatalf("width %d: the whole path (%d cells) is on a screen %d wide:\n%s", width, len(dir), width, s)
		}
		var rows []string
		for _, r := range rowsOf(s) {
			if strings.Contains(r, "production") {
				rows = append(rows, textOf(r))
			}
		}
		if len(rows) != 1 {
			t.Fatalf("width %d: the folder is on %d rows (%q), want one:\n%s", width, len(rows), rows, s)
		}
		row := rows[0]
		if !strings.HasPrefix(row, "…") || !strings.HasSuffix(row, "/deployments/production") {
			t.Errorf("width %d: the row is %q, want it to start with … and end /deployments/production", width, row)
		}
		if !strings.HasSuffix(dir, strings.TrimPrefix(row, "…")) {
			t.Errorf("width %d: the row %q is not the end of %q", width, row, dir)
		}
		if got := len([]rune(row)); got != width-2 {
			t.Errorf("width %d: the row is %d cells, want width-2 = %d", width, got, width-2)
		}
	}
}

// The folder is untrusted text: a control character in its name shows as
// an escape, and a line break cannot forge a row.
func TestSplashFolder_ControlRunesShowAsEscapes(t *testing.T) {
	t.Run("escape sequence", func(t *testing.T) {
		dir := filepath.Join(shortTempDir(t), "a\x1b[999mZZ")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		m := folderApp(t, dir, 80, 26, deploy.HealthResponding)

		if raw := m.View().Content; strings.Contains(raw, "\x1b[999m") {
			t.Errorf("the folder's escape sequence reached the terminal: %q", raw)
		}
		if s := screen(m); !strings.Contains(s, `a\x1b[999mZZ`) {
			t.Errorf("the escape does not show as \\x1b:\n%s", s)
		}
	})
	t.Run("line break", func(t *testing.T) {
		dir := filepath.Join(shortTempDir(t), "evil\nFORGEDROW")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		s := screen(folderApp(t, dir, 80, 26, deploy.HealthResponding))

		for _, row := range rowsOf(s) {
			if textOf(row) == "FORGEDROW" {
				t.Errorf("a line break in the folder name made a row of its own:\n%s", s)
			}
		}
		if !strings.Contains(s, `evil\nFORGEDROW`) {
			t.Errorf("the line break does not show as \\n:\n%s", s)
		}
	})
}
