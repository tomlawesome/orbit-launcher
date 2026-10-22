package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
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
// error names the limit that applied. It refuses a directory the
// launcher could not have written (RequireTrustedPath) before Docker
// runs.
func StandDown(ctx context.Context, targetDir string) error {
	// Compose reads the project name and the compose file from
	// targetDir, so it runs only from a path nobody else could have
	// written (#191) — the same rule Repair and the configuration
	// import already apply. A refusal reaches the failed screen as
	// its own words.
	if err := RequireTrustedPath(targetDir, EnvFile); err != nil {
		return err
	}
	limit := limitApplied(ctx, standDownTimeout)
	ctx, cancel := context.WithTimeout(ctx, standDownTimeout)
	defer cancel()
	cmd := standDownCommand(ctx, targetDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// The containers stopped so far stay stopped, so a second
			// stand-down picks up where this one was cut off.
			return &StandDownTimeoutError{Limit: limit, Words: StandDownCommandWords(targetDir), Err: err, Output: string(out)}
		}
		return fmt.Errorf("docker compose down: %w: %s", err, out)
	}
	return nil
}

// standDownCommand builds the exact command StandDown runs — separated
// out so its arguments (not just its behaviour once actually executed
// against a real deployment) are directly, cheaply testable.
func standDownCommand(ctx context.Context, targetDir string) *exec.Cmd {
	return dockerCommand(ctx, standDownArgs(targetDir)...)
}

// standDownArgs is the command's arguments after "docker", the one list
// both standDownCommand and StandDownCommandWords are built from.
func standDownArgs(targetDir string) []string {
	return []string{"compose",
		"--project-directory", targetDir,
		"--env-file", filepath.Join(targetDir, EnvFile), "down"}
}

// StandDownCommandWords returns the command StandDown runs as shell
// words, each already quoted, so a person can run it by hand when the
// stand-down timed out.
func StandDownCommandWords(targetDir string) []string {
	args := standDownArgs(targetDir)
	words := make([]string, 0, len(args)+1)
	words = append(words, "docker")
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return words
}

// StandDownTimeoutError is StandDown's answer when it was cut off at its
// limit. Words is the command to run by hand; Err and Output are what
// Docker and the cut-off left, kept for logs.
type StandDownTimeoutError struct {
	Limit  time.Duration
	Words  []string
	Err    error
	Output string
}

func (e *StandDownTimeoutError) Error() string {
	return fmt.Sprintf("docker compose down did not finish within %s: %v: %s", e.Limit, e.Err, e.Output)
}

func (e *StandDownTimeoutError) Unwrap() error { return e.Err }

// ErrNoTargetDir is returned for a stop or removal asked of no
// directory: a removal command only ever names a deployment the
// launcher actually found (#205).
var ErrNoTargetDir = errors.New("no deployment directory given")

// ErrRootFolder and ErrHomeFolder refuse a removal command for a folder
// that is plainly not Orbit's own: "sudo rm -rf" of it would delete far
// more than the deployment.
var (
	ErrRootFolder = errors.New("the deployment folder is the root folder")
	ErrHomeFolder = errors.New("the deployment folder is the home folder")
)

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
	words, err := VolumeRemovalCommandWords(targetDir)
	if err != nil {
		return nil, err
	}
	if err := refuseSharedFolder(targetDir); err != nil {
		return nil, err
	}
	return append(words, "&&", "sudo", "rm", "-rf", shellQuote(targetDir)), nil
}

// VolumeRemovalCommandWords is the data-volume half of
// RemovalCommandWords alone, quoted the same way.
//
// Compose removes only the volumes its own project declares, so this half
// is safe to offer for any folder, the root and home included; without it
// a leftover database volume refuses every later install.
func VolumeRemovalCommandWords(targetDir string) ([]string, error) {
	if targetDir == "" {
		return nil, ErrNoTargetDir
	}
	return []string{
		"docker", "compose", "--project-directory", shellQuote(targetDir),
		"--env-file", shellQuote(filepath.Join(targetDir, EnvFile)), "down", "-v",
	}, nil
}

// refuseSharedFolder returns ErrRootFolder or ErrHomeFolder when
// targetDir is the root folder, or the home folder of the launcher's
// session or of the folder's own account, whose "rm -rf" would delete
// far more than Orbit. Compared after cleaning, so "//", "/." or
// "home/../home" are caught too; the command still names targetDir as
// found.
func refuseSharedFolder(targetDir string) error {
	c := canonical(targetDir)
	if c == "/" {
		return ErrRootFolder
	}
	if home, err := os.UserHomeDir(); err == nil && c == canonical(home) {
		return ErrHomeFolder
	}
	// Under sudo or a root login $HOME is root's (Debian's sudo sets
	// HOME=/root), so a deployment installed straight into a service
	// account's home would pass the check above. The folder's own
	// account answers whose home it is: its owner's passwd entry, the
	// lookup ForeignOwned already makes. A folder that does not exist,
	// or an owner the account database cannot name, adds no refusal.
	if info, err := os.Stat(targetDir); err == nil {
		if u, err := user.LookupId(strconv.Itoa(ownerOf(info))); err == nil && u.HomeDir != "" && c == canonical(u.HomeDir) {
			return ErrHomeFolder
		}
	}
	return nil
}

// canonical is p made absolute and cleaned, then followed through links
// where the path exists: a home reached by another name is still the
// home. A path that does not exist is compared as spelled.
func canonical(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
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
