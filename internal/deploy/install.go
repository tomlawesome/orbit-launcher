package deploy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// Install fetches the current install.sh and runs it non-interactively
// against targetDir, streaming each line of its combined output to
// onLine as it happens (for a live progress screen). Config must already
// be written via WriteConfig before calling this — Install does not
// collect or stage configuration itself.
func Install(ctx context.Context, targetDir string, onLine func(line string)) error {
	script, err := FetchInstallScript(ctx)
	if err != nil {
		return err
	}
	return RunInstallScript(ctx, script, targetDir, onLine)
}

// RunInstallScript executes script (install.sh's content) against
// targetDir. Separated from Install so the process-execution and
// output-streaming behaviour is testable against a fake script, without
// needing real network access or Docker.
//
// The child is detached into its own session (Setsid): install.sh's
// has_controlling_terminal check opens /dev/tty directly, so merely
// redirecting stdin (which this also does, to /dev/null) would not be
// enough — orbit-launcher's own process has a real controlling terminal,
// and without Setsid the child would inherit it, making install.sh think
// it can fall back to its own interactive prompts. A new session has no
// controlling terminal at all, so install.sh reliably takes the
// non-interactive path and runs strictly against the .env-orbit
// WriteConfig already staged.
func RunInstallScript(ctx context.Context, script []byte, targetDir string, onLine func(line string)) error {
	scriptFile, err := os.CreateTemp("", "orbit-launcher-install-*.sh")
	if err != nil {
		return fmt.Errorf("stage install.sh: %w", err)
	}
	defer os.Remove(scriptFile.Name())
	if _, err := scriptFile.Write(script); err != nil {
		scriptFile.Close()
		return fmt.Errorf("stage install.sh: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		return fmt.Errorf("stage install.sh: %w", err)
	}

	cmd := exec.CommandContext(ctx, "bash", scriptFile.Name())
	cmd.Dir = targetDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("attach stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("attach stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start install.sh: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go streamLines(stdout, onLine, &wg)
	go streamLines(stderr, onLine, &wg)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("install.sh: %w", err)
	}
	return nil
}

func streamLines(r io.Reader, onLine func(string), wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if onLine != nil {
			onLine(scanner.Text())
		}
	}
}
