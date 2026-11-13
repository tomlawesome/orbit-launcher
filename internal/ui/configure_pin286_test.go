package ui

// #286: Configure's run is pinned to the release its confirm line names,
// as Update's is (UpdateModel.startRun): ORBIT_CHANNEL in the engine's
// environment when the lookup gave a version and Pin, otherwise unset.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/release"
)

func TestConfigure286Pin_RunIsPinnedToTheNamedRelease(t *testing.T) {
	cases := []struct {
		name   string
		target release.Target
		err    error
		want   string
	}{
		{"version and pin", release.Target{Version: "v0.3.6", Pin: true}, nil, "v0.3.6"},
		{"version without pin", release.Target{Version: "v0.3.6", Pin: false}, nil, "<unset>"},
		{"lookup failed", release.Target{}, errors.New("manifest unreachable"), "<unset>"},
	}
	for _, c := range cases {
		for _, d := range cfg286Directions {
			t.Run(c.name+" to "+d.to, func(t *testing.T) {
				out := t.TempDir()
				t.Setenv("ORBIT_T286_OUT", out)
				t.Setenv(deploy.ChannelEnv, "")
				if err := os.Unsetenv(deploy.ChannelEnv); err != nil {
					t.Fatal(err)
				}
				installScriptAt(t, "printf '%s\\n' \"${"+deploy.ChannelEnv+"-<unset>}\" >> \"$ORBIT_T286_OUT/channel\"\n"+
					"echo 'phase=host component=host state=failed reason=docker-host action=retry elapsed=0s'\n"+
					"exit 1\n")
				m, queue := cfg286App(t, cfg286Folder(t, shortTempDir(t), d.profiles, true))
				m = m.WithTargetLookup(func(context.Context) (release.Target, error) { return c.target, c.err })
				m = cfg286ToConfirm(t, m, d.to)

				cfg286Start(t, m, queue, d.to)

				got := docs260Lines(t, out, "channel")
				if len(got) != 1 || got[0] != c.want {
					t.Errorf("the engine saw %s = %q, want one run with %q", deploy.ChannelEnv, strings.Join(got, ","), c.want)
				}
			})
		}
	}
}
