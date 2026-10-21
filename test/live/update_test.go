//go:build live

package live

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

// The update leg's first install comes from an earlier Orbit preview release
// than the one the job hands every other leg in ORBIT_RELEASE_MANIFEST (#198).
// The live job sets both, verified, only when it is given that release's job
// IDs; see the `live` job in .gitlab-ci.yml.
//
// Why an earlier preview, and not the alternatives:
//   - The stable channel, which install.sh fetches on its own, cannot be it.
//     No stable release on github.com/tomlawesome/orbit carries the release
//     manifest asset (v1.1.0 and v1.2.0 have no assets at all, checked
//     2026-10-06), so install.sh's self-fetch refuses before installing
//     anything; and v1.2.0's image has no configure engine, so it is not the
//     generation the preview install.sh drives.
//   - Rebuilding the preview image with a changed label, as Orbit's own
//     compat job plans to, needs a test signing key and a registry for the
//     copy; install.sh only installs a digest it can pull from Orbit's
//     registry, named by a manifest signed with Orbit's key.
//   - An earlier preview release is a real update between two signed
//     releases. Its manifest names the source commit and the sha256 of the
//     install.sh it ships with, so the job fetches that install.sh and checks
//     it against the signed manifest: script and image stay one generation.
const (
	// firstManifestEnv is the earlier release's manifest, verified by the job
	// the same way as the current one.
	firstManifestEnv = "ORBIT_LIVE_FIRST_RELEASE_MANIFEST"
	// firstScriptEnv is that release's install.sh, already checked against
	// the manifest's files["install.sh"]. It reaches the launcher through
	// ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH, the way get-orbit.sh hands over a
	// verified copy.
	firstScriptEnv = "ORBIT_LIVE_FIRST_INSTALL_SCRIPT"
)

