package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #208 (#199 WI-4, EN-7). Finding a deployment's script is one rule,
// whichever script it is: absent is the caller's sentinel ("not
// available"), any other failure to look is an error naming the script,
// and a script that is there must pass the trust check before anything
// runs it. Repair (scripts/repair.sh) and configuration
// (scripts/configure.sh) used near-identical copies of that rule.

// lookupOutcome is how a script lookup ended.
type lookupOutcome string

const (
	lookupFound     lookupOutcome = "found"
	lookupSentinel  lookupOutcome = "absent (the caller's sentinel)"
	lookupUntrusted lookupOutcome = "refused as untrusted"
	lookupOther     lookupOutcome = "another error"
)

// scriptLookup is one caller of the lookup: which script it wants, its
// sentinel for an absent one, and how to run the lookup in a directory.
type scriptLookup struct {
	script   string
	sentinel error
	run      func(t *testing.T, dir string) error
}

var scriptLookups = []scriptLookup{
	{
		script:   "repair.sh",
		sentinel: ErrRepairUnavailable,
		run: func(t *testing.T, dir string) error {
			cmd, err := RepairCommand(dir, RepairCheck)
			if (cmd == nil) == (err == nil) {
				t.Errorf("RepairCommand returned cmd=%v with err=%v; want exactly one", cmd, err)
			}
			return err
		},
	},
	{
		script:   "configure.sh",
		sentinel: ErrNoConfigTree,
		run: func(t *testing.T, dir string) error {
			endSession, err := OpenConfigTree(dir)
			if err == nil {
				if endSession == nil {
					t.Error("OpenConfigTree succeeded with no endSession")
				} else {
					endSession()
				}
			}
			return err
		},
	},
}

func classifyLookup(err error, sentinel error) lookupOutcome {
	var untrusted *UntrustedPathError
	switch {
	case err == nil:
		return lookupFound
	case errors.Is(err, sentinel):
		return lookupSentinel
	case errors.As(err, &untrusted):
		return lookupUntrusted
	default:
		return lookupOther
	}
}

// scriptShape builds one arrangement of dir/scripts/<script>, with modes
// set exactly rather than through the umask.
type scriptShape struct {
	name  string
	build func(t *testing.T, dir, script string)
	// want is the outcome the decisions fix for this shape; "" leaves it
	// open and pins only that both lookups agree.
	want lookupOutcome
}

func mkdirExact(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatal(err)
	}
	chmod(t, path, mode)
}

func writeExact(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n"), mode); err != nil {
		t.Fatal(err)
	}
	chmod(t, path, mode)
}

var scriptShapes = []scriptShape{
	{
		name: "trusted script",
		build: func(t *testing.T, dir, script string) {
			mkdirExact(t, filepath.Join(dir, "scripts"), 0o755)
			writeExact(t, filepath.Join(dir, "scripts", script), 0o644)
		},
		want: lookupFound,
	},
	{
		name:  "no scripts directory",
		build: func(t *testing.T, dir, script string) {},
		want:  lookupSentinel,
	},
	{
		name:  "scripts directory without the script",
		build: func(t *testing.T, dir, script string) { mkdirExact(t, filepath.Join(dir, "scripts"), 0o755) },
		want:  lookupSentinel,
	},
	{
		name: "world-writable script",
		build: func(t *testing.T, dir, script string) {
			mkdirExact(t, filepath.Join(dir, "scripts"), 0o755)
			writeExact(t, filepath.Join(dir, "scripts", script), 0o666)
		},
		want: lookupUntrusted,
	},
	{
		name: "scripts is a file",
		build: func(t *testing.T, dir, script string) {
			writeExact(t, filepath.Join(dir, "scripts"), 0o644)
		},
	},
	{
		name: "script is a directory",
		build: func(t *testing.T, dir, script string) {
			mkdirExact(t, filepath.Join(dir, "scripts"), 0o755)
			mkdirExact(t, filepath.Join(dir, "scripts", script), 0o755)
		},
	},
	{
		name: "script is a symlink to a trusted file",
		build: func(t *testing.T, dir, script string) {
			mkdirExact(t, filepath.Join(dir, "scripts"), 0o755)
			writeExact(t, filepath.Join(dir, "scripts", "real.sh"), 0o644)
			if err := os.Symlink("real.sh", filepath.Join(dir, "scripts", script)); err != nil {
				t.Fatal(err)
			}
		},
	},
	{
		name: "script is a dangling symlink",
		build: func(t *testing.T, dir, script string) {
			mkdirExact(t, filepath.Join(dir, "scripts"), 0o755)
			if err := os.Symlink("gone.sh", filepath.Join(dir, "scripts", script)); err != nil {
				t.Fatal(err)
			}
		},
	},
	{
		name: "scripts is a symlink to a trusted directory",
		build: func(t *testing.T, dir, script string) {
			real := filepath.Join(dir, "real-scripts")
			mkdirExact(t, real, 0o755)
			writeExact(t, filepath.Join(real, script), 0o644)
			if err := os.Symlink("real-scripts", filepath.Join(dir, "scripts")); err != nil {
				t.Fatal(err)
			}
		},
	},
}

