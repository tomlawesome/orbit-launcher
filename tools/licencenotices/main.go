// Command licencenotices writes, or checks, the third-party notices the
// launcher embeds and prints with --licences (#182).
//
//	go run ./tools/licencenotices          # regenerate after a dependency change
//	go run ./tools/licencenotices -check   # fail if the committed file is stale, as CI runs it
//
// See package notices for what goes in.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tomlawesome/orbit-launcher/tools/licencenotices/notices"
	"github.com/tomlawesome/orbit-launcher/tools/licencereview/review"
)

const (
	outPath   = "internal/notices/THIRD_PARTY_NOTICES.txt"
	extrasDir = "internal/notices/extra"
)

func main() {
	os.Exit(cli(os.Args, os.Stdout, os.Stderr, review.Shipped))
}

// cli is the whole command bar os.Exit, so its tests need no subprocess.
// What ships is a parameter, review.Shipped from main, so a test can hand
// it fixed packages rather than run go list over this module, whose
// notices change with every dependency bump. Flags behave as the default
// flag set's would: usage on stderr, exit 2 on a bad flag, 0 on -h.
func cli(args []string, stdout, stderr io.Writer, shipped func() ([]review.Package, error)) int {
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "fail if "+outPath+" is not what this would write")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := run(*check, stdout, shipped); err != nil {
		fmt.Fprintln(stderr, "licencenotices:", err)
		return 1
	}
	return 0
}

func run(check bool, stdout io.Writer, shipped func() ([]review.Package, error)) error {
	pkgs, err := shipped()
	if err != nil {
		return err
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("go env GOROOT: %w", err)
	}
	goLicence, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE"))
	if err != nil {
		return err
	}
	names, err := filepath.Glob(filepath.Join(extrasDir, "*.txt"))
	if err != nil {
		return err
	}
	var extras []notices.Extra
	for _, name := range names {
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		e, err := notices.ParseExtra(name, string(content))
		if err != nil {
			return err
		}
		extras = append(extras, e)
	}
	text, err := notices.Build(pkgs, string(goLicence), extras)
	if err != nil {
		return err
	}

	if !check {
		return os.WriteFile(outPath, []byte(text), 0o644)
	}
	committed, err := os.ReadFile(outPath)
	if err != nil {
		return err
	}
	if string(committed) != text {
		return fmt.Errorf("%s is stale: a linked module, its version or a notice changed; regenerate it with `go run ./tools/licencenotices` and commit the result", outPath)
	}
	fmt.Fprintf(stdout, "%s is current\n", outPath)
	return nil
}