// TestLive_UpdateReconfiguresOnTheNewImage is #197's update case: when an
// update has to reconfigure, configure runs the image being installed, not
// the image the deployment already has.
//
// An update with a complete configuration never refuses, so the launcher
// would never run configure.sh in the tree install.sh hands over, and the
// leg would prove nothing (ai/orbit#1265). So before the update the leg
// empties the deployment's OIDC client secret file. install.sh's
// prepare_configuration then finds `missing OIDC_CLIENT_SECRET` in its
// readiness check with no terminal to ask on, refuses ("Required
// configuration fields require attention") in its configuration phase,
// which default_failure_reason reports as configuration-failure, and fail()
// hands the configure tree over on exactly that reason. The launcher seeds
// the tree's .env-orbit from the deployment, which still names the old
// image, and runs configure.sh --check and --set-oidc-secret there. Deleting
// the file instead would refuse too, but earlier, through the engine's guard
// against losing a configured secret rather than through the readiness
// check this leg means to exercise.
//
// The evidence is `docker events`: every container created during the
// update, recorded as it happens. None may come from the old image, and at
// least one one-off must come from the new image in the stretch where only
// the launcher runs anything: from leaving the configuration screen to the
// secret prompt. Against an Orbit without the .orbit-image pin, configure.sh
// in the tree runs the image the seeded .env-orbit names, and this fails.
func TestLive_UpdateReconfiguresOnTheNewImage(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	sweepLeftoverLiveStacks(t)
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	currentManifest := os.Getenv("ORBIT_RELEASE_MANIFEST")
	firstManifest := os.Getenv(firstManifestEnv)
	firstScript := os.Getenv(firstScriptEnv)
	if currentManifest == "" || firstManifest == "" || firstScript == "" {
		t.Skipf("no earlier Orbit release to update from: %s, %s and ORBIT_RELEASE_MANIFEST must all be set. "+
			"In CI, start the pipeline with ORBIT_FIRST_RECORD_IMAGE_JOB and ORBIT_FIRST_SIGN_EVIDENCE_JOB naming an earlier "+
			"ai/orbit preview pipeline's record_image and sign_evidence jobs (#198)", firstManifestEnv, firstScriptEnv)
	}
	newDigest := readManifestDigest(t, currentManifest)
	oldDigest := readManifestDigest(t, firstManifest)
	if oldDigest == newDigest {
		t.Fatalf("the earlier release names the same image as the current one (%s); an update between them proves nothing", newDigest)
	}

	binPath := binaryPath(t)
	dir := t.TempDir()
	// Isolated by its own project name, as every leg is (liveProjectName).
	t.Cleanup(func() { dockerComposeDown(t, dir) })

	appURL := fmt.Sprintf("https://orbit-live-update-%d.internal", time.Now().UnixNano())
	const clientID, secret = "orbit-launcher-live-update-test", "ci-live-update-fake-secret"

	var oldRef string
	installed := t.Run("InstallEarlier", func(t *testing.T) {
		session := startLiveEnv(t, binPath, dir,
			"ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH="+firstScript,
			"ORBIT_RELEASE_MANIFEST="+firstManifest)
		installThroughConsole(t, session, appURL, clientID, secret, "Get into Orbit")
		waitHealthy(t, session)
		quitFromSuccess(session)

		oldRef = deployedImage(t, dir)
		if got := digestOf(oldRef); got != oldDigest {
			t.Fatalf("the first install recorded ORBIT_IMAGE=%q, want the earlier release's %s", oldRef, oldDigest)
		}
	})

	t.Run("Update", func(t *testing.T) {
		if !installed {
			t.Skip("the earlier install failed; nothing to update")
		}
		emptyOIDCSecret(t, dir)

		old := imageIdentity{Digest: oldDigest, ID: localImageID(t, oldRef)}
		updated := imageIdentity{Digest: newDigest}
		recorder := recordContainerCreates(t)

		session := startLive(t, binPath, dir)
		fqdn := strings.TrimPrefix(appURL, "https://")
		// Chosen by digit, for the caret race removeAndAssertCleanup explains.
		session.mustSoonScreen("Install", fqdn)
		session.choose("Update")
		session.mustSoon("Pull the latest Orbit and update this deployment")
		session.send("\r") // Update Orbit, selected by default
		if _, err := session.expectWithinBudget("expected the mission console", session.screenBudget, vtscreen.ContainsAny(
			"Contacting the engine", "ORBIT · Update", "How will people sign in?", "OIDC client secret", "Update stopped")); err != nil {
			t.Fatalf("expected the mission console: %v", err)
		}

		// The engine's configuration refusal goes straight into guided
		// configuration (#251): no screen to dismiss, no key pressed. From
		// here until the secret prompt only the launcher runs anything:
		// install.sh's first attempt has exited, and the second starts only
		// once the secret is in. The guided session's title shows as soon
		// as the refusal lands, so the window opens after that attempt.
		session.expectWithin("expected the guided configuration after the refusal",
			vtscreen.ContainsAny("Orbit needs your configuration", "Update stopped", "Get into Orbit"))
		from := time.Now()
		screen := session.expectWithin("expected the in-console OIDC client secret prompt",
			vtscreen.ContainsAny("OIDC client secret", "Update stopped", "How will people sign in?", "Get into Orbit"))
		to := time.Now()
		if strings.Contains(screen, "The engine stopped before touching anything") || strings.Contains(screen, "Continue — guided configuration") {
			t.Fatalf("the removed configuration screen is shown. Screen:\n%s", screen)
		}
		if strings.Contains(screen, "Get into Orbit") {
			t.Fatalf("the update did not stop for configuration, so the launcher never ran configure and this leg proves nothing; "+
				"the emptied OIDC client secret should have made install.sh refuse. Screen:\n%s", screen)
		}
		if !strings.Contains(screen, "OIDC client secret") {
			// Without the pin this is where an old image lacking the
			// engine ends up: the evidence says which image ran.
			_, verdict := judgeConfigureImage(recorder.stop(t), from, to, old, updated)
			t.Fatalf("the launcher did not ask for the OIDC client secret (the only field the update should need); evidence: %v. Screen:\n%s", verdict, screen)
		}
		session.send(secret + "\r")
		acceptMenusUntil(t, session, "Get into Orbit")

		summary, err := judgeConfigureImage(recorder.stop(t), from, to, old, updated)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(summary)

		if got := digestOf(deployedImage(t, dir)); got != newDigest {
			t.Errorf("after the update the deployment records image digest %q, want the current release's %s", got, newDigest)
		}
		waitHealthy(t, session)
		quitFromSuccess(session)
	})

	t.Run("Remove", func(t *testing.T) {
		if !installed {
			t.Skip("the earlier install failed; nothing to remove")
		}
		removeAndAssertCleanup(t, binPath, dir, appURL)
	})
}

func readManifestDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read release manifest: %v", err)
	}
	digest, err := manifestImageDigest(data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return digest
}

// deployedImage is the ORBIT_IMAGE the deployment in dir records: the image
// install.sh installed, and what the launcher seeds into the configure tree
// on an update.
func deployedImage(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".env-orbit"))
	if err != nil {
		t.Fatalf("read the deployment's .env-orbit: %v", err)
	}
	return envFileValue(data, "ORBIT_IMAGE")
}

// localImageID is ref's local image ID, or "" if the daemon cannot say; the
// digest alone still identifies the image in events that name the
// reference.
func localImageID(t *testing.T, ref string) string {
	t.Helper()
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", ref).Output()
	if err != nil {
		t.Logf("docker image inspect %s: %v; matching the old image by digest only", ref, err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// emptyOIDCSecret empties the deployment's OIDC client secret file, keeping
// the file and its mode, so the update's readiness check reports
// `missing OIDC_CLIENT_SECRET` (an empty canonical secret file is missing to
// Orbit's engine, ai/orbit src/cli/orbit.check.test.ts).
func emptyOIDCSecret(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".env-orbit"))
	if err != nil {
		t.Fatalf("read the deployment's .env-orbit: %v", err)
	}
	if envFileValue(data, "OIDC_CLIENT_SECRET_FILE") == "" {
		t.Fatal("the first install keeps no OIDC_CLIENT_SECRET_FILE, so emptying the secret file would not make the update refuse")
	}
	path := filepath.Join(dir, ".orbit-secrets", "oidc-client-secret")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("the first install left no OIDC client secret file: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file (%s)", path, info.Mode())
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatalf("empty the OIDC client secret: %v", err)
	}
}

// createRecorder streams the daemon's container-create events into a file
// for the length of the update. A stream, not a replay with --until
// afterwards: the daemon keeps only its most recent events, and health
// checks on a running Orbit fill that buffer within minutes.
type createRecorder struct {
	cmd     *exec.Cmd
	out     *os.File
	stderr  bytes.Buffer
	path    string
	stopped bool
	creates []containerCreate
}

func recordContainerCreates(t *testing.T) *createRecorder {
	t.Helper()
	// Beside the raw log when there is one, so CI's artifact keeps the
	// evidence (its glob is live-raw.log.*).
	path := filepath.Join(t.TempDir(), "docker-events.jsonl")
	if raw := os.Getenv("ORBIT_LAUNCHER_LIVE_RAW_LOG"); raw != "" {
		path = raw + ".docker-events." + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatalf("create docker events log: %v", err)
	}
	// --since covers the moment between starting the stream and the
	// daemon subscribing it.
	since := time.Now()
	r := &createRecorder{out: out, path: path}
	r.cmd = exec.Command("docker", "events",
		"--since", fmt.Sprintf("%d.%09d", since.Unix(), since.Nanosecond()),
		"--filter", "type=container", "--filter", "event=create",
		"--format", "{{json .}}")
	r.cmd.Stdout = out
	r.cmd.Stderr = &r.stderr
	if err := r.cmd.Start(); err != nil {
		out.Close()
		t.Fatalf("start docker events: %v", err)
	}
	t.Cleanup(func() {
		if !r.stopped {
			_ = r.cmd.Process.Kill()
			_ = r.cmd.Wait()
			_ = r.out.Close()
		}
	})
	return r
}

// stop ends the recording and returns its container creates. Every create
// this leg judges happened long before the success screen, so a short wait
// for the stream to catch up is enough.
func (r *createRecorder) stop(t *testing.T) []containerCreate {
	t.Helper()
	if r.stopped {
		return r.creates
	}
	time.Sleep(2 * time.Second)
	r.stopped = true
	_ = r.cmd.Process.Kill()
	_ = r.cmd.Wait()
	if err := r.out.Close(); err != nil {
		t.Fatalf("close docker events log: %v", err)
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatalf("read docker events log: %v", err)
	}
	creates, err := parseContainerCreates(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, strings.TrimSpace(r.stderr.String()))
	}
	t.Logf("docker events: %d container creates recorded during the update, kept in %s", len(creates), r.path)
	r.creates = creates
	return creates
}
