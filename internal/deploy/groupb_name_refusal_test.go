package deploy

// v0.4.3 group B, W3 (ratified design, issue #277): a folder whose name
// holds a control character refuses every engine start that would run a
// script in it, with one sentence:
//
//	the folder's name holds a control character (\x1b), so nothing runs in it:
//	rename the folder to a plain name, then run the launcher from there
//
// The six starts are Install, Update, Repair's diagnosis, its safe and its
// dangerous repairs, and credential rotation (whose start is the dangerous
// repair, tested in the ui package). The control characters are U+0000 to
// U+001F and U+007F, shown as their escape; nothing is read from the folder
// and nothing is staged or run. U+202E alone is not refused. Remove still
// builds its command for the same folder.
//
// Written from the specification alone.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

const (
	gbStNameFirstLine  = "the folder's name holds a control character ("
	gbStNameSecondLine = "rename the folder to a plain name, then run the launcher from there"
)

// gbStControlNames are folder names (last element) holding one control
// character, with the escape the sentence must show for it.
var gbStControlNames = []struct {
	label  string
	name   string
	escape string
	onDisk bool // false where a file system cannot hold the name (NUL)
}{
	{"ESC", "ab\x1bcd", `\x1b`, true},
	{"LF", "ab\ncd", `\n`, true},
	{"CR", "ab\rcd", `\r`, true},
	{"TAB", "ab\tcd", `\t`, true},
	{"DEL", "ab\x7fcd", `\x7f`, true},
	{"SOH", "ab\x01cd", `\x01`, true},
	{"NUL", "ab\x00cd", `\x00`, false},
}

func gbStNameSentenceWant(t *testing.T, err error, escape string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error; want the control-character refusal naming %s", escape)
	}
	msg := err.Error()
	if want := gbStNameFirstLine + escape + "), so nothing runs in it:"; !strings.Contains(msg, want) {
		t.Errorf("error %q lacks %q", msg, want)
	}
	if !strings.Contains(msg, gbStNameSecondLine) {
		t.Errorf("error %q lacks %q", msg, gbStNameSecondLine)
	}
	for _, r := range strings.NewReplacer("\n", "", "\r", "", "\t", "").Replace(msg) {
		if r < 0x20 || r == 0x7f {
			t.Errorf("error %q holds the raw control character %q; it must show only the escape", msg, r)
			break
		}
	}
}

// gbStStarts are the six starts, each given a folder; the cleanup is
// called when the start returned one.
var gbStStarts = []struct {
	name  string
	start func(t *testing.T, dir string) (started bool, cleanup func() error, err error)
}{
	{"install", func(t *testing.T, dir string) (bool, func() error, error) {
		cmd, cleanup, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), dir)
		return cmd != nil, cleanup, err
	}},
	{"update", func(t *testing.T, dir string) (bool, func() error, error) {
		cmd, cleanup, err := BuildEngineCommand([]byte("#!/usr/bin/env bash\n"), dir, ActionUpdate)
		return cmd != nil, cleanup, err
	}},
	{"repair diagnosis (check)", func(t *testing.T, dir string) (bool, func() error, error) {
		cmd, err := RepairCommand(dir, RepairCheck)
		return cmd != nil, nil, err
	}},
	{"repair diagnosis (plan)", func(t *testing.T, dir string) (bool, func() error, error) {
		cmd, err := RepairCommand(dir, RepairPlan)
		return cmd != nil, nil, err
	}},
	{"safe repairs", func(t *testing.T, dir string) (bool, func() error, error) {
		cmd, err := RepairCommand(dir, RepairExecuteSafe)
		return cmd != nil, nil, err
	}},
	{"dangerous repairs (credential rotation)", func(t *testing.T, dir string) (bool, func() error, error) {
		cmd, err := RepairCommand(dir, RepairExecuteDangerous)
		return cmd != nil, nil, err
	}},
}

// Every start refuses a control-character folder name with the sentence,
// the character as its escape, no command, and nothing staged.
func TestGroupB_W3_EveryStartRefusesAControlCharacterName(t *testing.T) {
	for _, s := range gbStStarts {
		for _, n := range gbStControlNames {
			t.Run(s.name+"/"+n.label, func(t *testing.T) {
				tmp := t.TempDir()
				t.Setenv("TMPDIR", tmp)
				dir := filepath.Join(t.TempDir(), n.name)

				started, cleanup, err := s.start(t, dir)
				if cleanup != nil {
					defer cleanup()
				}
				gbStNameSentenceWant(t, err, n.escape)
				if started {
					t.Error("a command came back alongside the refusal")
				}
				if staged, _ := filepath.Glob(filepath.Join(tmp, "orbit-launcher-install-*")); len(staged) != 0 {
					t.Errorf("a refused start staged %v", staged)
				}
			})
		}
	}
}

