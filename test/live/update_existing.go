package live

import (
	"github.com/tomlawesome/orbit-launcher/internal/deploy"

	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// existingInputs is what TestLive_UpdateExistingDeployment needs to update a
// deployment somebody else installed (#331, ai/orbit#1666). Digests are
// sha256:<hex>, the part of a reference after the @.
type existingInputs struct {
	Dir, Project, AppURL, OldDigest, NewDigest, NewVersion, Binary, InstallScriptPath, InstallScriptURL string
}

// existingDeploymentInputs applies the rules for that test. With
// ORBIT_LIVE_EXISTING_DIR set every other missing or invalid input is an
// error naming it, never a skip (#307's rule). get, stat and read are the
// environment and filesystem, so the rules run without either.
func existingDeploymentInputs(
	get func(string) string,
	stat func(string) (os.FileInfo, error),
	read func(string) ([]byte, error),
) (existingInputs, error) {
	var in existingInputs
	in.Dir = get("ORBIT_LIVE_EXISTING_DIR")
	if !filepath.IsAbs(in.Dir) {
		return in, fmt.Errorf("ORBIT_LIVE_EXISTING_DIR=%q must be the absolute path of the deployment folder", in.Dir)
	}
	envPath := filepath.Join(in.Dir, deploy.EnvFile)
	envData, err := read(envPath)
	if err != nil {
		return in, fmt.Errorf("ORBIT_LIVE_EXISTING_DIR: cannot read %s: %w", deploy.EnvFile, err)
	}
	image := envFileValue(envData, "ORBIT_IMAGE")
	if in.OldDigest = digestOf(image); in.OldDigest == "" {
		return in, fmt.Errorf("ORBIT_LIVE_EXISTING_DIR: ORBIT_IMAGE in %s is %q, want <repository>@sha256:<64 hex>", deploy.EnvFile, image)
	}
	if in.AppURL = envFileValue(envData, "APP_URL"); in.AppURL == "" {
		return in, fmt.Errorf("ORBIT_LIVE_EXISTING_DIR: %s records no APP_URL", deploy.EnvFile)
	}
	if in.Project = envFileValue(envData, "COMPOSE_PROJECT_NAME"); in.Project == "" {
		in.Project = "orbit" // the compose file's own name
	}
	if _, err := stat(filepath.Join(in.Dir, deploy.ComposeFile)); err != nil {
		return in, fmt.Errorf("ORBIT_LIVE_EXISTING_DIR: no docker-compose.yml: %w", err)
	}

	manifest := get("ORBIT_RELEASE_MANIFEST")
	if manifest == "" {
		return in, fmt.Errorf("ORBIT_RELEASE_MANIFEST is not set: it names the candidate release")
	}
	data, err := read(manifest)
	if err != nil {
		return in, fmt.Errorf("ORBIT_RELEASE_MANIFEST: %w", err)
	}
	if in.NewDigest, err = manifestImageDigest(data); err != nil {
		return in, fmt.Errorf("ORBIT_RELEASE_MANIFEST %s: %w", manifest, err)
	}
	if in.NewVersion, err = manifestVersion(data); err != nil {
		return in, fmt.Errorf("ORBIT_RELEASE_MANIFEST %s: %w", manifest, err)
	}
	if in.NewDigest == in.OldDigest {
		return in, fmt.Errorf("ORBIT_RELEASE_MANIFEST names the image digest the deployment already records (%s); an update proves nothing", in.NewDigest)
	}

	if in.Binary = get("ORBIT_LAUNCHER_LIVE_BINARY"); in.Binary == "" {
		return in, fmt.Errorf("ORBIT_LAUNCHER_LIVE_BINARY is not set: this test must run the launcher from the extracted archive, not one built from source")
	}
	in.InstallScriptPath = get("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH")
	in.InstallScriptURL = strings.TrimSpace(get("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL"))
	if in.InstallScriptPath == "" && in.InstallScriptURL == "" {
		return in, fmt.Errorf("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH (or ORBIT_LAUNCHER_INSTALL_SCRIPT_URL) is not set: without one the launcher fetches Orbit's main")
	}
	return in, nil
}
