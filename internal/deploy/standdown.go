package deploy

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// standDownTimeout bounds StandDown. `docker compose down` stops each
// container with its own grace period, so a healthy stand-down takes
// seconds to a minute or two; five minutes means Docker is stuck, and
// past it the person is better served by a failed screen that says so
// than by a spinner that never ends (#207). A var only so its test can
// shorten it.
var standDownTimeout = 5 * time.Minute

// StandDown stops a deployment's containers and network — the safe,
// reversible half of Remove. It deliberately never passes -v: data
// volumes are left untouched, matching the "your files and data volumes
// are still on disk" claim in design/mockups.html section 11.
//
// --env-file is required, not optional: every variable the compose file
// interpolates (ORBIT_IMAGE, COMPOSE_PROJECT_NAME, ...) lives in
// .env-orbit, a non-standard filename Compose never auto-loads on its
// own — install.sh's own compose() helper always passes it explicitly
// for exactly this reason. Without it, "docker compose down" fails
// outright trying to interpolate ${ORBIT_IMAGE}, discovered via a real
// live deployment (issue #54) that unit tests mocking StandDown could
// never have caught.
//
// It gives up after standDownTimeout, or sooner if ctx ends, and the
// error names the limit that applied.
func StandDown(ctx context.Context, targetDir string) error {
	limit := limitApplied(ctx, standDownTimeout)
	ctx, cancel := context.WithTimeout(ctx, standDownTimeout)
	defer cancel()
	cmd := standDownCommand(ctx, targetDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("docker compose down did not finish within %s: %w: %s", limit, err, out)
		}
		return fmt.Errorf("docker compose down: %w: %s", err, out)
	}
	return nil
}

// standDownCommand builds the exact command StandDown runs — separated
// out so its arguments (not just its behaviour once actually executed
// against a real deployment) are directly, cheaply testable.
func standDownCommand(ctx context.Context, targetDir string) *exec.Cmd {
	envFile := filepath.Join(targetDir, ".env-orbit")
	cmd := exec.CommandContext(ctx, "docker", "compose",
		"--project-directory", targetDir, "--env-file", envFile, "down")
	cmd.WaitDelay = pipeWaitDelay
	return cmd
}

// ErrNoTargetDir is returned for a stop or removal asked of no
// directory: a removal command only ever names a deployment the
// launcher actually found (#205).
var ErrNoTargetDir = errors.New("no deployment directory given")

// RemovalCommand returns the exact, copy-pasteable shell command that
// fully and irreversibly removes an Orbit deployment — including its data
// volumes and every file in targetDir. This package never executes it:
// see removal_property_test.go, which asserts that as a real, checked
// property, not just a comment someone could quietly invalidate later.
// It is RemovalCommandWords joined by single spaces, so a screen built
// from the words and a clipboard built from the line cannot disagree.
func RemovalCommand(targetDir string) (string, error) {
	words, err := RemovalCommandWords(targetDir)
	if err != nil {
		return "", err
	}
	return strings.Join(words, " "), nil
}

// RemovalCommandWords returns RemovalCommand as shell words, each
// already quoted, so a caller can lay the command out on several lines
// without parsing it back.
//
// It passes --env-file for the same reason StandDown does: Compose never
// auto-loads .env-orbit, so without it "down -v" cannot resolve the
// compose file's variables. Paths are quoted because the line is pasted
// into a shell, so a directory name with a space or quote must still
// arrive as one argument.
func RemovalCommandWords(targetDir string) ([]string, error) {
	if targetDir == "" {
		return nil, ErrNoTargetDir
	}
	dir := shellQuote(targetDir)
	envFile := shellQuote(filepath.Join(targetDir, ".env-orbit"))
	return []string{
		"docker", "compose", "--project-directory", dir, "--env-file", envFile, "down", "-v",
		"&&", "sudo", "rm", "-rf", dir,
	}, nil
}

// shellSafe matches values that need no quoting in a POSIX shell.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

// shellQuote applies POSIX single-quote escaping (as Python's shlex.quote
// does), leaving the value bare when it only has shell-safe characters.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