// newLookupDir is a trusted, exactly-moded directory to arrange a shape in.
func newLookupDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "d")
	mkdirExact(t, dir, 0o755)
	return dir
}

// EN-7: the repair and configure lookups are one rule, so every shape of
// scripts/<name> ends the same way for both.
func TestScriptLookup_RepairAndConfigureEndTheSameWay(t *testing.T) {
	for _, shape := range scriptShapes {
		t.Run(shape.name, func(t *testing.T) {
			outcomes := make([]lookupOutcome, len(scriptLookups))
			for i, l := range scriptLookups {
				dir := newLookupDir(t)
				shape.build(t, dir, l.script)
				err := l.run(t, dir)
				outcomes[i] = classifyLookup(err, l.sentinel)
				t.Logf("%s: %s (err = %v)", l.script, outcomes[i], err)
				if shape.want != "" && outcomes[i] != shape.want {
					t.Errorf("%s: %s, want %s (err = %v)", l.script, outcomes[i], shape.want, err)
				}
			}
			for i := 1; i < len(outcomes); i++ {
				if outcomes[i] != outcomes[0] {
					t.Errorf("the lookups disagree: %s is %s, %s is %s",
						scriptLookups[0].script, outcomes[0], scriptLookups[i].script, outcomes[i])
				}
			}
		})
	}
}

// EN-7: "absent → sentinel, else wrap": a lookup that fails for a reason
// other than absence or distrust names the script it was looking for, so
// the failure screen says which file is wrong. (A trust refusal is worded
// by trusted.go, which this change does not touch.)
func TestScriptLookup_AFailureToLookNamesTheScript(t *testing.T) {
	for _, shape := range scriptShapes {
		for _, l := range scriptLookups {
			dir := newLookupDir(t)
			shape.build(t, dir, l.script)
			err := l.run(t, dir)
			switch classifyLookup(err, l.sentinel) {
			case lookupOther:
				if !strings.Contains(err.Error(), l.script) {
					t.Errorf("%s / %s: err = %q, want it to name %s", shape.name, l.script, err, l.script)
				}
			}
		}
	}
}

// A lookup never creates what it looks for: an absent script leaves the
// directory exactly as it was, for both callers.
func TestScriptLookup_AbsentScriptWritesNothing(t *testing.T) {
	for _, l := range scriptLookups {
		dir := newLookupDir(t)
		if err := l.run(t, dir); !errors.Is(err, l.sentinel) {
			t.Fatalf("%s: err = %v, want its sentinel", l.script, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("%s: looking for an absent script left %v behind", l.script, names)
		}
	}
}
