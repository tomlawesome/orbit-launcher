package ui

import (
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// 236-c9 follow-up: the confirm screen promises that the next screen
// shows how to remove the files. For the root or home folder that
// screen offers no command, so the promise is left out there, and kept
// for a folder of Orbit's own.
func TestRemove_ConfirmScreenPromisesARemovalOnlyWhenOneIsOffered(t *testing.T) {
	const home = "/home/someone"
	t.Setenv("HOME", home)
	const promise = "how to remove them"
	for _, target := range []string{"/", home} {
		s := screen(lowsRemoveModel(&deploy.Deployment{TargetDir: target}, standDownOK))
		if strings.Contains(s, promise) || strings.Contains(s, "next screen") {
			t.Errorf("target %q: the confirm screen promises a removal the next screen will not offer:\n%s", target, s)
		}
		if !strings.Contains(s, "not deleted") {
			t.Errorf("target %q: the confirm screen no longer says the files are not deleted:\n%s", target, s)
		}
	}
	if s := screen(lowsRemoveModel(&deploy.Deployment{TargetDir: "/opt/orbit"}, standDownOK)); !strings.Contains(s, promise) {
		t.Errorf("a folder of Orbit's own lost the promise:\n%s", s)
	}
}
