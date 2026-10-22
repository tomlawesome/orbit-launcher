package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #277 (v0.4.3 Note): Repair applies the folder-owner rule before it reads
// the deployment, so an unreadable settings file in another account's
// folder gets the owner refusal, not "permission denied". The launcher's
// own unreadable file still reads as a read error
// (TestRepairCommand_UnreadableEnvFileIsNeitherNoDeploymentNorUnavailable),
// and a readable one in another account's folder is refused
// (TestOwnerRuleGuardsEveryEntryPoint).
func TestRepairCommand_AnotherAccountsFolderIsRefusedBeforeItsSettingsAreRead(t *testing.T) {
	dir := trustedDeployment(t)
	env := filepath.Join(dir, EnvFile)
	chmod(t, env, 0o000)
	t.Cleanup(func() { chmod(t, env, 0o600) })
	asLauncherUser(t, os.Geteuid()+1)

	_, err := RepairCommand(dir, RepairPlan)
	var untrusted *UntrustedPathError
	if !errors.As(err, &untrusted) || !strings.Contains(err.Error(), "not by the user running the launcher") {
		t.Fatalf("err = %v, want the owner refusal", err)
	}
	if strings.Contains(err.Error(), "permission denied") {
		t.Errorf("err = %q: the settings file was read before the owner rule was applied", err)
	}
}
