package ui

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// For the root or home folder the launcher will not offer to delete the
// folder, but it still offers the data-volume half of the command, so
// the mail and documents can be removed.

const volumesOnlyHome = "/home/someone"

// volumesOnlyTargets are the two folders whose deletion is refused.
func volumesOnlyTargets(t *testing.T) []string {
	t.Helper()
	t.Setenv("HOME", volumesOnlyHome)
	return []string{"/", volumesOnlyHome}
}

// doneFor runs Remove for target through the stand-down to the done
// screen, at 80x24.
func doneFor(t *testing.T, target string, clip *bytes.Buffer) RemoveModel {
	t.Helper()
	m := lowsRemoveModel(&deploy.Deployment{TargetDir: target}, standDownOK)
	if clip != nil {
		m.clipboard = clip
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatalf("target %q: confirming started nothing", target)
	}
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateDone {
		t.Fatalf("target %q: state = %v, want removeStateDone", target, m.state)
	}
	return m
}

func TestRemove_DoneScreenOffersOnlyTheVolumeCommandForRootOrHome(t *testing.T) {
	for _, target := range volumesOnlyTargets(t) {
		s := screen(doneFor(t, target, nil))
		flat := proseOnly(s)
		if !strings.Contains(s, "down -v") {
			t.Errorf("target %q: no volume-removal command on the screen:\n%s", target, s)
		}
		if !strings.Contains(s, removeCopy) {
			t.Errorf("target %q: no %q row:\n%s", target, removeCopy, s)
		}
		if strings.Contains(s, "rm -rf") {
			t.Errorf("target %q: the screen offers to delete the folder:\n%s", target, s)
		}
		if !strings.Contains(flat, "The folder itself is not removed: it is your home folder, not a folder of Orbit's own.") &&
			!strings.Contains(flat, "the root folder") {
			t.Errorf("target %q: the screen does not say why the folder stays:\n%s", target, s)
		}
		if !strings.Contains(flat, "it cannot be undone") {
			t.Errorf("target %q: the screen lost its warning:\n%s", target, s)
		}
	}
}

func TestRemove_CopyOnTheRefusedScreenCopiesTheVolumeCommand(t *testing.T) {
	for _, target := range volumesOnlyTargets(t) {
		var buf bytes.Buffer
		m := doneFor(t, target, &buf)
		m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Copy command is the default row
		if cmd == nil {
			t.Fatalf("target %q: Enter on the default row ran nothing (outcome %v)", target, m.Outcome())
		}
		cmd()
		if !m.copied {
			t.Errorf("target %q: copied is false after Copy command", target)
		}

		const prefix, suffix = "\x1b]52;c;", "\x07"
		got := buf.String()
		if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
			t.Fatalf("target %q: clipboard write = %q, want an OSC 52 sequence", target, got)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
		if err != nil {
			t.Fatalf("target %q: payload is not standard base64: %v", target, err)
		}
		words, err := deploy.VolumeRemovalCommandWords(target)
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.Join(words, " "); string(decoded) != want {
			t.Errorf("target %q: copied %q, want %q", target, decoded, want)
		}
	}
}

func TestRemove_RefusedScreenMenuReadsCopyMenuExit(t *testing.T) {
	want := []string{"Copy command", "Menu", "Exit"}
	for _, target := range volumesOnlyTargets(t) {
		s := screen(doneFor(t, target, nil))
		if got := screenLabels(s); !reflect.DeepEqual(got, want) {
			t.Errorf("target %q: rows = %q, want %q:\n%s", target, got, want, s)
		}
	}
}

// Wording pins (owner decisions, 2026-10-22).

func TestRemove_OrdinaryFolderWarningNamesEverythingInTheFolder(t *testing.T) {
	m := newTestRemoveModel(standDownOK)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateDone {
		t.Fatalf("state = %v, want removeStateDone", m.state)
	}
	const want = "this deletes all mail, documents and configuration, and everything else in the folder, backups included — it cannot be undone"
	if flat := proseOnly(screen(m)); !strings.Contains(flat, want) {
		t.Errorf("the removed screen lacks %q:\n%s", want, screen(m))
	}
}

func TestRemove_RefusedScreenWording(t *testing.T) {
	const stays = "Orbit's files there stay for you to remove."
	for _, c := range []struct{ target, why string }{
		{"/", "The folder itself is not removed: it is the root folder"},
		{volumesOnlyHome, "The folder itself is not removed: it is your home folder, not a folder of Orbit's own."},
	} {
		t.Setenv("HOME", volumesOnlyHome)
		s := screen(doneFor(t, c.target, nil))
		flat := proseOnly(s)
		for _, want := range []string{
			"to remove Orbit's data volumes from this machine, copy and run",
			"this deletes all mail and documents — it cannot be undone",
			c.why,
			stays,
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("target %q: the screen lacks %q:\n%s", c.target, want, s)
			}
		}
	}
}

// Under sudo or as root, HOME names another account's folder. The
// account's own home, as the account database records it, still gets
// the volumes-only screen.
func TestRemove_DoneScreenForTheOwnersHomeWhateverHOMESays(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot look up the current account: %v", err)
	}
	if u.HomeDir == "" {
		t.Skip("the account database records no home folder")
	}
	info, err := os.Stat(u.HomeDir)
	if err != nil {
		t.Skipf("cannot stat the home folder: %v", err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		t.Skipf("home folder %q is not owned by the running account", u.HomeDir)
	}
	t.Setenv("HOME", filepath.Join(t.TempDir(), "root"))
	s := screen(doneFor(t, u.HomeDir, nil))
	flat := proseOnly(s)
	if !strings.Contains(s, "down -v") {
		t.Errorf("no volume-removal command on the screen:\n%s", s)
	}
	if !strings.Contains(s, removeCopy) {
		t.Errorf("no %q row:\n%s", removeCopy, s)
	}
	if strings.Contains(s, "rm -rf") {
		t.Errorf("the screen offers to delete the owner's home folder:\n%s", s)
	}
	const why = "The folder itself is not removed: it is your home folder, not a folder of Orbit's own."
	if !strings.Contains(flat, why) {
		t.Errorf("the screen lacks %q:\n%s", why, s)
	}
}
