//go:build live

package live

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

// TestLive_UpdateExistingDeployment drives one real Update of a deployment
// somebody else installed (ai/orbit#1666's upgrade cell) and leaves it
// running. It asserts only what the launcher owns: the Update goes through
// the console to "Get into Orbit", the record names the candidate digest,
// and the app reports the candidate version and is ready. Orbit seeds and
// verifies its own data. Opt-in: unset ORBIT_LIVE_EXISTING_DIR skips; set,
// every missing input is a failure (#331).
func TestLive_UpdateExistingDeployment(t *testing.T) {
	if os.Getenv("ORBIT_LIVE_EXISTING_DIR") == "" {
		t.Skip("ORBIT_LIVE_EXISTING_DIR is not set; this test updates a deployment installed elsewhere (#331)")
	}
	for _, tool := range []string{"docker", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not available: ORBIT_LIVE_EXISTING_DIR is set, so this test may not skip (#331)", tool)
		}
	}
	in, err := existingDeploymentInputs(os.Getenv, os.Stat, os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(in.Binary); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("ORBIT_LAUNCHER_LIVE_BINARY=%q is not an executable file (stat: %v)", in.Binary, err)
	}

	// The seed must be healthy first, so a broken seed fails as Orbit's.
	resp, err := healthClient.Get("http://localhost:3000/api/health")
	if err != nil {
		t.Fatalf("before the update, /api/health is unreachable: the seeded deployment is not up: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	status, statusErr := healthStatus(body)
	if readErr != nil || statusErr != nil || status != "ready" {
		t.Fatalf("before the update, /api/health reports %q, want ready (read: %v, parse: %v)", status, readErr, statusErr)
	}

	// extraEnv wins over the harness's own project (live_test.go startLiveEnv).
	session := startLiveEnv(t, in.Binary, in.Dir, "COMPOSE_PROJECT_NAME="+in.Project)
	fqdn := strings.TrimPrefix(strings.TrimPrefix(in.AppURL, "https://"), "http://")
	session.mustSoonScreen("Install", fqdn)
	session.choose("Update")
	session.mustSoon("Pull the latest Orbit and update this deployment")
	session.send("\r") // Update Orbit, selected by default
	if _, err := session.expectWithinBudget("expected the mission console", session.screenBudget, vtscreen.ContainsAny(
		"Contacting the engine", "ORBIT · Update", "How will people sign in?", "OIDC client secret", "Update stopped")); err != nil {
		t.Fatalf("expected the mission console: %v", err)
	}
	acceptMenusUntil(t, session, "Get into Orbit")

	if got := digestOf(deployedImage(t, in.Dir)); got != in.NewDigest {
		t.Errorf("after the update the deployment records image digest %q, want the candidate's %s", got, in.NewDigest)
	}
	waitHealthy(t, session)
	checkUpdatedApp(t, in.Dir, in.Project, in.NewVersion)
	quitFromSuccess(session)
	// No cleanup: Orbit checks and tears down its own deployment.
}
