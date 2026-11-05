package deploy

import (
	"os/exec"
	"slices"
	"testing"
)

// #336. WithChannel is new (design note section 4).

func lastEntry(env []string, name string) (string, bool) {
	found := ""
	ok := false
	for _, e := range env {
		if len(e) > len(name) && e[:len(name)+1] == name+"=" {
			found, ok = e, true
		}
	}
	return found, ok
}

func TestWithChannel_AppendsAsTheLastChannelEntryOverAnInheritedOne(t *testing.T) {
	cmd := exec.Command("true")
	cmd.Env = []string{"PATH=/usr/bin", "ORBIT_CHANNEL=latest", "HOME=/home/x"}
	WithChannel(cmd, "v0.3.6")

	if got, _ := lastEntry(cmd.Env, "ORBIT_CHANNEL"); got != "ORBIT_CHANNEL=v0.3.6" {
		t.Errorf("last ORBIT_CHANNEL entry = %q, want ORBIT_CHANNEL=v0.3.6 (env %v)", got, cmd.Env)
	}
	want := []string{"PATH=/usr/bin", "ORBIT_CHANNEL=latest", "HOME=/home/x", "ORBIT_CHANNEL=v0.3.6"}
	if !slices.Equal(cmd.Env, want) {
		t.Errorf("env = %v, want %v: nothing else may change", cmd.Env, want)
	}
}

func TestWithChannel_EmptyLeavesTheEnvironmentUntouched(t *testing.T) {
	cmd := exec.Command("true")
	cmd.Env = []string{"PATH=/usr/bin", "ORBIT_CHANNEL=latest"}
	WithChannel(cmd, "")
	if want := []string{"PATH=/usr/bin", "ORBIT_CHANNEL=latest"}; !slices.Equal(cmd.Env, want) {
		t.Errorf("env = %v, want %v", cmd.Env, want)
	}

	bare := exec.Command("true")
	WithChannel(bare, "")
	if bare.Env != nil {
		t.Errorf("env = %v, want nil (inherit) left alone", bare.Env)
	}
}
