package main

// v0.4.3-c audit Low M (issue #277): --licences (and --licenses) only
// print the notices text, which reads and runs nothing, so they answer as
// root too, as --version does. Any other argument as root is still
// refused. The effective user is injected through effectiveUID, as
// groupb_root_test.go does (groupBRunAs).

import (
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/notices"
)

// M (#277): as root, both spellings print the notices and return 0.
func TestLowsC_M_LicencesAnswerAsRoot(t *testing.T) {
	for _, arg := range []string{"--licences", "--licenses"} {
		t.Run(arg, func(t *testing.T) {
			code, stdout, stderr, started := groupBRunAs(t, 0, "<unset>", arg)
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			if started {
				t.Error("the app was started")
			}
			if stdout != notices.Text {
				t.Errorf("stdout is %d bytes, want the notices text (%d bytes)", len(stdout), len(notices.Text))
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
		})
	}
}

// M (#277): the exception is for these two alone; other arguments as
// root are still refused.
func TestLowsC_M_OtherArgumentsAsRootAreStillRefused(t *testing.T) {
	for _, arg := range []string{"--help", "something", "--licence", "--licenses=x"} {
		code, stdout, stderr, started := groupBRunAs(t, 0, "<unset>", arg)
		if code == 0 || started || stdout != "" || !strings.Contains(stderr, "doesn't run as root") {
			t.Errorf("%q as root: code=%d started=%v stdout=%q stderr=%q, want a refusal", arg, code, started, stdout, stderr)
		}
	}
}
