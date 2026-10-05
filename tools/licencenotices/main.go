// Command licencenotices writes, or checks, the third-party notices the
// launcher embeds and prints with --licences (#182).
//
//	go run ./tools/licencenotices          # regenerate after a dependency change
//	go run ./tools/licencenotices -check   # fail if the committed file is stale, as CI runs it
//
// See package notices for what goes in.
package main

import (
	"flag"
	"fmt"
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
	check := flag.Bool("check", false, "fail if "+outPath+" is not what this would write")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "licencenotices:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	pkgs, err := review.Shipped()
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
	fmt.Printf("%s is current\n", outPath)
	return nil
}