// The name check comes before anything is read. A real deployment in a
// control-named folder, whose settings file cannot be read, still gets the
// sentence (not a read error), and one in a parent folder's name too.
func TestGroupB_W3_NothingIsReadFromTheFolderBeforeTheRefusal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("an unreadable file stays readable to root")
	}
	places := []struct {
		label string
		make  func(base string) string
	}{
		{"last name", func(base string) string { return filepath.Join(base, "ab\x1bcd") }},
		{"parent name", func(base string) string { return filepath.Join(base, "ab\x1bcd", "orbit") }},
	}
	for _, p := range places {
		for _, mode := range []RepairMode{RepairCheck, RepairPlan, RepairExecuteSafe, RepairExecuteDangerous} {
			t.Run(p.label+"/"+string(mode), func(t *testing.T) {
				dir := p.make(t.TempDir())
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Skipf("file system cannot hold this folder name: %v", err)
				}
				env := filepath.Join(dir, EnvFile)
				testutil.WriteEnvOrbit(t, env)
				scriptTree(t, dir, 0o755, testutil.File{Path: "scripts/repair.sh", Body: "#!/usr/bin/env bash\n", Mode: 0o700})
				if err := os.Chmod(env, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(env, 0o600) })

				cmd, err := RepairCommand(dir, mode)
				gbStNameSentenceWant(t, err, `\x1b`)
				if cmd != nil {
					t.Error("a command came back alongside the refusal")
				}
			})
		}
	}
}

// Even a folder another account owns, or one with no deployment in it, is
// refused for its name first: the check is the top of each start.
func TestGroupB_W3_TheNameIsRefusedBeforeAnyOtherReason(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "no\x1bdeployment") // does not exist
	for _, mode := range []RepairMode{RepairCheck, RepairPlan, RepairExecuteSafe, RepairExecuteDangerous} {
		_, err := RepairCommand(dir, mode)
		gbStNameSentenceWant(t, err, `\x1b`)
		if strings.Contains(err.Error(), "no Orbit deployment") {
			t.Errorf("%s: %q reports a missing deployment, not the name", mode, err)
		}
	}
}

// U+202E alone is not refused: the screens show it escaped instead. A
// deployment in such a folder still gets its repair command.
func TestGroupB_W3_ARightToLeftOverrideAloneIsNotRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evil\u202etxt")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("file system cannot hold this folder name: %v", err)
	}
	testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
	scriptTree(t, dir, 0o755, testutil.File{Path: "scripts/repair.sh", Body: "#!/usr/bin/env bash\n", Mode: 0o700})

	for _, mode := range []RepairMode{RepairCheck, RepairPlan, RepairExecuteSafe, RepairExecuteDangerous} {
		cmd, err := RepairCommand(dir, mode)
		if err != nil || cmd == nil {
			t.Errorf("RepairCommand(%q, %s) = %v, %v; want a command", dir, mode, cmd, err)
		}
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	cmd, cleanup, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), dir)
	if err != nil || cmd == nil {
		t.Fatalf("BuildInstallCommand: %v, %v; want a command", cmd, err)
	}
	_ = cleanup()
	cmd, cleanup, err = BuildEngineCommand([]byte("#!/usr/bin/env bash\n"), dir, ActionUpdate)
	if err != nil || cmd == nil {
		t.Fatalf("BuildEngineCommand: %v, %v; want a command", cmd, err)
	}
	_ = cleanup()
}

// Remove runs nothing in the folder, so it still builds its command (and
// the volume half) for a control-named folder, ending in the removal.
func TestGroupB_W3_RemoveStillBuildsItsCommandForTheSameFolder(t *testing.T) {
	for _, n := range gbStControlNames {
		t.Run(n.label, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), n.name)

			words, err := RemovalCommandWords(dir)
			if err != nil {
				t.Fatalf("RemovalCommandWords: %v", err)
			}
			if len(words) < 5 {
				t.Fatalf("words = %q, want the whole command", words)
			}
			if tail := words[len(words)-5 : len(words)-1]; strings.Join(tail, " ") != "&& sudo rm -rf" {
				t.Errorf("the command does not end in the folder's removal: %q", words)
			}
			if _, err := RemovalCommand(dir); err != nil {
				t.Errorf("RemovalCommand: %v", err)
			}
			if _, err := VolumeRemovalCommandWords(dir); err != nil {
				t.Errorf("VolumeRemovalCommandWords: %v", err)
			}
		})
	}
}
