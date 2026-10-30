package main

// Pins #291 (the launcher refuses to run as root; owner decision 13a,
// 2026-10-23). When the effective user is root (euid 0) the launcher
// refuses before drawing any screen: a short message on stderr, a
// non-zero exit, and nothing read or run (the app is never started).
//
//   - under sudo (SUDO_USER set and not "root"): "...Run it again
//     without sudo, as the user who'll look after Orbit...";
//   - any other root login: "...Run it again as your own user, the one
//     who'll look after Orbit...";
//   - --version still works as root (it reads and runs nothing);
//   - a normal user is unchanged.
//
// SEAM: the main package has none today, so these tests name one, the
// package variable `effectiveUID` (a func() int, defaulting to
// os.Geteuid), the same shape as the deploy package's launcherEUID. The
// effective user is injected through it rather than needing a real root
// run. Until it exists this file does not compile.

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/release"
)

const (
	groupBSudoMessage = "orbit-launcher doesn't run as root. Run it again without sudo, as the user who'll look after Orbit. That user needs to be able to use Docker."
	groupBRootMessage = "orbit-launcher doesn't run as root. Run it again as your own user, the one who'll look after Orbit. That user needs to be able to use Docker."
)

// groupBRunAs runs the launcher with the given effective user and
// SUDO_USER (sudoUser == "<unset>" removes it), returning the exit code,
// stdout, stderr and whether the app was started.
func groupBRunAs(t *testing.T, euid int, sudoUser string, args ...string) (code int, stdout, stderr string, started bool) {
	t.Helper()
	saved := effectiveUID
	effectiveUID = func() int { return euid }
	t.Cleanup(func() { effectiveUID = saved })

	prev, had := os.LookupEnv("SUDO_USER")
	t.Cleanup(func() {
		if had {
			os.Setenv("SUDO_USER", prev)
		} else {
			os.Unsetenv("SUDO_USER")
		}
	})
	if sudoUser == "<unset>" {
		os.Unsetenv("SUDO_USER")
	} else {
		os.Setenv("SUDO_USER", sudoUser)
	}

	stub := func(stdout, stderr io.Writer) int {
		started = true
		return 0
	}
	var out, errb bytes.Buffer
	code = run(args, &out, &errb, stub)
	return code, out.String(), errb.String(), started
}

func TestGroupB_Root_RefusedUnderSudo(t *testing.T) {
	code, stdout, stderr, started := groupBRunAs(t, 0, "tom")
	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	if started {
		t.Error("the app was started as root")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if got := strings.TrimSpace(stderr); got != groupBSudoMessage {
		t.Errorf("stderr = %q, want %q", got, groupBSudoMessage)
	}
}

func TestGroupB_Root_RefusedWithoutSudo(t *testing.T) {
	for name, sudoUser := range map[string]string{
		"SUDO_USER unset": "<unset>",
		"SUDO_USER empty": "",
		"SUDO_USER root":  "root",
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr, started := groupBRunAs(t, 0, sudoUser)
			if code == 0 {
				t.Error("exit code = 0, want non-zero")
			}
			if started {
				t.Error("the app was started as root")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if got := strings.TrimSpace(stderr); got != groupBRootMessage {
				t.Errorf("stderr = %q, want %q", got, groupBRootMessage)
			}
		})
	}
}

// Whatever the arguments, root never reaches the app (there is no
// --help short-circuit today, and this issue adds none).
func TestGroupB_Root_RefusedWhateverTheArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"something"}} {
		code, _, stderr, started := groupBRunAs(t, 0, "<unset>", args...)
		if code == 0 || started || !strings.Contains(stderr, "doesn't run as root") {
			t.Errorf("args %q as root: code=%d started=%v stderr=%q, want a refusal", args, code, started, stderr)
		}
	}
}

func TestGroupB_Root_NormalUserIsUnchanged(t *testing.T) {
	for _, sudoUser := range []string{"<unset>", "tom"} {
		code, stdout, stderr, started := groupBRunAs(t, 1000, sudoUser)
		if !started {
			t.Errorf("SUDO_USER=%q: a normal user did not reach the app", sudoUser)
		}
		if code != 0 {
			t.Errorf("SUDO_USER=%q: exit code = %d, want the app's 0", sudoUser, code)
		}
		if stdout != "" || stderr != "" {
			t.Errorf("SUDO_USER=%q: stdout=%q stderr=%q, want both empty", sudoUser, stdout, stderr)
		}
	}
}

func TestGroupB_Root_VersionStillWorksAsRoot(t *testing.T) {
	oldVersion, oldRevision := release.Version, release.Revision
	t.Cleanup(func() { release.Version, release.Revision = oldVersion, oldRevision })
	release.Version, release.Revision = "1.2.3", "abc"

	for _, sudoUser := range []string{"<unset>", "tom"} {
		code, stdout, stderr, started := groupBRunAs(t, 0, sudoUser, "--version")
		if code != 0 {
			t.Errorf("SUDO_USER=%q: --version as root: exit code = %d, want 0", sudoUser, code)
		}
		if started {
			t.Errorf("SUDO_USER=%q: --version started the app", sudoUser)
		}
		if want := "orbit-launcher v1.2.3 (abc)\n"; stdout != want {
			t.Errorf("SUDO_USER=%q: stdout = %q, want %q", sudoUser, stdout, want)
		}
		if stderr != "" {
			t.Errorf("SUDO_USER=%q: stderr = %q, want empty", sudoUser, stderr)
		}
	}
}
